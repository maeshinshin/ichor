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
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

func newRolloutAdvancer(fake *fakeTalosClient) *configRolloutAdvancer {
	return &configRolloutAdvancer{Client: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}
}

// rolloutFixture is a cluster whose patch needs a reboot (machine.network.interfaces is uncovered by
// pkg/fieldmeta, so it defaults to reboot-required, on a control plane too) and a node still on the unpatched config.
type rolloutFixture struct {
	cluster *ichorv1alpha1.TalosCluster
	bundle  *secrets.Bundle
	role    string
	desired *ichorv1alpha1.TalosConfigPatch
}

func newRolloutFixture() *rolloutFixture { return newRolloutFixtureFor(roleWorker) }

// newRolloutFixtureFor builds the fixture for machines of role: the patch carries the machine type,
// and Talos refuses a worker-typed document on a control plane.
func newRolloutFixtureFor(role string) *rolloutFixture {
	bundle := newTestBundle("v1.14")
	secret := newTestSecret(bundle)
	cluster := newTestCluster(secret.Name, "v1.14")

	dhcp := true
	cfg := skeletonFor(role)
	cfg.MachineConfig.MachineNetwork = &typedconfig.NetworkConfig{
		NetworkInterfaces: []*typedconfig.Device{{DeviceInterface: "eth0", DeviceDHCP: &dhcp}},
	}

	return &rolloutFixture{cluster: cluster, bundle: bundle, role: role, desired: newTestPatch(cluster.Name, layerCluster, nil, cfg)}
}

func skeletonFor(role string) *typedconfig.Config {
	cfg := minimalConfigSkeleton()
	cfg.MachineConfig.MachineType = role

	return cfg
}

// before is what the node runs now: the cluster rendered without the patch.
func (f *rolloutFixture) before(m *ichorv1alpha1.TalosMachine) []byte {
	return renderWithConfig(f.cluster, m, f.bundle, layerCluster, nil, skeletonFor(f.role))
}

// after is what the node runs once the rollout worked: the rendered configuration.
func (f *rolloutFixture) after(m *ichorv1alpha1.TalosMachine) []byte {
	out, _, err := render.Render(f.cluster, m, f.bundle, []ichorv1alpha1.TalosConfigPatch{*f.desired}, nil)
	Expect(err).NotTo(HaveOccurred())

	return out
}

// newRolloutNode is the fixture plus one worker whose Kubernetes node (Ready or not) is named hostname.
func newRolloutNode(hostname string, ready bool) (*rolloutFixture, *ichorv1alpha1.TalosMachine) {
	f := newRolloutFixture()
	m := newTestMachine(f.cluster.Name, true)
	setHostname(m, hostname)
	newTestNode(hostname, ready)

	return f, m
}

// reconcileTimes runs n reconcile passes of op, each of which must succeed.
func reconcileTimes(r *TalosOperationReconciler, op *ichorv1alpha1.TalosOperation, n int) {
	GinkgoHelper()

	for range n {
		_, err := reconcileOperation(r, op)
		Expect(err).NotTo(HaveOccurred())
	}
}

var _ = Describe("configRolloutAdvancer", func() {
	It("is done for a machine that needs nothing even when no hostname is recorded", func() {
		f := newRolloutFixture()
		m := newTestMachine(f.cluster.Name, true)
		Expect(m.Spec.Hostname).To(BeEmpty())

		fake := &fakeTalosClient{current: f.after(m), bootID: testBootIDBefore}

		done, err := newRolloutAdvancer(fake).Advance(ctx, f.cluster, fetchMachine(m), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
		Expect(fake.rebootCalls).To(BeZero())
	})

	It("rolls out to a machine without a recorded hostname using the Kubernetes node name the node reports", func() {
		f := newRolloutFixture()
		m := newTestMachine(f.cluster.Name, true)
		newTestNode("node-cr-reported", true)

		fake := &fakeTalosClient{current: f.before(m), bootID: testBootIDBefore, nodeName: "node-cr-reported"}
		adv := newRolloutAdvancer(fake)

		done, err := adv.Advance(ctx, f.cluster, fetchMachine(m), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(fake.rebootCalls).To(Equal(1))

		fake.bootID, fake.current = testBootIDAfter, f.after(m)

		done, err = adv.Advance(ctx, f.cluster, fetchMachine(m), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
	})

	It("does nothing to a machine that already runs the rendered configuration", func() {
		f, m := newRolloutNode("node-cr-noop", true)

		fake := &fakeTalosClient{current: f.after(m), bootID: testBootIDBefore}

		done, err := newRolloutAdvancer(fake).Advance(ctx, f.cluster, fetchMachine(m), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
		Expect(fake.stagedCalls).To(BeEmpty())
		Expect(fake.applyCalls).To(BeEmpty())
		Expect(fake.rebootCalls).To(BeZero(), "no pending change means no reboot")
	})

	It("stages the rendered configuration, reboots once, and finishes when the node is back on it", func() {
		f, m := newRolloutNode("node-cr-ok", true)

		fake := &fakeTalosClient{current: f.before(m), bootID: testBootIDBefore}
		adv := newRolloutAdvancer(fake)

		done, err := adv.Advance(ctx, f.cluster, fetchMachine(m), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(fake.dryRunCalls).To(Equal(1), "dry run first, always")
		Expect(fake.stagedCalls).To(HaveLen(1))
		Expect(fake.applyCalls).To(BeEmpty(), "never NO_REBOOT for a reboot-requiring change")
		Expect(fake.rebootCalls).To(Equal(1))
		Expect(fake.stagedBudget).To(BeNumerically(">", 4*etcdDialTimeout), "the staged apply gets its own deadline")
		Expect(fake.rebootBudget).To(BeNumerically(">", 4*etcdDialTimeout), "so does the reboot")

		cond := apimeta.FindStatusCondition(fetchMachine(m).Status.Conditions, conditionConfigRollout)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Message).To(Equal(testBootIDBefore))

		// Not rebooted yet (same boot ID): wait, and above all do not reboot again.
		done, err = adv.Advance(ctx, f.cluster, fetchMachine(m), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(fake.rebootCalls).To(Equal(1))

		// Rebooted onto the rendered configuration.
		fake.bootID = testBootIDAfter
		fake.current = f.after(m)

		done, err = adv.Advance(ctx, f.cluster, fetchMachine(m), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
		Expect(fake.rebootCalls).To(Equal(1))
		Expect(apimeta.IsStatusConditionTrue(fetchMachine(m).Status.Conditions, conditionConfigRollout)).To(BeFalse())
	})

	It("applies a pending change that needs no reboot without rebooting", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)
		setHostname(m, "node-cr-safe")
		newTestNode("node-cr-safe", true)
		newTestPatch(cluster.Name, layerCluster, nil, extraArgsConfig())

		fake := &fakeTalosClient{current: renderWithConfig(cluster, m, bundle, layerCluster, nil, minimalConfigSkeleton())}

		_, err := newRolloutAdvancer(fake).Advance(ctx, cluster, fetchMachine(m), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.applyCalls).To(HaveLen(1))
		Expect(fake.stagedCalls).To(BeEmpty())
		Expect(fake.rebootCalls).To(BeZero())
	})

	It("reads the boot ID on a fresh deadline after a step that outlasts the dial bound (a slow drain)", func() {
		DeferCleanup(func(old time.Duration) { etcdDialTimeout = old }, etcdDialTimeout)

		etcdDialTimeout = 200 * time.Millisecond

		f, m := newRolloutNode("node-cr-slowdrain", true)

		fake := &fakeTalosClient{current: f.before(m), bootID: testBootIDBefore}
		fake.dryRunHook = func() { time.Sleep(2 * etcdDialTimeout) } // stands in for the evacuation and drain

		done, err := newRolloutAdvancer(fake).Advance(ctx, f.cluster, fetchMachine(m), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(fake.rebootCalls).To(Equal(1))
	})

	It("refuses to reboot a node that is not Ready", func() {
		f, m := newRolloutNode("node-cr-nr", false)

		fake := &fakeTalosClient{current: f.before(m), bootID: testBootIDBefore}

		done, err := newRolloutAdvancer(fake).Advance(ctx, f.cluster, fetchMachine(m), nil)
		Expect(err).To(MatchError(ContainSubstring("not Ready")))
		Expect(done).To(BeFalse())
		Expect(fake.stagedCalls).To(BeEmpty())
		Expect(fake.rebootCalls).To(BeZero())
	})

	It("refuses to reboot a control plane while another control plane is missing from etcd", func() {
		f := newRolloutFixtureFor(roleControlPlane)
		cp := newTestControlPlane(f.cluster.Name, "41")
		other := newTestControlPlane(f.cluster.Name, "42")
		setHostname(cp, "node-cr-cp1")
		setHostname(other, "node-cr-cp2")
		newTestNode("node-cr-cp1", true)

		fake := &fakeTalosClient{
			current: f.before(cp), bootID: testBootIDBefore,
			etcdMembers: []EtcdMember{{ID: 1, Hostname: "node-cr-cp1"}}, // cp2 is not a member
		}

		done, err := newRolloutAdvancer(fake).Advance(ctx, f.cluster, fetchMachine(cp), nil)
		Expect(err).To(MatchError(ContainSubstring("node-cr-cp2")))
		Expect(done).To(BeFalse())
		Expect(fake.stagedCalls).To(BeEmpty())
		Expect(fake.rebootCalls).To(BeZero(), "the etcd check precedes the reboot")

		fake.etcdMembers = append(fake.etcdMembers, EtcdMember{ID: 2, Hostname: "node-cr-cp2"})

		_, err = newRolloutAdvancer(fake).Advance(ctx, f.cluster, fetchMachine(cp), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.rebootCalls).To(Equal(1))
	})

	It("waits for a control plane to be back in etcd after its reboot", func() {
		f := newRolloutFixtureFor(roleControlPlane)
		cp := newTestControlPlane(f.cluster.Name, "43")
		setHostname(cp, "node-cr-cp3")
		newTestNode("node-cr-cp3", true)

		fake := &fakeTalosClient{
			current: f.before(cp), bootID: testBootIDBefore,
			etcdMembers: []EtcdMember{{ID: 1, Hostname: "node-cr-cp3"}},
		}
		adv := newRolloutAdvancer(fake)

		_, err := adv.Advance(ctx, f.cluster, fetchMachine(cp), nil)
		Expect(err).NotTo(HaveOccurred())

		fake.bootID, fake.current, fake.etcdMembers = testBootIDAfter, f.after(cp), nil

		done, err := adv.Advance(ctx, f.cluster, fetchMachine(cp), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse(), "not an etcd member again yet")

		fake.etcdMembers = []EtcdMember{{ID: 1, Hostname: "node-cr-cp3"}}

		done, err = adv.Advance(ctx, f.cluster, fetchMachine(cp), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
	})

	It("fails, and clears the marker, when the reboot call fails", func() {
		f, m := newRolloutNode("node-cr-rf", true)

		fake := &fakeTalosClient{current: f.before(m), bootID: testBootIDBefore, rebootErr: errors.New("node busy")}

		done, err := newRolloutAdvancer(fake).Advance(ctx, f.cluster, fetchMachine(m), nil)
		Expect(err).To(MatchError(ContainSubstring("reboot API call")))
		Expect(done).To(BeFalse())
		Expect(apimeta.IsStatusConditionTrue(fetchMachine(m).Status.Conditions, conditionConfigRollout)).To(BeFalse())
	})

	It("fails when the node rebooted but does not run the rendered configuration", func() {
		f, m := newRolloutNode("node-cr-bad", true)

		fake := &fakeTalosClient{current: f.before(m), bootID: testBootIDBefore}
		adv := newRolloutAdvancer(fake)

		_, err := adv.Advance(ctx, f.cluster, fetchMachine(m), nil)
		Expect(err).NotTo(HaveOccurred())

		fake.bootID = testBootIDAfter // rebooted, but current still the old configuration

		done, err := adv.Advance(ctx, f.cluster, fetchMachine(m), nil)
		Expect(err).To(MatchError(ContainSubstring("not running the rendered configuration")))
		Expect(done).To(BeFalse())
	})

	It("fails once the node has not come back within the verification window", func() {
		f, m := newRolloutNode("node-cr-slow", true)

		fake := &fakeTalosClient{current: f.before(m), bootID: testBootIDBefore}
		adv := newRolloutAdvancer(fake)

		_, err := adv.Advance(ctx, f.cluster, fetchMachine(m), nil)
		Expect(err).NotTo(HaveOccurred())

		// The marker is old: the node never came back.
		got := fetchMachine(m)
		cond := apimeta.FindStatusCondition(got.Status.Conditions, conditionConfigRollout)
		cond.LastTransitionTime = metav1.NewTime(time.Now().Add(-2 * configRolloutVerifyTimeout))
		Expect(k8sClient.Status().Update(ctx, got)).To(Succeed())

		fake.currentErr = errors.New("connection refused")

		done, err := adv.Advance(ctx, f.cluster, fetchMachine(m), nil)
		Expect(err).To(MatchError(ContainSubstring("did not come back")))
		Expect(done).To(BeFalse())
	})
})

var _ = Describe("a config operation on the backbone", func() {
	pend := func(ms ...*ichorv1alpha1.TalosMachine) {
		for _, m := range ms {
			m.Status.RebootPaths = []string{"machine.network"}
			Expect(k8sClient.Status().Update(ctx, m)).To(Succeed())
		}
	}

	It("lists only the machines with a pending change", func() {
		cluster := newOpTestCluster()
		cp1 := newOpTestMachine(cluster.Name, "controlplane", "cfgp-cp1")
		cp2 := newOpTestMachine(cluster.Name, "controlplane", "cfgp-cp2")
		cp3 := newOpTestMachine(cluster.Name, "controlplane", "cfgp-cp3")
		setUpgradeOrder(cp1, "1")
		setUpgradeOrder(cp2, "2")
		setUpgradeOrder(cp3, "3")
		cp2.Status.APIServerRestartPaths = []string{"cluster.apiServer"}
		Expect(k8sClient.Status().Update(ctx, cp2)).To(Succeed())

		op := newOpTestOperationOfKind(cluster.Name, ichorv1alpha1.TalosOperationKindConfig)
		fake := &fakeAdvancer{outcomes: map[string]advanceOutcome{}}
		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: fake.Advance}

		reconcileTimes(r, op, 4)

		got := fetchOperation(op)
		Expect(got.Status.Machines).To(HaveLen(1))
		Expect(got.Status.Machines[0].Name).To(Equal(cp2.Name))
		Expect(fake.calls).To(Equal([]string{cp2.Name}))
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
	})

	It("completes with no machines when none has a pending change", func() {
		cluster := newOpTestCluster()
		newOpTestMachine(cluster.Name, "controlplane", "cfgn-cp1")
		newOpTestMachine(cluster.Name, "controlplane", "cfgn-cp2")
		newOpTestMachine(cluster.Name, "controlplane", "cfgn-cp3")

		op := newOpTestOperationOfKind(cluster.Name, ichorv1alpha1.TalosOperationKindConfig)
		fake := &fakeAdvancer{outcomes: map[string]advanceOutcome{}}
		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: fake.Advance}

		reconcileTimes(r, op, 3)

		got := fetchOperation(op)
		Expect(got.Status.Machines).To(BeEmpty())
		Expect(fake.calls).To(BeEmpty())
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
	})

	It("takes the controller's own node last, after the control planes and the workers, and skips a held machine", func() {
		cluster := newOpTestCluster()
		cp1 := newOpTestMachine(cluster.Name, "controlplane", "cfg-cp1")
		cp2 := newOpTestMachine(cluster.Name, "controlplane", "cfg-cp2")
		cp3 := newOpTestMachine(cluster.Name, "controlplane", "cfg-cp3")
		w1 := newOpTestMachine(cluster.Name, "worker", "cfg-w1")
		held := newOpTestMachine(cluster.Name, "worker", "cfg-held")

		// cp1 sorts first by label, but runs the controller.
		setUpgradeOrder(cp1, "1")
		setUpgradeOrder(cp2, "2")
		setUpgradeOrder(cp3, "3")
		setHostname(cp1, "node-cfg-self")
		pend(cp1, cp2, cp3, w1)

		held.Annotations = map[string]string{ichorv1alpha1.TalosMachineUpgradeHoldAnnotation: resetOnDeleteValue}
		Expect(k8sClient.Update(ctx, held)).To(Succeed())

		op := newOpTestOperationOfKind(cluster.Name, ichorv1alpha1.TalosOperationKindConfig)
		fake := &fakeAdvancer{outcomes: map[string]advanceOutcome{}}
		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: fake.Advance, NodeName: "node-cfg-self"}

		reconcileTimes(r, op, 5)

		Expect(fake.calls).To(Equal([]string{cp2.Name, cp3.Name, w1.Name, cp1.Name}),
			"one control plane at a time, then workers, then the node the controller runs on")
		Expect(fetchOperation(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
	})

	It("pauses on a failed machine and advances nothing after it", func() {
		cluster := newOpTestCluster()
		cp1 := newOpTestMachine(cluster.Name, "controlplane", "cfgf-cp1")
		cp2 := newOpTestMachine(cluster.Name, "controlplane", "cfgf-cp2")
		newOpTestMachine(cluster.Name, "controlplane", "cfgf-cp3")
		setUpgradeOrder(cp1, "1")
		setUpgradeOrder(cp2, "2")
		pend(cp1, cp2)

		op := newOpTestOperationOfKind(cluster.Name, ichorv1alpha1.TalosOperationKindConfig)
		fake := &fakeAdvancer{outcomes: map[string]advanceOutcome{cp1.Name: {err: errors.New("etcd member missing")}}}
		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: fake.Advance}

		reconcileTimes(r, op, 3)

		Expect(fake.calls).To(Equal([]string{cp1.Name}))

		got := fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(got.Status.FailureReason).To(ContainSubstring("etcd member missing"))
	})
})
