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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

var _ = Describe("TalosOperation fromVersion", func() {
	It("records the version most selected machines run, once, when a Talos upgrade starts", func() {
		secret := newTestSecret(newTestBundle("v1.14"))
		cluster := newTestCluster(secret.Name, "v1.14")
		fakes := map[string]*fakeTalosClient{}

		for i, v := range []string{"v1.14.1", "v1.14.2", "v1.14.2"} {
			role := "worker"
			if i == 0 {
				role = "controlplane"
			}

			m := newOpTestMachine(cluster.Name, role, "fv-"+v+string(rune('a'+i)))
			fakes[m.Name] = &fakeTalosClient{version: v}
		}

		r := &TalosOperationReconciler{
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: func(_ context.Context, _ *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, _ *secrets.Bundle) (TalosClient, error) {
				return fakes[m.Name], nil
			},
			Advance: func(context.Context, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *ichorv1alpha1.TalosOperation) (bool, error) {
				return false, nil
			},
		}
		op := newOpTestOperation(cluster.Name)

		_, err := reconcileOperation(r, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(fetchOperation(op).Status.FromVersion).To(Equal("v1.14.2"))

		// Later reconciles never change it, even once the machines report something else.
		for _, f := range fakes {
			f.version = "v1.14.9"
		}

		_, err = reconcileOperation(r, op)
		Expect(err).NotTo(HaveOccurred())
		Expect(fetchOperation(op).Status.FromVersion).To(Equal("v1.14.2"))
	})
})
