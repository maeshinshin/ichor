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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

func operationGone(op *ichorv1alpha1.TalosOperation) bool {
	var got ichorv1alpha1.TalosOperation

	return apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Namespace: op.Namespace, Name: op.Name}, &got))
}

func lockHolder(cluster *ichorv1alpha1.TalosCluster) (string, bool) {
	holder, held, err := opTestLock(cluster).Holder(ctx)
	Expect(err).NotTo(HaveOccurred())

	return holder, held
}

// Hardware finding: deleting a Paused ca-rotation left it Paused with its finalizer and the
// machines quiesced. A rotation deleted before its point of no return now gives the cluster back.
var _ = Describe("Deleting a rotation operation", func() {
	// reconcileUntilGone reconciles op with a fresh reconciler each time until it is gone. A step that deletes
	// the operation cannot read it back, so this does not use the fixtures' step.
	reconcileUntilGone := func(newReconciler func() *TalosOperationReconciler, op *ichorv1alpha1.TalosOperation, limit int) {
		for range limit {
			if operationGone(op) {
				return
			}

			_, err := reconcileOperation(newReconciler(), op)
			Expect(err).NotTo(HaveOccurred())
		}

		Expect(operationGone(op)).To(BeTrue(), "the operation was not released")
	}

	Describe("a ca-rotation", func() {
		It("abandons a rotation paused before it made the new CA issue: the new CAs leave the bundle, the lock is free and the machines are rendered from the old CAs again", func() {
			f := newCARotFixture()
			f.holdHandoff = true

			op := f.newOp()
			op, _ = f.runUntil(op, atStage(ichorv1alpha1.CARotationStageAwaitingCredentials))
			op = f.step(op)
			Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
			Expect(f.storedRotation()).NotTo(BeNil())

			_, held := lockHolder(f.cluster)
			Expect(held).To(BeTrue())

			Expect(k8sClient.Delete(ctx, op)).To(Succeed())
			reconcileUntilGone(f.newReconciler, op, 6)

			Expect(f.storedRotation()).To(BeNil(), "rotation.yaml, with the new CAs' keys, is removed")
			Expect(f.secretData()).NotTo(HaveKey("rotation.yaml"))
			stored := f.storedBundle()
			Expect(stored.Certs.OS.Crt).To(Equal(f.old.Certs.OS.Crt), "the bundle still holds the CAs the cluster started with")
			Expect(stored.Certs.K8s.Crt).To(Equal(f.old.Certs.K8s.Crt))

			_, held = lockHolder(f.cluster)
			Expect(held).To(BeFalse(), "the cluster lock is released")

			active, err := activeCARotation(ctx, k8sClient, f.cluster.Namespace, f.cluster.Name)
			Expect(err).NotTo(HaveOccurred())
			Expect(active).To(BeEmpty(), "the machine controller no longer sees a rotation under way")

			// The machine controller renders the old CAs again.
			mr := &TalosMachineReconciler{
				Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
				TalosClient: func(_ context.Context, _ *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, _ *secrets.Bundle) (TalosClient, error) {
					return &rotClient{w: f.world, name: m.Name, node: f.world.nodes[m.Name]}, nil
				},
			}

			for range 4 {
				reconcileOK(mr, f.machines[0])
			}

			// The nodes still accept the extra CAs. Dropping them is a change the machine controller never makes by
			// itself (it does not reboot nodes), so it says what to run, as a hand-made abandon did on hardware.
			ready := readyCondition(fetchMachine(f.machines[0]))
			Expect(ready.Reason).To(Equal(reasonRebootRequired))
			Expect(ready.Message).To(ContainSubstring("acceptedCAs"))
			Expect(ready.Message).To(ContainSubstring("ichor config apply"))
		})

		It("abandons a rotation of CAs that survive the Issuing stage", func() {
			f := newCARotFixture()
			f.targets = []string{ichorv1alpha1.CARotationTargetTalos}
			f.holdHandoff = true

			op := f.newOp()
			op, _ = f.runUntil(op, atStage(ichorv1alpha1.CARotationStageAwaitingCredentials))
			f.holdHandoff = false
			op, _ = f.runUntil(op, atStage(ichorv1alpha1.CARotationStageIssuing))

			Expect(k8sClient.Delete(ctx, op)).To(Succeed())
			reconcileUntilGone(f.newReconciler, op, 6)
			Expect(f.storedRotation()).To(BeNil())

			_, held := lockHolder(f.cluster)
			Expect(held).To(BeFalse())
		})

		It("uncordons the control plane it was rebooting when it is abandoned", func() {
			f := newCARotFixtureCPs(1)
			f.targets = []string{ichorv1alpha1.CARotationTargetEtcd}

			cp := f.machines[0]
			f.world.nodeNames[cp.Name] = rotCPNode
			f.world.frozenBoot[cp.Name] = true

			cp.Status.NodeName = rotCPNode
			Expect(k8sClient.Status().Update(ctx, cp)).To(Succeed())

			kc := fake.NewClientset(&corev1.Node{Name: rotCPNode})
			f.kube = kc

			op := f.newOp()
			for range 12 {
				op = f.step(op)
			}

			Expect(op.Status.CARotation.EtcdRebooting).To(Equal(cp.Name))

			node, err := kc.CoreV1().Nodes().Get(ctx, rotCPNode, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(node.Spec.Unschedulable).To(BeTrue())

			Expect(k8sClient.Delete(ctx, op)).To(Succeed())
			reconcileUntilGone(f.newReconciler, op, 6)

			node, err = kc.CoreV1().Nodes().Get(ctx, rotCPNode, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(node.Spec.Unschedulable).To(BeFalse(), "the node accepts pods again")
		})

		It("keeps a rotation that is past the point of no return, says so, and releases it only when it is done", func() {
			f := newCARotFixture()
			op := f.newOp()
			op, _ = f.runUntil(op, atStage(ichorv1alpha1.CARotationStageDropping))

			Expect(k8sClient.Delete(ctx, op)).To(Succeed())
			op = f.step(op)

			Expect(operationGone(op)).To(BeFalse())
			Expect(f.storedRotation()).NotTo(BeNil(), "the new CAs' keys stay: the old CA is about to go")

			cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionDeletionHeld)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Message).To(ContainSubstring("Dropping"))

			f.acknowledge(op)
			reconcileUntilGone(f.newReconciler, op, 80)
			Expect(f.storedBundle().Certs.OS.Crt).NotTo(Equal(f.old.Certs.OS.Crt), "and the rotation ran to its end")
		})
	})

	Describe("a key-rotation", func() {
		It("abandons a rotation in Accepting: the new keys leave the bundle and the lock is released", func() {
			f := newKeyRotFixture(fastBundle())
			op := f.newOp(bothTargets)
			op, _ = f.runUntil(op, atStage(ichorv1alpha1.KeyRotationStageAccepting))
			Expect(f.storedKeys()).NotTo(BeNil())

			Expect(k8sClient.Delete(ctx, op)).To(Succeed())
			reconcileUntilGone(f.newReconciler, op, 6)

			Expect(f.storedKeys()).To(BeNil(), "keyrotation.yaml, with the new keys, is removed")
			stored := f.storedBundle()
			Expect(string(stored.Certs.K8sServiceAccount.Key)).To(Equal(string(f.old.Certs.K8sServiceAccount.Key)))
			Expect(stored.Secrets.SecretboxEncryptionSecret).To(Equal(f.old.Secrets.SecretboxEncryptionSecret))

			_, held := lockHolder(f.cluster)
			Expect(held).To(BeFalse())

			active, err := activeKeyRotation(ctx, k8sClient, f.cluster.Namespace, f.cluster.Name)
			Expect(err).NotTo(HaveOccurred())
			Expect(active).To(BeEmpty())
		})

		It("keeps a rotation whose new keys already sign and encrypt, and says so", func() {
			f := newKeyRotFixture(fastBundle())
			op := f.newOp(bothTargets)
			op, _ = f.runUntil(op, atStage(ichorv1alpha1.KeyRotationStageIssuing))

			Expect(k8sClient.Delete(ctx, op)).To(Succeed())
			op = f.step(op)

			Expect(operationGone(op)).To(BeFalse())
			Expect(f.storedKeys()).NotTo(BeNil())

			cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionDeletionHeld)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Message).To(ContainSubstring("Issuing"))
		})
	})
})
