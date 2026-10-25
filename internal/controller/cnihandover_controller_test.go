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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/siderolabs/talos/pkg/machinery/constants"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
	"github.com/maeshinshin/ichor/pkg/render"
)

// testArgoApplicationCRD is a minimal stand-in for Argo CD's own Application
// CRD -- just enough shape (argoproj.io/v1alpha1, namespaced, free-form
// status) to let this test set status.sync.status/status.health.status the
// way the real controller would, without vendoring Argo CD's Go types or its
// actual CRD YAML. Installed on demand (installArgoApplicationCRD below),
// never from suite_test.go's BeforeSuite, so every other test in this
// package keeps proving Argo CD's CRD is absent by default -- which is
// exactly the "Argo CD not installed" case CNIHandoverReconciler must not
// hang on.
var testArgoApplicationCRD = &apiextensionsv1.CustomResourceDefinition{
	Name: "applications.argoproj.io",
	Spec: apiextensionsv1.CustomResourceDefinitionSpec{
		Group: "argoproj.io",
		Names: apiextensionsv1.CustomResourceDefinitionNames{
			Plural: "applications", Singular: "application", Kind: "Application", ListKind: "ApplicationList",
		},
		Scope: apiextensionsv1.NamespaceScoped,
		Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
			Name: "v1alpha1", Served: true, Storage: true,
			Schema: &apiextensionsv1.CustomResourceValidation{
				OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
					Type:                   "object",
					XPreserveUnknownFields: ptr.To(true),
				},
			},
		}},
	},
}

// installArgoApplicationCRD installs testArgoApplicationCRD into envtest's
// apiserver. envtest.InstallCRDs creates-or-updates (envtest/crd.go), so
// calling this from more than one It is safe. It also ensures the
// "argocd" namespace exists -- cilium.New().ArgoCDApplication's own
// namespace -- since envtest never creates one beyond "default".
func installArgoApplicationCRD() {
	_, err := envtest.InstallCRDs(cfg, envtest.CRDInstallOptions{CRDs: []*apiextensionsv1.CustomResourceDefinition{testArgoApplicationCRD}})
	Expect(err).NotTo(HaveOccurred())

	ns := &corev1.Namespace{Name: "argocd"}
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, ns))).To(Succeed())
}

func reconcileHandover(r *CNIHandoverReconciler, namespace, name string) (ctrl.Result, error) {
	return r.Reconcile(ctx, ctrl.Request{Namespace: namespace, Name: name})
}

// newTestCiliumCluster is newTestCluster with the CNI preset set to cilium
// (and handover to the given mode) before Create -- spec.cni.preset is
// immutable once a TalosCluster exists (the CEL rule on TalosClusterCNI), so
// unlike every other field in these tests it cannot be changed with a
// follow-up Update.
func newTestCiliumCluster(handover string) *ichorv1alpha1.TalosCluster {
	cluster := &ichorv1alpha1.TalosCluster{
		GenerateName: testClusterGenerate, Namespace: tmNamespace,
		Spec: ichorv1alpha1.TalosClusterSpec{
			Endpoint:          ichorv1alpha1.TalosClusterEndpoint{Type: testEndpointType, Address: testClusterAddress},
			TalosVersion:      testTalosVersion,
			KubernetesVersion: "v" + constants.DefaultKubernetesVersion,
			VersionContract:   "v1.14",
			SecretsRef:        corev1.LocalObjectReference{Name: "does-not-exist"},
			ControlPlane:      ichorv1alpha1.TalosClusterControlPlane{Count: 1, Schedulable: testSchedulable},
			CNI:               ichorv1alpha1.TalosClusterCNI{Preset: "cilium", Handover: handover},
		},
	}
	Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

	return cluster
}

var _ = Describe("CNIHandoverReconciler", func() {
	It("does nothing for a cluster with no CNI preset configured", func() {
		cluster := newTestCluster("does-not-exist", "v1.14")
		// Preset left at its zero value, "" -- the CRD default ("flannel")
		// only applies through the apiserver's structural-schema defaulting,
		// which newTestCluster's Create already went through, so assert the
		// precondition rather than assume it.
		Expect(cluster.Spec.CNI.Preset).To(Equal("flannel"))

		r := &CNIHandoverReconciler{Client: k8sClient}
		result, err := reconcileHandover(r, cluster.Namespace, cluster.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero())

		var fetched ichorv1alpha1.TalosCluster
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, &fetched)).To(Succeed())
		Expect(apimeta.FindStatusCondition(fetched.Status.Conditions, render.ConditionCNIHandover)).To(BeNil())
	})

	It("ignores a TalosCluster that no longer exists", func() {
		r := &CNIHandoverReconciler{Client: k8sClient}
		result, err := reconcileHandover(r, tmNamespace, "no-such-cluster")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero())
	})

	It("sets Disabled and never requeues when spec.cni.handover is disabled", func() {
		cluster := newTestCiliumCluster("disabled")

		r := &CNIHandoverReconciler{Client: k8sClient}
		result, err := reconcileHandover(r, cluster.Namespace, cluster.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero())

		var fetched ichorv1alpha1.TalosCluster
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, &fetched)).To(Succeed())
		cond := apimeta.FindStatusCondition(fetched.Status.Conditions, render.ConditionCNIHandover)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("Disabled"))
	})

	It("sets ManualPending and never requeues when spec.cni.handover is manual", func() {
		cluster := newTestCiliumCluster("manual")

		r := &CNIHandoverReconciler{Client: k8sClient}
		result, err := reconcileHandover(r, cluster.Namespace, cluster.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeZero())

		var fetched ichorv1alpha1.TalosCluster
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, &fetched)).To(Succeed())
		cond := apimeta.FindStatusCondition(fetched.Status.Conditions, render.ConditionCNIHandover)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("ManualPending"))
	})

	It("waits and requeues, without erroring, when Argo CD is not installed at all", func() {
		cluster := newTestCiliumCluster("")

		// This package's envtest apiserver never loads Argo CD's CRD
		// (suite_test.go only loads config/crd/bases), so this exercises
		// the "Argo CD absent" path for real.
		r := &CNIHandoverReconciler{Client: k8sClient}
		result, err := reconcileHandover(r, cluster.Namespace, cluster.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(handoverRequeueInterval))

		var fetched ichorv1alpha1.TalosCluster
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, &fetched)).To(Succeed())
		cond := apimeta.FindStatusCondition(fetched.Status.Conditions, render.ConditionCNIHandover)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("WaitingForArgoCD"))
	})

	Describe("with Argo CD's Application CRD installed", func() {
		BeforeEach(func() {
			installArgoApplicationCRD()
		})

		AfterEach(func() {
			// Both Its below create the same cilium.New().ArgoCDApplication
			// Name/Namespace; envtest's apiserver keeps objects across Its
			// within this suite, so the next one would otherwise collide.
			app, err := cilium.New().ArgoCDApplication("enabled")
			Expect(err).NotTo(HaveOccurred())

			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, newUnstructuredApplication(app.Name, app.Namespace)))).To(Succeed())
		})

		It("waits and requeues while the Application exists but is not Synced and Healthy", func() {
			cluster := newTestCiliumCluster("")

			app, err := cilium.New().ArgoCDApplication("enabled")
			Expect(err).NotTo(HaveOccurred())

			live := newUnstructuredApplication(app.Name, app.Namespace)
			Expect(unstructured.SetNestedField(live.Object, "OutOfSync", "status", "sync", "status")).To(Succeed())
			Expect(unstructured.SetNestedField(live.Object, "Degraded", "status", "health", "status")).To(Succeed())
			Expect(k8sClient.Create(ctx, live)).To(Succeed())

			r := &CNIHandoverReconciler{Client: k8sClient}
			result, err := reconcileHandover(r, cluster.Namespace, cluster.Name)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(handoverRequeueInterval))

			var fetched ichorv1alpha1.TalosCluster
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, &fetched)).To(Succeed())
			cond := apimeta.FindStatusCondition(fetched.Status.Conditions, render.ConditionCNIHandover)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal("WaitingForSync"))
		})

		It("sets Complete, with no requeue, once the Application is Synced and Healthy -- "+
			"at exactly the Name/Namespace preset.ArgoCDApplication itself returns", func() {
			cluster := newTestCiliumCluster("")

			app, err := cilium.New().ArgoCDApplication("enabled")
			Expect(err).NotTo(HaveOccurred())

			live := newUnstructuredApplication(app.Name, app.Namespace)
			Expect(unstructured.SetNestedField(live.Object, "Synced", "status", "sync", "status")).To(Succeed())
			Expect(unstructured.SetNestedField(live.Object, "Healthy", "status", "health", "status")).To(Succeed())
			Expect(k8sClient.Create(ctx, live)).To(Succeed())

			r := &CNIHandoverReconciler{Client: k8sClient}
			result, err := reconcileHandover(r, cluster.Namespace, cluster.Name)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			var fetched ichorv1alpha1.TalosCluster
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, &fetched)).To(Succeed())
			cond := apimeta.FindStatusCondition(fetched.Status.Conditions, render.ConditionCNIHandover)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal("Complete"))
		})
	})
})

// newUnstructuredApplication builds a bare Argo CD Application object (just
// the GVK, name and namespace) so a test can set its status fields and
// Create it against envtest's apiserver.
func newUnstructuredApplication(name, namespace string) *unstructured.Unstructured {
	live := &unstructured.Unstructured{}
	live.SetGroupVersionKind(argoApplicationGVK)
	live.SetName(name)
	live.SetNamespace(namespace)

	return live
}
