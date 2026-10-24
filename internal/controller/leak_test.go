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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// bundleSecretMaterial lists every sensitive scalar in bundle, the same way
// pkg/render's own leak test does for its bundle.
func bundleSecretMaterial(bundle *secrets.Bundle) []string {
	return []string{
		bundle.Secrets.BootstrapToken,
		bundle.TrustdInfo.Token,
		bundle.Cluster.ID,
		bundle.Cluster.Secret,
		string(bundle.Certs.OS.Crt),
		string(bundle.Certs.OS.Key),
		string(bundle.Certs.K8s.Crt),
		string(bundle.Certs.K8s.Key),
	}
}

// TestTalosMachineBundleLeak forces every reconcile error/status path this
// test can reach and asserts that none of the bundle's secret material
// appears in the resulting condition messages, in the spirit of
// pkg/render's own TestRenderBundleLeak. What it could not force: a render
// failure (render's own inputs are all CRD-validated by the time they
// reach this controller, so there is no reachable way to make
// render.Render itself fail through the Kubernetes API) and "render
// failed" from an invalid versionContract (same reason -- the CRD's own
// pattern validation rejects it before a reconcile ever sees it).
var _ = Describe("TalosMachineReconciler bundle leak", func() {
	assertNoLeak := func(m *ichorv1alpha1.TalosMachine, secretsMaterial []string) {
		cond := readyCondition(m)
		Expect(cond).NotTo(BeNil())

		for _, s := range secretsMaterial {
			if s == "" {
				continue
			}

			Expect(cond.Message).NotTo(ContainSubstring(s), "Ready condition message leaked bundle material")
		}
	}

	It("cluster not found", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)
		m.Spec.ClusterRef.Name = "does-not-exist"
		Expect(k8sClient.Update(ctx, m)).To(Succeed())

		r := &TalosMachineReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		assertNoLeak(fetchMachine(m), bundleSecretMaterial(bundle))
	})

	It("secrets bundle Secret missing", func() {
		bundle := newTestBundle("v1.14")
		cluster := newTestCluster("does-not-exist", "v1.14")
		m := newTestMachine(cluster.Name, true)

		r := &TalosMachineReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		assertNoLeak(fetchMachine(m), bundleSecretMaterial(bundle))
	})

	It("secrets bundle JSON malformed", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		secret.Data[bundleSecretKey] = append(secret.Data[bundleSecretKey], []byte("}}}not json")...)
		Expect(k8sClient.Update(ctx, secret)).To(Succeed())

		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)

		r := &TalosMachineReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		assertNoLeak(fetchMachine(m), bundleSecretMaterial(bundle))
	})

	It("Talos client not configured", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)

		r := &TalosMachineReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		assertNoLeak(fetchMachine(m), bundleSecretMaterial(bundle))
	})

	It("Talos connect failed", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)

		r := &TalosMachineReconciler{
			Client: k8sClient, Scheme: k8sClient.Scheme(),
			TalosClient: func(context.Context, *ichorv1alpha1.TalosMachine) (TalosClient, error) {
				return nil, errors.New("dial refused")
			},
		}
		_, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		assertNoLeak(fetchMachine(m), bundleSecretMaterial(bundle))
	})

	It("current config unavailable", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)

		fake := &fakeTalosClient{currentErr: errors.New("read failed")}
		r := &TalosMachineReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), TalosClient: clientFactory(fake)}

		_, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		assertNoLeak(fetchMachine(m), bundleSecretMaterial(bundle))
	})

	It("config diff failed", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)

		fake := &fakeTalosClient{current: []byte("{{{not valid yaml")}
		r := &TalosMachineReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), TalosClient: clientFactory(fake)}

		_, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		assertNoLeak(fetchMachine(m), bundleSecretMaterial(bundle))
	})

	It("dry run failed", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)

		current := renderWithConfig(cluster, m, bundle, layerCluster, nil, minimalConfigSkeleton())

		desired := extraArgsConfig()
		newTestPatch(cluster.Name, layerCluster, nil, desired)

		fake := &fakeTalosClient{current: current, dryRunErr: errors.New("dry run failed")}
		r := &TalosMachineReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), TalosClient: clientFactory(fake)}

		_, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		assertNoLeak(fetchMachine(m), bundleSecretMaterial(bundle))
	})

	It("apply failed", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)

		current := renderWithConfig(cluster, m, bundle, layerCluster, nil, minimalConfigSkeleton())

		desired := extraArgsConfig()
		newTestPatch(cluster.Name, layerCluster, nil, desired)

		fake := &fakeTalosClient{current: current, applyErr: errors.New("apply failed")}
		r := &TalosMachineReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), TalosClient: clientFactory(fake)}

		_, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		assertNoLeak(fetchMachine(m), bundleSecretMaterial(bundle))
	})
})
