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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	yaml "go.yaml.in/yaml/v4"
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

		nsObj := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "watch-"}}
		Expect(k8sClient.Create(ctx, nsObj)).To(Succeed())

		ns = nsObj.Name

		bundle := newTestBundle("v1.14")
		raw, err := yaml.Marshal(bundle)
		Expect(err).NotTo(HaveOccurred())

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "bundle-", Namespace: ns},
			Data:       map[string][]byte{bundleSecretKey: raw},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		clus = &ichorv1alpha1.TalosCluster{
			ObjectMeta: metav1.ObjectMeta{GenerateName: testClusterGenerate, Namespace: ns},
			Spec:       newClusterSpec(secret.Name, "v1.14"),
		}
		Expect(k8sClient.Create(ctx, clus)).To(Succeed())

		m = &ichorv1alpha1.TalosMachine{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "machine-", Namespace: ns},
			Spec: ichorv1alpha1.TalosMachineSpec{
				ClusterRef:    corev1.LocalObjectReference{Name: clus.Name},
				Identity:      ichorv1alpha1.TalosMachineIdentity{UUID: "uuid-" + clus.Name},
				Endpoint:      "10.0.0.10:50000",
				Role:          roleWorker,
				ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
			},
		}
		Expect(k8sClient.Create(ctx, m)).To(Succeed())

		current, _, err := render.Render(clus, m, bundle, nil, nil)
		Expect(err).NotTo(HaveOccurred())

		fake := &fakeTalosClient{current: current}

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
		stop = cancel

		go func() {
			defer GinkgoRecover()
			Expect(mgr.Start(mctx)).To(Succeed())
		}()

		Eventually(appliedHash, settle, pollGap).ShouldNot(BeEmpty(), "the machine converges before the change")
	})

	AfterEach(func() { stop() })

	It("applies a TalosConfigPatch created after the machine converged", func() {
		before := appliedHash()

		Expect(k8sClient.Create(ctx, &ichorv1alpha1.TalosConfigPatch{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "patch-", Namespace: ns},
			Spec: ichorv1alpha1.TalosConfigPatchSpec{
				ClusterRef: corev1.LocalObjectReference{Name: clus.Name},
				Layer:      layerCluster,
				Documents: []ichorv1alpha1.TalosConfigPatchDocument{{
					Kind:         "SysctlConfig",
					SysctlConfig: &typedconfig.SysctlConfigV1Alpha1{Params: map[string]string{"net.core.somaxconn": "4096"}},
				}},
			},
		})).To(Succeed())

		Eventually(appliedHash, settle, pollGap).ShouldNot(Equal(before))
	})

	It("applies a TalosCluster spec change that is a render input", func() {
		before := appliedHash()

		var latest ichorv1alpha1.TalosCluster
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: clus.Name}, &latest)).To(Succeed())

		latest.Spec.Registries = []ichorv1alpha1.TalosClusterRegistry{{
			Host: "docker.io", Endpoints: []string{"https://mirror.example.com"},
		}}
		Expect(k8sClient.Update(ctx, &latest)).To(Succeed())

		Eventually(appliedHash, settle, pollGap).ShouldNot(Equal(before))
	})
})

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
	}
}
