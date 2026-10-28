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
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

var _ = Describe("IchorInstallation stack", func() {
	var (
		reg  *httptest.Server
		host string
	)

	newRegistry := func() (*httptest.Server, string) {
		s := httptest.NewServer(registry.New())
		DeferCleanup(s.Close)

		return s, strings.TrimPrefix(s.URL, "http://")
	}

	key := func(ns, n string) types.NamespacedName { return types.NamespacedName{Namespace: ns, Name: n} }

	getOrder := func(ns, n string) *unstructured.Unstructured {
		o := &unstructured.Unstructured{}
		o.SetGroupVersionKind(orderGVK)
		ExpectWithOffset(1, k8sClient.Get(ctx, key(ns, n), o)).To(Succeed())

		return o
	}

	deploymentReady := func(ns, n string) {
		EventuallyWithOffset(1, func() error {
			var d appsv1.Deployment
			if err := k8sClient.Get(ctx, key(ns, n), &d); err != nil {
				return err
			}

			d.Status.Replicas, d.Status.ReadyReplicas, d.Status.AvailableReplicas = 1, 1, 1

			return k8sClient.Status().Update(ctx, &d)
		}, 10*time.Second, 200*time.Millisecond).Should(Succeed())
	}

	It("applies, publishes and orders in sequence, and is idempotent", func() {
		reg, host = newRegistry()
		_ = reg

		createInstallation(ichorv1alpha1.IchorManagementModeIchor, "v0.1.0")

		r := &IchorInstallationReconciler{
			Client: k8sClient, Direct: k8sClient, ControllerNamespace: testIchorNamespace,
			ArgoCDNamespace: "argocd-absent", StackEnabled: true, RegistryHost: host,
		}

		pass := func() *ichorv1alpha1.IchorInstallation {
			res, err := r.Reconcile(ctx, reconcile.Request{Name: ichorv1alpha1.IchorInstallationName})
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
			ExpectWithOffset(1, res.RequeueAfter).To(BeNumerically(">", 0))

			var got ichorv1alpha1.IchorInstallation
			ExpectWithOffset(1, k8sClient.Get(ctx, key("", ichorv1alpha1.IchorInstallationName), &got)).To(Succeed())

			return &got
		}

		reason := func(i *ichorv1alpha1.IchorInstallation) string {
			c := apimeta.FindStatusCondition(i.Status.Conditions, conditionStackReady)
			ExpectWithOffset(1, c).NotTo(BeNil())

			return c.Reason
		}

		passUntil := func(want string) *ichorv1alpha1.IchorInstallation {
			var got *ichorv1alpha1.IchorInstallation

			EventuallyWithOffset(1, func() string {
				got = pass()

				return reason(got)
			}, 30*time.Second, 300*time.Millisecond).Should(Equal(want))

			return got
		}

		By("applying Kokumi first and waiting for its registry")
		got := passUntil("WaitingForKokumi")
		Expect(k8sClient.Get(ctx, key("", "kokumi"), &corev1.Namespace{})).To(Succeed())
		Expect(k8sClient.Get(ctx, key("kokumi", "kokumi-registry"), &appsv1.Deployment{})).To(Succeed())
		Expect(apiErrIsNotFound(k8sClient.Get(ctx, key("", "argocd"), &corev1.Namespace{}))).To(BeTrue(),
			"Argo CD must not be applied before Kokumi's registry is ready")

		By("generating the passwords once, outside status")
		var admin, redis corev1.Secret
		Expect(k8sClient.Get(ctx, key(testIchorNamespace, kokumiAdminSecret), &admin)).To(Succeed())
		Expect(k8sClient.Get(ctx, key(testIchorNamespace, argoRedisSecret), &redis)).To(Succeed())
		Expect(admin.Data).To(HaveKey(secretKeyPassword))
		Expect(admin.Data).To(HaveKey(secretKeyHash))

		By("applying Argo CD once Kokumi's registry is ready")
		deploymentReady("kokumi", "kokumi-registry")
		got = passUntil("WaitingForArgoCD")
		Expect(k8sClient.Get(ctx, key("argocd", "argocd-server"), &appsv1.Deployment{})).To(Succeed())
		Expect(got.Status.Stack.Kokumi.PublishedTag).To(BeEmpty(), "nothing is published before Argo CD is ready")

		By("publishing both bundles and creating one Order each")
		deploymentReady("argocd", "argocd-server")
		deploymentReady("argocd", "argocd-repo-server")
		got = passUntil("WaitingForDelivery")

		for n, st := range map[string]*ichorv1alpha1.IchorStackComponentStatus{
			"kokumi": got.Status.Stack.Kokumi, "argocd": got.Status.Stack.ArgoCD,
		} {
			Expect(st.PublishedTag).NotTo(BeEmpty())
			Expect(st.Digest).To(HavePrefix("sha256:"))
			Expect(st.OrderReady).To(BeFalse())

			ref, err := name.NewTag(host+"/ichor/"+n+":"+st.PublishedTag, name.Insecure)
			Expect(err).NotTo(HaveOccurred())

			d, err := remote.Head(ref)
			Expect(err).NotTo(HaveOccurred())
			Expect(d.Digest.String()).To(Equal(st.Digest))

			o := getOrder(n, n)
			src, _, _ := unstructured.NestedString(o.Object, "spec", "source", "oci")
			ver, _, _ := unstructured.NestedString(o.Object, "spec", "source", "version")
			mode, _, _ := unstructured.NestedString(o.Object, "spec", "promotion", "mode")
			Expect(src).To(Equal("oci://kokumi-registry.kokumi.svc.cluster.local:5000/ichor/" + n))
			Expect(ver).To(Equal(st.PublishedTag))
			Expect(mode).To(Equal("Automatic"))
			Expect(o.GetLabels()).To(HaveKeyWithValue(managedLabel, "true"))
		}

		By("reflecting Kokumi and Argo CD's view in status")
		for _, n := range []string{"kokumi", "argocd"} {
			o := getOrder(n, n)
			Expect(unstructured.SetNestedSlice(o.Object, []any{map[string]any{
				"type": "Ready", "status": "True", "reason": "Ready", "message": "ok",
				"lastTransitionTime": time.Now().UTC().Format(time.RFC3339),
			}}, "status", "conditions")).To(Succeed())
			Expect(k8sClient.Status().Update(ctx, o)).To(Succeed())

			app := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "argoproj.io/v1alpha1", "kind": "Application",
				"metadata": map[string]any{"name": n, "namespace": "argocd"},
				"spec": map[string]any{
					"project": "default",
					"source":  map[string]any{"repoURL": "oci://x", "path": "."},
					"destination": map[string]any{
						"server": "https://kubernetes.default.svc", "namespace": n,
					},
				},
			}}
			Expect(k8sClient.Create(ctx, app)).To(Succeed())
			Expect(unstructured.SetNestedField(app.Object, "Synced", "status", "sync", "status")).To(Succeed())
			Expect(unstructured.SetNestedField(app.Object, "Healthy", "status", "health", "status")).To(Succeed())
			Expect(k8sClient.Update(ctx, app)).To(Succeed())
			DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, app))).To(Succeed()) })
		}

		got = passUntil("Ready")
		Expect(got.Status.Stack.Kokumi.OrderReady).To(BeTrue())
		Expect(got.Status.Stack.ArgoCD.ApplicationHealth).To(Equal("Synced/Healthy"))
		Expect(got.Status.Stack.Kokumi.ApplicationHealth).To(Equal("Synced/Healthy"))

		raw, err := json.Marshal(got.Status)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring(string(admin.Data[secretKeyPassword])))
		Expect(string(raw)).NotTo(ContainSubstring(string(redis.Data[secretKeyPassword])))

		By("doing nothing on a second pass")
		orderRVs := map[string]string{}
		for _, n := range []string{"kokumi", "argocd"} {
			orderRVs[n] = getOrder(n, n).GetResourceVersion()
		}

		// Argo CD owns the applied objects now: a change to one must survive a pass.
		var srv appsv1.Deployment
		Expect(k8sClient.Get(ctx, key("kokumi", "kokumi-server"), &srv)).To(Succeed())
		two := int32(2)
		srv.Spec.Replicas = &two
		Expect(k8sClient.Update(ctx, &srv)).To(Succeed())

		again := pass()
		Expect(reason(again)).To(Equal("Ready"))
		Expect(again.Status.Stack).To(Equal(got.Status.Stack))

		for _, n := range []string{"kokumi", "argocd"} {
			Expect(getOrder(n, n).GetResourceVersion()).To(Equal(orderRVs[n]))
		}

		Expect(k8sClient.Get(ctx, key("kokumi", "kokumi-server"), &srv)).To(Succeed())
		Expect(*srv.Spec.Replicas).To(Equal(int32(2)), "Kokumi is applied once, then Argo CD's")

		var admin2, redis2 corev1.Secret
		Expect(k8sClient.Get(ctx, key(testIchorNamespace, kokumiAdminSecret), &admin2)).To(Succeed())
		Expect(k8sClient.Get(ctx, key(testIchorNamespace, argoRedisSecret), &redis2)).To(Succeed())
		Expect(admin2.Data).To(Equal(admin.Data))
		Expect(redis2.Data).To(Equal(redis.Data))

		var auth corev1.Secret
		Expect(k8sClient.Get(ctx, key("kokumi", "kokumi-server-auth"), &auth)).To(Succeed())
		Expect(string(auth.Data[secretKeyHash])).To(Equal(string(admin.Data[secretKeyHash])))

		By("publishing the same artifact again after the registry is lost")
		before := got.Status.Stack.Kokumi.Digest
		_, r.RegistryHost = newRegistry()
		again = pass()
		Expect(again.Status.Stack.Kokumi.Digest).To(Equal(before))
		Expect(getOrder("kokumi", "kokumi").GetAnnotations()).To(HaveKey(republishAnnotKey))

		By("moving only the Argo CD Order when its values change")
		var inst ichorv1alpha1.IchorInstallation
		Expect(k8sClient.Get(ctx, key("", ichorv1alpha1.IchorInstallationName), &inst)).To(Succeed())
		inst.Spec.Stack = &ichorv1alpha1.IchorStackSpec{
			ArgoCD: &ichorv1alpha1.IchorStackComponentSpec{
				Values: &apiextensionsv1.JSON{Raw: []byte(`{"global":{"domain":"argocd.example.test"}}`)},
			},
		}
		Expect(k8sClient.Update(ctx, &inst)).To(Succeed())

		moved := pass()
		Expect(moved.Status.Stack.ArgoCD.PublishedTag).NotTo(Equal(got.Status.Stack.ArgoCD.PublishedTag))
		Expect(moved.Status.Stack.Kokumi.PublishedTag).To(Equal(got.Status.Stack.Kokumi.PublishedTag))

		ver, _, _ := unstructured.NestedString(getOrder("argocd", "argocd").Object, "spec", "source", "version")
		Expect(ver).To(Equal(moved.Status.Stack.ArgoCD.PublishedTag))
	})
})

func apiErrIsNotFound(err error) bool { return err != nil && client.IgnoreNotFound(err) == nil }
