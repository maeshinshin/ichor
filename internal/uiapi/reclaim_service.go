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
	"net/netip"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// ReclaimService. The credential Secret is created with the backend's own identity (users are not
// granted Secret access); the MachineReclaim and the confirmation are written as the caller, so RBAC and the webhook
// judge the user. The talosconfig is never read back, logged or returned.

const reclaimLabelValue = "true"

var errReclaimOff = connect.NewError(connect.CodeFailedPrecondition,
	errors.New("reclaim is not enabled in this installation (reclaim.enabled)"))

// reclaimAllowed asks Kubernetes, as the caller, whether they hold the verb reclaim on machinereclaims.
// With nobody to ask (the backend on its own ServiceAccount) it allows: the webhook then judges the ServiceAccount.
func (s *Server) reclaimAllowed(ctx context.Context) (bool, error) {
	_, signedIn := IdentityFrom(ctx)
	if (!signedIn || s.Impersonate == nil) && !s.CallerCredentials {
		return true, nil
	}

	c, err := s.cl(ctx)
	if err != nil {
		return false, connect.NewError(connect.CodeUnavailable, fmt.Errorf("reclaim: acting as the user: %w", err))
	}

	review := &authv1.SelfSubjectAccessReview{Spec: authv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authv1.ResourceAttributes{
		Namespace: s.ControllerNamespace, Verb: ichorv1alpha1.ReclaimVerb, Group: ichorv1alpha1.GroupVersion.Group,
		Resource: "machinereclaims",
	}}}
	if err := c.Create(ctx, review); err != nil {
		return false, connect.NewError(connect.CodeUnavailable, fmt.Errorf("reclaim: asking Kubernetes whether you may: %w", err))
	}

	return review.Status.Allowed, nil
}

// reclaimGate refuses every call when the feature is off, and a caller without the verb.
func (s *Server) reclaimGate(ctx context.Context) error {
	if !s.ReclaimEnabled {
		return errReclaimOff
	}

	ok, err := s.reclaimAllowed(ctx)
	if err != nil {
		return err
	}

	if !ok {
		return connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("RBAC does not grant you the %q verb on machinereclaims", ichorv1alpha1.ReclaimVerb))
	}

	return nil
}

func (s *Server) CanReclaim(
	ctx context.Context, _ *connect.Request[pb.CanReclaimRequest],
) (*connect.Response[pb.CanReclaimResponse], error) {
	if !s.ReclaimEnabled {
		return connect.NewResponse(&pb.CanReclaimResponse{}), nil
	}

	ok, err := s.reclaimAllowed(ctx)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&pb.CanReclaimResponse{Enabled: true, Allowed: ok}), nil
}

func (s *Server) CreateReclaim(
	ctx context.Context, req *connect.Request[pb.CreateReclaimRequest],
) (*connect.Response[pb.CreateReclaimResponse], error) {
	if err := s.reclaimGate(ctx); err != nil {
		return nil, err
	}

	m := req.Msg

	address := strings.TrimSpace(m.GetAddress())
	if address == "" || len(m.GetTalosconfig()) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("address and talosconfig are required"))
	}

	// The request is written first, as the caller, with the name of the Secret it will use; the Secret then
	// carries its owner reference from the start. The backend holds only `create` on Secrets: no patch, no
	// read, no delete. A failed Secret create leaves a request without a credential, which the controller
	// reports and expires.
	name := "reclaim-" + utilrand.String(8)

	mr := &ichorv1alpha1.MachineReclaim{
		GenerateName: "reclaim-", Namespace: s.ControllerNamespace,
		Spec: ichorv1alpha1.MachineReclaimSpec{
			Address: address, CredentialSecretRef: name, ContextName: m.GetContext(),
			Force: m.GetForce(), ForceUngraceful: m.GetForceUngraceful(),
		},
	}
	if c := m.GetCluster(); c.GetName() != "" {
		mr.Spec.ClusterRef = &corev1.LocalObjectReference{Name: c.GetName()}
	}

	if err := s.Create(ctx, mr); err != nil {
		return nil, mapOperationWriteError("creating the reclaim in", types.NamespacedName{Namespace: s.ControllerNamespace}, err)
	}

	secret := &corev1.Secret{
		Name: name, Namespace: s.ControllerNamespace,
		Labels: map[string]string{ichorv1alpha1.ReclaimLabel: reclaimLabelValue},
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion: ichorv1alpha1.GroupVersion.String(), Kind: "MachineReclaim", Name: mr.Name, UID: mr.UID,
		}},
		Type: ichorv1alpha1.ReclaimCredentialSecretType,
		Data: map[string][]byte{ichorv1alpha1.ReclaimCredentialKey: m.GetTalosconfig()},
	}
	// Written by the backend: users hold no Secret rights.
	if err := s.Client.Create(ctx, secret); err != nil {
		return nil, connect.NewError(connect.CodeInternal,
			fmt.Errorf("reclaim %s was created but storing its credential failed", mr.Name))
	}

	if c := m.GetCluster(); c.GetName() != "" {
		if err := s.registerReclaimAddress(ctx, c, address); err != nil {
			return nil, err
		}
	}

	return connect.NewResponse(&pb.CreateReclaimResponse{Reclaim: reclaimToPB(mr)}), nil
}

// registerReclaimAddress appends the address to the cluster's discovery list when it is not there (the controller
// leaves registration to this user write).
func (s *Server) registerReclaimAddress(ctx context.Context, ref *pb.ObjectRef, address string) error {
	host := address
	if h, _, err := net.SplitHostPort(address); err == nil {
		host = h
	}

	if a, err := netip.ParseAddr(host); err != nil || !a.Is4() {
		return nil // discovery lists IPv4 addresses only; a name is left to the person
	}

	var c ichorv1alpha1.TalosCluster

	key := namespacedName(ref)
	if err := s.Get(ctx, key, &c); err != nil {
		return mapGetError("cluster "+key.String(), err)
	}

	if c.Spec.Discovery != nil && slices.Contains(c.Spec.Discovery.Addresses, host) {
		return nil
	}

	if c.Spec.Discovery == nil {
		c.Spec.Discovery = &ichorv1alpha1.TalosClusterDiscovery{}
	}

	c.Spec.Discovery.Addresses = append(c.Spec.Discovery.Addresses, host)

	if err := s.Update(ctx, &c, strictFieldValidation); err != nil {
		return mapOperationWriteError("adding the reclaim address to", key, err)
	}

	return nil
}

func (s *Server) GetReclaim(
	ctx context.Context, req *connect.Request[pb.GetReclaimRequest],
) (*connect.Response[pb.GetReclaimResponse], error) {
	if err := s.reclaimGate(ctx); err != nil {
		return nil, err
	}

	var mr ichorv1alpha1.MachineReclaim

	key := namespacedName(req.Msg.GetReclaim())
	if err := s.Get(ctx, key, &mr); err != nil {
		return nil, mapGetError("reclaim "+key.String(), err)
	}

	return connect.NewResponse(&pb.GetReclaimResponse{Reclaim: reclaimToPB(&mr)}), nil
}

func (s *Server) ListReclaims(
	ctx context.Context, _ *connect.Request[pb.ListReclaimsRequest],
) (*connect.Response[pb.ListReclaimsResponse], error) {
	if err := s.reclaimGate(ctx); err != nil {
		return nil, err
	}

	var list ichorv1alpha1.MachineReclaimList
	if err := s.List(ctx, &list, client.InNamespace(s.ControllerNamespace)); err != nil {
		return nil, mapListError("reclaims", err)
	}

	out := &pb.ListReclaimsResponse{}
	for i := range list.Items {
		out.Reclaims = append(out.Reclaims, reclaimToPB(&list.Items[i]))
	}

	return connect.NewResponse(out), nil
}

// ConfirmReclaim writes spec.confirmUUID (and confirmClusterName when given) and nothing else.
func (s *Server) ConfirmReclaim(
	ctx context.Context, req *connect.Request[pb.ConfirmReclaimRequest],
) (*connect.Response[pb.ConfirmReclaimResponse], error) {
	if err := s.reclaimGate(ctx); err != nil {
		return nil, err
	}

	var mr ichorv1alpha1.MachineReclaim

	key := namespacedName(req.Msg.GetReclaim())
	if err := s.Get(ctx, key, &mr); err != nil {
		return nil, mapGetError("reclaim "+key.String(), err)
	}

	uuid := strings.TrimSpace(req.Msg.GetUuid())
	if uuid == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("uuid is required"))
	}

	mr.Spec.ConfirmUUID = uuid
	if n := strings.TrimSpace(req.Msg.GetClusterName()); n != "" {
		mr.Spec.ConfirmClusterName = n
	}

	if err := s.Update(ctx, &mr); err != nil {
		return nil, mapOperationWriteError("confirming", key, err)
	}

	return connect.NewResponse(&pb.ConfirmReclaimResponse{Reclaim: reclaimToPB(&mr)}), nil
}

func reclaimToPB(mr *ichorv1alpha1.MachineReclaim) *pb.Reclaim {
	out := &pb.Reclaim{
		Ref: objectRef(mr.Namespace, mr.Name), Address: mr.Spec.Address, Phase: string(mr.Status.Phase),
		Result: mr.Status.Result, Force: mr.Spec.Force, ForceUngraceful: mr.Spec.ForceUngraceful,
		Confirmed: mr.Spec.ConfirmUUID != "",
	}
	if e := mr.Status.ExpiresAt; e != nil {
		out.ExpiresAt = timestamppb.New(e.Time)
	}

	if in := mr.Status.Inspection; in != nil {
		i := &pb.ReclaimInspection{
			Uuid: in.UUID, Hostname: in.Hostname, Role: in.Role, ClusterName: in.ClusterName, ClusterId: in.ClusterID,
			Refusals: in.Refusals,
		}
		for _, m := range in.EtcdMembers {
			i.EtcdMembers = append(i.EtcdMembers, m.Hostname)
		}

		for _, v := range in.Volumes {
			i.Volumes = append(i.Volumes, &pb.ReclaimVolume{Name: v.Name, Location: v.Location, Size: v.SizeBytes, Encrypted: v.Encrypted})
		}

		out.Inspection = i
	}

	return out
}
