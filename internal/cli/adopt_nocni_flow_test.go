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
	"context"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/internal/controller"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/cni/cilium"
	"github.com/maeshinshin/ichor/pkg/render"
)

// The Cilium-first order, end to end against fakes: Cilium, then Ichor,
// then the CRs, then the hand-over, then the release of the holds.

const (
	bootstrapEvent  = "bootstrap cilium"
	fakeReason      = "FakeController"
	keyDebugOn      = "debug"
	keyDebugEnabled = "enabled"
)

func debugValues() map[string]any {
	return map[string]any{keyDebugOn: map[string]any{keyDebugEnabled: true}}
}

// noCNIFlow is a no-CNI cluster Ichor has never seen. The machines' live configuration is the one
// the first render gives after the hand-over (no inline Cilium entry). A stand-in controller marks
// machines Ready and, only once the TalosCluster exists, sets CNIHandover.
type noCNIFlow struct {
	env  *flowEnv
	deps adoptDeps

	mu        sync.Mutex
	bootstrap []bootstrapCall

	// polled is set once adopt has read the TalosCluster after it exists: from then on the stand-in
	// may set CNIHandover, so the hand-over poll always finds it pending first (its "waiting for the
	// controller" line does not depend on how fast the goroutines run).
	polled atomic.Bool
}

type bootstrapCall struct {
	cluster *ichorv1alpha1.TalosCluster
	mode    cni.KubeProxyMode
}

func (f *noCNIFlow) calls() []bootstrapCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.bootstrap)
}

func newNoCNIFlow(t *testing.T) *noCNIFlow {
	t.Helper()

	env := newFlowEnv(t, cniCilium, func(c *ichorv1alpha1.TalosCluster) {
		apimeta.SetStatusCondition(&c.Status.Conditions, metav1.Condition{
			Type: render.ConditionCNIHandover, Status: metav1.ConditionTrue, Reason: fakeReason,
		})
	})

	ctx := context.Background()

	if err := env.k8s.Delete(ctx, env.cluster); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{flowCP, flowWorker} {
		m := &ichorv1alpha1.TalosMachine{}
		if err := env.k8s.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: name}, m); err != nil {
			t.Fatal(err)
		}

		if err := env.k8s.Delete(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	*env.writes = nil

	f := &noCNIFlow{env: env, deps: env.deps(t)}
	d := &f.deps

	node := func(name, addr, cidr string) *corev1.Node {
		n := cniNode(name, cidr, false, netNotReady)
		n.Status.Addresses = []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: addr}}

		return n
	}

	d.Target = fake.NewClientBuilder().WithScheme(newTestScheme(t)).
		WithObjects(node("cp", flowCPAddr, "10.244.0.0/24"), node("wk", flowWKAddr, nodeCIDR)).Build()

	members := []ClusterMember{
		{Hostname: "cp", Addresses: []string{flowCPAddr}, Role: roleControlPlane},
		{Hostname: "wk", Addresses: []string{flowWKAddr}, Role: testRoleWorker},
	}

	d.Seed = flowCPAddr
	d.ContractOverride = testVersionContract
	d.AssumeYes = true
	d.ReleaseWait = 2 * time.Second
	d.DialTalos = func(_ context.Context, endpoint string) (talosConfigReader, error) {
		return &membersReader{current: env.live[endpoint], version: testTalosVersion, members: members}, nil
	}
	d.ReadIdentity = func(_ context.Context, address string) (controller.HardwareIdentity, error) {
		return controller.HardwareIdentity{UUID: "uuid-" + address, Serial: "s"}, nil
	}
	d.Ichor = &adoptWriteIchor{Namespace: testNamespace, Manifest: []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: ichor-install-marker\n  namespace: default\n")}
	d.BootstrapCilium = func(_ context.Context, c *ichorv1alpha1.TalosCluster, mode cni.KubeProxyMode) error {
		f.mu.Lock()
		f.bootstrap = append(f.bootstrap, bootstrapCall{cluster: c.DeepCopy(), mode: mode})
		f.mu.Unlock()

		env.mu.Lock()
		*env.writes = append(*env.writes, bootstrapEvent)
		env.mu.Unlock()

		return nil
	}

	env.afterGet = func(o client.Object) {
		if _, ok := o.(*ichorv1alpha1.TalosCluster); ok {
			f.polled.Store(true)
		}
	}

	f.startController(t)

	return f
}

// startController stands in for the controllers: Ready machines, and CNIHandover=True on a
// TalosCluster that exists. It never creates a Job.
func (f *noCNIFlow) startController(t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)

		for ctx.Err() == nil {
			f.reconcileOnce(ctx)
			time.Sleep(2 * time.Millisecond)
		}
	}()

	t.Cleanup(func() { cancel(); <-done })
}

func (f *noCNIFlow) reconcileOnce(ctx context.Context) {
	var clusters ichorv1alpha1.TalosClusterList
	if f.env.k8s.List(ctx, &clusters) == nil {
		for i := range clusters.Items {
			c := &clusters.Items[i]
			if f.polled.Load() && !apimeta.IsStatusConditionTrue(c.Status.Conditions, render.ConditionCNIHandover) {
				apimeta.SetStatusCondition(&c.Status.Conditions, metav1.Condition{
					Type: render.ConditionCNIHandover, Status: metav1.ConditionTrue, Reason: fakeReason,
				})
				_ = f.env.k8s.Update(ctx, c)
			}
		}
	}

	var machines ichorv1alpha1.TalosMachineList
	if f.env.k8s.List(ctx, &machines) == nil {
		for i := range machines.Items {
			m := &machines.Items[i]
			if !apimeta.IsStatusConditionTrue(m.Status.Conditions, conditionReady) {
				apimeta.SetStatusCondition(&m.Status.Conditions, metav1.Condition{Type: conditionReady, Status: metav1.ConditionTrue, Reason: fakeReason})
				_ = f.env.k8s.Update(ctx, m)
			}
		}
	}
}

func (f *noCNIFlow) run(t *testing.T) error {
	t.Helper()

	return runAdopt(context.Background(), f.env.cluster.Name, testNamespace, f.deps)
}

// events is the recorded writes, without the stand-in controller's updates.
func (f *noCNIFlow) events() []string {
	f.env.mu.Lock()
	defer f.env.mu.Unlock()

	var out []string

	for _, w := range *f.env.writes {
		if !strings.HasPrefix(w, "update ") {
			out = append(out, w)
		}
	}

	return out
}

func indexOfPrefix(events []string, prefix string) int {
	return slices.IndexFunc(events, func(e string) bool { return strings.HasPrefix(e, prefix) })
}

func TestAdoptNoCNI_CiliumFirstThenIchorThenTheCRs(t *testing.T) {
	f := newNoCNIFlow(t)

	if err := f.run(t); err != nil {
		t.Fatalf("runAdopt: %v\n%s", err, f.env.stderr.String())
	}

	ev := f.events()

	if len(ev) == 0 || ev[0] != bootstrapEvent {
		t.Fatalf("Cilium must be installed before anything is written; events: %v", ev)
	}

	// Ichor's install (the marker), then the CRs, then the release of the holds.
	order := []string{"create ichor-install-marker", "create " + secretsRefName(f.env.cluster.Name), "create " + f.env.cluster.Name, "create uuid-", "patch uuid-"}
	last := 0

	for _, prefix := range order {
		i := indexOfPrefix(ev, prefix)
		if i < 0 {
			t.Fatalf("no %q among the events: %v", prefix, ev)
		}

		if i < last {
			t.Fatalf("%q came too early (events: %v)", prefix, ev)
		}

		last = i
	}

	for _, e := range ev {
		if strings.Contains(e, "ichor-cni") {
			t.Errorf("a Job was created for the hand-over: %q", e)
		}
	}

	for _, name := range []string{"uuid-" + flowCPAddr, "uuid-" + flowWKAddr} {
		if f.env.held(t, name) {
			t.Errorf("machine %s is still held", name)
		}
	}

	var got ichorv1alpha1.TalosCluster
	if err := f.env.k8s.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: f.env.cluster.Name}, &got); err != nil {
		t.Fatal(err)
	}

	if got.Spec.CNI.Preset != cniCilium || got.Spec.CNI.KubeProxy != kubeProxyEnabled {
		t.Errorf("written CNI = %+v, want the managed cilium preset with kube-proxy enabled", got.Spec.CNI)
	}
}

// The values the CLI installs are the values the controller derives from the cluster adopt wrote
// (the equality test), with a tunnel and operator values in play.
func TestAdoptNoCNI_InstalledValuesEqualTheControllersValues(t *testing.T) {
	f := newNoCNIFlow(t)
	f.deps.Tunnel = tunnelGeneve
	f.deps.CiliumValues = debugValues()

	if err := f.run(t); err != nil {
		t.Fatalf("runAdopt: %v\n%s", err, f.env.stderr.String())
	}

	calls := f.calls()
	if len(calls) != 1 {
		t.Fatalf("Cilium installed %d times, want once", len(calls))
	}

	var written ichorv1alpha1.TalosCluster
	if err := f.env.k8s.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: f.env.cluster.Name}, &written); err != nil {
		t.Fatal(err)
	}

	// What the CLI applies.
	installed, err := cilium.EffectiveValuesForMode(calls[0].cluster, calls[0].mode)
	if err != nil {
		t.Fatal(err)
	}

	// What the hand-over (and a Job, had one run) would apply: the written object, in the mode the
	// running Cilium reports, which is the mode that was installed.
	mode, _ := render.KubeProxyMode(&written)
	derived, err := cilium.EffectiveValuesForMode(&written, mode)
	if err != nil {
		t.Fatal(err)
	}

	if mode != calls[0].mode || !reflect.DeepEqual(installed, derived) {
		t.Fatalf("installed values differ from the controller's:\ninstalled %v\nderived   %v", installed, derived)
	}

	h1, err1 := controller.CNIValuesHash(calls[0].cluster, calls[0].mode, 0)
	h2, err2 := controller.CNIValuesHash(&written, mode, 0)

	if err1 != nil || err2 != nil || h1 != h2 {
		t.Fatalf("values hashes differ: %q (%v) vs %q (%v)", h1, err1, h2, err2)
	}

	if got, _ := installed["tunnelProtocol"].(string); got != tunnelGeneve {
		t.Errorf("tunnelProtocol = %v, want geneve", installed["tunnelProtocol"])
	}

	if debug, _ := installed[keyDebugOn].(map[string]any); debug[keyDebugEnabled] != true {
		t.Errorf("the --cilium-values debug.enabled did not reach the install: %v", installed[keyDebugOn])
	}
}

// A run that stops after Cilium went in is finished by running adopt again: Cilium is applied
// again (an upgrade to the same values), nothing already written is written twice, and a third
// run on the finished cluster does nothing.
func TestAdoptNoCNI_RerunAfterAFailureFinishes(t *testing.T) {
	f := newNoCNIFlow(t)
	good := f.deps.Ichor
	f.deps.Ichor = &adoptWriteIchor{Namespace: testNamespace, Manifest: []byte("kind: [not a manifest")}

	if err := f.run(t); err == nil {
		t.Fatal("first run succeeded with an unusable install manifest")
	}

	if len(f.calls()) != 1 {
		t.Fatalf("Cilium installed %d times in the first run, want once", len(f.calls()))
	}

	f.deps.Ichor = good

	if err := f.run(t); err != nil {
		t.Fatalf("second run: %v\n%s", err, f.env.stderr.String())
	}

	if len(f.calls()) != 2 || !reflect.DeepEqual(f.calls()[0].cluster.Spec.CNI, f.calls()[1].cluster.Spec.CNI) {
		t.Fatalf("second run applied Cilium %d times in total, or other values", len(f.calls()))
	}

	seen := map[string]bool{}

	for _, e := range f.events() {
		if strings.HasPrefix(e, "create ") {
			if seen[e] {
				t.Errorf("%q happened twice", e)
			}

			seen[e] = true
		}
	}

	for _, name := range []string{"uuid-" + flowCPAddr, "uuid-" + flowWKAddr} {
		if f.env.held(t, name) {
			t.Errorf("machine %s is still held after the rerun", name)
		}
	}

	before := len(f.events())

	if err := f.run(t); err != nil {
		t.Fatalf("third run: %v", err)
	}

	if len(f.events()) != before || len(f.calls()) != 2 {
		t.Errorf("a run on the finished cluster wrote again: %v", f.events()[before:])
	}
}

func TestAdoptNoCNI_ConsentNamesTheCiliumInstall(t *testing.T) {
	f := newNoCNIFlow(t)
	f.deps.AssumeYes = false
	f.deps.Stdin = strings.NewReader("n\n")

	err := f.run(t)
	if err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("runAdopt = %v, want the abort", err)
	}

	if !strings.Contains(f.env.stderr.String(), "Install Cilium "+cilium.ChartVersion) {
		t.Errorf("the consent prompt does not name the Cilium install:\n%s", f.env.stderr.String())
	}

	if len(f.calls()) != 0 || len(f.events()) != 0 {
		t.Errorf("something was done without consent: %v", f.events())
	}
}

func TestAdoptNoCNI_CiliumValuesRefusesAnOwnedKey(t *testing.T) {
	f := newNoCNIFlow(t)
	f.deps.CiliumValues = map[string]any{"kubeProxyReplacement": true}

	err := f.run(t)
	if err == nil || !strings.Contains(err.Error(), "kubeProxyReplacement") || !strings.Contains(err.Error(), "--cilium-values") {
		t.Fatalf("runAdopt = %v, want the owned-key refusal", err)
	}

	if len(f.events()) != 0 {
		t.Fatalf("wrote despite the refusal: %v", f.events())
	}
}

func TestReadAdoptCiliumValues(t *testing.T) {
	if v, err := readCiliumValuesFile(""); v != nil || err != nil {
		t.Fatalf("no file = %v, %v", v, err)
	}

	path := t.TempDir() + "/values.yaml"
	if err := os.WriteFile(path, []byte("debug:\n  enabled: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	v, err := readCiliumValuesFile(path)
	if err != nil || !reflect.DeepEqual(v, debugValues()) {
		t.Fatalf("values = %v, %v", v, err)
	}

	if _, err := readCiliumValuesFile(path + ".missing"); err == nil {
		t.Fatal("a missing file must fail")
	}
}

// Ichor already present (installed with Helm, its pods Pending until a CNI exists): Cilium still
// goes in first, adopt's install step is skipped, and the CRs wait for the webhooks.
func TestAdoptNoCNI_IchorAlreadyPresentStillInstallsCiliumFirst(t *testing.T) {
	f := newNoCNIFlow(t)

	inst := &ichorv1alpha1.IchorInstallation{Name: ichorv1alpha1.IchorInstallationName}
	if err := f.env.k8s.Create(context.Background(), inst); err != nil {
		t.Fatal(err)
	}

	// The controller's Deployment says which namespace it reads the bundle from.
	deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Name: "ichor-controller-manager", Namespace: testNamespace, Labels: map[string]string{"control-plane": "controller-manager"},
	}}
	if err := f.env.k8s.Create(context.Background(), deploy); err != nil {
		t.Fatal(err)
	}

	f.env.mu.Lock()
	*f.env.writes = nil
	f.env.mu.Unlock()

	if err := f.run(t); err != nil {
		t.Fatalf("runAdopt: %v\n%s", err, f.env.stderr.String())
	}

	ev := f.events()
	if len(ev) == 0 || ev[0] != bootstrapEvent {
		t.Fatalf("Cilium must come first; events: %v", ev)
	}

	if indexOfPrefix(ev, "create ichor-install-marker") >= 0 {
		t.Errorf("Ichor was installed although it is present: %v", ev)
	}

	if indexOfPrefix(ev, "create "+f.env.cluster.Name) < 0 {
		t.Errorf("the TalosCluster was not created: %v", ev)
	}
}

// A first run that dies after the first TalosMachine leaves the cluster with one machine. The rerun
// discovers the members again, writes the machine that is missing, and releases both holds.
func TestAdoptRerunWritesTheMachineAFailedRunNeverCreated(t *testing.T) {
	f := newNoCNIFlow(t)
	missing := "uuid-" + flowWKAddr
	fail := true

	f.deps.K8s = interceptor.NewClient(f.env.k8s.(client.WithWatch), interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, o ...client.CreateOption) error {
			if _, ok := obj.(*ichorv1alpha1.TalosMachine); ok && obj.GetName() == missing && fail {
				return errors.New("injected: the API server went away")
			}

			return c.Create(ctx, obj, o...)
		},
	})

	if err := f.run(t); err == nil {
		t.Fatal("first run succeeded although the second TalosMachine could not be created")
	}

	fail = false

	if err := f.run(t); err != nil {
		t.Fatalf("rerun: %v\n%s", err, f.env.stderr.String())
	}

	for _, name := range []string{"uuid-" + flowCPAddr, missing} {
		if f.env.held(t, name) {
			t.Errorf("machine %s is still held after the rerun", name)
		}
	}
}

// The controller reads the bundle Secret from its own namespace: a --namespace that differs is
// refused before the bundle is saved or anything is created, and says both namespaces.
func TestAdoptRefusesANamespaceThatIsNotTheControllersBeforeWritingAnything(t *testing.T) {
	f := newNoCNIFlow(t)
	saved := false
	f.deps.SaveBundle = func() error { saved = true; return nil }

	err := runAdopt(context.Background(), f.env.cluster.Name, "elsewhere", f.deps)
	if err == nil {
		t.Fatal("adopt accepted a --namespace the controller does not run in")
	}

	for _, want := range []string{`"elsewhere"`, `"` + testNamespace + `"`, "nothing was written"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not contain %s", err, want)
		}
	}

	if ev := f.events(); len(ev) != 0 || saved || len(f.calls()) != 0 {
		t.Errorf("something was written before the refusal: events %v, bundle saved %v, Cilium installs %d", ev, saved, len(f.calls()))
	}

	var secrets corev1.SecretList
	if err := f.env.k8s.List(context.Background(), &secrets, client.InNamespace("elsewhere")); err != nil || len(secrets.Items) != 0 {
		t.Errorf("Secrets in the wrong namespace: %d (%v)", len(secrets.Items), err)
	}
}

// The expected namespace is the installed controller's, wherever Helm put it, and ichor-system when
// Ichor is not there and adopt installs it.
func TestCheckControllerNamespace(t *testing.T) {
	ctx := context.Background()
	empty := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	if err := checkControllerNamespace(ctx, empty, &adoptWriteIchor{}, ichorControllerNamespace); err != nil {
		t.Errorf("no Ichor, default namespace: %v", err)
	}

	if err := checkControllerNamespace(ctx, empty, &adoptWriteIchor{}, "other"); err == nil {
		t.Error("no Ichor, --namespace other: accepted")
	}

	controller := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Name: "ichor-controller-manager", Namespace: "tenant", Labels: map[string]string{"control-plane": "controller-manager"},
	}}
	helm := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(controller).Build()

	if err := checkControllerNamespace(ctx, helm, &adoptWriteIchor{}, "tenant"); err != nil {
		t.Errorf("controller in tenant, --namespace tenant: %v", err)
	}

	if err := checkControllerNamespace(ctx, helm, &adoptWriteIchor{}, ichorControllerNamespace); err == nil {
		t.Error("controller in tenant, default --namespace: accepted")
	}
}

// A machine an operator rejected is a TalosMachine like any other: the rerun's discovery keys on
// its identity, finds it, and leaves it rejected.
func TestAdoptRerunLeavesARejectedMachineRejected(t *testing.T) {
	f := newNoCNIFlow(t)
	rejected := "uuid-" + flowWKAddr
	fail := true

	f.deps.K8s = interceptor.NewClient(f.env.k8s.(client.WithWatch), interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, o ...client.CreateOption) error {
			if _, ok := obj.(*ichorv1alpha1.TalosMachine); ok && obj.GetName() == rejected && fail {
				return errors.New("injected: the API server went away")
			}

			return c.Create(ctx, obj, o...)
		},
	})

	if err := f.run(t); err == nil {
		t.Fatal("first run succeeded although the second TalosMachine could not be created")
	}

	fail = false

	m := newTestMachine(f.env.cluster.Name)
	m.Name, m.Spec.Role, m.Spec.Endpoint = rejected, testRoleWorker, flowWKAddr
	m.Spec.Identity.UUID, m.Spec.ApprovalState = rejected, ichorv1alpha1.MachineApprovalStateRejected

	if err := f.env.k8s.Create(context.Background(), m); err != nil {
		t.Fatal(err)
	}

	if err := f.run(t); err != nil {
		t.Fatalf("rerun: %v\n%s", err, f.env.stderr.String())
	}

	var got ichorv1alpha1.TalosMachine
	if err := f.env.k8s.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: rejected}, &got); err != nil {
		t.Fatal(err)
	}

	if got.Spec.ApprovalState != ichorv1alpha1.MachineApprovalStateRejected {
		t.Errorf("approval state = %q after the rerun, want rejected", got.Spec.ApprovalState)
	}
}

// When the controller's namespace cannot be told (the Deployments cannot be listed, or only the
// IchorInstallation is visible), the default --namespace goes ahead and any other is refused as
// unconfirmed, not as a mismatch.
func TestCheckControllerNamespaceWhenItCannotBeTold(t *testing.T) {
	ctx := context.Background()
	forbidden := interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, o ...client.ListOption) error {
			if _, ok := list.(*appsv1.DeploymentList); ok {
				return apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, "", errors.New("no list"))
			}

			return c.List(ctx, list, o...)
		},
	}
	build := func(objs ...client.Object) client.Reader {
		return fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(objs...).WithInterceptorFuncs(forbidden).Build()
	}
	installation := &ichorv1alpha1.IchorInstallation{Name: ichorv1alpha1.IchorInstallationName}

	for name, c := range map[string]client.Reader{
		"Deployments not listable":        build(),
		"only the IchorInstallation seen": fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(installation).Build(),
	} {
		if err := checkControllerNamespace(ctx, c, &adoptWriteIchor{}, ichorControllerNamespace); err != nil {
			t.Errorf("%s, default --namespace: %v", name, err)
		}

		err := checkControllerNamespace(ctx, c, &adoptWriteIchor{}, "tenant")
		if err == nil || !strings.Contains(err.Error(), "could not confirm") || strings.Contains(err.Error(), "the namespace Ichor's controller runs in;") {
			t.Errorf("%s, --namespace tenant: %v, want a refusal that says it could not confirm", name, err)
		}
	}
}
