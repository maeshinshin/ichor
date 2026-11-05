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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

// recordingUpdateClient wraps a real client.Client and records what
// FieldValidation the last Update (and, since PatchService also creates
// objects, Create) call actually resolved to, so a test can check what a
// handler requests without needing an unknown field on the wire to see a
// difference.
type recordingUpdateClient struct {
	client.Client
	lastUpdateOptions client.UpdateOptions
	lastCreateOptions client.CreateOptions
}

func (c *recordingUpdateClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	c.lastUpdateOptions = *(&client.UpdateOptions{}).ApplyOptions(opts)
	return c.Client.Update(ctx, obj, opts...)
}

func (c *recordingUpdateClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	c.lastCreateOptions = *(&client.CreateOptions{}).ApplyOptions(opts)
	return c.Client.Create(ctx, obj, opts...)
}

// architecture.md, "Typed configuration": no admission webhook rejects an
// unknown field on a write; instead "Ichor's own clients ask for strict
// field validation ... so the CLI and the UI backend report the field
// loudly," because the API server's default is to silently prune one (the
// object is still persisted, just without it, logged only as a
// server-side warning the caller never sees).
var _ = Describe("Writes request strict field validation", func() {
	It("ApproveMachine's write requests FieldValidationStrict", func() {
		cluster := newTestCluster("does-not-exist")
		m := newTestMachine(cluster.Name, strongTestIdentity("uuid-strict-approve"), "", ichorv1alpha1.MachineApprovalStatePending)

		rec := &recordingUpdateClient{Client: k8sClient}
		s := &Server{Client: rec}
		_, err := s.ApproveMachine(ctx, connect.NewRequest(&pb.ApproveMachineRequest{
			Machine: objectRef(m.Namespace, m.Name), Role: pb.MachineRole_MACHINE_ROLE_WORKER,
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.lastUpdateOptions.FieldValidation).To(Equal("Strict"))
	})

	It("RejectMachine's write requests FieldValidationStrict", func() {
		cluster := newTestCluster("does-not-exist")
		m := newTestMachine(cluster.Name, strongTestIdentity("uuid-strict-reject"), "", ichorv1alpha1.MachineApprovalStatePending)

		rec := &recordingUpdateClient{Client: k8sClient}
		s := &Server{Client: rec}
		_, err := s.RejectMachine(ctx, connect.NewRequest(&pb.RejectMachineRequest{
			Machine: objectRef(m.Namespace, m.Name),
		}))
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.lastUpdateOptions.FieldValidation).To(Equal("Strict"))
	})

	// The empirical half: against this suite's own real API server (the
	// CRDs under config/crd/bases), the identical write with an unknown
	// field succeeds and silently drops it without strict validation, but
	// fails loudly naming the field with it -- the exact contrast the
	// design calls out, confirmed here rather than taken on faith.
	It("prunes an unknown field silently by default, and rejects it under strict validation", func() {
		cluster := newTestCluster("does-not-exist")

		lenient := newTestMachine(cluster.Name, strongTestIdentity("uuid-unknown-lenient"), "", ichorv1alpha1.MachineApprovalStatePending)
		Expect(k8sClient.Patch(ctx, lenient,
			client.RawPatch(types.MergePatchType, []byte(`{"spec":{"bogusField":"should not persist"}}`)),
		)).To(Succeed(), "the default is to accept the write and prune the unknown field, not reject it")

		strict := newTestMachine(cluster.Name, strongTestIdentity("uuid-unknown-strict"), "", ichorv1alpha1.MachineApprovalStatePending)
		err := k8sClient.Patch(ctx, strict,
			client.RawPatch(types.MergePatchType, []byte(`{"spec":{"bogusField":"should not persist"}}`)),
			strictFieldValidation,
		)
		Expect(err).To(HaveOccurred(), "strict validation must turn the same write into a loud rejection")
		Expect(err.Error()).To(ContainSubstring("bogusField"))
	})
})
