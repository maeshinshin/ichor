package cilium

import (
	"bytes"
	"strings"
	"testing"

	sigsyaml "sigs.k8s.io/yaml"

	"github.com/maeshinshin/ichor/pkg/cni"
)

// The agents read kube-proxy-replacement only at start, so a kube-proxy switch must change the agent
// DaemonSet's pod template or nothing restarts them. With the KubePrism address a user may set on an
// enabled cluster (ADR 0159) the two modes would otherwise render the same DaemonSet.
func TestAgentTemplateDiffersByKubeProxyMode(t *testing.T) {
	t.Parallel()

	user := map[string]any{"k8sServiceHost": "localhost", "k8sServicePort": 7445}

	render := func(mode cni.KubeProxyMode, user map[string]any) []byte {
		vals, err := EffectiveValues(user, mode, "", 0)
		if err != nil {
			t.Fatal(err)
		}

		manifest, err := renderChart(vals)
		if err != nil {
			t.Fatal(err)
		}

		return renderedAgentDS(t, manifest)
	}

	if bytes.Equal(render(cni.KubeProxyEnabled, user), render(cni.KubeProxyReplaced, nil)) {
		t.Fatal("the agent DaemonSet is identical for enabled and replaced: the agents would not restart on a switch")
	}
}

// renderedAgentDS is the cilium agent DaemonSet document of manifest.
func renderedAgentDS(t *testing.T, manifest []byte) []byte {
	t.Helper()

	for doc := range strings.SplitSeq(string(manifest), "\n---\n") {
		var obj struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		}

		if sigsyaml.Unmarshal([]byte(doc), &obj) == nil && obj.Kind == "DaemonSet" && obj.Metadata.Name == "cilium" {
			return []byte(doc)
		}
	}

	t.Fatal("no cilium DaemonSet in the rendered manifest")

	return nil
}
