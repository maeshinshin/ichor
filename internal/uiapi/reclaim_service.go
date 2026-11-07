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
	"crypto/rand"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"
	authnv1 "k8s.io/api/authentication/v1"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/reclaim"
)

// ReclaimService. The credential Secret is created with the backend's own identity (users are not
// granted Secret access); the MachineReclaim and the confirmation are written as the caller, so RBAC and the webhook
// judge the user. The talosconfig is never read back, logged or returned.

const reclaimLabelValue = "true"

// mayAll asks Kubernetes, as the caller, whether they hold every one of the attributes (a verb on a resource).
// With nobody to ask (the backend on its own ServiceAccount) it allows: the webhook then judges the ServiceAccount.
func (s *Server) mayAll(ctx context.Context, attrs ...authv1.ResourceAttributes) (bool, error) {
	_, signedIn := IdentityFrom(ctx)
	if (!signedIn || s.Impersonate == nil) && !s.CallerCredentials {
		return true, nil
	}

	return s.askAll(ctx, "reclaim: ", attrs...)
}

// reclaimAllowed: does the caller hold the verb reclaim on machinereclaims?
func (s *Server) reclaimAllowed(ctx context.Context) (bool, error) {
	return s.mayAll(ctx, authv1.ResourceAttributes{
		Namespace: s.ControllerNamespace, Verb: ichorv1alpha1.ReclaimVerb, Group: ichorv1alpha1.GroupVersion.Group,
		Resource: "machinereclaims",
	})
}

// clusterWritable: may the caller get and update the TalosCluster (registering the reclaimed address is an update
// made as them)?
func (s *Server) clusterWritable(ctx context.Context, ref *pb.ObjectRef) (bool, error) {
	attrs := authv1.ResourceAttributes{
		Namespace: ref.GetNamespace(), Group: ichorv1alpha1.GroupVersion.Group, Resource: "talosclusters", Name: ref.GetName(),
	}
	get, update := attrs, attrs
	get.Verb, update.Verb = "get", "update"

	return s.mayAll(ctx, get, update)
}

// reclaimUser is the Kubernetes user name the API server will see for writes made under ctx: the signed-in
// identity, else (the backend on its own ServiceAccount) whatever Kubernetes says we are. The webhook compares
// the audit annotations against it.
func (s *Server) reclaimUser(ctx context.Context) (string, error) {
	if id, ok := IdentityFrom(ctx); ok && s.Impersonate != nil {
		return id.User, nil
	}

	review := &authnv1.SelfSubjectReview{}
	if err := s.Client.Create(ctx, review); err != nil {
		return "", connect.NewError(connect.CodeUnavailable, fmt.Errorf("reclaim: asking Kubernetes who we are: %w", err))
	}

	return review.Status.UserInfo.Username, nil
}

// reclaimGate refuses a caller without the verb: RBAC alone decides.
func (s *Server) reclaimGate(ctx context.Context) error {
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
	ctx context.Context, req *connect.Request[pb.CanReclaimRequest],
) (*connect.Response[pb.CanReclaimResponse], error) {
	ok, err := s.reclaimAllowed(ctx)
	if err != nil {
		return nil, err
	}

	out := &pb.CanReclaimResponse{Enabled: true, Allowed: ok} // Enabled is deprecated, always true

	if c := req.Msg.GetCluster(); ok && c.GetName() != "" {
		if out.ClusterAllowed, err = s.clusterWritable(ctx, c); err != nil {
			return nil, err
		}
	}

	return connect.NewResponse(out), nil
}

func (s *Server) CreateReclaim(
	ctx context.Context, req *connect.Request[pb.CreateReclaimRequest],
) (*connect.Response[pb.CreateReclaimResponse], error) {
	if err := s.reclaimGate(ctx); err != nil {
		return nil, err
	}

	m := req.Msg

	address := strings.TrimSpace(m.GetAddress())
	own := m.GetUseClusterCredentials() // the cluster's own credentials, nothing pasted

	switch {
	case address == "":
		return nil, invalid("address is required")
	case own && (len(m.GetTalosconfig()) != 0 || m.GetCluster().GetName() == ""):
		return nil, invalid("use_cluster_credentials takes the cluster and no talosconfig")
	case !own && len(m.GetTalosconfig()) == 0:
		return nil, invalid("address and talosconfig are required")
	}

	// Refuse before anything is written: registering the address is an update made as the caller, and failing it
	// after the request and the credential exist would leave a half-done request carrying a foreign credential.
	if c := m.GetCluster(); c.GetName() != "" {
		ok, err := s.clusterWritable(ctx, c)
		if err != nil {
			return nil, err
		}

		if !ok {
			return nil, connect.NewError(connect.CodePermissionDenied,
				fmt.Errorf("you may reclaim machines but not add addresses to cluster %s", namespacedName(c)))
		}
	}

	s.pruneReclaimAddresses(ctx) // before this request adds its own address

	// The request is written first, as the caller, with the name of the Secret it will use; the Secret then
	// carries its owner reference from the start. The backend holds only `create` on Secrets: no patch, no
	// read, no delete. A failed Secret create leaves a request without a credential, which the controller
	// reports and expires.
	name := "reclaim-" + strings.ToLower(rand.Text()[:16])

	user, err := s.reclaimUser(ctx)
	if err != nil {
		return nil, err
	}

	mr := &ichorv1alpha1.MachineReclaim{
		GenerateName: "reclaim-", Namespace: s.ControllerNamespace,
		Annotations: map[string]string{ichorv1alpha1.ReclaimRequestedByAnnotation: user},
		Spec: ichorv1alpha1.MachineReclaimSpec{
			Address: address, CredentialSecretRef: name, ContextName: m.GetContext(),
			Force: m.GetForce(), ForceUngraceful: m.GetForceUngraceful(),
		},
	}
	if own {
		mr.Spec.CredentialSecretRef, mr.Spec.ClusterCredentials = "", true
	}
	if c := m.GetCluster(); c.GetName() != "" {
		mr.Spec.ClusterRef = &corev1.LocalObjectReference{Name: c.GetName()}
	}

	if err := s.Create(ctx, mr); err != nil {
		return nil, mapOperationWriteError("creating the reclaim in", types.NamespacedName{Namespace: s.ControllerNamespace}, err)
	}

	if !own {
		if err := s.storeReclaimCredential(ctx, mr, name, m.GetTalosconfig()); err != nil {
			return nil, err
		}
	}

	if c := m.GetCluster(); c.GetName() != "" {
		if err := s.registerReclaimAddress(ctx, c, address); err != nil {
			return nil, err
		}
	}

	return connect.NewResponse(&pb.CreateReclaimResponse{Reclaim: reclaimToPB(mr)}), nil
}

// storeReclaimCredential writes the pasted talosconfig as the request's Secret.
func (s *Server) storeReclaimCredential(ctx context.Context, mr *ichorv1alpha1.MachineReclaim, name string, talosconfig []byte) error {
	secret := &corev1.Secret{
		Name: name, Namespace: s.ControllerNamespace,
		Labels: map[string]string{ichorv1alpha1.ReclaimLabel: reclaimLabelValue},
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion: ichorv1alpha1.GroupVersion.String(), Kind: "MachineReclaim", Name: mr.Name, UID: mr.UID,
		}},
		Type: ichorv1alpha1.ReclaimCredentialSecretType,
		Data: map[string][]byte{ichorv1alpha1.ReclaimCredentialKey: talosconfig},
	}
	// Written by the backend: users hold no Secret rights.
	if err := s.Client.Create(ctx, secret); err != nil {
		return connect.NewError(connect.CodeInternal,
			fmt.Errorf("reclaim %s was created but storing its credential failed", mr.Name))
	}

	return nil
}

// registerReclaimAddress appends the address to the cluster's discovery list when it is not there (the controller
// leaves registration to this user write).
func (s *Server) registerReclaimAddress(ctx context.Context, ref *pb.ObjectRef, address string) error {
	host := reclaim.HostOnly(address)
	if a, err := netip.ParseAddr(host); err != nil || !a.Is4() {
		return nil // discovery lists IPv4 addresses only; a name is left to the person
	}

	key := namespacedName(ref)

	c, err := s.loadCluster(ctx, key)
	if err != nil {
		return err
	}

	if c.Spec.Discovery != nil && slices.Contains(c.Spec.Discovery.Addresses, host) {
		return nil
	}

	if c.Spec.Discovery == nil {
		c.Spec.Discovery = &ichorv1alpha1.TalosClusterDiscovery{}
	}

	c.Spec.Discovery.Addresses = append(c.Spec.Discovery.Addresses, host)

	// Remember that Ichor, not the user, added it, so a request that fails can take it back out.
	metav1.SetMetaDataAnnotation(&c.ObjectMeta, ichorv1alpha1.ReclaimAddedAddressesAnnotation, strings.Join(
		append(splitAddresses(c.Annotations[ichorv1alpha1.ReclaimAddedAddressesAnnotation]), host), ","))

	if err := s.Update(ctx, c, strictFieldValidation); err != nil {
		return mapOperationWriteError("adding the reclaim address to", key, err)
	}

	return nil
}

func splitAddresses(v string) []string {
	return slices.DeleteFunc(strings.Split(v, ","), func(a string) bool { return a == "" })
}

// pruneReclaimAddresses (called from the write paths CreateReclaim and ConfirmReclaim only, never from a read) takes back out of spec.discovery.addresses the addresses CreateReclaim added
// (ReclaimAddedAddressesAnnotation) for requests that did not end Done: failed, expired or deleted ones. An
// address stays when a Done or still running request names it, or a TalosMachine of the cluster is at it.
// An address the user had added never carries the annotation. The update is made as the caller and is best
// effort: a caller who may not update the cluster just leaves the cleanup to the next one.
func (s *Server) pruneReclaimAddresses(ctx context.Context) {
	var clusters ichorv1alpha1.TalosClusterList
	if err := s.List(ctx, &clusters, client.InNamespace(s.ControllerNamespace)); err != nil {
		return
	}

	var requests ichorv1alpha1.MachineReclaimList
	if err := s.List(ctx, &requests, client.InNamespace(s.ControllerNamespace)); err != nil {
		return
	}

	var machines ichorv1alpha1.TalosMachineList

	for i := range clusters.Items {
		c := &clusters.Items[i]

		added := splitAddresses(c.Annotations[ichorv1alpha1.ReclaimAddedAddressesAnnotation])
		if len(added) == 0 {
			continue
		}

		if machines.Items == nil {
			if err := s.List(ctx, &machines, client.InNamespace(s.ControllerNamespace)); err != nil {
				return
			}
		}

		inUse := func(a string) bool {
			for _, r := range requests.Items {
				if r.Spec.ClusterRef != nil && r.Spec.ClusterRef.Name == c.Name && reclaim.HostOnly(r.Spec.Address) == a &&
					r.Status.Phase != ichorv1alpha1.MachineReclaimFailed && r.Status.Phase != ichorv1alpha1.MachineReclaimExpired {
					return true
				}
			}

			for j := range machines.Items {
				m := &machines.Items[j]
				if m.Spec.ClusterRef.Name == c.Name &&
					(reclaim.HostOnly(m.Spec.Endpoint) == a || reclaim.HostOnly(m.Status.ObservedEndpoint) == a) {
					return true
				}
			}

			return false
		}

		gone := slices.DeleteFunc(slices.Clone(added), inUse)
		if len(gone) == 0 {
			continue
		}

		if c.Spec.Discovery != nil {
			c.Spec.Discovery.Addresses = slices.DeleteFunc(c.Spec.Discovery.Addresses, func(x string) bool { return slices.Contains(gone, x) })
		}

		if keep := slices.DeleteFunc(added, func(a string) bool { return slices.Contains(gone, a) }); len(keep) == 0 {
			delete(c.Annotations, ichorv1alpha1.ReclaimAddedAddressesAnnotation)
		} else {
			c.Annotations[ichorv1alpha1.ReclaimAddedAddressesAnnotation] = strings.Join(keep, ",")
		}

		_ = s.Update(ctx, c, strictFieldValidation)
	}
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

	s.pruneReclaimAddresses(ctx)

	var mr ichorv1alpha1.MachineReclaim

	key := namespacedName(req.Msg.GetReclaim())
	if err := s.Get(ctx, key, &mr); err != nil {
		return nil, mapGetError("reclaim "+key.String(), err)
	}

	uuid := strings.TrimSpace(req.Msg.GetUuid())
	if uuid == "" {
		return nil, invalid("uuid is required")
	}

	user, err := s.reclaimUser(ctx)
	if err != nil {
		return nil, err
	}

	metav1.SetMetaDataAnnotation(&mr.ObjectMeta, ichorv1alpha1.ReclaimConfirmedByAnnotation, user)

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
			Refusals: in.Refusals, Warnings: in.Warnings,
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
