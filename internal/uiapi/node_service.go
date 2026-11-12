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
	"errors"
	"fmt"
	"net"
	"slices"
	"sync"

	"connectrpc.com/connect"
	"golang.org/x/sync/errgroup"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
)

// errNoTalosClient is returned when the backend was started with no
// NodeTalosClient factory wired in -- cmd/ichor-ui always wires one, so
// this is a test/configuration state, not something a deployed instance
// hits (server.go's own doc comment on the field).
var errNoTalosClient = errors.New("no Talos API connectivity is configured for this backend")

// GetNode returns one machine's hardware, disks, live resource usage and
// Kubernetes node status.
func (s *Server) GetNode(
	ctx context.Context, req *connect.Request[pb.GetNodeRequest],
) (*connect.Response[pb.GetNodeResponse], error) {
	ref := req.Msg.GetMachine()

	m, tc, closeFn, _, err := s.dialMachine(ctx, ref)
	if err != nil {
		return nil, err
	}

	defer closeFn()

	detail := &pb.NodeDetail{Machine: ref}

	if detail.Hardware, err = tc.Hardware(ctx); err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("reading hardware: %w", err))
	}

	if detail.Disks, err = tc.Disks(ctx); err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("reading disks: %w", err))
	}

	if detail.ResourceUsage, err = tc.ResourceUsage(ctx); err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("reading resource usage: %w", err))
	}

	node, err := s.findKubernetesNode(ctx, m)

	switch {
	case apierrors.IsForbidden(err):
		detail.KubernetesPermissionDenied = true
	case err != nil:
		return nil, connect.NewError(connect.CodeInternal, err)
	case node != nil:
		detail.Kubernetes = kubernetesNodeStatus(node)
	}

	return connect.NewResponse(&pb.GetNodeResponse{Node: detail}), nil
}

// GetEffectiveConfig renders machine's complete configuration and returns
// it flattened, origin-annotated and secret-masked (effectiveconfig.go).
// It needs no Talos API call -- the rendered configuration is computed the
// same way the controller computes it, from the CRs and the secrets
// bundle, never read back off the node.
func (s *Server) GetEffectiveConfig(
	ctx context.Context, req *connect.Request[pb.GetEffectiveConfigRequest],
) (*connect.Response[pb.GetEffectiveConfigResponse], error) {
	ref := req.Msg.GetMachine()

	m, cluster, err := s.getMachineAndCluster(ctx, ref)
	if err != nil {
		return nil, err
	}

	if m.Spec.Role == "" {
		// Guarded here explicitly rather than left to pkg/render.BuildConfig's
		// machine.ParseType call: machinery's ParseType("") does not itself
		// reject an empty role (it is not this package's call to rely on that
		// changing), so an unapproved or otherwise role-less machine is
		// refused before any rendering is attempted, not after.
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("machine %q has no role yet; approve it before requesting its effective configuration", namespacedName(ref)))
	}

	bundle, err := s.renderBundle(ctx, cluster)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var patches ichorv1alpha1.TalosConfigPatchList
	if err := s.List(ctx, &patches, client.InNamespace(m.Namespace)); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Server.ControllerNamespace, not the TalosCluster's: same fixed namespace as loadBundle
	// above and as the controller's own resolution.
	sys, err := render.ResolveSystemInputs(ctx, maskedSecrets{s.Client}, s.ControllerNamespace, cluster)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// ResolveSystemInputs lists TalosStorage with the ServiceAccount; the
	// presets end up in the (unmasked) rendering, so the caller's own list
	// decides which of them they see. Registry credentials
	// stay on the ServiceAccount: they only leave as masked secrets.
	var storages ichorv1alpha1.TalosStorageList
	if err := s.List(ctx, &storages, client.InNamespace(cluster.Namespace)); err != nil {
		return nil, mapListError("TalosStorage", err)
	}

	sys.Storage = storages.Items

	values, err := effectiveConfigValues(cluster, m, bundle, patches.Items, sys)
	if err != nil {
		// A machine with no role yet (unapproved) or any other input the
		// renderer rejects is this machine's own state, not a server fault.
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("rendering configuration: %w", err))
	}

	return connect.NewResponse(&pb.GetEffectiveConfigResponse{
		Config: &pb.EffectiveConfig{Machine: ref, Values: values},
	}), nil
}

// StreamNodeLogs tails one or more machines' log, merging them onto one
// stream (node.proto's own doc comment: the node page's single-machine
// tail and the merged multi-node viewer are "the same mechanism at
// different cardinality"). service selects which log -- empty means
// Talos's kernel log (Dmesg), any other value a Talos service name
// (kubelet, etcd, trustd, apid, machined, ...), read through Talos's Logs
// API. It stops and asks the client to reconnect when the
// backend starts draining (architecture.md: "graceful termination that
// asks clients to reconnect"), surviving that drain through resume_token.
func (s *Server) StreamNodeLogs(
	ctx context.Context, req *connect.Request[pb.StreamNodeLogsRequest], stream *connect.ServerStream[pb.StreamNodeLogsResponse],
) error {
	if s.NodeTalosClient == nil {
		return connect.NewError(connect.CodeUnavailable, errNoTalosClient)
	}

	refs := req.Msg.GetMachines()
	if len(refs) == 0 {
		return invalid("machines is required")
	}

	if len(refs) > maxLogMachines {
		return invalid("at most %d machines per log stream", maxLogMachines)
	}

	// The same machine twice would double its lines and its connections.
	seen := map[types.NamespacedName]bool{}
	refs = slices.DeleteFunc(slices.Clone(refs), func(r *pb.ObjectRef) bool {
		dup := seen[namespacedName(r)]
		seen[namespacedName(r)] = true

		return dup
	})

	release, err := s.acquireLogSlots(len(refs))
	if err != nil {
		return err
	}
	defer release()

	service := req.Msg.GetService()

	opts := logOptions(req.Msg)

	tok := decodeResumeToken(req.Msg.GetResumeToken())

	var mu sync.Mutex

	send := func(ref *pb.ObjectRef, line LogLine) error {
		mu.Lock()
		defer mu.Unlock()

		if service == "" { // the kernel log carries the boot command line, with its talos.config token
			line.Message = string(redactKernelLog([]byte(line.Message)))
		}

		tok = tok.advance(namespacedName(ref).String(), line)

		token, err := tok.encode()
		if err != nil {
			return err
		}

		return stream.Send(&pb.StreamNodeLogsResponse{
			Machine: ref, Time: timestamppb.New(line.Time), Message: line.Message, ResumeToken: token,
		})
	}

	g, gctx := errgroup.WithContext(ctx)

	for _, ref := range refs {
		g.Go(func() error {
			_, tc, closeFn, _, err := s.dialMachine(gctx, ref)
			if err != nil {
				return err
			}

			defer closeFn()

			mu.Lock()
			since := tok[namespacedName(ref).String()].Time
			mu.Unlock()

			return tc.StreamLogs(gctx, service, since, opts, func(line LogLine) error { return send(ref, line) })
		})
	}

	done := make(chan error, 1)

	go func() { done <- g.Wait() }()

	select {
	case err := <-done:
		return err
	case <-s.Draining:
		return connect.NewError(connect.CodeUnavailable,
			errors.New("ichor-ui is shutting down; reconnect with the latest resume_token to continue"))
	}
}

// logOptions resolves a request's tail_lines and follow into what the Talos
// client gets: tail defaults to DefaultTailLines, is capped at MaxTailLines
// (negative stays negative, meaning the whole history), and follow defaults
// to true so existing callers keep streaming.
func logOptions(req *pb.StreamNodeLogsRequest) LogOptions {
	tail := req.GetTailLines()

	switch {
	case tail == 0:
		tail = DefaultTailLines
	case tail > MaxTailLines:
		tail = MaxTailLines
	}

	return LogOptions{Follow: req.Follow == nil || req.GetFollow(), TailLines: tail}
}

// dialMachine is dialNode for the machine ref names, read as the caller;
// it fails Unavailable when no NodeTalosClient factory is wired in.
func (s *Server) dialMachine(
	ctx context.Context, ref *pb.ObjectRef,
) (*ichorv1alpha1.TalosMachine, NodeTalosClient, func(), bool, error) {
	if s.NodeTalosClient == nil {
		return nil, nil, nil, false, connect.NewError(connect.CodeUnavailable, errNoTalosClient)
	}

	m, cluster, err := s.getMachineAndCluster(ctx, ref)
	if err != nil {
		return nil, nil, nil, false, err
	}

	tc, closeFn, maintenance, err := s.dialNode(ctx, cluster, m)

	return m, tc, closeFn, maintenance, err
}

// dialNode opens m's read-only Talos connection: maintenance mode
// (unauthenticated) for a machine not yet approved, since no CA trust
// exists for it yet, or the bundle's read-only identity otherwise
// (architecture.md "Trust"). An approved machine that has never converged
// may still be in maintenance mode (approved without an install disk, or
// waiting to be provisioned), where the bundle identity is not trusted: if
// the authenticated probe read fails the connection falls back to the
// maintenance API, the same rule the controller uses. The bool
// reports whether the connection is a maintenance one. The returned close
// function is always safe to call, whether or not the underlying client
// implements io.Closer.
func (s *Server) dialNode(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
) (NodeTalosClient, func(), bool, error) {
	var id *TalosIdentity

	if m.Spec.IsApproved() {
		i, err := s.nodeReader(ctx, cluster)
		if err != nil {
			return nil, nil, false, connect.NewError(connect.CodeInternal, err)
		}

		id = i
	}

	tc, closeFn, err := s.openNodeClient(ctx, cluster, m, id)
	if err != nil {
		return nil, nil, false, err
	}

	if id == nil {
		return tc, closeFn, true, nil
	}

	// ponytail: a Hardware read doubles as the trust probe; a cheaper
	// dedicated call would need a new NodeTalosClient method.
	if m.Status.AppliedConfigHash != "" {
		return tc, closeFn, false, nil
	}

	if _, probeErr := tc.Hardware(ctx); probeErr == nil {
		return tc, closeFn, false, nil
	}

	closeFn()

	tc, closeFn, err = s.openNodeClient(ctx, cluster, m, nil)
	if err != nil {
		return nil, nil, false, err
	}

	return tc, closeFn, true, nil
}

func (s *Server) openNodeClient(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, id *TalosIdentity,
) (NodeTalosClient, func(), error) {
	tc, err := s.NodeTalosClient(ctx, cluster, m, id)
	if err != nil {
		return nil, nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("connecting to machine's Talos API: %w", err))
	}

	return tc, closeFunc(tc), nil
}

// findKubernetesNode looks up the corev1.Node that backs m: by name when
// TalosMachineSpec.hostname is set, falling back to matching m's Talos
// endpoint address against every Node's reported addresses. A pending or
// not-yet-joined machine has no Node yet -- that is reported as (nil, nil),
// not an error.
func (s *Server) findKubernetesNode(ctx context.Context, m *ichorv1alpha1.TalosMachine) (*corev1.Node, error) {
	if m.Spec.Hostname != "" {
		var node corev1.Node
		// Read as the caller: the Node's status is returned to them.
		switch err := s.Get(ctx, client.ObjectKey{Name: m.Spec.Hostname}, &node); {
		case err == nil:
			return &node, nil
		case apierrors.IsForbidden(err):
			return nil, err
		}
	}

	// The address the node answers on now, then the one spec names: a node that moved is found by the first.
	var hosts []string

	for _, ep := range []string{m.DialEndpoint(), m.Spec.Endpoint} {
		if h, _, err := net.SplitHostPort(ep); err == nil {
			ep = h
		}

		hosts = append(hosts, ep)
	}

	var list corev1.NodeList
	if err := s.List(ctx, &list); err != nil {
		return nil, err
	}

	for _, host := range hosts {
		for i := range list.Items {
			for _, addr := range list.Items[i].Status.Addresses {
				if addr.Address == host {
					return &list.Items[i], nil
				}
			}
		}
	}

	return nil, nil
}

func kubernetesNodeStatus(node *corev1.Node) *pb.KubernetesNodeStatus {
	ready := false

	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			ready = c.Status == corev1.ConditionTrue

			break
		}
	}

	return &pb.KubernetesNodeStatus{
		NodeName:       node.Name,
		Ready:          ready,
		KubeletVersion: node.Status.NodeInfo.KubeletVersion,
		Schedulable:    !node.Spec.Unschedulable,
	}
}
