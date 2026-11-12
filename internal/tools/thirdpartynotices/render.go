package main

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/mod/module"
)

var (
	copyrightLine = regexp.MustCompile(`^(Portions )?Copyright\b|^(\(c\)|©) ?(19|20)\d\d`)
	placeholder   = regexp.MustCompile(`(?i)yyyy|name of copyright owner`)
)

// split separates a licence file into its words and its copyright lines. canon of the words
// identifies the licence text; the lines are what each component adds to it.
func split(text string) (words, text2 string, copyrights []string) {
	var keep, shown []string
	afterCopyright := false
	for l := range strings.SplitSeq(strings.ReplaceAll(text, "\r", ""), "\n") {
		t := strings.TrimSpace(strings.TrimLeft(l, "/*# "))
		if copyrightLine.MatchString(t) {
			if !placeholder.MatchString(t) {
				copyrights = append(copyrights, t)
			}
			afterCopyright = true
			continue
		}
		if afterCopyright && strings.EqualFold(t, "All rights reserved.") && len(copyrights) > 0 {
			copyrights[len(copyrights)-1] += " " + t
			continue
		}
		if t != "" {
			afterCopyright = false
		}
		shown = append(shown, strings.TrimRight(l, " \t"))
		if f := strings.Join(strings.Fields(l), " "); f != "" {
			keep = append(keep, f)
		}
	}
	return strings.Join(keep, " "), strings.TrimSpace(regexp.MustCompile(`\n{3,}`).ReplaceAllString(strings.Join(shown, "\n"), "\n\n")), copyrights
}

var (
	mitTitle       = regexp.MustCompile(`^(?i)(the )?mit license( \((mit|expat)\))?`)
	apacheAppendix = regexp.MustCompile(`(?s)APPENDIX: How to apply the Apache License.*?(limitations under the License\.|$)`)
	apacheNotice   = regexp.MustCompile(`(?s)Licensed under the Apache License, Version 2\.0 \(the "License"\);.*?limitations under the License\.`)
)

// canon reduces a licence body to the words that make it that licence: line breaks and indentation
// do not count, nor the Apache appendix (optional boilerplate every project fills in on its own).
func canon(body string) string {
	b := strings.Join(strings.Fields(body), " ")
	if strings.Contains(b, "TERMS AND CONDITIONS FOR USE, REPRODUCTION, AND DISTRIBUTION") {
		b = apacheAppendix.ReplaceAllString(apacheNotice.ReplaceAllString(b, ""), "")
		b = strings.ReplaceAll(b, "https://www.apache.org", "http://www.apache.org")
		b = strings.ReplaceAll(strings.ReplaceAll(b, "END OF TERMS AND CONDITIONS", ""), "Apache License Version 2.0, January 2004 http://www.apache.org/licenses/", "")
	}
	b = strings.NewReplacer("“", `"`, "”", `"`, "'Software'", `"Software"`, "'AS IS'", `"AS IS"`).Replace(b)
	return strings.Join(strings.Fields(mitTitle.ReplaceAllString(b, "")), " ")
}

// label names the licences a normalised body holds.
func label(body string) string {
	b := strings.ToLower(strings.Join(strings.Fields(body), " "))
	has := func(s string) bool { return strings.Contains(b, s) }
	var l []string
	add := func(ok bool, n string) {
		if ok {
			l = append(l, n)
		}
	}
	bsd := has("redistributions of source code must retain") && has("redistributions in binary form")
	bsd3 := bsd && (has("neither the name") || has("endorse or promote"))
	add(has("apache license") && has("version 2.0"), "Apache-2.0")
	add(has("mozilla public license version 2.0") || has("mozilla public license, version 2.0"), mplLabel)
	add(has("permission is hereby granted, free of charge"), "MIT")
	add(bsd3, "BSD-3-Clause")
	add(bsd && !bsd3, "BSD-2-Clause")
	add(has("with or without fee"), "ISC")
	add(has("additional ip rights grant"), "Go patent grant")
	add(has("free and unencumbered software"), "Unlicense")
	return strings.Join(l, " + ")
}

const mplLabel = "MPL-2.0"

// copyleft names the licences Ichor does not ship: its own licence is Apache-2.0, and only file-level
// MPL-2.0 is accepted. The MPL text itself names the GPL family as Secondary Licenses, so it is exempt.
var copyleft = []struct{ name, phrase string }{
	{"GPL", "gnu general public license"},
	{"LGPL", "gnu lesser general public license"},
	{"LGPL", "gnu library general public license"},
	{"AGPL", "gnu affero general public license"},
	{"SSPL", "server side public license"},
	{"BUSL", "business source license"},
}

// denied returns the copyleft licences a normalised body holds, or nil.
func denied(body string) []string {
	b := strings.ToLower(strings.Join(strings.Fields(body), " "))
	var l []string
	for _, c := range copyleft {
		if strings.Contains(b, c.phrase) && !slices.Contains(l, c.name) {
			l = append(l, c.name)
		}
	}
	return l
}

// A use is one component's claim on a variant.
type use struct {
	c      *component
	path   string
	nested bool
	copy   []string
	v      *variant
}

// A variant is one licence text, however many components carry it.
type variant struct {
	key, label, text, heading string
	uses                      []*use
}

func (v *variant) count() int { return len(v.uses) }

func (v *variant) rooted() bool {
	for _, u := range v.uses {
		if !u.nested {
			return true
		}
	}
	return false
}

type pool struct {
	byKey    map[string]*variant
	variants []*variant
	uses     map[*component][]*use
}

func buildPool(comps []component) (*pool, error) {
	slices.SortFunc(comps, func(a, b component) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.id(), b.id()))
	})
	p := &pool{byKey: map[string]*variant{}, uses: map[*component][]*use{}}
	add := func(c *component, f file) error {
		flat, text, cp := split(f.Text)
		body, lb := canon(flat), label(flat)
		if d := denied(flat); len(d) > 0 && !strings.Contains(lb, mplLabel) {
			return fmt.Errorf("%s %s: licence text matches %s, which Ichor does not ship; remove the dependency or decide in an ADR", c.id(), f.Path, strings.Join(d, " + "))
		}
		if lb == "" {
			return fmt.Errorf("%s %s: licence text of no known kind; teach label() about it", c.id(), f.Path)
		}
		if lb == mplLabel {
			body = lb // the texts differ in typography and in Exhibit B only; the fullest one stands for all
		}
		v := p.byKey[body]
		if v == nil {
			v = &variant{key: body, label: lb, text: text}
			p.byKey[body] = v
		}
		if lb == mplLabel && len(text) > len(v.text) {
			v.text = text
		}
		for _, u := range p.uses[c] {
			if u.v == v && u.nested == f.Nested && u.path == f.Path {
				return nil
			}
		}
		u := &use{c: c, path: f.Path, nested: f.Nested, copy: cp, v: v}
		v.uses = append(v.uses, u)
		p.uses[c] = append(p.uses[c], u)
		return nil
	}
	for i := range comps {
		for _, f := range comps[i].Files {
			if err := add(&comps[i], f); err != nil {
				return nil, err
			}
		}
	}
	// A nested file with the text of its own module's root file adds nothing.
	for i := range comps {
		c := &comps[i]
		var roots = map[*variant]bool{}
		for _, u := range p.uses[c] {
			if !u.nested {
				roots[u.v] = true
			}
		}
		kept := p.uses[c][:0]
		for _, u := range p.uses[c] {
			if u.nested && roots[u.v] {
				u.v.uses = slices.DeleteFunc(u.v.uses, func(x *use) bool { return x == u })
				continue
			}
			kept = append(kept, u)
		}
		p.uses[c] = kept
	}
	for _, v := range p.byKey {
		if len(v.uses) > 0 {
			p.variants = append(p.variants, v)
		}
	}
	// References to a label by components without a licence file of their own.
	for i := range comps {
		c := &comps[i]
		if c.RefLabel == "" {
			continue
		}
		v := p.resolve(c.RefLabel)
		if v == nil {
			return nil, fmt.Errorf("%s: no licence text labelled %s", c.id(), c.RefLabel)
		}
		u := &use{c: c, copy: c.Copyright, v: v}
		v.uses = append(v.uses, u)
		p.uses[c] = append(p.uses[c], u)
	}
	slices.SortFunc(p.variants, func(a, b *variant) int {
		return cmp.Or(cmp.Compare(a.label, b.label), cmp.Compare(b.count(), a.count()), cmp.Compare(a.key, b.key))
	})
	n := map[string]int{}
	for _, v := range p.variants {
		n[v.label]++
	}
	k := map[string]int{}
	for _, v := range p.variants {
		k[v.label]++
		v.heading = v.label
		if n[v.label] > 1 {
			v.heading = fmt.Sprintf("%s (variant %d of %d)", v.label, k[v.label], n[v.label])
		}
		// Within a variant: the uses in component order, which buildPool's sort fixed.
		slices.SortStableFunc(v.uses, func(a, b *use) int {
			return cmp.Or(cmp.Compare(a.c.Kind, b.c.Kind), cmp.Compare(a.c.id(), b.c.id()), cmp.Compare(a.path, b.path))
		})
	}
	return p, nil
}

// resolve returns the most used variant with exactly this label.
func (p *pool) resolve(l string) *variant {
	var best *variant
	for _, v := range p.variants {
		if v.label == l && (best == nil || v.count() > best.count()) {
			best = v
		}
	}
	return best
}

// ---- rendering

const (
	rule = "================================================================================"
	dash = "--------------------------------------------------------------------------------"
)

func repoURL(modPath string) string {
	p := strings.Split(modPath, "/")
	if p[0] == "github.com" && len(p) >= 3 {
		return "https://github.com/" + p[1] + "/" + p[2]
	}
	return "https://" + modPath
}

func subdir(modPath string) string {
	p := strings.Split(modPath, "/")
	if p[0] != "github.com" || len(p) <= 3 {
		return ""
	}
	if regexp.MustCompile(`^v\d+$`).MatchString(p[len(p)-1]) {
		p = p[:len(p)-1]
	}
	return strings.Join(p[3:], "/")
}

// A doc writes the notices file: the pool, the components and the text so far.
type doc struct {
	b     strings.Builder
	p     *pool
	comps []component
}

func (d *doc) printf(format string, a ...any) { fmt.Fprintf(&d.b, format, a...) }

func (d *doc) section(title string) { d.printf("\n%s\n%s\n%s\n\n", rule, title, rule) }

func (d *doc) heading(h string) { d.printf("\n%s\n%s\n%s\n\n", dash, h, dash) }

func (d *doc) entry(u *use, withPath bool) {
	name := u.c.id()
	if withPath && u.path != "" {
		name += " (" + u.path + ")"
	}
	d.printf("- %s\n", name)
	for _, c := range u.copy {
		d.printf("    %s\n", c)
	}
}

func render(comps []component) (string, error) {
	p, err := buildPool(comps)
	if err != nil {
		return "", err
	}
	d := &doc{p: p, comps: comps}
	d.printf("Ichor third-party notices\n\n")
	d.printf("Ichor is licensed under the Apache License, Version 2.0 (see LICENSE); Copyright 2026 maeshinshin.\n")
	d.printf("Ichor includes the components below, each under its own licence, and their licence texts, copyright notices and upstream notices follow.\n")
	d.mpl()
	d.notices()
	d.licences()
	d.nested()
	d.frontend()
	return strings.TrimRight(d.b.String(), "\n") + "\n", nil
}

func (d *doc) mpl() {
	d.section("1. Components under the Mozilla Public License 2.0")
	d.printf("The Source Code Form of each module below, at exactly the version named, is available from the\nupstream repository (the version tag) and from the Go module proxy (https://proxy.golang.org).\n\n")
	for i := range d.comps {
		c := &d.comps[i]
		var others []string
		isMPL := false
		for _, u := range d.p.uses[c] {
			if strings.Contains(u.v.label, mplLabel) {
				isMPL = true
			} else {
				others = append(others, u.v.label)
			}
		}
		if !isMPL {
			continue
		}
		esc, _ := module.EscapePath(c.Name)
		d.printf("- %s\n    Repository: %s", c.id(), repoURL(c.Name))
		if s := subdir(c.Name); s != "" {
			d.printf(" (directory %s)", s)
		}
		d.printf("\n    Module proxy: https://proxy.golang.org/%s/@v/%s.zip\n", esc, c.Version)
		if len(others) > 0 {
			d.printf("    Licensed per file: files that carry an MPL-2.0 header are under MPL-2.0, the others under %s.\n", strings.Join(others, ", "))
		}
	}
	for _, v := range d.p.variants {
		if v.label == mplLabel {
			d.printf("\n%s\n\n%s\n", v.heading, v.text)
		}
	}
}

func (d *doc) notices() {
	d.section("2. Upstream NOTICE texts")
	for i := range d.comps {
		for _, f := range d.comps[i].Notices {
			d.printf("%s: %s\n\n%s\n\n", d.comps[i].id(), f.Path, strings.TrimSpace(strings.ReplaceAll(f.Text, "\r", "")))
		}
	}
}

func (d *doc) licences() {
	d.section("3. Licence texts")
	d.printf("Each text is followed by the components that carry it and the copyright lines of their licence files.\n")
	for _, v := range d.p.variants {
		if !v.rooted() {
			continue
		}
		if v.label == mplLabel {
			d.printf("\n%s: see section 1.\n", v.heading)
		} else {
			d.heading(v.heading)
			d.printf("%s\n\nComponents:\n", v.text)
		}
		for _, u := range v.uses {
			if !u.nested {
				d.entry(u, false)
			}
		}
	}
}

func (d *doc) nested() {
	d.section("4. Licences inside the packages of a module")
	d.printf("Licence files in directories of packages that are linked, whose text differs from the module's own.\n")
	for _, v := range d.p.variants {
		var found []*use
		for _, u := range v.uses {
			if u.nested {
				found = append(found, u)
			}
		}
		if len(found) == 0 {
			continue
		}
		d.heading(v.heading)
		if v.rooted() || v.label == mplLabel {
			d.printf("Text: see above.\n")
		} else {
			d.printf("%s\n", v.text)
		}
		d.printf("\nFound in:\n")
		for _, u := range found {
			d.entry(u, true)
		}
	}
}

func (d *doc) listing(title string, kinds ...kind) {
	d.printf("%s\n\n", title)
	for i := range d.comps {
		c := &d.comps[i]
		if !slices.Contains(kinds, c.Kind) {
			continue
		}
		d.printf("- %s", c.id())
		if c.URL != "" {
			d.printf(" (%s)", c.URL)
		}
		d.printf("\n")
		if c.Note != "" {
			d.printf("    %s\n", c.Note)
		}
		for _, u := range d.p.uses[c] {
			where := "3"
			if u.v.label == mplLabel {
				where = "1"
			}
			d.printf("    Licence: %s (text in section %s)\n", u.v.heading, where)
			for _, l := range u.copy {
				d.printf("    %s\n", l)
			}
		}
	}
	d.printf("\n")
}

func (d *doc) frontend() {
	d.section("5. User interface")
	d.listing("Packages bundled into the UI:", kindNPM)
	d.listing("Packages whose CSS the UI stylesheet inlines:", kindCSS)
	d.printf("The UI components under ui/src derive from the preactcn registry (https://github.com/maeshinshin/preactcn),\nwhich credits the following upstream projects.\n\n")
	d.listing("Upstream projects:", kindUpstream)
	d.section("6. Cilium Helm chart")
	d.listing("Embedded chart:", kindChart)
}
