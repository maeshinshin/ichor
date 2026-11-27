package main

import (
	"os"
	"strings"
	"testing"
)

func TestRewriteLinksOfTheRealSection(t *testing.T) {
	b, err := os.ReadFile("testdata/v0.3.5.md")
	if err != nil {
		t.Fatal(err)
	}

	out := rewriteLinks(string(b), "v0.3.5")

	for _, bad := range []string{"](docs/", "](../", "](./"} {
		if strings.Contains(out, bad) {
			t.Errorf("output still holds %q", bad)
		}
	}

	want := "](https://github.com/maeshinshin/ichor/blob/v0.3.5/docs/guides/install-with-helm/upgrade.md)"
	if !strings.Contains(out, want) {
		t.Errorf("output lacks %q", want)
	}
}

func TestRewriteLinksKeepsTheRest(t *testing.T) {
	in := "[a](https://example.com/x) [b](#x) [c](mailto:a@b.c) [d](docs/a.md#top) [e](../docs/b.md)"
	base := "](https://github.com/maeshinshin/ichor/blob/v1.0.0/"
	want := "[a](https://example.com/x) [b](#x) [c](mailto:a@b.c) [d" + base + "docs/a.md#top) [e" + base + "docs/b.md)"

	if got := rewriteLinks(in, "v1.0.0"); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
