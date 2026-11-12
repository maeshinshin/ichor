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
	"encoding/json"
	"fmt"

	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// objectRef builds the wire ObjectRef every message that names a
// TalosCluster or TalosMachine carries. The browser never sees more of the
// Kubernetes object than this (common.proto's own doc comment).
func objectRef(namespace, name string) *pb.ObjectRef {
	return &pb.ObjectRef{Namespace: namespace, Name: name}
}

// namespacedName turns a client-supplied ObjectRef back into the key a
// client.Client Get/List needs. Every ClusterService/MachineService/
// NodeService method that takes an ObjectRef request field starts here.
func namespacedName(ref *pb.ObjectRef) types.NamespacedName {
	return types.NamespacedName{Namespace: ref.GetNamespace(), Name: ref.GetName()}
}

// conditions converts a CR's kstatus-compatible conditions to the wire
// Condition slice common.proto defines, trimmed to what a screen displays
// (common.proto's own doc comment); nil in, nil out, so an empty
// Status.Conditions never turns into an empty-but-allocated slice on the
// wire.
func conditions(cs []metav1.Condition) []*pb.Condition {
	if cs == nil {
		return nil
	}

	out := make([]*pb.Condition, len(cs))
	for i, c := range cs {
		out[i] = &pb.Condition{
			Type:               c.Type,
			Status:             string(c.Status),
			Reason:             c.Reason,
			Message:            c.Message,
			LastTransitionTime: timestamppb.New(c.LastTransitionTime.Time),
		}
	}

	return out
}

// machineRoleToSpec converts the wire MachineRole enum to the CRD's own
// spelling (TalosMachineSpec.spec.role: "controlplane"/"worker"), the
// mapping ApproveMachine applies to the request and specRoleToMachineRole
// applies in reverse for every read.
func machineRoleToSpec(r pb.MachineRole) string {
	switch r {
	case pb.MachineRole_MACHINE_ROLE_CONTROL_PLANE:
		return "controlplane"
	case pb.MachineRole_MACHINE_ROLE_WORKER:
		return "worker"
	default:
		return ""
	}
}

// specRoleToMachineRole is machineRoleToSpec's inverse, for every read of a
// TalosMachine.spec.role already stored (ListMachines, GetMachine,
// ApproveMachine's own response).
func specRoleToMachineRole(role string) pb.MachineRole {
	switch role {
	case "controlplane":
		return pb.MachineRole_MACHINE_ROLE_CONTROL_PLANE
	case "worker":
		return pb.MachineRole_MACHINE_ROLE_WORKER
	default:
		return pb.MachineRole_MACHINE_ROLE_UNSPECIFIED
	}
}

// specApprovalStateToPB converts TalosMachineSpec.spec.approvalState to the
// wire enum. The CRD's own default ("pending") means an empty
// string here is read the same way the CRD itself reads it, not as
// UNSPECIFIED -- only a value outside the CRD's enum (impossible through
// the API server's own validation, but not through this function's type)
// falls through to UNSPECIFIED.
func specApprovalStateToPB(state string) pb.MachineApprovalState {
	switch state {
	case ichorv1alpha1.MachineApprovalStatePending, "":
		return pb.MachineApprovalState_MACHINE_APPROVAL_STATE_PENDING
	case ichorv1alpha1.MachineApprovalStateApproved:
		return pb.MachineApprovalState_MACHINE_APPROVAL_STATE_APPROVED
	case ichorv1alpha1.MachineApprovalStateRejected:
		return pb.MachineApprovalState_MACHINE_APPROVAL_STATE_REJECTED
	default:
		return pb.MachineApprovalState_MACHINE_APPROVAL_STATE_UNSPECIFIED
	}
}

// approvalDecisionToPB converts TalosMachineSpec.spec.approvalDecision to
// the wire message, nil-safe: unset until the first approve/reject
// (ApproveMachine/RejectMachine both set it; see machine_service.go).
func approvalDecisionToPB(d *ichorv1alpha1.TalosMachineApprovalDecision) *pb.MachineApprovalDecision {
	if d == nil {
		return nil
	}

	return &pb.MachineApprovalDecision{
		Actor: d.Actor,
		At:    timestamppb.New(d.At.Time),
	}
}

// createdAtToPB is an object's creation time, nil when unset (an object not read from an API server).
func createdAtToPB(t metav1.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}

	return timestamppb.New(t.Time)
}

// machineToPB converts a TalosMachine to the wire Machine message
// MachineService's four methods all return (machine.proto's doc comment:
// "One Machine message serves both" the list and the detail view).
func machineToPB(m *ichorv1alpha1.TalosMachine) *pb.Machine {
	return &pb.Machine{
		Ref:     objectRef(m.Namespace, m.Name),
		Cluster: objectRef(m.Namespace, m.Spec.ClusterRef.Name),
		Identity: &pb.MachineIdentity{
			Uuid:         m.Spec.Identity.UUID,
			Serial:       m.Spec.Identity.Serial,
			MacAddresses: m.Spec.Identity.MACAddresses,
		},
		Endpoint:         m.DialEndpoint(),
		Role:             specRoleToMachineRole(m.Spec.Role),
		NodeLabels:       m.Spec.NodeLabels,
		Hostname:         m.Spec.Hostname,
		NodeName:         m.Status.NodeName,
		Install:          installToPB(m.Spec.Install),
		Network:          networkToPB(m.Spec.Network),
		Disks:            disksToPB(m.Spec.Disks),
		DiscoveredDisks:  discoveredDisksToPB(m.Status.DiscoveredDisks),
		DiscoveredLinks:  discoveredLinksToPB(m.Status.DiscoveredLinks),
		ApprovalState:    specApprovalStateToPB(m.Spec.ApprovalState),
		ApprovalDecision: approvalDecisionToPB(m.Spec.ApprovalDecision),
		Conditions:       conditions(m.Status.Conditions),
		Deleting:         !m.DeletionTimestamp.IsZero(),
		RebootPaths:      m.Status.RebootPaths,
		CreatedAt:        createdAtToPB(m.CreationTimestamp),
		Stage:            "", // Talos's own MachineStatus stage: not read by this phase (GetNode's job)
	}
}

// configLayerFromPB is configLayerToPB's (effectiveconfig.go) inverse, for
// PatchService's own writes. CONFIG_LAYER_SYSTEM and
// CONFIG_LAYER_UNSPECIFIED both return "", since neither is a valid value
// for a TalosConfigPatch to carry (system is generated by Ichor, never
// user-writable; unspecified is a required field left unset) -- callers
// check for "" rather than this function returning an error, matching
// ApproveMachine's own "check the zero value explicitly" style
// (machine_service.go).
func configLayerFromPB(layer pb.ConfigLayer) string {
	switch layer {
	case pb.ConfigLayer_CONFIG_LAYER_CLUSTER:
		return "cluster"
	case pb.ConfigLayer_CONFIG_LAYER_ROLE:
		return fieldRole
	case pb.ConfigLayer_CONFIG_LAYER_SELECTOR:
		return "selector"
	case pb.ConfigLayer_CONFIG_LAYER_MACHINE:
		return "machine"
	default:
		return ""
	}
}

// labelSelectorFromPB and labelSelectorToPB mirror metav1.LabelSelector,
// modeled directly in patch.proto rather than through
// typed-config mirror generator: this is Kubernetes' own well-known,
// finite API shape, not a Talos field this project would otherwise have
// to catalogue a second time.
func labelSelectorFromPB(ls *pb.LabelSelector) *metav1.LabelSelector {
	if ls == nil {
		return nil
	}

	out := &metav1.LabelSelector{MatchLabels: ls.GetMatchLabels()}
	for _, req := range ls.GetMatchExpressions() {
		out.MatchExpressions = append(out.MatchExpressions, metav1.LabelSelectorRequirement{
			Key: req.GetKey(), Operator: metav1.LabelSelectorOperator(req.GetOperator()), Values: req.GetValues(),
		})
	}

	return out
}

func labelSelectorToPB(ls *metav1.LabelSelector) *pb.LabelSelector {
	if ls == nil {
		return nil
	}

	out := &pb.LabelSelector{MatchLabels: ls.MatchLabels}
	for _, req := range ls.MatchExpressions {
		out.MatchExpressions = append(out.MatchExpressions, &pb.LabelSelectorRequirement{
			Key: req.Key, Operator: string(req.Operator), Values: req.Values,
		})
	}

	return out
}

// targetFromPB and targetToPB mirror TalosConfigPatchTarget
// (api/v1alpha1/talosconfigpatch_types.go): exactly one of role,
// labelSelector or machineRef is set, enforced by the CRD's own CEL rule
// on write (not re-validated here -- the API server's own rejection is
// what CreatePatch/UpdatePatch surface back as CodeInvalidArgument).
func targetFromPB(t *pb.PatchTarget) *ichorv1alpha1.TalosConfigPatchTarget {
	if t == nil {
		return nil
	}

	target := &ichorv1alpha1.TalosConfigPatchTarget{
		Role:          t.GetRole(),
		LabelSelector: labelSelectorFromPB(t.GetLabelSelector()),
	}
	if ref := t.GetMachineRef(); ref != "" {
		target.MachineRef = &corev1.LocalObjectReference{Name: ref}
	}

	return target
}

func targetToPB(t *ichorv1alpha1.TalosConfigPatchTarget) *pb.PatchTarget {
	if t == nil {
		return nil
	}

	out := &pb.PatchTarget{Role: t.Role, LabelSelector: labelSelectorToPB(t.LabelSelector)}
	if t.MachineRef != nil {
		out.MachineRef = t.MachineRef.Name
	}

	return out
}

// documentFromPB decodes a wire PatchDocument's opaque JSON into the typed
// TalosConfigPatchDocument the CRD actually stores, by wrapping it under
// its own kind's JSON key and letting encoding/json -- not a hand-written
// field table -- fill the matching struct field.
func documentFromPB(d *pb.PatchDocument) (ichorv1alpha1.TalosConfigPatchDocument, error) {
	key, ok := ichorv1alpha1.TalosConfigPatchDocumentFieldKey(d.GetKind())
	if !ok {
		return ichorv1alpha1.TalosConfigPatchDocument{}, fmt.Errorf("unknown document kind %q", d.GetKind())
	}

	raw := json.RawMessage(d.GetDocument())
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}

	wrapped, err := json.Marshal(map[string]any{"kind": d.GetKind(), key: raw})
	if err != nil {
		return ichorv1alpha1.TalosConfigPatchDocument{}, err
	}

	var doc ichorv1alpha1.TalosConfigPatchDocument
	if err := json.Unmarshal(wrapped, &doc); err != nil {
		return ichorv1alpha1.TalosConfigPatchDocument{}, fmt.Errorf("decoding %s document: %w", d.GetKind(), err)
	}

	return doc, nil
}

// documentToPB is documentFromPB's inverse: it re-encodes the one typed
// field kind populates back to plain JSON, so the wire document carries
// only that field's own shape, not the whole TalosConfigPatchDocument
// envelope (the other five fields, always nil here, would otherwise
// round-trip as explicit nulls).
func documentToPB(d ichorv1alpha1.TalosConfigPatchDocument) (*pb.PatchDocument, error) {
	key, ok := ichorv1alpha1.TalosConfigPatchDocumentFieldKey(d.Kind)
	if !ok {
		return nil, fmt.Errorf("unknown document kind %q", d.Kind)
	}

	full, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}

	var generic map[string]json.RawMessage
	if err := json.Unmarshal(full, &generic); err != nil {
		return nil, err
	}

	fieldRaw, ok := generic[key]
	if !ok {
		fieldRaw = json.RawMessage("{}")
	}

	return &pb.PatchDocument{Kind: d.Kind, Document: string(fieldRaw)}, nil
}

// patchToPB converts a TalosConfigPatch to the wire Patch message
// PatchService's read methods return.
func patchToPB(p *ichorv1alpha1.TalosConfigPatch) (*pb.Patch, error) {
	docs := make([]*pb.PatchDocument, 0, len(p.Spec.Documents))
	for _, d := range p.Spec.Documents {
		pd, err := documentToPB(d)
		if err != nil {
			return nil, fmt.Errorf("patch %q: %w", p.Name, err)
		}

		docs = append(docs, pd)
	}

	return &pb.Patch{
		Ref:       objectRef(p.Namespace, p.Name),
		Cluster:   objectRef(p.Namespace, p.Spec.ClusterRef.Name),
		Layer:     configLayerToPB(p.Spec.Layer),
		Priority:  p.Spec.Priority,
		Target:    targetToPB(p.Spec.Target),
		Documents: docs,
		CreatedAt: createdAtToPB(p.CreationTimestamp),
	}, nil
}

// applyPatchSpec fills spec from the wire Patch CreatePatch/UpdatePatch
// received -- a full replace, matching patch.proto's own "no partial-field
// merge" doc comment. layer is the one field checked explicitly here
// (empty is never valid); everything else the CRD's own CEL rules reject
// on write, surfaced back as CodeInvalidArgument by the caller rather than
// re-validated in Go.
func applyPatchSpec(spec *ichorv1alpha1.TalosConfigPatchSpec, p *pb.Patch) error {
	layer := configLayerFromPB(p.GetLayer())
	if layer == "" {
		return fmt.Errorf("layer is required and must be cluster, role, selector or machine")
	}

	docs := make([]ichorv1alpha1.TalosConfigPatchDocument, 0, len(p.GetDocuments()))
	for _, d := range p.GetDocuments() {
		doc, err := documentFromPB(d)
		if err != nil {
			return err
		}

		docs = append(docs, doc)
	}

	spec.ClusterRef = corev1.LocalObjectReference{Name: p.GetCluster().GetName()}
	spec.Layer = layer
	spec.Priority = p.GetPriority()
	spec.Target = targetFromPB(p.GetTarget())
	spec.Documents = docs

	return nil
}

// clusterToPB converts a TalosCluster to the wire Cluster message
// ClusterService's two methods return.
func clusterToPB(c *ichorv1alpha1.TalosCluster) *pb.Cluster {
	schedulable := c.Spec.ControlPlane.Schedulable == "true"

	var addrs, cidrs []string
	if d := c.Spec.Discovery; d != nil {
		addrs, cidrs = d.Addresses, d.CIDRs
	}

	var detected, detectedVersion string
	if st := c.Status.CNI; st != nil {
		detected, detectedVersion = st.Detected, st.DetectedVersion
	}

	var podSubnets, serviceSubnets []string
	if n := c.Spec.Network; n != nil {
		podSubnets, serviceSubnets = n.PodSubnets, n.ServiceSubnets
	}

	return &pb.Cluster{
		CniPreset:               c.Spec.CNI.Preset,
		CniDetected:             detected,
		CniDetectedVersion:      detectedVersion,
		DiscoveryAddresses:      addrs,
		DiscoveryCidrs:          cidrs,
		Ref:                     objectRef(c.Namespace, c.Name),
		TalosVersion:            c.Spec.TalosVersion,
		KubernetesVersion:       c.Spec.KubernetesVersion,
		Endpoint:                c.Spec.Endpoint.Address,
		ControlPlaneCount:       c.Spec.ControlPlane.Count,
		ControlPlaneSchedulable: schedulable,
		Conditions:              conditions(c.Status.Conditions),
		KubeProxyMode:           c.Spec.CNI.KubeProxy,
		Certificates:            certificatesToPB(c.Status.Certificates),
		Versions:                componentVersionsToPB(c),
		ReleaseSet:              releaseSetToPB(),
		CreatedAt:               createdAtToPB(c.CreationTimestamp),
		EndpointType:            c.Spec.Endpoint.Type,
		PodSubnets:              podSubnets,
		ServiceSubnets:          serviceSubnets,
		CniTunnel:               c.Spec.CNI.Tunnel,
		// Rollout and Handovers stay unset: TalosClusterStatus carries
		// neither a rollout nor a hand-over state yet (roadmap phases 6 and
		// 4/8's controller-side work).
	}
}

func certificatesToPB(in []ichorv1alpha1.CertificateExpiry) []*pb.CertificateExpiry {
	out := make([]*pb.CertificateExpiry, len(in))
	for i, c := range in {
		out[i] = &pb.CertificateExpiry{Name: c.Name, NotAfter: timestamppb.New(c.NotAfter.Time)}
	}

	return out
}
