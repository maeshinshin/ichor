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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
	"github.com/maeshinshin/ichor/pkg/storage"
)

const (
	storageNodeKey   = "ichor.test/storage"
	storageNodeValue = "yes"
	storageDisk      = "disk.transport == 'nvme'"

	storageTestEndpoint = "10.0.0.10:50000"
)

// fakeSettingReader stands in for the storage system's own API: the envtest apiserver has no
// Longhorn CRDs, so it returns the Setting a test configures (or NotFound when value is "").
type fakeSettingReader struct{ value string }

func (f fakeSettingReader) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	if f.value == "" {
		return apierrors.NewNotFound(schema.GroupResource{Group: longhornGroup, Resource: "settings"}, key.Name)
	}

	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("unexpected type %T", obj)
	}

	u.Object["value"] = f.value

	return nil
}

func (fakeSettingReader) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return fmt.Errorf("not used")
}

func newStorageTestMachine(clusterName string, nodeLabels map[string]string) *ichorv1alpha1.TalosMachine {
	m := &ichorv1alpha1.TalosMachine{
		GenerateName: "machine-", Namespace: tmNamespace,
		Spec: ichorv1alpha1.TalosMachineSpec{
			ClusterRef:    corev1.LocalObjectReference{Name: clusterName},
			Identity:      ichorv1alpha1.TalosMachineIdentity{UUID: "uuid-" + clusterName},
			Endpoint:      storageTestEndpoint,
			Role:          roleWorker,
			ApprovalState: ichorv1alpha1.MachineApprovalStateApproved,
			NodeLabels:    nodeLabels,
		},
	}
	Expect(k8sClient.Create(ctx, m)).To(Succeed())

	return m
}

func newRealStorage(clusterName string) *ichorv1alpha1.TalosStorage {
	s := &ichorv1alpha1.TalosStorage{
		GenerateName: "storage-", Namespace: tmNamespace,
		Spec: ichorv1alpha1.TalosStorageSpec{
			ClusterRef:   corev1.LocalObjectReference{Name: clusterName},
			Nodes:        metav1.LabelSelector{MatchLabels: map[string]string{storageNodeKey: storageNodeValue}},
			DiskPreset:   storage.LonghornV1,
			DiskSelector: storageDisk,
		},
	}
	Expect(k8sClient.Create(ctx, s)).To(Succeed())

	return s
}

// convergeMachine reconciles m against a node whose running config is exactly what the
// controller renders for it now (including TalosStorages), so it records convergence.
func convergeMachine(
	m *ichorv1alpha1.TalosMachine, current []byte,
) {
	fake := &fakeTalosClient{current: current}
	r := &TalosMachineReconciler{
		Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake),
	}

	reconcileOK(r, m)
	Expect(fake.applyCalls).To(BeEmpty(), "the node already runs the rendered config; nothing to apply")
}

func reconcileStorage(r *TalosStorageReconciler, s *ichorv1alpha1.TalosStorage) *ichorv1alpha1.TalosStorage {
	_, err := r.Reconcile(ctx, ctrl.Request{Namespace: s.Namespace, Name: s.Name})
	Expect(err).NotTo(HaveOccurred())

	var got ichorv1alpha1.TalosStorage
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(s), &got)).To(Succeed())

	return &got
}

var _ = Describe("TalosStorage layer 1 and status", func() {
	var (
		bundleRaw = newTestBundle("v1.14")
	)

	// systemInputs is what the machine controller resolves for cluster right now.
	systemInputs := func(cluster *ichorv1alpha1.TalosCluster) *render.SystemInputs {
		sys, err := render.ResolveSystemInputs(ctx, k8sClient, tmNamespace, cluster)
		Expect(err).NotTo(HaveOccurred())

		return sys
	}

	It("renders the preparation into the machine a TalosStorage selects, and into no other", func() {
		secret := newTestSecret(bundleRaw)
		cluster := newTestCluster(secret.Name, "v1.14")
		selected := newStorageTestMachine(cluster.Name, map[string]string{storageNodeKey: storageNodeValue})
		other := newStorageTestMachine(cluster.Name, nil)
		newRealStorage(cluster.Name)

		sys := systemInputs(cluster)
		wantSelected, _, err := render.Render(cluster, selected, bundleRaw, nil, sys)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(wantSelected)).To(ContainSubstring("UserVolumeConfig"))

		wantOther, _, err := render.Render(cluster, other, bundleRaw, nil, sys)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(wantOther)).NotTo(ContainSubstring("UserVolumeConfig"))

		// The node already runs exactly that config: if the controller ignored TalosStorage the
		// hashes would differ and it would dry-run and apply.
		convergeMachine(selected, wantSelected)
		convergeMachine(other, wantOther)

		Expect(fetchMachine(selected).Status.AppliedConfigHash).To(Equal(render.Hash(wantSelected)))
		Expect(fetchMachine(other).Status.AppliedConfigHash).To(Equal(render.Hash(wantOther)))
	})

	Describe("status", func() {
		newReconciler := func(setting string) *TalosStorageReconciler {
			return &TalosStorageReconciler{Client: k8sClient, APIReader: fakeSettingReader{value: setting}}
		}

		cond := func(s *ichorv1alpha1.TalosStorage, t string) *metav1.Condition {
			return apimeta.FindStatusCondition(s.Status.Conditions, t)
		}

		It("is not Ready with no selected machines, and an absent storage system is not an error", func() {
			secret := newTestSecret(bundleRaw)
			cluster := newTestCluster(secret.Name, "v1.14")
			newStorageTestMachine(cluster.Name, nil)
			s := newRealStorage(cluster.Name)

			got := reconcileStorage(newReconciler(""), s)

			ready := cond(got, conditionStorageReady)
			Expect(ready).NotTo(BeNil())
			Expect(ready.Status).To(Equal(metav1.ConditionFalse))
			Expect(ready.Reason).To(Equal(reasonStorageNoNodes))

			system := cond(got, conditionStorageSystem)
			Expect(system).NotTo(BeNil())
			Expect(system.Status).To(Equal(metav1.ConditionUnknown))
			Expect(system.Reason).To(Equal(reasonStorageSystemAbsent))
			Expect(got.Status.ObservedGeneration).To(Equal(got.Generation))
		})

		It("tracks convergence of the machines it governs, ignoring the ones it does not", func() {
			secret := newTestSecret(bundleRaw)
			cluster := newTestCluster(secret.Name, "v1.14")
			selected := newStorageTestMachine(cluster.Name, map[string]string{storageNodeKey: storageNodeValue})
			other := newStorageTestMachine(cluster.Name, nil)

			// The selected machine converged before the TalosStorage existed.
			before, _, err := render.Render(cluster, selected, bundleRaw, nil, systemInputs(cluster))
			Expect(err).NotTo(HaveOccurred())
			convergeMachine(selected, before)

			s := newRealStorage(cluster.Name)
			r := newReconciler("")

			// That convergence does not count: it predates this TalosStorage.
			ready := cond(reconcileStorage(r, s), conditionStorageReady)
			Expect(ready.Status).To(Equal(metav1.ConditionFalse))
			Expect(ready.Reason).To(Equal(reasonStorageNodesNotReady))
			Expect(ready.Message).To(ContainSubstring(selected.Name))
			Expect(ready.Message).NotTo(ContainSubstring(other.Name))

			after, _, err := render.Render(cluster, selected, bundleRaw, nil, systemInputs(cluster))
			Expect(err).NotTo(HaveOccurred())
			convergeMachine(selected, after)

			// `other` never converged, but no TalosStorage governs it, so it does not block Ready.
			ready = cond(reconcileStorage(r, s), conditionStorageReady)
			Expect(ready.Status).To(Equal(metav1.ConditionTrue), "message: %s", ready.Message)
			Expect(ready.Reason).To(Equal(reasonStoragePrepared))
		})

		It("reports governed nodes whose Node lacks the preset's extension labels, without gating Ready", func() {
			secret := newTestSecret(bundleRaw)
			cluster := newTestCluster(secret.Name, "v1.14")
			labels := map[string]string{storageNodeKey: storageNodeValue}
			bare := newStorageTestMachine(cluster.Name, labels)
			full := newStorageTestMachine(cluster.Name, labels)
			s := newRealStorage(cluster.Name)
			r := newReconciler("")

			// No Node exists yet: nothing is known, which is not a failure.
			ext := cond(reconcileStorage(r, s), conditionStorageExtensions)
			Expect(ext.Status).To(Equal(metav1.ConditionUnknown))
			Expect(ext.Reason).To(Equal(reasonStorageExtensionsUnknown))

			mkNode := func(m *ichorv1alpha1.TalosMachine, nodeLabels map[string]string) string {
				m.Spec.Hostname = "node-" + m.Name
				Expect(k8sClient.Update(ctx, m)).To(Succeed())

				n := &corev1.Node{}
				n.Name, n.Labels = m.Spec.Hostname, nodeLabels
				Expect(k8sClient.Create(ctx, n)).To(Succeed())
				DeferCleanup(func() { _ = k8sClient.Delete(ctx, n) })

				return n.Name
			}

			bareNode := mkNode(bare, map[string]string{talosExtensionLabelPrefix + "iscsi-tools": "v0.2.0"})
			fullNode := mkNode(full, map[string]string{
				talosExtensionLabelPrefix + "iscsi-tools":      "v0.2.0",
				talosExtensionLabelPrefix + "util-linux-tools": "2.41",
			})

			got := reconcileStorage(r, s)
			ext = cond(got, conditionStorageExtensions)
			Expect(ext.Status).To(Equal(metav1.ConditionFalse))
			Expect(ext.Reason).To(Equal(reasonStorageMissingExtensions))
			Expect(ext.Message).To(ContainSubstring(bareNode + " lacks siderolabs/util-linux-tools"))
			Expect(ext.Message).NotTo(ContainSubstring(fullNode))
			Expect(ext.Message).To(ContainSubstring("spec.schematic"))
			Expect(ext.Message).To(ContainSubstring("ichor upgrade talos"))
			Expect(cond(got, conditionStorageReady)).NotTo(BeNil(), "the rest of the reconcile still ran")
			Expect(cond(got, conditionStorageSystem)).NotTo(BeNil())

			// After upgrading the schematic the labels appear and the condition clears.
			var n corev1.Node
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: bareNode}, &n)).To(Succeed())
			n.Labels[talosExtensionLabelPrefix+"util-linux-tools"] = "2.41"
			Expect(k8sClient.Update(ctx, &n)).To(Succeed())

			ext = cond(reconcileStorage(r, s), conditionStorageExtensions)
			Expect(ext.Status).To(Equal(metav1.ConditionTrue), "message: %s", ext.Message)
			Expect(ext.Reason).To(Equal(reasonStorageExtensionsPresent))
		})

		It("reports ClusterNotFound", func() {
			s := newRealStorage("no-such-cluster")

			ready := cond(reconcileStorage(newReconciler(""), s), conditionStorageReady)
			Expect(ready.Status).To(Equal(metav1.ConditionFalse))
			Expect(ready.Reason).To(Equal(reasonStorageClusterNotFound))
		})

		It("reads the storage system's setting through the preset's consistency check", func() {
			secret := newTestSecret(bundleRaw)
			cluster := newTestCluster(secret.Name, "v1.14")
			s := newRealStorage(cluster.Name)

			system := cond(reconcileStorage(newReconciler("false"), s), conditionStorageSystem)
			Expect(system.Status).To(Equal(metav1.ConditionFalse))
			Expect(system.Reason).To(Equal(reasonStorageSystemMismatch))

			system = cond(reconcileStorage(newReconciler("true"), s), conditionStorageSystem)
			Expect(system.Status).To(Equal(metav1.ConditionTrue))
			Expect(system.Reason).To(Equal(reasonStorageSystemConsistent))
		})
	})
})
