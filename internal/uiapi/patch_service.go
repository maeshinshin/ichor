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
	"maps"
	"slices"

	"connectrpc.com/connect"
	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/fieldmeta"
)

// ListPatches returns every TalosConfigPatch the backend can list,
// optionally narrowed to one cluster (ListMachinesRequest's own filter
// convention, machine_service.go).
func (s *Server) ListPatches(
	ctx context.Context, req *connect.Request[pb.ListPatchesRequest],
) (*connect.Response[pb.ListPatchesResponse], error) {
	var list ichorv1alpha1.TalosConfigPatchList
	if err := s.List(ctx, &list); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	filter := req.Msg.GetCluster()

	patches := make([]*pb.Patch, 0, len(list.Items))

	for i := range list.Items {
		p := &list.Items[i]
		if filter != nil && (p.Namespace != filter.GetNamespace() || p.Spec.ClusterRef.Name != filter.GetName()) {
			continue
		}

		wire, err := patchToPB(p)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}

		patches = append(patches, wire)
	}

	return connect.NewResponse(&pb.ListPatchesResponse{Patches: patches}), nil
}

func (s *Server) getPatch(ctx context.Context, ref *pb.ObjectRef) (*ichorv1alpha1.TalosConfigPatch, error) {
	var p ichorv1alpha1.TalosConfigPatch

	key := namespacedName(ref)
	if err := s.Get(ctx, key, &p); err != nil {
		return nil, mapGetError(fmt.Sprintf("patch %q", key), err)
	}

	return &p, nil
}

// GetPatch returns one TalosConfigPatch.
func (s *Server) GetPatch(
	ctx context.Context, req *connect.Request[pb.GetPatchRequest],
) (*connect.Response[pb.GetPatchResponse], error) {
	p, err := s.getPatch(ctx, req.Msg.GetPatch())
	if err != nil {
		return nil, err
	}

	wire, err := patchToPB(p)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&pb.GetPatchResponse{Patch: wire}), nil
}

// CreatePatch writes a new TalosConfigPatch. patch.ref.namespace and
// patch.ref.name are both required -- this method never generates a name
// on the caller's behalf. Every structural rule the CRD's own CEL
// validation already states (layer/target consistency, exactly-one-of on
// target, exactly-one-of on a document's kind-matching field) is enforced
// there, on the write itself, not duplicated here: a rejection comes back
// as CodeInvalidArgument carrying the API server's own message.
func (s *Server) CreatePatch(
	ctx context.Context, req *connect.Request[pb.CreatePatchRequest],
) (*connect.Response[pb.CreatePatchResponse], error) {
	wire := req.Msg.GetPatch()
	ref := wire.GetRef()

	if ref.GetNamespace() == "" || ref.GetName() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("patch.ref.namespace and patch.ref.name are required"))
	}

	p := &ichorv1alpha1.TalosConfigPatch{}
	p.Namespace = ref.GetNamespace()
	p.Name = ref.GetName()

	if err := applyPatchSpec(&p.Spec, wire); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	if err := s.Create(ctx, p, strictFieldValidation); err != nil {
		return nil, mapOperationWriteError("creating patch", namespacedName(ref), err)
	}

	out, err := patchToPB(p)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	audit := s.opAudit(ctx, "create", ref)

	return connect.NewResponse(&pb.CreatePatchResponse{Patch: out, Audit: audit}), nil
}

// UpdatePatch replaces an existing TalosConfigPatch's spec wholesale
// (patch.proto's own "full replace" doc comment) -- the same simplicity
// CreatePatch has, no partial-field merge to get wrong.
func (s *Server) UpdatePatch(
	ctx context.Context, req *connect.Request[pb.UpdatePatchRequest],
) (*connect.Response[pb.UpdatePatchResponse], error) {
	wire := req.Msg.GetPatch()
	ref := wire.GetRef()

	p, err := s.getPatch(ctx, ref)
	if err != nil {
		return nil, err
	}

	if err := applyPatchSpec(&p.Spec, wire); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	if err := s.Update(ctx, p, strictFieldValidation); err != nil {
		return nil, mapOperationWriteError("updating patch", namespacedName(ref), err)
	}

	out, err := patchToPB(p)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	audit := s.opAudit(ctx, "update", ref)

	return connect.NewResponse(&pb.UpdatePatchResponse{Patch: out, Audit: audit}), nil
}

// DeletePatch removes a TalosConfigPatch outright: unlike RejectMachine
// (which must never delete, so a future discovery pass can't recreate it
// as fresh), a patch has no analogous "it would come back"
// concern -- deleting it is the entire operation "remove this patch"
// means.
func (s *Server) DeletePatch(
	ctx context.Context, req *connect.Request[pb.DeletePatchRequest],
) (*connect.Response[pb.DeletePatchResponse], error) {
	ref := req.Msg.GetPatch()

	p, err := s.getPatch(ctx, ref)
	if err != nil {
		return nil, err
	}

	if err := s.Delete(ctx, p); err != nil {
		return nil, mapOperationWriteError("deleting patch", namespacedName(ref), err)
	}

	audit := s.opAudit(ctx, "delete", ref)

	return connect.NewResponse(&pb.DeletePatchResponse{Audit: audit}), nil
}

// PreviewImpact classifies a proposed patch's own fields against
// pkg/fieldmeta, without writing anything (patch.proto's own doc comment:
// every path the patch sets, not a diff against what's currently
// applied -- the safe direction when a true diff isn't computed).
func (s *Server) PreviewImpact(
	ctx context.Context, req *connect.Request[pb.PreviewImpactRequest],
) (*connect.Response[pb.PreviewImpactResponse], error) {
	wire := req.Msg.GetPatch()

	clusterRef := wire.GetCluster()
	if clusterRef.GetName() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("patch.cluster is required"))
	}

	cluster, err := s.loadCluster(ctx, namespacedName(clusterRef))
	if err != nil {
		return nil, err
	}

	pinned, err := coreconfig.ParseContractFromVersion(cluster.Spec.VersionContract)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal,
			fmt.Errorf("cluster %q: parsing version contract %q: %w", cluster.Name, cluster.Spec.VersionContract, err))
	}

	var spec ichorv1alpha1.TalosConfigPatchSpec
	if err := applyPatchSpec(&spec, wire); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	paths, err := patchPaths(spec.Documents)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	impact := fieldmeta.Assess(paths, pinned)

	return connect.NewResponse(&pb.PreviewImpactResponse{
		RebootRequired:  impact.RebootRequired,
		InstallTimeOnly: impact.InstallTimeOnly,
		Unsupported:     impact.Unsupported,
	}), nil
}

// patchPaths flattens every document's own fields to the field paths it
// sets, reusing documentLeafPaths (effectiveconfig.go) -- the same
// per-document, in-isolation render patchOrigins already uses for origin
// attribution -- rather than a second implementation of "what paths does
// this one document set."
func patchPaths(documents []ichorv1alpha1.TalosConfigPatchDocument) ([]string, error) {
	seen := make(map[string]struct{})

	for _, d := range documents {
		leaves, err := documentLeafPaths(d)
		if err != nil {
			return nil, err
		}

		for p, v := range leaves {
			// The legacy document's schema "version" and the empty machine/cluster maps of a
			// sparse document set nothing; as unknown paths they would read as a reboot.
			if p == "version" || v == "{}" || v == "null" {
				continue
			}

			seen[p] = struct{}{}
		}
	}

	return slices.Collect(maps.Keys(seen)), nil
}
