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
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// fakeClock is a settable clock for the renderer.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// newTestProgress is a renderer on a buffer with a clock the test moves. tty switches the terminal
// mode on (a fake terminal: no real file descriptor) with the given width and colours.
func newTestProgress(tty, color bool, width int) (*progress, *bytes.Buffer, *fakeClock) {
	var buf bytes.Buffer

	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	p := &progress{w: &buf, now: clock.now, size: func() int { return width }, tty: tty, color: color}
	p.began = clock.now()

	return p, &buf, clock
}

func TestProgress_PlainDeduplicatesTheDetailAndShowsElapsed(t *testing.T) {
	p, buf, clock := newTestProgress(false, false, 80)

	s := p.Start("Ichor controller")
	for range 5 {
		s.Update("rolling out (0/1 available)")
		clock.advance(time.Second)
	}

	s.Update("rolling out (1/1 available)")
	s.Update("rolling out (0/1 available)") // a repeat of an older detail still counts as a change
	clock.advance(67 * time.Second)
	s.Done("serving Ichor's webhooks")

	// No bare start line, and the 1/1 that the next update superseded is printed.
	want := "… Ichor controller — rolling out (0/1 available)\n" +
		"… Ichor controller — rolling out (1/1 available)\n" +
		"… Ichor controller — rolling out (0/1 available)\n" +
		"✓ Ichor controller — serving Ichor's webhooks (1m12s)\n"
	wantText(t, buf.String(), want)

	if strings.Contains(buf.String(), "\x1b") || strings.Contains(buf.String(), "\r") {
		t.Errorf("plain output has terminal control characters: %q", buf.String())
	}
}

func TestProgress_WarnFailAndNoticeLines(t *testing.T) {
	p, buf, clock := newTestProgress(false, false, 80)

	p.Warn("endpoint type \"node\": a VIP is recommended")
	p.Start("ichor-ui").Warn("not ready after 30m0s")

	failing := p.Start("Install Ichor")
	clock.advance(36 * time.Second)
	_ = failing.Fail(errors.New("init: applying Ichor's own Deployment \"x\": boom\nsecond line"))

	want := "! endpoint type \"node\": a VIP is recommended\n" +
		"! ichor-ui — not ready after 30m0s\n" +
		"✗ Install Ichor — failed (36s)\n" +
		"    init: applying Ichor's own Deployment \"x\": boom\n" +
		"    second line\n"
	wantText(t, buf.String(), want)
}

func TestProgress_FailReturnsTheErrorMarkedAsShown(t *testing.T) {
	p, _, _ := newTestProgress(false, false, 80)

	boom := errors.New("boom")

	err := p.Start("A").Fail(boom)
	if !errors.Is(err, boom) || !errorShown(err) || err.Error() != "boom" {
		t.Errorf("Fail returned %v (shown=%v)", err, errorShown(err))
	}

	if errorShown(boom) {
		t.Error("an error no step printed counts as shown")
	}

	if err := p.Run("B", func(*step) (string, error) { return "", boom }); !errorShown(err) {
		t.Errorf("Run did not mark the error: %v", err)
	}
}

// An error a failed step printed is not printed again by cobra; any other error still is.
func TestSilenceIfShown(t *testing.T) {
	run := func(fail func() error) string {
		var errw bytes.Buffer

		cmd := &cobra.Command{Use: "x", SilenceUsage: true, RunE: func(cmd *cobra.Command, _ []string) error {
			return silenceIfShown(cmd, fail())
		}}
		cmd.SetErr(&errw)
		cmd.SetArgs(nil)

		if err := cmd.Execute(); err == nil {
			t.Fatal("Execute returned no error")
		}

		return errw.String()
	}

	p, _, _ := newTestProgress(false, false, 80)

	if got := run(func() error { return p.Start("A").Fail(errors.New("boom")) }); got != "" {
		t.Errorf("a shown error was printed again by cobra: %q", got)
	}

	if got := run(func() error { return errors.New("flag is invalid") }); !strings.Contains(got, "flag is invalid") {
		t.Errorf("an error no step showed must still be printed: %q", got)
	}
}

func TestProgress_ElapsedFormat(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                                     "0s",
		36 * time.Second:                      "36s",
		59*time.Second + 600*time.Millisecond: "1m00s",
		72 * time.Second:                      "1m12s",
		242 * time.Second:                     "4m02s",
		3725 * time.Second:                    "1h02m05s",
	} {
		if got := formatElapsed(d); got != want {
			t.Errorf("formatElapsed(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestProgress_ElapsedUnderASecondIsLeftOut(t *testing.T) {
	p, buf, _ := newTestProgress(false, false, 80)

	p.Start("Secrets bundle").Attn("back this up now").Done("~/.ichor/clusters/x/secrets.yaml")

	if want := "✓ Secrets bundle — ~/.ichor/clusters/x/secrets.yaml  ← back this up now\n"; !strings.HasSuffix(buf.String(), want) {
		t.Errorf("got %q, want suffix %q", buf.String(), want)
	}
}

func TestProgress_StepWriterTurnsHelperMessagesIntoDetail(t *testing.T) {
	p, buf, clock := newTestProgress(false, false, 80)

	s := p.Start("Ichor controller").Rewrite(initDetail)
	for range 3 {
		_, _ = fmt.Fprintf(s, "init: waiting for the controller to serve Ichor's webhooks: "+
			"the Deployment ichor-controller-manager is not rolled out (1 updated, 0 available of 1)\n")
		clock.advance(time.Second)
	}

	_, _ = fmt.Fprintf(s, "wait: pod ichor-system/ichor-controller-manager-abc Pending: 0/1 nodes are available: 1 node(s) had untolerated taint(s). no new claims, preemption: no\n")

	// A pod reason goes stale: the next detail change no longer carries it.
	clock.advance(progressNoteTTL + time.Second)
	s.Update("rolling out (1/1 available)")
	s.Done("ready")

	want := "… Ichor controller — rolling out (0/1 available)\n" +
		"… Ichor controller — rolling out (0/1 available) · pod Pending: 0/1 nodes are available: 1 node(s) had untolerated taint(s).\n" +
		"… Ichor controller — rolling out (1/1 available)\n" +
		"✓ Ichor controller — ready (49s)\n"
	wantText(t, buf.String(), want)
}

// A detail that the end line repeats is not printed twice, and a step with no update prints its
// end line alone.
func TestProgress_PlainDoesNotRepeatTheFinalDetailOrPrintABareStart(t *testing.T) {
	p, buf, _ := newTestProgress(false, false, 80)

	s := p.Start("Install Ichor")
	s.Update("applying Ichor's install manifest")
	s.Update("2 objects")
	s.Done("2 objects")

	w := p.Start("Ichor controller")
	w.Update("not creating IchorInstallation")
	w.Warn("not creating IchorInstallation")

	p.Start("Secrets bundle").Done("~/s.yaml")

	want := "… Install Ichor — applying Ichor's install manifest\n" +
		"✓ Install Ichor — 2 objects\n" +
		"! Ichor controller — not creating IchorInstallation\n" +
		"✓ Secrets bundle — ~/s.yaml\n"
	wantText(t, buf.String(), want)
}

// A detail that stays for a while is printed while the step runs, not only at its end.
func TestProgress_PlainPrintsALongWaitsDetailWhileItRuns(t *testing.T) {
	p, buf, clock := newTestProgress(false, false, 80)

	s := p.Start("Ichor controller")
	s.Update("rolling out (0/1 available)")

	if buf.Len() != 0 {
		t.Fatalf("the newest detail is held back at first: %q", buf.String())
	}

	clock.advance(plainHold)
	s.Update("rolling out (0/1 available)")

	if want := "… Ichor controller — rolling out (0/1 available)\n"; buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestProgress_RunEndsTheStep(t *testing.T) {
	p, buf, _ := newTestProgress(false, false, 80)

	if err := p.Run("A", func(*step) (string, error) { return "ok", nil }); err != nil {
		t.Fatal(err)
	}

	boom := errors.New("boom")
	if err := p.Run("B", func(*step) (string, error) { return "", boom }); !errors.Is(err, boom) {
		t.Fatalf("Run = %v, want boom", err)
	}

	if err := p.Run("C", func(s *step) (string, error) { s.Warn("careful"); return "", nil }); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"✓ A — ok\n", "✗ B — failed\n    boom\n", "! C — careful\n"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, buf.String())
		}
	}
}

func TestProgress_NoColorKeepsTheLayoutOnATerminal(t *testing.T) {
	colored, cbuf, _ := newTestProgress(true, true, 80)
	plain, pbuf, _ := newTestProgress(true, false, 80)

	for _, p := range []*progress{colored, plain} {
		p.Start("Install Ichor").Done("104 objects")
	}

	if !strings.Contains(cbuf.String(), "\x1b[32m✓\x1b[0m") {
		t.Errorf("the tick is not green: %q", cbuf.String())
	}

	if strings.Contains(pbuf.String(), "\x1b[3") || strings.Contains(pbuf.String(), "\x1b[2m") {
		t.Errorf("colours with NO_COLOR: %q", pbuf.String())
	}

	if !strings.Contains(pbuf.String(), "  ✓ Install Ichor      104 objects") {
		t.Errorf("layout changed without colour: %q", pbuf.String())
	}
}

func TestNewProgress_NoColorAndDumbTerminalAreHonoured(t *testing.T) {
	// A buffer is never a terminal: plain mode, whatever the environment says.
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TERM", "dumb")

	p := newProgress(&bytes.Buffer{})
	if p.tty || p.color {
		t.Errorf("tty=%v color=%v for a writer that is no terminal", p.tty, p.color)
	}
}

func TestProgress_TerminalRedrawsInPlace(t *testing.T) {
	p, buf, clock := newTestProgress(true, false, 80)

	s := p.Start("Ichor controller")
	clock.advance(1200 * time.Millisecond)
	s.Update("rolling out (0/1 available)")
	clock.advance(70 * time.Second)
	s.Update("rolling out (1/1 available)")
	s.Done("ready")

	out := buf.String()
	for _, w := range []string{
		"\r\x1b[2K  ⠋ Ichor controller     0s",                            // started: spinner frame 0, no detail
		"\r\x1b[2K  ⠹ Ichor controller   rolling out (0/1 available)  1s", // 1.2s: frame 12 mod 10 = 2
		"rolling out (1/1 available)  1m11s",
		"\r\x1b[2K  ✓ Ichor controller   ready",
		"1m11s\n",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%q", w, out)
		}
	}

	if strings.Count(out, "\n") != 1 {
		t.Errorf("a live line must not end in a newline until the step is over: %q", out)
	}
}

func TestProgress_TerminalTruncatesTheLiveLineToTheWidth(t *testing.T) {
	const width = 40

	p, buf, clock := newTestProgress(true, false, width)

	s := p.Start("Ichor controller")
	clock.advance(5 * time.Second)
	s.Update(strings.Repeat("x", 200))

	last := buf.String()[strings.LastIndex(buf.String(), "\r\x1b[2K")+len("\r\x1b[2K"):]
	if n := len([]rune(last)); n > width-1 {
		t.Errorf("live line is %d columns, want at most %d: %q", n, width-1, last)
	}

	if !strings.Contains(last, "…") || !strings.HasSuffix(last, "  5s") {
		t.Errorf("live line should end in an ellipsis in the detail and keep the time: %q", last)
	}

	// The final line keeps everything: it is not redrawn, so it may wrap.
	s.Done(strings.Repeat("y", 100))

	if !strings.Contains(buf.String(), strings.Repeat("y", 100)) {
		t.Errorf("the final detail was cut")
	}
}

func TestProgress_TerminalBlocksAreSeparatedByBlankLines(t *testing.T) {
	p, buf, _ := newTestProgress(true, false, 80)

	p.Header("Ichor init · c", "single · Talos v1")
	p.Warn("endpoint type \"node\": a VIP is recommended")
	p.Start("Secrets bundle").Done("~/s.yaml")
	p.Done()
	p.Warn("ichor-ui has no sign-in")
	p.Next("ichor ui")

	want := "Ichor init · c\n  single · Talos v1\n" +
		"\n  ! endpoint type \"node\": a VIP is recommended\n" +
		"\n\r\x1b[2K  ⠋ Secrets bundle       0s\r\x1b[2K  ✓ Secrets bundle     ~/s.yaml\n" +
		"\n  Done in 0s\n" +
		"\n  ! ichor-ui has no sign-in\n" +
		"\n  Next\n    ichor ui\n"
	if buf.String() != want {
		t.Errorf("got:\n%q\nwant:\n%q", buf.String(), want)
	}
}

// A full fake run on the plain renderer, printed with -v: the log a CI job sees.
func TestProgress_PlainSampleOfAWholeRun(t *testing.T) {
	p, buf, clock := newTestProgress(false, false, 80)
	step := func(name string, secs int, detail string) {
		s := p.Start(name)
		clock.advance(time.Duration(secs) * time.Second)
		s.Done(detail)
	}

	p.Header("Ichor init · initshow", "single · Talos v1.14.5 · Kubernetes v1.36.6 · Cilium 1.20.4")
	p.Warn("endpoint type \"node\": a VIP is recommended — changing it later re-issues every certificate")
	p.Start("Secrets bundle").Attn("back this up now").Done("~/.ichor/clusters/initshow/secrets.yaml")
	step("Configure node", 50, "10.244.1.136 installed Talos and rebooted")
	step("Bootstrap etcd", 20, "kubeconfig saved")
	step("Kubernetes API", 20, "reachable")
	step("Install Ichor", 36, "104 objects")
	p.Done()
	p.Next("ichor cluster status initshow")

	t.Logf("\n%s", buf.String())
}

// The ticker goroutine redraws the live line and stops drawing once the step is over (run with -race).
func TestProgress_TickerRedrawsUntilTheStepEnds(t *testing.T) {
	var mu sync.Mutex

	var buf bytes.Buffer

	p := &progress{w: lockedWriter{&mu, &buf}, now: time.Now, size: func() int { return 80 }, tty: true, tick: 2 * time.Millisecond}
	p.began = time.Now()

	s := p.Start("Ichor controller")

	time.Sleep(40 * time.Millisecond)
	s.Done("ready")

	mu.Lock()
	n := buf.Len()
	redraws := strings.Count(buf.String(), "\r\x1b[2K")
	mu.Unlock()

	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if redraws < 4 {
		t.Errorf("only %d draws in 40ms at a 2ms tick", redraws)
	}

	if buf.Len() != n {
		t.Errorf("the ticker drew after the step ended")
	}
}

type lockedWriter struct {
	mu  *sync.Mutex
	buf *bytes.Buffer
}

func (w lockedWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.buf.Write(b)
}
