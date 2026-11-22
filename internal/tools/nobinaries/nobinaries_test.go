package nobinaries

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// magics are the first bytes of an ELF, a Mach-O (32/64-bit, either byte order, and fat) and a PE executable.
var magics = [][]byte{
	[]byte("\x7fELF"), {0xfe, 0xed, 0xfa, 0xce}, {0xfe, 0xed, 0xfa, 0xcf}, {0xce, 0xfa, 0xed, 0xfe},
	{0xcf, 0xfa, 0xed, 0xfe}, {0xca, 0xfe, 0xba, 0xbe}, []byte("MZ"),
}

// allowed lists tracked executables that may stay. It is empty: a build artefact redistributes its dependencies.
var allowed = map[string]bool{}

func isExecutable(head []byte) bool {
	for _, m := range magics {
		if bytes.HasPrefix(head, m) {
			return true
		}
	}

	return false
}

// TestNoTrackedExecutables fails on any tracked file, of any type, that starts with an executable's magic bytes.
func TestNoTrackedExecutables(t *testing.T) {
	root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}

	dir := strings.TrimSpace(string(root))

	list, err := exec.Command("git", "-C", dir, "ls-files", "-z").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}

	n := 0

	for path := range strings.SplitSeq(string(list), "\x00") {
		if path == "" {
			continue
		}

		n++

		f, err := os.Open(filepath.Join(dir, path))
		if err != nil {
			continue // listed but deleted from the tree
		}

		head := make([]byte, 4)
		k, _ := f.Read(head)
		_ = f.Close()

		if isExecutable(head[:k]) && !allowed[path] {
			t.Errorf("%s is a tracked executable; build into bin/ and keep it untracked", path)
		}
	}

	if n < 100 {
		t.Fatalf("scanned only %d tracked files", n)
	}
}

// TestIsExecutable is the positive control: each magic is caught, text and short files are not.
func TestIsExecutable(t *testing.T) {
	for _, m := range magics {
		if !isExecutable(append(append([]byte{}, m...), "rest"...)) {
			t.Errorf("magic %q was not caught", m)
		}
	}

	for _, s := range []string{"", "M", "package p\n", "# MZ"} {
		if isExecutable([]byte(s)) {
			t.Errorf("%q was taken for an executable", s)
		}
	}
}
