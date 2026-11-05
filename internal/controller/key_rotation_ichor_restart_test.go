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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// Hardware: the pods of Ichor itself held projected tokens signed by the old
// service-account key when it was dropped, so the controller running the rotation crashlooped. The controller
// and ichor-ui are restarted once the new key signs, and the old key is not dropped before they have rolled out.
var _ = Describe("TalosOperation key-rotation, Ichor's own pods", func() {
	var controllerDep, uiDep *appsv1.Deployment

	// deployment creates a one-replica Deployment in the controller's namespace whose rollout is finished.
	deployment := func(name string, labels map[string]string) *appsv1.Deployment {
		d := &appsv1.Deployment{
			Name: name, Namespace: tmNamespace, Labels: labels,
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"key-rot-test": name}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"key-rot-test": name}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "registry.example/key-rotation:v1"}}},
				},
			},
		}
		Expect(k8sClient.Create(ctx, d)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, d))).To(Succeed()) })

		setRollout(d, true)

		return d
	}

	fresh := func(d *appsv1.Deployment) *appsv1.Deployment {
		GinkgoHelper()

		out := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: d.Namespace, Name: d.Name}, out)).To(Succeed())

		return out
	}

	marker := func(d *appsv1.Deployment) string {
		return fresh(d).Spec.Template.Annotations[keyRotationRestartMarker]
	}

	BeforeEach(func() {
		controllerDep = deployment("key-rot-controller", map[string]string{controlPlaneLabel: valueControllerMgr})
		uiDep = deployment("key-rot-ui", map[string]string{"app.kubernetes.io/component": "ui"})
	})

	It("restarts the controller and ichor-ui once the new key signs, and keeps the old key until they have rolled out", func() {
		f := newKeyRotFixture(fastBundle())
		op := f.newOp(saOnly)

		op, _ = f.runUntil(op, atKeyStage(ichorv1alpha1.KeyRotationStageIssuing))
		Expect(marker(controllerDep)).To(BeEmpty(), "Ichor's pods are restarted when the new key signs, not before")
		Expect(marker(uiDep)).To(BeEmpty())

		op, _ = f.runUntil(op, atKeyStage(ichorv1alpha1.KeyRotationStageSettlingTokens))
		Expect(marker(controllerDep)).To(Equal(string(op.UID)))
		Expect(marker(uiDep)).To(Equal(string(op.UID)))

		generation, uiGeneration := fresh(controllerDep).Generation, fresh(uiDep).Generation
		applied := len(f.world.applies)

		// The rollout is under way (the Deployment controller has not even seen the new template): the old key stays.
		for range 4 {
			op = f.step(op)
		}

		Expect(keyStageOf(op)).To(Equal(ichorv1alpha1.KeyRotationStageSettlingTokens), "the old key was dropped under pods that still hold its tokens")
		Expect(f.world.applies).To(HaveLen(applied))
		Expect(op.Status.Message + apimeta.FindStatusCondition(op.Status.Conditions, conditionKeyRotation).Message).To(ContainSubstring("controller Deployment"))

		// Each new reconciler (a restarted controller) finds the marker and does not restart them again.
		Expect(fresh(controllerDep).Generation).To(Equal(generation))

		setRollout(fresh(controllerDep), true)

		op = f.step(op)
		Expect(keyStageOf(op)).To(Equal(ichorv1alpha1.KeyRotationStageSettlingTokens), "ichor-ui has not rolled out yet")
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionKeyRotation).Message).To(ContainSubstring("ichor-ui"))

		setRollout(fresh(uiDep), true)

		f.runUntil(op, completed)
		Expect(fresh(controllerDep).Generation).To(Equal(generation), "restarted again after the first restart")
		Expect(fresh(uiDep).Generation).To(Equal(uiGeneration), "restarted again after the first restart")
	})

	It("resumes after the controller restarted itself: workloads already marked with this operation are not restarted again", func() {
		f := newKeyRotFixture(fastBundle())
		op := f.newOp(saOnly)

		// The previous incarnation patched both Deployments and was replaced before it wrote the next stage.
		for _, d := range []*appsv1.Deployment{controllerDep, uiDep} {
			d = fresh(d)
			d.Spec.Template.Annotations = map[string]string{keyRotationRestartMarker: string(op.UID)}
			Expect(k8sClient.Update(ctx, d)).To(Succeed())
			setRollout(fresh(d), true)
		}

		generation, uiGeneration := fresh(controllerDep).Generation, fresh(uiDep).Generation

		f.runUntil(op, completed)
		Expect(fresh(controllerDep).Generation).To(Equal(generation))
		Expect(fresh(uiDep).Generation).To(Equal(uiGeneration))
	})

	It("leaves them alone when only the secretbox key is rotated: no token depends on it", func() {
		f := newKeyRotFixture(fastBundle())
		op := f.newOp(boxOnly)

		f.runUntil(op, completed)
		Expect(marker(controllerDep)).To(BeEmpty())
		Expect(marker(uiDep)).To(BeEmpty())
	})
})
