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

	"connectrpc.com/connect"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
	storagepkg "github.com/maeshinshin/ichor/pkg/storage"
)

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

	out := make([]*pb.Storage, 0, len(storages.Items))

	for i := range storages.Items {
		ts := &storages.Items[i]
		if ts.Spec.ClusterRef.Name != cluster.Name {
			continue
		}

		out = append(out, storageToPB(ts, governed[ts.Name]))
	}

	return connect.NewResponse(&pb.ListStoragesResponse{Storages: out}), nil
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
