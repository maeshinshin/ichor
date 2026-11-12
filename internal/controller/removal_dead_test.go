package controller

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// A control plane whose etcd member is dead may be removed below the declared count, so its
// replacement's etcd can join; a healthy one may not.
var _ = Describe("removing a dead control plane below the declared count", func() {
	type fixture struct {
		r             *TalosMachineReconciler
		cp1, cp2, cp3 *ichorv1alpha1.TalosMachine
		survivor      *fakeTalosClient
		departing     map[string]*fakeTalosClient
	}

	setup := func(suffix string) *fixture {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestClusterWithCount(3, secret.Name)
		f := &fixture{
			cp1: newTestControlPlane(cluster.Name, suffix+"1"),
			cp2: newTestControlPlane(cluster.Name, suffix+"2"),
			cp3: newTestControlPlane(cluster.Name, suffix+"3"),
		}
		setHostname(f.cp1, "node-"+suffix+"1")

		f.survivor = &fakeTalosClient{etcdMembers: []EtcdMember{{ID: 42, Hostname: "node-" + suffix + "1"}}}
		f.departing = map[string]*fakeTalosClient{f.cp2.Name: f.survivor, f.cp3.Name: f.survivor}
		f.r = &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: perMachineClientFactory(f.departing),
		}

		reconcileOK(f.r, f.cp1, f.cp2, f.cp3)

		return f
	}

	It("removes it when the node cannot be dialed, and removes its etcd member through a healthy sibling", func() {
		f := setup("d1")

		Expect(k8sClient.Delete(ctx, f.cp1)).To(Succeed())
		reconcileOK(f.r, f.cp1)

		Expect(f.survivor.etcdRemoveIDs).To(Equal([]uint64{42}))
		Expect(machineGone(f.cp1)).To(BeTrue(),
			"3 -> 2 is below the declared 3, but the member is dead")
	})

	It("removes it when the node dials but its etcd answers nothing", func() {
		f := setup("d2")
		f.departing[f.cp1.Name] = &fakeTalosClient{etcdStatusErr: errors.New("connection refused")}

		Expect(k8sClient.Delete(ctx, f.cp1)).To(Succeed())
		reconcileOK(f.r, f.cp1)

		Expect(machineGone(f.cp1)).To(BeTrue())
	})

	It("still refuses a healthy member below the declared count, saying so", func() {
		f := setup("d3")
		f.departing[f.cp1.Name] = &fakeTalosClient{etcdStatus: EtcdStatus{MemberID: 42, LeaderID: 7}}

		Expect(k8sClient.Delete(ctx, f.cp1)).To(Succeed())
		reconcileOK(f.r, f.cp1)

		cond := expectRemoval(fetchMachine(f.cp1), reasonRemovalRefused)
		Expect(cond.Message).To(ContainSubstring("declared count"))
		Expect(cond.Message).To(ContainSubstring("healthy"))
		Expect(f.survivor.etcdRemoveIDs).To(BeEmpty())
	})

	It("still refuses when no healthy control plane can say whether the member is dead", func() {
		f := setup("d4")
		delete(f.departing, f.cp2.Name)
		delete(f.departing, f.cp3.Name)

		Expect(k8sClient.Delete(ctx, f.cp1)).To(Succeed())
		reconcileOK(f.r, f.cp1)

		expectRemoval(fetchMachine(f.cp1), reasonRemovalRefused)
	})

	It("never lets quorum be lost: a two control plane cluster refuses even a dead member", func() {
		f := setup("d5")
		Expect(k8sClient.Delete(ctx, f.cp3)).To(Succeed())
		f.cp3 = fetchMachine(f.cp3)
		// cp3 is now being deleted and not counted; 2 approved remain, one of them dead.
		Expect(k8sClient.Delete(ctx, f.cp1)).To(Succeed())
		reconcileOK(f.r, f.cp1)

		cond := expectRemoval(fetchMachine(f.cp1), reasonRemovalRefused)
		Expect(cond.Message).To(ContainSubstring("quorum"))
	})

	// resetDead starts the removal of a dead control plane that asks for a reset and runs it until it is
	// past the etcd step: the Node of the machine stays Ready, so the removal waits on it.
	resetDead := func(f *fixture, host string, self *fakeTalosClient) {
		newTestNode(host, true)
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, &corev1.Node{Name: host}) })

		f.departing[f.cp1.Name] = self

		got := fetchMachine(f.cp1)
		got.Annotations = map[string]string{resetOnDeleteAnnotation: resetOnDeleteValue}
		Expect(k8sClient.Update(ctx, got)).To(Succeed())
		Expect(k8sClient.Delete(ctx, got)).To(Succeed())

		reconcileOK(f.r, f.cp1)
		Expect(machineGone(f.cp1)).To(BeFalse())
	}

	// etcdForgets is the survivor's view once the dead member has been removed from etcd.
	etcdForgets := func(f *fixture) { f.survivor.etcdMembers = nil }

	It("carries on to the end after the reset was accepted, though the member is gone from etcd and the count is below the declared one", func() {
		f := setup("d6")
		self := &fakeTalosClient{etcdStatusErr: errors.New("connection refused")}
		resetDead(f, "node-d61", self)
		expectRemoval(fetchMachine(f.cp1), reasonResetRequested)

		etcdForgets(f)

		var n corev1.Node
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "node-d61"}, &n)).To(Succeed())
		n.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}}
		Expect(k8sClient.Status().Update(ctx, &n)).To(Succeed())

		reconcileOK(f.r, f.cp1)

		Expect(self.resetCalls).To(Equal(1), "the wipe is not requested again")
		Expect(machineGone(f.cp1)).To(BeTrue(), "the finalizer must not outlive the wiped node")
	})

	It("tries the reset again after it failed, though the member is gone from etcd and the count is below the declared one", func() {
		f := setup("d7")
		self := &fakeTalosClient{etcdStatusErr: errors.New("connection refused"), resetErr: errors.New("reset refused")}
		resetDead(f, "node-d71", self)
		expectRemoval(fetchMachine(f.cp1), reasonResetFailed)

		etcdForgets(f)

		self.resetErr = nil
		reconcileOK(f.r, f.cp1)

		Expect(self.resetCalls).To(Equal(2))
		expectRemoval(fetchMachine(f.cp1), reasonResetRequested)
	})

	It("still refuses a member that is gone from etcd when no round of this removal ever went on for it", func() {
		f := setup("d8")
		etcdForgets(f)

		Expect(k8sClient.Delete(ctx, f.cp1)).To(Succeed())
		reconcileOK(f.r, f.cp1)

		expectRemoval(fetchMachine(f.cp1), reasonRemovalRefused)
		Expect(machineGone(f.cp1)).To(BeFalse())
	})
})
