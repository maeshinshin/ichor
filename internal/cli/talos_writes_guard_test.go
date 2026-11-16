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

package cli

import (
	"go/ast"
	"go/types"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// talosWriters maps each method the CLI's own code calls on a Talos machinery client, and that changes a
// node, to the command that makes the call. rootCmd's help and docs/design/architecture/cli-and-ui.md must
// name every one of those commands, because both say the CLI never writes to a node otherwise, and so must
// every document that says the controller is the only writer (TestEveryDocThatClaimsASoleWriterNamesTheCLIExceptions).
var talosWriters = map[string]string{
	"ApplyConfiguration": "ichor init",
	"Bootstrap":          "ichor init",
	"EtcdRecover":        "ichor restore cluster",
	"ResetGeneric":       "ichor reclaim node",
}

// talosReaders are the machinery client methods known not to change a node. A method in neither map fails
// the test: say whether it writes, and if it does, name its command in talosWriters and in the two texts.
var talosReaders = []string{"Close", "Kubeconfig", "Version"}

// talosAPIPackages are the packages whose types reach a node: the machinery client, its gRPC service
// clients (interfaces; the message structs next to them only have getters) and the COSI state it exposes.
var talosAPIPackages = []string{
	"github.com/siderolabs/talos/pkg/machinery/client",
	"github.com/siderolabs/talos/pkg/machinery/api/",
	"github.com/cosi-project/runtime/pkg/state",
}

// TestTalosWritesAreNamedInTheExceptionList type-checks internal/cli and pkg/reclaim and finds every call
// of a method of the machinery client, its gRPC services or COSI state. It fails for a method that is
// neither known read-only nor a named exception, and for a named exception no call uses any more.
//
// shortcut: this sees the CLI's own two packages only. A node write made through another package of this
// module (internal/controller is imported by the CLI) is not seen; widen the roots when the CLI calls one.
func TestTalosWritesAreNamedInTheExceptionList(t *testing.T) {
	pkgs, err := packages.Load(&packages.Config{
		Dir:  "../..",
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports,
	}, "./internal/cli", "./pkg/reclaim")
	if err != nil {
		t.Fatalf("loading packages: %v", err)
	}

	called := map[string][]string{}

	for _, pkg := range pkgs {
		for _, e := range pkg.Errors {
			t.Fatalf("%s: %v", pkg.PkgPath, e)
		}

		for _, file := range pkg.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}

				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}

				s := pkg.TypesInfo.Selections[sel]
				if s == nil || s.Kind() != types.MethodVal || s.Obj().Pkg() == nil {
					return true
				}

				path := s.Obj().Pkg().Path()
				_, isInterface := s.Recv().Underlying().(*types.Interface)

				if !slices.ContainsFunc(talosAPIPackages, func(p string) bool {
					if strings.HasSuffix(p, "/") {
						return strings.HasPrefix(path, p) && isInterface
					}

					return path == p
				}) {
					return true
				}

				name := sel.Sel.Name
				at := pkg.Fset.Position(call.Pos()).String()
				called[name] = append(called[name], at)

				if _, writes := talosWriters[name]; !writes && !slices.Contains(talosReaders, name) {
					t.Errorf("%s calls %s.%s, which is not listed as read-only or as a node write; "+
						"if it changes a node, add it to talosWriters and name its command in rootCmd's help and "+
						"docs/design/architecture/cli-and-ui.md", at, path, name)
				}

				return true
			})
		}
	}

	if len(called) == 0 {
		t.Fatal("no call of a Talos machinery client method found; the scan has nothing to check")
	}

	help := strings.Join(strings.Fields(rootCmd.Long), " ")

	design, err := os.ReadFile("../../docs/design/architecture/cli-and-ui.md")
	if err != nil {
		t.Fatal(err)
	}

	designText := strings.Join(strings.Fields(string(design)), " ")

	for method, command := range talosWriters {
		if len(called[method]) == 0 {
			t.Errorf("talosWriters lists %s, but no call of it exists; drop it", method)
		}

		// the design page names a command without the leading "ichor "
		if !strings.Contains(help, command) || !strings.Contains(designText, strings.TrimPrefix(command, "ichor ")) {
			t.Errorf("%s is called (%v) but %q is missing from rootCmd's help or cli-and-ui.md's list of exceptions",
				method, called[method], command)
		}
	}
}

// soleWriterClaim finds a sentence that says the controller alone writes to a node, however it is put:
// "the only writer", "the only component that writes", "Only the controller holds a Talos identity that
// can change a node", "the sole writer". Whitespace is normalised first, so a wrapped sentence matches.
var soleWriterClaim = regexp.MustCompile(`(?i)\b(the only|only the|sole)\s+(\w+\s+){0,8}?(writer|writes|write|change a node|configures)\b`)

// writerDocsSkipped are the documents that record history or are generated from the command tree.
var writerDocsSkipped = map[string]bool{"adr": true, "reference": true, "images": true}

// TestEveryDocThatClaimsASoleWriterNamesTheCLIExceptions walks the documentation, the README and the
// instruction files and fails for each one that says the controller is the only writer to a node without
// naming, as code, every command in talosWriters. That is the rule overview.md's caption states; this
// keeps the next statement of it from dropping the exceptions.
func TestEveryDocThatClaimsASoleWriterNamesTheCLIExceptions(t *testing.T) {
	var files []string

	err := filepath.WalkDir("../../docs", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() && writerDocsSkipped[d.Name()] {
			return filepath.SkipDir
		}

		if !d.IsDir() && filepath.Ext(path) == ".md" {
			files = append(files, path)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	files = append(files, "../../README.md", "../../CLAUDE.md", "../../CONTRIBUTING.md")

	claims := 0

	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}

		text := strings.Join(strings.Fields(string(body)), " ")
		if !soleWriterClaim.MatchString(text) {
			continue
		}

		claims++

		for _, command := range slices.Compact(slices.Sorted(maps.Values(talosWriters))) {
			// a command in a code span, with or without "ichor ", followed by its end or its arguments
			named := regexp.MustCompile("`(ichor )?" + regexp.QuoteMeta(strings.TrimPrefix(command, "ichor ")) + "[` ]")
			if !named.MatchString(text) {
				t.Errorf("%s says the controller is the only writer to a node but does not name `%s`, "+
					"which writes to one", file, strings.TrimPrefix(command, "ichor "))
			}
		}
	}

	if claims == 0 {
		t.Fatal("no document claims a sole writer; the pattern or the file list has gone stale")
	}
}
