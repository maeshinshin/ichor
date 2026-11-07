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
	"bytes"
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/reclaim"
)

// ADR 0136: a machine of a cluster Ichor manages that has no approved TalosMachine (an orphan: removed
// before ADR 0135, so never wiped) is wiped with the cluster's own credentials, and its Kubernetes Node
// is deleted afterwards.
var _ = Describe("MachineReclaim of an orphan", func() {
	const (
		ns     = "ichor-system"
		uuid   = "orphan-uuid-1"
		orphan = "orphan-host-1"

		orphanContract = "v1.14"
	)

	var (
		node    *fakeReclaimNode
		r       *MachineReclaimReconciler
		cluster *ichorv1alpha1.TalosCluster
		dialed  reclaim.Credential
		now     time.Time
		seq     int
	)

	BeforeEach(func() {
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &corev1.Namespace{Name: ns}))).To(Succeed())

		seq++
		now = time.Now().Truncate(time.Second)
		dialed = reclaim.Credential{}

		bundle := newFreshTestBundle(orphanContract)
		sec := newTestSecretIn(ns, bundle)

		cluster = &ichorv1alpha1.TalosCluster{
			GenerateName: "orphan-", Namespace: ns, Spec: newClusterSpec(sec.Name, orphanContract),
		}
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
			Expect(k8sClient.Delete(ctx, sec)).To(Succeed())
		})

		node = &fakeReclaimNode{in: reclaim.Inspection{
			Address: testNodeAddr, UUID: uuid, Hostname: orphan, ClusterName: cluster.Name, ClusterID: bundle.Cluster.ID,
			Version: testTalosVer, ClusterReachable: true,
		}}

		r = &MachineReclaimReconciler{
			Client: inNamespace{k8sClient, ns}, ControllerNamespace: ns, Recorder: record.NewFakeRecorder(200),
			Dial: func(_ context.Context, cred reclaim.Credential, _, _ string) (reclaim.Node, error) {
				dialed = cred

				return node, nil
			},
			Probe:        func(context.Context, string) (string, error) { return uuid, nil },
			Now:          func() time.Time { return now },
			WaitWindow:   time.Second,
			WaitInterval: 10 * time.Millisecond,
		}
	})

	request := func() types.NamespacedName {
		mr := &ichorv1alpha1.MachineReclaim{
			Name: fmt.Sprintf("orphan-mr-%d", seq), Namespace: ns,
			Annotations: map[string]string{ichorv1alpha1.ReclaimRequestedByAnnotation: "alice"},
			Spec: ichorv1alpha1.MachineReclaimSpec{
				Address: testNodeAddr, ClusterCredentials: true,
				ClusterRef: &corev1.LocalObjectReference{Name: cluster.Name},
			},
		}
		Expect(k8sClient.Create(ctx, mr)).To(Succeed())

		return client.ObjectKeyFromObject(mr)
	}

	reconcileOK := func(key types.NamespacedName) {
		GinkgoHelper()

		_, err := r.Reconcile(ctrlLogInto(ctx, &bytes.Buffer{}), reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
	}

	get := func(key types.NamespacedName) *ichorv1alpha1.MachineReclaim {
		var mr ichorv1alpha1.MachineReclaim
		Expect(k8sClient.Get(ctx, key, &mr)).To(Succeed())

		return &mr
	}

	confirm := func(key types.NamespacedName) {
		mr := get(key)
		mr.Spec.ConfirmUUID = uuid
		mr.Annotations[ichorv1alpha1.ReclaimConfirmedByAnnotation] = "bob"
		Expect(k8sClient.Update(ctx, mr)).To(Succeed())
	}

	machine := func(approved bool) {
		state := ichorv1alpha1.MachineApprovalStatePending
		if approved {
			state = ichorv1alpha1.MachineApprovalStateApproved
		}

		m := &ichorv1alpha1.TalosMachine{
			GenerateName: machinePrefix, Namespace: ns,
			Spec: ichorv1alpha1.TalosMachineSpec{
				ClusterRef: corev1.LocalObjectReference{Name: cluster.Name}, Identity: ichorv1alpha1.TalosMachineIdentity{UUID: uuid},
				Endpoint: storageTestEndpoint, Role: roleWorker, ApprovalState: state,
			},
		}
		Expect(k8sClient.Create(ctx, m)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, m))).To(Succeed()) })
	}

	kubeNode := func(systemUUID string) {
		n := &corev1.Node{}
		n.Name = orphan
		Expect(k8sClient.Create(ctx, n)).To(Succeed())

		n.Status.NodeInfo.SystemUUID = systemUUID
		Expect(k8sClient.Status().Update(ctx, n)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, n))).To(Succeed()) })
	}

	nodeGone := func() bool {
		err := k8sClient.Get(ctx, types.NamespacedName{Name: orphan}, &corev1.Node{})
		if apierrors.IsNotFound(err) {
			return true
		}

		Expect(err).NotTo(HaveOccurred())

		return false
	}

	It("wipes an orphan with the cluster's own credentials and deletes its Kubernetes Node", func() {
		kubeNode(uuid)

		key := request()
		reconcileOK(key)

		mr := get(key)
		Expect(mr.Status.Phase).To(Equal(ichorv1alpha1.MachineReclaimAwaitingConfirmation))
		Expect(mr.Status.Inspection.Refusals).To(BeEmpty())
		Expect(mr.Status.Inspection.Warnings).To(ContainElement(ContainSubstring("--add-machine")))
		Expect(dialed.Empty()).To(BeFalse(), "the cluster's own credential is what dials the node")

		confirm(key)
		reconcileOK(key)
		Expect(node.resets.Load()).To(Equal(int32(1)))

		reconcileOK(key)

		mr = get(key)
		Expect(mr.Status.Phase).To(Equal(ichorv1alpha1.MachineReclaimDone))
		Expect(mr.Status.Result).To(ContainSubstring("Kubernetes Node " + orphan + " was deleted"))
		Expect(mr.Status.Audit.CredentialDeletedAt).NotTo(BeNil())
		Expect(nodeGone()).To(BeTrue())
	})

	It("keeps a Kubernetes Node of the same name that is not the machine's", func() {
		kubeNode("somebody-else")

		key := request()
		reconcileOK(key)
		confirm(key)
		reconcileOK(key)
		reconcileOK(key)

		Expect(get(key).Status.Result).To(ContainSubstring("kubectl delete node " + orphan))
		Expect(nodeGone()).To(BeFalse())
	})

	It("refuses a machine that is still an approved TalosMachine and points to Remove node", func() {
		machine(true)

		key := request()
		reconcileOK(key)

		mr := get(key)
		Expect(mr.Status.Inspection.Refusals).To(ContainElement(And(ContainSubstring("Remove node"), Not(ContainSubstring("adopt")))))

		confirm(key)
		reconcileOK(key)
		Expect(node.resets.Load()).To(BeZero())
		Expect(get(key).Status.Phase).To(Equal(ichorv1alpha1.MachineReclaimFailed))
	})

	It("treats a pending TalosMachine of the UUID as no machine", func() {
		machine(false)

		key := request()
		reconcileOK(key)
		Expect(get(key).Status.Inspection.Refusals).To(BeEmpty())
	})

	It("refuses an orphan control plane whose removal would break etcd quorum", func() {
		node.in.ControlPlane, node.in.EtcdKnown = true, true
		node.in.EtcdMembers = []reclaim.EtcdMember{{ID: 1}, {ID: 2}}

		key := request()
		reconcileOK(key)
		Expect(get(key).Status.Inspection.Refusals).To(ContainElement(ContainSubstring("loses quorum")))

		confirm(key)
		reconcileOK(key)
		Expect(node.resets.Load()).To(BeZero())
	})

	It("refuses a request that asks for the cluster's credentials without naming the cluster", func() {
		mr := &ichorv1alpha1.MachineReclaim{
			Name: fmt.Sprintf("orphan-bad-%d", seq), Namespace: ns,
			Spec: ichorv1alpha1.MachineReclaimSpec{Address: testNodeAddr, ClusterCredentials: true},
		}
		Expect(k8sClient.Create(ctx, mr)).NotTo(Succeed())
	})
})
