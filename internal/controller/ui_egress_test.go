/*
Copyright 2026 maeshinshin.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"encoding/json"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func uiDeployment(c ...corev1.Container) *appsv1.Deployment {
	return &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: c}}}}
}

func TestOIDCIssuerOf(t *testing.T) {
	envOnly := corev1.Container{Name: uiContainerName, Env: []corev1.EnvVar{{Name: envOIDCIssuer, Value: "https://env.example.test"}}}
	both := corev1.Container{
		Name: uiContainerName, Args: []string{"--addr=:8443", "--oidc-issuer=https://arg.example.test/realms/x"},
		Env: envOnly.Env,
	}

	for _, tc := range []struct {
		name string
		dep  *appsv1.Deployment
		want string
	}{
		{"no container", uiDeployment(), ""},
		{"no issuer", uiDeployment(corev1.Container{Name: uiContainerName, Args: []string{"--addr=:8443"}}), ""},
		{"env", uiDeployment(envOnly), "https://env.example.test"},
		{"the flag wins over the environment, as it does in ichor-ui", uiDeployment(both), "https://arg.example.test/realms/x"},
		{"another container is not ichor-ui", uiDeployment(corev1.Container{Name: "sidecar", Env: envOnly.Env}), ""},
		{"an env from a Secret has no value to read", uiDeployment(corev1.Container{Name: uiContainerName, Env: []corev1.EnvVar{{
			Name: envOIDCIssuer, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{}},
		}}}), ""},
	} {
		if got := oidcIssuerOf(tc.dep); got != tc.want {
			t.Errorf("%s: issuer = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestUIEgressSpecNamesTheIssuerOnItsPortAndSelectsOnlyIchorUI(t *testing.T) {
	for _, tc := range []struct {
		name, issuer, want, wantPort string
	}{
		{"a host name", "https://login.example.test/realms/x", `"toFQDNs":[{"matchName":"login.example.test"}]`, `"port":"443"`},
		{"a port in the URL", "https://login.example.test:8443", `"matchName":"login.example.test"`, `"port":"8443"`},
		{"an address", "https://192.0.2.9", `"toCIDRSet":[{"cidr":"192.0.2.9/32"}]`, `"port":"443"`},
		{"an in-cluster service", "http://dex.dex.svc:5556", `"k8sService":{"namespace":"dex","serviceName":"dex"}`, `"port":"5556"`},
	} {
		target, ok := parseURLTarget(tc.issuer)
		if !ok {
			t.Fatalf("%s: %q does not parse", tc.name, tc.issuer)
		}

		raw, _ := json.Marshal(uiEgressSpec(releaseCheckApp, target))
		text := string(raw)

		for _, want := range []string{tc.want, tc.wantPort, `"control-plane":"ui-backend"`, `"app.kubernetes.io/name":"` + releaseCheckApp + `"`} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: %s is missing from %s", tc.name, want, text)
			}
		}

		if strings.Contains(text, "controller-manager") || strings.Contains(text, "world") {
			t.Errorf("%s: the policy must select ichor-ui only and name no world entity: %s", tc.name, text)
		}
	}
}

const uiEgressNS = "ui-egress-test-ns"

var _ = Describe("UIEgress", Ordered, func() {
	var egress *UIEgress

	BeforeAll(func() {
		ensureCiliumPolicyCRD()
		Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metaOf(uiEgressNS)})).To(Or(Succeed(), MatchError(apierrors.IsAlreadyExists, "already exists")))

		Eventually(func() error {
			var l unstructured.UnstructuredList
			l.SetGroupVersionKind(ciliumPolicyGVK)

			return k8sClient.List(ctx, &l, client.InNamespace(uiEgressNS))
		}, "20s", "200ms").Should(Succeed())

		egress = &UIEgress{Client: k8sClient, Reader: k8sClient, Namespace: uiEgressNS}
	})

	policy := func() (*unstructured.Unstructured, error) {
		u := cnpGVK()

		return u, k8sClient.Get(ctx, types.NamespacedName{Namespace: uiEgressNS, Name: DefaultUIEgressPolicyName}, u)
	}

	setIssuer := func(issuer string) *appsv1.Deployment {
		dep := &appsv1.Deployment{}
		dep.Namespace, dep.Name = uiEgressNS, "ichor-ui"
		dep.Labels = map[string]string{controlPlaneLabel: uiPodLabelValue, nameLabel: releaseCheckApp}
		dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{controlPlaneLabel: uiPodLabelValue}}
		dep.Spec.Template.Labels = map[string]string{controlPlaneLabel: uiPodLabelValue}
		dep.Spec.Template.Spec.Containers = []corev1.Container{{Name: uiContainerName, Image: "ichor-ui:test"}}

		if issuer != "" {
			dep.Spec.Template.Spec.Containers[0].Env = []corev1.EnvVar{{Name: envOIDCIssuer, Value: issuer}}
		}

		Expect(k8sClient.Create(ctx, dep)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, dep))).To(Succeed()) })

		return dep
	}

	It("does nothing while Ichor's static policies are not installed", func() {
		setIssuer("https://login.example.test")

		changed, err := egress.Sync(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeFalse())

		_, err = policy()
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "a lone egress rule would cut every other flow of the pods")
	})

	It("writes the issuer rule beside the controller policy, leaves it alone when unchanged, and removes it with the issuer", func() {
		static := staticControllerPolicy(uiEgressNS, releaseCheckApp)
		Expect(k8sClient.Create(ctx, static)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, static))).To(Succeed()) })

		dep := setIssuer("https://login.example.test:8443/realms/x")

		changed, err := egress.Sync(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeTrue())

		got, err := policy()
		Expect(err).NotTo(HaveOccurred())

		raw, _ := json.Marshal(got.Object[keySpec])
		Expect(string(raw)).To(ContainSubstring(`"matchName":"login.example.test"`))
		Expect(string(raw)).To(ContainSubstring(`"port":"8443"`))
		Expect(string(raw)).To(ContainSubstring(`"ui-backend"`))
		Expect(string(raw)).NotTo(ContainSubstring("world"))

		changed, err = egress.Sync(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeFalse(), "an unchanged issuer must not be rewritten")

		// The issuer is dropped from the Deployment: the rule goes.
		dep.Spec.Template.Spec.Containers[0].Env = nil
		Expect(k8sClient.Update(ctx, dep)).To(Succeed())

		_, err = egress.Sync(ctx)
		Expect(err).NotTo(HaveOccurred())

		_, err = policy()
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("does nothing when disabled", func() {
		setIssuer("https://login.example.test")

		off := &UIEgress{Client: k8sClient, Reader: k8sClient, Namespace: uiEgressNS, Disabled: true}
		changed, err := off.Sync(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(changed).To(BeFalse())
	})
})
