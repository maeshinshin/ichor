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
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

// progress renders the steps of a long command (ichor init today; adopt and --follow can use it
// later) as a short list: one line per step, a spinner on the one that runs.
//
// On a terminal the running step is one live line redrawn in place; when it ends it is replaced by
// its final line. Anywhere else (a pipe, CI, TERM=dumb) there are no escape sequences and no
// spinner: a line is printed when a step's detail changes (never the same detail twice in a row,
// and the newest one is held back for plainHold, so that a detail the end line repeats is not
// printed twice) and when it ends, so a log stays short and grep-able.
//
//	steps := newProgress(os.Stderr)
//	s := steps.Start("Install Ichor")
//	s.Update("applying 104 objects")
//	s.Done("104 objects") // or s.Warn("...") or s.Fail(err)
type progress struct {
	mu sync.Mutex
	w  io.Writer

	tty   bool // w is a terminal: live line, colours unless NO_COLOR
	color bool
	size  func() int    // terminal width in columns
	tick  time.Duration // spinner redraw interval; zero means no ticker goroutine (tests)
	now   func() time.Time

	began time.Time
	cur   *step  // the running step, if any
	last  string // kind of the last block printed, for the blank line between blocks (terminal only)
}

const (
	progressNameWidth   = 18 // the step-name column
	progressDetailWidth = 46 // the detail column of a finished line, so the times line up
	progressTick        = 100 * time.Millisecond
	progressNoteTTL     = 45 * time.Second // a pod reason older than this is stale (waitDiagEvery is 30s)
	progressNoteMax     = 100              // longest pod reason shown, in runes
	plainHold           = 2 * time.Second  // how long plain mode holds back the newest detail
)

const (
	sgrBold   = "1"
	sgrDim    = "2"
	sgrRed    = "31"
	sgrGreen  = "32"
	sgrYellow = "33"

	ansiEraseLine = "\r\x1b[2K"

	markDone = "✓"
	markWarn = "!"
	markFail = "✗"
	markRun  = "…"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// newProgress renders to w. w is a terminal when it is a file whose descriptor is one and TERM is
// not "dumb"; NO_COLOR (set and not empty) keeps the layout and drops the colours.
func newProgress(w io.Writer) *progress {
	p := &progress{w: w, now: time.Now, size: func() int { return 80 }}

	if f, ok := w.(interface{ Fd() uintptr }); ok && os.Getenv("TERM") != "dumb" {
		fd := int(f.Fd()) //nolint:gosec // a file descriptor fits an int

		if term.IsTerminal(fd) {
			p.tty = true
			p.tick = progressTick
			p.color = os.Getenv("NO_COLOR") == ""
			p.size = func() int {
				if cols, _, err := term.GetSize(fd); err == nil && cols > 0 {
					return cols
				}

				return 80
			}
		}
	}

	p.began = p.now()

	return p
}

// Header prints the command's title and, dimmed below it, a one-line summary.
func (p *progress) Header(title, summary string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.eraseLocked()
	p.last = "head"
	p.out(p.paint(sgrBold, title) + "\n")

	if summary != "" {
		p.out(p.indent() + p.paint(sgrDim, summary) + "\n")
	}

	p.drawLocked()
}

// Warn prints a warning that is not tied to a step.
func (p *progress) Warn(msg string) {
	p.notice(markWarn, sgrYellow, msg, "")
}

// Info prints a plain note that is not tied to a step.
func (p *progress) Info(msg string) {
	p.notice(" ", "", msg, sgrDim)
}

func (p *progress) notice(mark, markSGR, msg, msgSGR string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.eraseLocked()
	p.gapLocked("note")

	// Wrapping measures plain text, so the colours go on each line after it. A log keeps one line.
	lines := []string{msg}
	if p.tty {
		lines = wrapLines(msg, p.size()-1-len([]rune(p.indent()))-2) // less the indent and the mark's space
	}

	for i, l := range lines {
		lead := p.indent() + "  " // a continuation lines up under the text
		if i == 0 {
			lead = p.indent() + p.paint(markSGR, mark) + " "
		}

		p.out(lead + p.paint(msgSGR, l) + "\n")
	}

	p.drawLocked()
}

// WarnWriter is an io.Writer that turns every line written to it into a Warn, dropping a leading
// "warning: ", for code that takes a writer for its warnings.
func (p *progress) WarnWriter() io.Writer {
	return lineWriter(func(line string) { p.Warn(strings.TrimPrefix(line, "warning: ")) })
}

// Write passes b through untouched (a line that must stay verbatim, like a generated password),
// without breaking the live line.
func (p *progress) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.eraseLocked()
	p.last = "raw"
	p.out(string(b))
	p.drawLocked()

	return len(b), nil
}

// Done prints how long the whole run took.
func (p *progress) Done() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.eraseLocked()
	p.gapLocked("done")
	p.out(p.indent() + p.paint(sgrBold, "Done in "+formatElapsed(p.now().Sub(p.began))) + "\n")
}

// Next prints the commands to run next.
func (p *progress) Next(cmds ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.eraseLocked()
	p.gapLocked("next")
	p.out(p.indent() + p.paint(sgrBold, "Next") + "\n")

	for _, c := range cmds {
		p.out(p.indent() + "  " + c + "\n")
	}
}

// Run runs fn as one step: the returned detail ends it as Done, an error as Fail. fn may end the
// step itself (Warn); Run then leaves it alone.
func (p *progress) Run(name string, fn func(*step) (string, error)) error {
	s := p.Start(name)

	detail, err := fn(s)

	switch {
	case s.isEnded():
	case err != nil:
		err = s.Fail(err)
	default:
		s.Done(detail)
	}

	return err
}

// step is one running line of a progress list. It is also an io.Writer: each line written to it
// becomes its detail, so a wait helper that reports to an io.Writer can report to a step.
type step struct {
	p     *progress
	name  string
	began time.Time

	detail  string
	printed string // what the non-terminal mode last printed, to print a change only
	pending string // the newest detail, not printed yet (non-terminal mode)
	pendAt  time.Time
	note    string // a pod reason, shown after the detail while fresh
	noteAt  time.Time
	attn    string // shown after the final detail in yellow, e.g. "back this up now"

	rewrite func(string) string // maps a written line to a detail
	ended   bool
	stop    chan struct{}
}

// Start begins a step. A step still running is ended as done first: steps run one at a time.
func (p *progress) Start(name string) *step {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cur != nil {
		p.endLocked(p.cur, markDone, sgrGreen, p.cur.detail, "")
	}

	s := &step{p: p, name: name, began: p.now()}
	p.cur = s

	if !p.tty {
		return s
	}

	p.gapLocked("step")
	p.drawLocked()

	if p.tick > 0 {
		s.stop = make(chan struct{})

		go p.spin(s)
	}

	return s
}

func (p *progress) spin(s *step) {
	t := time.NewTicker(p.tick)
	defer t.Stop()

	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			p.mu.Lock()
			if !s.ended {
				p.drawLocked()
			}
			p.mu.Unlock()
		}
	}
}

// Rewrite sets how a line written to the step becomes its detail. It returns s.
func (s *step) Rewrite(fn func(string) string) *step {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	s.rewrite = fn

	return s
}

// Attn sets a short call to action shown in yellow after the final detail.
func (s *step) Attn(text string) *step {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	s.attn = text

	return s
}

// Detail is the latest detail, without any pod reason.
func (s *step) Detail() string {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return s.detail
}

func (s *step) isEnded() bool {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	return s.ended
}

// Update sets the detail of the running step.
func (s *step) Update(detail string) {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	s.setDetailLocked(detail)
}

func (s *step) setDetailLocked(detail string) {
	if s.ended || detail == "" {
		return
	}

	s.detail = detail
	s.showLocked()
}

// showLocked redraws the live line, or in the non-terminal mode prints the detail if it changed.
func (s *step) showLocked() {
	p := s.p
	if p.tty {
		p.drawLocked()

		return
	}

	d := s.composedLocked()

	if d == s.pending {
		if p.now().Sub(s.pendAt) >= plainHold {
			s.flushLocked()
		}

		return
	}

	// A new text: the held one has been superseded, so it is printed now.
	s.flushLocked()

	if d != s.printed {
		s.pending, s.pendAt = d, p.now()
	}
}

// flushLocked prints the held-back detail (non-terminal mode).
func (s *step) flushLocked() {
	if s.pending != "" && s.pending != s.printed {
		s.printed = s.pending
		s.p.out(markRun + " " + s.name + " — " + s.pending + "\n")
	}

	s.pending = ""
}

// composedLocked is the detail followed by the pod reason, when there is a fresh one.
func (s *step) composedLocked() string {
	if s.note == "" || s.p.now().Sub(s.noteAt) > progressNoteTTL {
		return s.detail
	}

	if s.detail == "" {
		return s.note
	}

	return s.detail + " · " + s.note
}

// Write takes each line as the step's detail. "init: " prefixes and the "waiting for X: " lead of
// pollWhile are dropped; a "wait: pod ..." line from waitDiag becomes the pod reason shown after it.
func (s *step) Write(b []byte) (int, error) {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	for line := range strings.SplitSeq(string(b), "\n") {
		s.ingestLocked(strings.TrimSpace(line))
	}

	return len(b), nil
}

func (s *step) ingestLocked(line string) {
	line = strings.TrimPrefix(strings.TrimPrefix(line, "init: "), "adopt: ")
	if line == "" || s.ended {
		return
	}

	if pod, ok := strings.CutPrefix(line, "wait: pod "); ok {
		// "<ns>/<name> Pending: <why>" -> "pod Pending: <why>"
		if _, why, ok := strings.Cut(pod, " "); ok {
			s.note = "pod " + firstSentence(why, progressNoteMax)
			s.noteAt = s.p.now()
			s.showLocked()
		}

		return
	}

	if rest, ok := strings.CutPrefix(line, "waiting for "); ok {
		if _, msg, ok := strings.Cut(rest, ": "); ok {
			line = msg
		}
	}

	if s.rewrite != nil {
		line = s.rewrite(line)
	}

	s.setDetailLocked(line)
}

// Done ends the step as a success.
func (s *step) Done(detail string) { s.end(markDone, sgrGreen, detail, nil) }

// Warn ends the step with a warning: it finished, but the reader should look at the detail.
func (s *step) Warn(detail string) { s.end(markWarn, sgrYellow, detail, nil) }

// Fail ends the step as failed and prints err indented below it. It returns err marked as shown,
// so that the caller returns it and Execute does not print it a second time.
func (s *step) Fail(err error) error {
	s.end(markFail, sgrRed, "failed", err)

	return shownErr(err)
}

// shownError is an error a progress step already printed in full.
type shownError struct{ error }

func (e shownError) Unwrap() error { return e.error }

func shownErr(err error) error {
	if err == nil || errorShown(err) {
		return err
	}

	return shownError{err}
}

// errorShown reports whether err was printed by a failed step already.
func errorShown(err error) bool {
	var s shownError

	return errors.As(err, &s)
}

func (s *step) end(mark, style, detail string, err error) {
	s.p.mu.Lock()
	defer s.p.mu.Unlock()

	if s.ended {
		return
	}

	msg := ""
	if err != nil {
		msg = err.Error()
	}

	s.p.endLocked(s, mark, style, detail, msg)
}

func (p *progress) endLocked(s *step, mark, style, detail, errMsg string) {
	s.ended = true

	if s.stop != nil {
		close(s.stop)
	}

	if p.cur == s {
		p.cur = nil
	}

	if !p.tty && s.pending != detail {
		s.flushLocked()
	}

	elapsed := ""
	if d := p.now().Sub(s.began); d >= time.Second {
		elapsed = formatElapsed(d)
	}

	if p.tty {
		p.out(ansiEraseLine + p.rowLocked(mark, style, s.name, detail, s.attn, elapsed, true, 0) + "\n")
	} else {
		line := mark + " " + s.name
		if detail != "" {
			line += " — " + detail
		}

		if s.attn != "" {
			line += "  ← " + s.attn
		}

		if elapsed != "" {
			line += " (" + elapsed + ")"
		}

		p.out(line + "\n")
	}

	if errMsg != "" {
		for l := range strings.SplitSeq(strings.TrimRight(errMsg, "\n"), "\n") {
			p.out(p.indent() + "    " + l + "\n")
		}
	}
}

// drawLocked redraws the running step's live line (terminal only).
func (p *progress) drawLocked() {
	s := p.cur
	if !p.tty || s == nil {
		return
	}

	elapsed := p.now().Sub(s.began)
	frame := spinnerFrames[int(elapsed/progressTick)%len(spinnerFrames)]

	p.out(ansiEraseLine + p.rowLocked(frame, "", s.name, s.composedLocked(), "", formatElapsed(elapsed), false, p.size()))
}

// eraseLocked clears the live line so that a line printed now does not land on it.
func (p *progress) eraseLocked() {
	if p.tty && p.cur != nil {
		p.out(ansiEraseLine)
	}
}

// gapLocked puts a blank line between two different kinds of block (terminal only).
func (p *progress) gapLocked(kind string) {
	if p.tty && p.last != "" && p.last != kind {
		p.out("\n")
	}

	p.last = kind
}

// rowLocked lays out one terminal line: mark, name, detail, an optional call to action, the time.
// With width > 0 the line is cut to width-1 columns (a wrapped live line cannot be redrawn); with
// final it is padded so the times of the finished lines line up.
func (p *progress) rowLocked(mark, style, name, detail, attn, elapsed string, final bool, width int) string {
	head := p.indent() + mark + " " + padRight(name, progressNameWidth) + " "

	attnText := ""
	if attn != "" {
		attnText = "  ← " + attn
	}

	tailLen := 0
	if elapsed != "" {
		tailLen = 2 + len([]rune(elapsed))
	}

	if width > 0 {
		detail = truncate(detail, width-1-len([]rune(head))-len([]rune(attnText))-tailLen)
	}

	pad := ""
	if final && elapsed != "" {
		pad = strings.Repeat(" ", max(0, progressDetailWidth-len([]rune(detail))-len([]rune(attnText))))
	}

	out := p.indent() + p.paint(style, mark) + " " + padRight(name, progressNameWidth) + " " + p.paint(sgrDim, detail)
	if attnText != "" {
		out += p.paint(sgrYellow, attnText)
	}

	out += pad

	if elapsed != "" {
		out += "  " + p.paint(sgrDim, elapsed)
	}

	return out
}

func (p *progress) indent() string {
	if p.tty {
		return "  "
	}

	return ""
}

func (p *progress) paint(sgr, s string) string {
	if !p.color || sgr == "" || s == "" {
		return s
	}

	return "\x1b[" + sgr + "m" + s + "\x1b[0m"
}

func (p *progress) out(s string) { _, _ = io.WriteString(p.w, s) }

// formatElapsed is 36s, 1m12s or 1h02m05s.
func formatElapsed(d time.Duration) string {
	secs := int(d.Round(time.Second) / time.Second)

	switch {
	case secs < 60:
		return fmt.Sprintf("%ds", secs)
	case secs < 3600:
		return fmt.Sprintf("%dm%02ds", secs/60, secs%60)
	default:
		return fmt.Sprintf("%dh%02dm%02ds", secs/3600, secs%3600/60, secs%60)
	}
}

func padRight(s string, n int) string {
	if k := n - len([]rune(s)); k > 0 {
		return s + strings.Repeat(" ", k)
	}

	return s
}

// truncate cuts s to n runes, ending with "…" when it cut.
func truncate(s string, n int) string {
	r := []rune(s)

	switch {
	case len(r) <= n:
		return s
	case n <= 0:
		return ""
	default:
		return string(r[:n-1]) + "…"
	}
}

// wrapLines breaks s at spaces into lines of at most width runes. A word longer than width keeps a
// line of its own, uncut: it is usually a path or a field name the reader has to copy. A width of
// zero or less leaves s whole.
func wrapLines(s string, width int) []string {
	if width <= 0 || len([]rune(s)) <= width {
		return []string{s}
	}

	words := strings.Split(s, " ")
	lines := []string{}
	cur := words[0]

	for _, w := range words[1:] {
		if len([]rune(cur))+1+len([]rune(w)) <= width {
			cur += " " + w
		} else {
			lines = append(lines, cur)
			cur = w
		}
	}

	return append(lines, cur)
}

// firstSentence is s up to its first ". " (a scheduler message goes on with detail nobody needs
// in one line), cut to n runes.
func firstSentence(s string, n int) string {
	if i := strings.Index(s, ". "); i >= 0 {
		s = s[:i+1]
	}

	return truncate(s, n)
}

// lineWriter calls fn for every non-empty line written to it.
type lineWriter func(line string)

func (f lineWriter) Write(b []byte) (int, error) {
	for l := range strings.SplitSeq(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			f(l)
		}
	}

	return len(b), nil
}
