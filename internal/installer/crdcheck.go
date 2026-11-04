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

package installer

import (
	"fmt"
	"slices"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

// retiredField names a CRD field that was deliberately removed from the schema. checkAdditive
// lets exactly these paths disappear and refuses every other removal. This list stays short and explicit.
type retiredField struct {
	crd  string // CRD name
	path string // dotted property path from the object root, e.g. "spec.gitops"
}

const ichorInstallationsCRD = "ichorinstallations.ichor.maesh.dev"

// retiredFields is the whole list.
var retiredFields = []retiredField{
	// GitOps is permanently out of scope; spec.gitops (and everything under it) is gone.
	{crd: "talosclusters.ichor.maesh.dev", path: "spec.gitops"},
	// (hand-over is always to Ichor), retired.
	{crd: "talosclusters.ichor.maesh.dev", path: "spec.cni.handover"},
	// (the stack is gone), retired.
	{crd: ichorInstallationsCRD, path: "spec.stack"},
	{crd: ichorInstallationsCRD, path: "status.stack"},
	// The only management mode is ichor, so the field is gone.
	{crd: ichorInstallationsCRD, path: "spec.managementMode"},
}

func isRetired(crd, path string) bool {
	return slices.Contains(retiredFields, retiredField{crd, path})
}

// checkAdditive refuses a target CRD that would take something away from the live one: a
// served version, a schema property, a changed property type, or a changed scope. That is
// what "additive, never downgraded" means (design, "Upgrades"), judged on the schemas
// themselves rather than on a version string, so it also catches a hand-edited live CRD
// and a target built from an older tree.
func checkAdditive(live, target *unstructured.Unstructured) error {
	var l, t apiextensionsv1.CustomResourceDefinition

	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(live.Object, &l); err != nil {
		return fmt.Errorf("decoding live CRD %s: %w", live.GetName(), err)
	}

	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(target.Object, &t); err != nil {
		return fmt.Errorf("decoding target CRD %s: %w", target.GetName(), err)
	}

	if l.Spec.Scope != t.Spec.Scope {
		return fmt.Errorf("CRD %s: scope would change from %s to %s", l.Name, l.Spec.Scope, t.Spec.Scope)
	}

	for _, lv := range l.Spec.Versions {
		i := slices.IndexFunc(t.Spec.Versions, func(v apiextensionsv1.CustomResourceDefinitionVersion) bool {
			return v.Name == lv.Name
		})
		if i < 0 {
			return fmt.Errorf("CRD %s: version %s would be removed (the target is older than what is installed)", l.Name, lv.Name)
		}

		tv := t.Spec.Versions[i]

		if lv.Schema == nil || lv.Schema.OpenAPIV3Schema == nil {
			continue
		}

		if tv.Schema == nil || tv.Schema.OpenAPIV3Schema == nil {
			return fmt.Errorf("CRD %s version %s: the target has no schema", l.Name, lv.Name)
		}

		if err := schemaCovers(l.Name, tv.Schema.OpenAPIV3Schema, lv.Schema.OpenAPIV3Schema, "spec-of-"+lv.Name, ""); err != nil {
			return fmt.Errorf("CRD %s: %w (the target is older than what is installed)", l.Name, err)
		}
	}

	return nil
}

// schemaCovers reports whether target has every property live has, with the same type.
//
// rel is the dotted property path from the object root (empty at the root), matched against the
// retired fields of crd.
func schemaCovers(crd string, target, live *apiextensionsv1.JSONSchemaProps, path, rel string) error {
	if live.Type != "" && target.Type != live.Type {
		return fmt.Errorf("%s: type would change from %q to %q", path, live.Type, target.Type)
	}

	for name, lp := range live.Properties {
		tp, ok := target.Properties[name]
		childRel := name
		if rel != "" {
			childRel = rel + "." + name
		}

		if !ok {
			if isRetired(crd, childRel) {
				continue
			}

			return fmt.Errorf("%s.%s would be removed", path, name)
		}

		if err := schemaCovers(crd, &tp, &lp, path+"."+name, childRel); err != nil {
			return err
		}
	}

	if live.Items != nil && live.Items.Schema != nil {
		if target.Items == nil || target.Items.Schema == nil {
			return fmt.Errorf("%s: items schema would be removed", path)
		}

		if err := schemaCovers(crd, target.Items.Schema, live.Items.Schema, path+"[]", rel+"[]"); err != nil {
			return err
		}
	}

	if live.AdditionalProperties != nil && live.AdditionalProperties.Schema != nil {
		if target.AdditionalProperties == nil || target.AdditionalProperties.Schema == nil {
			return fmt.Errorf("%s: additionalProperties schema would be removed", path)
		}

		if err := schemaCovers(crd, target.AdditionalProperties.Schema, live.AdditionalProperties.Schema, path+"{}", rel+"{}"); err != nil {
			return err
		}
	}

	return nil
}
