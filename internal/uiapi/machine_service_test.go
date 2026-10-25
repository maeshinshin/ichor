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
	"reflect"

	"connectrpc.com/connect"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/types"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

func fetchTestMachine(m *ichorv1alpha1.TalosMachine) *ichorv1alpha1.TalosMachine {
	var got ichorv1alpha1.TalosMachine
	Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: m.Namespace, Name: m.Name}, &got)).To(Succeed())

	return &got
}

var _ = Describe("MachineService", func() {
	It("lists machines, pending ones included, optionally filtered by cluster", func() {
		cluster := newTestCluster("does-not-exist")
		other := newTestCluster("does-not-exist-2")
		pending := newTestMachine(cluster.Name, strongTestIdentity("uuid-1"), "", false)
		_ = newTestMachine(other.Name, strongTestIdentity("uuid-2"), "", false)

		s := &Server{Client: k8sClient}

		all, err := s.ListMachines(ctx, connect.NewRequest(&pb.ListMachinesRequest{}))
		Expect(err).NotTo(HaveOccurred())
		Expect(len(all.Msg.GetMachines())).To(BeNumerically(">=", 2))

		filtered, err := s.ListMachines(ctx, connect.NewRequest(&pb.ListMachinesRequest{
			Cluster: objectRef(cluster.Namespace, cluster.Name),
		}))
		Expect(err).NotTo(HaveOccurred())

		names := make([]string, 0, len(filtered.Msg.GetMachines()))
		for _, m := range filtered.Msg.GetMachines() {
			names = append(names, m.GetRef().GetName())
		}

		Expect(names).To(ContainElement(pending.Name))
		Expect(names).To(HaveLen(1))
	})

	It("gets one machine and reports its pending approval state", func() {
		cluster := newTestCluster("does-not-exist")
		m := newTestMachine(cluster.Name, strongTestIdentity("uuid-get"), "", false)

		s := &Server{Client: k8sClient}
		resp, err := s.GetMachine(ctx, connect.NewRequest(&pb.GetMachineRequest{Machine: objectRef(m.Namespace, m.Name)}))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Msg.GetMachine().GetApproved()).To(BeFalse())
		Expect(resp.Msg.GetMachine().GetRole()).To(Equal(pb.MachineRole_MACHINE_ROLE_UNSPECIFIED))
	})

	Describe("ApproveMachine", func() {
		It("approves a strong-identity pending machine and records who/when/what", func() {
			cluster := newTestCluster("does-not-exist")
			m := newTestMachine(cluster.Name, strongTestIdentity("uuid-approve"), "", false)

			s := &Server{Client: k8sClient}
			resp, err := s.ApproveMachine(ctx, connect.NewRequest(&pb.ApproveMachineRequest{
				Machine: objectRef(m.Namespace, m.Name), Role: pb.MachineRole_MACHINE_ROLE_WORKER, Hostname: "node-1",
			}))
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Msg.GetMachine().GetApproved()).To(BeTrue())
			Expect(resp.Msg.GetMachine().GetRole()).To(Equal(pb.MachineRole_MACHINE_ROLE_WORKER))

			audit := resp.Msg.GetAudit()
			Expect(audit.GetAction()).To(Equal("approve"))
			Expect(audit.GetTarget().GetName()).To(Equal(m.Name))
			Expect(audit.GetActor()).NotTo(BeEmpty())
			Expect(audit.GetAt()).NotTo(BeNil())

			got := fetchTestMachine(m)
			Expect(got.Spec.Approved).To(BeTrue())
			Expect(got.Spec.Role).To(Equal(roleWorker))
			Expect(got.Spec.Hostname).To(Equal("node-1"))
		})

		It("condition 1: refuses a weak identity", func() {
			cluster := newTestCluster("does-not-exist")
			weak := ichorv1alpha1.TalosMachineIdentity{UUID: "uuid-weak"} // no serial, no MAC
			m := newTestMachine(cluster.Name, weak, "", false)

			s := &Server{Client: k8sClient}
			_, err := s.ApproveMachine(ctx, connect.NewRequest(&pb.ApproveMachineRequest{
				Machine: objectRef(m.Namespace, m.Name), Role: pb.MachineRole_MACHINE_ROLE_WORKER,
			}))
			Expect(err).To(HaveOccurred())
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeFailedPrecondition))

			got := fetchTestMachine(m)
			Expect(got.Spec.Approved).To(BeFalse(), "a refused approval must not mutate the machine")
		})

		It("refuses MACHINE_ROLE_UNSPECIFIED rather than silently storing an empty role", func() {
			cluster := newTestCluster("does-not-exist")
			m := newTestMachine(cluster.Name, strongTestIdentity("uuid-norole"), "", false)

			s := &Server{Client: k8sClient}
			_, err := s.ApproveMachine(ctx, connect.NewRequest(&pb.ApproveMachineRequest{
				Machine: objectRef(m.Namespace, m.Name),
			}))
			Expect(err).To(HaveOccurred())
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeInvalidArgument))

			Expect(fetchTestMachine(m).Spec.Approved).To(BeFalse())
		})

		It("condition 3: refuses a second approval of an already-approved machine without mutating it", func() {
			cluster := newTestCluster("does-not-exist")
			m := newTestMachine(cluster.Name, strongTestIdentity("uuid-reapprove"), roleWorker, true)

			wantSpec := m.Spec.DeepCopy()

			s := &Server{Client: k8sClient}
			_, err := s.ApproveMachine(ctx, connect.NewRequest(&pb.ApproveMachineRequest{
				Machine: objectRef(m.Namespace, m.Name), Role: pb.MachineRole_MACHINE_ROLE_CONTROL_PLANE, Hostname: "renamed",
			}))
			Expect(err).To(HaveOccurred())
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeFailedPrecondition))

			got := fetchTestMachine(m)
			Expect(reflect.DeepEqual(&got.Spec, wantSpec)).To(BeTrue(),
				"re-approving an already-approved machine must cause no reconfiguration at all")
		})
	})

	Describe("RejectMachine", func() {
		It("records a reject audit for a pending machine without deleting or otherwise changing it", func() {
			cluster := newTestCluster("does-not-exist")
			m := newTestMachine(cluster.Name, strongTestIdentity("uuid-reject"), "", false)
			wantSpec := m.Spec.DeepCopy()

			s := &Server{Client: k8sClient}
			resp, err := s.RejectMachine(ctx, connect.NewRequest(&pb.RejectMachineRequest{
				Machine: objectRef(m.Namespace, m.Name), Reason: "wrong hardware",
			}))
			Expect(err).NotTo(HaveOccurred())

			audit := resp.Msg.GetAudit()
			Expect(audit.GetAction()).To(Equal("reject"))
			Expect(audit.GetReason()).To(Equal("wrong hardware"))

			got := fetchTestMachine(m)
			Expect(reflect.DeepEqual(&got.Spec, wantSpec)).To(BeTrue(), "reject must not mutate the machine")

			// condition 4: the machine must still exist under its own identity, so a
			// future discovery pass finds it unchanged rather than recreating it as
			// a fresh pending machine.
			Expect(got.Spec.Identity.UUID).To(Equal(m.Spec.Identity.UUID))
			Expect(got.Spec.Approved).To(BeFalse())
		})

		It("refuses to reject an already-approved machine", func() {
			cluster := newTestCluster("does-not-exist")
			m := newTestMachine(cluster.Name, strongTestIdentity("uuid-reject-approved"), roleWorker, true)

			s := &Server{Client: k8sClient}
			_, err := s.RejectMachine(ctx, connect.NewRequest(&pb.RejectMachineRequest{
				Machine: objectRef(m.Namespace, m.Name), Reason: "changed my mind",
			}))
			Expect(err).To(HaveOccurred())
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeFailedPrecondition))
			Expect(fetchTestMachine(m).Spec.Approved).To(BeTrue())
		})
	})
})
