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
	"bytes"
	_ "embed" // the pinned chart archive
	"fmt"
	"strings"

	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
	"helm.sh/helm/v3/pkg/releaseutil"
	sigsyaml "sigs.k8s.io/yaml"
)

// chartArchive is the pinned upstream chart (ChartVersion), checked in under
// chart/ and fetched, digest-verified, by `make cilium-chart`. It
// is embedded so rendering needs no network and is reproducible.
//
//go:embed chart/cilium-1.18.0.tgz
var chartArchive []byte

// renderChart expands the embedded chart with values into one multi-document
// YAML stream, like `helm template --namespace kube-system` without hooks
// (Talos applies the result directly, so Helm hooks have no meaning). The
// chart ships no crds/ directory -- the operator installs Cilium's CRDs
// itself -- so nothing else is included. Output order is Helm's install
// order, then template path, so it is deterministic.
func renderChart(values any) ([]byte, error) {
	ch, err := loader.LoadArchive(bytes.NewReader(chartArchive))
	if err != nil {
		return nil, fmt.Errorf("cilium: loading embedded chart: %w", err)
	}

	raw, err := sigsyaml.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("cilium: marshalling Helm values: %w", err)
	}

	vals := map[string]any{}
	if err := sigsyaml.Unmarshal(raw, &vals); err != nil {
		return nil, fmt.Errorf("cilium: reading Helm values: %w", err)
	}

	renderVals, err := chartutil.ToRenderValues(ch, vals, chartutil.ReleaseOptions{
		Name: ChartName, Namespace: namespace, IsInstall: true, Revision: 1,
	}, chartutil.DefaultCapabilities)
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
		out.WriteString("---\n# Source: " + m.Name + "\n" + strings.TrimSpace(m.Content) + "\n")
	}

	return []byte(out.String()), nil
}
