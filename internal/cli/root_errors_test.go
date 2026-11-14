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
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// A failing command prints its error once (cobra's own line and Execute's used to both print it).
func TestExecuteRootPrintsAnErrorOnce(t *testing.T) {
	var buf bytes.Buffer

	rootCmd.SetArgs([]string{"secrets", "--no-such-flag"})
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetErr(nil)
	})

	if _, err := executeRoot(&buf); err == nil {
		t.Fatal("want an error for an unknown flag")
	}

	if n := strings.Count(buf.String(), "unknown flag: --no-such-flag"); n != 1 {
		t.Fatalf("the error was printed %d times:\n%s", n, buf.String())
	}
}

// Every group in the command tree is held to one rule: an argument it does not know fails and names that argument,
// and no argument prints its help and succeeds. The groups are read from the tree, not listed, so a group added
// later is held to the same rule. A command with its own Run is not a group and is not run here.
func TestGroupRejectsUnknownSubcommand(t *testing.T) {
	var buf bytes.Buffer

	rootCmd.InitDefaultCompletionCmd() // cobra adds it inside ExecuteC, which the test does not reach
	rootCmd.SetOut(&buf)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})

	for _, path := range groupPaths(rootCmd, nil) {
		t.Run(strings.Join(path, " "), func(t *testing.T) {
			rootCmd.SetArgs(append(append([]string{}, path...), unknownArg))

			if _, err := executeRoot(&buf); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("unknown command %q", unknownArg)) {
				t.Fatalf("want an error naming %s, got %v", unknownArg, err)
			}

			rootCmd.SetArgs(path)

			if _, err := executeRoot(&buf); err != nil {
				t.Fatalf("a group with no argument prints its help and succeeds, got %v", err)
			}
		})
	}
}

const unknownArg = "nosuch"

// groupPaths lists the path of every group under c, depth first. A group has subcommands and no Run of its own:
// either no RunE yet (cobra's default, before rejectUnknownSubcommands runs) or groupRunE.
func groupPaths(c *cobra.Command, prefix []string) [][]string {
	var out [][]string

	for _, sub := range c.Commands() {
		p := append(append([]string{}, prefix...), sub.Name())

		if sub.HasSubCommands() && (sub.RunE == nil || reflect.ValueOf(sub.RunE).Pointer() == reflect.ValueOf(groupRunE).Pointer()) {
			out = append(out, p)
		}

		out = append(out, groupPaths(sub, p)...)
	}

	return out
}
