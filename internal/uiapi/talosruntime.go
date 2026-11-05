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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// roleWorkerName is the TalosMachine role that never runs etcd.
const roleWorkerName = "worker"

// GetNodeTalosRuntime reads one machine's live state, read-only:
// etcd, the heaviest processes, containers, network state, kernel modules
// and the Kubernetes view of the node. Authorization and the section model
// are GetNodeTalosDetails': the machine and cluster are read as the caller,
// sections run concurrently with their own timeout and fail independently.
func (s *Server) GetNodeTalosRuntime(
	ctx context.Context, req *connect.Request[pb.GetNodeTalosRuntimeRequest],
) (*connect.Response[pb.GetNodeTalosRuntimeResponse], error) {
	m, tc, closeFn, maintenance, err := s.dialMachine(ctx, req.Msg.GetMachine())
	if err != nil {
		return nil, err
	}

	defer closeFn()

	resp := &pb.GetNodeTalosRuntimeResponse{MaintenanceMode: maintenance}

	runSections(ctx,
		func(c context.Context) {
			if m.Spec.Role == roleWorkerName {
				resp.Etcd = &pb.TalosEtcdSection{NotApplicable: true}
				return
			}

			resp.Etcd = section(c, tc.TalosEtcd, func(e string) *pb.TalosEtcdSection { return &pb.TalosEtcdSection{Error: e} })
		},
		func(c context.Context) {
			resp.Processes = section(c, tc.TalosProcesses, func(e string) *pb.TalosProcessesSection { return &pb.TalosProcessesSection{Error: e} })
		},
		func(c context.Context) {
			resp.Containers = section(c, tc.TalosContainers, func(e string) *pb.TalosContainersSection { return &pb.TalosContainersSection{Error: e} })
			resp.Containers.Containers = sanitizeContainers(resp.Containers.Containers)
		},
		func(c context.Context) {
			resp.NetworkState = section(c, tc.TalosNetworkState, func(e string) *pb.TalosNetworkStateSection { return &pb.TalosNetworkStateSection{Error: e} })
		},
		func(c context.Context) {
			resp.KernelModules = section(c, tc.TalosKernelModules, func(e string) *pb.TalosKernelModulesSection { return &pb.TalosKernelModulesSection{Error: e} })
		},
		func(c context.Context) { resp.Kubernetes = s.kubernetesSection(c, m) },
	)

	return connect.NewResponse(resp), nil
}

// sanitizeContainers strips credentials from image references once, in the
// handler, so every NodeTalosClient implementation passes through it (the
// same boundary redactKernelArgs uses).
func sanitizeContainers(cs []*pb.TalosContainer) []*pb.TalosContainer {
	for _, c := range cs {
		c.Image = sanitizeImage(c.GetImage())
	}

	return cs
}

// kubernetesSection reads the Node behind m and the pods bound to it as the
// caller, so the caller's own Kubernetes RBAC decides what shows.
func (s *Server) kubernetesSection(ctx context.Context, m *ichorv1alpha1.TalosMachine) *pb.KubernetesNodeSection {
	node, err := s.findKubernetesNode(ctx, m)

	switch {
	case apierrors.IsForbidden(err):
		return &pb.KubernetesNodeSection{PermissionDenied: true}
	case err != nil:
		return &pb.KubernetesNodeSection{Error: truncateText(err.Error(), maxSectionErrorLen)}
	case node == nil:
		return &pb.KubernetesNodeSection{NotJoined: true}
	}

	out := kubernetesNodeSection(node)

	var pods corev1.PodList

	err = s.List(ctx, &pods, client.MatchingFields{"spec.nodeName": node.Name}, client.Limit(maxPods+1))
	if err != nil {
		out.PodsError = truncateText(err.Error(), maxSectionErrorLen)

		return out
	}

	now := s.now()

	for i := range pods.Items {
		if len(out.Pods) >= maxPods {
			out.PodsTruncated = true

			break
		}

		out.Pods = append(out.Pods, podFromObject(&pods.Items[i], now))
	}

	return out
}
