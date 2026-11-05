package render

import (
	"strings"
	"testing"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// A v1alpha1 Config document that sets cluster.apiServer cannot be merged under a multi-document
// version contract: the base config already carries a KubeAPIServerConfig document.
func TestLegacyAPIServerPatchFailsRender(t *testing.T) {
	cluster := testCluster("false")
	m := testMachine("cp1", "controlplane", nil)
	patch := configPatch("apiserver", &typedconfig.Config{
		ConfigVersion: configVersionV1Alpha1,
		MachineConfig: &typedconfig.MachineConfig{MachineType: "controlplane", MachineCertSANs: []string{}},
		ClusterConfig: &typedconfig.ClusterConfig{APIServerConfig: &typedconfig.APIServerConfig{
			ExtraArgsConfig: map[string][]string{"audit-log-maxage": {"3"}},
		}},
	})

	_, _, err := Render(cluster, m, testBundle(t), []ichorv1alpha1.TalosConfigPatch{patch}, nil)
	if err == nil {
		t.Fatal("expected a render error")
	}

	t.Log(err)

	if !strings.Contains(err.Error(), "apiServer") {
		t.Fatalf("error does not name apiServer: %v", err)
	}
}
