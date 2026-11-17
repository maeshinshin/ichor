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
	"slices"

	"connectrpc.com/connect"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
	storagepkg "github.com/maeshinshin/ichor/pkg/storage"
)

// storagePresets is every value of TalosStorage.spec.diskPreset this build knows, in the CRD enum's order.
// TestStoragePresetsMatchTheCRDEnum reads the enum from config/crd/bases and fails if the two differ.
var storagePresets = []string{storagepkg.LonghornV1, storagepkg.RookV1}

// presetUnavailable says why the page does not create a preset, and what to do instead. A preset not
// listed here is creatable.
var presetUnavailable = map[string]string{
	// shortcut: longhorn-v1 is refused here until a validating webhook checks the disk selector; drop this
	// entry only with that webhook, not with the CRD's non-empty rule alone.
	storagepkg.LonghornV1: "longhorn-v1 needs a disk selector, and this page does not write one: the API server " +
		"only checks that the selector is not empty, not that it is a valid Talos expression. Apply the TalosStorage " +
		"yourself with kubectl, as an ichor-admin, after checking the selector against the disks of the nodes it will prepare.",
}

// ListStorages returns the TalosStorages of one TalosCluster, each with the machines it governs
// under the controller's own rule (render.StorageFor). Read only: nothing here writes a TalosStorage.
// It runs as the signed-in user, like every read here, so the viewer role's get and list rules decide.
func (s *Server) ListStorages(
	ctx context.Context, req *connect.Request[pb.ListStoragesRequest],
) (*connect.Response[pb.ListStoragesResponse], error) {
	if req.Msg.GetCluster() == nil {
		return nil, invalid("cluster is required")
	}

	cluster, err := s.loadCluster(ctx, namespacedName(req.Msg.GetCluster()))
	if err != nil {
		return nil, err
	}

	var storages ichorv1alpha1.TalosStorageList
	if err := s.List(ctx, &storages, client.InNamespace(cluster.Namespace)); err != nil {
		return nil, mapListError("TalosStorage", err)
	}

	var machines ichorv1alpha1.TalosMachineList
	if err := s.List(ctx, &machines, client.InNamespace(cluster.Namespace)); err != nil {
		return nil, mapListError("TalosMachine", err)
	}

	governed := map[string][]*pb.ObjectRef{}

	for i := range machines.Items {
		m := &machines.Items[i]
		if m.Spec.ClusterRef.Name != cluster.Name {
			continue
		}

		st, err := render.StorageFor(cluster, m, storages.Items)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}

		if st != nil {
			governed[st.Name] = append(governed[st.Name], objectRef(m.Namespace, m.Name))
		}
	}

	presets := make([]*pb.StoragePreset, 0, len(storagePresets))

	for _, name := range storagePresets {
		reason, blocked := presetUnavailable[name]
		presets = append(presets, &pb.StoragePreset{Name: name, Creatable: !blocked, UnavailableReason: reason})
	}

	out := make([]*pb.Storage, 0, len(storages.Items))

	for i := range storages.Items {
		ts := &storages.Items[i]
		if ts.Spec.ClusterRef.Name != cluster.Name {
			continue
		}

		out = append(out, storageToPB(ts, governed[ts.Name]))
	}

	return connect.NewResponse(&pb.ListStoragesResponse{Storages: out, Presets: presets}), nil
}

// CreateStorage writes one TalosStorage in the cluster's namespace as the signed-in user: s.Create runs on
// the user's client, so the user's create verb on talosstorages decides, not the ServiceAccount's. Only a
// creatable preset is accepted, and the request has no field for a disk selector, so the page never writes one.
func (s *Server) CreateStorage(
	ctx context.Context, req *connect.Request[pb.CreateStorageRequest],
) (*connect.Response[pb.CreateStorageResponse], error) {
	m := req.Msg

	if m.GetCluster().GetName() == "" {
		return nil, invalid("cluster is required")
	}

	if m.GetName() == "" {
		return nil, invalid("name is required")
	}

	if !slices.Contains(storagePresets, m.GetDiskPreset()) {
		return nil, invalid("unknown disk preset %q", m.GetDiskPreset())
	}

	if reason, blocked := presetUnavailable[m.GetDiskPreset()]; blocked {
		return nil, invalid("%s", reason)
	}

	sel, err := metav1.ParseToLabelSelector(m.GetNodeSelector())
	if err != nil {
		return nil, invalid("node selector: %v", err)
	}

	// An empty selector would select every node in the cluster.
	if len(sel.MatchLabels) == 0 && len(sel.MatchExpressions) == 0 {
		return nil, invalid("node selector must name at least one label")
	}

	cluster, err := s.loadCluster(ctx, namespacedName(m.GetCluster()))
	if err != nil {
		return nil, err
	}

	ts := &ichorv1alpha1.TalosStorage{}
	ts.Namespace, ts.Name = cluster.Namespace, m.GetName()
	ts.Spec = ichorv1alpha1.TalosStorageSpec{
		ClusterRef: corev1.LocalObjectReference{Name: cluster.Name},
		Nodes:      *sel,
		DiskPreset: m.GetDiskPreset(),
	}

	if err := s.Create(ctx, ts, strictFieldValidation); err != nil {
		return nil, mapOperationWriteError("creating storage", types.NamespacedName{Namespace: ts.Namespace, Name: ts.Name}, err)
	}

	return connect.NewResponse(&pb.CreateStorageResponse{
		Storage: storageToPB(ts, nil),
		Audit:   s.opAudit(ctx, "create", objectRef(ts.Namespace, ts.Name)),
	}), nil
}

// storageToPB is one TalosStorage as the page shows it. A preset this build does not know is reported
// in preset_error, not refused: the TalosStorage still exists and its other fields are still true.
func storageToPB(ts *ichorv1alpha1.TalosStorage, governed []*pb.ObjectRef) *pb.Storage {
	out := &pb.Storage{
		Ref:              objectRef(ts.Namespace, ts.Name),
		DiskPreset:       ts.Spec.DiskPreset,
		DiskSelector:     ts.Spec.DiskSelector,
		NodeSelector:     metav1.FormatLabelSelector(&ts.Spec.Nodes),
		Conditions:       conditions(ts.Status.Conditions),
		GovernedMachines: governed,
	}

	if d := ts.Spec.EvacuationTimeout; d != nil {
		out.EvacuationTimeout = d.Duration.String()
	}

	p, err := storagepkg.NewPreset(ts.Spec.DiskPreset)
	if err != nil {
		out.PresetError = err.Error()
	} else {
		out.RequiredExtensions = p.RequiredExtensions()
	}

	return out
}
