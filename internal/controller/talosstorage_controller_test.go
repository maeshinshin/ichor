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
	"strings"

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

	iscsiToolsExt     = "siderolabs/iscsi-tools"
	iscsiToolsVersion = "v0.2.0"
	utilLinuxVersion  = "2.41"
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

// List answers the namespace discovery: one Longhorn Node in longhorn-system when a value is configured, none otherwise.
func (f fakeSettingReader) List(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
	if f.value == "" {
		return nil
	}

	ul, ok := list.(*unstructured.UnstructuredList)
	if !ok {
		return fmt.Errorf("unexpected type %T", list)
	}

	n := unstructured.Unstructured{}
	n.SetNamespace(defaultLonghornNamespace)
	n.SetName("node")
	ul.Items = []unstructured.Unstructured{n}

	return nil
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

		It("reports governed nodes whose Node lacks the preset's extension labels", func() {
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

			bareNode := mkNode(bare, map[string]string{talosExtensionLabelPrefix + "iscsi-tools": iscsiToolsVersion})
			fullNode := mkNode(full, map[string]string{
				talosExtensionLabelPrefix + "iscsi-tools":      iscsiToolsVersion,
				talosExtensionLabelPrefix + "util-linux-tools": utilLinuxVersion,
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
			n.Labels[talosExtensionLabelPrefix+"util-linux-tools"] = utilLinuxVersion
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

		It("has no StorageSystemConsistent condition on rook-v1, which has no Longhorn setting to check", func() {
			secret := newTestSecret(bundleRaw)
			cluster := newTestCluster(secret.Name, "v1.14")
			s := &ichorv1alpha1.TalosStorage{
				GenerateName: "rook-", Namespace: tmNamespace,
				Spec: ichorv1alpha1.TalosStorageSpec{
					ClusterRef: corev1.LocalObjectReference{Name: cluster.Name},
					Nodes:      metav1.LabelSelector{MatchLabels: map[string]string{storageNodeKey: storageNodeValue}},
					DiskPreset: storage.RookV1,
				},
			}
			Expect(k8sClient.Create(ctx, s)).To(Succeed())

			got := reconcileStorage(newReconciler(""), s)
			Expect(cond(got, conditionStorageReady)).NotTo(BeNil())
			Expect(cond(got, conditionStorageSystem)).To(BeNil())
			Expect(cond(got, conditionStorageExtensions)).To(BeNil(), "rook-v1 names no extension to check")
		})

		It("counts the governed set in ExtensionsPresent and reads a node with no Node yet as unknown", func() {
			secret := newTestSecret(bundleRaw)
			cluster := newTestCluster(secret.Name, "v1.14")
			labels := map[string]string{storageNodeKey: storageNodeValue}
			s := newRealStorage(cluster.Name)

			for i := range 3 {
				m := newStorageTestMachine(cluster.Name, labels)
				m.Spec.Hostname = "node-" + m.Name
				Expect(k8sClient.Update(ctx, m)).To(Succeed())

				if i == 2 {
					continue // a NotReady node after a reboot: its Node facts cannot be read
				}

				n := &corev1.Node{}
				n.Name = m.Spec.Hostname
				n.Labels = map[string]string{
					talosExtensionLabelPrefix + "iscsi-tools":      "present",
					talosExtensionLabelPrefix + "util-linux-tools": "present",
				}
				Expect(k8sClient.Create(ctx, n)).To(Succeed())
				DeferCleanup(func() { _ = k8sClient.Delete(ctx, n) })
			}

			ext := cond(reconcileStorage(newReconciler(""), s), conditionStorageExtensions)
			Expect(ext.Status).To(Equal(metav1.ConditionUnknown), "message: %s", ext.Message)
			Expect(ext.Message).To(ContainSubstring("2 of 3 governed nodes reported"))
		})
	})
})

// fakeSchematicFactory stands in for Image Factory: the ID is a function of the extensions, as there.
type fakeSchematicFactory struct {
	calls [][]string
	urls  []string
}

func (f *fakeSchematicFactory) Register(_ context.Context, factoryURL string, extensions []string) (string, error) {
	f.urls = append(f.urls, factoryURL)
	f.calls = append(f.calls, extensions)

	return "schematic-" + strings.Join(extensions, "+"), nil
}

// countingClient counts the patches made through it: an identical merge patch leaves the object's
// resourceVersion alone, so only a count shows a redundant write.
type countingClient struct {
	client.Client
	patches int
}

func (c *countingClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	c.patches++

	return c.Client.Patch(ctx, obj, patch, opts...)
}

var _ = Describe("TalosStorage adds the extensions its preset needs (ADR 0173)", func() {
	const wantID = "schematic-siderolabs/iscsi-tools+siderolabs/util-linux-tools"

	var (
		bundleRaw = newTestBundle("v1.14")
		factory   *fakeSchematicFactory
		actor     *countingClient
		r         *TalosStorageReconciler
	)

	BeforeEach(func() {
		factory = &fakeSchematicFactory{}
		actor = &countingClient{Client: k8sClient}
		r = &TalosStorageReconciler{
			Client: k8sClient, APIReader: fakeSettingReader{}, Actor: actor, Factory: factory,
		}
	})

	// bareNode makes a governed machine whose Kubernetes Node carries no extension label.
	bareNode := func(clusterName string) *ichorv1alpha1.TalosMachine {
		m := newStorageTestMachine(clusterName, map[string]string{storageNodeKey: storageNodeValue})
		m.Spec.Hostname = "node-" + m.Name
		Expect(k8sClient.Update(ctx, m)).To(Succeed())

		n := &corev1.Node{}
		n.Name = m.Spec.Hostname
		Expect(k8sClient.Create(ctx, n)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, n) })

		return m
	}

	storageOps := func(clusterName string) []ichorv1alpha1.TalosOperation {
		var all ichorv1alpha1.TalosOperationList

		var mine []ichorv1alpha1.TalosOperation

		Expect(k8sClient.List(ctx, &all, client.InNamespace(tmNamespace))).To(Succeed())

		for _, o := range all.Items {
			if o.Spec.ClusterRef.Name == clusterName && o.Annotations[ichorv1alpha1.OperationStorageExtensionsAnnotation] != "" {
				mine = append(mine, o)
			}
		}

		return mine
	}

	fetchCluster := func(c *ichorv1alpha1.TalosCluster) *ichorv1alpha1.TalosCluster {
		var got ichorv1alpha1.TalosCluster
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(c), &got)).To(Succeed())

		return &got
	}

	It("extends the schematic once, creates one paused upgrade, and is idempotent", func() {
		cluster := newTestCluster(newTestSecret(bundleRaw).Name, "v1.14")
		bareNode(cluster.Name)
		s := newRealStorage(cluster.Name)

		reconcileStorage(r, s)

		got := fetchCluster(cluster)
		Expect(got.Spec.Schematic).NotTo(BeNil())
		Expect(got.Spec.Schematic.ID).To(Equal(wantID))
		Expect(factory.calls).To(Equal([][]string{{iscsiToolsExt, "siderolabs/util-linux-tools"}}))
		Expect(factory.urls).To(Equal([]string{ichorv1alpha1.DefaultImageFactoryURL}), "an unset spec.imageFactoryURL is the stock factory")

		ops := storageOps(cluster.Name)
		Expect(ops).To(HaveLen(1))
		Expect(ops[0].Spec.Kind).To(Equal(ichorv1alpha1.TalosOperationKindTalos))
		Expect(ops[0].Spec.Paused).To(BeTrue(), "the upgrade is created waiting for approval")
		Expect(ops[0].Annotations).To(HaveKeyWithValue(ichorv1alpha1.OperationStorageExtensionsAnnotation, wantID))

		reconcileStorage(r, s)

		Expect(storageOps(cluster.Name)).To(HaveLen(1), "a second pass creates no second operation")
		Expect(fetchCluster(cluster).ResourceVersion).To(Equal(got.ResourceVersion), "a second pass writes the cluster no more")
		Expect(actor.patches).To(Equal(1), "the schematic is patched once, not on every pass")
	})

	It("is not Ready while the nodes lack the extensions, and names them and the operation", func() {
		cluster := newTestCluster(newTestSecret(bundleRaw).Name, "v1.14")
		m := bareNode(cluster.Name)
		s := newRealStorage(cluster.Name)

		// The node converged on the storage preparation, so Ready would be True without the gate.
		sys, err := render.ResolveSystemInputs(ctx, k8sClient, tmNamespace, cluster)
		Expect(err).NotTo(HaveOccurred())

		rendered, _, err := render.Render(cluster, m, bundleRaw, nil, sys)
		Expect(err).NotTo(HaveOccurred())
		convergeMachine(m, rendered)

		got := reconcileStorage(r, s)
		ready := apimeta.FindStatusCondition(got.Status.Conditions, conditionStorageReady)
		Expect(ready.Status).To(Equal(metav1.ConditionFalse), "message: %s", ready.Message)
		Expect(ready.Reason).To(Equal(reasonStorageExtensionsMissing))
		Expect(ready.Message).To(ContainSubstring(iscsiToolsExt))
		Expect(ready.Message).To(ContainSubstring("siderolabs/util-linux-tools"))
		Expect(ready.Message).To(ContainSubstring(storageOps(cluster.Name)[0].Name))
		Expect(ready.Message).To(ContainSubstring("waiting for approval"))

		// The upgrade ran: the Node now carries the labels.
		var n corev1.Node
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: m.Spec.Hostname}, &n)).To(Succeed())
		n.Labels = map[string]string{
			talosExtensionLabelPrefix + "iscsi-tools":      iscsiToolsVersion,
			talosExtensionLabelPrefix + "util-linux-tools": utilLinuxVersion,
		}
		Expect(k8sClient.Update(ctx, &n)).To(Succeed())

		// The new schematic is part of what the machine renders, so it converges on that.
		cluster = fetchCluster(cluster)
		sys, err = render.ResolveSystemInputs(ctx, k8sClient, tmNamespace, cluster)
		Expect(err).NotTo(HaveOccurred())

		rendered, _, err = render.Render(cluster, m, bundleRaw, nil, sys)
		Expect(err).NotTo(HaveOccurred())
		convergeMachine(m, rendered)

		ready = apimeta.FindStatusCondition(reconcileStorage(r, s).Status.Conditions, conditionStorageReady)
		Expect(ready.Status).To(Equal(metav1.ConditionTrue), "message: %s", ready.Message)
	})

	It("runs no upgrade until a person approves it", func() {
		cluster := newTestCluster(newTestSecret(bundleRaw).Name, "v1.14")
		bareNode(cluster.Name)
		reconcileStorage(r, newRealStorage(cluster.Name))

		op := &storageOps(cluster.Name)[0]
		opr, fake := newOpTestReconciler(nil)

		mustReconcileOperation(opr, op)
		Expect(fake.calls).To(BeEmpty(), "no machine is advanced before the approval")

		got := fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(pausedMessage(got)).To(ContainSubstring("waiting for approval"))
		Expect(pausedMessage(got)).To(ContainSubstring("ichor approve operation " + op.Name))
		Expect(pausedMessage(got)).NotTo(ContainSubstring("resume"))
		Expect(apimeta.FindStatusCondition(got.Status.Conditions, conditionOperationPaused).Reason).To(Equal(ichorv1alpha1.OperationAwaitingApprovalReason))
	})

	It("registers the schematic at the cluster's own Image Factory", func() {
		cluster := newTestCluster(newTestSecret(bundleRaw).Name, "v1.14")
		cluster.Spec.ImageFactoryURL = "https://factory.example.test:8443"
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())
		bareNode(cluster.Name)

		reconcileStorage(r, newRealStorage(cluster.Name))

		Expect(factory.urls).To(Equal([]string{"https://factory.example.test:8443"}))
	})

	It("writes nothing over a schematic it cannot read, and says what to do", func() {
		cluster := newTestCluster(newTestSecret(bundleRaw).Name, "v1.14")
		cluster.Spec.Schematic = &ichorv1alpha1.TalosClusterSchematic{ID: "custom-id"}
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		s := newRealStorage(cluster.Name)

		// Convergence is what would make Ready True without the extension gate.
		m := bareNode(cluster.Name)
		sys, err := render.ResolveSystemInputs(ctx, k8sClient, tmNamespace, cluster)
		Expect(err).NotTo(HaveOccurred())

		rendered, _, err := render.Render(cluster, m, bundleRaw, nil, sys)
		Expect(err).NotTo(HaveOccurred())
		convergeMachine(m, rendered)

		ready := apimeta.FindStatusCondition(reconcileStorage(r, s).Status.Conditions, conditionStorageReady)
		Expect(ready.Status).To(Equal(metav1.ConditionFalse))
		Expect(ready.Reason).To(Equal(reasonStorageSchematicNotMergeable))
		Expect(ready.Message).To(ContainSubstring(iscsiToolsExt))
		Expect(fetchCluster(cluster).Spec.Schematic.ID).To(Equal("custom-id"))
		Expect(storageOps(cluster.Name)).To(BeEmpty())
	})
	It("says in the condition that the identity may not write, naming webhook.enabled, and returns no error", func() {
		cluster := newTestCluster(newTestSecret(bundleRaw).Name, "v1.14")
		s := newRealStorage(cluster.Name)
		m := bareNode(cluster.Name)
		sys, err := render.ResolveSystemInputs(ctx, k8sClient, tmNamespace, cluster)
		Expect(err).NotTo(HaveOccurred())

		rendered, _, err := render.Render(cluster, m, bundleRaw, nil, sys)
		Expect(err).NotTo(HaveOccurred())
		convergeMachine(m, rendered)

		r.Actor = forbiddenActor{k8sClient}

		ready := apimeta.FindStatusCondition(reconcileStorage(r, s).Status.Conditions, conditionStorageReady)
		Expect(ready.Reason).To(Equal(reasonStorageSchematicNotMergeable))
		Expect(ready.Message).To(ContainSubstring("webhook.enabled"))
		Expect(storageOps(cluster.Name)).To(BeEmpty())
	})
})
