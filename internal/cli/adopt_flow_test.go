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

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
	"github.com/maeshinshin/ichor/pkg/render"
)

const (
	flowCP     = "cp"
	flowWorker = "wk"
	flowCPAddr = "10.0.0.10"
	flowWKAddr = "10.0.0.11"
)

// flowEnv is a cluster of one control plane and one worker, both held, whose live configuration
// is whatever liveCluster renders.
type flowEnv struct {
	cluster *ichorv1alpha1.TalosCluster
	k8s     client.Client
	// writes records every mutating call, in order, as "verb kind/name".
	writes *[]string
	// mu guards writes: a test's stand-in controller writes beside the adopt run.
	mu sync.Mutex
	// released records the machines whose hold annotation was removed, in order.
	released *[]string
	live     map[string][]byte
	// afterGet, when set before the run starts, sees every object a Get found.
	afterGet func(client.Object)
	stdout   bytes.Buffer
	stderr   bytes.Buffer
}

func newFlowEnv(t *testing.T, adoptPreset string, liveMutate func(*ichorv1alpha1.TalosCluster)) *flowEnv {
	t.Helper()

	bundle := newTestBundle(t)

	cluster := newTestCluster()
	cluster.Spec.CNI = ichorv1alpha1.TalosClusterCNI{Preset: adoptPreset, KubeProxy: kubeProxyEnabled}

	if adoptPreset == ichorv1alpha1.TalosClusterCNIPresetUnmanaged {
		cluster.Spec.CNI.Unmanaged = &ichorv1alpha1.TalosClusterCNIUnmanaged{TalosCNI: testNamespace}
	}

	liveCluster := cluster.DeepCopy()
	if liveMutate != nil {
		liveMutate(liveCluster)
	}

	mk := func(name, role, addr string) *ichorv1alpha1.TalosMachine {
		m := newTestMachine(cluster.Name)
		m.Name, m.Spec.Role, m.Spec.Endpoint = name, role, addr
		m.Annotations = map[string]string{ichorv1alpha1.TalosMachineHoldApplyAnnotation: annotationTrue}
		m.Status.Conditions = []metav1.Condition{{
			Type: conditionReady, Status: metav1.ConditionTrue, Reason: "Test", LastTransitionTime: metav1.Now(),
		}}

		return m
	}

	cp, wk := mk(flowCP, roleControlPlane, flowCPAddr), mk(flowWorker, testRoleWorker, flowWKAddr)

	env := &flowEnv{cluster: cluster, live: map[string][]byte{}, writes: &[]string{}, released: &[]string{}}

	for _, m := range []*ichorv1alpha1.TalosMachine{cp, wk} {
		cfg, _, err := render.Render(liveCluster, m, bundle, nil, nil)
		if err != nil {
			t.Fatalf("rendering the live configuration of %s: %v", m.Name, err)
		}

		env.live[m.Spec.Endpoint] = cfg
	}

	record := func(verb string, obj client.Object) {
		env.mu.Lock()
		defer env.mu.Unlock()

		*env.writes = append(*env.writes, verb+" "+obj.GetName())
	}

	env.k8s = fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster, cp, wk).
		WithInterceptorFuncs(interceptor.Funcs{
			// init applies server-side now; the order checks still read it as a create.
			Apply: func(ctx context.Context, c client.WithWatch, obj runtime.ApplyConfiguration, o ...client.ApplyOption) error {
				u := &unstructured.Unstructured{}
				b, _ := json.Marshal(obj)
				_ = u.UnmarshalJSON(b)
				record("create", u)

				return c.Apply(ctx, obj, o...)
			},
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, o ...client.CreateOption) error {
				record("create", obj)

				return c.Create(ctx, obj, o...)
			},
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, o ...client.GetOption) error {
				err := c.Get(ctx, key, obj, o...)
				if err == nil && env.afterGet != nil {
					env.afterGet(obj)
				}

				return err
			},
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, o ...client.UpdateOption) error {
				record("update", obj)

				return c.Update(ctx, obj, o...)
			},
			Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, o ...client.DeleteOption) error {
				record("delete", obj)

				return c.Delete(ctx, obj, o...)
			},
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, p client.Patch, o ...client.PatchOption) error {
				record("patch", obj)

				if _, held := obj.GetAnnotations()[ichorv1alpha1.TalosMachineHoldApplyAnnotation]; !held {
					*env.released = append(*env.released, obj.GetName())
				}

				return c.Patch(ctx, obj, p, o...)
			},
		}).Build()

	return env
}

func (e *flowEnv) deps(t *testing.T) adoptDeps {
	t.Helper()

	return adoptDeps{
		K8s: e.k8s, Bundle: newTestBundle(t),
		DialTalos: func(_ context.Context, endpoint string) (talosConfigReader, error) {
			return &versionedReader{current: e.live[endpoint], version: testTalosVersion}, nil
		},
		Stdout: &e.stdout, Stderr: &e.stderr, Stdin: strings.NewReader(""), Output: outputYAML,
		ReleaseWait: time.Second, Interval: 5 * time.Millisecond,
	}
}

func (e *flowEnv) held(t *testing.T, name string) bool {
	t.Helper()

	var m ichorv1alpha1.TalosMachine
	if err := e.k8s.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: name}, &m); err != nil {
		t.Fatal(err)
	}

	return m.Annotations[ichorv1alpha1.TalosMachineHoldApplyAnnotation] == annotationTrue
}

func unmanagedLive(c *ichorv1alpha1.TalosCluster) {
	c.Spec.CNI = ichorv1alpha1.TalosClusterCNI{
		Preset: ichorv1alpha1.TalosClusterCNIPresetUnmanaged, KubeProxy: kubeProxyEnabled,
		Unmanaged: &ichorv1alpha1.TalosClusterCNIUnmanaged{TalosCNI: testNamespace},
	}
}

// A managed cluster whose nodes still run Talos's flannel selection: the CNI paths are Ichor's
// (class B), listed for consent, never a block.
func TestAdoptPlanListsClassBAndWritesNothing(t *testing.T) {
	env := newFlowEnv(t, cniCilium, unmanagedLive)
	deps := env.deps(t)
	deps.PlanOnly = true

	if err := runAdopt(context.Background(), env.cluster.Name, testNamespace, deps); err != nil {
		t.Fatalf("runAdopt: %v\n%s", err, env.stderr.String())
	}

	if len(*env.writes) != 0 {
		t.Fatalf("--plan wrote: %v", *env.writes)
	}

	out := env.stderr.String()

	if !strings.Contains(out, "Ichor-owned settings") || !strings.Contains(out, "need consent") {
		t.Fatalf("class B was not listed for consent:\n%s", out)
	}

	if strings.Contains(out, "cannot patch automatically") {
		t.Fatalf("class B must not block a machine:\n%s", out)
	}

	if !strings.Contains(out, "--plan: nothing was written") {
		t.Fatalf("missing the plan-only line:\n%s", out)
	}
}

// Without consent nothing is written; with --yes the holds are released in order and the CA
// rotation advice is printed.
func TestAdoptNeedsConsentForClassB(t *testing.T) {
	env := newFlowEnv(t, cniCilium, unmanagedLive)
	deps := env.deps(t)
	deps.Stdin = strings.NewReader("n\n")

	if err := runAdopt(context.Background(), env.cluster.Name, testNamespace, deps); err == nil {
		t.Fatal("runAdopt succeeded without consent")
	}

	if len(*env.writes) != 0 {
		t.Fatalf("wrote before consent: %v", *env.writes)
	}

	if !strings.Contains(env.stderr.String(), "Ichor-owned") {
		t.Fatalf("the plan did not list class B:\n%s", env.stderr.String())
	}
}

func TestAdoptReleasesHoldInOrderAndPrintsAdvice(t *testing.T) {
	for _, preset := range []string{cniCilium, ichorv1alpha1.TalosClusterCNIPresetUnmanaged} {
		t.Run(preset, func(t *testing.T) {
			var live func(*ichorv1alpha1.TalosCluster)
			if preset == cniCilium {
				live = unmanagedLive
			}

			env := newFlowEnv(t, preset, live)
			deps := env.deps(t)
			deps.AssumeYes = true
			deps.CNIUnmanaged = preset == ichorv1alpha1.TalosClusterCNIPresetUnmanaged

			if err := runAdopt(context.Background(), env.cluster.Name, testNamespace, deps); err != nil {
				t.Fatalf("runAdopt: %v\n%s", err, env.stderr.String())
			}

			if want := []string{flowCP, flowWorker}; !slices.Equal(*env.released, want) {
				t.Fatalf("released %v, want %v (control plane first)", *env.released, want)
			}

			if env.held(t, flowCP) || env.held(t, flowWorker) {
				t.Fatal("a machine is still held")
			}

			if !strings.Contains(env.stderr.String(), caRotationAdvice) {
				t.Fatalf("the CA rotation advice was not printed:\n%s", env.stderr.String())
			}

			if preset == ichorv1alpha1.TalosClusterCNIPresetUnmanaged && strings.Contains(env.stderr.String(), "Ichor-owned") {
				t.Fatalf("an unmanaged plan lists class B items:\n%s", env.stderr.String())
			}
		})
	}
}

// A machine whose live configuration still drifts after the write keeps its hold, and so does
// every machine after it; the one before it was released.
func TestAdoptKeepsHoldWhereLiveDriftRemains(t *testing.T) {
	env := newFlowEnv(t, ichorv1alpha1.TalosClusterCNIPresetUnmanaged, nil)

	// The worker drifts in a legacy field only after the plan was made.
	drifting := &flipReader{first: env.live[flowWKAddr]}

	m := newTestMachine(env.cluster.Name)
	m.Name, m.Spec.Role = flowWorker, testRoleWorker

	later, _, err := render.Render(withAPIServerArg(env.cluster), m, newTestBundle(t), nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	drifting.then = later

	deps := env.deps(t)
	deps.AssumeYes = true
	deps.CNIUnmanaged = true
	deps.DialTalos = func(_ context.Context, endpoint string) (talosConfigReader, error) {
		if endpoint == flowWKAddr {
			return drifting, nil
		}

		return &versionedReader{current: env.live[endpoint], version: testTalosVersion}, nil
	}

	err = runAdopt(context.Background(), env.cluster.Name, testNamespace, deps)
	if err == nil || !strings.Contains(err.Error(), flowWorker) {
		t.Fatalf("runAdopt = %v, want the worker's drift to stop the release", err)
	}

	if env.held(t, flowCP) {
		t.Fatal("the control plane should have been released")
	}

	if !env.held(t, flowWorker) {
		t.Fatal("the drifting worker lost its hold")
	}

	if strings.Contains(env.stderr.String(), caRotationAdvice) {
		t.Fatal("advice printed although the adoption did not finish")
	}
}

// flipReader returns first for the plan's read and then for every later read.
type flipReader struct {
	first, then []byte
	reads       int
}

func (f *flipReader) CurrentConfig(context.Context) ([]byte, error) {
	f.reads++
	// reads: version gate does not read config; plan read is #1.
	if f.reads == 1 {
		return f.first, nil
	}

	return f.then, nil
}

func (f *flipReader) TalosVersion(context.Context) (string, error) { return testTalosVersion, nil }

func withAPIServerArg(c *ichorv1alpha1.TalosCluster) *ichorv1alpha1.TalosCluster {
	out := c.DeepCopy()
	out.Spec.Network = &ichorv1alpha1.TalosClusterNetwork{PodSubnets: []string{"10.77.0.0/16"}}

	return out
}

// Either kube-proxy mode with KubePrism off is refused (1.6) before anything is written: the CNI Job
// needs KubePrism, and enabling it needs a reboot.
func TestAdoptRefusesKubeProxyWithoutKubePrism(t *testing.T) {
	tests := []struct {
		name    string
		replace bool
		want    []string
	}{
		{kubeProxyReplaced, true, []string{"KubePrism"}},
		{kubeProxyEnabled, false, []string{"KubePrism is off on machine", "port 7445"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newFlowEnv(t, cniCilium, nil)

			if tc.replace {
				env.cluster.Spec.CNI.KubeProxy = kubeProxyReplaced

				if err := env.k8s.Update(context.Background(), env.cluster); err != nil {
					t.Fatal(err)
				}
			}

			*env.writes = nil

			for addr, cfg := range env.live {
				env.live[addr] = dropDocument(cfg, "KubePrismConfig")
			}

			deps := env.deps(t)
			deps.AssumeYes = true

			err := runAdopt(context.Background(), env.cluster.Name, testNamespace, deps)
			for _, w := range tc.want {
				if err == nil || !strings.Contains(err.Error(), w) {
					t.Fatalf("runAdopt = %v, want the KubePrism refusal with %q", err, w)
				}
			}

			if len(*env.writes) != 0 {
				t.Fatalf("wrote despite the refusal: %v", *env.writes)
			}
		})
	}
}

// A cluster Ichor has never seen needs --node to find its machines.
func TestAdoptNewClusterNeedsNode(t *testing.T) {
	env := newFlowEnv(t, cniCilium, nil)
	deps := env.deps(t)

	err := runAdopt(context.Background(), "other-cluster", testNamespace, deps)
	if err == nil || !strings.Contains(err.Error(), "--node") {
		t.Fatalf("runAdopt = %v, want the --node hint", err)
	}

	if len(*env.writes) != 0 {
		t.Fatalf("wrote: %v", *env.writes)
	}
}

// The unmanaged plan says what the CNI is and that Ichor leaves it alone.
func TestAdoptUnmanagedPlanNamesTheDetectedCNI(t *testing.T) {
	env := newFlowEnv(t, ichorv1alpha1.TalosClusterCNIPresetUnmanaged, nil)
	deps := env.deps(t)
	deps.PlanOnly = true
	deps.CNIUnmanaged = true
	deps.Target = fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(testDS("kube-flannel", flannelImage)).Build()

	if err := runAdopt(context.Background(), env.cluster.Name, testNamespace, deps); err != nil {
		t.Fatalf("runAdopt: %v\n%s", err, env.stderr.String())
	}

	if !strings.Contains(env.stderr.String(), "cni: unmanaged (flannel, not managed by Ichor)") {
		t.Fatalf("missing the unmanaged line:\n%s", env.stderr.String())
	}
}

// A managed Cilium that needs a takeover is not released while the takeover seam is absent, and
// the plan says the checks are pending.
func TestAdoptHelmCiliumWithoutTakeoverStaysHeld(t *testing.T) {
	env := newFlowEnv(t, cniCilium, nil)
	deps := env.deps(t)
	deps.AssumeYes = true
	deps.Target = fake.NewClientBuilder().WithScheme(newTestScheme(t)).
		WithObjects(testDS("cilium", ciliumImage), ciliumCM("false"), helmRecord("kube-system")).Build()

	if err := runAdopt(context.Background(), env.cluster.Name, testNamespace, deps); err != nil {
		t.Fatalf("runAdopt: %v\n%s", err, env.stderr.String())
	}

	out := env.stderr.String()

	for _, want := range []string{"Cilium takeover checks: pending", "machines stay held"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}

	if !env.held(t, flowCP) || !env.held(t, flowWorker) {
		t.Fatal("machines were released before the takeover")
	}
}

// dropDocument removes the multi-document YAML document of the given kind.
func dropDocument(cfg []byte, kind string) []byte {
	var kept []string

	for doc := range strings.SplitSeq(string(cfg), "\n---\n") {
		if !strings.Contains(doc, "kind: "+kind+"\n") {
			kept = append(kept, doc)
		}
	}

	return []byte(strings.Join(kept, "\n---\n"))
}

// membersReader is a node that also lists the cluster membership.
type membersReader struct {
	versionedReader
	members []ClusterMember
}

func (m *membersReader) ClusterMembers(context.Context) ([]ClusterMember, error) {
	return m.members, nil
}

// A cluster Ichor has never seen is discovered from one control plane's membership: --plan writes
// nothing; a real run creates the adopted, held objects (the release then waits for a controller
// that this test does not have).
func TestAdoptDiscoversANewCluster(t *testing.T) {
	env := newFlowEnv(t, cniCilium, nil)

	if err := env.k8s.Delete(context.Background(), env.cluster); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{flowCP, flowWorker} {
		var m ichorv1alpha1.TalosMachine
		if err := env.k8s.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: name}, &m); err != nil {
			t.Fatal(err)
		}

		if err := env.k8s.Delete(context.Background(), &m); err != nil {
			t.Fatal(err)
		}
	}

	*env.writes = nil

	members := []ClusterMember{
		{Hostname: "cp", Addresses: []string{flowCPAddr}, Role: roleControlPlane},
		{Hostname: "wk", Addresses: []string{flowWKAddr}, Role: testRoleWorker},
	}

	deps := env.deps(t)
	deps.CNIUnmanaged = true
	deps.Seed = flowCPAddr
	deps.ContractOverride = testVersionContract
	deps.ReleaseWait = 50 * time.Millisecond
	deps.DialTalos = func(_ context.Context, endpoint string) (talosConfigReader, error) {
		return &membersReader{current: env.live[endpoint], version: testTalosVersion, members: members}, nil
	}
	deps.ReadIdentity = func(_ context.Context, address string) (controller.HardwareIdentity, error) {
		return controller.HardwareIdentity{UUID: "uuid-" + address, Serial: "s"}, nil
	}

	deps.PlanOnly = true

	if err := runAdopt(context.Background(), env.cluster.Name, testNamespace, deps); err != nil {
		t.Fatalf("plan: %v\n%s", err, env.stderr.String())
	}

	if len(*env.writes) != 0 {
		t.Fatalf("--plan wrote: %v", *env.writes)
	}

	deps.PlanOnly = false
	deps.AssumeYes = true

	// The machines are Ready only once a controller runs; without one the release times out, after
	// the objects were written and the first hold was removed.
	err := runAdopt(context.Background(), env.cluster.Name, testNamespace, deps)
	if err == nil || !strings.Contains(err.Error(), "did not become Ready") {
		t.Fatalf("runAdopt = %v, want the Ready timeout\n%s", err, env.stderr.String())
	}

	var got ichorv1alpha1.TalosCluster
	if err := env.k8s.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: env.cluster.Name}, &got); err != nil {
		t.Fatal(err)
	}

	if got.Annotations[ichorv1alpha1.TalosClusterAdoptedAnnotation] != annotationTrue || got.Spec.CNI.Preset != ichorv1alpha1.TalosClusterCNIPresetUnmanaged ||
		got.Spec.ControlPlane.Count != 1 {
		t.Fatalf("unexpected TalosCluster: %+v", got)
	}

	if !env.held(t, "uuid-"+flowWKAddr) {
		t.Fatal("the worker must still be held behind the control plane")
	}
}

func ciliumFlowEnv(t *testing.T, preflight ciliumPreflight) (*flowEnv, adoptDeps) {
	t.Helper()

	env := newFlowEnv(t, cniCilium, nil)
	deps := env.deps(t)
	deps.AssumeYes = true
	deps.Preflight = preflight
	deps.Target = fake.NewClientBuilder().WithScheme(newTestScheme(t)).
		WithObjects(testDS("cilium", ciliumImage), ciliumCM("false"), helmRecord("kube-system")).Build()

	return env, deps
}

// A failing preflight refuses the adoption with nothing written.
func TestAdoptRefusesWhenCiliumPreflightFails(t *testing.T) {
	env, deps := ciliumFlowEnv(t, func(context.Context, cilium.PreflightInput) (*cilium.PreflightReport, error) {
		return &cilium.PreflightReport{VersionErr: errors.New("cilium 1.15 is too old")}, nil
	})

	err := runAdopt(context.Background(), env.cluster.Name, testNamespace, deps)
	if err == nil || !strings.Contains(err.Error(), "too old") {
		t.Fatalf("runAdopt = %v, want the preflight refusal", err)
	}

	if len(*env.writes) != 0 {
		t.Fatalf("wrote: %v", *env.writes)
	}
}

// With a passing preflight the holds are released only once CNIHandover is True.
func TestAdoptWaitsForCNIHandoverBeforeRelease(t *testing.T) {
	ok := func(context.Context, cilium.PreflightInput) (*cilium.PreflightReport, error) {
		return &cilium.PreflightReport{}, nil
	}

	env, deps := ciliumFlowEnv(t, ok)
	deps.ReleaseWait = 50 * time.Millisecond

	err := runAdopt(context.Background(), env.cluster.Name, testNamespace, deps)
	if err == nil || !strings.Contains(err.Error(), render.ConditionCNIHandover) {
		t.Fatalf("runAdopt = %v, want the hand-over timeout", err)
	}

	if !env.held(t, flowCP) || !env.held(t, flowWorker) {
		t.Fatal("a machine was released before CNIHandover")
	}

	var c ichorv1alpha1.TalosCluster
	if err := env.k8s.Get(context.Background(), client.ObjectKeyFromObject(env.cluster), &c); err != nil {
		t.Fatal(err)
	}

	c.Status.Conditions = []metav1.Condition{{Type: render.ConditionCNIHandover, Status: metav1.ConditionTrue, Reason: "Test", LastTransitionTime: metav1.Now()}}

	if err := env.k8s.Update(context.Background(), &c); err != nil {
		t.Fatal(err)
	}

	deps.ReleaseWait = time.Second

	if err := runAdopt(context.Background(), env.cluster.Name, testNamespace, deps); err != nil {
		t.Fatalf("runAdopt: %v\n%s", err, env.stderr.String())
	}

	if env.held(t, flowCP) || env.held(t, flowWorker) {
		t.Fatal("machines are still held after the hand-over")
	}
}
