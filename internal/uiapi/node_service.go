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
	"io"
	"net"
	"sync"

	"connectrpc.com/connect"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"golang.org/x/sync/errgroup"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
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

// GetNode returns one machine's hardware, disks, network interfaces,
// live resource usage and Kubernetes node status.
func (s *Server) GetNode(
	ctx context.Context, req *connect.Request[pb.GetNodeRequest],
) (*connect.Response[pb.GetNodeResponse], error) {
	if s.NodeTalosClient == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errNoTalosClient)
	}

	ref := req.Msg.GetMachine()

	m, cluster, err := s.getMachineAndCluster(ctx, ref)
	if err != nil {
		return nil, err
	}

	tc, closeFn, err := s.dialNode(ctx, cluster, m)
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

	if detail.NetworkInterfaces, err = tc.NetworkInterfaces(ctx); err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("reading network interfaces: %w", err))
	}

	if detail.ResourceUsage, err = tc.ResourceUsage(ctx); err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("reading resource usage: %w", err))
	}

	node, err := s.findKubernetesNode(ctx, m)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if node != nil {
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

	bundle, err := loadBundle(ctx, s.Client, s.ControllerNamespace, cluster)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var patches ichorv1alpha1.TalosConfigPatchList
	if err := s.List(ctx, &patches, client.InNamespace(m.Namespace)); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Server.ControllerNamespace, not the TalosCluster's: same fixed namespace as loadBundle
	// above and as the controller's own resolution.
	auth, err := render.ResolveRegistryAuth(ctx, s.Client, s.ControllerNamespace, cluster)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	values, err := effectiveConfigValues(cluster, m, bundle, patches.Items, auth)
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
		return connect.NewError(connect.CodeInvalidArgument, errors.New("machines is required"))
	}

	service := req.Msg.GetService()

	tok := decodeResumeToken(req.Msg.GetResumeToken())

	var mu sync.Mutex

	send := func(ref *pb.ObjectRef, line LogLine) error {
		mu.Lock()
		defer mu.Unlock()

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
			m, cluster, err := s.getMachineAndCluster(gctx, ref)
			if err != nil {
				return err
			}

			tc, closeFn, err := s.dialNode(gctx, cluster, m)
			if err != nil {
				return err
			}

			defer closeFn()

			mu.Lock()
			since := tok[namespacedName(ref).String()].Time
			mu.Unlock()

			return tc.StreamLogs(gctx, service, since, func(line LogLine) error { return send(ref, line) })
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

// dialNode opens m's read-only Talos connection: maintenance mode
// (unauthenticated) for a machine not yet approved, since no CA trust
// exists for it yet, or the bundle's read-only identity otherwise
// (architecture.md "Trust"). The returned close function is always safe to
// call, whether or not the underlying client implements io.Closer.
func (s *Server) dialNode(
	ctx context.Context, cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
) (NodeTalosClient, func(), error) {
	var bundle *secrets.Bundle

	if m.Spec.IsApproved() {
		b, err := loadBundle(ctx, s.Client, s.ControllerNamespace, cluster)
		if err != nil {
			return nil, nil, connect.NewError(connect.CodeInternal, err)
		}

		bundle = b
	}

	tc, err := s.NodeTalosClient(ctx, cluster, m, bundle)
	if err != nil {
		return nil, nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("connecting to machine's Talos API: %w", err))
	}

	closeFn := func() {}
	if closer, ok := tc.(io.Closer); ok {
		closeFn = func() { _ = closer.Close() }
	}

	return tc, closeFn, nil
}

// findKubernetesNode looks up the corev1.Node that backs m: by name when
// TalosMachineSpec.hostname is set, falling back to matching m's Talos
// endpoint address against every Node's reported addresses. A pending or
// not-yet-joined machine has no Node yet -- that is reported as (nil, nil),
// not an error.
func (s *Server) findKubernetesNode(ctx context.Context, m *ichorv1alpha1.TalosMachine) (*corev1.Node, error) {
	if m.Spec.Hostname != "" {
		var node corev1.Node
		if err := s.Get(ctx, client.ObjectKey{Name: m.Spec.Hostname}, &node); err == nil {
			return &node, nil
		}
	}

	host := m.Spec.Endpoint
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	var list corev1.NodeList
	if err := s.List(ctx, &list); err != nil {
		return nil, err
	}

	for i := range list.Items {
		for _, addr := range list.Items[i].Status.Addresses {
			if addr.Address == host {
				return &list.Items[i], nil
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
