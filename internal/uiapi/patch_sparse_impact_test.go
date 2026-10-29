package uiapi

import (
	"testing"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/fieldmeta"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// A sparse Config document that sets only cluster.apiServer must not drag machine.type and
// machine.certSANs into the impact: they are not set, and as unknown paths they would read as
// "needs a reboot" for what is an apiServer restart.
func TestSparseConfigPatchImpactIsNotAReboot(t *testing.T) {
	doc := ichorv1alpha1.TalosConfigPatchDocument{
		Kind: kindConfig,
		Config: &typedconfig.Config{
			ConfigVersion: "v1alpha1",
			ClusterConfig: &typedconfig.ClusterConfig{
				APIServerConfig: &typedconfig.APIServerConfig{ExtraArgsConfig: map[string][]string{"foo": {"b"}}},
			},
		},
	}

	paths, err := patchPaths([]ichorv1alpha1.TalosConfigPatchDocument{doc})
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range paths {
		if p == "machine.type" || p == "machine.certSANs" {
			t.Fatalf("sparse patch reports %q as set: %v", p, paths)
		}
	}

	contract, _ := coreconfig.ParseContractFromVersion("v1.14")

	if impact := fieldmeta.Assess(paths, contract); impact.RebootRequired {
		t.Fatalf("apiServer-only patch reported as needing a reboot: %v (paths %v)", impact.RebootPaths, paths)
	}
}
