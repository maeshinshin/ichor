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
	"errors"
	"time"

	"connectrpc.com/connect"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"google.golang.org/protobuf/encoding/protojson"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

const (
	nameKubelet = "kubelet"
	gatewayIP   = "10.0.0.1"
)

var _ = Describe("Talos runtime mapping", func() {
	It("keeps the executable name only and never the command line", func() {
		p := processFromInfo(&machineapi.ProcessInfo{
			Pid: 7, Executable: "/usr/bin/etcd", Command: "etcd",
			Args: "--token=" + fakeSecret, Label: "label-" + fakeSecret,
		})
		Expect(p.GetName()).To(Equal("etcd"))

		raw, err := protojson.Marshal(p)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring(fakeSecret))
	})

	It("keeps the union of the heaviest processes by CPU and memory, bounded", func() {
		all := make([]*machineapi.ProcessInfo, 0, 100)
		for i := range 100 {
			all = append(all, &machineapi.ProcessInfo{
				Pid: int32(i), Executable: "/bin/p", CpuTime: float64(i), ResidentMemory: uint64(100 - i), //nolint:gosec // test data
			})
		}

		got := topProcesses(all)
		Expect(got).To(HaveLen(2 * topProcessesEach))
		Expect(got[0].GetResidentBytes()).To(BeNumerically(">=", got[1].GetResidentBytes()))

		pids := map[int32]bool{}
		for _, p := range got {
			pids[p.GetPid()] = true
		}

		Expect(pids).To(HaveKey(int32(99)), "the busiest by CPU is kept")
		Expect(pids).To(HaveKey(int32(0)), "the largest by memory is kept")
	})

	It("removes credentials from image references and keeps digests", func() {
		Expect(sanitizeImage("user:" + fakeSecret + "@registry.local:5000/app:1")).To(Equal("registry.local:5000/app:1"))
		Expect(sanitizeImage("ghcr.io/siderolabs/kubelet@sha256:abc")).To(Equal("ghcr.io/siderolabs/kubelet@sha256:abc"))
		Expect(sanitizeImage("busybox")).To(Equal("busybox"))
	})

	It("maps a pod without reading its spec beyond container counts", func() {
		pod := &corev1.Pod{
			Namespace: "ns", Name: "p", CreationTimestamp: metav1.NewTime(time.Unix(0, 0)),
			Spec: corev1.PodSpec{Containers: []corev1.Container{
				{Name: "a", Env: []corev1.EnvVar{{Name: "TOKEN", Value: fakeSecret}}}, {Name: "b"},
			}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{
				{Ready: true, RestartCount: 2}, {Ready: false, RestartCount: 1},
			}},
		}

		out := podFromObject(pod, time.Unix(60, 0))
		Expect(out.GetReadyContainers()).To(Equal(int32(1)))
		Expect(out.GetTotalContainers()).To(Equal(int32(2)))
		Expect(out.GetRestarts()).To(Equal(int32(3)))
		Expect(out.GetAgeSeconds()).To(Equal(int64(60)))

		raw, err := protojson.Marshal(out)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring(fakeSecret))
	})
})

var _ = Describe("NodeService.GetNodeTalosRuntime", func() {
	req := func(m *ichorv1alpha1.TalosMachine) *connect.Request[pb.GetNodeTalosRuntimeRequest] {
		return connect.NewRequest(&pb.GetNodeTalosRuntimeRequest{Machine: objectRef(m.Namespace, m.Name)})
	}

	It("returns every section, strips credentials and shows the node's pods", func() {
		cluster := newTestClusterWithBundle()
		m := newTestMachine(cluster.Name, strongTestIdentity("uuid-rt-ok"), roleControlPlane, ichorv1alpha1.MachineApprovalStateApproved)
		m.Spec.Hostname = "node-runtime-ok"
		Expect(k8sClient.Update(ctx, m)).To(Succeed())

		node := &corev1.Node{
			Name: m.Spec.Hostname,
			Spec: corev1.NodeSpec{Taints: []corev1.Taint{{Key: "dedicated", Value: "x", Effect: corev1.TaintEffectNoSchedule}}},
		}
		Expect(k8sClient.Create(ctx, node)).To(Succeed())

		node.Status = corev1.NodeStatus{
			Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
			Capacity:    corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")},
			Allocatable: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("3")},
		}
		Expect(k8sClient.Status().Update(ctx, node)).To(Succeed())

		pod := &corev1.Pod{
			Name: "runtime-pod", Namespace: testNamespace,
			Spec: corev1.PodSpec{
				NodeName:   node.Name,
				Containers: []corev1.Container{{Name: "c", Image: "busybox", Env: []corev1.EnvVar{{Name: "TOKEN", Value: fakeSecret}}}},
			},
		}
		Expect(k8sClient.Create(ctx, pod)).To(Succeed())

		fake := &fakeNodeTalosClient{
			runtimeEtcd: &pb.TalosEtcdSection{
				Members: []*pb.TalosEtcdMember{{Id: "00ab", Hostname: "cp1", Leader: true}}, RaftTerm: 3,
			},
			runtimeProcesses: &pb.TalosProcessesSection{Total: 9, Processes: []*pb.TalosProcess{{Pid: 1, Name: "init"}}},
			runtimeContainers: &pb.TalosContainersSection{Containers: []*pb.TalosContainer{
				{Name: nameKubelet, Image: "user:" + fakeSecret + "@reg.local/kubelet:1"},
			}},
			runtimeNetwork: &pb.TalosNetworkStateSection{Routes: []*pb.TalosRoute{{Destination: "0.0.0.0/0", Gateway: gatewayIP}}},
			runtimeModules: &pb.TalosKernelModulesSection{Modules: []*pb.TalosKernelModule{{Name: "nf_tables"}}},
		}

		client, closeFn := newTestNodeServer(fake, nil)
		defer closeFn()

		resp, err := client.GetNodeTalosRuntime(ctx, req(m))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Msg.GetMaintenanceMode()).To(BeFalse())
		Expect(resp.Msg.GetEtcd().GetMembers()).To(HaveLen(1))
		Expect(resp.Msg.GetProcesses().GetTotal()).To(Equal(int32(9)))
		Expect(resp.Msg.GetContainers().GetContainers()[0].GetImage()).To(Equal("reg.local/kubelet:1"))
		Expect(resp.Msg.GetNetworkState().GetRoutes()).To(HaveLen(1))
		Expect(resp.Msg.GetKernelModules().GetModules()).To(HaveLen(1))

		kube := resp.Msg.GetKubernetes()
		Expect(kube.GetNodeName()).To(Equal(node.Name))
		Expect(kube.GetConditions()).To(HaveLen(1))
		Expect(kube.GetTaints()[0].GetKey()).To(Equal("dedicated"))
		Expect(kube.GetPods()).To(HaveLen(1))
		Expect(kube.GetPods()[0].GetName()).To(Equal("runtime-pod"))

		raw, err := protojson.Marshal(resp.Msg)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring(fakeSecret), "no secret-shaped value may reach the response")
		Expect(fake.closed).To(BeTrue())
	})

	It("skips etcd on a worker, reports a failing section inline and a node that has not joined", func() {
		cluster := newTestClusterWithBundle()
		m := newTestMachine(cluster.Name, strongTestIdentity("uuid-rt-worker"), roleWorker, ichorv1alpha1.MachineApprovalStateApproved)
		fake := &fakeNodeTalosClient{
			runtimeProcesses: &pb.TalosProcessesSection{Processes: []*pb.TalosProcess{{Name: "partial-should-be-dropped"}}},
			talosErr:         map[string]error{"processes": errors.New("listing processes: boom")},
		}

		client, closeFn := newTestNodeServer(fake, nil)
		defer closeFn()

		resp, err := client.GetNodeTalosRuntime(ctx, req(m))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Msg.GetEtcd().GetNotApplicable()).To(BeTrue())
		Expect(resp.Msg.GetProcesses().GetError()).To(ContainSubstring("boom"))
		Expect(resp.Msg.GetProcesses().GetProcesses()).To(BeEmpty())
		Expect(resp.Msg.GetKubernetes().GetNotJoined()).To(BeTrue())
	})

	It("flags maintenance mode for a machine that is not approved", func() {
		cluster := newTestClusterWithBundle()
		m := newTestMachine(cluster.Name, strongTestIdentity("uuid-rt-pending"), "", ichorv1alpha1.MachineApprovalStatePending)
		client, closeFn := newTestNodeServer(&fakeNodeTalosClient{}, nil)
		defer closeFn()

		resp, err := client.GetNodeTalosRuntime(ctx, req(m))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Msg.GetMaintenanceMode()).To(BeTrue())
	})

	It("reports CodeUnavailable when no NodeTalosClient is configured", func() {
		cluster := newTestClusterWithBundle()
		m := newTestMachine(cluster.Name, strongTestIdentity("uuid-rt-none"), roleWorker, ichorv1alpha1.MachineApprovalStateApproved)
		client, closeFn := newTestNodeServer(nil, nil)
		defer closeFn()

		_, err := client.GetNodeTalosRuntime(ctx, req(m))
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnavailable))
	})
})
