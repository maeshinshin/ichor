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
	"fmt"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	policyv1 "k8s.io/api/policy/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

const (
	shutdownBootBefore = "boot-before"
	shutdownOwnNode    = "ctrl-node"
	shutdownBootAfter  = "boot-after"
)

// orderedFake logs the name of every machine whose Shutdown was called, in
// one slice shared by the whole cluster, so a test can assert the order
// across machines (a per-machine fake cannot).
type orderedFake struct {
	*fakeTalosClient
	name string
	log  *[]string
}

func (o *orderedFake) Shutdown(ctx context.Context, force bool) error {
	*o.log = append(*o.log, o.name)

	return o.fakeTalosClient.Shutdown(ctx, force)
}

// shutdownFixture is a cluster of three control planes and two workers whose
// Talos API is faked per machine.
type shutdownFixture struct {
	cluster *ichorv1alpha1.TalosCluster
	cps     []*ichorv1alpha1.TalosMachine
	workers []*ichorv1alpha1.TalosMachine
	fakes   map[string]*fakeTalosClient
	log     []string
	r       *TalosOperationReconciler
}

func newShutdownFixture() *shutdownFixture {
	f := &shutdownFixture{fakes: map[string]*fakeTalosClient{}}

	// The unreachable window is tested on its own; elsewhere an unreachable worker counts at once.
	shutdownUnreachableWindow, shutdownNow = 0, time.Now
	secret := newTestSecret(newTestBundle("v1.14"))
	f.cluster = newTestCluster(secret.Name, "v1.14")

	for _, n := range []string{"sd-cp1", "sd-cp2", "sd-cp3"} {
		f.cps = append(f.cps, newOpTestMachine(f.cluster.Name, roleControlPlane, n))
	}

	for _, n := range []string{"sd-w1", "sd-w2"} {
		f.workers = append(f.workers, newOpTestMachine(f.cluster.Name, roleWorker, n))
	}

	for _, m := range slices.Concat(f.cps, f.workers) {
		f.fakes[m.Name] = &fakeTalosClient{bootID: shutdownBootBefore}
	}

	f.r = &TalosOperationReconciler{
		Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
		TalosClient: func(_ context.Context, _ *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, _ *secrets.Bundle) (TalosClient, error) {
			return &orderedFake{fakeTalosClient: f.fakes[m.Name], name: m.Name, log: &f.log}, nil
		},
	}

	return f
}

func (f *shutdownFixture) newOp(kind string) *ichorv1alpha1.TalosOperation {
	op := &ichorv1alpha1.TalosOperation{}
	op.GenerateName, op.Namespace = "sd-op-", tmNamespace
	op.Spec.ClusterRef.Name, op.Spec.Kind = f.cluster.Name, kind
	Expect(k8sClient.Create(ctx, op)).To(Succeed())

	return op
}

func (f *shutdownFixture) reconcile(op *ichorv1alpha1.TalosOperation) *ichorv1alpha1.TalosOperation {
	return reconcileFetch(f.r, op)
}

// halt makes the named machines unreachable, as a stopped node is.
func (f *shutdownFixture) halt(ms ...*ichorv1alpha1.TalosMachine) {
	for _, m := range ms {
		f.fakes[m.Name].bootIDErr = errors.New("connection refused")
	}
}

// powerOn brings the machines back on a new boot.
func (f *shutdownFixture) powerOn(ms ...*ichorv1alpha1.TalosMachine) {
	for _, m := range ms {
		f.fakes[m.Name].bootIDErr = nil
		f.fakes[m.Name].bootID = shutdownBootAfter
	}
}

func machineNames(ms []*ichorv1alpha1.TalosMachine) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Name)
	}

	return out
}

var _ = Describe("TalosOperation shutdown", func() {
	It("stops workers gracefully first, and control planes forced in one burst only after the workers are down", func() {
		f := newShutdownFixture()
		op := f.newOp(ichorv1alpha1.TalosOperationKindShutdown)

		got := f.reconcile(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))
		Expect(f.log).To(ConsistOf(machineNames(f.workers)))

		for _, w := range f.workers {
			Expect(f.fakes[w.Name].shutdownCalls).To(Equal([]bool{false}), "workers are drained, not forced")
		}

		// A worker is still answering: the control planes must wait.
		f.halt(f.workers[0])
		got = f.reconcile(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))
		Expect(f.log).To(HaveLen(len(f.workers)), "no control plane is touched while a worker is up")

		f.halt(f.workers[1])
		got = f.reconcile(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseShutDown))
		Expect(f.log[len(f.workers):]).To(ConsistOf(machineNames(f.cps)), "every control plane is asked in the last stage")

		for _, cp := range f.cps {
			Expect(f.fakes[cp.Name].shutdownCalls).To(Equal([]bool{true}), "control planes are forced: the API is about to go")
			Expect(got.Status.Machines).To(ContainElement(And(
				HaveField("Name", cp.Name), HaveField("Phase", ichorv1alpha1.TalosOperationMachinePhaseInProgress), HaveField("BootID", shutdownBootBefore),
			)))
		}

		// Nothing is sent twice.
		f.reconcile(op)
		Expect(f.log).To(HaveLen(len(f.workers) + len(f.cps)))
	})

	It("counts a worker as stopped only after it stayed unreachable for the window", func() {
		f := newShutdownFixture()
		now := time.Now()
		shutdownUnreachableWindow, shutdownNow = time.Minute, func() time.Time { return now }

		DeferCleanup(func() { shutdownUnreachableWindow, shutdownNow = 0, time.Now })

		op := f.newOp(ichorv1alpha1.TalosOperationKindShutdown)
		f.reconcile(op)

		f.halt(f.workers...)
		Expect(f.reconcile(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))

		// A blip: w1 answers again on its old boot, so its clock starts over.
		now = now.Add(45 * time.Second)
		f.fakes[f.workers[0].Name].bootIDErr = nil
		f.reconcile(op)
		f.halt(f.workers[0])

		now = now.Add(45 * time.Second)
		got := f.reconcile(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress), "w1 has been away for less than the window")
		Expect(f.log).To(HaveLen(len(f.workers)), "no control plane is touched yet")

		now = now.Add(2 * time.Minute)
		Expect(f.reconcile(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseShutDown))
	})

	It("pauses naming the PodDisruptionBudget when the workers' drain stalls", func() {
		f := newShutdownFixture()
		now := time.Now()
		shutdownNow = func() time.Time { return now }

		DeferCleanup(func() { shutdownNow = time.Now })

		pdb := &policyv1.PodDisruptionBudget{}
		pdb.Name, pdb.Namespace = "sd-stuck-pdb", testIchorNamespace
		pdb.Spec.MinAvailable = &intstr.IntOrString{IntVal: 1}
		pdb.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": "sd"}}
		Expect(k8sClient.Create(ctx, pdb)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, pdb)).To(Succeed()) })

		pdb.Status.ExpectedPods, pdb.Status.CurrentHealthy, pdb.Status.DesiredHealthy = 1, 1, 1
		Expect(k8sClient.Status().Update(ctx, pdb)).To(Succeed())

		op := f.newOp(ichorv1alpha1.TalosOperationKindShutdown)
		Expect(f.reconcile(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))

		now = now.Add(shutdownDrainStall + time.Minute)
		got := f.reconcile(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(got.Status.FailureReason).To(ContainSubstring(testIchorNamespace + "/sd-stuck-pdb"))
		Expect(f.log).To(HaveLen(len(f.workers)), "no control plane is touched")
	})

	It("does not stop a machine that is already unreachable", func() {
		f := newShutdownFixture()
		f.halt(f.workers[0])

		op := f.newOp(ichorv1alpha1.TalosOperationKindShutdown)
		f.reconcile(op)

		Expect(f.log).To(Equal([]string{f.workers[1].Name}))
	})

	It("keeps the controller's own node for the last burst, and asks it last", func() {
		f := newShutdownFixture()
		f.workers[0].Spec.Hostname = shutdownOwnNode
		Expect(k8sClient.Update(ctx, f.workers[0])).To(Succeed())
		f.r.NodeName = shutdownOwnNode

		op := f.newOp(ichorv1alpha1.TalosOperationKindShutdown)
		f.reconcile(op)
		Expect(f.log).To(Equal([]string{f.workers[1].Name}), "the node running this controller is not drained first")

		f.halt(f.workers[1])
		got := f.reconcile(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseShutDown))
		Expect(f.log).To(HaveLen(len(f.workers) + len(f.cps)))
		Expect(f.log[len(f.log)-1]).To(Equal(f.workers[0].Name), "its own node is asked after everything else")
		Expect(f.fakes[f.workers[0].Name].shutdownCalls).To(Equal([]bool{true}))
	})

	It("finds the controller's own node through what Talos reports when spec.hostname is empty", func() {
		f := newShutdownFixture()
		f.fakes[f.workers[0].Name].nodeName = shutdownOwnNode
		f.r.NodeName = shutdownOwnNode

		op := f.newOp(ichorv1alpha1.TalosOperationKindShutdown)
		f.reconcile(op)
		Expect(f.log).To(Equal([]string{f.workers[1].Name}), "the node running this controller is not drained first")
	})

	It("does not record a machine whose Shutdown request failed, so the next reconcile retries it", func() {
		f := newShutdownFixture()
		f.fakes[f.workers[0].Name].shutdownErr = errors.New("rpc failed")

		op := f.newOp(ichorv1alpha1.TalosOperationKindShutdown)
		_, err := reconcileOperation(f.r, op)
		Expect(err).To(HaveOccurred())
		Expect(fetchOperation(op).Status.Machines).NotTo(ContainElement(HaveField("Name", f.workers[0].Name)))

		f.fakes[f.workers[0].Name].shutdownErr = nil
		f.reconcile(op)
		Expect(f.fakes[f.workers[0].Name].shutdownCalls).To(HaveLen(2))
	})

	It("refuses every other disruptive operation until the machines are back, then lets them through", func() {
		f := newShutdownFixture()
		op := f.newOp(ichorv1alpha1.TalosOperationKindShutdown)
		f.reconcile(op)

		upgrade := f.newOp(ichorv1alpha1.TalosOperationKindTalos)
		advancer := &fakeAdvancer{}
		f.r.Advance = advancer.Advance

		got := f.reconcile(upgrade)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(advancer.calls).To(BeEmpty())

		// The machine controller's own gate refuses too, including the removal path.
		mr := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient}
		for _, completesTransition := range []bool{false, true} {
			_, err := mr.acquireDisruptiveLock(ctx, f.cluster, f.cps[0], "shutdown-test", completesTransition)
			var shuttingDown *shuttingDownError
			Expect(err).To(MatchError(func(e error) bool { return errors.As(e, &shuttingDown) },
				fmt.Sprintf("a shutting-down error when completesTransition=%v", completesTransition)))
		}

		f.halt(f.workers...)
		f.reconcile(op)
		f.halt(f.cps...)
		Expect(fetchOperation(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseShutDown))

		got = f.reconcile(upgrade)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused), "still marked while the machines are off")
		Expect(advancer.calls).To(BeEmpty())

		// Only some machines back is not enough.
		f.powerOn(f.cps...)
		got = f.reconcile(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseShutDown))

		f.powerOn(f.workers...)
		got = f.reconcile(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))

		lock, err := mr.acquireDisruptiveLock(ctx, f.cluster, f.cps[0], "shutdown-test", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(lock.Release(ctx, "shutdown-test")).To(Succeed())

		got = f.reconcile(upgrade)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))
		Expect(advancer.calls).NotTo(BeEmpty())
	})

	It("defers a removal while the cluster is shutting down and asks to be tried again, since nothing else wakes it", func() {
		f := newShutdownFixture()
		f.reconcile(f.newOp(ichorv1alpha1.TalosOperationKindShutdown))

		cp := fetchMachine(f.cps[0])
		controllerutil.AddFinalizer(cp, machineRemovalFinalizer)
		Expect(k8sClient.Update(ctx, cp)).To(Succeed())
		Expect(k8sClient.Delete(ctx, cp)).To(Succeed())

		mr := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace}
		res, err := reconcileMachine(mr, cp)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(nodeWaitInterval))
		Expect(apimeta.FindStatusCondition(fetchMachine(cp).Status.Conditions, conditionRemoval)).To(
			HaveField("Message", ContainSubstring("removal deferred")))
		Expect(controllerutil.ContainsFinalizer(fetchMachine(cp), machineRemovalFinalizer)).To(BeTrue())
	})

	It("lets only one shutdown run per cluster", func() {
		f := newShutdownFixture()
		first := f.newOp(ichorv1alpha1.TalosOperationKindShutdown)
		f.reconcile(first)

		second := f.newOp(ichorv1alpha1.TalosOperationKindShutdown)
		got := f.reconcile(second)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(got.Status.Machines).To(BeEmpty())
	})
})

// slowShutdownTalos is a fake Talos client whose calls each take delay, the way a stop pass that visits
// several nodes runs long. honourCtx makes a call give up at its context, as gRPC does; without it a call
// sleeps regardless, and a Shutdown then reports that the node did not answer, which is what a stuck call looks like.
type slowShutdownTalos struct {
	*orderedFake
	delay     time.Duration
	honourCtx bool
}

func (s *slowShutdownTalos) BootID(ctx context.Context) (string, error) {
	if err := s.wait(ctx); err != nil {
		return "", err
	}

	return s.orderedFake.BootID(ctx)
}

func (s *slowShutdownTalos) Shutdown(ctx context.Context, force bool) error {
	if err := s.wait(ctx); err != nil {
		return err
	}

	if !s.honourCtx {
		return errors.New("the node did not answer in time")
	}

	return s.orderedFake.Shutdown(ctx, force)
}

func (s *slowShutdownTalos) wait(ctx context.Context) error {
	if !s.honourCtx {
		time.Sleep(s.delay)

		return nil
	}

	select {
	case <-time.After(s.delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// slowStopFixture is the shutdown fixture with a short lease and every Talos call slowed by delay.
func slowStopFixture(delay time.Duration, honourCtx bool) *shutdownFixture {
	f := newShutdownFixture()
	f.r.lockDuration, f.r.renewInterval = testLeaseDuration, testRenewEvery
	f.r.TalosClient = func(_ context.Context, _ *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, _ *secrets.Bundle) (TalosClient, error) {
		return &slowShutdownTalos{
			orderedFake: &orderedFake{fakeTalosClient: f.fakes[m.Name], name: m.Name, log: &f.log},
			delay:       delay, honourCtx: honourCtx,
		}, nil
	}

	return f
}

var _ = Describe("a stop pass that outlasts the cluster lease", func() {
	It("keeps the cluster lock for as long as the pass runs", func() {
		// Six Talos calls at 400ms each take about 2.4s, past the one-second lease the top-of-reconcile renewal gives.
		f := slowStopFixture(400*time.Millisecond, true)
		op := f.newOp(ichorv1alpha1.TalosOperationKindShutdown)

		done := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(done)

			f.reconcile(op)
		}()

		// Steal only once the pass holds the lock, so the window is the pass's and not the start-up race.
		Eventually(func() string {
			holder, _, _ := opTestLock(f.cluster).Holder(ctx)

			return holder
		}, lockWaitBound, testRenewEvery).Should(Equal(operationLockIdentity(op)))

		stolen := tryStealLock(f.cluster, stealWindow)
		Eventually(done, lockWaitBound).Should(BeClosed())
		Expect(stolen).To(BeFalse(), "the stop pass must renew the lock while it runs, so another operation cannot take it mid-stop")
	})

	It("pauses a stop pass that runs past its bound, because it is stuck rather than slow", func() {
		DeferCleanup(func(saved time.Duration) { etcdDialTimeout = saved }, etcdDialTimeout)
		DeferCleanup(func(saved time.Duration) { shutdownPassSlack = saved }, shutdownPassSlack)
		// The bound is (3*2+2*3) calls at 50ms each, with no slack: 600ms. The stuck Shutdown returns after one second.
		etcdDialTimeout, shutdownPassSlack = 50*time.Millisecond, 0
		f := slowStopFixture(time.Second, false)
		op := f.newOp(ichorv1alpha1.TalosOperationKindShutdown)

		got := f.reconcile(op)

		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(got.Status.FailureReason).To(ContainSubstring("stopping the cluster did not finish within"))
		_, held, err := opTestLock(f.cluster).Holder(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(held).To(BeFalse(), "a stuck stop pass pauses and must not keep the cluster lock")
	})
})
