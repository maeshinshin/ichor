package cilium

import (
	"strings"
	"testing"

	sigsyaml "sigs.k8s.io/yaml"

	"github.com/maeshinshin/ichor/pkg/cni"
)

// The operator's replica count follows the control-plane count (values.go, operator): one control
// plane runs one replica, any other count keeps the chart's default of two.
func TestOperatorReplicasFollowTheControlPlaneCount(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		controlPlanes, want int
	}{
		{0, 2}, // unknown: the chart default
		{1, 1},
		{2, 2},
		{3, 2},
	} {
		manifest, err := New().WithControlPlanes(tc.controlPlanes).BootstrapManifest(cni.KubeProxyEnabled)
		if err != nil {
			t.Fatalf("control planes %d: %v", tc.controlPlanes, err)
		}

		if got := operatorReplicas(t, manifest); got != tc.want {
			t.Errorf("control planes %d: operator replicas = %d, want %d", tc.controlPlanes, got, tc.want)
		}
	}
}

// operatorReplicas reads spec.replicas of the cilium-operator Deployment in a rendered manifest.
func operatorReplicas(t *testing.T, manifest []byte) int {
	t.Helper()

	for doc := range strings.SplitSeq(string(manifest), "\n---\n") {
		var obj struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Replicas int `json:"replicas"`
			} `json:"spec"`
		}

		if err := sigsyaml.Unmarshal([]byte(doc), &obj); err != nil {
			continue
		}

		if obj.Kind == "Deployment" && obj.Metadata.Name == "cilium-operator" {
			return obj.Spec.Replicas
		}
	}

	t.Fatal("no cilium-operator Deployment in the rendered manifest")

	return 0
}
