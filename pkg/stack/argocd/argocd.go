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

// Package argocd renders the pinned Argo CD Helm chart into plain manifests
// for Ichor's standard stack, the same way pkg/cni/cilium renders
// Cilium: embedded chart, Helm's Go SDK, no cluster, no network.
package argocd

import (
	"bytes"
	_ "embed" // the pinned chart archive
	"errors"
	"fmt"
	"maps"
	"strings"

	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
	"helm.sh/helm/v3/pkg/releaseutil"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/maeshinshin/ichor/pkg/stack/kokumi"
	"github.com/maeshinshin/ichor/pkg/stack/policies"
)

const (
	// ChartName and ChartVersion pin the argo-cd chart (app version AppVersion).
	ChartName    = "argo-cd"
	ChartVersion = "10.11.6"
	// AppVersion is the Argo CD release the chart installs.
	AppVersion = "v3.5.6"
	// Namespace and ReleaseName are where and as what the chart renders.
	Namespace   = "argocd"
	ReleaseName = "argocd"
)

// chartArchive is argo-cd-10.11.6.tgz from
// https://github.com/argoproj/argo-helm/releases/download/argo-cd-10.11.6/argo-cd-10.11.6.tgz
// (sha256 d8f096d87513b41ff754053a7f9604c943bf29c15436a79226fd51595ea67d05),
// fetched and verified by `make argocd-chart`.
//
//go:embed chart/argo-cd-10.11.6.tgz
var chartArchive []byte

// Options are the inputs to Render.
type Options struct {
	// Values is the user layer, deep-merged over Ichor's defaults.
	Values map[string]any
	// RedisPassword fills the argocd-redis Secret. The chart would create it
	// with a hook Job, but hooks are dropped (the output is applied as plain
	// manifests, and Kokumi/Argo CD would re-run a Job), so Ichor renders it.
	RedisPassword string
	// OmitPolicies leaves the CiliumNetworkPolicies out
	// of the output, for the first apply, which runs before they may exist.
	// The published bundle keeps them so Argo CD does not prune them.
	OmitPolicies bool
}

// DefaultValues is the minimal Argo CD: no dex, no notifications, no
// ApplicationSet controller, no Redis HA. It also ships what Kokumi's
// Applications need: the `default` AppProject and the OCI repo-creds Secret
// for Kokumi's plain-HTTP registry.
func DefaultValues(redisPassword string) map[string]any {
	secret := func(name string, labels, data map[string]any) map[string]any {
		return extra("v1", "Secret", name, map[string]any{
			"type": "Opaque", "stringData": data, keyMetadata: map[string]any{"labels": labels},
		})
	}

	return map[string]any{
		"dex":             off(),
		"notifications":   off(),
		"redis-ha":        off(),
		"redisSecretInit": off(),
		"crds":            map[string]any{"keep": true},
		// The chart's Kubernetes NetworkPolicies add to the CiliumNetworkPolicies and its
		// argocd-server one allows all ingress; the Cilium ones (pkg/stack/policies) replace them.
		"global": map[string]any{"networkPolicy": map[string]any{"create": false}},
		"extraObjects": []any{
			extra("argoproj.io/v1alpha1", "AppProject", "default", map[string]any{
				"spec": map[string]any{
					"clusterResourceWhitelist": []any{map[string]any{"group": "*", "kind": "*"}},
					"destinations":             []any{map[string]any{"namespace": "*", "server": "*"}},
					"sourceRepos":              []any{"*"},
				},
			}),
			secret("ichor-kokumi-registry", map[string]any{"argocd.argoproj.io/secret-type": "repo-creds"},
				map[string]any{"type": "oci", "url": "oci://" + kokumi.RegistryHost, "insecureOCIForceHttp": "true"}),
			secret("argocd-redis", map[string]any{}, map[string]any{"auth": redisPassword}),
		},
	}
}

const keyMetadata = "metadata"

func off() map[string]any { return map[string]any{"enabled": false} }

// extra builds one namespaced object for the chart's extraObjects; fields are
// merged over the header, with fields[keyMetadata] merged into the metadata.
func extra(apiVersion, kind, name string, fields map[string]any) map[string]any {
	meta := map[string]any{"name": name, "namespace": Namespace}

	if m, ok := fields[keyMetadata].(map[string]any); ok {
		maps.Copy(meta, m)
	}

	o := map[string]any{"apiVersion": apiVersion, "kind": kind, keyMetadata: meta}
	for k, v := range fields {
		if k != keyMetadata {
			o[k] = v
		}
	}

	return o
}

// Render expands the embedded chart with Ichor's defaults and opts.Values into
// one multi-document YAML stream: Helm's install order, then template path,
// hooks and NOTES dropped. The output is deterministic.
func Render(opts Options) ([]byte, error) {
	if opts.RedisPassword == "" {
		return nil, errors.New("argocd: RedisPassword is required")
	}

	ch, err := loader.LoadArchive(bytes.NewReader(chartArchive))
	if err != nil {
		return nil, fmt.Errorf("argocd: loading embedded chart: %w", err)
	}

	// Round-trip through YAML so the user layer holds plain JSON-ish types.
	vals, err := roundTrip(opts.Values)
	if err != nil {
		return nil, err
	}

	defaults, err := roundTrip(DefaultValues(opts.RedisPassword))
	if err != nil {
		return nil, err
	}

	merged := chartutil.CoalesceTables(vals, defaults)

	// engine.Render does not evaluate subchart conditions; this is what makes
	// redis-ha.enabled=false drop the subchart.
	if err := chartutil.ProcessDependencies(ch, merged); err != nil {
		return nil, fmt.Errorf("argocd: processing chart dependencies: %w", err)
	}

	caps := *chartutil.DefaultCapabilities
	caps.KubeVersion = chartutil.KubeVersion{Version: "v1.34.0", Major: "1", Minor: "34"}

	renderVals, err := chartutil.ToRenderValues(ch, merged, chartutil.ReleaseOptions{
		Name: ReleaseName, Namespace: Namespace, IsInstall: true, Revision: 1,
	}, &caps)
	if err != nil {
		return nil, fmt.Errorf("argocd: preparing chart values: %w", err)
	}

	files, err := engine.Render(ch, renderVals)
	if err != nil {
		return nil, fmt.Errorf("argocd: rendering chart: %w", err)
	}

	for name := range files {
		// Chart 10.11.6 has no applicationSet.enabled switch, so the controller's templates are dropped here.
		if strings.HasSuffix(name, "NOTES.txt") || strings.Contains(name, "/argocd-applicationset/") {
			delete(files, name)
		}
	}

	// Hooks come back separately and are dropped.
	_, manifests, err := releaseutil.SortManifests(files, nil, releaseutil.InstallOrder)
	if err != nil {
		return nil, fmt.Errorf("argocd: ordering chart manifests: %w", err)
	}

	var out strings.Builder

	for _, m := range manifests {
		out.WriteString("---\n# Source: " + m.Name + "\n" + strings.TrimSpace(m.Content) + "\n")
	}

	if opts.OmitPolicies {
		return []byte(out.String()), nil
	}

	return policies.Append([]byte(out.String()), policies.Argocd), nil
}

func roundTrip(in map[string]any) (map[string]any, error) {
	raw, err := sigsyaml.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("argocd: marshalling Helm values: %w", err)
	}

	out := map[string]any{}
	if err := sigsyaml.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("argocd: reading Helm values: %w", err)
	}

	return out, nil
}
