// Package claimcheck keeps statements of testing, measurement and verification out of the repository.
// The rule is the maintainer's: such claims belong in conversation. The shapes below match testimony,
// a report that something was run and what it showed. A decision about testing is not testimony, so
// "pinned by TestFoo" or "the suite uses envtest" never match. Markdown is read as written; Go is read
// through its comments only, so string literals and identifiers are not scanned.
package claimcheck

import (
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A shape is one form of testimony. skip, when it matches the text just before a match, exempts that
// match: a modal ("would have to be verified on hardware first") states a requirement, and a present
// tense ("Cilium 1.19 is tested with 1.32 to 1.35") states the release matrix, which is a decision.
type shape struct {
	name string
	re   *regexp.Regexp
	skip *regexp.Regexp
}

var shapes = []shape{
	{"a measurement with its value, a parenthesis or a colon",
		regexp.MustCompile(`(?i)\bmeasured(\s*[:(]|\s+(on|with|about|roughly)\b)`), nil},
	{"a verb of checking, then hardware or a live cluster",
		regexp.MustCompile(`(?i)\b(tested|verified|confirmed|reproduced|observed|seen|validated|exercised)\b[^.;:]{0,40}\bon\s+(real\s+|bare[- ]metal\s+|physical\s+|a\s+real\s+|a\s+live\s+|our\s+)?(hardware|bare\s+metal|a\s+real\s+cluster|a\s+live\s+cluster)\b`),
		regexp.MustCompile(`(?i)\b(be|to|must|can|will|would|should|need)\s+$`)},
	{"tested with, against, on, under or locally",
		regexp.MustCompile(`(?i)\btested\s+(with|against|on|under|locally|by\s+hand)\b`),
		regexp.MustCompile(`(?i)\b(is|are)\s+((e2e|also|only|still)\s+)*$`)},
	{"a past or perfect passive of tested, measured or reproduced",
		regexp.MustCompile(`(?i)\b(was|were|has\s+been|have\s+been|had\s+been)\s+(tested|measured|reproduced)\b`), nil},
	{"a hyphenated apply-, hardware- or hand-tested form",
		regexp.MustCompile(`(?i)\b(apply|hardware|hand|lab|e2e)-(tested|verified|measured|confirmed)\b`), nil},
	{"confirmed or verified working, or worked on hardware",
		regexp.MustCompile(`(?i)\b((tested|verified|confirmed)\s+working|worked\s+on\s+(real\s+|bare[- ]metal\s+)?hardware)\b`), nil},
	{"a label such as Confirmed: or Tested:",
		regexp.MustCompile(`(?i)\b(tested|confirmed)\s*:`), nil},
	{"a bare status word in parentheses, such as (verified)",
		regexp.MustCompile(`(?i)\((measured|tested|verified|confirmed|reproduced)\)`), nil},
	{"a duration in seconds or minutes after took",
		regexp.MustCompile(`(?i)\btook\s+(about\s+|roughly\s+|some\s+|nearly\s+|around\s+)?\d+\s*(seconds?|minutes?)\b`), nil},
}

// negation is looked for in the text just before a match: "has not been tested with" reports a gap,
// not a result, and a gap is allowed to stay in the repository.
var negation = regexp.MustCompile(`(?i)\b(not|never|no|nor|without|untested|unverified)\b`)

// clause is the part of the text before a match that belongs to its sentence, so a "not" in the
// previous sentence cannot excuse a claim in this one.
func clause(b []byte) []byte {
	if i := bytes.LastIndexAny(b, ".;:!?|\n"); i >= 0 {
		return b[i+1:]
	}
	return b
}

type hit struct {
	line int
	name string
	text string
}

// commentsOnly blanks everything but the comments of a Go file, keeping its newlines and offsets, so
// the same matcher reads the comments and reports the right line.
func commentsOnly(path string, src []byte) ([]byte, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	out := bytes.Clone(src)
	for i, b := range out {
		if b != '\n' {
			out[i] = ' '
		}
	}
	for _, g := range f.Comments {
		for _, c := range g.List {
			copy(out[fset.Position(c.Pos()).Offset:], c.Text)
		}
	}
	return out, nil
}

func findClaims(text []byte) []hit {
	var out []hit
	for _, s := range shapes {
		for _, loc := range s.re.FindAllIndex(text, -1) {
			before := clause(text[max(0, loc[0]-24):loc[0]])
			if negation.Match(before) || (s.skip != nil && s.skip.Match(before)) {
				continue
			}
			out = append(out, hit{
				line: 1 + bytes.Count(text[:loc[0]], []byte("\n")),
				name: s.name,
				text: string(text[loc[0]:loc[1]]),
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].line < out[j].line })
	return out
}

// Each probe is a fragment of a forbidden shape, not a result. Every shape must match at least one.
func TestEachShapeFires(t *testing.T) {
	probes := map[string]string{
		"a measurement with its value, a parenthesis or a colon":      "measured: 45 seconds",
		"a verb of checking, then hardware or a live cluster":         "seen on hardware",
		"tested with, against, on, under or locally":                  "tested with",
		"a past or perfect passive of tested, measured or reproduced": "was tested",
		"a hyphenated apply-, hardware- or hand-tested form":          "apply-tested",
		"confirmed or verified working, or worked on hardware":        "confirmed working",
		"a label such as Confirmed: or Tested:":                       "Confirmed:",
		"a bare status word in parentheses, such as (verified)":       "(verified)",
		"a duration in seconds or minutes after took":                 "took about 45 seconds",
	}
	for _, s := range shapes {
		p, ok := probes[s.name]
		if !ok {
			t.Fatalf("no probe for shape %q", s.name)
		}
		if len(findClaims([]byte(p))) == 0 {
			t.Errorf("shape %q does not match its probe %q", s.name, p)
		}
	}
}

// A negation in the previous sentence must not hide a claim in this one.
func TestNegationStaysInItsSentence(t *testing.T) {
	if got := findClaims([]byte("The gap was not checked.\nMeasured: about 45 seconds.")); len(got) == 0 {
		t.Error("a claim after a negated sentence was not reported")
	}
}

// Sentences that record a decision, a limit or a mechanism. None may match.
var decisions = []string{
	"this behaviour is pinned by `TestFoo`",
	"the controller suite uses envtest",
	"both paths are covered by `TestCreateKubeProxySwitch`",
	"an override would allow a version this build has not been tested with",
	"the chart digest is verified against the pinned value before it is used",
	"the status is observed on the Node object",
	"the e2e harness runs on bare metal when `ICHOR_E2E=1` is set",
	"Cilium 1.19 is tested with Kubernetes 1.32 to 1.35",
	"the unreachable window is tested on its own",
	"it would have to be verified on hardware first",
}

func TestDecisionsDoNotMatch(t *testing.T) {
	for _, d := range decisions {
		if got := findClaims([]byte(d)); len(got) != 0 {
			t.Errorf("decision matched %q: %+v", d, got)
		}
	}
}

// TestRepositoryStatesNoClaims reads every tracked Markdown file and Go comment in the repository.
func TestRepositoryStatesNoClaims(t *testing.T) {
	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("finding the repository root: %v", err)
	}
	root := strings.TrimSpace(string(top))
	ls := exec.Command("git", "ls-files", "-z")
	ls.Dir = root
	out, err := ls.Output()
	if err != nil {
		t.Fatalf("listing tracked files: %v", err)
	}
	self := filepath.ToSlash(filepath.Join("internal", "tools", "claimcheck")) + "/"
	for name := range strings.SplitSeq(string(out), "\x00") {
		if strings.HasPrefix(name, self) || !(strings.HasSuffix(name, ".md") || strings.HasSuffix(name, ".go")) {
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(name, ".go") {
			if src, err = commentsOnly(name, src); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}
		for _, h := range findClaims(src) {
			t.Errorf("%s:%d: %s: %q", name, h.line, h.name, h.text)
		}
	}
}
