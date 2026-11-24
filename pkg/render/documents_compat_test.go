package render

import (
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/ichorconfig"
)

func storedDocumentsPatch(t *testing.T, docs string) ichorv1alpha1.TalosConfigPatch {
	t.Helper()

	p := ichorv1alpha1.TalosConfigPatch{Namespace: testNamespace, Name: "stored"}
	p.Spec.ClusterRef = corev1.LocalObjectReference{Name: testClusterName}
	p.Spec.Layer = layerCluster

	if err := json.Unmarshal([]byte(docs), &p.Spec.Documents); err != nil {
		t.Fatal(err)
	}

	return p
}

const (
	sysctlDocument      = `[{"kind":"SysctlConfig","sysctlConfig":{"params":{"net.ipv4.ip_forward":"1"}}}]`
	timeServersDocument = `[{"kind":"Config","config":{"version":"v1alpha1",` +
		`"machine":{"time":{"servers":["ntp.example"]}}}}]`
)

// A patch stored by v0.2 with machine.time.servers (a setting spec.config has no same-named place for) in
// spec.documents renders it with no migration run: the nodes keep what they had.
func TestStoredDocumentsPatchRendersWithoutMigration(t *testing.T) {
	out := mustRender(t, testCluster("false"), testMachine("cp1", "controlplane", nil), testBundle(t),
		[]ichorv1alpha1.TalosConfigPatch{storedDocumentsPatch(t, timeServersDocument)})

	requireAll(t, out, "ntp.example")
}

// Migrating a patch (spec.documents to the spec.config ConfigFromDocuments writes) does not change the render, byte
// for byte.
func TestMigratingDocumentsDoesNotChangeTheRender(t *testing.T) {
	stored := storedDocumentsPatch(t, sysctlDocument)

	cfg, err := ichorv1alpha1.ConfigFromDocuments(stored.Spec.Documents)
	if err != nil {
		t.Fatal(err)
	}

	migrated := stored
	migrated.Spec.Documents, migrated.Spec.Config = nil, cfg

	render := func(p ichorv1alpha1.TalosConfigPatch) string {
		return mustRender(t, testCluster("false"), testMachine("cp1", "controlplane", nil), testBundle(t),
			[]ichorv1alpha1.TalosConfigPatch{p})
	}

	before, after := render(stored), render(migrated)

	requireAll(t, before, "net.ipv4.ip_forward")

	if before != after {
		t.Errorf("the render changed when the patch was migrated:\n--- before\n%s\n--- after\n%s", before, after)
	}
}

// Each setting Talos 1.14 moved out of the v1alpha1 document, as a v0.2 patch stored it in spec.documents: it
// renders on a v1.14 contract and gives the node the configuration of the hand-written spec.config, and
// ConfigFromDocuments (the migration) writes exactly that spec.config.
func TestStoredV1Alpha1SettingsRenderLikeTheirSpecConfig(t *testing.T) {
	for name, c := range map[string]struct{ stored, config string }{
		"certSANs": {`{"cluster":{"apiServer":{"certSANs":["san.example"]}}}`,
			`{"cluster":{"apiServer":{"certExtraSANs":["san.example"]}}}`},
		"coreDNS.disabled": {`{"cluster":{"coreDNS":{"disabled":true}}}`,
			`{"cluster":{"coreDNS":{"enabled":false}}}`},
		"time.servers": {`{"machine":{"time":{"servers":["ntp.example"]}}}`,
			`{"machine":{"time":{"ntp":{"servers":["ntp.example"]}}}}`},
		"time.disabled": {`{"machine":{"time":{"disabled":true}}}`,
			`{"machine":{"time":{"enabled":false}}}`},
		"kernel.modules": {`{"machine":{"kernel":{"modules":[{"name":"br_netfilter","parameters":["a=1"]}]}}}`,
			`{"machine":{"kernel":{"modules":{"br_netfilter":{"parameters":["a=1"]}}}}}`},
		"extraHostEntries": {`{"machine":{"network":{"extraHostEntries":[{"ip":"10.1.1.1","aliases":["h.example"]}]}}}`,
			`{"machine":{"network":{"extraHostEntries":{"10.1.1.1":{"hostnames":["h.example"]}}}}}`},
		"nameservers": {`{"machine":{"network":{"nameservers":["1.1.1.1"]}}}`,
			`{"machine":{"network":{"nameservers":[{"address":"1.1.1.1"}]}}}`},
		"searchDomains": {`{"machine":{"network":{"searchDomains":["a.example"],"disableSearchDomain":true}}}`,
			`{"machine":{"network":{"searchDomains":{"domains":["a.example"],"disableDefault":true}}}}`},
		"mirrors.overridePath": {
			`{"machine":{"registries":{"mirrors":{"docker.io":{"endpoints":["https://m.example"],"overridePath":true}}}}}`,
			`{"machine":{"registries":{"mirrors":{"docker.io":` +
				`{"endpoints":[{"url":"https://m.example","overridePath":true}]}}}}}`},
		"imageCache.localEnabled": {`{"machine":{"features":{"imageCache":{"localEnabled":true}}}}`,
			`{"machine":{"features":{"imageCache":{"local":{"enabled":true}}}}}`},
		"inlineManifests": {`{"cluster":{"inlineManifests":[{"name":"a","contents":"b: c"}]}}`,
			`{"cluster":{"inlineManifests":{"a":{"manifest":"b: c"}}}}`},
	} {
		t.Run(name, func(t *testing.T) {
			stored := storedDocumentsPatch(t, `[{"kind":"Config","config":{"version":"v1alpha1",`+c.stored[1:]+`}]`)

			want := ichorconfig.Config{}
			if err := json.Unmarshal([]byte(c.config), &want); err != nil {
				t.Fatal(err)
			}

			got, err := ichorv1alpha1.ConfigFromDocuments(stored.Spec.Documents)
			if err != nil {
				t.Fatalf("the migration cannot carry it: %v", err)
			}

			g, _ := json.Marshal(got)
			w, _ := json.Marshal(want)

			if string(g) != string(w) {
				t.Errorf("the migration writes\n%s\nnot the hand-written\n%s", g, w)
			}

			byHand := stored
			byHand.Spec.Documents, byHand.Spec.Config = nil, &want

			render := func(p ichorv1alpha1.TalosConfigPatch) string {
				out, _, err := Render(testCluster("false"), testMachine("cp1", "controlplane", nil), testBundle(t),
					[]ichorv1alpha1.TalosConfigPatch{p}, nil)
				if err != nil {
					t.Fatalf("Render: %v", err)
				}

				return string(out)
			}

			if before, after := render(stored), render(byHand); before != after {
				t.Errorf("the stored patch renders differently from its spec.config:\n--- stored\n%s\n--- config\n%s",
					before, after)
			}
		})
	}
}

// A patch with a setting spec.config has no place for (extraManifests, a v1alpha1 list without names) renders as
// stored,
// and the settings Talos 1.14 moved still render in their own documents, not in a v1alpha1 spelling that conflicts.
func TestStoredPatchWithAnUnplacedSettingStillRendersTheRest(t *testing.T) {
	p := storedDocumentsPatch(t, `[{"kind":"Config","config":{"version":"v1alpha1","cluster":{`+
		`"apiServer":{"certSANs":["san.example"]},"extraManifests":["https://x.example/y.yaml"]}}}]`)

	if _, err := ichorv1alpha1.ConfigFromDocuments(p.Spec.Documents); err == nil {
		t.Fatal("the setting without a place was carried")
	}

	out := mustRender(t, testCluster("false"), testMachine("cp1", "controlplane", nil), testBundle(t),
		[]ichorv1alpha1.TalosConfigPatch{p})

	requireAll(t, out, "san.example", "https://x.example/y.yaml")
}
