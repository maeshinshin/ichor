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
	"os"

	"connectrpc.com/connect"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

var _ = Describe("PatchService with the documents the UI's field search builds", func() {
	// The UI's tests (ui/src/lib/fieldSearch.test.ts) build each document from its list of chosen
	// fields; this reads the same file and sends the documents to the API server.
	It("stores every field of every fixture document and returns it unchanged", func() {
		raw, err := os.ReadFile("../../ui/src/lib/fieldSearch.fixtures.json")
		Expect(err).NotTo(HaveOccurred())

		var cases []struct {
			Kind     string          `json:"kind"`
			Document json.RawMessage `json:"document"`
		}
		Expect(json.Unmarshal(raw, &cases)).To(Succeed())
		Expect(len(cases)).To(BeNumerically(">=", 3))

		cluster := newTestCluster("does-not-exist")
		s := &Server{Client: k8sClient}

		for i, c := range cases {
			name := fmt.Sprintf("fieldsearch-%d", i)
			_, err := s.CreatePatch(ctx, connect.NewRequest(&pb.CreatePatchRequest{Patch: &pb.Patch{
				Ref:       objectRef(testNamespace, name),
				Cluster:   objectRef(testNamespace, cluster.Name),
				Layer:     pb.ConfigLayer_CONFIG_LAYER_CLUSTER,
				Documents: []*pb.PatchDocument{{Kind: c.Kind, Document: string(c.Document)}},
			}}))
			Expect(err).NotTo(HaveOccurred(), c.Kind)

			got, err := s.GetPatch(ctx, connect.NewRequest(&pb.GetPatchRequest{Patch: objectRef(testNamespace, name)}))
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Msg.GetPatch().GetDocuments()).To(HaveLen(1))
			Expect(got.Msg.GetPatch().GetDocuments()[0].GetKind()).To(Equal(c.Kind))
			// A field the schema did not know is dropped by the API server, so equal JSON means it kept every one.
			Expect(got.Msg.GetPatch().GetDocuments()[0].GetDocument()).To(MatchJSON(c.Document), c.Kind)
		}
	})
})
