package controller

import (
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apimeta "k8s.io/apimachinery/pkg/api/meta"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// The removal of a control plane's etcd member asks the other control planes in name order until one
// answers, so an unreachable first sibling does not stall it.
var _ = Describe("removing an etcd member when the first sibling is unreachable", func() {
	// removeFourth creates n control planes under a cluster declaring count, sorts them by name, gives
	// the clients by sorted index (a missing index cannot be dialed), deletes the fourth and reconciles it.
	removeFourth := func(n int, count int32, host string, clients map[int]*fakeTalosClient) *ichorv1alpha1.TalosMachine {
		cluster := newTestClusterWithCount(count, newTestSecret(newTestBundle("v1.14")).Name)

		cps := make([]*ichorv1alpha1.TalosMachine, n)
		for i := range cps {
			cps[i] = newTestControlPlane(cluster.Name, string(rune('1'+i)))
		}

		slices.SortFunc(cps, func(a, b *ichorv1alpha1.TalosMachine) int { return strings.Compare(a.Name, b.Name) })
		setHostname(cps[3], host)

		byName := map[string]*fakeTalosClient{}
		for i, c := range clients {
			byName[cps[i].Name] = c
		}

		r := &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: perMachineClientFactory(byName),
		}
		reconcileEach(r, cps)

		Expect(k8sClient.Delete(ctx, cps[3])).To(Succeed())
		reconcileOK(r, cps[3])

		return cps[3]
	}

	It("removes the member through the next sibling that answers", func() {
		survivor := &fakeTalosClient{etcdMembers: []EtcdMember{{ID: 4, Hostname: "node-s4"}}}

		cp := removeFourth(4, 3, "node-s4", map[int]*fakeTalosClient{1: survivor, 2: survivor})

		Expect(survivor.etcdRemoveIDs).To(Equal([]uint64{4}))
		Expect(machineGone(cp)).To(BeTrue())
	})

	It("removes a dead member below the declared count when the first siblings are down", func() {
		survivor := &fakeTalosClient{etcdMembers: []EtcdMember{{ID: 4, Hostname: "node-s5"}}}

		cp := removeFourth(5, 5, "node-s5", map[int]*fakeTalosClient{2: survivor, 4: survivor})

		Expect(survivor.etcdRemoveIDs).To(Equal([]uint64{4}))
		Expect(machineGone(cp)).To(BeTrue())
	})

	It("names the siblings it tried when none answers", func() {
		cp := removeFourth(4, 3, "node-s6", nil)

		cond := apimeta.FindStatusCondition(fetchMachine(cp).Status.Conditions, conditionRemoval)
		Expect(cond.Reason).To(Equal(reasonEtcdMemberRemoveFail))
		Expect(cond.Message).To(ContainSubstring("none of the other control planes answered"))
	})
})
