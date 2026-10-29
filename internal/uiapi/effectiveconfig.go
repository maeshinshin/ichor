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

package uiapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/siderolabs/talos/pkg/machinery/config/container"
	"github.com/siderolabs/talos/pkg/machinery/config/encoder"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	yaml "go.yaml.in/yaml/v4"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
	pb "github.com/maeshinshin/ichor/gen/go/ichor/v1alpha1"
	"github.com/maeshinshin/ichor/pkg/render"
)

// secretMask is the fixed replacement EffectiveConfigValue.value carries for
// every secret field (node.proto's own doc comment: "a fixed mask
// (\"***\")"). It is also the string passed to machinery's own
// config.Provider.RedactSecrets, so the mask on the wire and the mask
// machinery applies are the same literal -- one constant, not two that
// could drift apart.
const secretMask = "***"

// effectiveConfigValues renders machine's complete configuration and
// returns it as the flat, origin-annotated, secret-masked value list
// node.proto's EffectiveConfig carries.
//
// Masking (docs/adr/0018): machinery's own config.Provider.RedactSecrets
// already knows, per config kind, exactly which fields are secret --
// tokens, CA keys (never certs: those stay public), the cluster
// bootstrap/discovery secret, aescbc/secretbox encryption secrets,
// registry auth, wireguard keys, disk-encryption passphrases, and more
// (confirmed by reading
// config/types/v1alpha1/v1alpha1_redact_test.go in the vendored module).
// Re-deriving that list by hand as a path denylist would both duplicate
// machinery's own authority on its config shape and -- worse -- silently
// fall behind every time machinery adds a new secret-bearing field. So
// this function never classifies a path as secret itself: it renders the
// config twice, once plain and once through RedactSecrets, and calls any
// leaf where the two differ secret, taking the redacted copy's value
// (never the plain one) for the wire. A leaf present in neither rendering
// never reaches the browser; a value is only ever read from the redacted
// tree when secret is true.
//
// Origin (docs/design/architecture.md "Patch layers"): a leaf's origin is
// the last (bottom-up, so highest-priority) applicable TalosConfigPatch
// whose own documents -- rendered in isolation via render.DocumentFromPatch,
// never diffed against the merged tree -- set that exact path. A leaf no
// patch sets came from Ichor's own base generation (the "system" layer).
func effectiveConfigValues(
	cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine,
	bundle *secrets.Bundle, candidates []ichorv1alpha1.TalosConfigPatch,
	sys *render.SystemInputs,
) ([]*pb.EffectiveConfigValue, error) {
	cfg, err := render.BuildConfig(cluster, m, bundle, candidates, sys)
	if err != nil {
		return nil, err
	}

	plain, err := encodeLeaves(cfg)
	if err != nil {
		return nil, fmt.Errorf("encoding rendered configuration: %w", err)
	}

	redacted, err := encodeLeaves(cfg.RedactSecrets(secretMask))
	if err != nil {
		return nil, fmt.Errorf("encoding redacted configuration: %w", err)
	}

	origin, err := patchOrigins(cluster, m, candidates)
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(plain))
	for p := range plain {
		paths = append(paths, p)
	}

	slices.Sort(paths)

	values := make([]*pb.EffectiveConfigValue, 0, len(paths))

	for _, path := range paths {
		value := plain[path]

		secret := false
		if r, ok := redacted[path]; ok && r != value {
			secret = true
			value = r
		}

		o, ok := origin[path]
		if !ok {
			o = &pb.ConfigOrigin{Layer: pb.ConfigLayer_CONFIG_LAYER_SYSTEM, Source: "system"}
		}

		values = append(values, &pb.EffectiveConfigValue{
			Path: path, Value: value, Secret: secret, Origin: o,
		})
	}

	return values, nil
}

// encodeLeaves renders cfg to YAML and flattens it to path -> JSON-encoded
// leaf value (node.proto: "value is the JSON-encoded rendered value").
func encodeLeaves(cfg interface {
	EncodeBytes(...encoder.Option) ([]byte, error)
}) (map[string]string, error) {
	out, err := cfg.EncodeBytes(encoder.WithComments(encoder.CommentsDisabled))
	if err != nil {
		return nil, err
	}

	return leafPaths(out)
}

// leafPaths decodes a "---"-separated multi-document YAML stream (the
// shape pkg/render and machinery's own container encoder produce) into a
// flat path -> JSON-encoded value map, keyed the way pkg/fieldmeta expects:
// rooted at "machine"/"cluster" for the legacy v1alpha1.Config document
// (identified, like internal/controller's own decodeDocs, by having no
// "kind" field), or at the document's own kind for a standalone document
// such as SysctlConfig or KernelModuleConfig. Only maps are recursed into;
// a list or scalar is one leaf, matching pkg/fieldmeta's and
// internal/controller/diff.go's own granularity.
func leafPaths(b []byte) (map[string]string, error) {
	leaves := make(map[string]string)
	dec := yaml.NewDecoder(bytes.NewReader(b))

	for {
		var doc map[string]any

		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, err
		}

		if doc == nil {
			continue
		}

		root, _ := doc["kind"].(string)
		if err := walkLeaves(root, doc, leaves); err != nil {
			return nil, err
		}
	}

	return leaves, nil
}

func walkLeaves(prefix string, v any, out map[string]string) error {
	m, ok := v.(map[string]any)
	if !ok {
		enc, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("encoding value at %q: %w", prefix, err)
		}

		out[prefix] = string(enc)

		return nil
	}

	for k, sub := range m {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}

		if err := walkLeaves(path, sub, out); err != nil {
			return err
		}
	}

	return nil
}

// patchOrigins maps every leaf path set by any patch applicable to m to
// that patch's ConfigOrigin, applied in render.SelectPatches' own
// bottom-up, then-priority order so a later patch's claim on a path
// overwrites an earlier one's -- the same "later patch wins" rule
// pkg/render's own applyPatches doc comment states for the merge itself.
func patchOrigins(
	cluster *ichorv1alpha1.TalosCluster, m *ichorv1alpha1.TalosMachine, candidates []ichorv1alpha1.TalosConfigPatch,
) (map[string]*pb.ConfigOrigin, error) {
	ordered, err := render.SelectPatches(cluster, m, candidates)
	if err != nil {
		return nil, err
	}

	origin := make(map[string]*pb.ConfigOrigin)

	for _, p := range ordered {
		layer := configLayerToPB(p.Spec.Layer)

		for _, d := range p.Spec.Documents {
			paths, err := documentLeafPaths(d)
			if err != nil {
				return nil, fmt.Errorf("patch %q: %w", p.Name, err)
			}

			for path := range paths {
				origin[path] = &pb.ConfigOrigin{Layer: layer, Source: p.Name}
			}
		}
	}

	return origin, nil
}

// documentLeafPaths renders one TalosConfigPatchDocument in isolation --
// never merged with any other patch or the base config, the same
// render.DocumentFromPatch call patchOrigins always used -- and flattens
// it to the field paths it sets. Shared by patchOrigins above (origin
// attribution) and patch_service.go's PreviewImpact (pkg/fieldmeta
// classification): both need exactly "what paths does this one document
// set," computed once rather than twice.
func documentLeafPaths(d ichorv1alpha1.TalosConfigPatchDocument) (map[string]string, error) {
	doc, err := render.DocumentFromPatch(d)
	if err != nil {
		return nil, err
	}

	c, err := container.New(doc)
	if err != nil {
		return nil, err
	}

	return encodeLeaves(c)
}

// configLayerToPB converts a TalosConfigPatchSpec.layer value to the wire
// ConfigLayer enum. "system" is deliberately absent: it is never a
// TalosConfigPatch value (pkg/render's own layerOrder comment), only the
// bucket effectiveConfigValues falls back to for a leaf no patch claims.
func configLayerToPB(layer string) pb.ConfigLayer {
	switch layer {
	case "cluster":
		return pb.ConfigLayer_CONFIG_LAYER_CLUSTER
	case fieldRole:
		return pb.ConfigLayer_CONFIG_LAYER_ROLE
	case "selector":
		return pb.ConfigLayer_CONFIG_LAYER_SELECTOR
	case "machine":
		return pb.ConfigLayer_CONFIG_LAYER_MACHINE
	default:
		return pb.ConfigLayer_CONFIG_LAYER_UNSPECIFIED
	}
}
