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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
)

// securityMachine reconciles a converged worker of a cluster with sec and returns it after one
// reconcile against a node that reports node.
func securityMachine(sec *ichorv1alpha1.TalosClusterSecurity, fake *fakeTalosClient) *ichorv1alpha1.TalosMachine {
	bundle := newTestBundle("v1.14")
	secret := newTestSecret(bundle)
	cluster := newTestCluster(secret.Name, "v1.14")
	cluster.Spec.Security = sec
	Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

	m := newTestMachine(cluster.Name, true)

	current, _, err := render.Render(cluster, m, bundle, nil, nil)
	Expect(err).NotTo(HaveOccurred())

	fake.current = current
	r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

	_, err = reconcileMachine(r, m)
	Expect(err).NotTo(HaveOccurred())

	return fetchMachine(m)
}

var _ = Describe("TalosMachineReconciler boot security (ADR 0123)", func() {
	preferred := func(lockdown string) *ichorv1alpha1.TalosClusterSecurity {
		return &ichorv1alpha1.TalosClusterSecurity{
			SecureBoot:     &ichorv1alpha1.TalosClusterSecureBoot{Mode: ichorv1alpha1.SecureBootPreferred},
			KernelLockdown: lockdown,
		}
	}

	It("records what the node reports and reports conformance as conditions", func() {
		got := securityMachine(preferred("integrity"), &fakeTalosClient{securityState: SecurityState{
			SecureBoot: true, BootedWithUKI: true, ModuleSignatureEnforced: true, UKISigningKeyFingerprint: testUKIFingerprint, PCRSigningKeyFingerprint: testPCRFingerprint,
		}})

		Expect(got.Status.Security).To(Equal(&ichorv1alpha1.TalosMachineSecurity{
			SecureBoot: true, BootedWithUKI: true, ModuleSignatureEnforced: true, UKISigningKeyFingerprint: testUKIFingerprint, PCRSigningKeyFingerprint: testPCRFingerprint,
			KernelLockdown: "integrity",
		}))

		sb := apimeta.FindStatusCondition(got.Status.Conditions, ichorv1alpha1.ConditionSecureBoot)
		Expect(sb).NotTo(BeNil())
		Expect(sb.Status).To(Equal(metav1.ConditionTrue))

		kl := apimeta.FindStatusCondition(got.Status.Conditions, ichorv1alpha1.ConditionKernelLockdown)
		Expect(kl).NotTo(BeNil())
		Expect(kl.Status).To(Equal(metav1.ConditionTrue))

		Expect(readyCondition(got).Status).To(Equal(metav1.ConditionTrue))
	})

	It("reports a node without SecureBoot and a lockdown below the expectation, and stays Ready", func() {
		got := securityMachine(preferred("confidentiality"), &fakeTalosClient{securityState: SecurityState{BootedWithUKI: true}})

		sb := apimeta.FindStatusCondition(got.Status.Conditions, ichorv1alpha1.ConditionSecureBoot)
		Expect(sb).NotTo(BeNil())
		Expect(sb.Status).To(Equal(metav1.ConditionFalse))
		Expect(sb.Reason).To(Equal("SecureBootOff"))

		kl := apimeta.FindStatusCondition(got.Status.Conditions, ichorv1alpha1.ConditionKernelLockdown)
		Expect(kl).NotTo(BeNil())
		Expect(kl.Status).To(Equal(metav1.ConditionFalse))
		Expect(kl.Message).To(ContainSubstring("none"))

		Expect(readyCondition(got).Status).To(Equal(metav1.ConditionTrue), "a node that cannot be fixed by configuration is reported, not blocked")
	})

	It("records the state of a cluster that sets nothing, without conditions", func() {
		got := securityMachine(nil, &fakeTalosClient{securityState: SecurityState{SecureBoot: true, BootedWithUKI: true}})

		Expect(got.Status.Security).NotTo(BeNil())
		Expect(got.Status.Security.SecureBoot).To(BeTrue())
		Expect(apimeta.FindStatusCondition(got.Status.Conditions, ichorv1alpha1.ConditionSecureBoot)).To(BeNil())
		Expect(apimeta.FindStatusCondition(got.Status.Conditions, ichorv1alpha1.ConditionKernelLockdown)).To(BeNil())
	})

	It("drops the conditions when the option is removed", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		cluster.Spec.Security = preferred("")
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		m := newTestMachine(cluster.Name, true)
		current, _, err := render.Render(cluster, m, bundle, nil, nil)
		Expect(err).NotTo(HaveOccurred())

		fake := &fakeTalosClient{current: current, securityState: SecurityState{BootedWithUKI: true}}
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

		_, err = reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(apimeta.FindStatusCondition(fetchMachine(m).Status.Conditions, ichorv1alpha1.ConditionSecureBoot)).NotTo(BeNil())

		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)).To(Succeed())
		cluster.Spec.Security = nil
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		current, _, err = render.Render(cluster, m, bundle, nil, nil)
		Expect(err).NotTo(HaveOccurred())

		fake.current = current

		_, err = reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(apimeta.FindStatusCondition(fetchMachine(m).Status.Conditions, ichorv1alpha1.ConditionSecureBoot)).To(BeNil())
	})

	It("keeps the last recorded state when the node cannot be asked", func() {
		got := securityMachine(preferred(""), &fakeTalosClient{securityErr: errors.New("boom")})

		Expect(got.Status.Security).To(BeNil())
		Expect(readyCondition(got).Status).To(Equal(metav1.ConditionTrue))

		sb := apimeta.FindStatusCondition(got.Status.Conditions, ichorv1alpha1.ConditionSecureBoot)
		Expect(sb).NotTo(BeNil())
		Expect(sb.Status).To(Equal(metav1.ConditionUnknown))
	})
})
