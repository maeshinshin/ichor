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
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	yaml "go.yaml.in/yaml/v4"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	sigsyaml "sigs.k8s.io/yaml"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1/ichorv1alpha1connect"
	"github.com/maeshinshin/ichor/internal/secretsbundle"
	"github.com/maeshinshin/ichor/internal/uiapi"
)

const (
	testUIApp       = "ichor-ui"
	testUIActor     = "ichor-ui"
	testMachineName = "node-a"
	testPatchName   = "cli-patch"
)

// setAPITestFlags points the package-level flag state at the test
// namespace and restores it afterwards.
func setAPITestFlags(t *testing.T, yes bool) {
	t.Helper()

	oldOpts, oldYes, oldFmt := apiOpts, assumeYes, outputFormat
	apiOpts = apiOptions{Namespace: testNamespace, IchorNamespace: ichorControllerNamespace}
	assumeYes, outputFormat = yes, outputJSON

	t.Cleanup(func() { apiOpts, assumeYes, outputFormat = oldOpts, oldYes, oldFmt })
}

func weakMachine(name string) *ichorv1alpha1.TalosMachine {
	m := newTestMachine("c")
	m.Name = name
	m.Spec.Role, m.Spec.ApprovalState = "", ichorv1alpha1.MachineApprovalStatePending
	m.Spec.Identity = ichorv1alpha1.TalosMachineIdentity{UUID: "only-a-uuid"}

	return m
}

func strongMachine() *ichorv1alpha1.TalosMachine {
	m := weakMachine("strong")
	m.Spec.Identity = ichorv1alpha1.TalosMachineIdentity{UUID: "u", Serial: "s", MACAddresses: []string{"aa:bb:cc:dd:ee:ff"}}

	return m
}

// uiBackendObjects is what dialBackend finds: the Service and one ready pod.
func uiBackendObjects() []client.Object {
	sel := map[string]string{"app": testUIApp}

	return []client.Object{
		&corev1.Service{
			Name: testUIServiceName, Namespace: ichorControllerNamespace, Labels: map[string]string{uiComponentLabel: uiComponentValue},
			Spec: corev1.ServiceSpec{Selector: sel, Ports: []corev1.ServicePort{{Port: 8080}}},
		},
		&corev1.Pod{
			Name: "ichor-ui-0", Namespace: ichorControllerNamespace, Labels: sel,
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			},
		},
	}
}

// backendFor serves the real uiapi handlers (plus extra, if given) over
// httptest and returns apiDeps whose fake tunnel "forwards" to it.
func backendFor(t *testing.T, k8s client.Client, forwards *atomic.Int32, mount func(*http.ServeMux)) apiDeps {
	t.Helper()

	server := &uiapi.Server{Client: k8s, ControllerNamespace: ichorControllerNamespace}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(http.ResponseWriter, *http.Request) {})
	mux.Handle(ichorv1alpha1connect.NewClusterServiceHandler(server))
	mux.Handle(ichorv1alpha1connect.NewMachineServiceHandler(server))
	mux.Handle(ichorv1alpha1connect.NewPatchServiceHandler(server))
	mux.Handle(ichorv1alpha1connect.NewOperationServiceHandler(server))
	mux.Handle(ichorv1alpha1connect.NewCniServiceHandler(server))

	if mount != nil {
		mount(mux)
	} else {
		mux.Handle(ichorv1alpha1connect.NewNodeServiceHandler(server))
	}

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	port := int32(srv.Listener.Addr().(*net.TCPAddr).Port) //nolint:errcheck,forcetypeassert // httptest listens on tcp

	return apiDeps{
		K8s: k8s,
		Forward: func(ctx context.Context, _ *corev1.Pod, _, _ int32, ready chan struct{}) error {
			forwards.Add(1)
			close(ready)
			<-ctx.Done()

			return nil
		},
		FreePort: func() (int32, error) { return port, nil },
		Stderr:   &bytes.Buffer{},
	}
}

func getMachine(t *testing.T, k8s client.Client, name string) ichorv1alpha1.TalosMachine {
	t.Helper()

	var m ichorv1alpha1.TalosMachine
	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: name}, &m); err != nil {
		t.Fatal(err)
	}

	return m
}

var approveWorker = machineApproveOptions{Role: testRoleWorker, Reason: "test"}

func TestApproveThroughBackendRunsTheBackendsChecks(t *testing.T) {
	setAPITestFlags(t, true)

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).
		WithObjects(append(uiBackendObjects(), weakMachine("weak"), strongMachine())...).Build()

	var forwards atomic.Int32

	deps := backendFor(t, k8s, &forwards, nil)

	c, err := connectAPI(context.Background(), apiOpts, deps, false)
	if err != nil {
		t.Fatal(err)
	}

	defer c.Close()

	if c.Local {
		t.Fatal("expected the backend path, got the local fallback")
	}

	err = runMachineApprove(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, "weak", approveWorker)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("weak identity: want FailedPrecondition from the backend, got %v", err)
	}

	if got := getMachine(t, k8s, "weak"); got.Spec.IsApproved() {
		t.Fatal("a refused approval must not have written the machine")
	}

	var out bytes.Buffer
	if err := runMachineApprove(context.Background(), c, strings.NewReader(""), &out, &bytes.Buffer{}, "strong", approveWorker); err != nil {
		t.Fatal(err)
	}

	got := getMachine(t, k8s, "strong")
	if !got.Spec.IsApproved() || got.Spec.ApprovalDecision.Actor != testUIActor {
		t.Fatalf("want approved by ichor-ui, got %+v", got.Spec)
	}

	if forwards.Load() != 1 {
		t.Fatalf("want exactly one tunnel, got %d", forwards.Load())
	}
}

// The fallback must not be a way around the approval checks: it runs the
// same uiapi.Server code, so a weak identity is refused there too.
func TestApproveFallbackKeepsTheChecks(t *testing.T) {
	setAPITestFlags(t, true)

	// No Service: the backend is unreachable.
	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(weakMachine("weak"), strongMachine()).Build()

	var stderr bytes.Buffer

	deps := apiDeps{K8s: k8s, Stderr: &stderr}

	c, err := connectAPI(context.Background(), apiOpts, deps, false)
	if err != nil {
		t.Fatal(err)
	}

	if !c.Local || !strings.Contains(stderr.String(), "not reachable") {
		t.Fatalf("want a visible fallback, local=%v stderr=%q", c.Local, stderr.String())
	}

	err = runMachineApprove(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, "weak", approveWorker)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("weak identity through the fallback: want FailedPrecondition, got %v", err)
	}

	if getMachine(t, k8s, "weak").Spec.IsApproved() {
		t.Fatal("the fallback approved a weak identity")
	}

	err = runMachineApprove(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, "strong",
		machineApproveOptions{Role: "nonsense"})
	if err == nil {
		t.Fatal("an unknown role must be refused")
	}

	if err := runMachineApprove(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, "strong", approveWorker); err != nil {
		t.Fatal(err)
	}

	got := getMachine(t, k8s, "strong")
	if !got.Spec.IsApproved() || got.Spec.ApprovalDecision.Actor != actorCLI {
		t.Fatalf("want approved by %s, got %+v", actorCLI, got.Spec)
	}

	// Approving twice is refused by the same code path.
	err = runMachineApprove(context.Background(), c, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, "strong", approveWorker)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("second approval: want FailedPrecondition, got %v", err)
	}
}

func TestApproveAsksUnlessYes(t *testing.T) {
	setAPITestFlags(t, false)

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(strongMachine()).Build()
	c := localClients(apiOpts, apiDeps{K8s: k8s})

	err := runMachineApprove(context.Background(), c, strings.NewReader("n\n"), &bytes.Buffer{}, &bytes.Buffer{}, "strong", approveWorker)
	if err == nil || getMachine(t, k8s, "strong").Spec.IsApproved() {
		t.Fatalf("declined confirmation must not approve (err=%v)", err)
	}

	if err := runMachineApprove(context.Background(), c, strings.NewReader("y\n"), &bytes.Buffer{}, &bytes.Buffer{}, "strong", approveWorker); err != nil {
		t.Fatal(err)
	}
}

func TestCommandsThatNeedTheBackendRefuseToFallBack(t *testing.T) {
	setAPITestFlags(t, true)

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).Build()

	_, err := connectAPI(context.Background(), apiOptions{Local: true}, apiDeps{K8s: k8s, Stderr: &bytes.Buffer{}}, true)
	if err == nil {
		t.Fatal("--local with a backend-only command must fail")
	}

	_, err = connectAPI(context.Background(), apiOpts, apiDeps{K8s: k8s, Stderr: &bytes.Buffer{}}, true)
	if err == nil || !strings.Contains(err.Error(), "read-only Talos identity") {
		t.Fatalf("unreachable backend with a backend-only command: want errBackendRequired, got %v", err)
	}

	c := localClients(apiOpts, apiDeps{K8s: k8s})
	if _, err := c.Node.GetNode(context.Background(), connect.NewRequest(&pb.GetNodeRequest{})); err == nil {
		t.Fatal("the local fallback must not serve GetNode")
	}
}

// TestPatchCreatePrintsItsImpact: the help promises create shows the impact,
// even when nothing needs confirming.
func TestPatchCreatePrintsItsImpact(t *testing.T) {
	setAPITestFlags(t, true)

	cluster := newTestCluster()
	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster).Build()
	c := localClients(apiOpts, apiDeps{K8s: k8s})

	p := sysctlPatch(cluster.Name, map[string]string{testSysctlKey: "1"})
	p.Name = testPatchName
	p.Spec.Layer = cniPatchLayer

	var out, errw bytes.Buffer
	if err := runPatchWrite(context.Background(), c, strings.NewReader(""), &out, &errw, &p, false); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(errw.String(), "Impact of patch "+testNamespace+"/"+testPatchName+":") {
		t.Fatalf("no impact summary on stderr: %q", errw.String())
	}
}

func TestPatchWriteAndPreviewLocally(t *testing.T) {
	setAPITestFlags(t, true)

	cluster := newTestCluster()
	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster).Build()
	c := localClients(apiOpts, apiDeps{K8s: k8s})

	p := sysctlPatch(cluster.Name, map[string]string{testSysctlKey: "1"})
	p.Name = testPatchName
	p.Spec.Layer = "cluster"

	raw, err := sigsyaml.Marshal(&p)
	if err != nil {
		t.Fatal(err)
	}

	got, err := readPatchFile(bytes.NewReader(raw), "-", testNamespace)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runPatchPreview(context.Background(), c, &out, got); err != nil {
		t.Fatal(err)
	}

	if err := runPatchWrite(context.Background(), c, strings.NewReader(""), &out, &bytes.Buffer{}, got, false); err != nil {
		t.Fatal(err)
	}

	var stored ichorv1alpha1.TalosConfigPatch
	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: testPatchName}, &stored); err != nil {
		t.Fatal(err)
	}

	got.Spec.Priority = 5
	if err := runPatchWrite(context.Background(), c, strings.NewReader(""), &out, &bytes.Buffer{}, got, true); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := runPatchList(context.Background(), c, &out, cluster.Name); err != nil || !strings.Contains(out.String(), testPatchName) {
		t.Fatalf("list: err=%v out=%s", err, out.String())
	}

	if err := runPatchDelete(context.Background(), c, strings.NewReader(""), &out, &bytes.Buffer{}, testPatchName); err != nil {
		t.Fatal(err)
	}

	if err := k8s.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: testPatchName}, &stored); err == nil {
		t.Fatal("patch still exists after delete")
	}
}

func TestReadPatchFileIsStrict(t *testing.T) {
	_, err := readPatchFile(strings.NewReader(`
apiVersion: ichor.maesh.dev/v1alpha1
kind: TalosConfigPatch
metadata: {name: p}
spec: {clusterRef: {name: c}, layer: cluster, bogusField: 1, documents: []}
`), "-", testNamespace)
	if err == nil {
		t.Fatal("an unknown field must be rejected, not dropped")
	}

	if _, err := readPatchFile(strings.NewReader("kind: TalosMachine\nmetadata: {name: p}\n"), "-", testNamespace); err == nil {
		t.Fatal("a different kind must be rejected")
	}
}

func TestEffectiveConfigFallbackMasksSecrets(t *testing.T) {
	setAPITestFlags(t, true)

	bundle := newTestBundle(t)

	raw, err := yaml.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}

	cluster := newTestCluster()
	cluster.Spec.SecretsRef = corev1.LocalObjectReference{Name: "bundle"}

	secret := &corev1.Secret{
		Name: "bundle", Namespace: ichorControllerNamespace,
		Data: map[string][]byte{secretsbundle.SecretKey: raw},
	}

	// The controller answers the backend's request for the cluster's public facts (ADR 0108); the fake
	// client has none, so the answer is put on the request as it is created.
	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(cluster, newTestMachine(cluster.Name), secret).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if req, ok := obj.(*ichorv1alpha1.TalosCredentialRequest); ok {
					req.Status = ichorv1alpha1.TalosCredentialRequestStatus{
						Phase: ichorv1alpha1.CredentialPhaseIssued,
						Public: &ichorv1alpha1.TalosCredentialPublic{
							ClusterID: bundle.Cluster.ID, TalosCA: string(bundle.Certs.OS.Crt), KubernetesCA: string(bundle.Certs.K8s.Crt),
							EtcdCA: string(bundle.Certs.Etcd.Crt), KubernetesAggregatorCA: string(bundle.Certs.K8sAggregator.Crt),
						},
					}
				}

				return cl.Create(ctx, obj, opts...)
			},
		}).Build()
	c := localClients(apiOpts, apiDeps{K8s: k8s})

	var out bytes.Buffer
	if err := runConfigEffective(context.Background(), c, &out, testMachineName); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "***") {
		t.Fatalf("expected masked secrets in the output")
	}

	// Cluster.ID and certificates are public by design (uiapi's own list).
	for _, s := range []string{bundle.Secrets.BootstrapToken, bundle.TrustdInfo.Token, bundle.Cluster.Secret, string(bundle.Certs.OS.Key), string(bundle.Certs.K8s.Key)} {
		if s != "" && strings.Contains(out.String(), s) {
			t.Fatalf("secret material %.12q leaked into the CLI output", s)
		}
	}
}

// logsHandler is a NodeService whose first stream ends with Unavailable
// after two lines, as a draining ichor-ui does; the second must be asked to
// resume from the first's last token.
type logsHandler struct {
	ichorv1alpha1connect.UnimplementedNodeServiceHandler

	calls  atomic.Int32
	tokens chan string
}

func (h *logsHandler) StreamNodeLogs(
	ctx context.Context, req *connect.Request[pb.StreamNodeLogsRequest], stream *connect.ServerStream[pb.StreamNodeLogsResponse],
) error {
	h.tokens <- req.Msg.GetResumeToken()

	ref := req.Msg.GetMachines()[0]

	if h.calls.Add(1) == 1 {
		_ = stream.Send(&pb.StreamNodeLogsResponse{Machine: ref, Message: "one", ResumeToken: "t1"})
		_ = stream.Send(&pb.StreamNodeLogsResponse{Machine: ref, Message: "two", ResumeToken: "t2"})

		return connect.NewError(connect.CodeUnavailable, context.Canceled)
	}

	_ = stream.Send(&pb.StreamNodeLogsResponse{Machine: ref, Message: "three", ResumeToken: "t3"})
	<-ctx.Done()

	return nil
}

func TestNodeLogsFollowResumesAfterDrain(t *testing.T) {
	setAPITestFlags(t, true)

	outputFormat = outputYAML // plain text for logs

	old := logsReconnectDelay
	logsReconnectDelay = time.Millisecond

	t.Cleanup(func() { logsReconnectDelay = old })

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(uiBackendObjects()...).Build()
	h := &logsHandler{tokens: make(chan string, 8)}

	var forwards atomic.Int32

	deps := backendFor(t, k8s, &forwards, func(mux *http.ServeMux) { mux.Handle(ichorv1alpha1connect.NewNodeServiceHandler(h)) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var out syncBuffer

	done := make(chan error, 1)

	go func() {
		done <- runNodeLogs(ctx, func(ctx context.Context) (*apiClients, error) { return connectAPI(ctx, apiOpts, deps, true) },
			&out, &bytes.Buffer{}, []string{testMachineName}, nodeLogsOptions{Follow: true})
	}()

	deadline := time.After(5 * time.Second)

	for !strings.Contains(out.String(), "three") {
		select {
		case <-deadline:
			t.Fatalf("never saw the resumed line; output so far: %q", out.String())
		case <-time.After(10 * time.Millisecond):
		}
	}

	cancel()

	if err := <-done; err != nil {
		t.Fatal(err)
	}

	if first, second := <-h.tokens, <-h.tokens; first != "" || second != "t2" {
		t.Fatalf("want resume tokens \"\" then \"t2\", got %q then %q", first, second)
	}

	if got := out.String(); got != "one\ntwo\nthree\n" {
		t.Fatalf("unexpected output %q", got)
	}
}

func TestNodeLogsWithoutFollowStopsWhenIdle(t *testing.T) {
	setAPITestFlags(t, true)

	outputFormat = outputYAML

	k8s := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(uiBackendObjects()...).Build()
	h := &logsHandler{tokens: make(chan string, 8)}
	h.calls.Store(1) // skip the Unavailable branch: send one line, then block

	var forwards atomic.Int32

	deps := backendFor(t, k8s, &forwards, func(mux *http.ServeMux) { mux.Handle(ichorv1alpha1connect.NewNodeServiceHandler(h)) })

	var out syncBuffer

	err := runNodeLogs(context.Background(), func(ctx context.Context) (*apiClients, error) { return connectAPI(ctx, apiOpts, deps, true) },
		&out, &bytes.Buffer{}, []string{testMachineName}, nodeLogsOptions{Idle: 200 * time.Millisecond})
	if err != nil || out.String() != "three\n" {
		t.Fatalf("err=%v out=%q", err, out.String())
	}
}

// syncBuffer is a bytes.Buffer safe to read while the command still writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}
