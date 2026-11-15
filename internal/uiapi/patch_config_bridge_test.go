package uiapi

import (
	"bufio"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/ichorconfig"
	"github.com/maeshinshin/ichor/pkg/typedconfig"
)

// The wire carries spec.config as itself, JSON: a patch read for the editor and written back is the same patch,
// and no document is converted on the way.
func TestPatchWireCarriesSpecConfigAsItself(t *testing.T) {
	p := &ichorv1alpha1.TalosConfigPatch{
		Name: "p", Namespace: "ns",
		Spec: ichorv1alpha1.TalosConfigPatchSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "c"},
			Layer:      "cluster",
			Config: &ichorconfig.Config{
				Machine: &ichorconfig.Machine{
					Type: "controlplane", CertSANs: []string{gatewayIP},
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

	want, err := json.Marshal(p.Spec.Config)
	if err != nil {
		t.Fatal(err)
	}

	if wire.GetConfig() != string(want) || len(wire.GetDocuments()) != 0 { //nolint:staticcheck // the deprecated field must stay empty
		t.Fatalf("the wire is not spec.config alone:\n%s\n%s", wire.GetConfig(), want)
	}

	var spec ichorv1alpha1.TalosConfigPatchSpec
	if err := applyPatchSpec(&spec, wire); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(spec.Config, p.Spec.Config) {
		t.Errorf("the patch changed on the way:\n%+v\n%+v", p.Spec.Config, spec.Config)
	}

	// A patch that sets nothing is "{}" out and nothing back.
	empty, err := patchToPB(&ichorv1alpha1.TalosConfigPatch{Spec: ichorv1alpha1.TalosConfigPatchSpec{Layer: "cluster"}})
	if err != nil || empty.GetConfig() != "{}" {
		t.Fatalf("an empty patch is %q, %v", empty.GetConfig(), err)
	}

	var back ichorv1alpha1.TalosConfigPatchSpec
	if err := applyPatchSpec(&back, empty); err != nil || back.Config != nil {
		t.Errorf("an empty patch came back as %+v, %v", back.Config, err)
	}
}

// notCarried reads the settings of a Talos document that spec.config does not carry (pkg/ichorconfig's own list),
// each as the JSON the editor's text box would send for it: the setting's path, with a key where the schema has a
// map, and a string for its value; the key of the result is the first segment of the path spec.config has no field
// for, which is the one a refusal names. A setting whose path is also in spec.config, spelled another way, is not one
// the box can send as unknown, so it is left out; the second result counts them.
func notCarried(t *testing.T) (settings map[string]refusal, spelledElsewhere []string) {
	t.Helper()

	f, err := os.Open("../../pkg/ichorconfig/testdata/not_carried.txt")
	if err != nil {
		t.Fatal(err)
	}

	defer f.Close()

	settings = map[string]refusal{}

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		kind, path, ok := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		if !ok || strings.HasPrefix(kind, "#") || kind != "Config" {
			continue
		}

		typ := reflect.TypeFor[ichorconfig.Config]()
		segs := strings.Split(path, ".")
		known := true
		out := make([]string, 0, len(segs))

		for _, seg := range segs {
			for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Map {
				if typ.Kind() == reflect.Map {
					out = append(out, "k")
				}

				typ = typ.Elem()
			}

			var next reflect.Type

			if typ.Kind() == reflect.Struct {
				for sf := range typ.Fields() {
					if name, _, _ := strings.Cut(sf.Tag.Get("json"), ","); name == seg {
						next = sf.Type
					}
				}
			}

			out = append(out, seg)

			if next == nil {
				known = false

				break
			}

			typ = next
		}

		if !known {
			settings[path] = refusal{nested(out, `"x"`), out[len(out)-1]}
		} else {
			spelledElsewhere = append(spelledElsewhere, path)
		}
	}

	return settings, spelledElsewhere
}

// refusal is what the box sends for a setting, and the name a refusal of it has to give.
type refusal struct{ config, name string }

// nested is the JSON object with value at the path of keys.
func nested(keys []string, value string) string {
	if len(keys) == 0 {
		return value
	}

	return `{"` + keys[0] + `":` + nested(keys[1:], value) + `}`
}

// A setting the editor sends that spec.config does not carry is refused by name, never dropped: the API server
// would prune it without a word.
func TestPatchFromTheEditorRefusesWhatSpecConfigDoesNotCarry(t *testing.T) {
	settings, elsewhere := notCarried(t)
	if len(settings) < 10 {
		t.Fatalf("only %d settings of pkg/ichorconfig's list are unknown to spec.config (%d are spelled at their path in another way)", len(settings), len(elsewhere))
	}

	for path, r := range settings {
		var spec ichorv1alpha1.TalosConfigPatchSpec

		err := applyPatchSpec(&spec, &pb.Patch{Layer: pb.ConfigLayer_CONFIG_LAYER_CLUSTER, Config: r.config})
		if err == nil || !strings.Contains(err.Error(), "does not carry") || !strings.Contains(err.Error(), r.name+`"`) {
			t.Errorf("%s (%s): want a refusal naming the setting, got %v", path, r.config, err)
		}
	}
}

// A client that still writes documents, or does not know config, is refused rather than read as an empty patch.
func TestPatchFromAnOlderClientIsRefused(t *testing.T) {
	for name, wire := range map[string]*pb.Patch{
		"documents": {Layer: pb.ConfigLayer_CONFIG_LAYER_CLUSTER, Documents: []*pb.PatchDocument{{Kind: "SysctlConfig", Document: `{"params":{}}`}}}, //nolint:staticcheck // the old shape is what is tested
		"no config": {Layer: pb.ConfigLayer_CONFIG_LAYER_CLUSTER},
	} {
		var spec ichorv1alpha1.TalosConfigPatchSpec
		if err := applyPatchSpec(&spec, wire); err == nil || !strings.Contains(err.Error(), "config is required") {
			t.Errorf("%s: want a refusal naming config, got %v", name, err)
		}
	}
}
