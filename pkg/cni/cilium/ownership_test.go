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

package cilium

import (
	"strings"
	"testing"

	sigsyaml "sigs.k8s.io/yaml"

	"github.com/maeshinshin/ichor/pkg/cni"
)

// Every bootstrap object carries Helm's release-ownership metadata for release
// cilium in kube-system, so the Job's Helm release adopts it cleanly.
func TestBootstrapManifest_ObjectsCarryHelmOwnership(t *testing.T) {
	out, err := New().BootstrapManifest(cni.KubeProxyEnabled)
	if err != nil {
		t.Fatal(err)
	}

	n := 0

	for doc := range strings.SplitSeq(string(out), "\n---\n") {
		doc = strings.TrimPrefix(doc, "---\n")
		if strings.TrimSpace(doc) == "" {
			continue
		}

		var o struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Name        string            `json:"name"`
				Annotations map[string]string `json:"annotations"`
				Labels      map[string]string `json:"labels"`
			} `json:"metadata"`
		}
		if err := sigsyaml.Unmarshal([]byte(doc), &o); err != nil {
			t.Fatal(err)
		}

		n++

		if o.Metadata.Annotations["meta.helm.sh/release-name"] != ReleaseName ||
			o.Metadata.Annotations["meta.helm.sh/release-namespace"] != ReleaseNamespace ||
			o.Metadata.Labels["app.kubernetes.io/managed-by"] != "Helm" {
			t.Errorf("%s %q lacks the Helm ownership metadata: annotations %v, labels %v",
				o.Kind, o.Metadata.Name, o.Metadata.Annotations, o.Metadata.Labels)
		}
	}

	if n == 0 {
		t.Fatal("no objects rendered")
	}
}
