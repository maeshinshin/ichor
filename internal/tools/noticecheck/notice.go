package noticecheck

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

const (
	mplMark       = "This Source Code Form is subject to the terms of the Mozilla Public"
	apacheMark    = "Licensed under the Apache License"
	mplCopyright  = "// Copyright 2026 maeshinshin."
	generatedMark = "// Code generated "
)

var (
	// talosDescription is the description line the CRD generator writes for every mirrored Talos type, and the
	// reference pages repeat it.
	talosDescription = regexp.MustCompile(`Talos: [A-Za-z0-9_]+ — https?://`)
	// noticePath is a NOTICE line that is a repository path and nothing else.
	noticePath = regexp.MustCompile(`^[A-Za-z0-9_./-]+/[A-Za-z0-9_.-]+$`)
	// talosImports are the packages a generated file imports to name a Talos type or a type mirrored from one.
	talosImports = []string{`"github.com/maeshinshin/ichor/pkg/typedconfig"`, `"github.com/siderolabs/talos/`}
)

// goHeader is the text of a Go file before its package clause: its licence header and build constraints.
func goHeader(src string) string {
	if i := strings.Index("\n"+src, "\npackage "); i >= 0 {
		return ("\n" + src)[:i]
	}

	return src
}

// derivedGo reports whether a Go file contains Talos-derived content. Only generated files qualify: the
// hand-written ones that name Talos types (the generator's own tables, the renderers) are Ichor's code.
func derivedGo(path, src string) bool {
	hdr := "\n" + goHeader(src) // the marker counts only before the package clause, as the Go tools read it
	if !strings.Contains(hdr, "\n"+generatedMark) {
		return false
	}

	return strings.Contains(hdr, generatedMark+"by mirrorgen") ||
		strings.HasPrefix(path, "pkg/typedconfig/") ||
		slices.ContainsFunc(talosImports, func(i string) bool { return strings.Contains(src, i) })
}

// derivedOther reports whether a manifest or a reference page holds the Talos schema the generators write.
func derivedOther(path, src string) bool {
	switch {
	case strings.HasSuffix(path, ".yaml"), strings.HasSuffix(path, ".yml"), strings.HasSuffix(path, ".md"), strings.HasSuffix(path, ".json"):
		return talosDescription.MatchString(src)
	}

	return false
}

// check returns what is wrong with the licence treatment of files (every path of the repository, with the
// text of the ones that matter) against NOTICE, one line per problem.
func check(files map[string]string, notice string) []string {
	named := map[string]bool{}

	for line := range strings.SplitSeq(notice, "\n") {
		if noticePath.MatchString(line) {
			named[line] = true
		}
	}

	var out []string

	for _, path := range slices.Sorted(maps.Keys(files)) {
		src := files[path]

		if strings.HasSuffix(path, ".go") {
			out = append(out, checkGo(path, src, named[path])...)
		} else if derivedOther(path, src) && !named[path] {
			out = append(out, fmt.Sprintf("%s: holds Talos-derived schema and cannot carry a header, so NOTICE must name it", path))
		}
	}

	for _, path := range slices.Sorted(maps.Keys(named)) {
		if _, ok := files[path]; !ok {
			out = append(out, fmt.Sprintf("NOTICE names %s, which does not exist", path))
		}
	}

	return out
}

func checkGo(path, src string, inNotice bool) []string {
	hdr := goHeader(src)
	mpl, apache, derived := strings.Contains(hdr, mplMark), strings.Contains(hdr, apacheMark), derivedGo(path, src)

	var out []string

	switch {
	case mpl && apache:
		out = append(out, path+": the header grants both MPL-2.0 and Apache-2.0; a file has one licence")
	case derived && apache:
		out = append(out, path+": contains Talos-derived content but carries an Apache-2.0 block; it takes the MPL-2.0 header")
	case derived && !mpl:
		out = append(out, path+": contains Talos-derived content but its header has no MPL-2.0 notice")
	case mpl && !derived:
		out = append(out, path+": carries an MPL-2.0 notice but holds no Talos-derived content; it is Apache-2.0")
	}

	if derived && mpl && !strings.Contains(hdr, mplCopyright) {
		out = append(out, fmt.Sprintf("%s: the MPL-2.0 header lacks the line %q", path, mplCopyright))
	}

	if derived && !inNotice {
		out = append(out, path+": contains Talos-derived content but NOTICE does not list it")
	}

	return out
}
