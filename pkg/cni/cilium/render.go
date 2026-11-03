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

package cilium

import (
	_ "embed" // the pinned chart archive
	"fmt"
	"strings"

	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
	"helm.sh/helm/v3/pkg/releaseutil"
	sigsyaml "sigs.k8s.io/yaml"
)

// chartArchive is the pinned upstream chart (ChartVersion), checked in under
// chart/ and fetched, digest-verified, by `make cilium-chart`. It
// is embedded so rendering needs no network and is reproducible.
//
//go:embed chart/cilium-1.20.4.tgz
var chartArchive []byte

// renderChart expands the embedded chart with values into one multi-document
// YAML stream, like `helm template --namespace kube-system` without hooks
// (Talos applies the result directly, so Helm hooks have no meaning). The
// chart ships no crds/ directory -- the operator installs Cilium's CRDs
// itself -- so nothing else is included. Output order is Helm's install
// order, then template path, so it is deterministic.
func renderChart(values any) ([]byte, error) {
	ch, err := freshChart()
	if err != nil {
		return nil, err
	}

	raw, err := sigsyaml.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("cilium: marshalling Helm values: %w", err)
	}

	vals := map[string]any{}
	if err := sigsyaml.Unmarshal(raw, &vals); err != nil {
		return nil, fmt.Errorf("cilium: reading Helm values: %w", err)
	}

	// Helm's default capabilities, with the Kubernetes version the chart's version-gated templates read.
	kv, err := chartutil.ParseKubeVersion(RenderKubernetesVersion)
	if err != nil {
		return nil, fmt.Errorf("cilium: rendering Kubernetes version: %w", err)
	}

	caps := chartutil.DefaultCapabilities.Copy()
	caps.KubeVersion = *kv

	renderVals, err := chartutil.ToRenderValues(ch, vals, chartutil.ReleaseOptions{
		Name: ChartName, Namespace: namespace, IsInstall: true, Revision: 1,
	}, caps)
	if err != nil {
		return nil, fmt.Errorf("cilium: preparing chart values: %w", err)
	}

	files, err := engine.Render(ch, renderVals)
	if err != nil {
		return nil, fmt.Errorf("cilium: rendering chart: %w", err)
	}

	for name := range files {
		if strings.HasSuffix(name, "NOTES.txt") {
			delete(files, name)
		}
	}

	// Hooks come back separately and are dropped.
	_, manifests, err := releaseutil.SortManifests(files, nil, releaseutil.InstallOrder)
	if err != nil {
		return nil, fmt.Errorf("cilium: ordering chart manifests: %w", err)
	}

	var out strings.Builder

	for _, m := range manifests {
		content, err := withHelmOwnership(m.Content)
		if err != nil {
			return nil, fmt.Errorf("cilium: marking %s as Helm-owned: %w", m.Name, err)
		}

		out.WriteString("---\n# Source: " + m.Name + "\n" + content)
	}

	return []byte(out.String()), nil
}

// Helm's release-ownership metadata (pkg/action/validate.go in Helm): with it on the
// bootstrap objects, the Helm release the CNI Job creates over them adopts
// them without a conflict check, and `helm` tooling reads them as release members.
const (
	// ReleaseName is the Helm release Ichor owns Cilium as; it equals the chart name.
	ReleaseName = ChartName

	// ReleaseNamespace is where that release and its objects live.
	ReleaseNamespace = namespace

	annotationReleaseName      = "meta.helm.sh/release-name"
	annotationReleaseNamespace = "meta.helm.sh/release-namespace"
	labelManagedBy             = "app.kubernetes.io/managed-by"
)

// withHelmOwnership returns doc (one rendered manifest) with the three
// ownership markers set, as Helm sets them when it installs the object.
func withHelmOwnership(doc string) (string, error) {
	doc = strings.TrimSpace(doc)

	var obj map[string]any
	if err := sigsyaml.Unmarshal([]byte(doc), &obj); err != nil {
		return "", err
	}

	if len(obj) == 0 {
		return doc + "\n", nil
	}

	meta, _ := obj["metadata"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
		obj["metadata"] = meta
	}

	for key, set := range map[string]map[string]string{
		"annotations": {annotationReleaseName: ReleaseName, annotationReleaseNamespace: ReleaseNamespace},
		"labels":      {labelManagedBy: "Helm"},
	} {
		m, _ := meta[key].(map[string]any)
		if m == nil {
			m = map[string]any{}
			meta[key] = m
		}

		for k, v := range set {
			m[k] = v
		}
	}

	out, err := sigsyaml.Marshal(obj)
	if err != nil {
		return "", err
	}

	return string(out), nil
}
