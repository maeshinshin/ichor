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

package v1alpha1

import (
	"bytes"
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
)

// samplesDir is config/samples: not a resource of config/default (CONTRIBUTING.md's chart
// warning has a cousin here -- nothing applies this directory for real), so it exists purely as
// copy-pasteable documentation. It is most load-bearing for TalosConfigPatch.spec.documents[],
// the one API in this project hardest to guess the shape of from the CRD schema alone, which is
// exactly why a kubebuilder-scaffolded "# TODO(user): Add fields here" placeholder here is worse
// than no sample at all: it fails for the first person who copies it, and the failure looks like
// their own mistake. This suite is what keeps every sample honest against the types as they
// change, by actually creating each one.
const samplesDir = "../../config/samples"

// sampleFiles mirrors config/samples/kustomization.yaml's own resource list -- the one file in
// that directory this suite does not expect to be a CR, kustomization.yaml itself, is the one
// name missing from here on purpose.
var sampleFiles = []string{
	"ichor_v1alpha1_taloscluster.yaml",
	"ichor_v1alpha1_talosmachine.yaml",
	"ichor_v1alpha1_talosconfigpatch.yaml",
	"ichor_v1alpha1_talosstorage.yaml",
	"ichor_v1alpha1_talosoperation.yaml",
	"ichor_v1alpha1_ichoruser.yaml",
}

var _ = Describe("config/samples", func() {
	for _, name := range sampleFiles {
		It("applies "+name+" as a minimal, valid example", func() {
			data, err := os.ReadFile(filepath.Join(samplesDir, name))
			Expect(err).NotTo(HaveOccurred())

			var raw map[string]any
			Expect(k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096).Decode(&raw)).To(Succeed())

			obj := &unstructured.Unstructured{Object: raw}
			if obj.GetNamespace() == "" {
				obj.SetNamespace(testNamespace)
			}

			Expect(k8sClient.Create(context.Background(), obj)).To(Succeed(),
				"config/samples/%s must apply as committed -- it is documentation, not a placeholder "+
					"for the reader to fill in", name)
		})
	}
})
