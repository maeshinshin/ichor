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

// Package installer is the process the installer Job runs (ADR 0059): it snapshots the live
// manifests, applies the manifest embedded in this binary -- the target version's own, so
// nothing is fetched -- in the order installer RBAC, CRDs, controller, UI, checks each step, and rolls back
// from the snapshot when one fails.
package installer

import (
	"bytes"
	"errors"
	"io"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
)

const (
	kindCRD        = "CustomResourceDefinition"
	kindSecret     = "Secret"
	kindDeployment = "Deployment"

	// installerNamePrefix names the installer's own roles and bindings (config/default prefixes
	// config/rbac/ichor-installer's names with ichor-).
	installerNamePrefix = "ichor-installer"

	// uiNameMarker is how the UI objects are told apart from the controller objects: config/default
	// prefixes every name with ichor-, and config/manager-ui names its objects ui*.
	uiNameMarker = "ichor-ui"
)

// Decode splits a multi-document YAML stream into objects, skipping empty documents.
func Decode(data []byte) ([]*unstructured.Unstructured, error) {
	dec := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)

	var objs []*unstructured.Unstructured

	for {
		var obj unstructured.Unstructured

		if err := dec.Decode(&obj); err != nil {
			if errors.Is(err, io.EOF) {
				return objs, nil
			}

			return nil, err
		}

		if len(obj.Object) > 0 {
			objs = append(objs, &obj)
		}
	}
}

// plan is the manifest split into the steps, each in the manifest's own order.
type plan struct {
	clusterRBAC, namespacedRBAC, crds, controller, ui []*unstructured.Unstructured
}

// classify splits objs. A Secret is never part of the plan: the installer neither applies it
// (that would overwrite what the cluster generated) nor snapshots it (the snapshot lives in
// a ConfigMap).
func classify(objs []*unstructured.Unstructured) plan {
	var p plan

	for _, o := range objs {
		switch {
		case o.GetKind() == kindSecret:
		case isInstallerRBAC(o):
			if o.GetNamespace() == "" {
				p.clusterRBAC = append(p.clusterRBAC, o)
			} else {
				p.namespacedRBAC = append(p.namespacedRBAC, o)
			}
		case o.GetKind() == kindCRD:
			p.crds = append(p.crds, o)
		case strings.Contains(o.GetName(), uiNameMarker):
			p.ui = append(p.ui, o)
		default:
			p.controller = append(p.controller, o)
		}
	}

	return p
}

// isInstallerRBAC reports whether o is one of the installer's own roles or bindings. They are
// applied first: a run started by an older release holds that release's rights, which may lack
// a rule the new manifest needs (a new CRD name, a delete verb), and the installer may
// escalate and patch roles, so it grants itself the new rights before using them.
func isInstallerRBAC(o *unstructured.Unstructured) bool {
	switch o.GetKind() {
	case kindClusterRole, "ClusterRoleBinding", "Role", "RoleBinding":
		return strings.HasPrefix(o.GetName(), installerNamePrefix)
	}

	return false
}

func (p plan) nonRBAC() []*unstructured.Unstructured {
	out := make([]*unstructured.Unstructured, 0, len(p.crds)+len(p.controller)+len(p.ui))
	out = append(out, p.crds...)
	out = append(out, p.controller...)

	return append(out, p.ui...)
}
