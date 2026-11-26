package readmeversion

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var shBlock = regexp.MustCompile("(?s)```sh\n(.*?)```")

// missingKubeconfig returns the ichor commands that follow `ichor init` in the sh blocks of md with no
// `export KUBECONFIG=` or --merge-kubeconfig before them. init without --merge-kubeconfig only prints
// the export line, and the CLI reads $KUBECONFIG or ~/.kube/config. init, version and create user
// need no cluster context.
func missingKubeconfig(md string) (bad []string) {
	inited, ready := false, false
	for _, m := range shBlock.FindAllStringSubmatch(md, -1) {
		// join backslash continuations into one command
		for line := range strings.SplitSeq(strings.ReplaceAll(m[1], "\\\n", " "), "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "export KUBECONFIG="):
				ready = true
			case strings.HasPrefix(line, "ichor init"):
				inited = true
				ready = strings.Contains(line, "--merge-kubeconfig")
			case strings.HasPrefix(line, "ichor version"), strings.HasPrefix(line, "ichor create user"):
			case strings.HasPrefix(line, "ichor ") && inited && !ready:
				bad = append(bad, line)
			}
		}
	}
	return bad
}

func TestMissingKubeconfig(t *testing.T) {
	const bad = "```sh\nichor init --cluster a \\\n  --preset single\n```\n```sh\nichor describe cluster a\n```\n"
	if got := missingKubeconfig(bad); len(got) != 1 {
		t.Errorf("got %v, want the describe line", got)
	}
	if got := missingKubeconfig(strings.Replace(bad, "single", "single --merge-kubeconfig", 1)); len(got) != 0 {
		t.Errorf("got %v with --merge-kubeconfig", got)
	}
}

// TestQuickStartsSetKubeconfig holds the README and the getting-started pages to the rule above.
func TestQuickStartsSetKubeconfig(t *testing.T) {
	files, err := filepath.Glob("../../../docs/guides/getting-started/*.md")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, "../../../README.md")
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range missingKubeconfig(string(b)) {
			t.Errorf("%s: %q runs after ichor init with no export KUBECONFIG= or --merge-kubeconfig before it", f, l)
		}
	}
}
