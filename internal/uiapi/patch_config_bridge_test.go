package uiapi

import (
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/ichorconfig"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// The wire carries Talos documents and the object holds spec.config: a patch read for the editor and written back
// is the same patch.
func TestPatchWireRoundTripsThroughSpecConfig(t *testing.T) {
	p := &ichorv1alpha1.TalosConfigPatch{
		Name: "p", Namespace: "ns",
		Spec: ichorv1alpha1.TalosConfigPatchSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c"},
			Layer:      "cluster",
			Config: &ichorconfig.Config{
				Machine: &ichorconfig.Machine{
					Type: "controlplane", CertSANs: []string{"10.0.0.1"},
					Sysctls: map[string]string{"net.ipv4.ip_forward": "1"},
					Kernel: &ichorconfig.MachineKernel{Modules: map[string]*ichorconfig.MachineKernelModulesEntry{
						"nbd": {}, "br_netfilter": {Parameters: []string{"nf_conntrack_helper=1"}},
					}},
					Volumes: &ichorconfig.MachineVolumes{User: map[string]*ichorconfig.MachineVolumesUserEntry{
						"data": {Provisioning: &typedconfig.ProvisioningSpec{DiskSelectorSpec: &typedconfig.DiskSelector{Match: `disk.wwid == "x"`}, ProvisioningMinSize: "0"}},
					}},
				},
				Cluster: &ichorconfig.Cluster{
					ApiServer:       &ichorconfig.ClusterApiServer{ExtraArgs: map[string][]string{"audit-log-maxage": {"3"}}},
					InlineManifests: map[string]*ichorconfig.ClusterInlineManifestsEntry{"m": {Manifest: "kind: List"}},
				},
			},
		},
	}

	wire, err := patchToPB(p)
	if err != nil {
		t.Fatal(err)
	}

	if len(wire.GetDocuments()) < 5 {
		t.Fatalf("the wire carries %d documents", len(wire.GetDocuments()))
	}

	var spec ichorv1alpha1.TalosConfigPatchSpec
	if err := applyPatchSpec(&spec, wire); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(spec.Config, p.Spec.Config) {
		t.Errorf("the patch changed on the way:\n%+v\n%+v", p.Spec.Config, spec.Config)
	}
}

// A document the editor sends that spec.config cannot carry is refused by name, never dropped.
func TestPatchFromTheEditorRefusesWhatSpecConfigDoesNotCarry(t *testing.T) {
	var spec ichorv1alpha1.TalosConfigPatchSpec

	err := applyPatchSpec(&spec, &pb.Patch{
		Layer:     pb.ConfigLayer_CONFIG_LAYER_CLUSTER,
		Documents: []*pb.PatchDocument{{Kind: "Config", Document: `{"cluster": {"extraManifests": ["https://example.invalid/x.yaml"]}}`}},
	})
	if err == nil || !strings.Contains(err.Error(), "cluster extraManifests") {
		t.Fatalf("want a refusal naming the setting, got %v", err)
	}
}
