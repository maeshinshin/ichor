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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// Hardware finding (lane E): an aggregator CA rotation turned Completed 13 s before the
// last kube-apiserver restart on the endpoint control plane was over, because the pass applied a stage to every
// control plane and verified only that the Talos API answered. Talos restarts kube-apiserver some seconds after
// the apply, so each stage waits for every control plane's kube-apiserver to run in a new container.
var _ = Describe("TalosOperation ca-rotation of the aggregator CA: kube-apiserver restarts", func() {
	controlPlanes := func(f *caRotFixture) []*rotNode {
		var out []*rotNode

		for _, m := range f.machines {
			if m.Spec.Role == roleControlPlane {
				out = append(out, f.world.nodes[m.Name])
			}
		}

		return out
	}

	It("completes only after every control plane's kube-apiserver restarted for every stage", func() {
		f := newCARotFixture()
		f.targets = []string{ichorv1alpha1.CARotationTargetAggregator}
		f.world.apiRestartReads = 4

		op, _ := f.runUntil(f.newOp(), completed)

		for _, n := range controlPlanes(f) {
			Expect(n.restartIn).To(BeZero(), "a restart was still to come when the operation turned Completed")
			Expect(n.instance).To(BeNumerically(">=", 2), "kube-apiserver restarted in each applying stage")
		}

		Expect(op.Status.CARotation.APIServerInstances).To(BeEmpty())
	})

	It("waits, with the reason, while a kube-apiserver still runs in the container it ran before the apply", func() {
		f := newCARotFixture()
		f.targets = []string{ichorv1alpha1.CARotationTargetAggregator}
		f.world.apiRestartReads = 1_000

		op := f.newOp()

		var seen string

		for range 12 {
			op = f.step(op)

			if cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation); cond != nil &&
				strings.Contains(cond.Message, "kube-apiserver") && strings.Contains(cond.Message, "restart") {
				seen = cond.Message
			}
		}

		Expect(seen).NotTo(BeEmpty())
		Expect(op.Status.Phase).NotTo(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
	})

	It("goes on once a stage that changed nothing for kube-apiserver has waited out the grace period", func() {
		f := newCARotFixture()
		f.targets = []string{ichorv1alpha1.CARotationTargetAggregator}
		f.world.apiNoRestart = true

		op := f.newOp()

		for range 120 {
			op = f.step(op)
			if completed(op) {
				return
			}

			if cond := apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation); cond != nil &&
				strings.Contains(cond.Message, "ran before the apply") {
				Expect(op.Status.CARotation.AppliedAt).NotTo(BeNil())

				old := metav1.NewTime(time.Now().Add(-2 * apiServerRestartGrace))
				op.Status.CARotation.AppliedAt = &old
				Expect(k8sClient.Status().Update(ctx, op)).To(Succeed())
			}
		}

		Fail("the rotation did not complete after the grace period")
	})

	It("does not look at kube-apiserver when the aggregator CA is not rotated", func() {
		f := newCARotFixture()
		f.targets = []string{ichorv1alpha1.CARotationTargetEtcd}
		f.world.apiRestartReads = 1_000

		f.runUntil(f.newOp(), completed)
	})
})
