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
	"reflect"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/constants"
	yaml "go.yaml.in/yaml/v4"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
	"github.com/maeshinshin/ichor/pkg/render"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// tmNamespace is the namespace every fixture below lives in; envtest's
// kube-apiserver always has "default", so there is nothing to create.
const tmNamespace = "default"

// Literals reused across this file, diff_test.go and leak_test.go.
const (
	roleWorker   = "worker"
	layerCluster = "cluster"
	kindConfig   = "Config"
)

// fakeTalosClient is the Talos API boundary test double (unit rule 2: no
// Talos call in these tests ever reaches a real node). Its fields are set
// up per test and its call counts are asserted on, e.g. to confirm a dry
// run never happens on the converged path and an apply never happens on a
// blocked one.
type fakeTalosClient struct {
	current    []byte
	currentErr error

	warnings  []string
	dryRunErr error

	applyErr error

	dryRunCalls int
	applyCalls  [][]byte
}

func (f *fakeTalosClient) CurrentConfig(context.Context) ([]byte, error) {
	return f.current, f.currentErr
}

func (f *fakeTalosClient) DryRunApply(_ context.Context, _ []byte) ([]string, error) {
	f.dryRunCalls++

	return f.warnings, f.dryRunErr
}

func (f *fakeTalosClient) ApplyNoReboot(_ context.Context, cfg []byte) error {
	f.applyCalls = append(f.applyCalls, cfg)

	return f.applyErr
}

func clientFactory(fake *fakeTalosClient) func(
	context.Context, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *secrets.Bundle,
) (TalosClient, error) {
	return func(context.Context, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *secrets.Bundle) (TalosClient, error) {
		return fake, nil
	}
}

func newTestBundle(versionContract string) *secrets.Bundle {
	contract, err := coreconfig.ParseContractFromVersion(versionContract)
	Expect(err).NotTo(HaveOccurred())

	bundle, err := secrets.NewBundle(secrets.NewFixedClock(time.Now()), contract)
	Expect(err).NotTo(HaveOccurred())

	return bundle
}

func newTestSecret(bundle *secrets.Bundle) *corev1.Secret {
	raw, err := yaml.Marshal(bundle)
	Expect(err).NotTo(HaveOccurred())

	secret := &corev1.Secret{
		GenerateName: "bundle-", Namespace: tmNamespace,
		Data: map[string][]byte{bundleSecretKey: raw},
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())

	return secret
}

func newTestCluster(secretName, versionContract string) *ichorv1alpha1.TalosCluster {
	cluster := &ichorv1alpha1.TalosCluster{
		GenerateName: "cluster-", Namespace: tmNamespace,
		Spec: ichorv1alpha1.TalosClusterSpec{
			Endpoint:          ichorv1alpha1.TalosClusterEndpoint{Type: "vip", Address: "10.0.0.5"},
			TalosVersion:      "v1.14.3",
			KubernetesVersion: "v" + constants.DefaultKubernetesVersion,
			VersionContract:   versionContract,
			SecretsRef:        corev1.LocalObjectReference{Name: secretName},
			ControlPlane:      ichorv1alpha1.TalosClusterControlPlane{Count: 1, Schedulable: "true"},
		},
	}
	Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

	return cluster
}

func newTestMachine(clusterName string, approved bool) *ichorv1alpha1.TalosMachine {
	m := &ichorv1alpha1.TalosMachine{
		GenerateName: "machine-", Namespace: tmNamespace,
		Spec: ichorv1alpha1.TalosMachineSpec{
			ClusterRef: corev1.LocalObjectReference{Name: clusterName},
			Identity:   ichorv1alpha1.TalosMachineIdentity{UUID: "uuid-" + clusterName},
			Endpoint:   "10.0.0.10:50000",
			Role:       roleWorker,
			Approved:   approved,
		},
	}
	Expect(k8sClient.Create(ctx, m)).To(Succeed())

	return m
}

// newTestPatch creates a cluster-layer (or, with target set, narrower)
// TalosConfigPatch carrying a single typed Config document.
func newTestPatch(clusterName, layer string, target *ichorv1alpha1.TalosConfigPatchTarget, cfg *typedconfig.Config) *ichorv1alpha1.TalosConfigPatch {
	p := &ichorv1alpha1.TalosConfigPatch{
		GenerateName: "patch-", Namespace: tmNamespace,
		Spec: ichorv1alpha1.TalosConfigPatchSpec{
			ClusterRef: corev1.LocalObjectReference{Name: clusterName},
			Layer:      layer,
			Target:     target,
			Documents:  []ichorv1alpha1.TalosConfigPatchDocument{{Kind: kindConfig, Config: cfg}},
		},
	}
	Expect(k8sClient.Create(ctx, p)).To(Succeed())

	return p
}

func newTestSysctlPatch(clusterName string, params map[string]string) *ichorv1alpha1.TalosConfigPatch {
	p := &ichorv1alpha1.TalosConfigPatch{
		GenerateName: "patch-", Namespace: tmNamespace,
		Spec: ichorv1alpha1.TalosConfigPatchSpec{
			ClusterRef: corev1.LocalObjectReference{Name: clusterName},
			Layer:      layerCluster,
			Documents: []ichorv1alpha1.TalosConfigPatchDocument{{
				Kind:         "SysctlConfig",
				SysctlConfig: &typedconfig.SysctlConfigV1Alpha1{Params: params},
			}},
		},
	}
	Expect(k8sClient.Create(ctx, p)).To(Succeed())

	return p
}

// minimalConfigSkeleton is the smallest typedconfig.Config the CRD schema
// accepts: "version" plus the required machine/cluster sub-documents, each
// with their own required fields filled with throwaway values. Tests build
// on top of it, and render both "current" and "desired" through the same
// skeleton, so the only difference changedPaths finds is the one field
// each test is actually about -- not these required-but-irrelevant values.
func minimalConfigSkeleton() *typedconfig.Config {
	return &typedconfig.Config{
		ConfigVersion: "v1alpha1",
		MachineConfig: &typedconfig.MachineConfig{
			MachineType:     roleWorker,
			MachineToken:    "328hom.uqjzh6jnn2eie9oi",
			MachineCertSANs: []string{"10.0.0.10"},
		},
		ClusterConfig: &typedconfig.ClusterConfig{},
	}
}

// extraArgsConfig is minimalConfigSkeleton with one extra, safe (pkg/fieldmeta:
// cluster.apiServer needs no reboot) API server argument set -- the non-reboot
// change every "applies without a reboot" test reuses.
func extraArgsConfig() *typedconfig.Config {
	cfg := minimalConfigSkeleton()
	cfg.ClusterConfig.APIServerConfig = &typedconfig.APIServerConfig{ExtraArgsConfig: map[string][]string{"foo": {"b"}}}

	return cfg
}

// renderWithConfig renders cluster/m/bundle with a single, uncreated
// (not stored in the API server) Config-kind patch, for computing the
// "current" bytes a test's fakeTalosClient should return -- the node's
// state before whatever TalosConfigPatch the test goes on to create.
func renderWithConfig(
	cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle,
	layer string, target *ichorv1alpha1.TalosConfigPatchTarget, cfg *typedconfig.Config,
) []byte {
	patch := ichorv1alpha1.TalosConfigPatch{
		Name: "skeleton", Namespace: cluster.Namespace,
		Spec: ichorv1alpha1.TalosConfigPatchSpec{
			ClusterRef: corev1.LocalObjectReference{Name: cluster.Name},
			Layer:      layer,
			Target:     target,
			Documents:  []ichorv1alpha1.TalosConfigPatchDocument{{Kind: kindConfig, Config: cfg}},
		},
	}

	b, _, err := render.Render(cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{patch})
	Expect(err).NotTo(HaveOccurred())

	return b
}

func reconcileMachine(r *TalosMachineReconciler, m *ichorv1alpha1.TalosMachine) (ctrl.Result, error) {
	return r.Reconcile(ctx, ctrl.Request{Namespace: m.Namespace, Name: m.Name})
}

func fetchMachine(m *ichorv1alpha1.TalosMachine) *ichorv1alpha1.TalosMachine {
	var got ichorv1alpha1.TalosMachine
	Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: m.Namespace, Name: m.Name}, &got)).To(Succeed())

	return &got
}

func readyCondition(m *ichorv1alpha1.TalosMachine) *metav1.Condition {
	return apimeta.FindStatusCondition(m.Status.Conditions, conditionReady)
}

func warningsCondition(m *ichorv1alpha1.TalosMachine) *metav1.Condition {
	return apimeta.FindStatusCondition(m.Status.Conditions, conditionConfigWarnings)
}

var _ = Describe("TalosMachineReconciler", func() {
	It("does nothing at all for a machine that is not approved", func() {
		cluster := newTestCluster("does-not-exist", "v1.14")
		m := newTestMachine(cluster.Name, false)

		r := &TalosMachineReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}

		_, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		got := fetchMachine(m)
		Expect(got.Status.Conditions).To(BeEmpty())
		Expect(got.Status.ObservedGeneration).To(BeZero())
		Expect(got.Status.AppliedConfigHash).To(BeEmpty())
	})

	It("records convergence and attempts no apply when hashes match", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)

		current, hash, err := render.Render(cluster, m, bundle, nil)
		Expect(err).NotTo(HaveOccurred())

		fake := &fakeTalosClient{current: current}
		r := &TalosMachineReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), TalosClient: clientFactory(fake)}

		_, err = reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		Expect(fake.dryRunCalls).To(Equal(0))
		Expect(fake.applyCalls).To(BeEmpty())

		got := fetchMachine(m)
		Expect(got.Status.AppliedConfigHash).To(Equal(hash))
		Expect(got.Status.ObservedGeneration).To(Equal(got.Generation))

		cond := readyCondition(got)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(reasonConverged))
	})

	It("applies a non-reboot change with NO_REBOOT and converges hash and observedGeneration", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)

		current := renderWithConfig(cluster, m, bundle, layerCluster, nil, minimalConfigSkeleton())

		desired := extraArgsConfig()
		patch := newTestPatch(cluster.Name, layerCluster, nil, desired)
		wantRendered, wantHash, err := render.Render(cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{*patch})
		Expect(err).NotTo(HaveOccurred())

		fake := &fakeTalosClient{current: current}
		r := &TalosMachineReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), TalosClient: clientFactory(fake)}

		_, err = reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		Expect(fake.dryRunCalls).To(Equal(1))
		Expect(fake.applyCalls).To(HaveLen(1))
		Expect(fake.applyCalls[0]).To(Equal(wantRendered))

		got := fetchMachine(m)
		Expect(got.Status.AppliedConfigHash).To(Equal(wantHash))
		Expect(got.Status.ObservedGeneration).To(Equal(got.Generation))

		cond := readyCondition(got)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(reasonApplied))

		warn := warningsCondition(got)
		Expect(warn).NotTo(BeNil())
		Expect(warn.Status).To(Equal(metav1.ConditionFalse))
	})

	It("takes the cluster lock, applies nothing, and releases it for a reboot-causing change", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)

		current := renderWithConfig(cluster, m, bundle, layerCluster, nil, minimalConfigSkeleton())

		// cluster.proxy.disabled is deliberately uncovered by pkg/fieldmeta's
		// table (see its own test), so it stays reboot-required by default.
		disabled := true
		desired := minimalConfigSkeleton()
		desired.ClusterConfig.ProxyConfig = &typedconfig.ProxyConfig{Disabled: &disabled}
		newTestPatch(cluster.Name, layerCluster, nil, desired)

		fake := &fakeTalosClient{current: current}
		r := &TalosMachineReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), TalosClient: clientFactory(fake)}

		_, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		Expect(fake.applyCalls).To(BeEmpty())

		got := fetchMachine(m)
		Expect(got.Status.AppliedConfigHash).To(BeEmpty())

		cond := readyCondition(got)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(reasonRebootRequired))

		lock := clusterlock.New(k8sClient, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, 0)
		_, held, err := lock.Holder(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(held).To(BeFalse(), "cluster lock should have been released")
	})

	It("reports the lock's holder and leaves it alone when another identity already holds it", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)

		current := renderWithConfig(cluster, m, bundle, layerCluster, nil, minimalConfigSkeleton())

		disabled := true
		desired := minimalConfigSkeleton()
		desired.ClusterConfig.ProxyConfig = &typedconfig.ProxyConfig{Disabled: &disabled}
		newTestPatch(cluster.Name, layerCluster, nil, desired)

		lock := clusterlock.New(k8sClient, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, 0)
		Expect(lock.Acquire(ctx, "someone-else")).To(Succeed())

		fake := &fakeTalosClient{current: current}
		r := &TalosMachineReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), TalosClient: clientFactory(fake)}

		_, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		Expect(fake.applyCalls).To(BeEmpty())

		cond := readyCondition(fetchMachine(m))
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(reasonClusterLockHeld))
		Expect(cond.Message).To(ContainSubstring("someone-else"))

		holder, held, err := lock.Holder(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(held).To(BeTrue(), "the other identity's lock must not be released by a failed Acquire")
		Expect(holder).To(Equal("someone-else"))
	})

	It("applies nothing and reports reset-and-reinstall for an install-time-only change", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)

		target := &ichorv1alpha1.TalosConfigPatchTarget{MachineRef: &corev1.LocalObjectReference{Name: m.Name}}
		current := renderWithConfig(cluster, m, bundle, "machine", target, minimalConfigSkeleton())

		desired := minimalConfigSkeleton()
		wipe := true
		desired.MachineConfig.MachineInstall = &typedconfig.InstallConfig{InstallDisk: "/dev/sdb", InstallWipe: &wipe}
		newTestPatch(cluster.Name, "machine", target, desired)

		fake := &fakeTalosClient{current: current}
		r := &TalosMachineReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), TalosClient: clientFactory(fake)}

		_, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		Expect(fake.applyCalls).To(BeEmpty())

		got := fetchMachine(m)
		cond := readyCondition(got)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(reasonInstallTimeOnlyChange))
		Expect(cond.Message).To(ContainSubstring("machine.install.disk"))
	})

	It("applies nothing and names the path for a change unsupported on the pinned version", func() {
		bundle := newTestBundle("v1.9")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.9")
		m := newTestMachine(cluster.Name, true)

		current, _, err := render.Render(cluster, m, bundle, nil)
		Expect(err).NotTo(HaveOccurred())

		// SysctlConfig as its own document is only valid from Talos >1.13
		// (pkg/fieldmeta); v1.9 is pinned here, so this is unsupported.
		newTestSysctlPatch(cluster.Name, map[string]string{"net.ipv4.ip_forward": "1"})

		fake := &fakeTalosClient{current: current}
		r := &TalosMachineReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), TalosClient: clientFactory(fake)}

		_, err = reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		Expect(fake.applyCalls).To(BeEmpty())

		got := fetchMachine(m)
		cond := readyCondition(got)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(reasonUnsupportedTalosVersion))
		Expect(cond.Message).To(ContainSubstring("SysctlConfig.params."))
	})

	It("surfaces dry-run warnings in status", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)

		current := renderWithConfig(cluster, m, bundle, layerCluster, nil, minimalConfigSkeleton())

		desired := extraArgsConfig()
		newTestPatch(cluster.Name, layerCluster, nil, desired)

		fake := &fakeTalosClient{current: current, warnings: []string{"deprecated field migrated: example"}}
		r := &TalosMachineReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), TalosClient: clientFactory(fake)}

		_, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		got := fetchMachine(m)
		warn := warningsCondition(got)
		Expect(warn).NotTo(BeNil())
		Expect(warn.Status).To(Equal(metav1.ConditionTrue))
		Expect(warn.Message).To(ContainSubstring("deprecated field migrated: example"))
	})

	It("never modifies spec on any object it touches", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)

		current := renderWithConfig(cluster, m, bundle, layerCluster, nil, minimalConfigSkeleton())

		desired := extraArgsConfig()
		patch := newTestPatch(cluster.Name, layerCluster, nil, desired)

		wantMachineSpec := m.Spec.DeepCopy()
		wantClusterSpec := cluster.Spec.DeepCopy()
		wantPatchSpec := patch.Spec.DeepCopy()

		fake := &fakeTalosClient{current: current}
		r := &TalosMachineReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), TalosClient: clientFactory(fake)}

		_, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		gotMachine := fetchMachine(m)
		Expect(reflect.DeepEqual(&gotMachine.Spec, wantMachineSpec)).To(BeTrue(), "TalosMachine.spec was modified")

		var gotCluster ichorv1alpha1.TalosCluster
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, &gotCluster)).To(Succeed())
		Expect(reflect.DeepEqual(&gotCluster.Spec, wantClusterSpec)).To(BeTrue(), "TalosCluster.spec was modified")

		var gotPatch ichorv1alpha1.TalosConfigPatch
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: patch.Namespace, Name: patch.Name}, &gotPatch)).To(Succeed())
		Expect(reflect.DeepEqual(&gotPatch.Spec, wantPatchSpec)).To(BeTrue(), "TalosConfigPatch.spec was modified")
	})
})
