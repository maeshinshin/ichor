/*
Copyright 2026 maeshinshin.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cli

import (
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Human output. A command prints for a person unless -o yaml|json was given:
//
//	func runThing(out io.Writer, ...) error {
//		if outputFormat != outputHuman {
//			return writeMessage(out, outputFormat, msg) // today's machine output, unchanged
//		}
//
//		h := newHuman(out)
//		h.Table([]string{"name", "role", "ready", "age"}, [][]string{{n, humanEnum(r.String()), h.Mark(markOK), humanAge(ts)}})
//		h.Block("initshow · ichor-system", kv{"Talos", "v1.14.5"}, kv{"Health", h.Mark(markOK) + " ready"})
//
//		return h.Err()
//	}
//
// A command that has not been converted needs nothing: writeMessage prints YAML for an unset -o.
// The helpers reuse progress.go's terminal detection (a terminal that is not TERM=dumb gets colour
// unless NO_COLOR is set; a pipe gets plain text) and never truncate: a long cell stays whole.
//
// Table, Block, Mark, humanEnum, humanAge and humanUntil are the vocabulary; conditionMark picks
// the glyph of a status condition. They write with the first error kept in Err.

// The status values of a condition (metav1.ConditionTrue and False, as the wire message carries them).
const (
	condTrue  = "True"
	condFalse = "False"
	// condPaused is the Paused condition type and phase.
	condPaused = "Paused"
)

// outputHuman is the -o value of "not given": print for a person.
const outputHuman = ""

// humanNow is the clock the ages are taken against; tests replace it.
var humanNow = time.Now

// mark is a status glyph: ok ✓, warn !, fail ✗, pending …
type mark int

const (
	markOK mark = iota
	markWarning
	markFailed
	markPending
)

// kv is one label and value of a Block. A row with an empty value is left out.
type kv struct{ Key, Val string }

// human writes tables and key/value blocks to w.
type human struct {
	w    io.Writer
	p    *progress // only for its terminal detection and paint
	err  error
	nblk int
}

// newHuman prints to w, with colour when w is a terminal (and NO_COLOR is unset).
func newHuman(w io.Writer) *human { return &human{w: w, p: newProgress(w)} }

// Err is the first write error, if any.
func (h *human) Err() error { return h.err }

func (h *human) print(s string) {
	if h.err == nil {
		_, h.err = io.WriteString(h.w, s)
	}
}

// Paint wraps s in an SGR code (sgrDim, sgrBold, ...) on a colour terminal.
func (h *human) Paint(sgr, s string) string { return h.p.paint(sgr, s) }

// Mark is the glyph for a status, coloured on a terminal.
func (h *human) Mark(m mark) string {
	switch m {
	case markOK:
		return h.p.paint(sgrGreen, markDone)
	case markWarning:
		return h.p.paint(sgrYellow, markWarn)
	case markFailed:
		return h.p.paint(sgrRed, markFail)
	default:
		return h.p.paint(sgrDim, markRun)
	}
}

var ansiSeq = regexp.MustCompile("\x1b\\[[0-9;]*m")

// width is the printed width of s: its runes, without colour codes.
func width(s string) int { return len([]rune(ansiSeq.ReplaceAllString(s, ""))) }

const tableGap = "  "

// Table prints an uppercase header (dim on a colour terminal) and the rows, each column as wide as
// its widest cell, kubectl style: no borders, two spaces between columns, no trailing spaces. An
// empty cell prints "-". Cells may hold colour codes (Mark); the alignment ignores them.
func (h *human) Table(header []string, rows [][]string) {
	widths := make([]int, len(header))
	head := make([]string, len(header))

	for i, c := range header {
		head[i] = strings.ToUpper(c)
		widths[i] = width(head[i])
	}

	cells := make([][]string, len(rows))

	for r, row := range rows {
		cells[r] = make([]string, len(header))

		for i := range header {
			c := ""
			if i < len(row) {
				c = row[i]
			}

			if c == "" {
				c = "-"
			}

			cells[r][i] = c
			widths[i] = max(widths[i], width(c))
		}
	}

	line := func(cols []string, style string) {
		var b strings.Builder

		for i, c := range cols {
			if i == len(cols)-1 {
				b.WriteString(h.p.paint(style, c))

				break
			}

			b.WriteString(h.p.paint(style, c) + strings.Repeat(" ", widths[i]-width(c)) + tableGap)
		}

		h.print(b.String() + "\n")
	}

	line(head, sgrDim)

	for _, row := range cells {
		line(row, "")
	}
}

// fieldLabelWidth is the narrowest label column of a Block, so blocks of short labels line up the
// same everywhere.
const fieldLabelWidth = 12

// Block prints an optional bold title and below it the labelled values, the labels dim in an
// aligned column. With a title the rows are indented two spaces; a block without one starts at
// the left edge. A blank line separates two blocks.
func (h *human) Block(title string, rows ...kv) {
	if !slices.ContainsFunc(rows, func(r kv) bool { return r.Val != "" }) {
		return
	}

	if h.nblk > 0 {
		h.print("\n")
	}

	h.nblk++

	indent := ""

	if title != "" {
		h.print(h.p.paint(sgrBold, title) + "\n")

		indent = "  "
	}

	w := fieldLabelWidth

	for _, r := range rows {
		if r.Val != "" {
			w = max(w, width(r.Key))
		}
	}

	for _, r := range rows {
		if r.Val == "" {
			continue
		}

		h.print(indent + h.p.paint(sgrDim, padRight(r.Key, w)) + " " + r.Val + "\n")
	}
}

// Title starts a block whose body is lines (Line) instead of labelled values.
func (h *human) Title(title string) {
	if h.nblk > 0 {
		h.print("\n")
	}

	h.nblk++
	h.print(h.p.paint(sgrBold, title) + "\n")
}

// Note prints one plain line ("no clusters").
func (h *human) Note(s string) { h.print(s + "\n") }

// Line prints one indented line under a Title.
func (h *human) Line(s string) { h.print("  " + s + "\n") }

// Conditions prints a Block of status conditions, one line each: glyph, type, reason, message.
func (h *human) Conditions(title string, conds []conditionRow) {
	if len(conds) == 0 {
		return
	}

	h.Title(title)

	w := 0
	for _, c := range conds {
		w = max(w, len(c.Type))
	}

	rw := 0
	for _, c := range conds {
		rw = max(rw, len(c.Reason))
	}

	for _, c := range conds {
		line := h.Mark(c.Mark) + " " + padRight(c.Type, w)
		if c.Reason != "" {
			line += "  " + padRight(c.Reason, rw)
		}

		if c.Message != "" {
			line += "  " + h.p.paint(sgrDim, c.Message)
		}

		h.Line(line)
	}
}

// conditionRow is one line of Conditions.
type conditionRow struct {
	Mark                  mark
	Type, Reason, Message string
}

// Polarity of the condition types Ichor sets: most are good when True (Ready, Provisioned,
// NetworkPoliciesApplied ...); these are a problem or advice when True, and these are in progress.
var (
	problemWhenTrue = map[string]bool{
		"ConfigWarnings": true, "CARotationAdvised": true, "CNIUnsupported": true, "CiliumUpgradeAvailable": true,
		"CertificatesExpiring": true, "AutoPatchHalted": true, "CAClientsNotRestarted": true, "BundleBackupStale": true,
		"RemovedAPIsInUse": true, "AdoptionHeld": true, "ExternalSuggested": true, condPaused: true,
	}
	progressWhenTrue = map[string]bool{
		"Transitioning": true, "VerifyingNewSide": true, "ConfigRollout": true, "KubernetesUpgrade": true,
		"TalosUpgrade": true, "CARotation": true, "Removal": true, "Shutdown": true, "CNIHandover": true,
	}
)

// conditionMark is the glyph of a condition of type typ and status "True", "False" or "Unknown".
func conditionMark(typ, status string) mark {
	switch {
	case status != condTrue && status != condFalse:
		return markPending
	case problemWhenTrue[typ]:
		if status == condTrue {
			return markWarning
		}

		return markOK
	case progressWhenTrue[typ]:
		if status == condTrue {
			return markPending
		}

		return markOK
	case status == condTrue:
		return markOK
	case typ == conditionReady:
		return markFailed
	default:
		return markWarning
	}
}

// enumPrefixes maps every enum value name of the compiled-in protobuf files to its short form:
// MACHINE_ROLE_CONTROL_PLANE -> CONTROL_PLANE, by dropping the enum type's own name in upper snake
// case, which is how proto style prefixes values.
var enumPrefixes = sync.OnceValue(func() map[string]string {
	m := map[string]string{}

	protoregistry.GlobalFiles.RangeFiles(func(f protoreflect.FileDescriptor) bool {
		enums := f.Enums()
		for i := range enums.Len() {
			e := enums.Get(i)
			prefix := upperSnake(string(e.Name())) + "_"
			vals := e.Values()

			for j := range vals.Len() {
				name := string(vals.Get(j).Name())
				m[name] = strings.TrimPrefix(name, prefix)
			}
		}

		return true
	})

	return m
})

func upperSnake(s string) string {
	var b strings.Builder

	for i, r := range s {
		if unicode.IsUpper(r) && i > 0 {
			b.WriteByte('_')
		}

		b.WriteRune(unicode.ToUpper(r))
	}

	return b.String()
}

// humanEnum turns a protobuf enum value name into what a person types: MACHINE_ROLE_CONTROL_PLANE
// is "control-plane". The *_UNSPECIFIED value is "". A string that is no known enum value is only
// lowercased and its underscores turned into dashes.
func humanEnum(s string) string {
	short, ok := enumPrefixes()[s]
	if !ok {
		short = s
	}

	if short == "UNSPECIFIED" {
		return ""
	}

	return strings.ToLower(strings.ReplaceAll(short, "_", "-"))
}

// humanSpan is a duration as one unit: 45s, 3m, 2h, 5d.
func humanSpan(d time.Duration) string {
	d = max(d, 0)

	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
}

// humanAge is how long ago ts was ("3m", "2h", "5d"), or "" when it is not set.
func humanAge(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return ""
	}

	return humanSpan(humanNow().Sub(ts.AsTime()))
}

// humanUntil is when ts comes: "in 364d", "expired 2d ago", or "" when it is not set.
func humanUntil(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return ""
	}

	d := ts.AsTime().Sub(humanNow())
	if d < 0 {
		return "expired " + humanSpan(-d) + " ago"
	}

	return "in " + humanSpan(d)
}

// shortNames shortens names that are long (the UUID a machine is named after) to their first eight
// characters, but only where that stays unique among names; otherwise the name stays whole.
func shortNames(names []string) []string {
	const n = 8

	count := map[string]int{}

	for _, s := range names {
		count[prefixOf(s, n)]++
	}

	out := make([]string, len(names))

	for i, s := range names {
		out[i] = s
		if p := prefixOf(s, n); count[p] == 1 {
			out[i] = p
		}
	}

	return out
}

func prefixOf(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}

	return s
}
