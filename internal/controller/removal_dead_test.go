package controller

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// ADR 0106: a control plane whose etcd member is dead may be removed below the declared count, so its
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

		for _, cp := range []*ichorv1alpha1.TalosMachine{f.cp1, f.cp2, f.cp3} {
			reconcileOK(f.r, cp)
		}

		return f
	}

	It("removes it when the node cannot be dialed, and removes its etcd member through a healthy sibling", func() {
		f := setup("d1")

		Expect(k8sClient.Delete(ctx, f.cp1)).To(Succeed())
		reconcileOK(f.r, f.cp1)

		Expect(f.survivor.etcdRemoveIDs).To(Equal([]uint64{42}))
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, machineKeyOf(f.cp1), &ichorv1alpha1.TalosMachine{}))).To(BeTrue(),
			"3 -> 2 is below the declared 3, but the member is dead")
	})

	It("removes it when the node dials but its etcd answers nothing", func() {
		f := setup("d2")
		f.departing[f.cp1.Name] = &fakeTalosClient{etcdStatusErr: errors.New("connection refused")}

		Expect(k8sClient.Delete(ctx, f.cp1)).To(Succeed())
		reconcileOK(f.r, f.cp1)

		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, machineKeyOf(f.cp1), &ichorv1alpha1.TalosMachine{}))).To(BeTrue())
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
})
