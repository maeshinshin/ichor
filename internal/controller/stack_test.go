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
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/stackapply"
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

	// runStackJob does what the ichor-stack Job's pod would: apply the component against the
	// API server (Deployments become Ready as the kubelet would make them), then marks the Job
	// complete the way the installer tests do.
	runStackJob := func(component string, job *batchv1.Job, deployments ...string) {
		GinkgoHelper()

		var admin, redis corev1.Secret
		Expect(k8sClient.Get(ctx, key(testIchorNamespace, kokumiAdminSecret), &admin)).To(Succeed())
		Expect(k8sClient.Get(ctx, key(testIchorNamespace, argoRedisSecret), &redis)).To(Succeed())

		errc := make(chan error, 1)

		go func() {
			errc <- stackapply.Apply(ctx, k8sClient, stackapply.Options{
				Component:         component,
				KokumiAdminHash:   string(admin.Data[secretKeyHash]),
				ArgoRedisPassword: string(redis.Data[secretKeyPassword]), Timeout: time.Minute,
			})
		}()

		for _, d := range deployments {
			deploymentReady(map[string]string{componentKokumi: "kokumi", componentArgoCD: componentArgoCD}[component], d)
		}

		Eventually(errc, time.Minute).Should(Receive(BeNil()))
		setJobCondition(job, batchv1.JobComplete, "")
	}

	It("applies, publishes and orders in sequence, and is idempotent", func() {
		reg, host = newRegistry()
		_ = reg

		ensureCiliumPolicyCRD()
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

		By("creating the Kokumi Job and applying nothing itself")
		_ = passUntil("WaitingForKokumi")
		Expect(apiErrIsNotFound(k8sClient.Get(ctx, key("", "kokumi"), &corev1.Namespace{}))).To(BeTrue(),
			"the controller must not apply Kokumi: the Job does")

		var kjob batchv1.Job
		Expect(k8sClient.Get(ctx, key(testIchorNamespace, stackJobName(componentKokumi)), &kjob)).To(Succeed())
		Expect(kjob.Spec.Template.Spec.ServiceAccountName).To(Equal("ichor-stack"))

		By("generating the passwords once, outside status")
		var admin, redis corev1.Secret
		Expect(k8sClient.Get(ctx, key(testIchorNamespace, kokumiAdminSecret), &admin)).To(Succeed())
		Expect(k8sClient.Get(ctx, key(testIchorNamespace, argoRedisSecret), &redis)).To(Succeed())
		Expect(admin.Data).To(HaveKey(secretKeyPassword))
		Expect(admin.Data).To(HaveKey(secretKeyHash))

		By("applying Argo CD (its own Job) only after Kokumi's Job completed")
		runStackJob(componentKokumi, &kjob, "kokumi-registry")
		got := passUntil("WaitingForArgoCD")
		Expect(k8sClient.Get(ctx, key("kokumi", "kokumi-registry"), &appsv1.Deployment{})).To(Succeed())
		Expect(apiErrIsNotFound(k8sClient.Get(ctx, key("", componentArgoCD), &corev1.Namespace{}))).To(BeTrue(),
			"Argo CD must not be applied before Kokumi's Job is done")
		Expect(got.Status.Stack.Kokumi.PublishedTag).To(BeEmpty(), "nothing is published before Argo CD is ready")

		var ajob batchv1.Job
		Expect(k8sClient.Get(ctx, key(testIchorNamespace, stackJobName(componentArgoCD)), &ajob)).To(Succeed())

		By("publishing both bundles, without a Secret, and creating one Order each")
		runStackJob(componentArgoCD, &ajob, "argocd-server", "argocd-repo-server")
		got = passUntil("WaitingForDelivery")

		for n, st := range map[string]*ichorv1alpha1.IchorStackComponentStatus{
			componentKokumi: got.Status.Stack.Kokumi, componentArgoCD: got.Status.Stack.ArgoCD,
		} {
			Expect(st.PublishedTag).NotTo(BeEmpty())
			Expect(st.Digest).To(HavePrefix("sha256:"))
			Expect(st.OrderReady).To(BeFalse())

			ref, err := name.NewTag(host+"/ichor/"+n+":"+st.PublishedTag, name.Insecure)
			Expect(err).NotTo(HaveOccurred())

			d, err := remote.Head(ref)
			Expect(err).NotTo(HaveOccurred())
			Expect(d.Digest.String()).To(Equal(st.Digest))

			pushed := pushedBundle(ref)
			Expect(pushed).NotTo(MatchRegexp(`(?m)^kind:\s*"?Secret"?\s*$`), n+": a Secret was published")
			Expect(pushed).NotTo(ContainSubstring(string(admin.Data[secretKeyHash])))
			Expect(pushed).NotTo(ContainSubstring(string(redis.Data[secretKeyPassword])))

			o := getOrder(n, n)
			src, _, _ := unstructured.NestedString(o.Object, "spec", "source", "oci")
			ver, _, _ := unstructured.NestedString(o.Object, "spec", "source", "version")
			mode, _, _ := unstructured.NestedString(o.Object, "spec", "promotion", "mode")
			Expect(src).To(Equal("oci://kokumi-registry.kokumi.svc.cluster.local:5000/ichor/" + n))
			Expect(ver).To(Equal(st.PublishedTag))
			Expect(mode).To(Equal("Automatic"))
			Expect(o.GetLabels()).To(HaveKeyWithValue(managedLabel, "true"))
		}

		By("having the Job create the Secrets directly, safe from Argo CD's prune")
		for _, id := range []types.NamespacedName{
			key("kokumi", "kokumi-server-auth"), key(componentArgoCD, "argocd-redis"), key(componentArgoCD, "ichor-kokumi-registry"),
		} {
			var sec corev1.Secret
			Expect(k8sClient.Get(ctx, id, &sec)).To(Succeed(), id.String())
			Expect(sec.Annotations).To(HaveKeyWithValue("argocd.argoproj.io/sync-options", "Prune=false"))
			Expect(sec.Annotations).To(HaveKeyWithValue("argocd.argoproj.io/compare-options", "IgnoreExtraneous"))
		}

		By("reflecting Kokumi and Argo CD's view in status")
		for _, n := range []string{componentKokumi, componentArgoCD} {
			o := getOrder(n, n)
			Expect(unstructured.SetNestedSlice(o.Object, []any{map[string]any{
				"type": reasonReady, "status": "True", "reason": reasonReady, "message": "ok",
				"lastTransitionTime": time.Now().UTC().Format(time.RFC3339),
			}}, "status", "conditions")).To(Succeed())
			Expect(k8sClient.Status().Update(ctx, o)).To(Succeed())

			app := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "argoproj.io/v1alpha1", "kind": "Application",
				metaKey: map[string]any{labelName: n, nsKey: componentArgoCD},
				"spec": map[string]any{
					"project": "x",
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
		for _, n := range []string{componentKokumi, componentArgoCD} {
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

		for _, n := range []string{componentKokumi, componentArgoCD} {
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

		ver, _, _ := unstructured.NestedString(getOrder(componentArgoCD, componentArgoCD).Object, "spec", "source", "version")
		Expect(ver).To(Equal(moved.Status.Stack.ArgoCD.PublishedTag))
	})
})

// pushedBundle downloads what the controller pushed to ref and returns manifest.yaml.
func pushedBundle(ref name.Reference) string {
	GinkgoHelper()

	img, err := remote.Image(ref)
	Expect(err).NotTo(HaveOccurred())

	layers, err := img.Layers()
	Expect(err).NotTo(HaveOccurred())
	Expect(layers).To(HaveLen(1))

	rc, err := layers[0].Compressed()
	Expect(err).NotTo(HaveOccurred())

	defer func() { _ = rc.Close() }()

	zr, err := gzip.NewReader(rc)
	Expect(err).NotTo(HaveOccurred())

	tr := tar.NewReader(zr)
	_, err = tr.Next()
	Expect(err).NotTo(HaveOccurred())

	b, err := io.ReadAll(tr)
	Expect(err).NotTo(HaveOccurred())

	return string(b)
}

// ensureCiliumPolicyCRD installs a permissive stand-in for Cilium's CiliumNetworkPolicy CRD,
// which the Job applies last (envtest has no Cilium).
func ensureCiliumPolicyCRD() {
	GinkgoHelper()

	preserve := true
	crd := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "ciliumnetworkpolicies.cilium.io"},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "cilium.io", Scope: apiextensionsv1.NamespaceScoped,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural: "ciliumnetworkpolicies", Singular: "ciliumnetworkpolicy", Kind: "CiliumNetworkPolicy",
			},
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name: "v2", Served: true, Storage: true,
				Schema: &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
					Type: "object", XPreserveUnknownFields: &preserve,
				}},
			}},
		},
	}

	if err := k8sClient.Create(ctx, crd); err != nil {
		Expect(apierrors.IsAlreadyExists(err)).To(BeTrue())
	}
}

func apiErrIsNotFound(err error) bool { return err != nil && client.IgnoreNotFound(err) == nil }
