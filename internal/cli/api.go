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
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	authv1client "k8s.io/client-go/kubernetes/typed/authentication/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	sigsyaml "sigs.k8s.io/yaml"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1/ichorv1alpha1connect"
	"github.com/maeshinshin/ichor/internal/uiapi"
)

// Command Use strings several subcommand groups share.
const (
	useList    = "list"
	useGetName = "get NAME"
)

// actorCLI is the audit actor the in-process fallback records, so an
// AuditRecord says which path wrote the CR.
const actorCLI = "ichor-cli"

// apiConnectTimeout bounds how long reaching ichor-ui may take (finding a
// ready pod, bringing the tunnel up, one probe request) before the CLI
// concludes the backend is unreachable. A var so tests can shrink it.
var apiConnectTimeout = 5 * time.Second

// apiOpts is the flag state every API-backed command group shares.
var apiOpts apiOptions

type apiOptions struct {
	// Namespace is where the CRs a command names live.
	Namespace string
	// IchorNamespace is where Ichor is installed: ichor-ui's Service and
	// the secrets bundle Secret (uiapi.Server.ControllerNamespace).
	IchorNamespace string
	// ArgoCDNamespace is where Argo CD runs; the fallback reads its argocd-cm
	// to refuse writes to CRs Argo CD tracks.
	ArgoCDNamespace string
	// Local skips ichor-ui and computes from the Kubernetes API.
	Local bool
	// BreakGlass is the reason of an emergency direct write;
	// empty means off. Setting it implies Local: break-glass is decided by the
	// caller's own RBAC, never by the backend's ServiceAccount.
	BreakGlass string
}

// breakGlassOf is the request field the --break-glass flag fills; nil when off.
func breakGlassOf(opts apiOptions) *pb.BreakGlass {
	if opts.BreakGlass == "" {
		return nil
	}

	return &pb.BreakGlass{Reason: opts.BreakGlass}
}

// addAPIFlags registers the shared flags on a command group.
func addAPIFlags(cmd *cobra.Command) {
	f := cmd.PersistentFlags()
	f.StringVarP(&apiOpts.Namespace, "namespace", "n", ichorControllerNamespace, "namespace of the Ichor resources the command names")
	f.StringVar(&apiOpts.IchorNamespace, "ichor-namespace", ichorControllerNamespace,
		"namespace Ichor is installed in (where ichor-ui runs and the secrets bundle lives)")
	f.StringVar(&apiOpts.ArgoCDNamespace, "argocd-namespace", "argocd",
		"namespace Argo CD runs in, used by the --local fallback to read argocd-cm when checking whether a resource is tracked")
	f.BoolVar(&apiOpts.Local, "local", false, "do not use ichor-ui; compute from the Kubernetes API directly")
	f.StringVar(&apiOpts.BreakGlass, "break-glass", "",
		"incident only: write a GitOps-tracked resource now and open the matching pull request; the value is the reason, "+
			"recorded in the audit record, an Event and the pull request. Needs the break-glass RBAC verb on the resource; implies --local")
}

// apiDeps is connectAPI's dependencies. Forward and FreePort are the seams
// a test replaces; the real ones need a cluster.
type apiDeps struct {
	K8s client.Client

	// Forward is uiDeps.Forward: ichor ui's own tunnel.
	Forward func(ctx context.Context, pod *corev1.Pod, localPort, remotePort int32, ready chan struct{}) error

	// FreePort picks the local port the tunnel listens on.
	FreePort func() (int32, error)

	Stderr io.Writer

	// WhoAmI is the caller's own name from a SelfSubjectReview on the
	// kubeconfig; nil or failing means the audit actor stays actorCLI.
	WhoAmI func(ctx context.Context) (string, error)

	// actor is set by connectAPI for the in-process clients.
	actor string
}

// apiClients is the Connect surface every API-backed command uses, whether
// it is backed by ichor-ui over the tunnel or by a uiapi.Server running in
// this process on the user's own Kubernetes credentials.
type apiClients struct {
	Cluster   ichorv1alpha1connect.ClusterServiceClient
	Machine   ichorv1alpha1connect.MachineServiceClient
	Node      ichorv1alpha1connect.NodeServiceClient
	Patch     ichorv1alpha1connect.PatchServiceClient
	Operation ichorv1alpha1connect.OperationServiceClient

	// Local is true when the clients are the in-process fallback.
	Local bool

	// Close tears the tunnel down; never nil.
	Close func()

	// Reconnect, when set, opens a fresh tunnel after the old one died (the
	// kube-apiserver the port-forward runs through restarted); nil for the
	// in-process fallback, which has no tunnel.
	Reconnect func(context.Context) (*apiClients, error)
}

// errBackendRequired is what a command that must go through ichor-ui
// (the read-only Talos identity lives there; the CLI never touches a node)
// returns when ichor-ui is unreachable or --local was given.
var errBackendRequired = errors.New("this command needs ichor-ui, because only the backend holds the read-only " +
	"Talos identity (the CLI never talks to a node directly); run it without --local while ichor-ui is reachable")

// errOIDCBackend is returned by dialBackend when /auth/config says OIDC is
// enabled, and is what a backend-only command returns to the user while
// ichor-ui requires OIDC: the CLI does not forward its kubeconfig credentials
// and has no ID token to send (point 5).
var errOIDCBackend = errors.New("ichor-ui requires OIDC login and the CLI cannot present an OIDC token yet " +
	"(it never forwards kubeconfig credentials), so this command, which only the backend can serve, is unavailable")

// connectAPI returns clients for one command invocation. It tries ichor-ui
// first; only when ichor-ui cannot be reached at all (no Service, no ready
// pod, the tunnel fails, nothing answers) does it fall back to a
// uiapi.Server in this process. The decision is made once, up front, and
// is never revisited after a call has been sent: an error the backend
// returns -- a refused approval, a rejected patch -- is the answer, not a
// reason to retry the same write through another path.
//
// requireBackend turns the fallback into errBackendRequired, for the
// commands that need the backend's Talos identity.
func connectAPI(ctx context.Context, opts apiOptions, deps apiDeps, requireBackend bool) (*apiClients, error) {
	if opts.BreakGlass != "" {
		if requireBackend {
			return nil, fmt.Errorf("--break-glass applies to writes, which never need the backend: %w", errBackendRequired)
		}

		opts.Local = true
	}

	if !opts.Local {
		c, err := dialBackend(ctx, opts, deps)
		if err == nil {
			stop := c.Close
			c.Close = func() { stop() }
			c.Reconnect = func(ctx context.Context) (*apiClients, error) {
				nc, err := dialBackend(ctx, opts, deps)
				if err != nil {
					return nil, err
				}

				stop()
				stop = nc.Close

				return nc, nil
			}

			return c, nil
		}

		switch {
		case errors.Is(err, errOIDCBackend) && requireBackend:
			return nil, err
		case errors.Is(err, errOIDCBackend):
			_, _ = fmt.Fprintln(deps.Stderr, "ichor: ichor-ui requires OIDC; using your own kubeconfig and RBAC in this process instead")
		case requireBackend:
			return nil, fmt.Errorf("%w (ichor-ui: %v)", errBackendRequired, err)
		default:
			_, _ = fmt.Fprintf(deps.Stderr, "ichor: ichor-ui is not reachable (%v); computing from the Kubernetes API directly\n", err)
		}
	} else if requireBackend {
		return nil, errBackendRequired
	}

	deps.actor = actorCLI

	if deps.WhoAmI != nil {
		if name, err := deps.WhoAmI(ctx); err == nil && name != "" {
			deps.actor = name
		}
	}

	return localClients(opts, deps), nil
}

// localClients is the fallback: the very handlers ichor-ui serves, run
// in-process over the CLI's own Kubernetes client. Because it is the same
// uiapi.Server, every check -- ApproveMachine's identity-strength and role
// rules, strict field validation on every write, the secret masking in
// GetEffectiveConfig -- is the same code, not a copy of it.
func localClients(opts apiOptions, deps apiDeps) *apiClients {
	s := &uiapi.Server{
		Client: deps.K8s, ControllerNamespace: opts.IchorNamespace, ArgoCDNamespace: opts.ArgoCDNamespace, Actor: cmp.Or(deps.actor, actorCLI),
		CallerCredentials: true,
	}

	return &apiClients{
		Cluster: s, Machine: s, Patch: s, Operation: s, Node: localNode{s}, Local: true, Close: func() {},
	}
}

// localNode serves only GetEffectiveConfig, which needs no Talos call.
type localNode struct{ s *uiapi.Server }

func (l localNode) GetEffectiveConfig(
	ctx context.Context, req *connect.Request[pb.GetEffectiveConfigRequest],
) (*connect.Response[pb.GetEffectiveConfigResponse], error) {
	return l.s.GetEffectiveConfig(ctx, req)
}

func (localNode) GetNode(context.Context, *connect.Request[pb.GetNodeRequest]) (*connect.Response[pb.GetNodeResponse], error) {
	return nil, connect.NewError(connect.CodeUnavailable, errBackendRequired)
}

func (localNode) GetNodeTalosDetails(
	context.Context, *connect.Request[pb.GetNodeTalosDetailsRequest],
) (*connect.Response[pb.GetNodeTalosDetailsResponse], error) {
	return nil, connect.NewError(connect.CodeUnavailable, errBackendRequired)
}

func (localNode) GetNodeTalosRuntime(
	context.Context, *connect.Request[pb.GetNodeTalosRuntimeRequest],
) (*connect.Response[pb.GetNodeTalosRuntimeResponse], error) {
	return nil, connect.NewError(connect.CodeUnavailable, errBackendRequired)
}

func (localNode) StreamNodeLogs(
	context.Context, *connect.Request[pb.StreamNodeLogsRequest],
) (*connect.ServerStreamForClient[pb.StreamNodeLogsResponse], error) {
	return nil, connect.NewError(connect.CodeUnavailable, errBackendRequired)
}

// dialBackend reuses ichor ui's discovery: the ichor-ui-service
// Service's selector finds a ready pod, the same tunnel code forwards to
// it, and one HTTP request proves something is answering before any RPC is
// sent.
func dialBackend(ctx context.Context, opts apiOptions, deps apiDeps) (*apiClients, error) {
	svc, err := findUIService(ctx, deps.K8s, opts.IchorNamespace)
	if err != nil {
		return nil, err
	}

	if len(svc.Spec.Ports) == 0 || len(svc.Spec.Selector) == 0 {
		return nil, fmt.Errorf("service %s/%s has no port or selector", svc.Namespace, svc.Name)
	}

	dctx, cancel := context.WithTimeout(ctx, apiConnectTimeout)
	defer cancel()

	pod, err := waitForReadyPod(dctx, deps.K8s, opts.IchorNamespace, svc.Spec.Selector, "", deps.Stderr)
	if err != nil {
		return nil, fmt.Errorf("no ready ichor-ui pod: %w", err)
	}

	port, err := deps.FreePort()
	if err != nil {
		return nil, err
	}

	fctx, stop := context.WithCancel(ctx)
	ready := make(chan struct{})
	done := make(chan error, 1)

	go func() { done <- deps.Forward(fctx, pod, port, svc.Spec.Ports[0].Port, ready) }()

	select {
	case <-ready:
	case err := <-done:
		stop()

		return nil, fmt.Errorf("port-forward to pod/%s ended before it was ready: %v", pod.Name, err)
	case <-dctx.Done():
		stop()

		return nil, fmt.Errorf("port-forward to pod/%s: %w", pod.Name, dctx.Err())
	}

	base := fmt.Sprintf("http://localhost:%d", port)

	if err := probe(dctx, base); err != nil {
		stop()

		return nil, fmt.Errorf("probing ichor-ui through the tunnel: %w", err)
	}

	if oidcEnabled(dctx, base) {
		stop()

		return nil, errOIDCBackend
	}

	h := http.DefaultClient

	return &apiClients{
		Cluster:   ichorv1alpha1connect.NewClusterServiceClient(h, base),
		Machine:   ichorv1alpha1connect.NewMachineServiceClient(h, base),
		Node:      ichorv1alpha1connect.NewNodeServiceClient(h, base),
		Patch:     ichorv1alpha1connect.NewPatchServiceClient(h, base),
		Operation: ichorv1alpha1connect.NewOperationServiceClient(h, base),
		Close:     stop,
	}, nil
}

// probe succeeds on any HTTP response: ichor-ui serves its SPA at "/".
func probe(ctx context.Context, base string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/", nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}

	return resp.Body.Close()
}

// oidcEnabled reads ichor-ui's GET /auth/config. Anything but a
// 200 JSON answer with enabled:true -- an older backend, an unparseable body --
// counts as off: the backend then rejects an unauthenticated call itself.
func oidcEnabled(ctx context.Context, base string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/auth/config", nil)
	if err != nil {
		return false
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}

	defer func() { _ = resp.Body.Close() }()

	var cfg struct {
		Enabled bool `json:"enabled"`
	}

	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&cfg) != nil {
		return false
	}

	return cfg.Enabled
}

// selfSubjectName asks the API server who the kubeconfig's credentials are.
// Only the username is kept; nothing of the credential is read.
func selfSubjectName(ctx context.Context, c authv1client.SelfSubjectReviewInterface) (string, error) {
	r, err := c.Create(ctx, &authv1.SelfSubjectReview{}, metav1.CreateOptions{})
	if err != nil {
		return "", err
	}

	return r.Status.UserInfo.Username, nil
}

// freePort asks the kernel for an unused local port.
func freePort() (int32, error) {
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}

	defer func() { _ = l.Close() }()

	return int32(l.Addr().(*net.TCPAddr).Port), nil //nolint:errcheck,forcetypeassert // a tcp listener's Addr is a *net.TCPAddr
}

// newAPIDeps builds the real dependencies from the kubeconfig.
func newAPIDeps(cmd *cobra.Command) (apiDeps, error) {
	cfg, err := loadKubeconfig(kubeconfigPath)
	if err != nil {
		return apiDeps{}, err
	}

	k8sClient, err := newKubeClientForConfig(cfg)
	if err != nil {
		return apiDeps{}, err
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return apiDeps{}, err
	}

	return apiDeps{K8s: k8sClient, Forward: portForwardToPod(cfg, clientset), FreePort: freePort, Stderr: cmd.ErrOrStderr(),
		WhoAmI: func(ctx context.Context) (string, error) {
			return selfSubjectName(ctx, clientset.AuthenticationV1().SelfSubjectReviews())
		},
	}, nil
}

// runAPI is every command's shell: validate -o, connect, run, close.
func runAPI(cmd *cobra.Command, requireBackend bool, fn func(ctx context.Context, c *apiClients) error) error {
	if err := validateOutputFormat(outputFormat); err != nil {
		return err
	}

	deps, err := newAPIDeps(cmd)
	if err != nil {
		return err
	}

	c, err := connectAPI(cmd.Context(), apiOpts, deps, requireBackend)
	if err != nil {
		return err
	}

	defer c.Close()

	return fn(cmd.Context(), c)
}

// writeMessage prints a wire message in the requested format. Everything
// printed was already masked by the server.
func writeMessage(w io.Writer, format string, msg proto.Message) error {
	raw, err := protojson.MarshalOptions{UseProtoNames: false}.Marshal(msg)
	if err != nil {
		return err
	}

	switch format {
	case outputJSON:
		var buf bytes.Buffer
		if err := json.Indent(&buf, raw, "", "  "); err != nil {
			return err
		}

		buf.WriteByte('\n')
		_, err = w.Write(buf.Bytes())

		return err
	case outputYAML:
		out, err := sigsyaml.JSONToYAML(raw)
		if err != nil {
			return err
		}

		_, err = w.Write(out)

		return err
	default:
		return fmt.Errorf("invalid output format %q", format)
	}
}

func objectRef(namespace, name string) *pb.ObjectRef {
	return &pb.ObjectRef{Namespace: namespace, Name: name}
}

// clusterFilter turns an optional --cluster value into the request filter.
func clusterFilter(namespace, name string) *pb.ObjectRef {
	if name == "" {
		return nil
	}

	return objectRef(namespace, name)
}
