package render

import (
	"strings"
	"testing"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/cni"
	"github.com/maeshinshin/ichor/pkg/ichorconfig"
)

func ichorPatch(name string, c *ichorconfig.Config) ichorv1alpha1.TalosConfigPatch {
	p := sysctlPatch(name, layerCluster, 0, nil, "")
	p.Spec.Documents = nil
	p.Spec.Config = c

	return p
}

// A setting written in spec.config reaches the machine exactly as the same setting written as a document.
func TestSpecConfigRendersLikeTheDocumentItConvertsTo(t *testing.T) {
	m := testMachine("node-a", roleWorker, nil)
	bundle := testBundle(t)

	asDocument := mustRender(t, testCluster("true"), m, bundle, []ichorv1alpha1.TalosConfigPatch{sysctlPatch("p", layerCluster, 0, nil, "1")})
	asConfig := mustRender(t, testCluster("true"), m, bundle, []ichorv1alpha1.TalosConfigPatch{
		ichorPatch("p", &ichorconfig.Config{Machine: &ichorconfig.Machine{Sysctls: map[string]string{sysctlKey: "1"}}}),
	})

	if asDocument != asConfig {
		t.Errorf("the rendered configurations differ:\n--- documents\n%s\n--- config\n%s", asDocument, asConfig)
	}

	if !strings.Contains(asConfig, sysctlKey) {
		t.Errorf("the setting is not in the rendered configuration:\n%s", asConfig)
	}
}

// The guards on what a patch may write apply to spec.config as to spec.documents: Ichor's own Cilium inline
// manifest is dropped once the cluster has handed over, and Talos API access is left out of workers.
func TestSpecConfigPassesTheSameGuardsAsDocuments(t *testing.T) {
	bundle := testBundle(t)

	cluster := testCluster("true")
	apimeta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type: ConditionCNIHandover, Status: metav1.ConditionTrue,
		Reason: handoverCompleteReason, Message: handoverCompleteMessage,
	})

	enabled := true
	c := &ichorconfig.Config{
		Cluster: &ichorconfig.Cluster{InlineManifests: map[string]*ichorconfig.ClusterInlineManifestsEntry{
			cni.InlineManifestName: {Manifest: "kind: Nothing\n"},
			"unrelated.yaml":       {Manifest: "kind: Other\n"},
		}},
		Machine: &ichorconfig.Machine{Features: &ichorconfig.MachineFeatures{
			KubernetesTalosAPIAccess: &ichorconfig.MachineFeaturesKubernetesTalosAPIAccess{
				Enabled: &enabled, AllowedRoles: []string{"os:reader"}, AllowedKubernetesNamespaces: []string{"kube-system"},
			},
		}},
	}

	patch := ichorPatch("guards", c)
	before := patch.DeepCopy()

	worker := mustRender(t, cluster, testMachine("node-w", roleWorker, nil), bundle, []ichorv1alpha1.TalosConfigPatch{patch})
	requireNone(t, worker, "name: "+cni.InlineManifestName, "KubeTalosAPIAccessConfig")
	requireAll(t, worker, "unrelated.yaml")

	control := mustRender(t, cluster, testMachine("node-c", roleControlPlane, nil), bundle, []ichorv1alpha1.TalosConfigPatch{patch})
	requireNone(t, control, "name: "+cni.InlineManifestName)
	requireAll(t, control, "unrelated.yaml", "KubeTalosAPIAccessConfig")

	if len(patch.Spec.Config.Cluster.InlineManifests) != len(before.Spec.Config.Cluster.InlineManifests) {
		t.Errorf("Render mutated the caller's patch")
	}
}

func TestRenderRefusesAPatchThatSetsDocumentsAndConfig(t *testing.T) {
	p := sysctlPatch("both", layerCluster, 0, nil, "1")
	p.Spec.Config = &ichorconfig.Config{}

	_, _, err := Render(testCluster("true"), testMachine("node-a", roleWorker, nil), testBundle(t), []ichorv1alpha1.TalosConfigPatch{p}, nil)
	if err == nil || !strings.Contains(err.Error(), "spec.documents and spec.config are both set") {
		t.Fatalf("want the refusal naming both, got %v", err)
	}
}
