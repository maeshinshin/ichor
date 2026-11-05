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

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// An approved machine whose node fell back to maintenance mode (disk wiped or replaced) is sent its
// configuration again on its own.
var _ = Describe("TalosMachineReconciler re-provisioning a node that fell back to maintenance mode", func() {
	const (
		oldFingerprint = "OJ+e3lINvDPSrrxIkkatieIh0ewV9pPDSMWLCCGTZ6o="
		newFingerprint = "q3Nf3cKzv2rYxN0m8l1uZJ0cZ6wq8uYV1m3m1k9m1WE="
		hostname       = "cp-wiped"
		otherMember    = "cp-other"
	)

	var (
		cluster  *ichorv1alpha1.TalosCluster
		machine  *ichorv1alpha1.TalosMachine
		bundle   *secrets.Bundle
		talos    *fakeTalosClient
		maint    *fakeMaintenanceClient
		recorder *record.FakeRecorder
		r        *TalosMachineReconciler
	)

	// setup builds a machine that converged once (an applied hash, a stored time, Provisioned=True)
	// and whose node now answers only the maintenance API with the recorded identity.
	setup := func(role string) {
		bundle = newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster = newTestCluster(secret.Name, "v1.14")

		wipe := false
		cfg := minimalConfigSkeleton()
		cfg.MachineConfig.MachineType = role
		cfg.MachineConfig.MachineInstall = &typedconfig.InstallConfig{InstallDisk: provDisk, InstallWipe: &wipe}
		newTestPatch(cluster.Name, layerCluster, nil, cfg)

		machine = newTestMachine(cluster.Name, true)

		got := fetchMachine(machine)
		got.Spec.Role = role
		got.Spec.Identity.Serial = provSerial
		got.Spec.Identity.MACAddresses = []string{provMAC}
		got.Spec.Hostname = hostname
		Expect(k8sClient.Update(ctx, got)).To(Succeed())

		got = fetchMachine(machine)
		stored := metav1.NewTime(time.Now().Add(-48 * time.Hour))
		got.Status.AppliedConfigHash = "applied-long-ago"
		got.Status.ObservedInputsHash = "inputs-long-ago"
		got.Status.ConfigStoredAt = &stored
		got.Status.MaintenanceFingerprint = oldFingerprint
		apimeta.SetStatusCondition(&got.Status.Conditions, metav1.Condition{
			Type: conditionProvisioned, Status: metav1.ConditionTrue, Reason: reasonNodeJoined, Message: "joined",
		})
		Expect(k8sClient.Status().Update(ctx, got)).To(Succeed())

		talos = &fakeTalosClient{currentErr: errUnauthenticated}
		maint = &fakeMaintenanceClient{
			identity:    HardwareIdentity{UUID: got.Spec.Identity.UUID, Serial: provSerial, MACAddresses: []string{provMAC}},
			fingerprint: newFingerprint,
		}
		recorder = record.NewFakeRecorder(20)
		r = &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: clientFactory(talos), MaintenanceClient: maintenanceFactory(maint), Recorder: recorder,
		}
	}

	reconcileOnce := func() *ichorv1alpha1.TalosMachine {
		reconcileOK(r, machine)

		return fetchMachine(machine)
	}

	// siblingControlPlane adds a second approved control plane the wiped one can be compared with.
	siblingControlPlane := func() {
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)).To(Succeed())
		cluster.Spec.ControlPlane.Count = 3
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		sibling := newTestControlPlane(cluster.Name, "9")
		approveAt(sibling, time.Now().Add(-72*time.Hour))
		approveAt(machine, time.Now().Add(-48*time.Hour))
	}

	It("sends the rendered configuration to a matching node in maintenance mode, once, and follows it to running", func() {
		setup(roleWorker)

		got := reconcileOnce()
		Expect(maint.applyCalls).To(HaveLen(1))
		Expect(got.Status.ConfigStoredAt.Time).To(BeTemporally("~", time.Now(), time.Minute))
		Expect(got.Status.AppliedConfigHash).To(BeEmpty(), "the machine is followed like a first provisioning")
		Expect(got.Status.Stage).To(Equal(stageInstalling))
		Expect(got.Status.MaintenanceFingerprint).To(Equal(newFingerprint), "the old certificate is gone with the wiped disk")
		Expect(readyCondition(got).Reason).To(Equal(reasonConfigStored))
		Expect(readyCondition(got).Message).To(ContainSubstring("re-installed"))
		Expect(apimeta.FindStatusCondition(got.Status.Conditions, conditionProvisioned).Status).To(Equal(metav1.ConditionFalse))
		Expect(recorder.Events).To(Receive(And(ContainSubstring("Warning"), ContainSubstring("NodeReinstalled"))))

		// The node still answers insecurely while it starts to install: nothing is sent again.
		got = reconcileOnce()
		Expect(maint.applyCalls).To(HaveLen(1))
		Expect(readyCondition(got).Reason).To(Equal(reasonAwaitingInstall))

		// The node installed and trusts the bundle again.
		talos.currentErr = nil
		talos.current = maint.applyCalls[0]
		talos.stage = stageRunning
		got = reconcileOnce()
		Expect(readyCondition(got).Reason).To(Equal(reasonConverged))
		Expect(apimeta.FindStatusCondition(got.Status.Conditions, conditionProvisioned).Status).To(Equal(metav1.ConditionTrue))
		Expect(maint.applyCalls).To(HaveLen(1))
	})

	It("keeps refusing a node whose identity is not the approved one", func() {
		setup(roleWorker)
		maint.identity.Serial = "someone-elses"

		got := reconcileOnce()
		Expect(maint.applyCalls).To(BeEmpty())
		Expect(readyCondition(got).Reason).To(Equal(reasonIdentityMismatch))
		Expect(got.Status.AppliedConfigHash).To(Equal("applied-long-ago"))

		maint.identity.Serial = provSerial
		maint.identity.UUID = "another-uuid"
		got = reconcileOnce()
		Expect(maint.applyCalls).To(BeEmpty())
		Expect(readyCondition(got).Reason).To(Equal(reasonIdentityMismatch))
	})

	It("sends nothing to a node that presents another certificate than the one the operator accepted", func() {
		setup(roleWorker)

		got := fetchMachine(machine)
		got.Annotations = map[string]string{ichorv1alpha1.TalosMachineTrustMaintenanceFingerprintAnnotation: oldFingerprint}
		Expect(k8sClient.Update(ctx, got)).To(Succeed())

		maint.err = errors.New("rpc error: code = Unavailable desc = " + maintenancePinMismatch + ": the node presented X, pinned Y")
		got = reconcileOnce()
		Expect(maint.applyCalls).To(BeEmpty())
		Expect(readyCondition(got).Reason).To(Equal(reasonMaintenanceChanged))
	})

	It("keeps the old report when the node answers neither API", func() {
		setup(roleWorker)
		maint.err = errors.New("connection refused")

		got := reconcileOnce()
		Expect(maint.applyCalls).To(BeEmpty())
		Expect(readyCondition(got).Reason).To(Equal(reasonCurrentConfigUnavailable))
	})

	It("sends nothing while hold-apply is set", func() {
		setup(roleWorker)

		got := fetchMachine(machine)
		got.Annotations = map[string]string{ichorv1alpha1.TalosMachineHoldApplyAnnotation: "true"}
		Expect(k8sClient.Update(ctx, got)).To(Succeed())

		got = reconcileOnce()
		Expect(maint.applyCalls).To(BeEmpty())
		Expect(readyCondition(got).Reason).To(Equal(reasonReprovisionBlocked))
		Expect(readyCondition(got).Message).To(ContainSubstring(ichorv1alpha1.TalosMachineHoldApplyAnnotation))
	})

	It("drops the AdoptionHeld condition in the reconcile that sees hold-apply gone, even on the maintenance path", func() {
		setup(roleWorker)

		got := fetchMachine(machine)
		got.Annotations = map[string]string{ichorv1alpha1.TalosMachineHoldApplyAnnotation: "true"}
		Expect(k8sClient.Update(ctx, got)).To(Succeed())

		// The running-node path set it before the wipe; the maintenance path never touches it.
		got = fetchMachine(machine)
		apimeta.SetStatusCondition(&got.Status.Conditions, metav1.Condition{
			Type: conditionAdoptionHeld, Status: metav1.ConditionTrue, Reason: reasonAdoptionHeld, Message: "held",
		})
		Expect(k8sClient.Status().Update(ctx, got)).To(Succeed())

		got = reconcileOnce()
		Expect(readyCondition(got).Reason).To(Equal(reasonReprovisionBlocked))

		got.Annotations = nil
		Expect(k8sClient.Update(ctx, got)).To(Succeed())

		got = reconcileOnce()
		Expect(readyCondition(got).Reason).To(Equal(reasonConfigStored))
		Expect(apimeta.FindStatusCondition(got.Status.Conditions, conditionAdoptionHeld)).To(BeNil())
	})

	It("never probes or sends when the endpoint is not a valid target", func() {
		setup(roleWorker)

		got := fetchMachine(machine)
		got.Spec.Endpoint = "127.0.0.1:50000"
		Expect(k8sClient.Update(ctx, got)).To(Succeed())

		got = reconcileOnce()
		Expect(maint.applyCalls).To(BeEmpty())
		Expect(readyCondition(got).Reason).To(Equal(reasonEndpointRefused))
	})

	// The identity a node reports on the insecure maintenance API is self-reported. A twin that
	// reports an approved, converged machine's UUID, serial and MACs from another address inside a
	// discovery range must not become where that machine is dialled, or it would be sent the
	// configuration (the CA keys) and the live node's etcd member would be removed. Only the node
	// at the machine's own address that fell back to maintenance mode (a real wipe) is re-provisioned.
	Describe("with a twin answering the maintenance API at another address", func() {
		const twin = "10.0.9.9:50000"

		It("is not followed to the twin, sends it nothing and removes no etcd member; a wipe at the real address still re-provisions", func() {
			setup(roleControlPlane)
			siblingControlPlane()
			talos.etcdMembers = []EtcdMember{{ID: 1, Hostname: otherMember}, {ID: 2, Hostname: hostname}}

			// Discover finds a machine by the name the UUID gives it.
			got := fetchMachine(machine)
			got.Spec.Identity.UUID = machine.Name
			Expect(k8sClient.Update(ctx, got)).To(Succeed())
			maint.identity.UUID = machine.Name

			realNodeWiped := false
			r.MaintenanceClient = func(_ context.Context, endpoint string) (MaintenanceClient, error) {
				if endpoint == twin || realNodeWiped {
					return maint, nil
				}

				return nil, errors.New("connection refused")
			}

			d := &DiscoveryReconciler{Client: k8sClient, MaintenanceClient: r.MaintenanceClient}
			_, err := d.Discover(ctx, cluster, twin)
			Expect(err).NotTo(HaveOccurred())
			Expect(fetchMachine(machine).Status.ObservedEndpoint).To(BeEmpty(), "discovery must not redirect an approved machine")

			reconcileOnce() // adds the removal finalizer if it is missing
			got = reconcileOnce()
			Expect(maint.applyCalls).To(BeEmpty(), "the twin is not sent the configuration")
			Expect(talos.etcdRemoveIDs).To(BeEmpty(), "the live member is not removed")
			Expect(got.DialEndpoint()).To(Equal(got.Spec.Endpoint))
			Expect(readyCondition(got).Reason).To(Equal(reasonCurrentConfigUnavailable))

			realNodeWiped = true
			got = reconcileOnce()
			Expect(maint.applyCalls).To(HaveLen(1), "the wiped node at the machine's own address is re-provisioned")
			Expect(talos.etcdRemoveIDs).To(Equal([]uint64{2}))
			Expect(readyCondition(got).Reason).To(Equal(reasonConfigStored))
		})
	})

	Describe("for a control plane", func() {
		It("removes the stale etcd member through a surviving control plane, then sends the configuration", func() {
			setup(roleControlPlane)
			siblingControlPlane()
			talos.etcdMembers = []EtcdMember{{ID: 1, Hostname: otherMember}, {ID: 2, Hostname: hostname}}

			reconcileOnce() // adds the removal finalizer if it is missing
			got := reconcileOnce()
			Expect(talos.etcdRemoveIDs).To(Equal([]uint64{2}))
			Expect(maint.applyCalls).To(HaveLen(1))
			Expect(readyCondition(got).Reason).To(Equal(reasonConfigStored))
			Expect(recorder.Events).To(Receive(ContainSubstring("StaleEtcdMemberRemoved")))
			Expect(recorder.Events).To(Receive(ContainSubstring("NodeReinstalled")))
		})

		It("sends nothing and names the removal path when etcd refuses to drop the member", func() {
			setup(roleControlPlane)
			siblingControlPlane()
			talos.etcdMembers = []EtcdMember{{ID: 2, Hostname: hostname}}
			talos.etcdRemoveErr = errors.New("etcdserver: no leader")

			reconcileOnce()
			got := reconcileOnce()
			Expect(maint.applyCalls).To(BeEmpty())
			Expect(readyCondition(got).Reason).To(Equal(reasonStaleEtcdMember))
			Expect(readyCondition(got).Message).To(ContainSubstring("ichor node remove"))
			Expect(got.Status.AppliedConfigHash).To(Equal("applied-long-ago"))
		})

		It("sends nothing when etcd cannot be read, rather than guessing", func() {
			setup(roleControlPlane)
			siblingControlPlane()
			talos.etcdMembersErr = errors.New("rpc error: code = Unavailable")

			reconcileOnce()
			got := reconcileOnce()
			Expect(maint.applyCalls).To(BeEmpty())
			Expect(readyCondition(got).Reason).To(Equal(reasonReprovisionBlocked))
		})

		It("sends nothing when there is no other control plane, pointing to the cluster recovery", func() {
			setup(roleControlPlane)

			reconcileOnce()
			got := reconcileOnce()
			Expect(maint.applyCalls).To(BeEmpty())
			Expect(readyCondition(got).Reason).To(Equal(reasonReprovisionBlocked))
			Expect(readyCondition(got).Message).To(ContainSubstring("ichor restore --recover-cluster"))
		})

		It("sends the configuration when etcd no longer lists the member, under the cluster lock", func() {
			setup(roleControlPlane)
			siblingControlPlane()
			talos.etcdMembers = []EtcdMember{{ID: 1, Hostname: otherMember}}

			lock := opTestLock(cluster)
			Expect(lock.Acquire(ctx, "someone-else")).To(Succeed())

			reconcileOnce()
			got := reconcileOnce()
			Expect(maint.applyCalls).To(BeEmpty())
			Expect(talos.etcdRemoveIDs).To(BeEmpty(), "the member is not touched without the lock")
			Expect(readyCondition(got).Reason).To(Equal(reasonClusterLockHeld))

			Expect(lock.Release(ctx, "someone-else")).To(Succeed())

			reconcileOnce()
			Expect(maint.applyCalls).To(HaveLen(1))
			Expect(talos.etcdRemoveIDs).To(BeEmpty())

			expectLockFree(cluster, "the lock is released once the configuration is stored")
		})
	})
})
