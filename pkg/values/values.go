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

// Package values is the Helm-values engine shared by the components Ichor
// installs as Helm charts (ADR 0099, decisions 3 and 4): the merge order
// (Ichor defaults, user values, Ichor-owned keys), owned-key rejection, an
// unknown-key check against the chart's own values.yaml and schema, schema
// validation and a size cap. It knows nothing about any one chart.
package values

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	sigsyaml "sigs.k8s.io/yaml"
)

// MaxSize is the cap on merged values (ADR 0099, decision 4.5): they are
// stored on a CR and rendered into a Job.
const MaxSize = 64 << 10

// Spec describes the rules of one chart.
type Spec struct {
	// Chart is the loaded chart; its values.yaml and values.schema.json are
	// the authority for the unknown-key and schema checks.
	Chart *chart.Chart
	// Owned maps a dotted path (for example "cgroup.autoMount.enabled") to
	// the reason users may not set it.
	Owned map[string]string
	// Schema is Chart's values.schema.json compiled once by CompileSchema. When
	// set, CheckSchema validates against it; when nil it compiles the schema
	// on every call, as chartutil does, which is most of a check's cost.
	Schema *jsonschema.Schema
}

// schemaURL is the resource URL Helm compiles values.schema.json under, so
// the validation messages match Helm's.
const schemaURL = "file:///values.schema.json"

// CompileSchema compiles ch's values.schema.json for Spec.Schema the way Helm
// compiles it (same resource URL, so the same messages). It returns nil for a
// chart without a schema. A chart with subcharts is refused: their schemas
// would be skipped.
func CompileSchema(ch *chart.Chart) (*jsonschema.Schema, error) {
	if ch.Schema == nil {
		return nil, nil
	}

	if len(ch.Dependencies()) > 0 {
		return nil, fmt.Errorf("%s: compiling a schema with subcharts is not supported", ch.Name())
	}

	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(ch.Schema))
	if err != nil {
		return nil, fmt.Errorf("reading %s values.schema.json: %w", ch.Name(), err)
	}

	c := jsonschema.NewCompiler()
	if err := c.AddResource(schemaURL, doc); err != nil {
		return nil, fmt.Errorf("adding %s values.schema.json: %w", ch.Name(), err)
	}

	schema, err := c.Compile(schemaURL)
	if err != nil {
		return nil, fmt.Errorf("compiling %s values.schema.json: %w", ch.Name(), err)
	}

	return schema, nil
}

// merge returns base with over merged on top, leaving both untouched: maps
// merge key by key, any other value (lists included) in over replaces the one
// in base. Helm's MergeTables treats its first argument as the winner and
// mutates it, so both sides are copied by a JSON round trip (which also turns
// Go numbers into float64, what the values come in as anyway).
func merge(base, over map[string]any) map[string]any {
	return chartutil.MergeTables(Clone(over), Clone(base))
}

// Clone returns a deep copy of m by a JSON round trip (values are JSON-shaped: they come from a CR
// or a chart); a nil m gives an empty map.
func Clone(m map[string]any) map[string]any {
	out := map[string]any{}
	raw, _ := json.Marshal(m)

	_ = json.Unmarshal(raw, &out)

	return out
}

// Effective is merge(merge(defaults, user), owned): the order of ADR 0099
// decision 3. Owned values win over anything the user set.
func Effective(defaults, user, owned map[string]any) map[string]any {
	return merge(merge(defaults, user), owned)
}

// Set puts v at the dotted path in m, creating the maps on the way.
func Set(m map[string]any, path string, v any) {
	keys := strings.Split(path, ".")
	for _, k := range keys[:len(keys)-1] {
		next, ok := m[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[k] = next
		}

		m = next
	}

	m[keys[len(keys)-1]] = v
}

// Get returns the value at the dotted path.
func Get(m map[string]any, path string) (any, bool) {
	var cur any = m

	for k := range strings.SplitSeq(path, ".") {
		cm, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}

		if cur, ok = cm[k]; !ok {
			return nil, false
		}
	}

	return cur, true
}

// Delete removes the value at the dotted path, if present.
func Delete(m map[string]any, path string) {
	keys := strings.Split(path, ".")
	for _, k := range keys[:len(keys)-1] {
		next, ok := m[k].(map[string]any)
		if !ok {
			return
		}

		m = next
	}

	delete(m, keys[len(keys)-1])
}

// CheckOwned returns one error per Owned key the user set, each naming the key
// and the reason.
func (s Spec) CheckOwned(user map[string]any) []error {
	var errs []error

	for _, path := range slices.Sorted(maps.Keys(s.Owned)) {
		if _, ok := Get(user, path); ok {
			errs = append(errs, fmt.Errorf("%s is set by Ichor and cannot be overridden: %s", path, s.Owned[path]))
		}
	}

	return errs
}

// CheckUnknown returns one error per user key that is in neither the chart's
// values.yaml nor its schema, with a "did you mean" when a sibling key is
// close. The schema allows extra properties, so this is what catches typos.
// Free-form maps (an object the schema leaves without properties, or an empty
// or null default such as annotations) are exempt.
func (s Spec) CheckUnknown(user map[string]any) []error {
	var schema map[string]any

	_ = json.Unmarshal(s.Chart.Schema, &schema) // a missing or broken schema just narrows the check

	var errs []error

	walkUnknown(user, s.Chart.Values, schema, "", &errs)

	return errs
}

func walkUnknown(user, defaults, schema map[string]any, prefix string, errs *[]error) {
	props, _ := schema["properties"].(map[string]any)

	for _, k := range slices.Sorted(maps.Keys(user)) {
		path := prefix + k

		_, inDefaults := defaults[k]
		_, inSchema := props[k]

		if !inDefaults && !inSchema {
			msg := fmt.Sprintf("unknown key %s", path)
			if sugg := closest(k, siblings(defaults, props)); sugg != "" {
				msg += fmt.Sprintf(" (did you mean %s%s?)", prefix, sugg)
			}

			*errs = append(*errs, errors.New(msg))

			continue
		}

		um, ok := user[k].(map[string]any)
		if !ok {
			continue
		}

		dm, _ := defaults[k].(map[string]any)
		sm, _ := props[k].(map[string]any)

		if freeFormNames[k] || labelKeyed(dm) || isFreeForm(dm, inDefaults, sm) {
			continue
		}

		walkUnknown(um, dm, sm, path+".", errs)
	}
}

// freeFormNames are map keys that hold Kubernetes resource quantities in every
// chart, even when the chart ships a non-empty default (Cilium's cni.resources
// lists only requests, but limits are legal). Labels and annotations need no
// entry: their defaults are empty, bare objects or label-keyed.
// ponytail: a name heuristic; a chart-specific exempt list can replace it if it
// ever hides a real typo.
var freeFormNames = map[string]bool{"resources": true}

// labelKeyed reports a default map whose keys look like label keys
// ("kubernetes.io/os"): such a map is a label set, so any key is allowed.
func labelKeyed(defaults map[string]any) bool {
	for k := range defaults {
		if strings.ContainsAny(k, "/.") {
			return true
		}
	}

	return false
}

// isFreeForm reports a map that takes arbitrary keys: the schema describes it
// as a bare object, or (with no schema) its default is empty or null.
func isFreeForm(defaults map[string]any, inDefaults bool, schema map[string]any) bool {
	if schema != nil {
		_, hasProps := schema["properties"]

		return !hasProps
	}

	return inDefaults && len(defaults) == 0
}

func siblings(defaults, props map[string]any) []string {
	set := map[string]any{}
	maps.Copy(set, defaults)
	maps.Copy(set, props)

	return slices.Sorted(maps.Keys(set))
}

// closest returns the candidate within edit distance 2 of k (1 for short
// keys), or "".
func closest(k string, candidates []string) string {
	limit := 2
	if len(k) <= 4 {
		limit = 1
	}

	best, bestD := "", limit+1

	for _, c := range candidates {
		if d := editDistance(strings.ToLower(k), strings.ToLower(c)); d < bestD {
			best, bestD = c, d
		}
	}

	return best
}

func editDistance(a, b string) int {
	row := make([]int, len(b)+1)
	for j := range row {
		row[j] = j
	}

	for i := range len(a) {
		diag := row[0] // row[j-1] of the previous pass
		row[0] = i + 1

		for j := range len(b) {
			cost := 1
			if a[i] == b[j] {
				cost = 0
			}

			diag, row[j+1] = row[j+1], min(row[j+1]+1, row[j]+1, diag+cost)
		}
	}

	return row[len(b)]
}

// CheckSchema validates effective, coalesced with the chart's own defaults the
// way Helm does at render time, against the chart's values.schema.json.
func (s Spec) CheckSchema(effective map[string]any) error {
	coalesced, err := chartutil.CoalesceValues(s.Chart, effective)
	if err != nil {
		return fmt.Errorf("combining values with the chart defaults: %w", err)
	}

	if s.Schema == nil {
		if err := chartutil.ValidateAgainstSchema(s.Chart, coalesced); err != nil {
			return fmt.Errorf("values do not match the chart schema: %w", err)
		}

		return nil
	}

	// The same message as chartutil's: the chart's name, then the validator's
	// report without its leading line.
	if err := s.Schema.Validate(map[string]any(coalesced)); err != nil {
		report := strings.TrimPrefix(err.Error(), "jsonschema validation failed with '"+schemaURL+"#'\n")

		return errors.New("values do not match the chart schema: " + s.Chart.Name() + ":\n" + report + "\n")
	}

	return nil
}

// CheckSize fails when the YAML of v exceeds MaxSize.
func CheckSize(v map[string]any) error {
	raw, err := sigsyaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshalling values: %w", err)
	}

	if len(raw) > MaxSize {
		return fmt.Errorf("values are %d bytes, over the %d byte limit", len(raw), MaxSize)
	}

	return nil
}

// Check runs the owned-key, unknown-key, schema and size checks on user, with
// effective being the merged result, and joins everything it finds.
func (s Spec) Check(user, effective map[string]any) error {
	errs := s.CheckOwned(user)
	errs = append(errs, s.CheckUnknown(user)...)

	if err := s.CheckSchema(effective); err != nil {
		errs = append(errs, err)
	}

	if err := CheckSize(effective); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}
