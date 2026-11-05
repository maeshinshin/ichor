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

package controller

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/clusterlock"
	"github.com/maeshinshin/ichor/pkg/storage"
)

// nodeFixture is a cluster whose machines each have a faked Talos API and a Kubernetes Node in a
// fake clientset. machines[i] is named hostnames[i] as a Node and as an etcd member.
type nodeFixture struct {
	cluster  *ichorv1alpha1.TalosCluster
	machines map[string]*ichorv1alpha1.TalosMachine
	fakes    map[string]*fakeTalosClient
	kube     *drainFake
	r        *TalosOperationReconciler
}

// newNodeFixture creates one machine per (host, role) pair, in order.
func newNodeFixture(hosts map[string]string) *nodeFixture {
	f := &nodeFixture{machines: map[string]*ichorv1alpha1.TalosMachine{}, fakes: map[string]*fakeTalosClient{}}
	secret := newTestSecret(newTestBundle("v1.14"))
	f.cluster = newTestCluster(secret.Name, "v1.14")

	f.kube = newDrainFake(func() int {
		total := 0
		for _, fk := range f.fakes {
			total += fk.rebootCalls
		}

		return total
	})

	for host, role := range hosts {
		m := newOpTestMachine(f.cluster.Name, role, host)
		setHostname(m, host)
		f.machines[host] = m
		f.fakes[m.Name] = &fakeTalosClient{bootID: testBootIDBefore}

		_, err := f.kube.CoreV1().Nodes().Create(context.Background(), drainTestNode(host, false), metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
	}

	// A second schedulable worker, so the last-worker guard does not apply unless a test removes it.
	_, err := f.kube.CoreV1().Nodes().Create(context.Background(), drainTestNode(nodeSpareWorker, false), metav1.CreateOptions{})
	Expect(err).NotTo(HaveOccurred())

	f.r = f.reconciler()

	return f
}

const nodeSpareWorker = "spare-worker"

func (f *nodeFixture) reconciler() *TalosOperationReconciler {
	return &TalosOperationReconciler{
		Client: k8sClient, APIReader: k8sClient, ControllerNamespace: tmNamespace, KubeClient: f.kube,
		TalosClient: func(_ context.Context, _ *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, _ *secrets.Bundle) (TalosClient, error) {
			return f.fakes[m.Name], nil
		},
	}
}

// members makes every machine's etcd list the control planes among hosts.
func (f *nodeFixture) members(hosts ...string) {
	list := make([]EtcdMember, 0, len(hosts))
	for i, h := range hosts {
		list = append(list, EtcdMember{ID: uint64(i + 1), Hostname: h})
	}

	for _, fk := range f.fakes {
		fk.etcdMembers = list
	}
}

func (f *nodeFixture) newOp(host string, spec ichorv1alpha1.TalosOperationNodeSpec) *ichorv1alpha1.TalosOperation {
	spec.MachineRef.Name = f.machines[host].Name

	op := &ichorv1alpha1.TalosOperation{}
	op.GenerateName, op.Namespace = nodeOpPrefix, tmNamespace
	op.Spec.ClusterRef.Name, op.Spec.Kind, op.Spec.Node = f.cluster.Name, ichorv1alpha1.TalosOperationKindNode, &spec
	Expect(k8sClient.Create(ctx, op)).To(Succeed())

	return op
}

func (f *nodeFixture) reconcile(op *ichorv1alpha1.TalosOperation) *ichorv1alpha1.TalosOperation {
	_, err := reconcileOperation(f.r, op)
	Expect(err).NotTo(HaveOccurred())

	return fetchOperation(op)
}

func (f *nodeFixture) fake(host string) *fakeTalosClient { return f.fakes[f.machines[host].Name] }

func (f *nodeFixture) unschedulable(host string) bool { return f.kube.node(host).Spec.Unschedulable }

func nodeSpec(action string) ichorv1alpha1.TalosOperationNodeSpec {
	return ichorv1alpha1.TalosOperationNodeSpec{Action: action}
}

const nodeOpPrefix = "node-op-"

const (
	hostW1  = "w1"
	hostCP1 = "cp1"
	hostCP2 = "cp2"
	hostCP3 = "cp3"
)

func threeControlPlanes() map[string]string {
	return map[string]string{hostCP1: roleControlPlane, hostCP2: roleControlPlane, hostCP3: roleControlPlane, hostW1: roleWorker}
}

var _ = Describe("TalosOperation node actions", func() {
	BeforeEach(func() { nodeActionSettle = 0 })

	Describe("reboot", func() {
		It("drains, reboots, waits for a new boot and a Ready node, then uncordons", func() {
			f := newNodeFixture(map[string]string{hostW1: roleWorker})
			op := f.newOp(hostW1, nodeSpec(ichorv1alpha1.NodeActionReboot))

			got := f.reconcile(op)
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))
			Expect(f.fake(hostW1).rebootCalls).To(Equal(1))
			Expect(got.Status.Node.Step).To(Equal(ichorv1alpha1.NodeStepRequested))
			Expect(got.Status.Machines).To(HaveLen(1))
			Expect(got.Status.Machines[0].BootID).To(Equal(testBootIDBefore))
			Expect(f.unschedulable(hostW1)).To(BeTrue(), "the node is cordoned for the reboot")

			got = f.reconcile(op)
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress), "same boot ID: still waiting")
			Expect(f.fake(hostW1).rebootCalls).To(Equal(1), "never rebooted twice")

			f.fake(hostW1).bootID = testBootIDAfter
			got = f.reconcile(op)
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
			Expect(f.unschedulable(hostW1)).To(BeFalse(), "uncordoned once back")
			Expect(f.fake(hostW1).rebootCalls).To(Equal(1))
		})

		It("a node that is away while rebooting is waited for, and a restarted controller does not reboot it again", func() {
			f := newNodeFixture(map[string]string{hostW1: roleWorker})
			op := f.newOp(hostW1, nodeSpec(ichorv1alpha1.NodeActionReboot))
			f.reconcile(op)

			f.fake(hostW1).bootIDErr = errors.New("connection refused")
			f.r = f.reconciler() // the controller was on the rebooting node and restarted elsewhere

			got := f.reconcile(op)
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))
			Expect(f.fake(hostW1).rebootCalls).To(Equal(1))

			f.fake(hostW1).bootIDErr, f.fake(hostW1).bootID = nil, testBootIDAfter
			Expect(f.reconcile(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
			Expect(f.fake(hostW1).rebootCalls).To(Equal(1))
		})

		It("refuses the last schedulable worker, for reboot and shutdown", func() {
			for _, action := range []string{ichorv1alpha1.NodeActionReboot, ichorv1alpha1.NodeActionShutdown} {
				f := newNodeFixture(map[string]string{hostW1: roleWorker})
				Expect(f.kube.CoreV1().Nodes().Delete(context.Background(), nodeSpareWorker, metav1.DeleteOptions{})).To(Succeed())

				got := f.reconcile(f.newOp(hostW1, nodeSpec(action)))
				Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
				Expect(got.Status.FailureReason).To(ContainSubstring("last schedulable"))
				Expect(f.fake(hostW1).rebootCalls).To(BeZero())
				Expect(f.fake(hostW1).shutdownCalls).To(BeEmpty())
			}
		})

		It("names override in the refusal, and lets spec.node.override waive it with no reason", func() {
			for _, action := range []string{ichorv1alpha1.NodeActionReboot, ichorv1alpha1.NodeActionShutdown} {
				f := newNodeFixture(map[string]string{hostW1: roleWorker})
				Expect(f.kube.CoreV1().Nodes().Delete(context.Background(), nodeSpareWorker, metav1.DeleteOptions{})).To(Succeed())

				got := f.reconcile(f.newOp(hostW1, nodeSpec(action)))
				Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
				Expect(got.Status.FailureReason).To(ContainSubstring("override"))

				f = newNodeFixture(map[string]string{hostW1: roleWorker})
				Expect(f.kube.CoreV1().Nodes().Delete(context.Background(), nodeSpareWorker, metav1.DeleteOptions{})).To(Succeed())

				spec := nodeSpec(action)
				spec.Override = true // the reason is optional
				got = f.reconcile(f.newOp(hostW1, spec))
				Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))

				if action == ichorv1alpha1.NodeActionReboot {
					Expect(f.fake(hostW1).rebootCalls).To(Equal(1))
				} else {
					Expect(f.fake(hostW1).shutdownCalls).NotTo(BeEmpty())
				}
			}
		})

		It("refuses a node that is not Ready", func() {
			f := newNodeFixture(map[string]string{hostW1: roleWorker})
			Expect(f.kube.CoreV1().Nodes().Delete(context.Background(), hostW1, metav1.DeleteOptions{})).To(Succeed())
			_, err := f.kube.CoreV1().Nodes().Create(context.Background(), &corev1.Node{Name: hostW1}, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			got := f.reconcile(f.newOp(hostW1, nodeSpec(ichorv1alpha1.NodeActionReboot)))
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
			Expect(got.Status.FailureReason).To(ContainSubstring("not Ready"))
			Expect(f.fake(hostW1).rebootCalls).To(BeZero())
		})

		It("refuses a control plane while an etcd member is missing, and reboots it once all are present", func() {
			f := newNodeFixture(threeControlPlanes())
			f.members(hostCP1, hostCP2)

			got := f.reconcile(f.newOp(hostCP1, nodeSpec(ichorv1alpha1.NodeActionReboot)))
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
			Expect(got.Status.FailureReason).To(ContainSubstring("could lose quorum"))
			Expect(f.fake(hostCP1).rebootCalls).To(BeZero())
			Expect(f.unschedulable(hostCP1)).To(BeFalse(), "a refusal leaves no cordon behind")

			f.members(hostCP1, hostCP2, hostCP3)
			op := f.newOp(hostCP1, nodeSpec(ichorv1alpha1.NodeActionReboot))
			Expect(f.reconcile(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))
		})

		It("pauses on a failed Reboot call, uncordons, and never retries by itself", func() {
			f := newNodeFixture(map[string]string{hostW1: roleWorker})
			f.fake(hostW1).rebootErr = errors.New("boom")
			op := f.newOp(hostW1, nodeSpec(ichorv1alpha1.NodeActionReboot))

			got := f.reconcile(op)
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
			Expect(got.Status.FailureReason).To(ContainSubstring("boom"))
			Expect(f.unschedulable(hostW1)).To(BeFalse())

			f.reconcile(op)
			Expect(f.fake(hostW1).rebootCalls).To(Equal(1))
		})

		It("ends the storage evacuation when the operation pauses on a failed Reboot call", func() {
			f := newNodeFixture(map[string]string{hostW1: roleWorker})
			hook, err := storage.NewEvacuationHook(storage.LonghornV1)
			Expect(err).NotTo(HaveOccurred())

			evacuator := &fakeStorageEvacuator{target: &evacuationTarget{hook: hook, timeout: time.Minute, namespace: defaultLonghornNamespace}}
			f.r.Evacuator = evacuator
			f.fake(hostW1).rebootErr = errors.New("boom")
			op := f.newOp(hostW1, nodeSpec(ichorv1alpha1.NodeActionReboot))

			got := f.reconcile(op) // begins the evacuation
			Expect(evacuator.beginCalls).To(Equal(1))
			Expect(evacuator.endCalls).To(BeZero())
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))

			got = f.reconcile(op) // replicas are gone, the reboot call fails
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
			Expect(evacuator.endCalls).To(Equal(1), "a paused operation must not leave the Longhorn node unschedulable")
		})

		It("ends the storage evacuation when the operation pauses on a drain failure (a bare pod)", func() {
			f := newNodeFixture(map[string]string{hostW1: roleWorker})
			hook, err := storage.NewEvacuationHook(storage.LonghornV1)
			Expect(err).NotTo(HaveOccurred())

			evacuator := &fakeStorageEvacuator{target: &evacuationTarget{hook: hook, timeout: time.Minute, namespace: defaultLonghornNamespace}}
			f.r.Evacuator = evacuator
			_, err = f.kube.CoreV1().Pods("default").Create(context.Background(), drainTestPod("bare", hostW1, "", false), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			op := f.newOp(hostW1, nodeSpec(ichorv1alpha1.NodeActionReboot))

			got := f.reconcile(op) // begins the evacuation
			Expect(evacuator.beginCalls).To(Equal(1))
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))

			got = f.reconcile(op) // replicas are gone, the drain refuses the bare pod
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
			Expect(got.Status.FailureReason).To(ContainSubstring("declare no controller"))
			Expect(evacuator.endCalls).To(Equal(1), "a paused operation must not leave the Longhorn node unschedulable")
			Expect(f.unschedulable(hostW1)).To(BeFalse())
		})

		It("does not pause on a drain failure while ending the storage evacuation fails, and retries", func() {
			f := newNodeFixture(map[string]string{hostW1: roleWorker})
			hook, err := storage.NewEvacuationHook(storage.LonghornV1)
			Expect(err).NotTo(HaveOccurred())

			evacuator := &fakeStorageEvacuator{target: &evacuationTarget{hook: hook, timeout: time.Minute, namespace: defaultLonghornNamespace}}
			f.r.Evacuator = evacuator
			_, err = f.kube.CoreV1().Pods("default").Create(context.Background(), drainTestPod("bare", hostW1, "", false), metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())

			op := f.newOp(hostW1, nodeSpec(ichorv1alpha1.NodeActionReboot))
			f.reconcile(op) // begins the evacuation

			evacuator.endErr = errors.New("conflict")
			_, err = reconcileOperation(f.r, op)
			Expect(err).To(MatchError(ContainSubstring("conflict")))
			Expect(fetchOperation(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress), "not paused while the node is still unschedulable")

			evacuator.endErr = nil
			got := f.reconcile(op)
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
			Expect(evacuator.endCalls).To(Equal(2))
		})

		It("is refused while another operation holds the cluster lock", func() {
			f := newNodeFixture(map[string]string{hostW1: roleWorker})
			Expect(clusterlock.New(k8sClient, types.NamespacedName{Namespace: f.cluster.Namespace, Name: f.cluster.Name}, 0).
				Acquire(ctx, "someone-else")).To(Succeed())

			got := f.reconcile(f.newOp(hostW1, nodeSpec(ichorv1alpha1.NodeActionReboot)))
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
			Expect(f.fake(hostW1).rebootCalls).To(BeZero())
		})

		It("is refused while the cluster is being shut down", func() {
			f := newNodeFixture(map[string]string{hostW1: roleWorker})
			sd := &ichorv1alpha1.TalosOperation{}
			sd.GenerateName, sd.Namespace = "sd-", tmNamespace
			sd.Spec.ClusterRef.Name, sd.Spec.Kind = f.cluster.Name, ichorv1alpha1.TalosOperationKindShutdown
			Expect(k8sClient.Create(ctx, sd)).To(Succeed())
			sd.Status.Phase = ichorv1alpha1.TalosOperationPhaseShutDown
			sd.Status.Machines = []ichorv1alpha1.TalosOperationMachineStatus{{Name: "x", Phase: ichorv1alpha1.TalosOperationMachinePhaseInProgress}}
			Expect(k8sClient.Status().Update(ctx, sd)).To(Succeed())

			got := f.reconcile(f.newOp(hostW1, nodeSpec(ichorv1alpha1.NodeActionReboot)))
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
			Expect(f.fake(hostW1).rebootCalls).To(BeZero())
		})
	})

	Describe("shutdown", func() {
		It("asks a worker to shut down with Talos's drain, then rests in ShutDown and completes when it is powered on", func() {
			f := newNodeFixture(map[string]string{hostW1: roleWorker})
			op := f.newOp(hostW1, nodeSpec(ichorv1alpha1.NodeActionShutdown))

			got := f.reconcile(op)
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))
			Expect(f.fake(hostW1).shutdownCalls).To(Equal([]bool{false}))

			f.fake(hostW1).bootIDErr = errors.New("connection refused")
			got = f.reconcile(op)
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseShutDown))

			// The machine stays off; another operation is not blocked by this record's lock.
			lock := clusterlock.New(k8sClient, types.NamespacedName{Namespace: f.cluster.Namespace, Name: f.cluster.Name}, 0)
			Expect(lock.Acquire(ctx, "other")).To(Succeed())
			Expect(lock.Release(ctx, "other")).To(Succeed())

			got = f.reconcile(op)
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseShutDown), "still off")

			f.fake(hostW1).bootIDErr, f.fake(hostW1).bootID = nil, testBootIDAfter
			Expect(f.reconcile(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
			Expect(f.fake(hostW1).shutdownCalls).To(HaveLen(1))
		})

		It("refuses a control plane while an etcd member is missing", func() {
			f := newNodeFixture(threeControlPlanes())
			f.members(hostCP1, hostCP2)

			got := f.reconcile(f.newOp(hostCP3, nodeSpec(ichorv1alpha1.NodeActionShutdown)))
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
			Expect(f.fake(hostCP3).shutdownCalls).To(BeEmpty())
		})

		It("stops the only control plane without a drain, because the API goes away with it", func() {
			f := newNodeFixture(map[string]string{hostCP1: roleControlPlane})
			f.members(hostCP1)

			f.reconcile(f.newOp(hostCP1, nodeSpec(ichorv1alpha1.NodeActionShutdown)))
			Expect(f.fake(hostCP1).shutdownCalls).To(Equal([]bool{true}))
		})
	})

	Describe("restart-service", func() {
		It("restarts an allowed service and waits for a Ready node", func() {
			f := newNodeFixture(map[string]string{hostW1: roleWorker})
			spec := nodeSpec(ichorv1alpha1.NodeActionRestartService)
			spec.Service = serviceKubelet
			op := f.newOp(hostW1, spec)

			Expect(f.reconcile(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))
			Expect(f.fake(hostW1).serviceRestarts).To(Equal([]string{"kubelet"}))

			Expect(f.reconcile(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
			Expect(f.fake(hostW1).serviceRestarts).To(HaveLen(1))
		})

		It("is not complete until the restarted service is Running and healthy", func() {
			f := newNodeFixture(map[string]string{hostW1: roleWorker})
			svc := f.fake(hostW1)
			svc.serviceState, svc.serviceUnhealthy = "Starting", true

			spec := nodeSpec(ichorv1alpha1.NodeActionRestartService)
			spec.Service = serviceKubelet
			op := f.newOp(hostW1, spec)

			Expect(f.reconcile(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))

			got := f.reconcile(op)
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress), "Node Ready is not enough")
			Expect(got.Status.Node.Message).To(ContainSubstring("not Running"))

			svc.serviceState = "Running"
			got = f.reconcile(op)
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))
			Expect(got.Status.Node.Message).To(ContainSubstring("not healthy"))

			svc.serviceUnhealthy = false
			Expect(f.reconcile(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
		})

		It("never restarts a service outside the allowlist, in the API or in the executor", func() {
			f := newNodeFixture(map[string]string{hostW1: roleWorker})

			for _, svc := range []string{"etcd", "apid", "trustd", "machined"} {
				bad := &ichorv1alpha1.TalosOperation{}
				bad.GenerateName, bad.Namespace = nodeOpPrefix, tmNamespace
				bad.Spec.ClusterRef.Name, bad.Spec.Kind = f.cluster.Name, ichorv1alpha1.TalosOperationKindNode
				bad.Spec.Node = &ichorv1alpha1.TalosOperationNodeSpec{
					MachineRef: corev1.LocalObjectReference{Name: f.machines[hostW1].Name},
					Action:     ichorv1alpha1.NodeActionRestartService, Service: svc,
				}
				Expect(k8sClient.Create(ctx, bad)).NotTo(Succeed(), svc)

				n := &nodeRun{
					r: f.r, op: &ichorv1alpha1.TalosOperation{}, m: f.machines[hostW1], talos: f.fake(hostW1),
					spec: &ichorv1alpha1.TalosOperationNodeSpec{Action: ichorv1alpha1.NodeActionRestartService, Service: svc}, dial: ctx,
				}
				_, err := n.beginRestartService(ctx)
				Expect(err).To(MatchError(ContainSubstring("may not be restarted")))
			}

			Expect(f.fake(hostW1).serviceRestarts).To(BeEmpty())
		})

		It("pauses when the restart call fails", func() {
			f := newNodeFixture(map[string]string{hostW1: roleWorker})
			f.fake(hostW1).serviceRestartErr = errors.New("boom")
			spec := nodeSpec(ichorv1alpha1.NodeActionRestartService)
			spec.Service = "cri"

			got := f.reconcile(f.newOp(hostW1, spec))
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
		})
	})

	Describe("etcd-defrag", func() {
		It("defragments a follower and checks the alarms afterwards", func() {
			f := newNodeFixture(threeControlPlanes())
			f.members(hostCP1, hostCP2, hostCP3)
			f.fake(hostCP2).etcdStatus = EtcdStatus{MemberID: 2, LeaderID: 1}

			got := f.reconcile(f.newOp(hostCP2, nodeSpec(ichorv1alpha1.NodeActionEtcdDefrag)))
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
			Expect(f.fake(hostCP2).defragCalls).To(Equal(1))
		})

		It("refuses the leader of a multi-member etcd unless allowLeader is set", func() {
			f := newNodeFixture(threeControlPlanes())
			f.members(hostCP1, hostCP2, hostCP3)
			f.fake(hostCP1).etcdStatus = EtcdStatus{MemberID: 1, LeaderID: 1}

			got := f.reconcile(f.newOp(hostCP1, nodeSpec(ichorv1alpha1.NodeActionEtcdDefrag)))
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
			Expect(got.Status.FailureReason).To(ContainSubstring("leads etcd"))
			Expect(f.fake(hostCP1).defragCalls).To(BeZero())

			spec := nodeSpec(ichorv1alpha1.NodeActionEtcdDefrag)
			spec.AllowLeader = true
			Expect(f.reconcile(f.newOp(hostCP1, spec)).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
			Expect(f.fake(hostCP1).defragCalls).To(Equal(1))
		})

		It("fails when an alarm is still set afterwards", func() {
			f := newNodeFixture(map[string]string{hostCP1: roleControlPlane})
			f.members(hostCP1)
			f.fake(hostCP1).etcdStatus = EtcdStatus{MemberID: 1, LeaderID: 1}
			f.fake(hostCP1).etcdAlarms = []string{"NOSPACE"}

			got := f.reconcile(f.newOp(hostCP1, nodeSpec(ichorv1alpha1.NodeActionEtcdDefrag)))
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
			Expect(got.Status.FailureReason).To(ContainSubstring("NOSPACE"))
		})

		It("refuses a worker", func() {
			f := newNodeFixture(map[string]string{hostW1: roleWorker})

			got := f.reconcile(f.newOp(hostW1, nodeSpec(ichorv1alpha1.NodeActionEtcdDefrag)))
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
			Expect(f.fake(hostW1).defragCalls).To(BeZero())
		})
	})

	Describe("etcd-forfeit-leadership", func() {
		It("hands leadership over and verifies a new leader", func() {
			f := newNodeFixture(threeControlPlanes())
			f.members(hostCP1, hostCP2, hostCP3)
			fk := f.fake(hostCP1)
			fk.etcdStatus = EtcdStatus{MemberID: 1, LeaderID: 1}
			fk.forfeitHook = func() { fk.etcdStatus.LeaderID = 2 }
			op := f.newOp(hostCP1, nodeSpec(ichorv1alpha1.NodeActionEtcdForfeitLeadership))

			Expect(f.reconcile(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))
			Expect(fk.forfeitCalls).To(Equal(1))

			Expect(f.reconcile(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))
			Expect(fk.forfeitCalls).To(Equal(1))
		})

		It("waits while the same member still leads", func() {
			f := newNodeFixture(threeControlPlanes())
			f.members(hostCP1, hostCP2, hostCP3)
			f.fake(hostCP1).etcdStatus = EtcdStatus{MemberID: 1, LeaderID: 1}
			op := f.newOp(hostCP1, nodeSpec(ichorv1alpha1.NodeActionEtcdForfeitLeadership))

			f.reconcile(op)
			Expect(f.reconcile(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))
		})

		It("refuses a member that does not lead, and a single-member etcd", func() {
			f := newNodeFixture(threeControlPlanes())
			f.members(hostCP1, hostCP2, hostCP3)
			f.fake(hostCP2).etcdStatus = EtcdStatus{MemberID: 2, LeaderID: 1}

			got := f.reconcile(f.newOp(hostCP2, nodeSpec(ichorv1alpha1.NodeActionEtcdForfeitLeadership)))
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
			Expect(f.fake(hostCP2).forfeitCalls).To(BeZero())

			single := newNodeFixture(map[string]string{hostCP1: roleControlPlane})
			single.members(hostCP1)
			single.fake(hostCP1).etcdStatus = EtcdStatus{MemberID: 1, LeaderID: 1}

			got = single.reconcile(single.newOp(hostCP1, nodeSpec(ichorv1alpha1.NodeActionEtcdForfeitLeadership)))
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))
			Expect(single.fake(hostCP1).forfeitCalls).To(BeZero())
		})
	})

	Describe("reset", func() {
		It("is not creatable without the machine's name as confirmation", func() {
			f := newNodeFixture(map[string]string{hostW1: roleWorker})
			spec := nodeSpec(ichorv1alpha1.NodeActionReset)
			spec.MachineRef.Name = f.machines[hostW1].Name
			spec.Confirm = "something-else"

			op := &ichorv1alpha1.TalosOperation{}
			op.GenerateName, op.Namespace = nodeOpPrefix, tmNamespace
			op.Spec.ClusterRef.Name, op.Spec.Kind, op.Spec.Node = f.cluster.Name, ichorv1alpha1.TalosOperationKindNode, &spec
			Expect(k8sClient.Create(ctx, op)).NotTo(Succeed())
		})

		It("hands a worker to the removal flow with the reset opt-in and completes when the machine is gone", func() {
			f := newNodeFixture(map[string]string{hostW1: roleWorker})
			spec := nodeSpec(ichorv1alpha1.NodeActionReset)
			spec.Confirm = f.machines[hostW1].Name
			op := f.newOp(hostW1, spec)

			got := f.reconcile(op)
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))
			Expect(got.Status.Node.Step).To(Equal(ichorv1alpha1.NodeStepRemoving))

			var m ichorv1alpha1.TalosMachine
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: tmNamespace, Name: f.machines[hostW1].Name}, &m)).To(Succeed())
			Expect(resetRequested(&m)).To(BeTrue())
			Expect(m.DeletionTimestamp.IsZero()).To(BeFalse(), "deleting it is what starts the removal flow")
			Expect(f.fake(hostW1).resetCalls).To(BeZero(), "the removal flow resets, not this operation")

			// The removal flow takes the cluster lock; the operation holds none while it waits.
			lock := clusterlock.New(k8sClient, types.NamespacedName{Namespace: f.cluster.Namespace, Name: f.cluster.Name}, 0)
			Expect(lock.Acquire(ctx, controllerIdentity+"-removal")).To(Succeed())
			Expect(f.reconcile(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseInProgress))
			Expect(lock.Release(ctx, controllerIdentity+"-removal")).To(Succeed())

			controllerutil.RemoveFinalizer(&m, machineRemovalFinalizer)
			Expect(k8sClient.Update(ctx, &m)).To(Succeed())
			Eventually(func() bool {
				return f.reconcile(op).Status.Phase == ichorv1alpha1.TalosOperationPhaseCompleted
			}, 5*time.Second).Should(BeTrue())
		})

		It("completes when discovery re-creates a pending machine of the same name, and never deletes it", func() {
			f := newNodeFixture(map[string]string{hostW1: roleWorker})
			spec := nodeSpec(ichorv1alpha1.NodeActionReset)
			spec.Confirm = f.machines[hostW1].Name
			op := f.newOp(hostW1, spec)
			f.reconcile(op)

			name := types.NamespacedName{Namespace: tmNamespace, Name: f.machines[hostW1].Name}

			var old ichorv1alpha1.TalosMachine
			Expect(k8sClient.Get(ctx, name, &old)).To(Succeed())
			Expect(fetchOperation(op).Status.Node.MachineUID).To(Equal(old.UID), "the machine the reset started on is recorded by UID")

			// The removal flow is done and, 15 s later at the latest, discovery recreated the machine
			// (a pending one, with the same name: the node's UUID) before the operation looked again.
			controllerutil.RemoveFinalizer(&old, machineRemovalFinalizer)
			Expect(k8sClient.Update(ctx, &old)).To(Succeed())
			Eventually(func() bool { return apierrors.IsNotFound(k8sClient.Get(ctx, name, &ichorv1alpha1.TalosMachine{})) }, 5*time.Second).Should(BeTrue())

			pending := &ichorv1alpha1.TalosMachine{}
			pending.Namespace, pending.Name = name.Namespace, name.Name
			pending.Spec = ichorv1alpha1.TalosMachineSpec{
				ClusterRef: corev1.LocalObjectReference{Name: f.cluster.Name},
				Identity:   ichorv1alpha1.TalosMachineIdentity{UUID: "uuid-" + hostW1},
				Endpoint:   "10.0.0.31:50000", ApprovalState: ichorv1alpha1.MachineApprovalStatePending,
			}
			Expect(k8sClient.Create(ctx, pending)).To(Succeed())

			Expect(f.reconcile(op).Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhaseCompleted))

			var got ichorv1alpha1.TalosMachine
			Expect(k8sClient.Get(ctx, name, &got)).To(Succeed())
			Expect(got.UID).To(Equal(pending.UID))
			Expect(got.DeletionTimestamp.IsZero()).To(BeTrue(), "the new pending machine is not the operation's to delete")
		})

		It("passes the wipe mode on to the removal flow", func() {
			f := newNodeFixture(map[string]string{hostW1: roleWorker})
			spec := nodeSpec(ichorv1alpha1.NodeActionReset)
			spec.Confirm = f.machines[hostW1].Name
			spec.WipeMode = ichorv1alpha1.WipeModeSystemDisk

			f.reconcile(f.newOp(hostW1, spec))

			var m ichorv1alpha1.TalosMachine
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: tmNamespace, Name: f.machines[hostW1].Name}, &m)).To(Succeed())
			Expect(m.Annotations).To(HaveKeyWithValue(resetWipeModeAnnotation, ichorv1alpha1.WipeModeSystemDisk))
		})

		It("is not creatable with a wipe mode on any other action", func() {
			f := newNodeFixture(map[string]string{hostW1: roleWorker})
			spec := nodeSpec(ichorv1alpha1.NodeActionReboot)
			spec.MachineRef.Name = f.machines[hostW1].Name
			spec.WipeMode = ichorv1alpha1.WipeModeAll

			op := &ichorv1alpha1.TalosOperation{}
			op.GenerateName, op.Namespace = nodeOpPrefix, tmNamespace
			op.Spec.ClusterRef.Name, op.Spec.Kind, op.Spec.Node = f.cluster.Name, ichorv1alpha1.TalosOperationKindNode, &spec
			Expect(k8sClient.Create(ctx, op)).NotTo(Succeed())
		})

		It("refuses to reset a control plane the cluster cannot do without", func() {
			f := newNodeFixture(threeControlPlanes())
			f.cluster.Spec.ControlPlane.Count = 3
			Expect(k8sClient.Update(ctx, f.cluster)).To(Succeed())
			spec := nodeSpec(ichorv1alpha1.NodeActionReset)
			spec.Confirm = f.machines[hostCP1].Name

			got := f.reconcile(f.newOp(hostCP1, spec))
			Expect(got.Status.Phase).To(Equal(ichorv1alpha1.TalosOperationPhasePaused))

			var m ichorv1alpha1.TalosMachine
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: tmNamespace, Name: f.machines[hostCP1].Name}, &m)).To(Succeed())
			Expect(m.DeletionTimestamp.IsZero()).To(BeTrue())
			Expect(resetRequested(&m)).To(BeFalse())
		})
	})
})
