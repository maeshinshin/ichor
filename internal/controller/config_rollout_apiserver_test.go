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

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
)

const (
	apiInstanceOld = "containerd://kube-apiserver-old"
	apiInstanceNew = "containerd://kube-apiserver-new"
)

// apiServerFixture is a cluster whose spec.authentication (ADR 0094) was just set: control planes
// still run the configuration rendered without it, and the only difference is the
// KubeAuthenticationConfig document, reboot-free but restarting kube-apiserver (pkg/fieldmeta's
// APIServerRestart class).
type apiServerFixture struct {
	cluster *ichorv1alpha1.TalosCluster // with spec.authentication
	plain   *ichorv1alpha1.TalosCluster // the same without it
	bundle  *secrets.Bundle
}

func newAPIServerFixture() *apiServerFixture {
	bundle := newTestBundle("v1.14")
	secret := newTestSecret(bundle)
	cluster := newTestCluster(secret.Name, "v1.14")
	plain := cluster.DeepCopy()

	cluster.Spec.Authentication = &ichorv1alpha1.TalosClusterAuthentication{
		External: []ichorv1alpha1.TalosClusterExternalIssuer{{
			IssuerURL: "https://idp.example.org", Audiences: []string{"ichor"},
			UsernameClaim: "email", GroupsClaim: "groups", UsernamePrefix: "oidc:", GroupsPrefix: "oidc:",
		}},
	}
	Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

	return &apiServerFixture{cluster: cluster, plain: plain, bundle: bundle}
}

func (f *apiServerFixture) render(c *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine) []byte {
	out, _, err := render.Render(c, m, f.bundle, nil, nil)
	Expect(err).NotTo(HaveOccurred())

	return out
}

func (f *apiServerFixture) before(m *ichorv1alpha1.TalosMachine) []byte { return f.render(f.plain, m) }
func (f *apiServerFixture) after(m *ichorv1alpha1.TalosMachine) []byte  { return f.render(f.cluster, m) }

// ageRollout backdates the ConfigRollout marker so a wait looks like it started a while ago.
func ageRollout(m *ichorv1alpha1.TalosMachine, by time.Duration) {
	got := fetchMachine(m)
	cond := apimeta.FindStatusCondition(got.Status.Conditions, conditionConfigRollout)
	Expect(cond).NotTo(BeNil())

	cond.LastTransitionTime = metav1.NewTime(time.Now().Add(-by))
	Expect(k8sClient.Status().Update(ctx, got)).To(Succeed())
}

var _ = Describe("the API server restart class", func() {
	It("is not applied by the machine controller on a control plane, only reported", func() {
		f := newAPIServerFixture()
		cp := newTestControlPlane(f.cluster.Name, "61")

		fake := &fakeTalosClient{current: f.before(cp)}
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

		for range 2 { // the first reconcile of a control plane only adds its removal finalizer
			_, err := reconcileMachine(r, cp)
			Expect(err).NotTo(HaveOccurred())
		}

		Expect(fake.applyCalls).To(BeEmpty(), "applying it from here would restart every control plane's API server at once")
		Expect(fake.stagedCalls).To(BeEmpty())

		got := fetchMachine(cp)
		cond := readyCondition(got)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(reasonAPIServerRestartRequired))
		Expect(cond.Message).To(ContainSubstring("KubeAuthenticationConfig"))
		Expect(cond.Message).To(ContainSubstring("run `ichor config apply --cluster " + f.cluster.Name + " --follow`"))
		Expect(got.Status.APIServerRestartPaths).To(ContainElement(HavePrefix("KubeAuthenticationConfig")))
		Expect(got.Status.RebootPaths).To(BeEmpty())
		Expect(got.Status.AppliedConfigHash).To(BeEmpty())
	})

	It("is applied by the machine controller on a worker, which has no API server to restart", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)
		newTestPatch(cluster.Name, layerCluster, nil, extraArgsConfig())

		fake := &fakeTalosClient{current: renderWithConfig(cluster, m, bundle, layerCluster, nil, minimalConfigSkeleton())}
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

		_, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.applyCalls).To(HaveLen(1))
	})

	It("is applied by the config operation without a drain or reboot, and the step waits for /readyz", func() {
		f := newAPIServerFixture()
		cp := newTestControlPlane(f.cluster.Name, "62")
		fake := &fakeTalosClient{current: f.before(cp), apiInstance: apiInstanceOld}
		adv := newRolloutAdvancer(fake)

		done, err := adv.Advance(ctx, f.cluster, fetchMachine(cp), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(fake.dryRunCalls).To(Equal(1))
		Expect(fake.applyCalls).To(HaveLen(1), "one plain apply")
		Expect(fake.stagedCalls).To(BeEmpty())
		Expect(fake.rebootCalls).To(BeZero(), "no reboot")
		Expect(fake.apiReadyCalls).To(BeZero())

		cond := apimeta.FindStatusCondition(fetchMachine(cp).Status.Conditions, conditionConfigRollout)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Reason).To(Equal(reasonRolloutRestarting))
		Expect(cond.Message).To(Equal(apiInstanceOld))

		// Applied, but the old API server container is still there: the restart has not started, so
		// even a /readyz that answers (the old one) is not the end of the step.
		fake.current = f.after(cp)

		done, err = adv.Advance(ctx, f.cluster, fetchMachine(cp), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(fake.apiReadyCalls).To(BeZero())

		// Restarted, not ready yet.
		fake.apiInstance, fake.apiReadyErr = apiInstanceNew, errors.New("connection refused")

		done, err = adv.Advance(ctx, f.cluster, fetchMachine(cp), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(fake.apiReadyCalls).To(Equal(1))

		// Ready.
		fake.apiReadyErr = nil

		done, err = adv.Advance(ctx, f.cluster, fetchMachine(cp), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
		Expect(fake.applyCalls).To(HaveLen(1), "applied once")
		Expect(apimeta.IsStatusConditionTrue(fetchMachine(cp).Status.Conditions, conditionConfigRollout)).To(BeFalse())
	})

	It("accepts a pod that never changed once the grace period passed and /readyz answers", func() {
		f := newAPIServerFixture()
		cp := newTestControlPlane(f.cluster.Name, "63")
		fake := &fakeTalosClient{current: f.before(cp), apiInstance: apiInstanceOld}
		adv := newRolloutAdvancer(fake)

		_, err := adv.Advance(ctx, f.cluster, fetchMachine(cp), nil)
		Expect(err).NotTo(HaveOccurred())

		fake.current = f.after(cp)
		ageRollout(cp, 2*apiServerRestartGrace)

		done, err := adv.Advance(ctx, f.cluster, fetchMachine(cp), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
	})

	It("pauses with a clear reason when /readyz does not return in time, and a retry waits again", func() {
		f := newAPIServerFixture()
		cp := newTestControlPlane(f.cluster.Name, "64")
		fake := &fakeTalosClient{current: f.before(cp), apiInstance: apiInstanceOld}
		adv := newRolloutAdvancer(fake)

		_, err := adv.Advance(ctx, f.cluster, fetchMachine(cp), nil)
		Expect(err).NotTo(HaveOccurred())

		fake.current, fake.apiInstance, fake.apiReadyErr = f.after(cp), apiInstanceNew, errors.New("connection refused")
		ageRollout(cp, 2*apiServerReadyTimeout)

		done, err := adv.Advance(ctx, f.cluster, fetchMachine(cp), nil)
		Expect(err).To(MatchError(And(ContainSubstring("did not answer /readyz"), ContainSubstring("retry"))))
		Expect(done).To(BeFalse())

		cond := apimeta.FindStatusCondition(fetchMachine(cp).Status.Conditions, conditionConfigRollout)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(reasonRolloutAPINotReady))

		// The retry (ADR 0095) calls Advance again: the configuration is on the node, so it must not
		// report "nothing pending" but wait again, with a fresh clock.
		done, err = adv.Advance(ctx, f.cluster, fetchMachine(cp), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeFalse())
		Expect(fake.applyCalls).To(HaveLen(1), "nothing is applied twice")

		fake.apiReadyErr = nil

		done, err = adv.Advance(ctx, f.cluster, fetchMachine(cp), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
	})

	It("resumes after a controller restart without applying twice, and re-applies when it stopped before the apply", func() {
		f := newAPIServerFixture()
		cp := newTestControlPlane(f.cluster.Name, "65")
		fake := &fakeTalosClient{current: f.before(cp), apiInstance: apiInstanceOld}

		_, err := newRolloutAdvancer(fake).Advance(ctx, f.cluster, fetchMachine(cp), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.applyCalls).To(HaveLen(1))

		// A new advancer: nothing is remembered except the marker on the machine. The node is still
		// on the old configuration (the controller died after recording the marker, before the apply
		// landed), so the step applies again.
		_, err = newRolloutAdvancer(fake).Advance(ctx, f.cluster, fetchMachine(cp), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.applyCalls).To(HaveLen(2))

		// Applied now, and the controller dies once more while the API server is down: the new one
		// treats errors from its own cluster and the node as waiting, and finishes.
		fake.current, fake.apiInstance, fake.currentErr = f.after(cp), apiInstanceNew, errors.New("connection refused")

		done, err := newRolloutAdvancer(fake).Advance(ctx, f.cluster, fetchMachine(cp), nil)
		Expect(err).NotTo(HaveOccurred(), "an unreachable node inside the window is waiting")
		Expect(done).To(BeFalse())

		fake.currentErr = nil

		done, err = newRolloutAdvancer(fake).Advance(ctx, f.cluster, fetchMachine(cp), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(done).To(BeTrue())
		Expect(fake.applyCalls).To(HaveLen(2))
	})

	It("rolls the control planes one at a time: the second is not touched until the first answers /readyz", func() {
		f := newAPIServerFixture()
		cp1 := newTestControlPlane(f.cluster.Name, "66")
		cp2 := newTestControlPlane(f.cluster.Name, "67")
		cp3 := newTestControlPlane(f.cluster.Name, "70") // an odd count, or the operation refuses to start; already converged
		setUpgradeOrder(cp1, "1")
		setUpgradeOrder(cp2, "2")
		setUpgradeOrder(cp3, "3")
		markAPIServerRestartPending(cp1, cp2) // what the machine controller reports; cp3 is converged

		fake1 := &fakeTalosClient{current: f.before(cp1), apiInstance: apiInstanceOld}
		fake2 := &fakeTalosClient{current: f.before(cp2), apiInstance: apiInstanceOld}
		fake3 := &fakeTalosClient{current: f.after(cp3), apiInstance: apiInstanceNew}

		// What the nodes do once their configuration is applied: the API server container changes.
		restart := func(fk *fakeTalosClient, m *ichorv1alpha1.TalosMachine) {
			if len(fk.applyCalls) > 0 && fk.apiInstance == apiInstanceOld {
				fk.current, fk.apiInstance = f.after(m), apiInstanceNew
			}
		}

		adv := newRolloutAdvancer(nil)
		adv.TalosClient = perMachineClientFactory(map[string]*fakeTalosClient{cp1.Name: fake1, cp2.Name: fake2, cp3.Name: fake3})

		op := newConfigOpFor(f.cluster.Name)
		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: adv.Advance}

		fake1.apiReadyErr = errors.New("connection refused")

		for range 6 {
			_, err := reconcileOperation(r, op)
			Expect(err).NotTo(HaveOccurred())
			restart(fake1, cp1)
		}

		Expect(fake1.applyCalls).To(HaveLen(1))
		Expect(fake2.applyCalls).To(BeEmpty(), "the second control plane waits while the first has no ready API server")

		fake1.apiReadyErr = nil

		for range 8 {
			_, err := reconcileOperation(r, op)
			Expect(err).NotTo(HaveOccurred())
			restart(fake2, cp2)
		}

		Expect(fake2.applyCalls).To(HaveLen(1))
		Expect(fake2.apiReadyCalls).To(BeNumerically(">", 0))
		Expect(fetchOperation(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
		Expect(fake1.rebootCalls + fake2.rebootCalls).To(BeZero())
	})

	It("pauses the operation, applying nothing further, when the first API server never returns", func() {
		f := newAPIServerFixture()
		cp1 := newTestControlPlane(f.cluster.Name, "68")
		cp2 := newTestControlPlane(f.cluster.Name, "69")
		cp3 := newTestControlPlane(f.cluster.Name, "71")
		setUpgradeOrder(cp1, "1")
		setUpgradeOrder(cp2, "2")
		setUpgradeOrder(cp3, "3")
		markAPIServerRestartPending(cp1, cp2) // what the machine controller reports; cp3 is converged

		fake1 := &fakeTalosClient{current: f.before(cp1), apiInstance: apiInstanceOld, apiReadyErr: errors.New("connection refused")}
		fake2 := &fakeTalosClient{current: f.before(cp2), apiInstance: apiInstanceOld}
		fake3 := &fakeTalosClient{current: f.after(cp3), apiInstance: apiInstanceNew}

		adv := newRolloutAdvancer(nil)
		adv.TalosClient = perMachineClientFactory(map[string]*fakeTalosClient{cp1.Name: fake1, cp2.Name: fake2, cp3.Name: fake3})

		op := newConfigOpFor(f.cluster.Name)
		r := &TalosOperationReconciler{Client: k8sClient, APIReader: k8sClient, Advance: adv.Advance}

		_, err := reconcileOperation(r, op)
		Expect(err).NotTo(HaveOccurred())
		_, err = reconcileOperation(r, op)
		Expect(err).NotTo(HaveOccurred())

		fake1.current, fake1.apiInstance = f.after(cp1), apiInstanceNew
		ageRollout(cp1, 2*apiServerReadyTimeout)

		for range 3 {
			_, err = reconcileOperation(r, op)
			Expect(err).NotTo(HaveOccurred())
		}

		got := fetchOperation(op)
		Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		Expect(got.Status.FailureReason).To(ContainSubstring("did not answer /readyz"))
		Expect(fake2.applyCalls).To(BeEmpty())
	})
})

// markAPIServerRestartPending sets what the machine controller reports for a control plane whose
// pending change restarts kube-apiserver; a config operation selects machines by it.
func markAPIServerRestartPending(ms ...*ichorv1alpha1.TalosMachine) {
	for _, m := range ms {
		m.Status.APIServerRestartPaths = []string{"KubeAuthenticationConfig.configuration"}
		Expect(k8sClient.Status().Update(ctx, m)).To(Succeed())
	}
}

func newConfigOpFor(clusterName string) *ichorv1alpha1.TalosOperation {
	op := &ichorv1alpha1.TalosOperation{
		GenerateName: "cfg-api-op-", Namespace: tmNamespace,
		Spec: ichorv1alpha1.TalosOperationSpec{
			ClusterRef: corev1.LocalObjectReference{Name: clusterName}, Kind: ichorv1alpha1.TalosOperationKindConfig,
		},
	}
	Expect(k8sClient.Create(ctx, op)).To(Succeed())

	return op
}
