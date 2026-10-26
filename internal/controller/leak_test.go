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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// TestTalosMachineBundleLeak forces every reconcile error/status path that
// has real bundle material loaded nearby and asserts that none of it
// appears in the resulting condition messages, in the spirit of
// pkg/render's own TestRenderBundleLeak. Branches that fail before a
// bundle is ever loaded ("cluster not found", "secrets Secret missing")
// have nothing that could leak and belong with the functional tests in
// talosmachine_controller_test.go instead -- a leak check with no secret
// material anywhere nearby cannot fail, which reads like coverage without
// being any. What this file could not force: a render failure (render's
// own inputs are all CRD-validated by the time they reach this
// controller, so there is no reachable way to make render.Render itself
// fail through the Kubernetes API) and "render failed" from an invalid
// versionContract (same reason -- the CRD's own pattern validation
// rejects it before a reconcile ever sees it).
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

	It("secrets bundle YAML malformed", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		// A leading tab is never valid YAML indentation, so this is
		// guaranteed to fail decoding rather than parse as something else.
		secret.Data[bundleSecretKey] = append([]byte("\tnot valid yaml\n"), secret.Data[bundleSecretKey]...)
		Expect(k8sClient.Update(ctx, secret)).To(Succeed())

		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)

		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace}
		_, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		got := fetchMachine(m)
		Expect(readyCondition(got).Reason).To(Equal(reasonSecretsBundleUnavailable))
		assertNoLeak(got, bundleSecretMaterial(bundle))
	})

	It("Talos client not configured", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)

		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace}
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
			Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace,
			TalosClient: func(context.Context, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *secrets.Bundle) (TalosClient, error) {
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
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

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
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

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
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

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
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

		_, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		assertNoLeak(fetchMachine(m), bundleSecretMaterial(bundle))
	})

	// TestTalosMachineBundleLeak's own routes above all force an error; none
	// of them has anything to say about setWarnings, the one *success* path
	// that still writes free-form text (Talos's own dry-run warnings) into a
	// status Condition. Verified against Talos v1.14.3's actual source
	// (ADR 0041) that no warning it generates today echoes a submitted
	// value -- but that is a fact about today's machinery version, not
	// about this code, so this does not rely on it: it feeds setWarnings
	// the bundle's own secret material directly (standing in for whatever a
	// future Talos version might start echoing) and asserts
	// the scrubber still caught it.
	It("dry run warnings echo bundle material", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)

		current := renderWithConfig(cluster, m, bundle, layerCluster, nil, minimalConfigSkeleton())

		fake := &fakeTalosClient{current: current, warnings: bundleSecretMaterial(bundle)}
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

		_, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		got := fetchMachine(m)
		warn := warningsCondition(got)
		Expect(warn).NotTo(BeNil())
		Expect(warn.Status).To(Equal(metav1.ConditionTrue), "non-empty warnings must still surface as a Condition")

		for _, s := range bundleSecretMaterial(bundle) {
			if s == "" {
				continue
			}

			Expect(warn.Message).NotTo(ContainSubstring(s), "ConfigWarnings condition message leaked bundle material")
		}
	})
})
