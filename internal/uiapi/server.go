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
	"time"

	"connectrpc.com/connect"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/gitopstracking"
	"github.com/maeshinshin/ichor/pkg/prmode"
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
		ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, bundle *secrets.Bundle,
	) (NodeTalosClient, error)

	// Actor is the name recorded on every AuditRecord. Empty means the
	// package default "ichor-ui"; the CLI's in-process fallback
	// (internal/cli) sets "ichor-cli" so the audit trail says which path
	// wrote the CR.
	Actor string

	// ArgoCDNamespace is where argocd-cm is read to tell whether Argo CD
	// tracks a CR (docs/adr/0051). Empty means "argocd".
	ArgoCDNamespace string

	// Draining is closed when the process is shutting down, so a
	// StreamNodeLogs call in flight stops and tells its client to
	// reconnect elsewhere instead of being cut off mid-write
	// (architecture.md: "graceful termination that asks clients to
	// reconnect"). nil (the zero value) is valid and never closes, which
	// is what every non-streaming test wants.
	Draining <-chan struct{}
}

func (s *Server) actorName() string {
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
	var m ichorv1alpha1.TalosMachine

	key := types.NamespacedName{Namespace: ref.GetNamespace(), Name: ref.GetName()}
	if err := s.Get(ctx, key, &m); err != nil {
		return nil, nil, mapGetError(fmt.Sprintf("machine %q", key), err)
	}

	var c ichorv1alpha1.TalosCluster

	ckey := types.NamespacedName{Namespace: m.Namespace, Name: m.Spec.ClusterRef.Name}
	if err := s.Get(ctx, ckey, &c); err != nil {
		return nil, nil, mapGetError(fmt.Sprintf("cluster %q for machine %q", ckey, key), err)
	}

	return &m, &c, nil
}

// mapGetError turns a client.Client Get error into the connect.Code a
// browser-facing read should report: CodeNotFound for a missing object,
// CodeInternal for everything else (a transient API server problem is not
// the caller's fault to diagnose).
func mapGetError(what string, err error) error {
	if apierrors.IsNotFound(err) {
		return connect.NewError(connect.CodeNotFound, fmt.Errorf("%s not found: %w", what, err))
	}

	return connect.NewError(connect.CodeInternal, fmt.Errorf("getting %s: %w", what, err))
}

// defaultArgoCDNamespace is where Argo CD's argocd-cm is read when
// Server.ArgoCDNamespace is empty (the same default `ichor init --argocd`
// uses).
const defaultArgoCDNamespace = "argocd"

// requireUntracked is the one place every write to an existing CR passes
// through (docs/adr/0051): it asks pkg/gitopstracking whether Argo CD or Flux
// owns obj and puts the answer through pkg/prmode.Decide. No repository
// connection is configurable yet, so hasRepoConnection is false and anything
// tracked is read-only; opening a PR for ModeOpenPR is a later unit.
//
// A failed tracking check refuses the write: "cannot tell" must never turn
// into "may write" (docs/adr/0044).
func (s *Server) requireUntracked(ctx context.Context, obj client.Object) error {
	ns := s.ArgoCDNamespace
	if ns == "" {
		ns = defaultArgoCDNamespace
	}

	_, by, err := gitopstracking.IsTracked(ctx, s.Client, ns, obj)
	if err != nil {
		return connect.NewError(connect.CodeUnavailable,
			fmt.Errorf("cannot tell whether Argo CD or Flux tracks %q, so it is not written: %w",
				client.ObjectKeyFromObject(obj), err))
	}

	if prmode.Decide(by, false) == prmode.ModeDirectWrite {
		return nil
	}

	return connect.NewError(connect.CodeFailedPrecondition,
		fmt.Errorf("%q is tracked by %s, so it is read-only here: run `ichor gitops export` to put it in Git "+
			"and change it with a pull request", client.ObjectKeyFromObject(obj), by))
}
