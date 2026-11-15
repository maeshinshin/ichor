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
	return configPatch(name, c)
}

// Removing spec.documents from the CRD changed nothing about what reaches a node: a setting written in
// spec.config is rendered as the Talos document that carries it, the same one spec.documents carried before it was removed.
func TestSpecConfigRendersAsTheTalosDocumentThatCarriesIt(t *testing.T) {
	m := testMachine("node-a", roleWorker, nil)
	bundle := testBundle(t)

	out := mustRender(t, testCluster("true"), m, bundle, []ichorv1alpha1.TalosConfigPatch{
		ichorPatch("p", &ichorconfig.Config{Machine: &ichorconfig.Machine{Sysctls: map[string]string{sysctlKey: "1"}}}),
	})

	if want := "kind: SysctlConfig\nparams:\n    " + sysctlKey + ": \"1\"\n"; !strings.Contains(out, want) {
		t.Errorf("the rendered configuration lacks the SysctlConfig document:\n%s\n--- in\n%s", want, out)
	}
}

// The guards on what a patch may write apply to spec.config: Ichor's own Cilium inline
// manifest is dropped once the cluster has handed over, and Talos API access is left out of workers.
func TestSpecConfigPassesTheGuards(t *testing.T) {
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

// A cluster-layer patch reaches workers too: what it says about the control plane (here, the API server) applies to
// control planes and is left out of workers, which Talos would otherwise refuse.
func TestControlPlaneSettingsOfAClusterPatchAreLeftOutOfWorkers(t *testing.T) {
	bundle := testBundle(t)
	patch := ichorPatch("apiserver", &ichorconfig.Config{Cluster: &ichorconfig.Cluster{ApiServer: &ichorconfig.ClusterApiServer{
		ExtraArgs: map[string][]string{"audit-log-maxage": {"3"}},
	}}})

	requireAll(t, mustRender(t, testCluster("true"), testMachine("cp", roleControlPlane, nil), bundle, []ichorv1alpha1.TalosConfigPatch{patch}),
		"audit-log-maxage")
	requireNone(t, mustRender(t, testCluster("true"), testMachine("w", roleWorker, nil), bundle, []ichorv1alpha1.TalosConfigPatch{patch}),
		"audit-log-maxage")
}
