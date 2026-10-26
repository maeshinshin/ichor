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

	remaining    int
	remainingErr error

	beginCalls int
	beginErr   error
	endCalls   int
	endErr     error
}

func (f *fakeStorageEvacuator) Resolve(context.Context, *ichorv1alpha1.TalosCluster, string) (*evacuationTarget, error) {
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

var _ = Describe("talosUpgradeAdvancer", func() {
	It("fails (never succeeds) when the node reboots but is still running its previous version -- an A/B slot rollback", func() {
		secret := newTestSecret(newTestBundle("v1.14"))
		cluster := newTestCluster(secret.Name, "v1.14")
		cp := newTestControlPlane(cluster.Name, "ab1")
		setHostname(cp, "node-ab1")
		newTestNode("node-ab1", true)

		fake := &fakeTalosClient{version: testOldTalosVersion, bootID: testBootIDBefore}
		adv := newUpgradeAdvancer(fake, nil)

		done, err := adv.Advance(ctx, cluster, fetchMachine(cp), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(fake.upgradeCalls).To(HaveLen(1), "the Upgrade API call must have been made")

		// The node reboots (boot ID changes) but comes back on the same old
		// version: Talos's own A/B bootloader fell back.
		fake.bootID = testBootIDAfter

		done, err = adv.Advance(ctx, cluster, fetchMachine(cp), nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("fell back to its previous A/B slot"))
		Expect(done).To(BeFalse())
	})

	It("succeeds once the node reboots onto the target version, is Ready, and is back in etcd", func() {
		secret := newTestSecret(newTestBundle("v1.14"))
		cluster := newTestCluster(secret.Name, "v1.14")
		cp := newTestControlPlane(cluster.Name, "ok1")
		setHostname(cp, "node-ok1")
		newTestNode("node-ok1", true)

		fake := &fakeTalosClient{version: testOldTalosVersion, bootID: testBootIDBefore}
		adv := newUpgradeAdvancer(fake, nil)

		done, err := adv.Advance(ctx, cluster, fetchMachine(cp), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())

		// The node reboots onto the target version and rejoins etcd.
		fake.bootID = testBootIDAfter
		fake.version = cluster.Spec.TalosVersion
		fake.etcdMembers = []EtcdMember{{ID: 1, Hostname: "node-ok1"}}

		done, err = adv.Advance(ctx, cluster, fetchMachine(cp), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())

		got := fetchMachine(cp)
		Expect(apimeta.IsStatusConditionTrue(got.Status.Conditions, conditionTalosUpgrade)).To(BeFalse(),
			"the in-flight marker must be cleared once convergence is confirmed")
	})

	It("waits for a node that has not become Ready or rejoined etcd yet after rebooting, without failing", func() {
		secret := newTestSecret(newTestBundle("v1.14"))
		cluster := newTestCluster(secret.Name, "v1.14")
		cp := newTestControlPlane(cluster.Name, "wait1")
		setHostname(cp, "node-wait1")
		newTestNode("node-wait1", true)

		fake := &fakeTalosClient{version: testOldTalosVersion, bootID: testBootIDBefore}
		adv := newUpgradeAdvancer(fake, nil)

		_, err := adv.Advance(ctx, cluster, fetchMachine(cp), nil)
		Expect(err).NotTo(HaveOccurred())

		// Rebooted onto the target version, but not an etcd member yet (still joining).
		fake.bootID = testBootIDAfter
		fake.version = cluster.Spec.TalosVersion
		fake.etcdMembers = nil

		done, err := adv.Advance(ctx, cluster, fetchMachine(cp), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
	})

	It("refuses to start an upgrade on a node that is not Ready", func() {
		secret := newTestSecret(newTestBundle("v1.14"))
		cluster := newTestCluster(secret.Name, "v1.14")
		cp := newTestControlPlane(cluster.Name, "nr1")
		setHostname(cp, "node-nr1")
		newTestNode("node-nr1", false)

		fake := &fakeTalosClient{version: testOldTalosVersion, bootID: testBootIDBefore}
		adv := newUpgradeAdvancer(fake, nil)

		done, err := adv.Advance(ctx, cluster, fetchMachine(cp), nil)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not Ready"))
		Expect(done).To(BeFalse())
		Expect(fake.upgradeCalls).To(BeEmpty(), "the Upgrade API must never be called against a not-Ready node")
	})

	It("refuses an unsupported Talos upgrade path without ever calling the Upgrade API", func() {
		secret := newTestSecret(newTestBundle("v1.14"))
		cluster := newTestCluster(secret.Name, "v1.14")
		cp := newTestControlPlane(cluster.Name, "path1")
		setHostname(cp, "node-path1")
		newTestNode("node-path1", true)

		// v1.9.0 predates 1.14's MinimumHostUpgradeVersion (1.12.0): unsupported.
		fake := &fakeTalosClient{version: "v1.9.0", bootID: testBootIDBefore}
		adv := newUpgradeAdvancer(fake, nil)

		done, err := adv.Advance(ctx, cluster, fetchMachine(cp), nil)
		Expect(err).To(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(fake.upgradeCalls).To(BeEmpty())
	})

	It("pauses (never proceeds to the Upgrade API) once evacuation times out with replicas still remaining, and re-enables scheduling", func() {
		secret := newTestSecret(newTestBundle("v1.14"))
		cluster := newTestCluster(secret.Name, "v1.14")
		cp := newTestControlPlane(cluster.Name, "evac1")
		setHostname(cp, "node-evac1")
		newTestNode("node-evac1", true)

		hook, err := storage.NewEvacuationHook(storage.LonghornV1)
		Expect(err).NotTo(HaveOccurred())

		evacuator := &fakeStorageEvacuator{
			target:    &evacuationTarget{hook: hook, timeout: time.Minute, namespace: "longhorn-system"},
			remaining: 3,
		}

		fake := &fakeTalosClient{version: testOldTalosVersion, bootID: testBootIDBefore}
		adv := newUpgradeAdvancer(fake, evacuator)

		// First call: begins evacuation, does not yet proceed to the Upgrade API.
		done, err := adv.Advance(ctx, cluster, fetchMachine(cp), nil)
		Expect(err).NotTo(HaveOccurred())
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
		secret := newTestSecret(newTestBundle("v1.14"))
		cluster := newTestCluster(secret.Name, "v1.14")
		cp := newTestControlPlane(cluster.Name, "evac2")
		setHostname(cp, "node-evac2")
		newTestNode("node-evac2", true)

		hook, err := storage.NewEvacuationHook(storage.LonghornV1)
		Expect(err).NotTo(HaveOccurred())

		evacuator := &fakeStorageEvacuator{
			target: &evacuationTarget{hook: hook, timeout: time.Minute, namespace: "longhorn-system"},
		}

		fake := &fakeTalosClient{version: testOldTalosVersion, bootID: testBootIDBefore}
		adv := newUpgradeAdvancer(fake, evacuator)

		_, err = adv.Advance(ctx, cluster, fetchMachine(cp), nil) // begins evacuation
		Expect(err).NotTo(HaveOccurred())

		evacuator.remaining = 0 // the replica has finished moving off

		done, err := adv.Advance(ctx, cluster, fetchMachine(cp), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(fake.upgradeCalls).To(HaveLen(1))
		Expect(evacuator.endCalls).To(BeZero(),
			"scheduling must stay disabled until the drain-and-reboot is confirmed, not re-enabled the moment replicas finish moving")

		got := fetchMachine(cp)
		Expect(apimeta.IsStatusConditionTrue(got.Status.Conditions, conditionStorageEvacuation)).To(BeTrue())
	})
})
