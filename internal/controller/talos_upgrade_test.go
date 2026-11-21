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
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/storage"
)

// testBootIDBefore and testBootIDAfter are the two boot IDs every test
// below scripts into fakeTalosClient.bootID: before == what BootID
// returned right before the Upgrade API call, after == what it returns
// once (simulated) rebooted -- the only two values talosUpgradeAdvancer
// ever compares (goconst).
const (
	testBootIDBefore = "boot-1"
	testBootIDAfter  = "boot-2"

	// testOldTalosVersion is every test's "from" version: within 1.14's
	// MinimumHostUpgradeVersion (1.12.0), so upgrade.TalosPath
	// allows it unless a test is specifically about an unsupported path.
	testOldTalosVersion = "v1.12.0"
)

// newTestNode creates a Kubernetes Node named name with a Ready condition
// set to ready -- talosUpgradeAdvancer.nodeReady's own input.
func newTestNode(name string, ready bool) {
	status := corev1.ConditionTrue
	if !ready {
		status = corev1.ConditionFalse
	}

	n := &corev1.Node{}
	n.Name = name
	Expect(k8sClient.Create(ctx, n)).To(Succeed())

	n.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}}
	Expect(k8sClient.Status().Update(ctx, n)).To(Succeed())
}

// newUpgradeNode is a cluster with its secrets bundle and one control plane whose Kubernetes node
// (Ready or not) is named node-<suffix>.
func newUpgradeNode(suffix string, ready bool) (*ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine) {
	cluster := newTestCluster(newTestSecret(newTestBundle("v1.14")).Name, "v1.14")
	cp := newTestControlPlane(cluster.Name, suffix)
	setHostname(cp, "node-"+suffix)
	newTestNode("node-"+suffix, ready)

	return cluster, cp
}

// newUpgradeAdvancer wires fake as the one Talos API connection every
// machine dials (talosmachine_controller_test.go's clientFactory, reused
// as-is) and evacuator as the storage coordinator, nil meaning "no storage
// system configured".
func newUpgradeAdvancer(fake *fakeTalosClient, evacuator StorageEvacuator) *talosUpgradeAdvancer {
	return &talosUpgradeAdvancer{
		Client: k8sClient, ControllerNamespace: tmNamespace,
		TalosClient: clientFactory(fake),
		Evacuator:   evacuator,
	}
}

// fakeStorageEvacuator is a StorageEvacuator test double: Resolve always
// returns the scripted target (nil meaning "nothing to evacuate"), and
// Begin/End/ReplicasRemaining record how many times each was called.
type fakeStorageEvacuator struct {
	target *evacuationTarget
	// resolveDelay makes Resolve slow, standing in for a long evacuation.
	resolveDelay time.Duration

	remaining    int
	remainingErr error

	beginCalls int
	beginErr   error
	endCalls   int
	endErr     error
}

func (f *fakeStorageEvacuator) Resolve(context.Context, *ichorv1alpha1.TalosCluster, string) (*evacuationTarget, error) {
	time.Sleep(f.resolveDelay)

	return f.target, nil
}

func (f *fakeStorageEvacuator) Begin(context.Context, *evacuationTarget, string) error {
	f.beginCalls++

	return f.beginErr
}

func (f *fakeStorageEvacuator) ReplicasRemaining(context.Context, *evacuationTarget, string) (int, error) {
	return f.remaining, f.remainingErr
}

func (f *fakeStorageEvacuator) End(context.Context, *evacuationTarget, string) error {
	f.endCalls++

	return f.endErr
}

// testOldSchematic and testNewSchematic are the two Image Factory schematics the schematic tests switch between.
const (
	testOldSchematic = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testNewSchematic = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

var _ = Describe("talosUpgradeAdvancer schematic", func() {
	It("upgrades a machine whose version matches but whose running schematic differs, and finishes on the new one", func() {
		cluster, cp := newUpgradeNode("sc1", true)
		cluster.Spec.Schematic = &ichorv1alpha1.TalosClusterSchematic{ID: testNewSchematic}

		fake := &fakeTalosClient{version: cluster.Spec.TalosVersion, schematic: testOldSchematic, bootID: testBootIDBefore}
		adv := newUpgradeAdvancer(fake, nil)

		Expect(advanceOK(adv.Advance, cluster, fetchMachine(cp), nil)).To(BeFalse())
		Expect(fake.upgradeCalls).To(HaveLen(1))
		Expect(fake.upgradeCalls[0]).To(ContainSubstring(testNewSchematic + ":" + cluster.Spec.TalosVersion))

		fake.bootID, fake.schematic = testBootIDAfter, testNewSchematic
		fake.etcdMembers = []EtcdMember{{ID: 1, Hostname: "node-sc1"}}

		Expect(advanceOK(adv.Advance, cluster, fetchMachine(cp), nil)).To(BeTrue())
	})

	It("fails when the node reboots onto the right version but still the old schematic", func() {
		cluster, cp := newUpgradeNode("sc2", true)
		cluster.Spec.Schematic = &ichorv1alpha1.TalosClusterSchematic{ID: testNewSchematic}

		fake := &fakeTalosClient{version: cluster.Spec.TalosVersion, schematic: testOldSchematic, bootID: testBootIDBefore}
		adv := newUpgradeAdvancer(fake, nil)

		advanceOK(adv.Advance, cluster, fetchMachine(cp), nil)

		fake.bootID = testBootIDAfter

		_, err := adv.Advance(ctx, cluster, fetchMachine(cp), nil)
		Expect(err).To(MatchError(ContainSubstring("fell back")))
	})

	It("does nothing for a machine already on the target version and schematic", func() {
		cluster, cp := newUpgradeNode("sc3", true)
		cluster.Spec.Schematic = &ichorv1alpha1.TalosClusterSchematic{ID: testNewSchematic}

		fake := &fakeTalosClient{
			version: cluster.Spec.TalosVersion, schematic: testNewSchematic, bootID: testBootIDBefore,
			etcdMembers: []EtcdMember{{ID: 1, Hostname: "node-sc3"}},
		}

		Expect(advanceOK(newUpgradeAdvancer(fake, nil).Advance, cluster, fetchMachine(cp), nil)).To(BeTrue())
		Expect(fake.upgradeCalls).To(BeEmpty())
	})

	It("treats a node that booted no Image Factory asset as running the stock schematic", func() {
		cluster, cp := newUpgradeNode("sc4", true)

		fake := &fakeTalosClient{
			version: cluster.Spec.TalosVersion, bootID: testBootIDBefore,
			etcdMembers: []EtcdMember{{ID: 1, Hostname: "node-sc4"}},
		}

		Expect(advanceOK(newUpgradeAdvancer(fake, nil).Advance, cluster, fetchMachine(cp), nil)).To(BeTrue())
		Expect(fake.upgradeCalls).To(BeEmpty())
	})
})

var _ = Describe("talosUpgradeAdvancer", func() {
	It("finishes a control plane already on the target without a recorded hostname, using the name the node reports", func() {
		secret := newTestSecret(newTestBundle("v1.14"))
		cluster := newTestCluster(secret.Name, "v1.14")
		cp := newTestControlPlane(cluster.Name, "nh1")
		newTestNode("node-nh1", true)

		fake := &fakeTalosClient{
			version: cluster.Spec.TalosVersion, bootID: testBootIDBefore, nodeName: "node-nh1",
			etcdMembers: []EtcdMember{{ID: 1, Hostname: "node-nh1"}},
		}

		Expect(advanceOK(newUpgradeAdvancer(fake, nil).Advance, cluster, fetchMachine(cp), nil)).To(BeTrue())
		Expect(fake.upgradeCalls).To(BeEmpty())
	})

	It("upgrades a node without a recorded hostname once it names its Kubernetes node", func() {
		secret := newTestSecret(newTestBundle("v1.14"))
		cluster := newTestCluster(secret.Name, "v1.14")
		cp := newTestControlPlane(cluster.Name, "nh2")
		newTestNode("node-nh2", true)

		fake := &fakeTalosClient{version: testOldTalosVersion, bootID: testBootIDBefore, nodeName: "node-nh2"}

		advanceOK(newUpgradeAdvancer(fake, nil).Advance, cluster, fetchMachine(cp), nil)
		Expect(fake.upgradeCalls).To(HaveLen(1))
	})

	It("gives the Upgrade RPC far more time than the 15s dial bound, since it blocks while the node pulls the installer", func() {
		cluster, cp := newUpgradeNode("to1", true)

		fake := &fakeTalosClient{version: testOldTalosVersion, bootID: testBootIDBefore}
		adv := newUpgradeAdvancer(fake, nil)

		advanceOK(adv.Advance, cluster, fetchMachine(cp), nil)
		Expect(fake.upgradeCalls).To(HaveLen(1))
		Expect(fake.upgradeBudget).To(BeNumerically(">", 4*etcdDialTimeout),
			"the Upgrade RPC's context must not inherit the %s dial bound", etcdDialTimeout)
	})

	It("reads the boot ID on a fresh deadline after an evacuation that outlasts the dial bound", func() {
		DeferCleanup(func(old time.Duration) { etcdDialTimeout = old }, etcdDialTimeout)

		etcdDialTimeout = 200 * time.Millisecond

		cluster, cp := newUpgradeNode("slow1", true)

		fake := &fakeTalosClient{version: testOldTalosVersion, bootID: testBootIDBefore}
		adv := newUpgradeAdvancer(fake, &fakeStorageEvacuator{resolveDelay: 2 * etcdDialTimeout})

		advanceOK(adv.Advance, cluster, fetchMachine(cp), nil)
		Expect(fake.upgradeCalls).To(HaveLen(1))
	})

	It("fails (never succeeds) when the node reboots but is still running its previous version -- an A/B slot rollback", func() {
		cluster, cp := newUpgradeNode("ab1", true)

		fake := &fakeTalosClient{version: testOldTalosVersion, bootID: testBootIDBefore}
		adv := newUpgradeAdvancer(fake, nil)

		done := advanceOK(adv.Advance, cluster, fetchMachine(cp), nil)
		Expect(done).To(BeFalse())
		Expect(fake.upgradeCalls).To(HaveLen(1), "the Upgrade API call must have been made")

		// The node reboots (boot ID changes) but comes back on the same old
		// version: Talos's own A/B bootloader fell back.
		fake.bootID = testBootIDAfter

		done, err := adv.Advance(ctx, cluster, fetchMachine(cp), nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("fell back to its previous A/B slot"))
		Expect(done).To(BeFalse())
	})

	It("succeeds once the node reboots onto the target version, is Ready, and is back in etcd", func() {
		cluster, cp := newUpgradeNode("ok1", true)

		fake := &fakeTalosClient{version: testOldTalosVersion, bootID: testBootIDBefore}
		adv := newUpgradeAdvancer(fake, nil)

		Expect(advanceOK(adv.Advance, cluster, fetchMachine(cp), nil)).To(BeFalse())

		// The node reboots onto the target version and rejoins etcd.
		fake.bootID = testBootIDAfter
		fake.version = cluster.Spec.TalosVersion
		fake.etcdMembers = []EtcdMember{{ID: 1, Hostname: "node-ok1"}}

		Expect(advanceOK(adv.Advance, cluster, fetchMachine(cp), nil)).To(BeTrue())

		got := fetchMachine(cp)
		Expect(apimeta.IsStatusConditionTrue(got.Status.Conditions, conditionTalosUpgrade)).To(BeFalse(),
			"the in-flight marker must be cleared once convergence is confirmed")
	})

	It("waits for a node that has not become Ready or rejoined etcd yet after rebooting, without failing", func() {
		cluster, cp := newUpgradeNode("wait1", true)

		fake := &fakeTalosClient{version: testOldTalosVersion, bootID: testBootIDBefore}
		adv := newUpgradeAdvancer(fake, nil)

		advanceOK(adv.Advance, cluster, fetchMachine(cp), nil)

		// Rebooted onto the target version, but not an etcd member yet (still joining).
		fake.bootID = testBootIDAfter
		fake.version = cluster.Spec.TalosVersion
		fake.etcdMembers = nil

		Expect(advanceOK(adv.Advance, cluster, fetchMachine(cp), nil)).To(BeFalse())
	})

	It("fails a node that has not come back within talosUpgradeVerifyTimeout of its Upgrade call", func() {
		cluster, cp := newUpgradeNode("late1", true)

		fake := &fakeTalosClient{version: testOldTalosVersion, bootID: testBootIDBefore}
		adv := newUpgradeAdvancer(fake, nil)

		Expect(advanceOK(adv.Advance, cluster, fetchMachine(cp), nil)).To(BeFalse())

		// The Upgrade call was acknowledged long ago and the node has not rebooted since: the clock has run out.
		setAckedClock(fetchMachine(cp), conditionTalosUpgrade, time.Now().Add(-talosUpgradeVerifyTimeout-time.Minute))

		done, err := adv.Advance(ctx, cluster, fetchMachine(cp), nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("did not come back"))
		Expect(done).To(BeFalse())
	})

	It("refuses to start an upgrade on a node that is not Ready", func() {
		cluster, cp := newUpgradeNode("nr1", false)

		fake := &fakeTalosClient{version: testOldTalosVersion, bootID: testBootIDBefore}
		adv := newUpgradeAdvancer(fake, nil)

		done, err := adv.Advance(ctx, cluster, fetchMachine(cp), nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not Ready"))
		Expect(done).To(BeFalse())
		Expect(fake.upgradeCalls).To(BeEmpty(), "the Upgrade API must never be called against a not-Ready node")
	})

	It("refuses an unsupported Talos upgrade path without ever calling the Upgrade API", func() {
		cluster, cp := newUpgradeNode("path1", true)

		// v1.9.0 predates 1.14's MinimumHostUpgradeVersion (1.12.0): unsupported.
		fake := &fakeTalosClient{version: "v1.9.0", bootID: testBootIDBefore}
		adv := newUpgradeAdvancer(fake, nil)

		done, err := adv.Advance(ctx, cluster, fetchMachine(cp), nil)
		Expect(err).To(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(fake.upgradeCalls).To(BeEmpty())
	})

	It("pauses (never proceeds to the Upgrade API) once evacuation times out with replicas still remaining, and re-enables scheduling", func() {
		cluster, cp := newUpgradeNode("evac1", true)

		hook, err := storage.NewEvacuationHook(storage.LonghornV1)
		Expect(err).NotTo(HaveOccurred())

		evacuator := &fakeStorageEvacuator{
			target:    &evacuationTarget{hook: hook, timeout: time.Minute, namespace: defaultLonghornNamespace},
			remaining: 3,
		}

		fake := &fakeTalosClient{version: testOldTalosVersion, bootID: testBootIDBefore}
		adv := newUpgradeAdvancer(fake, evacuator)

		// First call: begins evacuation, does not yet proceed to the Upgrade API.
		done := advanceOK(adv.Advance, cluster, fetchMachine(cp), nil)
		Expect(done).To(BeFalse())
		Expect(evacuator.beginCalls).To(Equal(1))
		Expect(fake.upgradeCalls).To(BeEmpty(), "evacuation must finish before the Upgrade API is ever called")

		// Push the evacuation condition's own clock back past the timeout,
		// with replicas still remaining.
		got := fetchMachine(cp)
		cond := apimeta.FindStatusCondition(got.Status.Conditions, conditionStorageEvacuation)
		Expect(cond).NotTo(BeNil())
		cond.LastTransitionTime = metav1.NewTime(time.Now().Add(-2 * time.Minute))
		Expect(k8sClient.Status().Update(ctx, got)).To(Succeed())

		done, err = adv.Advance(ctx, cluster, fetchMachine(cp), nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("timed out"))
		Expect(done).To(BeFalse())
		Expect(evacuator.endCalls).To(Equal(1), "a paused evacuation must re-enable scheduling, not leave the node unschedulable indefinitely")
		Expect(fake.upgradeCalls).To(BeEmpty(), "a timed-out evacuation must pause the rollout, never proceed to the Upgrade API")
	})

	It("proceeds to the Upgrade API once evacuation reports done, without re-enabling scheduling yet", func() {
		cluster, cp := newUpgradeNode("evac2", true)

		hook, err := storage.NewEvacuationHook(storage.LonghornV1)
		Expect(err).NotTo(HaveOccurred())

		evacuator := &fakeStorageEvacuator{
			target: &evacuationTarget{hook: hook, timeout: time.Minute, namespace: defaultLonghornNamespace},
		}

		fake := &fakeTalosClient{version: testOldTalosVersion, bootID: testBootIDBefore}
		adv := newUpgradeAdvancer(fake, evacuator)

		advanceOK(adv.Advance, cluster, fetchMachine(cp), nil) // begins evacuation

		evacuator.remaining = 0 // the replica has finished moving off

		Expect(advanceOK(adv.Advance, cluster, fetchMachine(cp), nil)).To(BeFalse())
		Expect(fake.upgradeCalls).To(HaveLen(1))
		Expect(evacuator.endCalls).To(BeZero(),
			"scheduling must stay disabled until the drain-and-reboot is confirmed, not re-enabled the moment replicas finish moving")

		got := fetchMachine(cp)
		Expect(apimeta.IsStatusConditionTrue(got.Status.Conditions, conditionStorageEvacuation)).To(BeTrue())
	})
})

var _ = Describe("talosUpgradeAdvancer after a fallback", func() {
	It("calls the Upgrade API again when a fallen-back operation is resumed", func() {
		cluster, cp := newUpgradeNode("rt1", true)
		fake := &fakeTalosClient{
			version: testOldTalosVersion, bootID: testBootIDBefore, etcdMembers: []EtcdMember{{ID: 1, Hostname: "node-rt1"}},
		}
		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: newUpgradeAdvancer(fake, nil).Advance}
		op := newOpTestOperation(cluster.Name)

		mustReconcileOperation(r, op)
		Expect(fake.upgradeCalls).To(HaveLen(1))

		// The node boots its previous slot: same version, new boot ID.
		fake.bootID = testBootIDAfter

		mustReconcileOperation(r, op)
		Expect(fetchOperation(op).Status.FailureReason).To(ContainSubstring("fell back to its previous A/B slot"))

		// What `ichor resume operation` writes on a failed step.
		got := fetchOperation(op)
		got.Annotations = map[string]string{ichorv1alpha1.OperationRetryAnnotation: ichorv1alpha1.OperationRetryRequested}
		Expect(k8sClient.Update(ctx, got)).To(Succeed())

		mustReconcileOperation(r, op)
		Expect(fake.upgradeCalls).To(HaveLen(2), "a resumed fallback must issue the upgrade again")
		Expect(apimeta.IsStatusConditionTrue(fetchMachine(cp).Status.Conditions, conditionTalosUpgrade)).To(BeTrue(),
			"the retried upgrade must record its request")
	})

	It("names the schematic with both values when a fallback keeps the version", func() {
		cluster, cp := newUpgradeNode("msg1", true)
		cluster.Spec.Schematic = &ichorv1alpha1.TalosClusterSchematic{ID: testNewSchematic}

		fake := &fakeTalosClient{version: cluster.Spec.TalosVersion, schematic: testOldSchematic, bootID: testBootIDBefore}
		adv := newUpgradeAdvancer(fake, nil)

		advanceOK(adv.Advance, cluster, fetchMachine(cp), nil)

		fake.bootID = testBootIDAfter

		_, err := adv.Advance(ctx, cluster, fetchMachine(cp), nil)
		Expect(err).To(MatchError(ContainSubstring(
			"is now running schematic " + testOldSchematic + ", want " + testNewSchematic)))
	})

	It("names the version and the schematic when a fallback differs in both", func() {
		cluster, cp := newUpgradeNode("msg2", true)
		cluster.Spec.Schematic = &ichorv1alpha1.TalosClusterSchematic{ID: testNewSchematic}

		fake := &fakeTalosClient{version: testOldTalosVersion, schematic: testOldSchematic, bootID: testBootIDBefore}
		adv := newUpgradeAdvancer(fake, nil)

		advanceOK(adv.Advance, cluster, fetchMachine(cp), nil)

		fake.bootID = testBootIDAfter

		_, err := adv.Advance(ctx, cluster, fetchMachine(cp), nil)
		Expect(err).To(MatchError(ContainSubstring(
			"is now running " + testOldTalosVersion + " with schematic " + testOldSchematic + ", want " + testNewSchematic)))
	})
})

var _ = Describe("talosUpgradeAdvancer after a verify timeout", func() {
	It("calls the Upgrade API again when a timed-out operation is resumed", func() {
		cluster, cp := newUpgradeNode("vt1", true)
		fake := &fakeTalosClient{
			version: testOldTalosVersion, bootID: testBootIDBefore, etcdMembers: []EtcdMember{{ID: 1, Hostname: "node-vt1"}},
		}
		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: newUpgradeAdvancer(fake, nil).Advance}
		op := newOpTestOperation(cluster.Name)

		mustReconcileOperation(r, op)
		Expect(fake.upgradeCalls).To(HaveLen(1))

		// The node never reboots: the verify window runs out with the boot ID unchanged.
		setAckedClock(fetchMachine(cp), conditionTalosUpgrade, time.Now().Add(-talosUpgradeVerifyTimeout-time.Minute))

		mustReconcileOperation(r, op)
		Expect(fetchOperation(op).Status.FailureReason).To(ContainSubstring("did not come back"))

		// What `ichor resume operation` writes on a failed step.
		got := fetchOperation(op)
		got.Annotations = map[string]string{ichorv1alpha1.OperationRetryAnnotation: ichorv1alpha1.OperationRetryRequested}
		Expect(k8sClient.Update(ctx, got)).To(Succeed())

		mustReconcileOperation(r, op)
		Expect(fake.upgradeCalls).To(HaveLen(2), "a resumed timed-out upgrade must issue the upgrade again")
	})
})

// setUpgradeCondition writes the TalosUpgrade condition of cp, as a previous Advance pass would have left it.
func setUpgradeCondition(cp *ichorv1alpha1.TalosMachine, reason string) {
	GinkgoHelper()

	m := fetchMachine(cp)
	apimeta.SetStatusCondition(&m.Status.Conditions, metav1.Condition{
		Type: conditionTalosUpgrade, Status: metav1.ConditionFalse, Reason: reason, Message: "test",
	})
	Expect(k8sClient.Status().Update(ctx, m)).To(Succeed())
}

var _ = Describe("talosUpgradeAdvancer finish after a failed upgrade", func() {
	DescribeTable("a node verified on the target ends Converged, whichever failure reason it carried",
		func(suffix, reason string) {
			cluster, cp := newUpgradeNode(suffix, true)
			fake := &fakeTalosClient{
				version: cluster.Spec.TalosVersion, bootID: testBootIDAfter,
				etcdMembers: []EtcdMember{{ID: 1, Hostname: "node-" + suffix}},
			}
			adv := newUpgradeAdvancer(fake, nil)
			setUpgradeCondition(cp, reason)

			Expect(advanceOK(adv.Advance, cluster, fetchMachine(cp), nil)).To(BeTrue())
			Expect(fake.upgradeCalls).To(BeEmpty(), "a node already on the target is not upgraded again")

			got := apimeta.FindStatusCondition(fetchMachine(cp).Status.Conditions, conditionTalosUpgrade)
			Expect(got).NotTo(BeNil())
			Expect(got.Status).To(Equal(metav1.ConditionFalse))
			Expect(got.Reason).To(Equal(reasonUpgradeConverged), "a verified node must not keep its failure reason")
		},
		Entry("after a fallback", "fin1", reasonUpgradeFellBack),
		Entry("after a verify timeout", "fin2", reasonUpgradeVerifyTimedOut),
	)

	DescribeTable("a node on the target that is not Ready yet keeps its failure reason",
		func(suffix, reason string) {
			cluster, cp := newUpgradeNode(suffix, false)
			fake := &fakeTalosClient{
				version: cluster.Spec.TalosVersion, bootID: testBootIDAfter,
				etcdMembers: []EtcdMember{{ID: 1, Hostname: "node-" + suffix}},
			}
			adv := newUpgradeAdvancer(fake, nil)
			setUpgradeCondition(cp, reason)

			Expect(advanceOK(adv.Advance, cluster, fetchMachine(cp), nil)).To(BeFalse())

			got := apimeta.FindStatusCondition(fetchMachine(cp).Status.Conditions, conditionTalosUpgrade)
			Expect(got).NotTo(BeNil())
			Expect(got.Reason).To(Equal(reason), "a node not yet verified is not converged")
		},
		Entry("after a fallback", "fin3", reasonUpgradeFellBack),
		Entry("after a verify timeout", "fin4", reasonUpgradeVerifyTimedOut),
	)

	DescribeTable("a node whose name is unreadable keeps waiting while it carries a failure reason",
		func(suffix, reason string) {
			cluster, cp := newUpgradeNode(suffix, true)
			m := fetchMachine(cp)
			m.Spec.Hostname = ""
			Expect(k8sClient.Update(ctx, m)).To(Succeed())

			fake := &fakeTalosClient{
				version: cluster.Spec.TalosVersion, bootID: testBootIDAfter, nodeNameErr: errors.New("not reporting yet"),
			}
			adv := newUpgradeAdvancer(fake, nil)
			setUpgradeCondition(cp, reason)

			Expect(advanceOK(adv.Advance, cluster, fetchMachine(cp), nil)).To(BeFalse())
		},
		Entry("after a fallback", "fin5", reasonUpgradeFellBack),
		Entry("after a verify timeout", "fin6", reasonUpgradeVerifyTimedOut),
	)

	DescribeTable("a wait on a node carrying a failure reason has its own clock, and a resume restarts it",
		func(suffix string, ready bool, nameErr error, members []EtcdMember) {
			cluster, cp := newUpgradeNode(suffix, ready)
			if nameErr != nil {
				m := fetchMachine(cp)
				m.Spec.Hostname = ""
				Expect(k8sClient.Update(ctx, m)).To(Succeed())
			}

			fake := &fakeTalosClient{
				version: cluster.Spec.TalosVersion, bootID: testBootIDAfter, nodeNameErr: nameErr, etcdMembers: members,
			}
			adv := newUpgradeAdvancer(fake, nil)
			setUpgradeCondition(cp, reasonUpgradeFellBack)
			op := &ichorv1alpha1.TalosOperation{Spec: ichorv1alpha1.TalosOperationSpec{Kind: ichorv1alpha1.TalosOperationKindTalos}}

			// The failure's own transition is old: the clock must not be read from it.
			m := fetchMachine(cp)
			apimeta.FindStatusCondition(m.Status.Conditions, conditionTalosUpgrade).LastTransitionTime =
				metav1.NewTime(time.Now().Add(-2 * talosUpgradeVerifyTimeout))
			Expect(k8sClient.Status().Update(ctx, m)).To(Succeed())

			Expect(advanceOK(adv.Advance, cluster, fetchMachine(cp), op)).To(BeFalse())
			Expect(stepRenewUntil(op, fetchMachine(cp))).To(BeTemporally("<", time.Now().Add(talosUpgradeVerifyTimeout+time.Minute)))

			m = fetchMachine(cp)
			waiting := apimeta.FindStatusCondition(m.Status.Conditions, conditionTalosUpgradeWaiting)
			Expect(waiting).NotTo(BeNil())
			Expect(apimeta.FindStatusCondition(m.Status.Conditions, conditionTalosUpgrade).Reason).To(Equal(reasonUpgradeFellBack))

			waiting.LastTransitionTime = metav1.NewTime(time.Now().Add(-2 * talosUpgradeVerifyTimeout))
			Expect(k8sClient.Status().Update(ctx, m)).To(Succeed())
			Expect(stepRenewUntil(op, fetchMachine(cp))).To(BeTemporally("<", time.Now()), "the lock is no longer renewed")

			done, err := adv.Advance(ctx, cluster, fetchMachine(cp), op)
			Expect(err).To(HaveOccurred())
			Expect(done).To(BeFalse())

			got := fetchMachine(cp)
			cond := apimeta.FindStatusCondition(got.Status.Conditions, conditionTalosUpgrade)
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(reasonUpgradeVerifyTimedOut))
			Expect(apimeta.FindStatusCondition(got.Status.Conditions, conditionTalosUpgradeWaiting)).To(BeNil())

			// A resumed operation waits on a fresh clock instead of failing at once.
			Expect(advanceOK(adv.Advance, cluster, fetchMachine(cp), op)).To(BeFalse())
			Expect(apimeta.FindStatusCondition(fetchMachine(cp).Status.Conditions, conditionTalosUpgradeWaiting)).NotTo(BeNil())
		},
		Entry("an unreadable node name", "fin7", true, errors.New("not reporting yet"), nil),
		Entry("a node that is not Ready", "fin8", false, nil, []EtcdMember{{ID: 1, Hostname: "node-fin8"}}),
		Entry("a control plane missing from etcd", "fin9", true, nil, nil),
	)
})
