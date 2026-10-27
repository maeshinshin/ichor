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

package prmode

import (
	"bytes"
	"fmt"
	"strings"

	yaml "go.yaml.in/yaml/v4"
)

// FieldUpdate is one field to change within the document Patch finds:
// Path is a sequence of mapping keys (no list indices -- see SetNode's
// doc comment), Value is what that field becomes.
type FieldUpdate struct {
	Path  []string
	Value any
	// Merge edits the existing value in place instead of replacing it: only what differs from
	// Value changes, so comments and formatting on the rest stay (a whole-spec update would
	// otherwise lose every comment inside it).
	Merge bool
}

// defaultIndent is detectIndent's fallback for a document with no
// indented, non-comment line to measure (e.g. a single flat mapping).
const defaultIndent = 2

// Patch finds the document in data (a whole file's bytes, `---`-
// delimited documents and all) whose apiVersion/kind/metadata.name/
// metadata.namespace equal target, applies every update to it, and
// returns the file's new bytes.
//
// Every byte outside that one document -- every other document, any
// separator line, any comment or blank line that is not itself
// inside the matched document -- is reused verbatim from data, never
// re-rendered: this package decodes and re-encodes (go.yaml.in/yaml/v4's
// yaml.Node, the only way to change one value without a plain
// unmarshal-to-struct-then-marshal round trip losing comments, key
// order, quote style and anchors) only the one document that actually
// changed. Within that document, go-yaml's Node round trip reproduces
// comments, key order, quote style, anchors/aliases and block/flow
// list style exactly (verified in patch_test.go against fixtures
// carrying all of them) with one known exception: a blank line with no
// comment on it, directly between two sibling keys inside the document
// being edited, is dropped -- a go-yaml library limitation (blank lines
// are only preserved when attached to a comment), not something this
// package works around; TestPatch_BlankLineBetweenKeysIsDropped records
// it so it is never mistaken for a regression.
//
// If no document in data matches target, Patch fails loudly (an error,
// not a silent no-op) -- the same requirement Locate's own doc comment
// explains.
func Patch(data []byte, target Target, updates []FieldUpdate) ([]byte, error) {
	segments := splitDocuments(data)

	for i, seg := range segments {
		doc, err := decodeDocument(seg.content)
		if err != nil {
			return nil, err
		}

		if doc == nil {
			continue
		}

		if !matches(doc, target) {
			continue
		}

		for _, u := range updates {
			if err := setNode(doc, u.Path, u.Value, u.Merge); err != nil {
				return nil, fmt.Errorf("prmode: setting %s: %w", strings.Join(u.Path, "."), err)
			}
		}

		newContent, err := encodeDocument(doc, seg.content)
		if err != nil {
			return nil, err
		}

		var out bytes.Buffer
		for j, s := range segments {
			out.Write(s.sep)

			if j == i {
				out.Write(newContent)
			} else {
				out.Write(s.content)
			}
		}

		return out.Bytes(), nil
	}

	return nil, fmt.Errorf("prmode: no document matches %s %s (namespace %s, name %s)",
		target.APIVersion, target.Kind, target.Namespace, target.Name)
}

// RemoveDocument finds the document in data that equals target, as Patch
// does, and removes just that document: its `---` separator and its bytes.
// Every other byte is kept as it was. remaining is false when no real
// document is left (only comments or nothing), so the caller can delete the
// file instead of committing an empty one. A target that matches nothing is
// an error, as for Patch.
func RemoveDocument(data []byte, target Target) (out []byte, remaining bool, err error) {
	segments := splitDocuments(data)
	idx := -1

	for i, seg := range segments {
		doc, err := decodeDocument(seg.content)
		if err != nil {
			return nil, false, err
		}

		if doc != nil && matches(doc, target) {
			idx = i

			break
		}
	}

	if idx < 0 {
		return nil, false, fmt.Errorf("prmode: no document matches %s %s (namespace %s, name %s)",
			target.APIVersion, target.Kind, target.Namespace, target.Name)
	}

	var buf bytes.Buffer

	for j, s := range segments {
		if j == idx {
			continue
		}

		// The first document went, so the next one becomes the first and
		// loses the separator that introduced it.
		if idx != 0 || j != 1 {
			buf.Write(s.sep)
		}

		buf.Write(s.content)

		doc, err := decodeDocument(s.content)
		if err != nil {
			return nil, false, err
		}

		remaining = remaining || doc != nil
	}

	return buf.Bytes(), remaining, nil
}

// encodeDocument re-renders doc using the same indent width original
// (the document's own, pre-mutation bytes) was written with, so an
// untouched sibling field's indentation does not shift just because a
// different field's value changed.
func encodeDocument(doc *yaml.Node, original []byte) ([]byte, error) {
	var buf bytes.Buffer

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(detectIndent(original))

	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("prmode: encoding document: %w", err)
	}

	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("prmode: encoding document: %w", err)
	}

	return buf.Bytes(), nil
}

// detectIndent returns the leading-space width of the first indented,
// non-comment-only line in data, or defaultIndent if there is none.
// YAML requires one consistent indent unit per nesting level throughout
// a well-formed document, so the first such line already names it.
func detectIndent(data []byte) int {
	for line := range strings.SplitSeq(string(data), "\n") {
		trimmed := strings.TrimLeft(line, " ")
		n := len(line) - len(trimmed)

		if n > 0 && trimmed != "" && trimmed[0] != '#' {
			return n
		}
	}

	return defaultIndent
}

// setNode walks doc (a DocumentNode) following path's mapping keys and
// replaces the leaf's value, preserving that node's own HeadComment/
// LineComment/FootComment and, when the new value's Kind matches the old
// node's, its Style too (so e.g. a single-quoted scalar being replaced
// by another short string stays single-quoted rather than falling back
// to go-yaml's own default style).
//
// path may only name mapping keys, not list indices: every field PR mode
// edits today (TalosCluster.spec.* and siblings) is nested maps, and
// adding index support speculatively is not this unit's job.
// shortcut: no list-index support; add it if a field under a list ever
// needs editing in place.
func setNode(doc *yaml.Node, path []string, newValue any, merge bool) error {
	if len(doc.Content) == 0 {
		return fmt.Errorf("empty document")
	}

	node := doc.Content[0]

	for i, key := range path {
		if node.Kind != yaml.MappingNode {
			return fmt.Errorf("%s is not a mapping", strings.Join(path[:i], "."))
		}

		child := findMappingValue(node, key)
		if child == nil && i == len(path)-1 {
			// Only the leaf may be absent: a field the CR never set (a
			// strict approval adds role, hostname ...). A missing parent is
			// still an error, so a typo cannot invent structure.
			node.Content = append(node.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &yaml.Node{Kind: yaml.ScalarNode})
			child = node.Content[len(node.Content)-1]
		}

		if child == nil {
			return fmt.Errorf("key %q not found at %s", key, strings.Join(path[:i], "."))
		}

		node = child
	}

	replacement := &yaml.Node{}
	if err := replacement.Encode(newValue); err != nil {
		return fmt.Errorf("encoding new value: %w", err)
	}

	if merge && node.Kind != 0 {
		mergeNode(node, replacement)

		return nil
	}

	replacement.HeadComment = node.HeadComment
	replacement.LineComment = node.LineComment
	replacement.FootComment = node.FootComment

	if replacement.Kind == node.Kind {
		replacement.Style = node.Style
	}

	*node = *replacement

	return nil
}

// findMappingValue returns m's value node for key, or nil if m (a
// MappingNode) has no such key.
func findMappingValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}

	return nil
}

// mergeNode makes old equal updated while touching as little as it can: an equal scalar keeps
// its node (style, comments), a changed scalar takes the new value but keeps its comments and
// style, mappings are merged key by key (old order kept, new keys appended, absent keys
// removed) and sequences item by item by position. Anything of a different kind is replaced,
// keeping the old node's own comments.
func mergeNode(old, updated *yaml.Node) {
	switch {
	case old.Kind == yaml.MappingNode && updated.Kind == yaml.MappingNode:
		merged := make([]*yaml.Node, 0, len(updated.Content))

		for i := 0; i+1 < len(old.Content); i += 2 {
			if nv := findMappingValue(updated, old.Content[i].Value); nv != nil {
				mergeNode(old.Content[i+1], nv)

				merged = append(merged, old.Content[i], old.Content[i+1])
			}
		}

		for i := 0; i+1 < len(updated.Content); i += 2 {
			if findMappingValue(old, updated.Content[i].Value) == nil {
				merged = append(merged, updated.Content[i], updated.Content[i+1])
			}
		}

		old.Content = merged
	case old.Kind == yaml.SequenceNode && updated.Kind == yaml.SequenceNode:
		n := min(len(old.Content), len(updated.Content))
		for i := range n {
			mergeNode(old.Content[i], updated.Content[i])
		}

		old.Content = append(old.Content[:n], updated.Content[n:]...)
	case old.Kind == yaml.ScalarNode && updated.Kind == yaml.ScalarNode:
		if old.ShortTag() == updated.ShortTag() && old.Value == updated.Value {
			return
		}

		style := old.Style
		old.Tag, old.Value, old.Style = updated.Tag, updated.Value, updated.Style

		if old.ShortTag() == updated.ShortTag() && style != 0 && updated.Style == 0 {
			old.Style = style
		}
	default:
		head, line, foot := old.HeadComment, old.LineComment, old.FootComment
		*old = *updated
		old.HeadComment, old.LineComment, old.FootComment = head, line, foot
	}
}
