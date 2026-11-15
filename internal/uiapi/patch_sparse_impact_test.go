package uiapi

import (
	"testing"

	coreconfig "github.com/siderolabs/talos/pkg/machinery/config"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/fieldmeta"
	"github.com/maeshinshin/ichor/pkg/ichorconfig"
)

// A patch that sets only cluster.apiServer must not drag machine.type and machine.certSANs into the impact:
// they are not set, and as unknown paths they would read as "needs a reboot" for what is an apiServer restart.
func TestSparseConfigPatchImpactIsNotAReboot(t *testing.T) {
	spec := ichorv1alpha1.TalosConfigPatchSpec{Config: &ichorconfig.Config{Cluster: &ichorconfig.Cluster{
		ApiServer: &ichorconfig.ClusterApiServer{ExtraArgs: map[string][]string{"foo": {"b"}}},
	}}}

	docs, err := spec.EffectiveDocuments(true)
	if err != nil {
		t.Fatal(err)
	}

	paths, err := patchPaths(docs)
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
