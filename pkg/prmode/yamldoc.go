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
	"fmt"
	"regexp"

	yaml "go.yaml.in/yaml/v4"
)

// Target identifies one CR within a YAML file -- a file can hold more
// than one document, and more than one CR, separated by "---"
// (architecture.md's test requirement: edit only the right one).
type Target struct {
	APIVersion string
	Kind       string
	Namespace  string
	Name       string
}

// typeMeta is the subset of a Kubernetes object this package needs to
// decide whether a YAML document is Target: not api/v1alpha1's own
// types, since a document here could in principle be any kind this
// project's CRDs don't cover, and decoding just these four fields is all
// matching needs.
type typeMeta struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
}

// docSeparator matches a YAML document-start marker at the beginning of
// a line: "---" alone, or followed by trailing whitespace or a comment.
// Per the YAML spec, block scalar content is always indented at least
// one column past the key that introduces it, so a column-0 "---" line
// can never be scalar content -- only ever a real stream-level marker --
// which is what makes splitting on it safe without a full YAML lexer.
//
// shortcut: this does not account for "---" appearing at column 0 inside
// an open flow collection spanning multiple lines (legal YAML, but not a
// shape real Kubernetes manifests use); upgrade to a lexer-aware split
// if that ever needs to work.
var docSeparator = regexp.MustCompile(`(?m)^---[ \t]*(#.*)?\r?\n`)

// docSegment is one `---`-delimited document's raw bytes, kept apart
// from any separator line that introduced it (sep) so Patch can splice a
// re-encoded Content back into the file without ever re-rendering (and
// so risking reformatting) a separator line, or any other document's
// bytes.
type docSegment struct {
	// sep is the raw "---...\n" line that precedes Content, or empty
	// for the file's first document when it has no leading separator.
	sep []byte
	// content is this document's own raw bytes, with sep removed.
	content []byte
}

// splitDocuments splits data into its `---`-delimited documents,
// preserving every byte: concatenating every segment's sep+content in
// order reproduces data exactly.
func splitDocuments(data []byte) []docSegment {
	matches := docSeparator.FindAllIndex(data, -1)

	segments := make([]docSegment, 0, len(matches)+1)
	segments = append(segments, docSegment{content: data[:firstStart(matches, len(data))]})

	for i, m := range matches {
		end := len(data)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}

		segments = append(segments, docSegment{sep: data[m[0]:m[1]], content: data[m[1]:end]})
	}

	return segments
}

// firstStart returns the start offset of matches' first match, or
// fallback (the whole data) if there are none.
func firstStart(matches [][]int, fallback int) int {
	if len(matches) == 0 {
		return fallback
	}

	return matches[0][0]
}

// decodeDocument parses seg's content as a single YAML document, or nil
// if the document is empty (an all-comments or whitespace-only segment,
// legal between two separators).
func decodeDocument(content []byte) (*yaml.Node, error) {
	var node yaml.Node
	if err := yaml.Unmarshal(content, &node); err != nil {
		return nil, fmt.Errorf("prmode: parsing YAML document: %w", err)
	}

	if len(node.Content) == 0 {
		return nil, nil
	}

	return &node, nil
}

// matches reports whether doc's top-level apiVersion/kind/metadata.name/
// metadata.namespace equal target. doc is the DocumentNode
// decodeDocument returns (never nil on the path matches is called from).
// Not every YAML document in a repository is a Kubernetes object (a
// kustomization.yaml, a values file); failing to decode as one just
// means "not a match", never an error -- so this never fails.
func matches(doc *yaml.Node, target Target) bool {
	var tm typeMeta
	if err := doc.Decode(&tm); err != nil {
		return false
	}

	return tm.APIVersion == target.APIVersion &&
		tm.Kind == target.Kind &&
		tm.Metadata.Name == target.Name &&
		tm.Metadata.Namespace == target.Namespace
}
