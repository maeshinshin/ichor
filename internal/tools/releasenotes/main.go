// Command releasenotes reads a CHANGELOG section on stdin and writes it to stdout with every relative Markdown link
// made absolute to the tagged tree, so the links resolve from the GitHub release page: releasenotes vX.Y.Z.
package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
)

const blob = "https://github.com/maeshinshin/ichor/blob/"

// shortcut: only inline [text](target) links; the changelog uses no reference-style or HTML links. Extend if it does.
var link = regexp.MustCompile(`\]\(([^)\s]+)\)`)

var scheme = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9+.-]*:|#|/)`)

func rewriteLinks(s, tag string) string {
	return link.ReplaceAllStringFunc(s, func(m string) string {
		t := link.FindStringSubmatch(m)[1]
		if scheme.MatchString(t) {
			return m
		}

		p, anchor, _ := strings.Cut(t, "#")
		if anchor != "" {
			anchor = "#" + anchor
		}

		// The changelog sits at the repository root, so "../" can only mean the root as well.
		return "](" + blob + tag + path.Clean("/"+p) + anchor + ")"
	})
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: releasenotes vX.Y.Z < section.md")
		os.Exit(2)
	}

	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Print(rewriteLinks(string(b), os.Args[1]))
}
