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

	"k8s.io/apimachinery/pkg/types"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

var _ = Describe("PatchService with the documents the UI's field search builds", func() {
	// The UI's tests (ui/src/lib/fieldSearch.test.ts) build the documents from the settings chosen in the search
	// and show each setting at its path in the expected spec.config; this reads the same file and sends the
	// documents to the API server, whose conversion has to store that spec.config. The wire carries documents.
	It("stores the spec.config of each fixture and returns its documents unchanged", func() {
		raw, err := os.ReadFile("../../ui/src/lib/fieldSearch.fixtures.json")
		Expect(err).NotTo(HaveOccurred())

		var cases []struct {
			Documents []struct {
				Kind     string          `json:"kind"`
				Document json.RawMessage `json:"document"`
			} `json:"documents"`
			Config json.RawMessage `json:"config"`
		}
		Expect(json.Unmarshal(raw, &cases)).To(Succeed())
		Expect(len(cases)).To(BeNumerically(">=", 3))

		cluster := newTestCluster("does-not-exist")
		s := &Server{Client: k8sClient}

		for i, c := range cases {
			name := fmt.Sprintf("fieldsearch-%d", i)

			var docs []*pb.PatchDocument
			for _, d := range c.Documents {
				docs = append(docs, &pb.PatchDocument{Kind: d.Kind, Document: string(d.Document)})
			}

			_, err := s.CreatePatch(ctx, connect.NewRequest(&pb.CreatePatchRequest{Patch: &pb.Patch{
				Ref:       objectRef(testNamespace, name),
				Cluster:   objectRef(testNamespace, cluster.Name),
				Layer:     pb.ConfigLayer_CONFIG_LAYER_CLUSTER,
				Documents: docs,
			}}))
			Expect(err).NotTo(HaveOccurred(), name)

			// What the API server holds is spec.config, the one place a patch carries Talos configuration. A
			// setting the schema did not know is dropped by the API server, so equal JSON means it kept every one.
			var stored ichorv1alpha1.TalosConfigPatch
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: name}, &stored)).To(Succeed())

			config, err := json.Marshal(stored.Spec.Config)
			Expect(err).NotTo(HaveOccurred())
			Expect(config).To(MatchJSON(c.Config), name)

			got, err := s.GetPatch(ctx, connect.NewRequest(&pb.GetPatchRequest{Patch: objectRef(testNamespace, name)}))
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Msg.GetPatch().GetDocuments()).To(HaveLen(len(c.Documents)), name)

			// The documents come back as the editor sent them, whatever the order spec.config converts them in.
			back := map[string]string{}
			for _, d := range got.Msg.GetPatch().GetDocuments() {
				back[d.GetKind()] = d.GetDocument()
			}

			for _, d := range c.Documents {
				Expect(back).To(HaveKey(d.Kind), name)
				Expect(back[d.Kind]).To(MatchJSON(d.Document), d.Kind)
			}
		}
	})
})
