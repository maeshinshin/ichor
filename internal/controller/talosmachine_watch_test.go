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
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/settings"
	"github.com/maeshinshin/ichor/pkg/render"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/constants"
)

// These tests run the real manager, so an event on a render input has to reach an already
// converged TalosMachine through SetupWithManager's watches and nothing else. The wait is well
// under transientRecheck (30s, ADR 0060) and a converged machine is not requeued, so the only
// way the second apply happens in time is the watch.
var _ = Describe("TalosMachine re-reconcile on render inputs", func() {
	const (
		settle  = 15 * time.Second
		pollGap = 100 * time.Millisecond
	)

	var (
		ns   string
		clus *ichorv1alpha1.TalosCluster
		m    *ichorv1alpha1.TalosMachine
		stop context.CancelFunc
	)

	appliedHash := func() string {
		var got ichorv1alpha1.TalosMachine
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: m.Name}, &got)).To(Succeed())

		return got.Status.AppliedConfigHash
	}

	BeforeEach(func() {
		Expect(settle).To(BeNumerically("<", transientRecheck))

		name, bundle, secretName := newWatchNamespace("watch-")
		ns = name

		clus = &ichorv1alpha1.TalosCluster{
			GenerateName: testClusterGenerate, Namespace: ns,
			Spec: newClusterSpec(secretName, "v1.14"),
		}
		Expect(k8sClient.Create(ctx, clus)).To(Succeed())

		m = newTestMachineIn(ns, clus.Name, true)

		current, _, err := render.Render(clus, m, bundle, nil, nil)
		Expect(err).NotTo(HaveOccurred())

		stop = runWatchedReconciler(ns, &fakeTalosClient{current: current})

		Eventually(appliedHash, settle, pollGap).ShouldNot(BeEmpty(), "the machine converges before the change")
	})

	AfterEach(func() { stop() })

	It("applies a TalosConfigPatch created after the machine converged", func() {
		before := appliedHash()

		Expect(k8sClient.Create(ctx, &ichorv1alpha1.TalosConfigPatch{
			GenerateName: patchPrefix, Namespace: ns,
			Spec: ichorv1alpha1.TalosConfigPatchSpec{
				ClusterRef: corev1.LocalObjectReference{Name: clus.Name},
				Layer:      layerCluster,
				Documents: []ichorv1alpha1.TalosConfigPatchDocument{{
					Kind:         kindSysctl,
					SysctlConfig: &typedconfig.SysctlConfigV1Alpha1{Params: map[string]string{"net.core.somaxconn": "4096"}},
				}},
			},
		})).To(Succeed())

		Eventually(appliedHash, settle, pollGap).ShouldNot(Equal(before))
	})

	It("re-renders a TalosCluster spec change that is a render input", func() {
		before := appliedHash()

		var latest ichorv1alpha1.TalosCluster
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: clus.Name}, &latest)).To(Succeed())

		latest.Spec.Registries = []ichorv1alpha1.TalosClusterRegistry{{
			Host: "docker.io", Endpoints: []string{"https://mirror.example.com"},
		}}
		Expect(k8sClient.Update(ctx, &latest)).To(Succeed())

		// A registry mirror is read live by Talos (ADR 0009), so the re-render is applied
		// rather than refused with RebootRequired.
		Eventually(appliedHash, settle, pollGap).ShouldNot(Equal(before))
		Expect(readyCondition(fetchMachineIn(ns, m.Name)).Reason).NotTo(Equal(reasonRebootRequired))
	})
})

var _ = Describe("TalosMachine re-reconcile on a registry auth Secret", func() {
	It("re-renders when the password in the referenced Secret changes", func() {
		const (
			settle  = 15 * time.Second
			pollGap = 100 * time.Millisecond
		)

		ns, bundle, secretName := newWatchNamespace("watch-auth-")

		auth := &corev1.Secret{
			GenerateName: "regauth-", Namespace: ns, Data: map[string][]byte{"password": []byte("first")},
		}
		Expect(k8sClient.Create(ctx, auth)).To(Succeed())

		spec := newClusterSpec(secretName, "v1.14")
		spec.Registries = []ichorv1alpha1.TalosClusterRegistry{{
			Host: "registry.example.com", Endpoints: []string{"https://private.example.com"}, Username: "deploy",
			AuthSecretRef: &corev1.LocalObjectReference{Name: auth.Name},
		}}
		clus := &ichorv1alpha1.TalosCluster{GenerateName: testClusterGenerate, Namespace: ns, Spec: spec}
		Expect(k8sClient.Create(ctx, clus)).To(Succeed())

		m := newTestMachineIn(ns, clus.Name, true)

		resolved, err := render.ResolveRegistryAuth(ctx, k8sClient, ns, clus)
		Expect(err).NotTo(HaveOccurred())

		current, _, err := render.Render(clus, m, bundle, nil, &render.SystemInputs{RegistryAuth: resolved})
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(runWatchedReconciler(ns, &fakeTalosClient{current: current}))

		applied := func() string { return fetchMachineIn(ns, m.Name).Status.AppliedConfigHash }
		Eventually(applied, settle, pollGap).ShouldNot(BeEmpty())

		before := applied()

		var latest corev1.Secret
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: auth.Name}, &latest)).To(Succeed())

		latest.Data["password"] = []byte("second")
		Expect(k8sClient.Update(ctx, &latest)).To(Succeed())

		// Registry auth is picked up live too (Talos restarts the CRI service itself; ADR 0009), so
		// the re-render is applied. That it is applied at all, with nothing else touched, is the
		// watch's doing.
		Eventually(applied, settle, pollGap).ShouldNot(Equal(before))
		Expect(readyCondition(fetchMachineIn(ns, m.Name)).Reason).NotTo(Equal(reasonRebootRequired))
	})
})

// newWatchNamespace creates a namespace of its own holding a secrets bundle Secret and returns the
// namespace, the bundle and the Secret's name.
func newWatchNamespace(prefix string) (ns string, bundle *secrets.Bundle, secretName string) {
	nsObj := &corev1.Namespace{GenerateName: prefix}
	Expect(k8sClient.Create(ctx, nsObj)).To(Succeed())

	bundle = newTestBundle("v1.14")

	return nsObj.Name, bundle, newTestSecretIn(nsObj.Name, bundle).Name
}

// runWatchedReconciler runs the real manager in ns with a TalosMachineReconciler against fake, so
// only the watches can wake a machine; the returned func stops it.
func runWatchedReconciler(ns string, fake *fakeTalosClient) context.CancelFunc {
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme.Scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
		Cache:   cache.Options{DefaultNamespaces: map[string]cache.Config{ns: {}}},
		// Every spec builds the same controller name.
		Controller: config.Controller{SkipNameValidation: ptr.To(true)},
	})
	Expect(err).NotTo(HaveOccurred())

	Expect((&TalosMachineReconciler{
		Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(),
		ControllerNamespace: ns, TalosClient: clientFactory(fake),
	}).SetupWithManager(mgr)).To(Succeed())

	mctx, cancel := context.WithCancel(ctx)

	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(mctx)).To(Succeed())
	}()

	return cancel
}

// newClusterSpec is newTestCluster's spec without creating anything, so a fixture can place
// the cluster in a namespace of its own.
func newClusterSpec(secretName, versionContract string) ichorv1alpha1.TalosClusterSpec {
	return ichorv1alpha1.TalosClusterSpec{
		Endpoint:          ichorv1alpha1.TalosClusterEndpoint{Type: testEndpointType, Address: testClusterAddress},
		TalosVersion:      testTalosVersion,
		KubernetesVersion: "v" + constants.DefaultKubernetesVersion,
		VersionContract:   versionContract,
		SecretsRef:        corev1.LocalObjectReference{Name: secretName},
		ControlPlane:      ichorv1alpha1.TalosClusterControlPlane{Count: 1, Schedulable: testSchedulable},
		DriftPolicy:       settings.DriftPolicyDefault, // what the defaulting webhook fills; envtest here runs without it
	}
}

func fetchMachineIn(ns, name string) *ichorv1alpha1.TalosMachine {
	var got ichorv1alpha1.TalosMachine
	Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &got)).To(Succeed())

	return &got
}

// Literals shared by the fixtures in this file and talosmachine_controller_test.go (goconst).
const (
	bundleGenerate = "bundle-"
	machinePrefix  = "machine-"
	patchPrefix    = "patch-"
	kindSysctl     = "SysctlConfig"
)
