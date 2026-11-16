/*
Copyright 2026 maeshinshin.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"encoding/json"
	"testing"

	"github.com/maeshinshin/ichor/pkg/ichorconfig"
)

const kindBondConfig = "BondConfig"

// asymmetricBondSettings are the bond settings that one spelling has and the other does not. Each must stay
// expressible in spec.config, whatever the link documents are joined into. The eight document-only settings are
// written under machine.network.bonds; the three v1alpha1-only settings under machine.network.interfaces.
var asymmetricBondSettings = []struct {
	name, spec, kind, path string
}{
	{"hardwareAddr", `"hardwareAddr":"LjxNXm9w"`, kindBondConfig, "hardwareAddr"},
	{"nsIp6Targets", `"nsIp6Targets":["fd00::1"]`, kindBondConfig, "nsIp6Targets"},
	{adLACPActiveName, `"adLACPActive":1`, kindBondConfig, adLACPActiveName},
	{"missedMax", missedMaxJSON, kindBondConfig, "missedMax"},
	{"up", `"up":true`, kindBondConfig, "up"},
	{"multicast", `"multicast":true`, kindBondConfig, "multicast"},
	{"addresses[].routePriority", `"addresses":[{"address":"10.0.0.2/24","routePriority":100}]`, kindBondConfig, "addresses.0.routePriority"},
	{"routes[].table", `"routes":[{"destination":"10.0.0.0/8","gateway":"10.0.0.1","table":100}]`, kindBondConfig, "routes.0.table"},
	{"primary", `"primary":"eth0"`, configKind, "machine.network.interfaces.0.bond.primary"},
	{"adActorSystem", `"adActorSystem":"00:00:00:00:00:01"`, configKind, "machine.network.interfaces.0.bond.adActorSystem"},
	{"deviceSelectors", `"deviceSelectors":[{"driver":"virtio_net"}]`, configKind, "machine.network.interfaces.0.bond.deviceSelectors.0.driver"},
}

// TestAsymmetricBondSettingsSurvive writes each asymmetric bond setting alone in spec.config and checks that it
// reaches the Talos document that carries it, and that the documents read back into spec.config without losing it.
func TestAsymmetricBondSettingsSurvive(t *testing.T) {
	t.Parallel()

	for _, tc := range asymmetricBondSettings {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var spec string

			if tc.kind == kindBondConfig {
				spec = `{"machine":{"network":{"bonds":{"bond0":{"links":["eth0","eth1"],"bondMode":1,` + tc.spec + `}}}}}`
			} else {
				spec = `{"machine":{"network":{"interfaces":[{"interface":"bond0","bond":{"mode":"active-backup","interfaces":["eth0","eth1"],` + tc.spec + `}}]}}}`
			}

			var c ichorconfig.Config
			if err := json.Unmarshal([]byte(spec), &c); err != nil {
				t.Fatal(err)
			}

			docs, err := ConvertConfig(&c, true)
			if err != nil {
				t.Fatalf("ConvertConfig: %v", err)
			}

			if got := documentPath(t, docs, tc.kind, tc.path); got == nil {
				t.Fatalf("%s at %s.%s is not in the documents", tc.name, tc.kind, tc.path)
			}

			if _, err := ConfigFromDocuments(docs); err != nil {
				t.Fatalf("documents read back lost %s: %v", tc.name, err)
			}
		})
	}
}

// documentPath returns the value at path inside the document of kind, or nil when the document or path is absent.
// A path element that is a number indexes a list.
func documentPath(t *testing.T, docs []TalosConfigPatchDocument, kind, path string) any {
	t.Helper()

	key, ok := TalosConfigPatchDocumentFieldKey(kind)
	if !ok {
		t.Fatalf("no document kind %q", kind)
	}

	for _, d := range docs {
		if d.Kind != kind {
			continue
		}

		raw, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}

		var env map[string]any
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatal(err)
		}

		cur := env[key]

		for _, p := range splitDots(path) {
			switch node := cur.(type) {
			case map[string]any:
				cur = node[p]
			case []any:
				i := 0
				for _, r := range p {
					i = i*10 + int(r-'0')
				}

				if i >= len(node) {
					return nil
				}

				cur = node[i]
			default:
				return nil
			}
		}

		return cur
	}

	return nil
}

func splitDots(s string) []string {
	var out []string

	start := 0
	for i := range len(s) {
		if s[i] == '.' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}

	return append(out, s[start:])
}
