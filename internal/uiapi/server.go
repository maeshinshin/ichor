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

package uiapi

import (
	"context"
	"fmt"
	"sync"
	"time"

	"connectrpc.com/connect"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/internal/settings"
)

// Server implements ClusterServiceHandler, MachineServiceHandler and
// NodeServiceHandler (gen/go/ichor/v1alpha1/ichorv1alpha1connect) on one
// type: all three read the same two Kubernetes resources (TalosCluster,
// TalosMachine) through the embedded client.Client, so splitting them into
// three Go types would only have meant three copies of the same lookups.
type Server struct {
	client.Client

	// ControllerNamespace is this backend's own namespace (POD_NAMESPACE in
	// practice, see cmd/ichor-ui/main.go), the same meaning
	// TalosMachineReconciler.ControllerNamespace has on the controller side
	// (ADR 0011): loadBundle resolves the secrets bundle Secret here, never
	// in the TalosCluster's own namespace, since RBAC cannot scope a Secret
	// read to "wherever that TalosCluster happens to live" at anything
	// narrower than cluster-wide.
	ControllerNamespace string

	// Now returns the current time. nil means time.Now -- tests inject a
	// fixed clock so audit timestamps and log resume tokens are
	// deterministic, the same reason pkg/render's tests use
	// secrets.NewFixedClock.
	Now func() time.Time

	// NodeTalosClient opens the UI backend's read-only connection to one
	// machine's Talos API (architecture.md "Trust": "the UI backend holds
	// a read-only [Talos identity]"). NewMachineryNodeTalosClient is the
	// real implementation cmd/ichor-ui wires in; tests supply a fake. nil
	// is reported through GetNode/GetEffectiveConfig/StreamNodeLogs
	// returning connect.CodeUnavailable, the same "not configured" shape
	// internal/controller.TalosMachineReconciler.TalosClient uses.
	NodeTalosClient func(
		ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, id *TalosIdentity,
	) (NodeTalosClient, error)

	// Actor is the name recorded on every AuditRecord. Empty means the
	// package default "ichor-ui"; the CLI's in-process fallback
	// (internal/cli) sets "ichor-cli" so the audit trail says which path
	// wrote the CR.
	Actor string

	// Impersonate returns a client that acts as id against the Kubernetes API
	// (docs/adr/0058). nil means calls always use Client, the ServiceAccount.
	Impersonate func(Identity) (client.Client, error)

	// CallerCredentials is true when Client carries the caller's own
	// Kubernetes credentials (the CLI's in-process fallback, docs/adr/0049)
	// rather than the backend's ServiceAccount. Break-glass is allowed to ask
	// Client "may I?" only then, or through Impersonate (docs/adr/0065).
	CallerCredentials bool

	// DownloadTalosClient opens the connection for the support bundle, etcd
	// snapshot and packet capture downloads (docs/adr/0084), each with the
	// least Talos role its call needs. NewMachineryDownloadTalosClient is the
	// real one; nil answers Unavailable.
	DownloadTalosClient DownloadTalosClientFactory

	// VerifyKubeconfig checks that a kubeconfig authenticates to its cluster before a CA rotation's
	// credentials hand-off is acknowledged (docs/adr/0093). nil means credentials.VerifyKubeconfig;
	// tests supply a fake, since no API server accepts a freshly minted CA.
	VerifyKubeconfig func(ctx context.Context, kubeconfig []byte) error

	// Users is non-nil when Ichor's own users are on (docs/adr/0094); UserService
	// answers FailedPrecondition otherwise.
	Users *UserSettings

	// Settings, when set, is read per call (ADR 0105).
	Settings *settings.Reader

	// creds holds the identities and public facts the controller issued (credissuer.go).
	creds credentialCache

	diag     diagnostics
	diagOnce sync.Once

	// Draining is closed when the process is shutting down, so a
	// StreamNodeLogs call in flight stops and tells its client to
	// reconnect elsewhere instead of being cut off mid-write
	// (architecture.md: "graceful termination that asks clients to
	// reconnect"). nil (the zero value) is valid and never closes, which
	// is what every non-streaming test wants.
	Draining <-chan struct{}
}

func (s *Server) actorName(ctx context.Context) string {
	if id, ok := IdentityFrom(ctx); ok {
		return id.User
	}

	if s.Actor != "" {
		return s.Actor
	}

	return actor
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}

	return time.Now()
}

// getMachineAndCluster reads ref's TalosMachine and the TalosCluster it
// names (TalosMachineSpec.ClusterRef, resolved in the machine's own
// namespace -- the same lookup pkg/render's callers already do). Every
// Connect method that needs either starts here so the connect.Code a
// missing object produces is chosen in exactly one place.
func (s *Server) getMachineAndCluster(
	ctx context.Context, ref *pb.ObjectRef,
) (*ichorv1alpha1.TalosMachine, *ichorv1alpha1.TalosCluster, error) {
	m, err := s.resolveMachine(ctx, ref)
	if err != nil {
		return nil, nil, err
	}

	c, err := s.loadCluster(ctx, types.NamespacedName{Namespace: m.Namespace, Name: m.Spec.ClusterRef.Name})
	if err != nil {
		return nil, nil, err
	}

	return m, c, nil
}

// loadCluster reads the TalosCluster at key, mapping a failed read to its connect.Code.
func (s *Server) loadCluster(ctx context.Context, key types.NamespacedName) (*ichorv1alpha1.TalosCluster, error) {
	var c ichorv1alpha1.TalosCluster
	if err := s.Get(ctx, key, &c); err != nil {
		return nil, mapGetError(fmt.Sprintf("cluster %q", key), err)
	}

	return &c, nil
}

// mapGetError turns a client.Client Get error into the connect.Code a
// browser-facing read should report: CodeNotFound for a missing object,
// CodePermissionDenied for a forbidden read, CodeInternal for everything else (a transient API server problem is not
// the caller's fault to diagnose).
func mapGetError(what string, err error) error {
	if apierrors.IsNotFound(err) {
		return connect.NewError(connect.CodeNotFound, fmt.Errorf("%s not found: %w", what, err))
	}

	if apierrors.IsForbidden(err) {
		return connect.NewError(connect.CodePermissionDenied, fmt.Errorf("getting %s: %w", what, err))
	}

	return connect.NewError(connect.CodeInternal, fmt.Errorf("getting %s: %w", what, err))
}

// mapListError is mapGetError for a List: forbidden is the caller's answer,
// anything else is internal.
func mapListError(what string, err error) error {
	if apierrors.IsForbidden(err) {
		return connect.NewError(connect.CodePermissionDenied, fmt.Errorf("listing %s: %w", what, err))
	}

	return connect.NewError(connect.CodeInternal, fmt.Errorf("listing %s: %w", what, err))
}
