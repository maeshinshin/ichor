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
	If    string `yaml:"if"`
	Shell string `yaml:"shell"`
	Run   string `yaml:"run"`
}

type defaults struct {
	Run struct {
		Shell string `yaml:"shell"`
	} `yaml:"run"`
}

type job struct {
	Permissions map[string]string `yaml:"permissions"`
	Defaults    defaults          `yaml:"defaults"`
	Steps       []step            `yaml:"steps"`
}

// file is a workflow (jobs) or a composite action (runs.steps).
type file struct {
	Permissions map[string]string `yaml:"permissions"`
	Defaults    defaults          `yaml:"defaults"`
	Jobs        map[string]job    `yaml:"jobs"`
	Runs        struct {
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

// earlyExit is a pipe into a reader that can quit before the writer is done: the writer then dies of SIGPIPE and
// pipefail turns that into a failure of the whole pipeline.
var earlyExit = regexp.MustCompile(`\|\s*(head\b|grep\b[^|\n]*\s(-[a-zA-Z]*[qm]|--quiet|--max-count))`)

// shortcut: any step that names pipefail anywhere counts as running under it, even if only an inner shell does.
func TestNoEarlyExitReaderUnderPipefail(t *testing.T) {
	for _, p := range paths(t) {
		for _, s := range runSteps(t, p) {
			if s.shell != "bash" && !strings.Contains(s.shell, "pipefail") && !strings.Contains(s.run, "pipefail") {
				continue
			}

			if l := slices.IndexFunc(code(s.run), earlyExit.MatchString); l >= 0 {
				t.Errorf("%s job %q step %q pipes into a reader that exits early (%q): under pipefail the writer's SIGPIPE fails the step; capture the output (`test -n \"$(...)\"`) or use a here-string",
					s.file, s.job, s.name, code(s.run)[l])
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

// The uninstall guide sends manifest installs to the release's install.yaml, so the release must carry it, and the
// signed checksums must cover it: a file outside them is not covered by `cosign verify-blob` of the checksums.
func TestReleaseAttachesAndChecksumsInstallYaml(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot, ".goreleaser.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	type extra struct {
		Glob string `yaml:"glob"`
	}

	var c struct {
		Checksum struct {
			ExtraFiles []extra `yaml:"extra_files"`
		} `yaml:"checksum"`
		Release struct {
			ExtraFiles []extra `yaml:"extra_files"`
		} `yaml:"release"`
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}

	for name, l := range map[string][]extra{"release.extra_files": c.Release.ExtraFiles, "checksum.extra_files": c.Checksum.ExtraFiles} {
		if !slices.ContainsFunc(l, func(e extra) bool { return e.Glob == "./dist/install.yaml" }) {
			t.Errorf(".goreleaser.yaml %s lacks ./dist/install.yaml", name)
		}
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
	".github/workflows/release.yml: Take the release notes from CHANGELOG.md":                          "awk, then the releasenotes tool in nix develop",
	".github/workflows/release.yml: Check that the tag was not cut from main":                          "git",
	".github/workflows/release.yml: Add the changelog section":                                         "git and hack/add-release-section.sh",
	".github/workflows/release.yml: Set the README's version":                                          "sync-main installs no Nix; make release-prep edits text only",
	".github/workflows/release.yml: Resolve the digests":                                               "grep over bin/digests.txt",
	".github/workflows/release.yml: Sign the images and the chart":                                     "a loop whose body is nix develop --command cosign",
	".github/workflows/release.yml: Generate the SBOMs":                                                "a loop whose body is nix develop --command syft",
	".github/workflows/release.yml: Verify the signatures as the controller does":                      "bin/verifyimage is built by this workflow",
	".github/workflows/test-chart.yml: Show why the deploy failed":                                     "failure diagnostics; every line is best effort",
	".github/workflows/cilium-patch.yml: Open or update the issue":                                     "a job without a checkout; it only runs the runner's gh",
	".github/actions/nix/action.yml: Empty /nix before the second attempt":                             "runs before Nix exists",
	".github/workflows/cilium-patch.yml: Apply the patch":                                              "a job without Nix; it only runs git, to keep repository code away from the write token",
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

// docsInputs are what the docs build and its browser check read besides docs/ and site/. A change to one
// must run the Docs workflow, or a break first shows after a release tag, when the docs and the Helm index fail to publish.
var docsInputs = []string{
	"Makefile", "flake.nix", "flake.lock", "hack/docs-shots.mjs",
	".github/actions/nix/**", ".github/actions/npm-cache/**", ".github/workflows/docs.yml",
}

func TestDocsRunsForItsBuildInputs(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot, ".github/workflows/docs.yml"))
	if err != nil {
		t.Fatal(err)
	}

	var w struct {
		On map[string]struct {
			Paths []string `yaml:"paths"`
		} `yaml:"on"`
	}
	if err := yaml.Unmarshal(b, &w); err != nil {
		t.Fatal(err)
	}

	linked := docsLinksOutsideDocs(t)

	for _, event := range []string{"push", "pull_request"} {
		for _, want := range append(slices.Clone(docsInputs), linked...) {
			if !slices.ContainsFunc(w.On[event].Paths, func(pat string) bool { return pathFilterMatches(pat, want) }) {
				t.Errorf("docs.yml on.%s.paths lacks %q: a change to it would not run Docs", event, want)
			}
		}
	}
}

// pathFilterMatches covers the two forms docs.yml uses: a file name, and a directory with `/**`.
func pathFilterMatches(pattern, path string) bool {
	if dir, ok := strings.CutSuffix(pattern, "/**"); ok {
		return strings.HasPrefix(path, dir+"/")
	}

	return pattern == path
}

var mdLink = regexp.MustCompile(`\]\(([^)\s#]+)`)

// docsLinksOutsideDocs lists the repository files that published docs pages link to outside docs/. The site build fails on
// a broken relative link, so a change to one of them can break the build. docs/adr is not published.
func docsLinksOutsideDocs(t *testing.T) []string {
	t.Helper()

	docs := filepath.Join(repoRoot, "docs")
	seen := map[string]bool{}

	err := filepath.WalkDir(docs, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() && p == filepath.Join(docs, "adr") {
			return filepath.SkipDir
		}

		if d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}

		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}

		for _, m := range mdLink.FindAllStringSubmatch(string(b), -1) {
			if strings.Contains(m[1], ":") {
				continue
			}

			target := filepath.Join(filepath.Dir(p), m[1])
			if rel, _ := filepath.Rel(docs, target); !strings.HasPrefix(rel, "..") {
				continue
			}

			if _, err := os.Stat(target); err != nil {
				continue
			}

			rel, _ := filepath.Rel(repoRoot, target)
			seen[filepath.ToSlash(rel)] = true
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(seen) == 0 {
		t.Fatal("no docs page links outside docs/: the link scan is broken")
	}

	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}

	slices.Sort(out)

	return out
}

// The install action extracts with `tar --skip-old-files`: a file the failed first attempt left half-written is kept
// and registered, so the second attempt may only start from an empty /nix.
func TestNixRetryStartsFromAnEmptyStore(t *testing.T) {
	steps := load(t, ".github/actions/nix/action.yml").Runs.Steps
	retry := slices.IndexFunc(steps, func(s step) bool { return s.Name == "Install Nix (second attempt)" })
	if retry < 1 {
		t.Fatal("the nix action has no second install attempt")
	}

	prev := steps[retry-1]
	if prev.If != steps[retry].If || !strings.Contains(prev.Run, "sudo rm -rf /nix") {
		t.Errorf("the step before the second attempt is %q (if %q): want `sudo rm -rf /nix` under the same if as the attempt (%q)", prev.Name, prev.If, steps[retry].If)
	}
}

// writeJobsMayRun lists the jobs that hold a write permission and still run repository code, keyed "file: job id".
var writeJobsMayRun = map[string]string{
	".github/workflows/release.yml: cli": "tag-triggered, runs only goreleaser and the release-notes tool of the tagged tree it publishes",
}

var repoCode = regexp.MustCompile(`\bmake (test|lint)\b|\bgo (test|run)\b`)

// A job with a write token must not run the repository's tests, linter or Go code: that code, and what it downloads, would
// hold the token. Such work goes in a job with read-only permissions that hands its result on as an artifact.
func TestAWriteTokenJobRunsNoRepositoryCode(t *testing.T) {
	for _, p := range paths(t) {
		f := load(t, p)
		for id, j := range f.Jobs {
			perms := j.Permissions
			if perms == nil {
				perms = f.Permissions
			}

			if !slices.Contains(mapValues(perms), "write") {
				continue
			}

			if _, ok := writeJobsMayRun[p+": "+id]; ok {
				continue
			}

			for _, s := range j.Steps {
				if m := repoCode.FindString(strings.Join(code(s.Run), "\n")); m != "" {
					t.Errorf("%s job %q has a write permission and step %q runs %q", p, id, s.Name, m)
				}
			}
		}
	}
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}

	return out
}
