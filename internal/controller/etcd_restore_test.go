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
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/etcdbackup"
	"github.com/maeshinshin/ichor/pkg/render"
)

// restoreFixture is a cluster of three control planes, each with a faked Talos API, a backup storage
// that holds one good snapshot, and a reconciler whose destructive job is captured instead of started
// (ADR 0089). The test then runs the job's steps itself, which is what makes "the controller
// restarted here" a matter of building a new reconciler and a new job.
type restoreFixture struct {
	backup *backupFixture
	bk     *ichorv1alpha1.TalosEtcdBackup
	cps    map[string]*ichorv1alpha1.TalosMachine // by hostname
	fakes  map[string]*fakeTalosClient            // by machine name
	r      *TalosOperationReconciler
	jobs   []*restoreJob

	snapshot     string
	snapshotBody []byte
}

const (
	restoreHostA = "cp-a"
	restoreHostB = "cp-b"
	restoreHostC = "cp-c"
)

// newRestoreFixture builds the cluster; nodeName is the node the controller claims to run on.
func newRestoreFixture(nodeName string) *restoreFixture {
	b := newBackupFixture()
	f := &restoreFixture{backup: b, cps: map[string]*ichorv1alpha1.TalosMachine{}, fakes: map[string]*fakeTalosClient{}}

	for i, host := range []string{restoreHostA, restoreHostB, restoreHostC} {
		m := newTestControlPlane(b.cluster.Name, string(rune('1'+i)))
		setHostname(m, host)
		f.cps[host] = m
		f.fakes[m.Name] = &fakeTalosClient{etcdData: true, bootID: testBootIDBefore}
	}

	f.bk = b.newBackup(nil)
	f.snapshot, f.snapshotBody = f.putSnapshot(time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC), "etcd-snapshot")
	f.r = f.reconciler(nodeName)

	return f
}

// putSnapshot stores a good, encrypted snapshot taken at t.
func (f *restoreFixture) putSnapshot(t time.Time, body string) (name string, plain []byte) {
	plain = validTestSnapshot(body)

	pass, err := etcdbackup.DeriveKey(f.backup.bundle)
	Expect(err).NotTo(HaveOccurred())

	name = etcdbackup.ObjectName(t)
	f.putSealed(name, pass, plain)

	return name, plain
}

// putSealed stores plain in the backup storage under name, encrypted with key.
func (f *restoreFixture) putSealed(name string, key etcdbackup.Key, plain []byte) {
	var buf bytes.Buffer

	w, err := etcdbackup.Encrypt(&buf, key)
	Expect(err).NotTo(HaveOccurred())
	_, err = w.Write(plain)
	Expect(err).NotTo(HaveOccurred())
	Expect(w.Close()).To(Succeed())

	f.backup.store.objects[testBackupBase+"/"+name] = buf.Bytes()
}

// reconciler is a controller process: a new one has no memory of the old.
func (f *restoreFixture) reconciler(nodeName string) *TalosOperationReconciler {
	return &TalosOperationReconciler{
		Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, NodeName: nodeName,
		NewStore:    func(etcdbackup.S3Config) (etcdbackup.Store, error) { return f.backup.store, nil },
		TalosClient: perMachineClientFactory(f.fakes),
		launchTail:  func(j *restoreJob) { f.jobs = append(f.jobs, j) },
	}
}

func (f *restoreFixture) newOp(mutate func(*ichorv1alpha1.TalosOperation)) *ichorv1alpha1.TalosOperation {
	op := &ichorv1alpha1.TalosOperation{
		GenerateName: "etcd-restore-", Namespace: tmNamespace,
		Spec: ichorv1alpha1.TalosOperationSpec{
			ClusterRef: corev1.LocalObjectReference{Name: f.backup.cluster.Name}, Kind: ichorv1alpha1.TalosOperationKindEtcdRestore,
			EtcdRestore: &ichorv1alpha1.TalosOperationEtcdRestoreSpec{
				BackupRef: corev1.LocalObjectReference{Name: f.bk.Name}, Snapshot: f.snapshot,
				Confirm: f.backup.cluster.Name, Reason: "a bad change deleted the namespace",
			},
		},
	}
	if mutate != nil {
		mutate(op)
	}

	Expect(k8sClient.Create(ctx, op)).To(Succeed())

	return op
}

func (f *restoreFixture) reconcile(op *ichorv1alpha1.TalosOperation) *ichorv1alpha1.TalosOperation {
	return f.reconcileWith(f.r, op)
}

func (f *restoreFixture) reconcileWith(r *TalosOperationReconciler, op *ichorv1alpha1.TalosOperation) *ichorv1alpha1.TalosOperation {
	_, err := reconcileOperation(r, op)
	Expect(err).NotTo(HaveOccurred())

	return fetchOperation(op)
}

func (f *restoreFixture) fake(host string) *fakeTalosClient { return f.fakes[f.cps[host].Name] }

// target is the host of the control plane the marker names as the one the snapshot is recovered on.
func (f *restoreFixture) target() string {
	name := f.marker().Target
	for host, m := range f.cps {
		if m.Name == name {
			return host
		}
	}

	Fail("the marker's target " + name + " is not one of the control planes")

	return ""
}

func (f *restoreFixture) targetFake() *fakeTalosClient { return f.fake(f.target()) }

// hosts is every control plane's host, which is what a healthy etcd lists as members.
func (f *restoreFixture) hosts() []string {
	out := make([]string, 0, len(f.cps))
	for h := range f.cps {
		out = append(out, h)
	}

	return out
}

func (f *restoreFixture) marker() *etcdbackup.RestoreMarker {
	m, err := etcdbackup.GetMarker(ctx, f.backup.store, testBackupBase, mustPass(f.backup.bundle))
	Expect(err).NotTo(HaveOccurred())

	return m
}

func (f *restoreFixture) pinned() bool {
	_, ok := f.backup.store.objects[etcdbackup.PinnedKey(testBackupBase)]

	return ok
}

// lastJob is the job the most recent reconcile started.
func (f *restoreFixture) lastJob() *restoreJob {
	Expect(f.jobs).NotTo(BeEmpty())

	return f.jobs[len(f.jobs)-1]
}

// wipeHooks makes a reset empty the node's etcd data, and a bootstrap fill the target's again.
func (f *restoreFixture) wipeHooks() {
	for _, fk := range f.fakes {
		fk.resetEtcdHook = func() { fk.etcdData = false }
		fk.bootstrapRecoverHook = func() { fk.etcdData = true }
	}
}

// run steps the job until it is done, failed or maxSteps passed.
func (f *restoreFixture) run(job *restoreJob, maxSteps int) (done bool, err error) {
	for range maxSteps {
		if done, err = job.step(ctx); done || err != nil {
			return done, err
		}
	}

	return false, nil
}

// members makes the target's etcd list every control plane.
func (f *restoreFixture) members(hosts ...string) {
	list := make([]EtcdMember, 0, len(hosts))
	for i, h := range hosts {
		list = append(list, EtcdMember{ID: uint64(i + 1), Hostname: h})
	}

	for _, fk := range f.fakes {
		fk.etcdMembers = list
	}
}

// converged marks every machine as having reconciled the token, Ready.
func (f *restoreFixture) converged(token string) {
	for _, m := range f.cps {
		got := fetchMachine(m)
		got.Status.ObservedReconcileRequest = token
		apimeta.SetStatusCondition(&got.Status.Conditions, metav1.Condition{
			Type: conditionReady, Status: metav1.ConditionTrue, Reason: reasonConverged, Message: "ok",
		})
		Expect(k8sClient.Status().Update(ctx, got)).To(Succeed())
	}
}

func lockHeld(cluster *ichorv1alpha1.TalosCluster) bool {
	_, held, err := opTestLock(cluster).Holder(ctx)
	Expect(err).NotTo(HaveOccurred())

	return held
}

// dropOperation is the rollback's effect on an operation: it is simply gone from etcd.
func dropOperation(op *ichorv1alpha1.TalosOperation) {
	got := fetchOperation(op)
	controllerutil.RemoveFinalizer(got, etcdRestoreFinalizer)
	Expect(k8sClient.Update(ctx, got)).To(Succeed())

	err := k8sClient.Delete(ctx, got)
	Expect(client.IgnoreNotFound(err)).To(Succeed())
	Eventually(func() bool {
		return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(op), &ichorv1alpha1.TalosOperation{}))
	}).Should(BeTrue())
}

var _ = Describe("TalosOperation etcd-restore", func() {
	BeforeEach(func() { restoreTailInterval = time.Millisecond })

	Describe("the sequence", func() {
		It("verifies and pins the snapshot, marks the restore, wipes every control plane, recovers one, waits for the rest and reconciles the machines", func() {
			f := newRestoreFixture("")
			f.wipeHooks()
			op := f.newOp(nil)

			got := f.reconcile(op)
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))
			Expect(got.Status.EtcdRestore.Step).To(Equal(ichorv1alpha1.EtcdRestoreStepWiping))
			Expect(controllerutil.ContainsFinalizer(got, etcdRestoreFinalizer)).To(BeTrue())
			Expect(lockHeld(f.backup.cluster)).To(BeTrue())

			// Everything the job needs survives in the bucket before anything is wiped.
			m := f.marker()
			Expect(m).NotTo(BeNil())
			Expect(m.Step).To(Equal(ichorv1alpha1.EtcdRestoreStepWiping))
			Expect(m.Operation).To(Equal(op.Name))
			Expect(m.Snapshot).To(Equal(f.snapshot))
			Expect(m.Reason).To(Equal("a bad change deleted the namespace"))
			Expect(f.pinned()).To(BeTrue(), "a copy the retention cannot prune")

			for _, fk := range f.fakes {
				Expect(fk.resetEtcdCalls).To(BeZero(), "the controller itself wipes nothing: the job does")
			}

			// The job: one pass sends the wipe to every control plane at once.
			job := f.lastJob()
			done, err := job.step(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeFalse())

			for _, fk := range f.fakes {
				Expect(fk.resetEtcdCalls).To(Equal(1))
			}

			// They are wiped; the next pass records Recovering before it touches the target, and a
			// later one sees etcd answer on it.
			done, err = f.run(job, 5)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeTrue())
			Expect(f.marker().Step).To(Equal(ichorv1alpha1.EtcdRestoreStepRejoining))

			target := f.targetFake()
			Expect(target.recovered).To(HaveLen(1))
			Expect(target.recovered[0]).To(Equal(f.snapshotBody), "the plaintext snapshot, as EtcdSnapshot streamed it")
			Expect(target.bootstrapRecoverCalls).To(Equal(1))

			for _, fk := range f.fakes {
				if fk != target {
					Expect(fk.recovered).To(BeEmpty(), "only the target gets the snapshot")
				}

				Expect(fk.resetEtcdCalls).To(Equal(1), "no node is wiped twice")
			}

			// The other control planes have not rejoined yet: wait.
			f.members(f.target())
			got = f.reconcile(op)
			Expect(got.Status.EtcdRestore.Step).To(Equal(ichorv1alpha1.EtcdRestoreStepRejoining))
			Expect(got.Status.EtcdRestore.Message).To(ContainSubstring("rejoin"))
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))

			// All three are members: every machine is asked to reconcile and the operation waits.
			f.members(f.hosts()...)
			got = f.reconcile(op)
			Expect(got.Status.EtcdRestore.Step).To(Equal(ichorv1alpha1.EtcdRestoreStepReconciling))

			token := got.Status.EtcdRestore.ReconcileRequest
			Expect(token).To(Equal(string(op.UID)))

			for _, m := range f.cps {
				Expect(fetchMachine(m).Annotations).To(HaveKeyWithValue(ichorv1alpha1.EtcdRestoreReconcileAnnotation, token))
			}

			got = f.reconcile(op)
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))
			Expect(got.Status.EtcdRestore.Message).To(ContainSubstring("has not reconciled yet"))

			f.converged(token)

			got = f.reconcile(op)
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
			Expect(f.marker()).To(BeNil(), "the marker is removed when the restore is done")
			Expect(f.pinned()).To(BeFalse())
			Expect(lockHeld(f.backup.cluster)).To(BeFalse())
		})

		It("waits for a machine that reconciled but is not Ready, and says which", func() {
			f := newRestoreFixture("")
			f.wipeHooks()
			op := f.newOp(nil)
			f.reconcile(op)

			_, err := f.run(f.lastJob(), 5)
			Expect(err).NotTo(HaveOccurred())

			f.members(f.hosts()...)
			got := f.reconcile(op)
			token := got.Status.EtcdRestore.ReconcileRequest

			f.converged(token)

			bad := fetchMachine(f.cps[restoreHostB])
			apimeta.SetStatusCondition(&bad.Status.Conditions, metav1.Condition{
				Type: conditionReady, Status: metav1.ConditionFalse, Reason: reasonRebootRequired, Message: "reboot needed",
			})
			Expect(k8sClient.Status().Update(ctx, bad)).To(Succeed())

			got = f.reconcile(op)
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))
			Expect(got.Status.EtcdRestore.Message).To(ContainSubstring(f.cps[restoreHostB].Name))
			Expect(got.Status.EtcdRestore.Message).To(ContainSubstring(reasonRebootRequired))
		})
	})

	Describe("the Kubernetes API is rolled back to the snapshot", func() {
		It("recreates the operation from the marker when the controller starts, and finishes the restore", func() {
			f := newRestoreFixture("")
			f.wipeHooks()
			op := f.newOp(nil)
			f.reconcile(op)

			_, err := f.run(f.lastJob(), 5)
			Expect(err).NotTo(HaveOccurred())

			// etcd went back to the snapshot, which predates the operation, and the controller restarted.
			dropOperation(op)

			restarted := f.reconciler("")
			Expect(restarted.ResumeEtcdRestores(ctx)).To(Succeed())

			got := fetchOperation(op)
			Expect(got.Spec.Kind).To(Equal(ichorv1alpha1.TalosOperationKindEtcdRestore))
			Expect(got.Spec.EtcdRestore.Snapshot).To(Equal(f.snapshot))
			Expect(got.Spec.EtcdRestore.Reason).To(Equal("a bad change deleted the namespace"))
			Expect(got.Status.EtcdRestore.Step).To(Equal(ichorv1alpha1.EtcdRestoreStepRejoining))
			Expect(got.Status.EtcdRestore.Target).To(Equal(f.marker().Target))
			Expect(got.UID).NotTo(Equal(op.UID))

			f.members(f.hosts()...)
			got = f.reconcileWith(restarted, op)
			Expect(got.Status.EtcdRestore.Step).To(Equal(ichorv1alpha1.EtcdRestoreStepReconciling))

			f.converged(got.Status.EtcdRestore.ReconcileRequest)
			Expect(f.reconcileWith(restarted, op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
			Expect(f.marker()).To(BeNil())
		})

		It("treats a marker still at Wiping or Recovering with the operation gone as recovered", func() {
			f := newRestoreFixture("")
			op := f.newOp(nil)
			f.reconcile(op)
			Expect(f.marker().Step).To(Equal(ichorv1alpha1.EtcdRestoreStepWiping))

			dropOperation(op)
			Expect(f.reconciler("").ResumeEtcdRestores(ctx)).To(Succeed())

			Expect(fetchOperation(op).Status.EtcdRestore.Step).To(Equal(ichorv1alpha1.EtcdRestoreStepRejoining))
			Expect(f.marker().Step).To(Equal(ichorv1alpha1.EtcdRestoreStepRejoining), "the marker only moves forward")
		})

		It("leaves an operation that still exists alone", func() {
			f := newRestoreFixture("")
			op := f.newOp(nil)
			f.reconcile(op)

			uid := fetchOperation(op).UID
			Expect(f.reconciler("").ResumeEtcdRestores(ctx)).To(Succeed())
			Expect(fetchOperation(op).UID).To(Equal(uid))
		})

		It("does nothing without a marker", func() {
			f := newRestoreFixture("")
			Expect(f.reconciler("").ResumeEtcdRestores(ctx)).To(Succeed())
		})

		It("resumes a process that survived: the operation vanishing from the API is noticed", func() {
			f := newRestoreFixture("")
			f.wipeHooks()
			op := f.newOp(nil)
			f.reconcile(op)

			// the real registry is what a live process uses to remember
			defaultRestoreTails.mu.Lock()
			defaultRestoreTails.inflight[client.ObjectKeyFromObject(op)] = client.ObjectKeyFromObject(f.bk)
			defaultRestoreTails.mu.Unlock()
			DeferCleanup(func() { defaultRestoreTails.forget(client.ObjectKeyFromObject(op)) })

			_, err := f.run(f.lastJob(), 5)
			Expect(err).NotTo(HaveOccurred())

			dropOperation(op)

			_, err = reconcileOperation(f.r, op)
			Expect(err).NotTo(HaveOccurred())
			Expect(fetchOperation(op).Status.EtcdRestore.Step).To(Equal(ichorv1alpha1.EtcdRestoreStepRejoining))
		})
	})

	Describe("a restart of the controller", func() {
		It("at Wiping starts the job again from the marker without writing a new one or pinning again", func() {
			f := newRestoreFixture("")
			op := f.newOp(nil)
			f.reconcile(op)

			before := f.marker()
			first := f.lastJob()

			restarted := f.reconciler("")
			f.jobs = nil

			got := f.reconcileWith(restarted, op)
			Expect(got.Status.EtcdRestore.Step).To(Equal(ichorv1alpha1.EtcdRestoreStepWiping))
			Expect(f.jobs).To(HaveLen(1))
			Expect(f.jobs[0]).NotTo(BeIdenticalTo(first))
			Expect(f.marker()).To(Equal(before), "the marker was not rewritten")

			_, err := f.jobs[0].step(ctx)
			Expect(err).NotTo(HaveOccurred())

			for _, fk := range f.fakes {
				Expect(fk.resetEtcdCalls).To(Equal(1), "wiped by the new job, once")
			}
		})

		It("at Recovering does not wipe again: a recovered target is recognised by its etcd data", func() {
			f := newRestoreFixture("")
			f.wipeHooks()
			op := f.newOp(nil)
			f.reconcile(op)

			// The old process wiped everything, recorded Recovering, uploaded the snapshot and
			// bootstrapped... and the controller restarted before etcd answered on the target.
			target := f.targetFake()
			target.etcdStatusErr = errors.New("not yet")

			job := f.lastJob()
			_, err := f.run(job, 3)
			Expect(err).NotTo(HaveOccurred())
			Expect(f.marker().Step).To(Equal(ichorv1alpha1.EtcdRestoreStepRecovering))
			Expect(target.recovered).To(HaveLen(1))

			resets := map[string]int{}
			for name, fk := range f.fakes {
				resets[name] = fk.resetEtcdCalls
			}

			restarted := f.reconciler("")
			f.jobs = nil

			got := f.reconcileWith(restarted, op)
			Expect(got.Status.EtcdRestore.Step).To(Equal(ichorv1alpha1.EtcdRestoreStepRecovering), "the status catches up with the marker")

			next := f.lastJob()
			done, err := next.step(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeFalse(), "etcd on the target does not answer yet")
			Expect(target.recovered).To(HaveLen(1), "the snapshot is not uploaded twice")
			Expect(target.bootstrapRecoverCalls).To(Equal(1))

			target.etcdStatusErr = nil
			done, err = next.step(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeTrue())

			for name, fk := range f.fakes {
				Expect(fk.resetEtcdCalls).To(Equal(resets[name]), "no wipe after the recovery began")
			}
		})

		It("at Rejoining carries on without a new job", func() {
			f := newRestoreFixture("")
			f.wipeHooks()
			op := f.newOp(nil)
			f.reconcile(op)
			_, err := f.run(f.lastJob(), 5)
			Expect(err).NotTo(HaveOccurred())

			f.members(f.hosts()...)
			Expect(f.reconcile(op).Status.EtcdRestore.Step).To(Equal(ichorv1alpha1.EtcdRestoreStepReconciling))

			restarted := f.reconciler("")
			f.jobs = nil

			f.converged(fetchOperation(op).Status.EtcdRestore.ReconcileRequest)
			Expect(f.reconcileWith(restarted, op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
			Expect(f.jobs).To(BeEmpty())
		})

		It("finishes when only the status write of the end was lost", func() {
			f := newRestoreFixture("")
			f.wipeHooks()
			op := f.newOp(nil)
			f.reconcile(op)
			_, err := f.run(f.lastJob(), 5)
			Expect(err).NotTo(HaveOccurred())
			f.members(f.hosts()...)
			got := f.reconcile(op)

			// the marker is gone (finish deleted it) but the phase was never written
			Expect(etcdbackup.DeleteMarker(ctx, f.backup.store, testBackupBase)).To(Succeed())

			got = f.reconcile(got)
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
		})
	})

	Describe("pause on failure", func() {
		expectRefused := func(f *restoreFixture, op *ichorv1alpha1.TalosOperation, substr string) {
			got := f.reconcile(op)
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
			Expect(got.Status.FailureReason).To(ContainSubstring(substr))
			Expect(got.Status.FailureReason).To(ContainSubstring("nothing was changed"))
			Expect(f.marker()).To(BeNil())
			Expect(f.pinned()).To(BeFalse())
			Expect(f.jobs).To(BeEmpty())

			for _, fk := range f.fakes {
				Expect(fk.resetEtcdCalls).To(BeZero())
				Expect(fk.recovered).To(BeEmpty())
			}
		}

		It("refuses a snapshot that is not in the storage", func() {
			f := newRestoreFixture("")
			expectRefused(f, f.newOp(func(op *ichorv1alpha1.TalosOperation) {
				op.Spec.EtcdRestore.Snapshot = etcdbackup.ObjectName(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
			}), "not in the backup storage")
		})

		It("refuses a snapshot that fails its checksum and never wipes", func() {
			f := newRestoreFixture("")
			name, _ := f.putSnapshot(time.Date(2026, 6, 1, 11, 0, 0, 0, time.UTC), "good")

			pass, err := etcdbackup.DeriveKey(f.backup.bundle)
			Expect(err).NotTo(HaveOccurred())

			bad := validTestSnapshot("corrupt")
			bad[3] ^= 0xff
			f.putSealed(name, pass, bad)

			expectRefused(f, f.newOp(func(op *ichorv1alpha1.TalosOperation) { op.Spec.EtcdRestore.Snapshot = name }), "does not pass its check")
		})

		It("refuses a snapshot sealed with another cluster's bundle", func() {
			f := newRestoreFixture("")
			name := etcdbackup.ObjectName(time.Date(2026, 6, 1, 11, 30, 0, 0, time.UTC))

			f.putSealed(name, etcdbackup.Key{Current: "some-other-passphrase"}, validTestSnapshot("foreign"))

			expectRefused(f, f.newOp(func(op *ichorv1alpha1.TalosOperation) { op.Spec.EtcdRestore.Snapshot = name }), "secrets bundle")
		})

		It("refuses while a control plane cannot be reached", func() {
			f := newRestoreFixture("")
			delete(f.fakes, f.cps[restoreHostC].Name)

			expectRefused(f, f.newOp(nil), "cannot be reached")
		})

		It("refuses a snapshot older than a CA rotation of the cluster", func() {
			f := newRestoreFixture("")
			rot := &ichorv1alpha1.TalosOperation{
				GenerateName: "rot-", Namespace: tmNamespace,
				Spec: ichorv1alpha1.TalosOperationSpec{
					ClusterRef: corev1.LocalObjectReference{Name: f.backup.cluster.Name}, Kind: ichorv1alpha1.TalosOperationKindCARotation,
				},
			}
			Expect(k8sClient.Create(ctx, rot)).To(Succeed())

			expectRefused(f, f.newOp(nil), "rotated the cluster's CAs")
		})

		It("refuses a snapshot taken before a key rotation of the cluster finished", func() {
			f := newRestoreFixture("")
			rot := &ichorv1alpha1.TalosOperation{
				GenerateName: "keys-", Namespace: tmNamespace,
				Spec: ichorv1alpha1.TalosOperationSpec{
					ClusterRef: corev1.LocalObjectReference{Name: f.backup.cluster.Name}, Kind: ichorv1alpha1.TalosOperationKindKeyRotation,
					KeyRotation: &ichorv1alpha1.TalosOperationKeyRotationSpec{Targets: []string{ichorv1alpha1.KeyRotationTargetSecretbox}},
				},
			}
			Expect(k8sClient.Create(ctx, rot)).To(Succeed())

			// Finished after the snapshot (taken 2026-06-01) was.
			finished := metav1.Now()
			rot.Status.Phase, rot.Status.FinishedAt = ichorv1alpha1.TalosOperationPhaseCompleted, &finished
			rot.Status.KeyRotation = &ichorv1alpha1.TalosOperationKeyRotationStatus{Stage: ichorv1alpha1.KeyRotationStageDone}
			Expect(k8sClient.Status().Update(ctx, rot)).To(Succeed())

			expectRefused(f, f.newOp(nil), "service-account or secretbox key")
		})

		It("refuses while another restore is recorded in the storage", func() {
			f := newRestoreFixture("")
			Expect(etcdbackup.PutMarker(ctx, f.backup.store, testBackupBase, mustPass(f.backup.bundle), &etcdbackup.RestoreMarker{
				Namespace: tmNamespace, Operation: "someone-else", Step: ichorv1alpha1.EtcdRestoreStepWiping,
			})).To(Succeed())

			got := f.reconcile(f.newOp(nil))
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
			Expect(got.Status.FailureReason).To(ContainSubstring("someone-else"))
			Expect(f.pinned()).To(BeFalse())
			Expect(f.jobs).To(BeEmpty())
		})

		It("refuses a backup of another cluster", func() {
			f := newRestoreFixture("")
			other := newBackupFixture()
			otherBk := other.newBackup(nil)

			got := f.reconcile(f.newOp(func(op *ichorv1alpha1.TalosOperation) { op.Spec.EtcdRestore.BackupRef.Name = otherBk.Name }))
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
			Expect(got.Status.FailureReason).To(ContainSubstring("backs up cluster"))
		})

		It("a final error of the job is recorded in the marker, shown on the operation, and never retried", func() {
			f := newRestoreFixture("")
			f.wipeHooks()
			op := f.newOp(nil)
			f.reconcile(op)

			f.targetFake().bootstrapRecoverErr = errors.New("etcd is not configured, bootstrap is not possible")

			job := f.lastJob()
			_, _ = job.step(ctx)
			_, err := job.step(ctx)
			Expect(err).To(HaveOccurred())
			job.fail(ctx, err)

			Expect(f.marker().Failure).To(ContainSubstring("bootstrapping etcd from the snapshot"))

			got := f.reconcile(op)
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
			Expect(got.Status.FailureReason).To(ContainSubstring("not possible"))

			f.targetFake().bootstrapRecoverErr = nil
			f.jobs = nil
			got = f.reconcile(op)
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
			Expect(f.jobs).To(BeEmpty(), "a failed restore is not retried by itself")
			Expect(f.targetFake().bootstrapRecoverCalls).To(Equal(1))
		})

		It("a node that is not ready for recovery yet is waited for, not failed", func() {
			f := newRestoreFixture("")
			f.wipeHooks()
			op := f.newOp(nil)
			f.reconcile(op)

			f.targetFake().recoverErr = errors.Join(ErrRecoveryNotReady, errors.New("etcd service is not ready for recovery yet"))

			job := f.lastJob()
			_, err := f.run(job, 4)
			Expect(err).NotTo(HaveOccurred())
			Expect(f.marker().Step).To(Equal(ichorv1alpha1.EtcdRestoreStepRecovering))
			Expect(f.marker().Failure).To(BeEmpty())

			f.targetFake().recoverErr = nil
			done, err := f.run(job, 4)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeTrue())
		})

		It("gives up at the deadline and says what it was waiting for", func() {
			f := newRestoreFixture("")
			op := f.newOp(nil)
			f.reconcile(op)

			f.targetFake().resetEtcdErr = errors.New("permission denied")

			job := f.lastJob()
			_, _ = job.step(ctx)
			job.now = func() time.Time { return time.Now().Add(etcdRestoreTailTimeout + time.Minute) }

			_, err := job.step(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("did not finish"))
			Expect(err.Error()).To(ContainSubstring("permission denied"))
		})

		It("a tampered pinned snapshot stops the restore before anything is uploaded", func() {
			f := newRestoreFixture("")
			f.wipeHooks()
			op := f.newOp(nil)
			f.reconcile(op)

			f.backup.store.objects[etcdbackup.PinnedKey(testBackupBase)] = []byte("not an age file")

			job := f.lastJob()
			_, _ = job.step(ctx)
			_, err := job.step(ctx)
			Expect(err).To(HaveOccurred())
			Expect(f.targetFake().recovered).To(BeEmpty())
		})

		It("a restore that has not finished within its overall time pauses", func() {
			f := newRestoreFixture("")
			op := f.newOp(nil)
			f.reconcile(op)

			m := f.marker()
			m.StartedAt = time.Now().Add(-etcdRestoreOverallTimeout - time.Minute)
			Expect(etcdbackup.PutMarker(ctx, f.backup.store, testBackupBase, mustPass(f.backup.bundle), m)).To(Succeed())

			got := f.reconcile(op)
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
			Expect(got.Status.FailureReason).To(ContainSubstring("did not finish"))
		})

		It("a missing marker after the start is reported, not guessed at", func() {
			f := newRestoreFixture("")
			op := f.newOp(nil)
			f.reconcile(op)
			Expect(etcdbackup.DeleteMarker(ctx, f.backup.store, testBackupBase)).To(Succeed())

			got := f.reconcile(op)
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
			Expect(got.Status.FailureReason).To(ContainSubstring("marker is gone"))
		})
	})

	Describe("the node the controller runs on", func() {
		It("is left for after the recovery, then wiped by the job through Talos alone once the others joined", func() {
			f := newRestoreFixture(restoreHostA)
			f.wipeHooks()
			op := f.newOp(nil)

			got := f.reconcile(op)
			Expect(got.Status.EtcdRestore.Target).NotTo(Equal(f.cps[restoreHostA].Name), "not the controller's own node")
			Expect(got.Status.EtcdRestore.Message).To(ContainSubstring(f.cps[restoreHostA].Name))

			job := f.lastJob()
			Expect(job.order).To(HaveLen(2))
			Expect(job.own).NotTo(BeNil())
			Expect(job.peers).To(ConsistOf(restoreHostB, restoreHostC))

			// Recovered, but the others have not joined: the own node is left alone.
			f.members(f.target())
			done, err := f.run(job, 8)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeFalse())
			Expect(f.marker().Step).To(Equal(ichorv1alpha1.EtcdRestoreStepRejoining))
			Expect(f.fake(restoreHostA).resetEtcdCalls).To(BeZero(), "wiping it would stop the controller before the recovery")
			Expect(f.targetFake().recovered).To(HaveLen(1))

			// Both others are members: the intent is recorded, then the wipe is sent, once.
			f.members(restoreHostB, restoreHostC)
			_, err = job.step(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(f.fake(restoreHostA).resetEtcdCalls).To(Equal(1))
			Expect(f.marker().OwnWipeAt.IsZero()).To(BeFalse(), "recorded before the wipe was sent")

			// It reboots wiped (the fake's data is gone), and the job is done.
			done, err = job.step(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeTrue())
			Expect(f.marker().OwnDone).To(BeTrue())

			// The reconcile does not wipe it either, and goes on once it is a member.
			f.reconcile(op)
			Expect(f.fake(restoreHostA).resetEtcdCalls).To(Equal(1))

			f.members(f.hosts()...)
			Expect(f.reconcile(op).Status.EtcdRestore.Step).To(Equal(ichorv1alpha1.EtcdRestoreStepReconciling))
		})

		It("continues after a process died with the wipe recorded: waits while unreachable, asks again when it is stale", func() {
			f := newRestoreFixture(restoreHostA)
			f.wipeHooks()
			op := f.newOp(nil)
			f.reconcile(op)
			f.members(f.target())

			_, err := f.run(f.lastJob(), 8)
			Expect(err).NotTo(HaveOccurred())

			// The wipe was recorded, and the process died before (or while) sending it.
			f.members(restoreHostB, restoreHostC)
			m := f.marker()
			m.OwnWipeAt = time.Now().Add(-time.Minute).UTC()
			Expect(etcdbackup.PutMarker(ctx, f.backup.store, testBackupBase, mustPass(f.backup.bundle), m)).To(Succeed())

			f.jobs = nil
			r2 := f.reconciler(restoreHostB) // the replacement runs elsewhere; the marker still names the own node
			f.reconcileWith(r2, op)

			job := f.lastJob()
			Expect(job.own).NotTo(BeNil())

			_, err = job.step(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(f.fake(restoreHostA).resetEtcdCalls).To(BeZero(), "a recent request is trusted to be underway")

			// An hour later nothing happened and it still has data: ask again.
			job.now = func() time.Time { return time.Now().Add(time.Hour) }
			_, err = job.step(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(f.fake(restoreHostA).resetEtcdCalls).To(Equal(1))

			// Unreachable while it reboots: wait; back and wiped: done.
			f.fake(restoreHostA).etcdDataErr = errors.New("connection refused")
			done, err := job.step(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeFalse())

			f.fake(restoreHostA).etcdDataErr = nil
			done, err = job.step(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeTrue())
		})

		It("has no own wipe when the controller does not run on a control plane", func() {
			f := newRestoreFixture("a-worker")
			f.wipeHooks()
			op := f.newOp(nil)
			f.reconcile(op)

			job := f.lastJob()
			Expect(job.own).To(BeNil())

			f.members(f.hosts()...)
			done, err := f.run(job, 8)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeTrue())
			Expect(f.marker().Own).To(BeEmpty())
		})

		It("does not wipe a control plane that is a member", func() {
			f := newRestoreFixture(restoreHostA)
			f.wipeHooks()
			op := f.newOp(nil)
			f.reconcile(op)
			f.members(f.hosts()...)
			done, err := f.run(f.lastJob(), 8)
			Expect(err).NotTo(HaveOccurred())
			Expect(done).To(BeTrue())

			f.reconcile(op)
			Expect(f.fake(restoreHostA).resetEtcdCalls).To(BeZero())
		})

		It("says that the restore cannot finish by itself when it runs on the only control plane", func() {
			b := newBackupFixture()
			f := &restoreFixture{backup: b, cps: map[string]*ichorv1alpha1.TalosMachine{}, fakes: map[string]*fakeTalosClient{}}
			m := newTestControlPlane(b.cluster.Name, "9")
			setHostname(m, restoreHostA)
			f.cps[restoreHostA] = m
			f.fakes[m.Name] = &fakeTalosClient{etcdData: true}
			f.bk = b.newBackup(nil)
			f.snapshot, f.snapshotBody = f.putSnapshot(time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC), "solo")
			f.r = f.reconciler(restoreHostA)

			got := f.reconcile(f.newOp(nil))
			Expect(got.Status.EtcdRestore.Message).To(ContainSubstring("ichor restore --recover-cluster"))
			Expect(got.Status.EtcdRestore.Target).To(Equal(m.Name))
		})
	})

	Describe("deleting the operation", func() {
		It("is held while etcd is being wiped or recovered", func() {
			f := newRestoreFixture("")
			op := f.newOp(nil)
			f.reconcile(op)

			Expect(k8sClient.Delete(ctx, op)).To(Succeed())

			got := f.reconcile(op)
			Expect(got.DeletionTimestamp.IsZero()).To(BeFalse())
			Expect(controllerutil.ContainsFinalizer(got, etcdRestoreFinalizer)).To(BeTrue())
			Expect(f.marker()).NotTo(BeNil())
		})

		It("clears the marker and the pinned copy once the cluster is restored, whether or not the rest finished", func() {
			f := newRestoreFixture("")
			f.wipeHooks()
			op := f.newOp(nil)
			f.reconcile(op)
			_, err := f.run(f.lastJob(), 5)
			Expect(err).NotTo(HaveOccurred())
			f.members(f.target())
			Expect(f.reconcile(op).Status.EtcdRestore.Step).To(Equal(ichorv1alpha1.EtcdRestoreStepRejoining))

			Expect(k8sClient.Delete(ctx, op)).To(Succeed())
			_, err = reconcileOperation(f.r, op)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(op), &ichorv1alpha1.TalosOperation{}))
			}).Should(BeTrue())
			Expect(f.marker()).To(BeNil())
			Expect(f.pinned()).To(BeFalse())
		})

		It("clears a refused restore's finalizer without touching a marker that is not its own", func() {
			f := newRestoreFixture("")
			Expect(etcdbackup.PutMarker(ctx, f.backup.store, testBackupBase, mustPass(f.backup.bundle), &etcdbackup.RestoreMarker{
				Namespace: tmNamespace, Operation: "someone-else", Step: ichorv1alpha1.EtcdRestoreStepWiping,
			})).To(Succeed())

			op := f.newOp(nil)
			f.reconcile(op)
			Expect(k8sClient.Delete(ctx, op)).To(Succeed())
			_, err := reconcileOperation(f.r, op)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(op), &ichorv1alpha1.TalosOperation{}))
			}).Should(BeTrue())
			Expect(f.marker()).NotTo(BeNil())
			Expect(f.marker().Operation).To(Equal("someone-else"))
		})
	})

	Describe("the machine controller's acknowledgement", func() {
		It("echoes the reconcile request annotation into status.observedReconcileRequest", func() {
			bundle := newTestBundle("v1.14")
			secret := newTestSecret(bundle)
			cluster := newTestCluster(secret.Name, "v1.14")
			m := newTestMachine(cluster.Name, true)

			rendered, _, err := render.Render(cluster, m, bundle, nil, nil)
			Expect(err).NotTo(HaveOccurred())

			fake := &fakeTalosClient{current: rendered}
			r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

			_, err = reconcileMachine(r, m)
			Expect(err).NotTo(HaveOccurred())
			Expect(fetchMachine(m).Status.ObservedReconcileRequest).To(BeEmpty())

			got := fetchMachine(m)
			got.Annotations = map[string]string{ichorv1alpha1.EtcdRestoreReconcileAnnotation: "token-1"}
			Expect(k8sClient.Update(ctx, got)).To(Succeed())

			_, err = reconcileMachine(r, m)
			Expect(err).NotTo(HaveOccurred())
			Expect(fetchMachine(m).Status.ObservedReconcileRequest).To(Equal("token-1"))
		})
	})
})

// mustPass is the backup passphrase of bundle, which also keys the restore marker's HMAC.
func mustPass(b *secrets.Bundle) etcdbackup.Key {
	pass, err := etcdbackup.DeriveKey(b)
	Expect(err).NotTo(HaveOccurred())

	return pass
}
