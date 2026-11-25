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
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/maeshinshin/ichor/internal/etcdbackup"
)

var flagInText = regexp.MustCompile(`--([a-z][a-z0-9]*(?:-[a-z0-9]+)*)(-\*)?`)

func allFlagNames(c *cobra.Command, into map[string]bool) {
	add := func(f *pflag.Flag) { into[f.Name] = true }

	c.Flags().VisitAll(add)
	c.PersistentFlags().VisitAll(add)

	for _, sub := range c.Commands() {
		allFlagNames(sub, into)
	}
}

// TestErrorMessagesNameFlagsThatExist: a flag named in an error the CLI builds (fmt.Errorf or errors.New) is a flag
// of some command; a user who follows the message must not meet "unknown flag".
func TestErrorMessagesNameFlagsThatExist(t *testing.T) {
	// create-namespace is helm's, named in the install command a message shows.
	known := map[string]bool{"help": true, "create-namespace": true}
	allFlagNames(rootCmd, known)

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}

	fset := token.NewFileSet()

	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}

		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}

		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}

		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "Errorf" && sel.Sel.Name != "New") {
				return true
			}

			ast.Inspect(call, func(m ast.Node) bool {
				lit, ok := m.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}

				text, _ := strconv.Unquote(lit.Value)
				for _, m := range flagInText.FindAllStringSubmatch(text, -1) {
					if m[2] == "" && !known[m[1]] { // --name-* is a family of flags
						t.Errorf("%s: an error names --%s, which no command has", fset.Position(lit.Pos()), m[1])
					}
				}

				return true
			})

			return false
		})
	}
}

// TestSharedErrorsNameTheCommandsFlag: a message built outside the CLI package names the flag of the command that shows it.
func TestSharedErrorsNameTheCommandsFlag(t *testing.T) {
	err := etcdbackup.NewBackup{Name: "n", Cluster: "c", SecretName: "s", Endpoint: "http://s3.example.com", Bucket: "bucket", Prefix: "p"}.Validate()
	if err == nil {
		t.Fatal("a plain http:// endpoint was accepted")
	}

	for _, m := range flagInText.FindAllStringSubmatch(err.Error(), -1) {
		if createEtcdBackupCmd.Flags().Lookup(m[1]) == nil {
			t.Errorf("the message names --%s, which create etcd-backup does not have: %v", m[1], err)
		}
	}
}
