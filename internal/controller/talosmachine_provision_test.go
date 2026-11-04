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

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
	"github.com/maeshinshin/ichor/pkg/render"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

const (
	provSerial = "serial-1"
	provMAC    = "aa:bb:cc:dd:ee:01"
)

var errUnauthenticated = errors.New("x509: certificate signed by unknown authority")

var _ = Describe("TalosMachineReconciler provisioning a node in maintenance mode", func() {
	var (
		cluster  *ichorv1alpha1.TalosCluster
		machine  *ichorv1alpha1.TalosMachine
		bundle   *secrets.Bundle
		patch    *ichorv1alpha1.TalosConfigPatch
		talos    *fakeTalosClient
		maint    *fakeMaintenanceClient
		recorder *record.FakeRecorder
		r        *TalosMachineReconciler
	)

	// installDiskPatch gives the cluster's render the install disk a real node needs.
	installDiskPatch := func(role string) {
		wipe := false
		cfg := minimalConfigSkeleton()
		cfg.MachineConfig.MachineType = role
		cfg.MachineConfig.MachineInstall = &typedconfig.InstallConfig{InstallDisk: "/dev/vda", InstallWipe: &wipe}
		patch = newTestPatch(cluster.Name, layerCluster, nil, cfg)
	}

	setup := func(role string) {
		bundle = newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster = newTestCluster(secret.Name, "v1.14")
		machine = newTestMachine(cluster.Name, true)

		got := fetchMachine(machine)
		got.Spec.Role = role
		got.Spec.Identity.Serial = provSerial
		got.Spec.Identity.MACAddresses = []string{provMAC}
		Expect(k8sClient.Update(ctx, got)).To(Succeed())

		talos = &fakeTalosClient{currentErr: errUnauthenticated}
		maint = &fakeMaintenanceClient{identity: HardwareIdentity{
			UUID: got.Spec.Identity.UUID, Serial: provSerial, MACAddresses: []string{provMAC},
		}}
		recorder = record.NewFakeRecorder(10)
		r = &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: clientFactory(talos), MaintenanceClient: maintenanceFactory(maint), Recorder: recorder,
		}
	}

	reconcileOnce := func() *ichorv1alpha1.TalosMachine {
		_, err := reconcileMachine(r, machine)
		Expect(err).NotTo(HaveOccurred())

		return fetchMachine(machine)
	}

	It("applies the rendered configuration once to a node in maintenance mode and follows it, never applying again", func() {
		setup(roleWorker)
		installDiskPatch(roleWorker)

		got := reconcileOnce()
		Expect(maint.applyCalls).To(HaveLen(1))

		want, _, err := render.Render(cluster, fetchMachine(machine), bundle, []ichorv1alpha1.TalosConfigPatch{*patch}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(maint.applyCalls[0]).To(Equal(want))

		Expect(got.Status.ConfigStoredAt).NotTo(BeNil())
		Expect(got.Status.Stage).To(Equal(stageInstalling))
		Expect(readyCondition(got).Reason).To(Equal(reasonConfigStored))
		Expect(readyCondition(got).Status).To(Equal(metav1.ConditionFalse))
		Expect(apimeta.FindStatusCondition(got.Status.Conditions, conditionProvisioned).Status).To(Equal(metav1.ConditionFalse))
		Expect(recorder.Events).To(Receive(ContainSubstring("ConfigApplied")))

		// The node still answers insecurely: stored is stored, nothing is applied again.
		got = reconcileOnce()
		Expect(maint.applyCalls).To(HaveLen(1))
		Expect(readyCondition(got).Reason).To(Equal(reasonAwaitingInstall))
		Expect(got.Status.Stage).To(Equal(stageMaintenance))

		// Neither API answers while it installs and reboots.
		maint.err = errors.New("connection refused")
		got = reconcileOnce()
		Expect(maint.applyCalls).To(HaveLen(1))
		Expect(readyCondition(got).Reason).To(Equal(reasonNodeRebooting))
		Expect(got.Status.Stage).To(Equal(stageRebooting))
	})

	It("pins the maintenance certificate it first sees and sends nothing to a node that presents another", func() {
		setup(roleWorker)
		installDiskPatch(roleWorker)

		// A node that was never discovered through the controller is pinned at its first probe.
		maint.fingerprint = "OJ+e3lINvDPSrrxIkkatieIh0ewV9pPDSMWLCCGTZ6o="
		maint.applyErr = errors.New("refused")
		got := reconcileOnce()
		Expect(got.Status.MaintenanceFingerprint).To(Equal("OJ+e3lINvDPSrrxIkkatieIh0ewV9pPDSMWLCCGTZ6o="))
		Expect(maint.applyCalls).To(HaveLen(1))

		// Another node (or the same one after a reboot) answers with a different certificate.
		maint.applyErr = nil
		maint.err = errors.New("rpc error: code = Unavailable desc = " + maintenancePinMismatch + ": the node presented X, pinned that")
		got = reconcileOnce()
		Expect(maint.applyCalls).To(HaveLen(1), "the configuration must not be sent to a node whose certificate changed")
		Expect(readyCondition(got).Reason).To(Equal(reasonMaintenanceChanged))
		Expect(readyCondition(got).Message).To(ContainSubstring(ichorv1alpha1.TalosMachineTrustMaintenanceFingerprintAnnotation))
	})

	It("follows the node through installing and rebooting to running, then records the join", func() {
		setup(roleWorker)
		installDiskPatch(roleWorker)
		reconcileOnce()
		Expect(recorder.Events).To(Receive(ContainSubstring("ConfigApplied")))

		// The node now trusts the bundle's CA.
		talos.currentErr = nil
		talos.current = maint.applyCalls[0]

		for stage, reason := range map[string]string{
			stageInstalling: reasonNodeInstalling,
			stageRebooting:  reasonNodeRebooting,
			"booting":       reasonNodeStarting,
		} {
			talos.stage = stage
			got := reconcileOnce()
			Expect(got.Status.Stage).To(Equal(stage))
			Expect(readyCondition(got).Reason).To(Equal(reason))
			Expect(talos.applyCalls).To(BeEmpty())
		}

		// A volume that failed to come up (TPM key on a TPM-less machine) is named in the message.
		talos.stage = "booting"
		talos.volumeFailure = "STATE: stat /dev/tpm0: no such file or directory"
		got := reconcileOnce()
		Expect(readyCondition(got).Reason).To(Equal(reasonNodeStarting))
		Expect(readyCondition(got).Message).To(ContainSubstring("a volume failed"))
		Expect(readyCondition(got).Message).To(ContainSubstring("stat /dev/tpm0: no such file or directory"))
		talos.volumeFailure = ""

		talos.stage = stageRunning
		got = reconcileOnce()
		Expect(got.Status.Stage).To(Equal(stageRunning))
		Expect(readyCondition(got).Reason).To(Equal(reasonConverged))
		Expect(readyCondition(got).Status).To(Equal(metav1.ConditionTrue))
		Expect(apimeta.FindStatusCondition(got.Status.Conditions, conditionProvisioned).Reason).To(Equal(reasonNodeJoined))
		Expect(recorder.Events).To(Receive(ContainSubstring("NodeJoined")))
		Expect(maint.applyCalls).To(HaveLen(1))
	})

	It("refuses to apply when the node at the endpoint is not the recorded machine", func() {
		setup(roleWorker)
		installDiskPatch(roleWorker)

		maint.identity.Serial = "someone-elses"

		got := reconcileOnce()
		Expect(maint.applyCalls).To(BeEmpty())
		Expect(readyCondition(got).Reason).To(Equal(reasonIdentityMismatch))
		Expect(got.Status.ConfigStoredAt).To(BeNil())

		maint.identity.Serial = provSerial
		maint.identity.MACAddresses = []string{"11:22:33:44:55:66"}
		reconcileOnce()
		Expect(maint.applyCalls).To(BeEmpty())
	})

	// The webhook already refuses these, but a CR written while it was down (or before it
	// existed) must still not make the controller push CA keys to the controller's own network position.
	It("never probes or applies to an endpoint that is loopback, link-local or inside the cluster's pod range", func() {
		setup(roleWorker)
		installDiskPatch(roleWorker)

		for _, ep := range []string{"127.0.0.1:50000", "169.254.169.254:80", "[::1]:50000"} {
			got := fetchMachine(machine)
			got.Spec.Endpoint = ep
			Expect(k8sClient.Update(ctx, got)).To(Succeed())

			got = reconcileOnce()
			Expect(maint.applyCalls).To(BeEmpty(), ep)
			Expect(readyCondition(got).Reason).To(Equal(reasonEndpointRefused), ep)
			Expect(got.Status.ConfigStoredAt).To(BeNil(), ep)
		}
	})

	It("refuses to apply a configuration that names no install disk", func() {
		setup(roleWorker)

		got := reconcileOnce()
		Expect(maint.applyCalls).To(BeEmpty())
		Expect(readyCondition(got).Reason).To(Equal(reasonInstallDiskUnknown))
		Expect(got.Status.ConfigStoredAt).To(BeNil())
	})

	It("reports the old CurrentConfigUnavailable when the node answers neither API", func() {
		setup(roleWorker)
		installDiskPatch(roleWorker)
		maint.err = errors.New("connection refused")

		got := reconcileOnce()
		Expect(maint.applyCalls).To(BeEmpty())
		Expect(readyCondition(got).Reason).To(Equal(reasonCurrentConfigUnavailable))
	})

	It("never probes the insecure API for a machine that has already converged", func() {
		setup(roleWorker)
		installDiskPatch(roleWorker)

		got := fetchMachine(machine)
		got.Status.AppliedConfigHash = "something"
		Expect(k8sClient.Status().Update(ctx, got)).To(Succeed())

		got = reconcileOnce()
		Expect(maint.applyCalls).To(BeEmpty())
		Expect(readyCondition(got).Reason).To(Equal(reasonCurrentConfigUnavailable))
	})

	It("keeps a control plane join behind the cluster lock", func() {
		setup(roleControlPlane)
		installDiskPatch(roleControlPlane)

		lock := clusterlock.New(k8sClient, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, 0)
		Expect(lock.Acquire(ctx, "someone-else")).To(Succeed())

		reconcileOnce() // adds the removal finalizer
		got := reconcileOnce()
		Expect(maint.applyCalls).To(BeEmpty())
		Expect(readyCondition(got).Reason).To(Equal(reasonClusterLockHeld))

		Expect(lock.Release(ctx, "someone-else")).To(Succeed())

		got = reconcileOnce()
		Expect(maint.applyCalls).To(HaveLen(1))
		Expect(got.Status.ConfigStoredAt).NotTo(BeNil())

		_, held, err := lock.Holder(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(held).To(BeFalse(), "the lock is released once the configuration is stored")
	})

	It("joins a worker while the cluster lock is held: a worker is no etcd member, so its join takes no lock", func() {
		setup(roleWorker)
		installDiskPatch(roleWorker)

		lock := clusterlock.New(k8sClient, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, 0)
		Expect(lock.Acquire(ctx, "someone-else")).To(Succeed())
		DeferCleanup(func() { Expect(lock.Release(ctx, "someone-else")).To(Succeed()) })

		got := reconcileOnce()
		Expect(maint.applyCalls).To(HaveLen(1))
		Expect(got.Status.ConfigStoredAt).NotTo(BeNil())
	})

	It("does not join a control plane held by the approval cap", func() {
		secret := newTestSecret(newTestBundle("v1.14"))
		cluster = newTestClusterWithCount(1, secret.Name)
		installDiskPatch(roleControlPlane)

		maint = &fakeMaintenanceClient{}
		talos = &fakeTalosClient{currentErr: errUnauthenticated}
		r = &TalosMachineReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: clientFactory(talos), MaintenanceClient: maintenanceFactory(maint),
		}

		a, b, c := newTestControlPlane(cluster.Name, "1"), newTestControlPlane(cluster.Name, "2"), newTestControlPlane(cluster.Name, "3")
		base := time.Now().Add(-time.Hour)
		approveAt(a, base)
		approveAt(b, base.Add(time.Minute))
		approveAt(c, base.Add(2*time.Minute))
		maint.identity = HardwareIdentity{UUID: c.Spec.Identity.UUID}

		machine = c
		reconcileTwice(r, c)
		Expect(maint.applyCalls).To(BeEmpty())
		Expect(readyCondition(fetchMachine(c)).Reason).To(Equal(reasonControlPlaneOverCap))
	})
})
