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
	"os"
	"path"
	"strings"
	"testing"
)

// initCommandFlags reads the `ichor init` command lines in the fenced code blocks of a markdown
// document and returns how many there are and the long flag names they pass.
//
// A line counts when a token `init` follows a token whose base name is `ichor` (so `ichor init`,
// `/tmp/ichor init`). Flags are the tokens after `init` that start with `--`; `--name=value` gives
// `name`, and a bare `--` gives nothing. The scan stops at a shell operator (`&&`, `|`, `;`, `>`, ...),
// so a later command on the same line is not read as init's. A line ending in `\` continues the next.
// Ignored: prose and inline code (a flag in a sentence is not a command), `#` comment lines inside a
// block, every other command (kubectl's flags come before its `init` word), and option values.
const ichorBinary = "ichor"

func initCommandFlags(doc string) (commands int, flags []string) {
	inFence := false
	var pending string
	for raw := range strings.SplitSeq(doc, "\n") {
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			pending = ""

			continue
		}
		if !inFence || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if rest, continued := strings.CutSuffix(trimmed, `\`); continued {
			pending += rest + " "

			continue
		}
		fields := strings.Fields(pending + trimmed)
		pending = ""
		for i := 1; i < len(fields); i++ {
			if fields[i] != "init" || path.Base(fields[i-1]) != ichorBinary {
				continue
			}
			commands++
			for _, tok := range fields[i+1:] {
				if strings.ContainsAny(tok[:1], "&|;<>") {
					break
				}
				if name, ok := strings.CutPrefix(tok, "--"); ok && name != "" {
					name, _, _ = strings.Cut(name, "=")
					flags = append(flags, name)
				}
			}
		}
	}

	return commands, flags
}

func TestInitCommandFlagsParsesOnlyFencedInitCommands(t *testing.T) {
	doc := "Prose says `init --admin-user admin` and `ichor init --bogus`.\n" +
		"```sh\n" +
		"# ichor init --in-a-comment\n" +
		"kubectl --context $C -n $NS exec $P-tools -- env HOME=/tmp/home /tmp/ichor init --yes \\\n" +
		"  --talos-version v1.14.5 --ui-url=https://$LB_IP \\\n" +
		"  --admin-user admin && ichor rollout config --cluster $P\n" +
		"ichor rollout config --cluster $P --yes\n" +
		"ichor init --skip-signature-check > out.txt --redirected\n" +
		"```\n"
	commands, flags := initCommandFlags(doc)
	if commands != 2 {
		t.Errorf("commands = %d, want 2", commands)
	}
	want := []string{"yes", "talos-version", "ui-url", "admin-user", "skip-signature-check"}
	if strings.Join(flags, " ") != strings.Join(want, " ") {
		t.Errorf("flags = %v, want %v", flags, want)
	}
}

// The UI demo README runs `ichor init` in a fenced block; every flag it passes must be one init
// registers, or the command the reader copies fails with "unknown flag".
func TestUIDemoReadmeInitFlagsAreRegistered(t *testing.T) {
	doc, err := os.ReadFile("../../test/harness/ui-demo/README.md")
	if err != nil {
		t.Fatal(err)
	}
	commands, flags := initCommandFlags(string(doc))
	if commands == 0 || len(flags) == 0 {
		t.Fatalf("found %d ichor init command lines with %d flags; the example this test guards is gone", commands, len(flags))
	}
	for _, name := range flags {
		if initCmd.LocalFlags().Lookup(name) == nil && initCmd.InheritedFlags().Lookup(name) == nil {
			t.Errorf("the UI demo README passes --%s to ichor init, which does not register it", name)
		}
	}
}
