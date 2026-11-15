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
	"reflect"

	"connectrpc.com/connect"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/types"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
)

var _ = Describe("PatchService with the config the UI's field search builds", func() {
	// The UI's tests (ui/src/lib/fieldSearch.test.ts) build a spec.config from the settings chosen in the search,
	// two documents of one kind among them; this reads the same file and sends each config over the wire to the API
	// server, then has the controller's conversion turn what is stored into the Talos documents.
	It("stores the spec.config of each fixture, returns it unchanged, and converts it to the documents the fixture lists", func() {
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
		Expect(len(cases)).To(BeNumerically(">=", 5))

		cluster := newTestCluster("does-not-exist")
		s := &Server{Client: k8sClient}

		for i, c := range cases {
			name := fmt.Sprintf("fieldsearch-%d", i)

			_, err := s.CreatePatch(ctx, connect.NewRequest(&pb.CreatePatchRequest{Patch: &pb.Patch{
				Ref:     objectRef(testNamespace, name),
				Cluster: objectRef(testNamespace, cluster.Name),
				Layer:   pb.ConfigLayer_CONFIG_LAYER_CLUSTER,
				Config:  string(c.Config),
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
			Expect(got.Msg.GetPatch().GetConfig()).To(MatchJSON(c.Config), name)

			// The controller applies what spec.config converts to: a document for each entry of a collection.
			docs, err := stored.Spec.EffectiveDocuments(true)
			Expect(err).NotTo(HaveOccurred(), name)
			Expect(docs).To(HaveLen(len(c.Documents)), name)

			for _, want := range c.Documents {
				var found bool

				for _, d := range docs {
					key, _ := ichorv1alpha1.TalosConfigPatchDocumentFieldKey(d.Kind)
					full, err := json.Marshal(d)
					Expect(err).NotTo(HaveOccurred())

					var generic map[string]json.RawMessage
					Expect(json.Unmarshal(full, &generic)).To(Succeed())

					if d.Kind == want.Kind && jsonEqual(generic[key], want.Document) {
						found = true
					}
				}

				Expect(found).To(BeTrue(), "%s: no %s document %s", name, want.Kind, want.Document)
			}
		}
	})
})

var _ = Describe("PatchService opening and saving a patch", func() {
	// The editor has no kind to pick: it reads config, and sends config back.
	It("saves a patch it read back, config only, and the config is unchanged", func() {
		cluster := newTestCluster("does-not-exist")
		s := &Server{Client: k8sClient}
		config := `{"machine":{"kernel":{"modules":{"br_netfilter":{},"nbd":{}}},"sysctls":{"net.core.somaxconn":"1024"}}}`

		_, err := s.CreatePatch(ctx, connect.NewRequest(&pb.CreatePatchRequest{Patch: &pb.Patch{
			Ref: objectRef(testNamespace, "open-and-save"), Cluster: objectRef(testNamespace, cluster.Name),
			Layer: pb.ConfigLayer_CONFIG_LAYER_CLUSTER, Config: config,
		}}))
		Expect(err).NotTo(HaveOccurred())

		got, err := s.GetPatch(ctx, connect.NewRequest(&pb.GetPatchRequest{Patch: objectRef(testNamespace, "open-and-save")}))
		Expect(err).NotTo(HaveOccurred())

		opened := got.Msg.GetPatch()
		Expect(opened.GetDocuments()).To(BeEmpty()) //nolint:staticcheck // never set
		opened.Priority = 7

		_, err = s.UpdatePatch(ctx, connect.NewRequest(&pb.UpdatePatchRequest{Patch: opened}))
		Expect(err).NotTo(HaveOccurred())

		again, err := s.GetPatch(ctx, connect.NewRequest(&pb.GetPatchRequest{Patch: objectRef(testNamespace, "open-and-save")}))
		Expect(err).NotTo(HaveOccurred())
		Expect(again.Msg.GetPatch().GetPriority()).To(Equal(int32(7)))
		Expect(again.Msg.GetPatch().GetConfig()).To(MatchJSON(config))
	})
})

// jsonEqual compares two JSON texts by value.
func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}

	return reflect.DeepEqual(x, y)
}
