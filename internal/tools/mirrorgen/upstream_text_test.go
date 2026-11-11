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

package main

import (
	"bytes"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	// minSentenceLen and minSentenceWords keep short generic phrases out of the corpus.
	minSentenceLen   = 40
	minSentenceWords = 6
	machineryModule  = "github.com/siderolabs/talos/pkg/machinery"
)

var (
	nonWord     = regexp.MustCompile(`[^\p{L}\p{N}]+`)
	sentenceEnd = regexp.MustCompile(`[.!?](?:\s+|$)`)
	// docgenKey is a key of the mini-YAML in machinery's doc comments; only the text after it is prose.
	docgenKey = regexp.MustCompile(`^\s*(?:description|examples|values|alias|schemaRoot|schemaMeta|schema|schemaRequired):\s*[|>]?-?\s*`)
)

type upstreamSentence struct {
	raw  string
	norm string
}

func normalize(s string) string {
	return strings.TrimSpace(nonWord.ReplaceAllString(strings.ToLower(s), " "))
}

// upstreamSentences returns the long-enough sentences of every comment in the machinery config types
// (file headers and generated files excluded), keyed by their first minSentenceWords normalized words.
func upstreamSentences(t *testing.T) map[string][]upstreamSentence {
	t.Helper()

	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", machineryModule).Output()
	if err != nil {
		t.Fatalf("locating %s: %v", machineryModule, err)
	}

	root := filepath.Join(strings.TrimSpace(string(out)), "config", "types")
	index := map[string][]upstreamSentence{}
	seen := map[string]bool{}
	fset := token.NewFileSet()

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}

		src, err := os.ReadFile(path)
		if err != nil || bytes.Contains(src, []byte("Code generated")) {
			return err
		}

		file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
		if err != nil {
			return err
		}

		for _, cg := range file.Comments {
			if cg.End() < file.Package && cg != file.Doc {
				continue // the file header
			}

			for para := range strings.SplitSeq(cg.Text(), "\n\n") {
				var lines []string
				for line := range strings.SplitSeq(para, "\n") {
					lines = append(lines, docgenKey.ReplaceAllString(line, ""))
				}

				for _, raw := range sentenceEnd.Split(strings.Join(lines, " "), -1) {
					norm := normalize(raw)
					words := strings.Fields(norm)

					// controller-gen's own boilerplate also appears in machinery's sources.
					if len(norm) < minSentenceLen || len(words) < minSentenceWords || seen[norm] || strings.HasPrefix(norm, "deepcopy") {
						continue
					}

					seen[norm] = true
					key := strings.Join(words[:minSentenceWords], " ")
					index[key] = append(index[key], upstreamSentence{strings.TrimSpace(raw), norm})
				}
			}
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(index) == 0 {
		t.Fatalf("no comments found under %s", root)
	}

	return index
}

// trackedFiles lists the repository's tracked files; without a .git (a `git archive` checkout) every file is tracked.
func trackedFiles(t *testing.T, root string) []string {
	t.Helper()

	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root

	if out, err := cmd.Output(); err == nil {
		var files []string

		for f := range strings.SplitSeq(strings.TrimRight(string(out), "\x00"), "\x00") {
			files = append(files, filepath.Join(root, f))
		}

		return files
	}

	var files []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, path)
		}

		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	return files
}

// firstUpstreamSentence returns the first corpus sentence that text contains.
func firstUpstreamSentence(text string, index map[string][]upstreamSentence) (string, bool) {
	norm := normalize(text)
	words := strings.Fields(norm)

	for i := 0; i+minSentenceWords <= len(words); i++ {
		for _, s := range index[strings.Join(words[i:i+minSentenceWords], " ")] {
			if strings.Contains(" "+norm+" ", " "+s.norm+" ") {
				return s.raw, true
			}
		}
	}

	return "", false
}

// The repository holds no prose of the Talos machinery config types: generated output points at the
// upstream symbol (structDef.reference) instead of copying its description.
func TestNoUpstreamProse(t *testing.T) {
	index := upstreamSentences(t)
	root := filepath.Join("..", "..", "..")

	for _, path := range trackedFiles(t, root) {
		if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
			continue
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		if bytes.IndexByte(data, 0) >= 0 {
			continue // binary
		}

		if s, found := firstUpstreamSentence(string(data), index); found {
			rel, _ := filepath.Rel(root, path)
			t.Errorf("%s contains a sentence from the Talos machinery config types:\n\t%q\n"+
				"Remove it or write it in your own words. If the file is generated, change the generator "+
				"(internal/tools/mirrorgen) to emit a reference to the upstream symbol instead, then run make generate manifests docs-reference.",
				filepath.ToSlash(rel), s)
		}
	}
}
