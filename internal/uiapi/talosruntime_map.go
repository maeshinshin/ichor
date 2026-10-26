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
	"cmp"
	"fmt"
	"net/netip"
	"path"
	"slices"
	"strings"
	"time"

	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	corev1 "k8s.io/api/core/v1"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// The functions below build the wire messages of GetNodeTalosRuntime field by field, like talosdetails_map.go: no
// Talos or
// Kubernetes object is marshalled whole.

const (
	// topProcessesEach is how many processes are kept by CPU time and by
	// resident memory; the response carries their union.
	topProcessesEach = 15
	maxContainers    = 300
	maxRoutes        = 200
	maxPods          = 300
	maxConditionMsg  = 200
)

// addrString is "" for an unset address, where netip.Addr prints "invalid IP".
func addrString(a netip.Addr) string {
	if !a.IsValid() {
		return ""
	}

	return a.String()
}

func memberID(id uint64) string { return fmt.Sprintf("%016x", id) }

// processFromInfo keeps the executable's base name and numbers. Neither
// Args nor the full Executable path leaves this function: a command line can
// carry tokens and passwords.
func processFromInfo(p *machineapi.ProcessInfo) *pb.TalosProcess {
	name := path.Base(p.GetExecutable())
	if name == "." || name == "/" {
		name = p.GetCommand()
	}

	return &pb.TalosProcess{
		Pid: p.GetPid(), Ppid: p.GetPpid(), State: p.GetState(), Threads: p.GetThreads(),
		CpuSeconds: p.GetCpuTime(), ResidentBytes: p.GetResidentMemory(), VirtualBytes: p.GetVirtualMemory(),
		Name: name,
	}
}

// topProcesses returns the union of the heaviest processes by CPU time and by
// resident memory, ordered by resident memory.
func topProcesses(all []*machineapi.ProcessInfo) []*pb.TalosProcess {
	byCPU := slices.Clone(all)
	slices.SortFunc(byCPU, func(a, b *machineapi.ProcessInfo) int { return cmp.Compare(b.GetCpuTime(), a.GetCpuTime()) })

	byMem := slices.Clone(all)
	slices.SortFunc(byMem, func(a, b *machineapi.ProcessInfo) int {
		return cmp.Compare(b.GetResidentMemory(), a.GetResidentMemory())
	})

	seen := map[int32]bool{}
	out := []*pb.TalosProcess{}

	for _, list := range [][]*machineapi.ProcessInfo{byMem, byCPU} {
		for _, p := range list[:min(topProcessesEach, len(list))] {
			if !seen[p.GetPid()] {
				seen[p.GetPid()] = true

				out = append(out, processFromInfo(p))
			}
		}
	}

	slices.SortFunc(out, func(a, b *pb.TalosProcess) int { return cmp.Compare(b.GetResidentBytes(), a.GetResidentBytes()) })

	return out
}

// sanitizeImage drops credentials from an image reference
// (user:password@registry/repo:tag). A digest's "@" comes after the first "/"
// and is kept.
func sanitizeImage(ref string) string {
	host, rest, ok := strings.Cut(ref, "/")
	if !ok {
		return ref
	}

	if _, after, found := strings.Cut(host, "@"); found {
		host = after
	}

	return host + "/" + rest
}

func containerFromInfo(c *machineapi.ContainerInfo) *pb.TalosContainer {
	return &pb.TalosContainer{
		RuntimeNamespace: c.GetNamespace(), Id: c.GetId(), Pod: c.GetPodId(), Name: c.GetName(),
		Image: sanitizeImage(c.GetImage()), Status: c.GetStatus(), Pid: c.GetPid(),
	}
}

func truncateText(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}

	return s
}

// kubernetesNodeSection builds the Node half of the Kubernetes view: its
// conditions, taints and capacity against allocatable. Pods are added by the
// caller.
func kubernetesNodeSection(node *corev1.Node) *pb.KubernetesNodeSection {
	out := &pb.KubernetesNodeSection{
		NodeName: node.Name, KubeletVersion: node.Status.NodeInfo.KubeletVersion, Unschedulable: node.Spec.Unschedulable,
	}

	for _, c := range node.Status.Conditions {
		out.Conditions = append(out.Conditions, &pb.KubernetesCondition{
			Type: string(c.Type), Status: string(c.Status), Reason: c.Reason, Message: truncateText(c.Message, maxConditionMsg),
		})
	}

	for _, t := range node.Spec.Taints {
		out.Taints = append(out.Taints, &pb.KubernetesTaint{Key: t.Key, Value: t.Value, Effect: string(t.Effect)})
	}

	for _, name := range []corev1.ResourceName{
		corev1.ResourceCPU, corev1.ResourceMemory, corev1.ResourcePods, corev1.ResourceEphemeralStorage,
	} {
		capQ, allocQ := node.Status.Capacity[name], node.Status.Allocatable[name]
		out.Resources = append(out.Resources, &pb.KubernetesResource{
			Name: string(name), Capacity: capQ.String(), Allocatable: allocQ.String(),
		})
	}

	return out
}

// podFromObject keeps the pod's identity, phase, readiness and restarts. No
// spec, environment, volume or annotation is read.
func podFromObject(p *corev1.Pod, now time.Time) *pb.KubernetesPod {
	out := &pb.KubernetesPod{
		Namespace: p.Namespace, Name: p.Name, Phase: string(p.Status.Phase),
		TotalContainers: int32(len(p.Spec.Containers)), //nolint:gosec // container counts are tiny
		AgeSeconds:      int64(now.Sub(p.CreationTimestamp.Time).Seconds()),
	}

	for _, cs := range p.Status.ContainerStatuses {
		if cs.Ready {
			out.ReadyContainers++
		}

		out.Restarts += cs.RestartCount
	}

	return out
}
