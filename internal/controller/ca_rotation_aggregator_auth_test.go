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

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// Hardware finding: kube-apiserver merges the aggregator CAs into
// kube-system/extension-apiserver-authentication and never removes the old one, so after Done an extension API
// server that reads the ConfigMap kept trusting the old CA. Finalizing deletes the ConfigMap so the apiserver
// recreates it from the new bundle, and the rotation is complete only when it holds the new CA alone.
var _ = Describe("TalosOperation ca-rotation of the aggregator CA: extension-apiserver-authentication", func() {
	It("deletes the ConfigMap in Finalizing and completes only when it holds the new CA alone", func() {
		f := newCARotFixture()
		f.targets = []string{ichorv1alpha1.CARotationTargetAggregator}
		f.aggregatorAuthPending = 3

		op := f.newOp()
		op, _ = f.runUntil(op, atStage(ichorv1alpha1.CARotationStageFinalizing))
		Expect(f.aggregatorAuthCalls).To(BeEmpty(), "nothing is deleted while the old aggregator CA is still in use")
		rot := f.storedRotation()

		for range 3 {
			op = f.step(op)
			Expect(op.Status.Phase).NotTo(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
		}

		Expect(f.aggregatorAuthCalls).To(HaveLen(3))
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation).Message).To(ContainSubstring(extensionAuthConfigMap))

		op, _ = f.runUntil(op, completed)
		Expect(f.aggregatorAuthCalls).To(HaveLen(4))
		Expect(f.aggregatorAuthCalls[0]).To(Equal(fp(rot.Aggregator.Crt)), "the new aggregator CA is the one that must remain")
		Expect(apimeta.FindStatusCondition(op.Status.Conditions, conditionCARotation).Message).To(ContainSubstring("Kubernetes aggregator"))
	})

	It("does not touch the ConfigMap when the aggregator CA is not rotated", func() {
		f := newCARotFixture()
		f.targets = []string{ichorv1alpha1.CARotationTargetEtcd}
		f.runUntil(f.newOp(), completed)

		Expect(f.aggregatorAuthCalls).To(BeEmpty())
	})
})

var _ = Describe("resetAggregatorAuth", func() {
	var oldCA, newCA string

	BeforeEach(func() {
		oldCA = string(newFreshTestBundle("v1.14").Certs.K8sAggregator.Crt)
		newCA = string(newFreshTestBundle("v1.14").Certs.K8sAggregator.Crt)
	})

	wantFP := func() string { return fp([]byte(newCA)) }

	configMap := func(ca string) *corev1.ConfigMap {
		return &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: kubeSystemNamespace, Name: extensionAuthConfigMap, UID: "uid-1"},
			Data:       map[string]string{extensionAuthCAKey: ca, "client-ca-file": "other"},
		}
	}

	deletes := func(kc *fake.Clientset) []clienttesting.DeleteActionImpl {
		var out []clienttesting.DeleteActionImpl

		for _, a := range kc.Actions() {
			if d, ok := a.(clienttesting.DeleteActionImpl); ok {
				out = append(out, d)
			}
		}

		return out
	}

	It("deletes a ConfigMap that still carries the old CA, by name and with the UID it read, and reports it not settled", func() {
		kc := fake.NewClientset(configMap(oldCA + newCA))

		settled, err := resetAggregatorAuth(ctx, kc.CoreV1().ConfigMaps(kubeSystemNamespace), wantFP())
		Expect(err).NotTo(HaveOccurred())
		Expect(settled).To(BeFalse())

		got := deletes(kc)
		Expect(got).To(HaveLen(1))
		Expect(got[0].Name).To(Equal(extensionAuthConfigMap))
		Expect(got[0].Namespace).To(Equal(kubeSystemNamespace))
		Expect(got[0].DeleteOptions.Preconditions).NotTo(BeNil())
		Expect(string(*got[0].DeleteOptions.Preconditions.UID)).To(Equal("uid-1"))
	})

	It("reports a missing ConfigMap as not settled (kube-apiserver has not recreated it yet) and deletes nothing", func() {
		kc := fake.NewClientset()

		settled, err := resetAggregatorAuth(ctx, kc.CoreV1().ConfigMaps(kubeSystemNamespace), wantFP())
		Expect(err).NotTo(HaveOccurred())
		Expect(settled).To(BeFalse())
		Expect(deletes(kc)).To(BeEmpty())
	})

	It("is settled, and deletes nothing, once the ConfigMap holds the new CA alone", func() {
		kc := fake.NewClientset(configMap(newCA))

		settled, err := resetAggregatorAuth(ctx, kc.CoreV1().ConfigMaps(kubeSystemNamespace), wantFP())
		Expect(err).NotTo(HaveOccurred())
		Expect(settled).To(BeTrue())
		Expect(deletes(kc)).To(BeEmpty())
	})

	It("deletes a ConfigMap that holds neither CA, as one the new aggregator CA has not reached", func() {
		kc := fake.NewClientset(configMap(oldCA))

		settled, err := resetAggregatorAuth(ctx, kc.CoreV1().ConfigMaps(kubeSystemNamespace), wantFP())
		Expect(err).NotTo(HaveOccurred())
		Expect(settled).To(BeFalse())
		Expect(deletes(kc)).To(HaveLen(1))
	})
})
