// Package workflowcheck holds tests over .github/workflows and .github/actions: rules a reviewer would otherwise have to
// remember on every workflow edit.
package workflowcheck

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v4"
)

const repoRoot = "../../.."

type step struct {
	Name  string `yaml:"name"`
	Shell string `yaml:"shell"`
	Run   string `yaml:"run"`
}

type defaults struct {
	Run struct {
		Shell string `yaml:"shell"`
	} `yaml:"run"`
}

type job struct {
	Defaults defaults `yaml:"defaults"`
	Steps    []step   `yaml:"steps"`
}

// file is a workflow (jobs) or a composite action (runs.steps).
type file struct {
	Defaults defaults       `yaml:"defaults"`
	Jobs     map[string]job `yaml:"jobs"`
	Runs     struct {
		Steps []step `yaml:"steps"`
	} `yaml:"runs"`
}

// runStep is one step with a run: and the shell it runs under when nothing in the step names one.
type runStep struct {
	file, job, name, shell, run string
}

func load(t *testing.T, path string) file {
	t.Helper()

	b, err := os.ReadFile(filepath.Join(repoRoot, path))
	if err != nil {
		t.Fatal(err)
	}

	var f file
	if err := yaml.Unmarshal(b, &f); err != nil {
		t.Fatalf("%s: %v", path, err)
	}

	return f
}

func paths(t *testing.T) []string {
	t.Helper()

	var all []string
	for _, g := range []string{".github/workflows/*.yml", ".github/actions/*/action.yml"} {
		m, err := filepath.Glob(filepath.Join(repoRoot, g))
		if err != nil || len(m) == 0 {
			t.Fatalf("glob %s: %v (%d files)", g, err, len(m))
		}

		for _, p := range m {
			r, _ := filepath.Rel(repoRoot, p)
			all = append(all, r)
		}
	}

	return all
}

// runSteps lists every run: step in path with its effective shell: the step's, else the job's, else the workflow's.
func runSteps(t *testing.T, path string) []runStep {
	t.Helper()

	f := load(t, path)
	var out []runStep
	add := func(jobID, shell string, steps []step) {
		for _, s := range steps {
			if s.Run == "" {
				continue
			}

			if s.Shell != "" {
				shell = s.Shell
			}

			out = append(out, runStep{path, jobID, s.Name, shell, s.Run})
		}
	}

	add("", "", f.Runs.Steps)

	for id, j := range f.Jobs {
		shell := f.Defaults.Run.Shell
		if j.Defaults.Run.Shell != "" {
			shell = j.Defaults.Run.Shell
		}

		add(id, shell, j.Steps)
	}

	return out
}

// code is the run: script without comment lines.
func code(run string) []string {
	var lines []string

	for l := range strings.SplitSeq(run, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}

	return lines
}

var pipe = regexp.MustCompile(`(^|[^|])\|([^|]|$)`)

// GitHub: the default shell on Linux is `bash -e {0}`; only an explicit `shell: bash` runs `bash --noprofile --norc
// -eo pipefail {0}`. Without pipefail a pipe takes the exit status of its last command.
// shortcut: a pipe inside quotes (an awk program, a regexp) counts too; allowlist the step when one is real.
func TestAPipeRunsUnderPipefail(t *testing.T) {
	for _, p := range paths(t) {
		for _, s := range runSteps(t, p) {
			if s.shell == "bash" || strings.Contains(s.shell, "pipefail") {
				continue
			}

			if pipeline := slices.IndexFunc(code(s.run), pipe.MatchString); pipeline >= 0 {
				t.Errorf("%s job %q step %q pipes (%q) without pipefail: set `shell: bash` (or defaults.run.shell: bash); the step's shell is %q",
					s.file, s.job, s.name, code(s.run)[pipeline], s.shell)
			}
		}
	}
}

var innerShell = regexp.MustCompile(`\b(?:ba)?sh\b[^|\n]*? -c\b`)

// A `bash -c` / `sh -c` starts a new shell that does not inherit pipefail from the step's shell.
// shortcut: everything after the -c in the step counts as its argument; narrow it when a step mixes a pipe-free bash -c with a later pipe.
func TestAnInnerShellWithAPipeSetsPipefail(t *testing.T) {
	for _, p := range paths(t) {
		for _, s := range runSteps(t, p) {
			var text strings.Builder
			for _, l := range code(s.run) {
				text.WriteString(l + "\n")
			}

			if loc := innerShell.FindStringIndex(text.String()); loc != nil {
				rest := text.String()[loc[1]:]
				if pipe.MatchString(rest) && !strings.Contains(rest, "pipefail") && !strings.Contains(text.String()[loc[0]:loc[1]], "pipefail") {
					t.Errorf("%s step %q: a bash -c / sh -c holds a pipe without pipefail: use `bash -o pipefail -c`", p, s.name)
				}
			}
		}
	}
}

func TestReleaseReplacesExistingArtifacts(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot, ".goreleaser.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	var c struct {
		Release struct {
			ReplaceExistingArtifacts bool `yaml:"replace_existing_artifacts"`
		} `yaml:"release"`
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}

	if !c.Release.ReplaceExistingArtifacts {
		t.Error("release.replace_existing_artifacts is not true: the cli job cannot be re-run after a partial upload")
	}
}

// prOnly are PR commands the release verify job does not run, with the reason. Empty today: every make target,
// goreleaser check and actionlint a PR job runs is also run by verify.
var prOnly = map[string]string{}

// gates are the commands of a run: that a PR runs and verify must too.
func gates(run string) []string {
	var out []string

	for _, l := range code(run) {
		f := strings.Fields(l)
		for i, w := range f {
			switch {
			case w == "make":
				for _, tg := range f[i+1:] {
					tg = strings.Trim(tg, `"'`)
					if !regexp.MustCompile(`^[a-z][a-z0-9-]*$`).MatchString(tg) {
						break
					}

					out = append(out, "make "+tg)
				}
			case w == "actionlint":
				out = append(out, "actionlint")
			case w == "goreleaser" && i+1 < len(f) && f[i+1] == "check":
				out = append(out, "goreleaser check")
			}
		}
	}

	return out
}

func TestReleaseVerifyRunsEveryPRGate(t *testing.T) {
	have := map[string]bool{}

	for _, s := range runSteps(t, ".github/workflows/release.yml") {
		if s.job == "verify" {
			for _, g := range gates(s.run) {
				have[g] = true
			}
		}
	}

	for _, p := range []string{".github/workflows/test.yml", ".github/workflows/lint.yml"} {
		for _, s := range runSteps(t, p) {
			for _, g := range gates(s.run) {
				if _, skip := prOnly[g]; !have[g] && !skip {
					t.Errorf("%s job %q runs %q, which release verify does not", p, s.job, g)
				}
			}
		}
	}
}

// nixExempt are run: steps that do not start with `nix develop --command`, keyed "file: step name".
var nixExempt = map[string]string{
	// Shell builtins, git, awk and the repository's own scripts; no dev shell tool is called.
	".github/workflows/docs.yml: Dispatch the workflow on main":                                        "gh on a job that checks out nothing",
	".github/workflows/lint.yml: Keep the changelog block out of the goreleaser config":                "grep",
	".github/workflows/test.yml: Fail if regenerating produced a diff":                                 "git diff",
	".github/workflows/release.yml: Fail if regenerating produced a diff":                              "git diff",
	".github/workflows/release.yml: CHANGELOG.md has a section for the tag":                            "awk and grep",
	".github/workflows/release.yml: Take the release notes from CHANGELOG.md":                          "awk",
	".github/workflows/release.yml: Check that the tag was not cut from main":                          "git",
	".github/workflows/release.yml: Add the changelog section":                                         "git and hack/add-release-section.sh",
	".github/workflows/release.yml: Set the README's version":                                          "sync-main installs no Nix; make release-prep edits text only",
	".github/workflows/release.yml: Resolve the digests":                                               "grep over bin/digests.txt",
	".github/workflows/release.yml: Sign the images and the chart":                                     "a loop whose body is nix develop --command cosign",
	".github/workflows/release.yml: Generate the SBOMs":                                                "a loop whose body is nix develop --command syft",
	".github/workflows/release.yml: Verify the signatures as the controller does":                      "bin/verifyimage is built by this workflow",
	".github/workflows/test-chart.yml: Show why the deploy failed":                                     "failure diagnostics; every line is best effort",
	".github/actions/nix/action.yml: Free disk space":                                                  "runs before Nix exists",
	".github/actions/nix/action.yml: Keep the devShell and the flake inputs out of garbage collection": "builds the dev shell profile itself",
}

func TestEveryRunStartsInNixDevelop(t *testing.T) {
	for _, p := range paths(t) {
		for _, s := range runSteps(t, p) {
			if _, ok := nixExempt[p+": "+s.name]; ok {
				continue
			}

			if !strings.HasPrefix(code(s.run)[0], "nix develop --command") {
				t.Errorf("%s step %q starts with %q, not `nix develop --command`", p, s.name, code(s.run)[0])
			}
		}
	}
}
