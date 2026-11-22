// Package errassert keeps one assertion shape out of the tests: Expect(apierrors.IsNotFound(err)).To(BeTrue()).
// The predicate takes the error and returns a bool, so a failure prints only "Expected <bool>: false"
// and the error that explained it is gone. Assert the error with MatchError(predicate, description),
// which prints the error it got.
package errassert

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// shape matches Expect( then a call to an Is* predicate or errors.As, in any package. Go's regexp has no
// lookahead, so findDiscards drops the IsStatusCondition* family (conditions, not errors) by name.
// The equivalent search: git grep -nP 'Expect\((\w+\.)?(?!IsStatusCondition)(Is[A-Z]\w*|As)\(' -- '*_test.go'
var shape = regexp.MustCompile(`Expect\(\s*(?:\w+\.)?(Is[A-Z]\w*|As)\(`)

func findDiscards(src string) []int {
	var lines []int
	for _, m := range shape.FindAllStringSubmatchIndex(src, -1) {
		if strings.HasPrefix(src[m[2]:m[3]], "IsStatusCondition") {
			continue
		}
		lines = append(lines, 1+strings.Count(src[:m[0]], "\n"))
	}
	return lines
}

// The probes are the shapes PR #195 converted, so the pattern is shown to fire; the controls are what it must leave alone.
func TestShapeFiresOnTheDiscardingAssertions(t *testing.T) {
	for _, p := range []string{
		`Expect(apierrors.IsNotFound(err)).To(BeTrue())`,
		`Expect(apierrors.IsForbidden(saClient.List(ctx, &l))).To(BeTrue(), "denied")`,
		`Expect(errors.As(err, &shuttingDown)).To(BeTrue())`,
		`Expect(IsMaintenancePinMismatch(pin.verify(state))).To(BeTrue())`,
		"Expect(\n\t\tapierrors.IsNotFound(err)).To(BeTrue())",
	} {
		if len(findDiscards(p)) == 0 {
			t.Errorf("the pattern does not match %q", p)
		}
	}
	for _, c := range []string{
		`Expect(err).To(MatchError(apierrors.IsNotFound, "gone"))`,
		`Expect(apimeta.IsStatusConditionTrue(conds, "Ready")).To(BeTrue())`,
		`Expect(IsStatusConditionFalse(conds, "Ready")).To(BeTrue())`,
	} {
		if got := findDiscards(c); len(got) != 0 {
			t.Errorf("the pattern matches %q, which keeps the error", c)
		}
	}
}

// TestNoTestDiscardsTheErrorItAsserts reads every tracked _test.go file in the repository.
func TestNoTestDiscardsTheErrorItAsserts(t *testing.T) {
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
	self := filepath.ToSlash(filepath.Join("internal", "tools", "errassert")) + "/"
	for name := range strings.SplitSeq(string(out), "\x00") {
		if strings.HasPrefix(name, self) || !strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range findDiscards(string(src)) {
			t.Errorf("%s:%d: the assertion evaluates a predicate on the error and discards it; use Expect(err).To(MatchError(predicate, \"why\"))", name, line)
		}
	}
}
