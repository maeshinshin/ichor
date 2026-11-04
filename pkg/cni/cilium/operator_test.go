package cilium

import (
	"strings"
	"testing"

	sigsyaml "sigs.k8s.io/yaml"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
)

// renderedOperatorReplicas is spec.replicas of the cilium-operator Deployment the chart renders for
// cluster when it has nodes nodes.
func renderedOperatorReplicas(t *testing.T, cluster *ichorv1alpha1.TalosCluster, nodes int) int {
	t.Helper()

	vals, err := EffectiveValuesForNodes(cluster, cni.KubeProxyEnabled, nodes)
	if err != nil {
		t.Fatal(err)
	}

	manifest, err := renderChart(vals)
	if err != nil {
		t.Fatal(err)
	}

	return operatorReplicas(t, manifest)
}

// A cluster that is bootstrapped with one node and grows to its desired control-plane count must not
// run more operator replicas than it has nodes: the operator holds host ports and requires one pod
// per node, so a replica without a node blocks every later rollout of the Deployment (ADR 0113
// amendment 2026-11-04). The replicas follow the nodes that exist; the desired count is only the
// fallback when the node count is unknown.
func TestOperatorReplicasFollowTheNodesThatExist(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		desired     int32
		nodes, want int
	}{
		{"HA cluster with its first node", 3, 1, 1},
		{"HA cluster with two nodes", 3, 2, 2},
		{"HA cluster with all nodes", 3, 3, 2},
		{"one control plane, node count unknown", 1, 0, 1},
		{"HA cluster, node count unknown", 3, 0, 2},
		{"one control plane with workers stays at one replica", 1, 4, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := &ichorv1alpha1.TalosCluster{}
			c.Spec.ControlPlane.Count = tc.desired

			if got := renderedOperatorReplicas(t, c, tc.nodes); got != tc.want {
				t.Errorf("operator replicas = %d, want %d", got, tc.want)
			}
		})
	}
}

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
