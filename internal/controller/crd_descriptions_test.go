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

package controller

import (
	"os"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

func loadCRDBase(t *testing.T, file string) map[string]any {
	t.Helper()

	raw, err := os.ReadFile("../../config/crd/bases/" + file)
	if err != nil {
		t.Fatal(err)
	}

	var crd map[string]any
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatal(err)
	}

	return crd
}

func crdVersion(t *testing.T, crd map[string]any) map[string]any {
	t.Helper()

	versions, _, err := unstructured.NestedSlice(crd, "spec", "versions")
	if err != nil || len(versions) != 1 {
		t.Fatalf("want one CRD version, got %d (err %v)", len(versions), err)
	}

	v, ok := versions[0].(map[string]any)
	if !ok {
		t.Fatalf("CRD version is %T, want a map", versions[0])
	}

	return v
}

// TestTalosClusterPrinterColumnNamesAConditionTheControllerSets pins the condition behind the
// TalosCluster status column: a column on a condition nothing sets shows blank on every object.
func TestTalosClusterPrinterColumnNamesAConditionTheControllerSets(t *testing.T) {
	t.Parallel()

	v := crdVersion(t, loadCRDBase(t, "ichor.maesh.dev_talosclusters.yaml"))
	cols, _, _ := unstructured.NestedSlice(v, "additionalPrinterColumns")

	want := ".status.conditions[?(@.type=='" + conditionTransitioning + "')].status"
	found := false

	for _, c := range cols {
		col, ok := c.(map[string]any)
		if !ok {
			t.Fatalf("printer column is %T, want a map", c)
		}

		if col["name"] == conditionTransitioning {
			found = true

			if col["jsonPath"] != want {
				t.Errorf("column %q reads %v, want %s", conditionTransitioning, col["jsonPath"], want)
			}
		}

		if path, _ := col["jsonPath"].(string); strings.Contains(path, "@.type=='Ready'") {
			t.Errorf("column %q reads the Ready condition, which no controller sets on a TalosCluster", col["name"])
		}
	}

	if !found {
		t.Errorf("no printer column named %q", conditionTransitioning)
	}
}

// TestTalosMachineConditionsDocumentConvergedByName pins that kubectl explain and the generated reference
// carry the meaning of the ConfigRollout reason Converged.
func TestTalosMachineConditionsDocumentConvergedByName(t *testing.T) {
	t.Parallel()

	v := crdVersion(t, loadCRDBase(t, "ichor.maesh.dev_talosmachines.yaml"))

	desc, found, err := unstructured.NestedString(v, "schema", "openAPIV3Schema", "properties", "status", "properties", "conditions", "description")
	if err != nil || !found {
		t.Fatalf("status.conditions has no description (found %v, err %v)", found, err)
	}

	for _, name := range []string{conditionConfigRollout, reasonRolloutConverged} {
		if !strings.Contains(desc, name) {
			t.Errorf("status.conditions description does not mention %q", name)
		}
	}
}

// TestSecretsRefDescriptionSaysWhatTheSecretHolds pins the wording away from "CA bundle": the Secret holds
// the whole secrets bundle, not only the CA certificates.
func TestSecretsRefDescriptionSaysWhatTheSecretHolds(t *testing.T) {
	t.Parallel()

	v := crdVersion(t, loadCRDBase(t, "ichor.maesh.dev_talosclusters.yaml"))

	desc, _, _ := unstructured.NestedString(v, "schema", "openAPIV3Schema", "properties", "spec", "properties", "secretsRef", "description")
	if strings.Contains(desc, "CA bundle") || !strings.Contains(desc, "secrets.yaml") {
		t.Errorf("secretsRef description = %q", desc)
	}
}
