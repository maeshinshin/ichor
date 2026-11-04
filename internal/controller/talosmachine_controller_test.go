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
	"io"
	"reflect"
	"strconv"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	yaml "go.yaml.in/yaml/v4"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
	"github.com/maeshinshin/ichor/internal/settings"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
	"github.com/maeshinshin/ichor/pkg/cni"
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

	// Shared across every TalosCluster fixture in this package (goconst):
	// the exact values never matter to what each test is actually about.
	testEndpointType    = "vip"
	testTalosVersion    = "v1.14.3"
	testSchedulable     = "true"
	testClusterAddress  = "10.0.0.5"
	testClusterGenerate = "cluster-"
)

// fakeTalosClient is the Talos API boundary test double (unit rule 2: no
// Talos call in these tests ever reaches a real node). Its fields are set
// up per test and its call counts are asserted on, e.g. to confirm a dry
// run never happens on the converged path and an apply never happens on a
// blocked one.
type fakeTalosClient struct {
	volumeFailure string

	current    []byte
	currentErr error

	warnings  []string
	dryRunErr error

	applyErr error

	dryRunCalls int
	// dryRunHook runs inside DryRunApply: a concurrent writer touching the machine mid-operation.
	dryRunHook func()
	// dryRunBudget and applyBudget are the time left on each call's context when it arrived.
	dryRunBudget, applyBudget time.Duration
	applyCalls                [][]byte

	// etcd-related fields: removal_test.go's fixtures for removeEtcdMember.
	etcdMembers []EtcdMember
	// etcdListHook runs inside EtcdMemberList: another reconcile arriving while a removal holds the cluster lock.
	etcdListHook   func()
	etcdMembersErr error
	etcdLeaveErr   error
	etcdLeaveCalls int
	etcdRemoveErr  error
	etcdRemoveIDs  []uint64

	// reset-related fields: etcd_removal_test.go's fixtures for resetNode (ADR 0042).
	resetErr   error
	resetCalls int

	// upgrade-related fields: talos_upgrade_test.go's fixtures for
	// talosUpgradeAdvancer (ADR 0043). version and bootID are mutated
	// directly between Advance calls within one test to simulate a reboot
	// (and, for the A/B-fallback case, a reboot that lands back on the old
	// version) -- there is no auto-incrementing behavior here to fake.
	version      string
	versionErr   error
	bootID       string
	bootIDErr    error
	upgradeErr   error
	upgradeCalls []string
	// upgradeBudget is the time left on the Upgrade call's context when it arrived.
	upgradeBudget time.Duration

	// config-rollout fields: config_rollout_test.go's fixtures (ADR 0080).
	stagedErr    error
	stagedCalls  [][]byte
	rebootErr    error
	rebootCalls  int
	stagedBudget time.Duration
	rebootBudget time.Duration

	// kube-apiserver fields: the config rollout's API server restart wait (ADR 0080 amendment).
	apiInstance    string
	apiInstanceErr error
	apiReadyErr    error
	apiReadyCalls  int
	apiReadyHook   func()

	// shutdown-related fields: shutdown_test.go's fixtures (ADR 0067).
	shutdownErr   error
	shutdownCalls []bool // the force argument of each call, in order

	// kubernetes-upgrade fields: kubernetes_upgrade_test.go's fixtures.
	pulled  []string
	pullErr error

	// stage and stageErr serve talosmachine_provision_test.go (MachineStage).
	stage    string
	stageErr error
	// schematic is the Image Factory schematic the fake node reports ("" = not an Image Factory asset).
	identity     HardwareIdentity
	identityErr  error
	schematic    string
	schematicErr error
	// node-action fields (ADR 0085).
	serviceRestarts   []string
	serviceRestartErr error
	serviceState      string // "" means Running
	serviceUnhealthy  bool
	etcdStatus        EtcdStatus
	etcdStatusErr     error
	etcdAlarms        []string
	etcdAlarmsErr     error
	defragCalls       int
	defragErr         error
	forfeitCalls      int
	forfeitErr        error
	forfeitHook       func()

	// etcd-restore fields (ADR 0089): etcdData is the answer of EtcdDataPresent.
	etcdData              bool
	etcdDataErr           error
	resetEtcdCalls        int
	resetEtcdErr          error
	resetEtcdHook         func()
	recovered             [][]byte
	recoverErr            error
	bootstrapRecoverCalls int
	bootstrapRecoverErr   error
	bootstrapRecoverHook  func()

	// nodeName and nodeNameErr serve NodeName: the Kubernetes node name the node reports.
	nodeName    string
	nodeNameErr error
	kubeconfig  []byte
	manifests   []map[string]any
}

func (f *fakeTalosClient) NodeName(context.Context) (string, error) {
	return f.nodeName, f.nodeNameErr
}

func (f *fakeTalosClient) CurrentConfig(context.Context) ([]byte, error) {
	return f.current, f.currentErr
}

func (f *fakeTalosClient) DryRunApply(ctx context.Context, _ []byte) ([]string, error) {
	f.dryRunCalls++
	if f.dryRunHook != nil {
		f.dryRunHook()
	}
	if dl, ok := ctx.Deadline(); ok {
		f.dryRunBudget = time.Until(dl)
	}

	return f.warnings, f.dryRunErr
}

func (f *fakeTalosClient) ApplyNoReboot(ctx context.Context, cfg []byte) error {
	if dl, ok := ctx.Deadline(); ok {
		f.applyBudget = time.Until(dl)
	}
	f.applyCalls = append(f.applyCalls, cfg)

	return f.applyErr
}

func (f *fakeTalosClient) ApplyStaged(ctx context.Context, cfg []byte) error {
	if dl, ok := ctx.Deadline(); ok {
		f.stagedBudget = time.Until(dl)
	}
	f.stagedCalls = append(f.stagedCalls, cfg)

	return f.stagedErr
}

func (f *fakeTalosClient) Reboot(ctx context.Context) error {
	if dl, ok := ctx.Deadline(); ok {
		f.rebootBudget = time.Until(dl)
	}
	f.rebootCalls++

	return f.rebootErr
}

func (f *fakeTalosClient) EtcdMemberList(context.Context) ([]EtcdMember, error) {
	if f.etcdListHook != nil {
		f.etcdListHook()
	}

	return f.etcdMembers, f.etcdMembersErr
}

func (f *fakeTalosClient) EtcdLeaveCluster(context.Context) error {
	f.etcdLeaveCalls++

	return f.etcdLeaveErr
}

func (f *fakeTalosClient) EtcdRemoveMemberByID(_ context.Context, memberID uint64) error {
	f.etcdRemoveIDs = append(f.etcdRemoveIDs, memberID)

	return f.etcdRemoveErr
}

func (f *fakeTalosClient) Reset(context.Context) error {
	f.resetCalls++

	return f.resetErr
}

func (f *fakeTalosClient) Shutdown(_ context.Context, force bool) error {
	f.shutdownCalls = append(f.shutdownCalls, force)

	return f.shutdownErr
}

func (f *fakeTalosClient) MachineStage(context.Context) (string, error) {
	return f.stage, f.stageErr
}

func (f *fakeTalosClient) VolumeFailures(context.Context) (string, error) {
	return f.volumeFailure, nil
}

// kube-apiserver fields (config_rollout_apiserver_test.go, ADR 0080 amendment).
func (f *fakeTalosClient) APIServerInstance(context.Context) (string, error) {
	return f.apiInstance, f.apiInstanceErr
}

func (f *fakeTalosClient) APIServerReady(context.Context) error {
	f.apiReadyCalls++

	if f.apiReadyHook != nil {
		f.apiReadyHook()
	}

	return f.apiReadyErr
}

func (f *fakeTalosClient) Identity(context.Context) (HardwareIdentity, error) {
	return f.identity, f.identityErr
}

func (f *fakeTalosClient) Schematic(context.Context) (string, error) {
	return f.schematic, f.schematicErr
}

func (f *fakeTalosClient) Version(context.Context) (string, error) {
	return f.version, f.versionErr
}

func (f *fakeTalosClient) BootID(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err // like gRPC: a call on an expired context never reaches the node
	}

	return f.bootID, f.bootIDErr
}

func (f *fakeTalosClient) Upgrade(ctx context.Context, image string) error {
	if dl, ok := ctx.Deadline(); ok {
		f.upgradeBudget = time.Until(dl)
	}
	f.upgradeCalls = append(f.upgradeCalls, image)

	return f.upgradeErr
}

func (f *fakeTalosClient) PullImage(_ context.Context, image string, _ bool) error {
	f.pulled = append(f.pulled, image)

	return f.pullErr
}

func (f *fakeTalosClient) Kubeconfig(context.Context) ([]byte, error) {
	return f.kubeconfig, nil
}

func (f *fakeTalosClient) BootstrapManifests(context.Context) ([]map[string]any, error) {
	return f.manifests, nil
}

func clientFactory(fake *fakeTalosClient) func(
	context.Context, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *secrets.Bundle,
) (TalosClient, error) {
	return func(context.Context, *ichorv1alpha1.TalosCluster, *ichorv1alpha1.TalosMachine, *secrets.Bundle) (TalosClient, error) {
		return fake, nil
	}
}

var (
	testBundleMu    sync.Mutex
	testBundleCache = map[string]*secrets.Bundle{}
)

// newTestBundle returns one shared bundle per version contract: generating one costs a 4096-bit RSA
// key, which dominates the suite under CPU contention. Callers must treat it as read-only; a spec
// that needs its own (or a distinct) bundle calls newFreshTestBundle.
func newTestBundle(versionContract string) *secrets.Bundle {
	testBundleMu.Lock()
	defer testBundleMu.Unlock()

	if b, ok := testBundleCache[versionContract]; ok {
		return b
	}

	b := newFreshTestBundle(versionContract)
	testBundleCache[versionContract] = b

	return b
}

func newFreshTestBundle(versionContract string) *secrets.Bundle {
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
		Data: map[string][]byte{secretsbundle.SecretKey: raw},
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())

	return secret
}

func newTestCluster(secretName, versionContract string) *ichorv1alpha1.TalosCluster {
	cluster := &ichorv1alpha1.TalosCluster{
		GenerateName: testClusterGenerate, Namespace: tmNamespace, Spec: newClusterSpec(secretName, versionContract),
	}
	Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

	return cluster
}

func newTestMachine(clusterName string, approved bool) *ichorv1alpha1.TalosMachine {
	state := ichorv1alpha1.MachineApprovalStatePending
	if approved {
		state = ichorv1alpha1.MachineApprovalStateApproved
	}

	m := &ichorv1alpha1.TalosMachine{
		GenerateName: machinePrefix, Namespace: tmNamespace,
		Spec: ichorv1alpha1.TalosMachineSpec{
			ClusterRef:    corev1.LocalObjectReference{Name: clusterName},
			Identity:      ichorv1alpha1.TalosMachineIdentity{UUID: "uuid-" + clusterName},
			Endpoint:      storageTestEndpoint,
			Role:          roleWorker,
			ApprovalState: state,
		},
	}
	Expect(k8sClient.Create(ctx, m)).To(Succeed())

	return m
}

// newTestPatch creates a cluster-layer (or, with target set, narrower)
// TalosConfigPatch carrying a single typed Config document.
func newTestPatch(clusterName, layer string, target *ichorv1alpha1.TalosConfigPatchTarget, cfg *typedconfig.Config) *ichorv1alpha1.TalosConfigPatch {
	p := &ichorv1alpha1.TalosConfigPatch{
		GenerateName: patchPrefix, Namespace: tmNamespace,
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
		GenerateName: patchPrefix, Namespace: tmNamespace,
		Spec: ichorv1alpha1.TalosConfigPatchSpec{
			ClusterRef: corev1.LocalObjectReference{Name: clusterName},
			Layer:      layerCluster,
			Documents: []ichorv1alpha1.TalosConfigPatchDocument{{
				Kind:         kindSysctl,
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

	b, _, err := render.Render(cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{patch}, nil)
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

		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace}

		_, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		got := fetchMachine(m)
		Expect(got.Status.Conditions).To(BeEmpty())
		Expect(got.Status.ObservedGeneration).To(BeZero())
		Expect(got.Status.AppliedConfigHash).To(BeEmpty())
	})

	It("reports ClusterNotFound when the referenced TalosCluster does not exist", func() {
		cluster := newTestCluster("does-not-exist", "v1.14")
		m := newTestMachine(cluster.Name, true)
		m.Spec.ClusterRef.Name = "no-such-cluster"
		Expect(k8sClient.Update(ctx, m)).To(Succeed())

		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace}
		_, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		cond := readyCondition(fetchMachine(m))
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(reasonClusterNotFound))
		Expect(cond.Message).To(ContainSubstring("no-such-cluster"))
	})

	It("reports SecretsBundleUnavailable when the secrets Secret does not exist", func() {
		cluster := newTestCluster("no-such-secret", "v1.14")
		m := newTestMachine(cluster.Name, true)

		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace}
		_, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		cond := readyCondition(fetchMachine(m))
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(reasonSecretsBundleUnavailable))
	})

	It("records convergence and attempts no apply when hashes match", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)

		current, hash, err := render.Render(cluster, m, bundle, nil, nil)
		Expect(err).NotTo(HaveOccurred())

		fake := &fakeTalosClient{current: current}
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

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

	It("records the node's Kubernetes name in status.nodeName once it is read", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)

		current, _, err := render.Render(cluster, m, bundle, nil, nil)
		Expect(err).NotTo(HaveOccurred())

		fake := &fakeTalosClient{current: current, nodeName: "ichor-cp-1"}
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

		_, err = reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(fetchMachine(m).Status.NodeName).To(Equal("ichor-cp-1"))
	})

	It("reconciles a TalosCluster that declares registries, resolving auth from the controller namespace", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		authSecret := &corev1.Secret{
			GenerateName: "regauth-", Namespace: tmNamespace,
			Data: map[string][]byte{"password": []byte("s3cr3t-pw")},
		}
		Expect(k8sClient.Create(ctx, authSecret)).To(Succeed())

		cluster := newTestCluster(secret.Name, "v1.14")
		cluster.Spec.Registries = []ichorv1alpha1.TalosClusterRegistry{
			{Host: "docker.io", Endpoints: []string{"https://mirror.example.com"}},
			{
				Host: "registry.example.com", Endpoints: []string{"https://private.example.com"}, Username: "deploy",
				AuthSecretRef: &corev1.LocalObjectReference{Name: authSecret.Name},
			},
		}
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		m := newTestMachine(cluster.Name, true)

		auth, err := render.ResolveRegistryAuth(ctx, k8sClient, tmNamespace, cluster)
		Expect(err).NotTo(HaveOccurred())

		current, hash, err := render.Render(cluster, m, bundle, nil, &render.SystemInputs{RegistryAuth: auth})
		Expect(err).NotTo(HaveOccurred())

		fake := &fakeTalosClient{current: current}
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

		_, err = reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		got := fetchMachine(m)
		cond := readyCondition(got)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Reason).To(Equal(reasonConverged), "message: %s", cond.Message)
		Expect(got.Status.AppliedConfigHash).To(Equal(hash))
		Expect(fake.applyCalls).To(BeEmpty())
	})

	It("stays Ready when the node carries the same mirrors in the legacy machine.registries.mirrors form (Ichor upgrade)", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		cluster.Spec.Registries = []ichorv1alpha1.TalosClusterRegistry{
			{Host: "ghcr.io", Endpoints: []string{"http://10.0.0.1:5000"}, SkipFallback: true, OverridePath: true},
		}
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		m := newTestMachine(cluster.Name, true)

		rendered, hash, err := render.Render(cluster, m, bundle, nil, nil)
		Expect(err).NotTo(HaveOccurred())

		fake := &fakeTalosClient{current: legacyMirrorForm(rendered)}
		Expect(string(fake.current)).To(ContainSubstring("mirrors:"))
		Expect(string(fake.current)).NotTo(ContainSubstring("RegistryMirrorConfig"))

		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

		_, err = reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		got := fetchMachine(m)
		cond := readyCondition(got)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Reason).To(Equal(reasonConverged), "message: %s", cond.Message)
		Expect(got.Status.AppliedConfigHash).To(Equal(hash))
		Expect(fake.applyCalls).To(BeEmpty())
	})

	It("applies a non-reboot change with NO_REBOOT and converges hash and observedGeneration", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)

		current := renderWithConfig(cluster, m, bundle, layerCluster, nil, minimalConfigSkeleton())

		desired := extraArgsConfig()
		patch := newTestPatch(cluster.Name, layerCluster, nil, desired)
		wantRendered, wantHash, err := render.Render(cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{*patch}, nil)
		Expect(err).NotTo(HaveOccurred())

		fake := &fakeTalosClient{current: current}
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

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

	It("applies nothing and never takes the cluster lock for a reboot-causing change", func() {
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
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

		_, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		Expect(fake.applyCalls).To(BeEmpty())
		Expect(fake.stagedCalls).To(BeEmpty(), "no operation, so nothing is staged")
		Expect(fake.rebootCalls).To(BeZero(), "no operation, so nothing reboots")

		got := fetchMachine(m)
		Expect(got.Status.AppliedConfigHash).To(BeEmpty())

		cond := readyCondition(got)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(reasonRebootRequired))
		Expect(cond.Message).To(ContainSubstring("cluster.proxy.disabled"), "the message names the field that needs the reboot")
		Expect(cond.Message).To(ContainSubstring("ichor config apply"), "and says how to apply it")
		Expect(got.Status.RebootPaths).To(ContainElement("cluster.proxy.disabled"))

		lock := clusterlock.New(k8sClient, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, 0)
		_, held, err := lock.Holder(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(held).To(BeFalse(), "refuseReboot never takes the cluster lock")
	})

	It("never touches a cluster lock a running operation holds as the controller", func() {
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
		Expect(lock.Acquire(ctx, controllerIdentity)).To(Succeed())

		fake := &fakeTalosClient{current: current}
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

		_, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		holder, held, err := lock.Holder(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(held).To(BeTrue())
		Expect(holder).To(Equal(controllerIdentity))
		Expect(fake.stagedCalls).To(BeEmpty())
		Expect(fake.rebootCalls).To(BeZero(), "the machine reconciler never reboots a node")
	})

	It("reports RebootRequired and leaves another identity's cluster lock alone", func() {
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
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

		_, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		Expect(fake.applyCalls).To(BeEmpty())

		cond := readyCondition(fetchMachine(m))
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(reasonRebootRequired))

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
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

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

		current, _, err := render.Render(cluster, m, bundle, nil, nil)
		Expect(err).NotTo(HaveOccurred())

		// SysctlConfig as its own document is only valid from Talos >1.13
		// (pkg/fieldmeta); v1.9 is pinned here, so this is unsupported.
		newTestSysctlPatch(cluster.Name, map[string]string{"net.ipv4.ip_forward": "1"})

		fake := &fakeTalosClient{current: current}
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

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
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

		_, err := reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		got := fetchMachine(m)
		warn := warningsCondition(got)
		Expect(warn).NotTo(BeNil())
		Expect(warn.Status).To(Equal(metav1.ConditionTrue))
		Expect(warn.Message).To(ContainSubstring("deprecated field migrated: example"))
	})

	It("applies on the first reconcile even with driftPolicy warnOnly, since there is no prior convergence to compare against", func() {
		// "Sensibly" here means: a hash mismatch with nothing recorded yet
		// in status.observedInputsHash is treated as a desired-state
		// change, exactly like the "applies a non-reboot change" test
		// above -- never as drift, since drift requires a previous
		// convergence to compare against and this machine has none.
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		Expect(cluster.Spec.DriftPolicy).To(Equal(settings.DriftPolicyDefault), "set by the helper")
		m := newTestMachine(cluster.Name, true)

		current := renderWithConfig(cluster, m, bundle, layerCluster, nil, minimalConfigSkeleton())

		desired := extraArgsConfig()
		patch := newTestPatch(cluster.Name, layerCluster, nil, desired)
		wantRendered, wantHash, err := render.Render(cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{*patch}, nil)
		Expect(err).NotTo(HaveOccurred())

		fake := &fakeTalosClient{current: current}
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

		_, err = reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		Expect(fake.applyCalls).To(HaveLen(1))
		Expect(fake.applyCalls[0]).To(Equal(wantRendered))

		got := fetchMachine(m)
		Expect(got.Status.AppliedConfigHash).To(Equal(wantHash))
		Expect(got.Status.ObservedInputsHash).NotTo(BeEmpty())

		cond := readyCondition(got)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(reasonApplied))
	})

	It("reports drift and applies nothing when driftPolicy is warnOnly and the node moved on its own", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)

		patch := newTestPatch(cluster.Name, layerCluster, nil, extraArgsConfig())
		wantRendered, wantHash, err := render.Render(cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{*patch}, nil)
		Expect(err).NotTo(HaveOccurred())

		fake := &fakeTalosClient{current: wantRendered}
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

		// First reconcile converges: nothing to apply, but it records the
		// fingerprint of what fed this render (cluster, machine, patch).
		_, err = reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(fetchMachine(m).Status.AppliedConfigHash).To(Equal(wantHash))

		// Now the node drifts on its own: a safe field (cluster.apiServer,
		// per pkg/fieldmeta) changes directly on the node, with nothing on
		// Ichor's side touched -- cluster, the patch and the machine all
		// keep the same generation.
		drifted := extraArgsConfig()
		drifted.ClusterConfig.APIServerConfig.ExtraArgsConfig["foo"] = []string{"c"}
		driftedCurrent := renderWithConfig(cluster, m, bundle, layerCluster, nil, drifted)
		fake.current = driftedCurrent

		_, err = reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		Expect(fake.applyCalls).To(BeEmpty())

		got := fetchMachine(m)
		Expect(got.Status.AppliedConfigHash).To(Equal(wantHash), "left at the last known-good hash, not the node's drifted one")

		cond := readyCondition(got)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(reasonNodeDrifted))
		Expect(cond.Message).To(ContainSubstring("cluster.apiServer.extraArgs.foo"))
		Expect(cond.Message).To(ContainSubstring("warnOnly"))
	})

	It("reports drift and applies nothing while hold-apply is set, then applies once it is removed", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)

		patch := newTestPatch(cluster.Name, layerCluster, nil, extraArgsConfig())
		wantRendered, wantHash, err := render.Render(cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{*patch}, nil)
		Expect(err).NotTo(HaveOccurred())

		m.Annotations = map[string]string{ichorv1alpha1.TalosMachineHoldApplyAnnotation: strconv.FormatBool(true)}
		Expect(k8sClient.Update(ctx, m)).To(Succeed())

		// The node differs from the render (a first-time mismatch, which would otherwise be applied).
		other := extraArgsConfig()
		other.ClusterConfig.APIServerConfig.ExtraArgsConfig["foo"] = []string{"c"}

		fake := &fakeTalosClient{current: renderWithConfig(cluster, m, bundle, layerCluster, nil, other)}
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

		_, err = reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.applyCalls).To(BeEmpty())

		got := fetchMachine(m)
		Expect(got.Status.AppliedConfigHash).To(BeEmpty())

		cond := readyCondition(got)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(reasonAdoptionHeld))
		Expect(cond.Message).To(ContainSubstring("cluster.apiServer.extraArgs.foo"))
		Expect(apimeta.IsStatusConditionTrue(got.Status.Conditions, conditionAdoptionHeld)).To(BeTrue())

		// Release the hold: the controller applies the render.
		got.Annotations = nil
		Expect(k8sClient.Update(ctx, got)).To(Succeed())

		_, err = reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.applyCalls).To(HaveLen(1))
		Expect(fake.applyCalls[0]).To(Equal(wantRendered))

		got = fetchMachine(m)
		Expect(got.Status.AppliedConfigHash).To(Equal(wantHash))
		Expect(apimeta.FindStatusCondition(got.Status.Conditions, conditionAdoptionHeld)).To(BeNil())
	})

	It("reports Converged while hold-apply is set when the node differs only in the legacy registry mirror form", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		cluster.Spec.Registries = []ichorv1alpha1.TalosClusterRegistry{
			{Host: "ghcr.io", Endpoints: []string{"http://10.0.0.1:5000"}, SkipFallback: true, OverridePath: true},
		}
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		m := newTestMachine(cluster.Name, true)
		m.Annotations = map[string]string{ichorv1alpha1.TalosMachineHoldApplyAnnotation: strconv.FormatBool(true)}
		Expect(k8sClient.Update(ctx, m)).To(Succeed())

		rendered, hash, err := render.Render(cluster, m, bundle, nil, nil)
		Expect(err).NotTo(HaveOccurred())

		fake := &fakeTalosClient{current: legacyMirrorForm(rendered)}
		Expect(string(fake.current)).NotTo(ContainSubstring("RegistryMirrorConfig"))

		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

		_, err = reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.applyCalls).To(BeEmpty())

		got := fetchMachine(m)
		cond := readyCondition(got)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(reasonConverged), "message: %s", cond.Message)
		Expect(got.Status.AppliedConfigHash).To(Equal(hash))
		Expect(apimeta.IsStatusConditionTrue(got.Status.Conditions, conditionAdoptionHeld)).To(BeTrue())
	})

	It("applies a render change from an Ichor upgrade under warnOnly instead of calling it drift, and a restart re-applies nothing", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)

		patch := newTestPatch(cluster.Name, layerCluster, nil, extraArgsConfig())
		wantRendered, wantHash, err := render.Render(cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{*patch}, nil)
		Expect(err).NotTo(HaveOccurred())

		fake := &fakeTalosClient{current: wantRendered}
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

		_, err = reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(fetchMachine(m).Status.AppliedConfigHash).To(Equal(wantHash))

		// A restart on the same Ichor changes nothing: a fresh reconciler, same version, no apply.
		_, err = reconcileMachine(&TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.applyCalls).To(BeEmpty())

		// The node holds what the previous Ichor rendered, which differs from what this one does,
		// while no CR generation moved.
		old := extraArgsConfig()
		old.ClusterConfig.APIServerConfig.ExtraArgsConfig["foo"] = []string{"c"}
		fake.current = renderWithConfig(cluster, m, bundle, layerCluster, nil, old)

		prev := renderVersion
		renderVersion = prev + 1

		DeferCleanup(func() { renderVersion = prev })

		_, err = reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		Expect(fake.applyCalls).To(HaveLen(1))
		Expect(fake.applyCalls[0]).To(Equal(wantRendered))

		cond := readyCondition(fetchMachine(m))
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(reasonApplied))

		// Converged at the new version, the same difference is drift again.
		fake.current = renderWithConfig(cluster, m, bundle, layerCluster, nil, old)
		fake.applyCalls = nil

		_, err = reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.applyCalls).To(BeEmpty())
		Expect(readyCondition(fetchMachine(m)).Reason).To(Equal(reasonNodeDrifted))
	})

	It("applies the CNI inlineManifests removal under warnOnly once the hand-over completes, not NodeDrifted", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)

		cfg := minimalConfigSkeleton()
		cfg.ClusterConfig.ClusterInlineManifests = []*typedconfig.ClusterInlineManifest{
			{InlineManifestName: cni.InlineManifestName, InlineManifestContents: "kind: List"},
		}
		patch := newTestPatch(cluster.Name, layerCluster, nil, cfg)

		before, _, err := render.Render(cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{*patch}, nil)
		Expect(err).NotTo(HaveOccurred())

		fake := &fakeTalosClient{current: before}
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

		_, err = reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(readyCondition(fetchMachine(m)).Reason).To(Equal(reasonConverged))

		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, cluster)).To(Succeed())
		// The hand-over completes: a status write, no generation moves.
		apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type: render.ConditionCNIHandover, Status: metav1.ConditionTrue, Reason: "Complete", Message: "handed over",
		})
		Expect(k8sClient.Status().Update(ctx, cluster)).To(Succeed())

		after, afterHash, err := render.Render(cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{*patch}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(after).NotTo(Equal(before))

		_, err = reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		Expect(fake.applyCalls).To(HaveLen(1))
		Expect(fake.applyCalls[0]).To(Equal(after))

		got := fetchMachine(m)
		Expect(got.Status.AppliedConfigHash).To(Equal(afterHash))
		Expect(readyCondition(got).Reason).NotTo(Equal(reasonNodeDrifted))
	})

	It("corrects drift when driftPolicy is autoCorrect and every changed path is reboot-free", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)

		// driftPolicy is set before the converging reconcile below, not
		// between it and the drift reconcile: a later change would itself
		// bump cluster.Generation and be indistinguishable, by this
		// mechanism, from a render-affecting change (see the package doc
		// comment's note on this). Setting it up front keeps this test
		// about drift, not about that edge case.
		cluster.Spec.DriftPolicy = driftPolicyAutoCorrect
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		patch := newTestPatch(cluster.Name, layerCluster, nil, extraArgsConfig())
		wantRendered, wantHash, err := render.Render(cluster, m, bundle, []ichorv1alpha1.TalosConfigPatch{*patch}, nil)
		Expect(err).NotTo(HaveOccurred())

		fake := &fakeTalosClient{current: wantRendered}
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

		_, err = reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(fetchMachine(m).Status.AppliedConfigHash).To(Equal(wantHash))

		drifted := extraArgsConfig()
		drifted.ClusterConfig.APIServerConfig.ExtraArgsConfig["foo"] = []string{"c"}
		fake.current = renderWithConfig(cluster, m, bundle, layerCluster, nil, drifted)

		_, err = reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		Expect(fake.applyCalls).To(HaveLen(1))
		Expect(fake.applyCalls[0]).To(Equal(wantRendered), "re-applied the rendered configuration, correcting the drift")

		got := fetchMachine(m)
		Expect(got.Status.AppliedConfigHash).To(Equal(wantHash))

		cond := readyCondition(got)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(reasonDriftCorrected))
	})

	It("reports drift and applies nothing when driftPolicy is autoCorrect but a changed path needs a reboot", func() {
		bundle := newTestBundle("v1.14")
		secret := newTestSecret(bundle)
		cluster := newTestCluster(secret.Name, "v1.14")
		m := newTestMachine(cluster.Name, true)

		// See the "corrects drift" test above: driftPolicy is set before the
		// converging reconcile, not after, so flipping it doesn't itself
		// look like a render-affecting change.
		cluster.Spec.DriftPolicy = driftPolicyAutoCorrect
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		baseline, baseHash, err := render.Render(cluster, m, bundle, nil, nil)
		Expect(err).NotTo(HaveOccurred())

		fake := &fakeTalosClient{current: baseline}
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

		_, err = reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(fetchMachine(m).Status.AppliedConfigHash).To(Equal(baseHash))

		// cluster.proxy.disabled is deliberately uncovered by pkg/fieldmeta's
		// table, so it stays reboot-required by default (see the existing
		// reboot test above) -- drift there must still refuse to apply even
		// though the policy is autoCorrect.
		disabled := true
		driftedCfg := minimalConfigSkeleton()
		driftedCfg.ClusterConfig.ProxyConfig = &typedconfig.ProxyConfig{Disabled: &disabled}
		fake.current = renderWithConfig(cluster, m, bundle, layerCluster, nil, driftedCfg)

		_, err = reconcileMachine(r, m)
		Expect(err).NotTo(HaveOccurred())

		Expect(fake.applyCalls).To(BeEmpty())

		got := fetchMachine(m)
		Expect(got.Status.AppliedConfigHash).To(Equal(baseHash))

		cond := readyCondition(got)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(reasonRebootRequired))
		Expect(cond.Message).To(ContainSubstring("drifted"))

		lock := clusterlock.New(k8sClient, types.NamespacedName{Namespace: cluster.Namespace, Name: cluster.Name}, 0)
		_, held, err := lock.Holder(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(held).To(BeFalse(), "cluster lock should have been released")
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
		r := &TalosMachineReconciler{Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, TalosClient: clientFactory(fake)}

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

// node-action fields (node_action_test.go, ADR 0085).
func (f *fakeTalosClient) ServiceRestart(_ context.Context, id string) error {
	f.serviceRestarts = append(f.serviceRestarts, id)

	return f.serviceRestartErr
}

func (f *fakeTalosClient) ServiceState(context.Context, string) (string, bool, bool, error) {
	state := f.serviceState
	if state == "" {
		state = talosServiceRunning
	}

	return state, true, !f.serviceUnhealthy, nil
}

func (f *fakeTalosClient) EtcdStatus(context.Context) (EtcdStatus, error) {
	return f.etcdStatus, f.etcdStatusErr
}

func (f *fakeTalosClient) EtcdAlarms(context.Context) ([]string, error) {
	return f.etcdAlarms, f.etcdAlarmsErr
}

func (f *fakeTalosClient) EtcdDefragment(context.Context) error {
	f.defragCalls++

	return f.defragErr
}

func (f *fakeTalosClient) EtcdForfeitLeadership(context.Context) error {
	f.forfeitCalls++
	if f.forfeitHook != nil {
		f.forfeitHook()
	}

	return f.forfeitErr
}

// etcd-restore fields (etcd_restore_test.go, ADR 0089).
func (f *fakeTalosClient) EtcdDataPresent(context.Context) (bool, error) {
	return f.etcdData, f.etcdDataErr
}

func (f *fakeTalosClient) ResetEtcdData(context.Context) error {
	f.resetEtcdCalls++
	if f.resetEtcdHook != nil {
		f.resetEtcdHook()
	}

	return f.resetEtcdErr
}

func (f *fakeTalosClient) EtcdRecover(_ context.Context, snapshot io.Reader) error {
	b, err := io.ReadAll(snapshot)
	if err != nil {
		return err
	}

	f.recovered = append(f.recovered, b)

	return f.recoverErr
}

func (f *fakeTalosClient) BootstrapRecover(context.Context) error {
	f.bootstrapRecoverCalls++
	if f.bootstrapRecoverHook != nil {
		f.bootstrapRecoverHook()
	}

	return f.bootstrapRecoverErr
}
