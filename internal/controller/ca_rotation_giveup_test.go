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
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
)

var _ = Describe("a CA rotation whose step keeps failing", func() {
	BeforeEach(func() {
		saved := rotationGiveUpAfter
		rotationGiveUpAfter = 2 * time.Second
		DeferCleanup(func() { rotationGiveUpAfter = saved })
	})

	// stepUntilFailed steps op until it gives up: paused with a FailureReason.
	stepUntilFailed := func(f *caRotFixture, op *ichorv1alpha1.TalosOperation) *ichorv1alpha1.TalosOperation {
		var cur *ichorv1alpha1.TalosOperation

		Eventually(func() string {
			cur = f.step(op)
			return cur.Status.FailureReason
		}).WithTimeout(30 * time.Second).WithPolling(50 * time.Millisecond).ShouldNot(BeEmpty())

		return cur
	}

	It("pauses on a failed step, releases the cluster lock to the next holder, and resumes on the retry annotation", func() {
		f := newCARotFixture()
		firstCP := f.machines[0].Name
		f.world.nodes[firstCP].down = true
		op := f.newOp()

		op = stepUntilFailed(f, op)
		Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(op.Status.FailureReason).To(ContainSubstring("gave up after being blocked"))
		Expect(op.Status.FailureReason).To(ContainSubstring("connection refused"))
		Expect(drainEvents(f.recorder)).To(ContainElement(ContainSubstring("Warning StepFailed")))

		// The holder is stopped, so the next holder takes the lock over instead of waiting out the lease.
		lock := clusterlock.New(k8sClient, types.NamespacedName{Namespace: f.cluster.Namespace, Name: f.cluster.Name},
			clusterlock.DefaultDuration)
		err := lock.Acquire(ctx, "ichor-controller/test/next-holder")
		Expect(err).To(HaveOccurred())

		retry, err := releaseIfHolderStopped(ctx, k8sClient, lock, err)
		Expect(err).NotTo(HaveOccurred())
		Expect(retry).To(BeTrue())
		Expect(lock.Acquire(ctx, "ichor-controller/test/next-holder")).To(Succeed())
		Expect(lock.Release(ctx, "ichor-controller/test/next-holder")).To(Succeed())

		// The retry annotation resumes the rotation once the cause has cleared.
		f.world.nodes[firstCP].down = false

		cur := fetchOperation(op)
		base := cur.DeepCopy()
		if cur.Annotations == nil {
			cur.Annotations = map[string]string{}
		}
		cur.Annotations[ichorv1alpha1.OperationRetryAnnotation] = ichorv1alpha1.OperationRetryRequested
		Expect(k8sClient.Patch(ctx, cur, ctrlclient.MergeFrom(base))).To(Succeed())

		op = f.step(cur)
		Expect(op.Status.FailureReason).To(BeEmpty())
		Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))
		Expect(fetchOperation(op).Annotations).NotTo(HaveKey(ichorv1alpha1.OperationRetryAnnotation))
	})

	It("refuses other disruptive work while the stage it recorded is half-applied", func() {
		f := newCARotFixture()
		op, _ := f.runUntil(f.newOp(), atStage(ichorv1alpha1.CARotationStageAccepting))
		f.world.failApply = func(string, int) error { return errors.New("the apply timed out") }

		op = stepUntilFailed(f, op)
		Expect(op.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(op.Status.CARotation.Stage).To(Equal(ichorv1alpha1.CARotationStageAccepting))

		// The rotation still counts as under way, so the stage guards refuse another operation and
		// stop every machine's reconcile, whichever holds the lock.
		Expect(activeCARotation(ctx, k8sClient, f.cluster.Namespace, f.cluster.Name)).To(Equal(op.Name))

		err := shutdownBlocker(ctx, k8sClient, f.cluster.Namespace, f.cluster.Name, nil)
		_, refused := errors.AsType[*shuttingDownError](err)
		Expect(refused).To(BeTrue())
	})
})
