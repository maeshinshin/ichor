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

package typedconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	mmeta "github.com/siderolabs/talos/pkg/machinery/config/types/meta"
	yaml "go.yaml.in/yaml/v4"
)

// This file is the substitution table: the handful of
// machinery field types that have no faithful structural-schema
// representation (no exported fields, or a custom marshaller that changes
// the wire shape on write), keyed by Go type in
// internal/tools/mirrorgen/main.go. Each one round-trips through
// machinery's own YAML or TextMarshaler/TextUnmarshaler implementation via
// yaml.Node, so Talos keeps defining what the string actually means --
// mirrorgen only has to know that the field is a string (or []string for
// meta.ArgValue), not what's inside it.

// machineryToMirrorScalar renders v exactly as machinery's own YAML
// marshaller would and keeps just the scalar text, e.g. "2.5 GB" for a
// DiskSize, "0o755" for a FileMode, "1h30m0s" for a time.Duration. v is
// addressed internally so that both pointer- and value-receiver marshallers
// are found.
func machineryToMirrorScalar[T any](v T) (string, error) {
	var node yaml.Node

	if err := node.Encode(&v); err != nil {
		return "", fmt.Errorf("encoding %T: %w", v, err)
	}

	return node.Value, nil
}

// mirrorToMachineryScalar parses s the same way machinery's own YAML
// unmarshaller would for type T.
func mirrorToMachineryScalar[T any](s string) (T, error) {
	var out T

	node := yaml.Node{Kind: yaml.ScalarNode, Value: s}
	if err := node.Decode(&out); err != nil {
		return out, fmt.Errorf("parsing %q as %T: %w", s, out, err)
	}

	return out, nil
}

// argValueFromMachinery mirrors meta.ArgValue, which machinery marshals as
// either a single string or a list of strings depending on how it was
// constructed. A one-element result always round-trips as the scalar form
// (the common case in real Talos configs, e.g. extraArgs); anything else
// round-trips as a list.
func argValueFromMachinery(v mmeta.ArgValue) ([]string, error) {
	var node yaml.Node

	if err := node.Encode(&v); err != nil {
		return nil, fmt.Errorf("encoding arg value: %w", err)
	}

	if node.Kind == yaml.SequenceNode {
		var out []string
		if err := node.Decode(&out); err != nil {
			return nil, fmt.Errorf("decoding arg value list: %w", err)
		}

		return out, nil
	}

	return []string{node.Value}, nil
}

// mirrorToMachineryUnstructured renders v -- one of the five field-override
// types in internal/tools/mirrorgen/main.go's fieldOverrides table -- through
// its own JSON tags into the map[string]any shape machinery's
// meta.Unstructured carries on the wire. Unlike the scalar substitutions
// above, these fields have no faithful machinery-side type to round-trip
// through (that's the whole reason they were meta.Unstructured); JSON is the
// shape every upstream schema here (Kubernetes, OCI, kubelet) already
// commits to.
func mirrorToMachineryUnstructured[T any](v T) (mmeta.Unstructured, error) {
	obj, err := jsonConvert[map[string]any](v)

	return mmeta.Unstructured{Object: obj}, err
}

// machineryToMirrorUnstructured is mirrorToMachineryUnstructured's inverse.
func machineryToMirrorUnstructured[T any](u mmeta.Unstructured) (T, error) {
	return jsonConvert[T](u.Object)
}

// machineryToMirrorSubset reads u into T, a local subset of an upstream type (the mirror structs ending in
// Subset), and refuses a field T does not carry: a subset leaves fields out on purpose, and a document that
// sets one must be told so rather than have it dropped.
func machineryToMirrorSubset[T any](u mmeta.Unstructured) (out T, err error) {
	data, err := json.Marshal(u.Object)
	if err != nil {
		return out, fmt.Errorf("encoding %T: %w", u.Object, err)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&out); err != nil {
		return out, fmt.Errorf("the document sets a field Ichor does not carry: %w", err)
	}

	return out, nil
}

// refuseSet is the error for a document that sets field, a typed field the mirror does not carry because Ichor
// owns what it sets; why says what owns it. An unset value is no error.
func refuseSet(field string, v any, why string) error {
	if reflect.ValueOf(v).IsZero() {
		return nil
	}

	return fmt.Errorf("%s is set, and Ichor does not carry it: %s", field, why)
}

// jsonConvert re-reads in as Out through its JSON form.
func jsonConvert[Out any](in any) (out Out, err error) {
	data, err := json.Marshal(in)
	if err != nil {
		return out, fmt.Errorf("encoding %T: %w", in, err)
	}

	if err := json.Unmarshal(data, &out); err != nil {
		return out, fmt.Errorf("decoding %T as %T: %w", in, out, err)
	}

	return out, nil
}

func argValueToMachinery(s []string) (mmeta.ArgValue, error) {
	var (
		node yaml.Node
		out  mmeta.ArgValue
	)

	if len(s) == 1 {
		node = yaml.Node{Kind: yaml.ScalarNode, Value: s[0]}
	} else if err := node.Encode(s); err != nil {
		return out, fmt.Errorf("encoding arg value list: %w", err)
	}

	if err := node.Decode(&out); err != nil {
		return out, fmt.Errorf("decoding arg value: %w", err)
	}

	return out, nil
}
