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
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

// parentDefaultAllowlist names the optional object parents whose absence is meaningful, so they
// carry no default although a child does. Key: "<kind> <path>"; a key also covers everything
// beneath it. The value is the reason. Nothing else may be listed.
var parentDefaultAllowlist = map[string]string{
	"IchorInstallation .spec.installerSignature":             "unset is read as required, the child default's own value",
	"MachineReclaim .spec.clusterRef":                        "optional reference; a LocalObjectReference's name default is not a target",
	"TalosCluster .spec.authentication.ichor":                "presence turns Ichor's OIDC issuer on",
	"TalosCluster .spec.cni.unmanaged":                       "only valid with preset unmanaged; a default would fail the CEL rule on every other preset",
	"TalosCluster .spec.controlPlaneMetrics":                 "presence exposes the metrics; unset leaves Talos alone",
	"TalosCluster .spec.registries[].authSecretRef":          "no reference means no credentials",
	"TalosCluster .spec.security.firewall":                   "presence turns the firewall on",
	"TalosCluster .spec.security.kubeletServingCertificates": "presence turns it on",
	"TalosCluster .spec.security.wireGuard":                  "presence turns encryption on",
	"TalosConfigPatch .spec.target.machineRef":               "one of the target selectors; unset means not targeting by name",
	"TalosCredentialRequest .spec.machineRef":                "only for the download purposes",
	"TalosMachine .spec.network":                             "unset leaves networking to Talos (DHCP)",
	"TalosOperation .spec.keyRotation":                       "present only for the key-rotation kind",
	"TalosOperation .spec.node":                              "present only for the node kind",
	// Kubernetes core/v1 types embedded in a static pod; their absent objects carry Kubernetes semantics.
	"TalosConfigPatch .spec.documents[].kubeStaticPodConfig.pod": "embedded core/v1 PodSpec",
	"TalosConfigPatch .spec.config.machine.staticPods{}.pod":     "embedded core/v1 PodSpec",
}

func allowlisted(key string) (string, bool) {
	for k := range parentDefaultAllowlist {
		if key == k || strings.HasPrefix(key, k+".") || strings.HasPrefix(key, k+"[") || strings.HasPrefix(key, k+"{") {
			return k, true
		}
	}

	return "", false
}

// The API server applies a child's default only when its parent object is present, so an optional
// parent with defaulted children and no default of its own is stored without them when the user
// leaves the parent out. TestOptionalParentsDefaultTheirChildren walks every generated CRD and
// fails on such a parent.
func TestOptionalParentsDefaultTheirChildren(t *testing.T) {
	files, err := filepath.Glob("../../config/crd/bases/*.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no generated CRDs found: %v", err)
	}

	used := map[string]bool{}

	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}

		var crd apiextensionsv1.CustomResourceDefinition
		if err := yaml.Unmarshal(raw, &crd); err != nil {
			t.Fatalf("%s: %v", f, err)
		}

		for _, v := range crd.Spec.Versions {
			if v.Schema == nil || v.Schema.OpenAPIV3Schema == nil {
				continue
			}

			walkParents(v.Schema.OpenAPIV3Schema, crd.Spec.Names.Kind, "", true, func(key string) {
				if k, ok := allowlisted(key); ok {
					used[k] = true

					return
				}

				t.Errorf("%s: optional parent has defaulted children but no default of its own; "+
					"add +kubebuilder:default on the field (the children spelled out if CEL reads them) "+
					"or allowlist it with the reason", key)
			})
		}
	}

	for k := range parentDefaultAllowlist {
		if !used[k] {
			t.Errorf("allowlist entry %q matches no parent; remove it", k)
		}
	}
}

// walkParents calls report for each optional object under s that has no default while a direct
// child does. It descends through properties, array items and map values.
func walkParents(s *apiextensionsv1.JSONSchemaProps, kind, path string, required bool, report func(string)) {
	if s.Type == "object" && len(s.Properties) > 0 {
		if !required && s.Default == nil && hasDefaultedChild(s) {
			report(kind + " " + path)
		}

		names := make([]string, 0, len(s.Properties))
		for n := range s.Properties {
			names = append(names, n)
		}

		sort.Strings(names)

		for _, n := range names {
			c := s.Properties[n]
			walkParents(&c, kind, path+"."+n, slices.Contains(s.Required, n), report)
		}
	}

	if s.Items != nil && s.Items.Schema != nil {
		walkParents(s.Items.Schema, kind, path+"[]", true, report)
	}

	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
		walkParents(s.AdditionalProperties.Schema, kind, path+"{}", true, report)
	}
}

func hasDefaultedChild(s *apiextensionsv1.JSONSchemaProps) bool {
	for _, c := range s.Properties {
		if c.Default != nil {
			return true
		}
	}

	return false
}

func TestEffectivePresetReadsUnsetAsCilium(t *testing.T) {
	for preset, want := range map[string]string{"": "cilium", "cilium": "cilium", "unmanaged": "unmanaged", "flannel": "flannel"} {
		if got := (TalosClusterCNI{Preset: preset}).EffectivePreset(); got != want {
			t.Errorf("EffectivePreset(%q) = %q, want %q", preset, got, want)
		}
	}
}
