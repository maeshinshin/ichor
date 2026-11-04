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
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// humanTestNow is the fixed clock of the human output tests.
// Values the human output tests repeat.
const (
	humanControlPlane = "control-plane"
	humanK8s          = "v1.36.6"
	humanUpToDate     = "up-to-date"
	humanCluster      = "initshow"
	humanSvcCIDR      = "10.96.0.0/12"
	humanLatest       = "v0.3.0"
	humanEtcd         = "etcd"
	humanElsewhere    = "elsewhere"
)

var humanTestNow = time.Date(2026, 11, 4, 3, 13, 39, 0, time.UTC)

// fixedHumanClock pins the clock the ages are taken against.
func fixedHumanClock(t *testing.T) {
	t.Helper()

	old := humanNow
	humanNow = func() time.Time { return humanTestNow }

	t.Cleanup(func() { humanNow = old })
}

// ttyHuman is a human on a fake colour terminal.
func ttyHuman() (*human, *bytes.Buffer) {
	p, buf, _ := newTestProgress(true, true, 80)

	return &human{w: buf, p: p}, buf
}

func plainHuman() (*human, *bytes.Buffer) {
	p, buf, _ := newTestProgress(false, false, 80)

	return &human{w: buf, p: p}, buf
}

func TestHumanTableAlignsColumnsAndLeavesNoTrailingSpaces(t *testing.T) {
	h, buf := plainHuman()
	h.Table([]string{"id", "ready", "age"}, [][]string{
		{"alpha", h.Mark(markOK), "5m"},
		{"a-much-longer-name", "", "2h"},
	})

	want := "ID                  READY  AGE\n" +
		"alpha               ✓      5m\n" +
		"a-much-longer-name  -      2h\n"
	if buf.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestHumanTableOnATerminalAlignsPastTheColourCodes(t *testing.T) {
	h, buf := ttyHuman()
	h.Table([]string{"id", "ready", "age"}, [][]string{{"a", h.Mark(markOK), "1m"}, {"bb", h.Mark(markFailed), "2m"}})

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if !strings.HasPrefix(lines[0], "\x1b[2mID\x1b[0m") {
		t.Errorf("header is not dim: %q", lines[0])
	}

	// the rows are as wide as the header's last column allows, whatever the colour codes add
	if width(lines[1]) != width(lines[2]) || !strings.Contains(lines[1], "\x1b[32m✓\x1b[0m") || !strings.Contains(lines[2], "\x1b[31m✗\x1b[0m") {
		t.Errorf("rows are off:\n%q\n%q", lines[1], lines[2])
	}
}

func TestHumanPlainOutputHasNoEscapeSequences(t *testing.T) {
	h, buf := plainHuman()
	h.Table([]string{"a"}, [][]string{{h.Mark(markWarning)}})
	h.Block("Title", kv{"Key", h.Mark(markFailed) + " v"})
	h.Conditions("Conditions", []conditionRow{{Mark: markPending, Type: conditionReady, Reason: "R", Message: "m"}})

	if strings.Contains(buf.String(), "\x1b") {
		t.Errorf("escape sequence in plain output: %q", buf.String())
	}
}

func TestHumanBlockAlignsLabelsAndSkipsEmptyValues(t *testing.T) {
	h, buf := plainHuman()
	h.Block("initshow · ichor-system", kv{"Talos", testTalosVersion}, kv{labelKubernetes, humanK8s}, kv{"Unset", ""}, kv{"Endpoint", "10.0.0.1 (node)"})
	h.Block("Empty", kv{"Nothing", ""})
	h.Block("", kv{"cli", testSettingsVersion}, kv{"new installs", "Talos v1.14.5"})

	want := "initshow · ichor-system\n" +
		"  Talos        v1.14.5\n" +
		"  Kubernetes   v1.36.6\n" +
		"  Endpoint     10.0.0.1 (node)\n" +
		"\n" +
		"cli          v0.1.0\n" +
		"new installs Talos v1.14.5\n"
	if buf.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestHumanMarksCarryColourOnlyOnATerminal(t *testing.T) {
	h, _ := ttyHuman()
	for m, want := range map[mark]string{
		markOK: "\x1b[32m✓\x1b[0m", markWarning: "\x1b[33m!\x1b[0m", markFailed: "\x1b[31m✗\x1b[0m", markPending: "\x1b[2m…\x1b[0m",
	} {
		if got := h.Mark(m); got != want {
			t.Errorf("tty Mark(%d) = %q, want %q", m, got, want)
		}
	}

	p, _ := plainHuman()
	for m, want := range map[mark]string{markOK: "✓", markWarning: "!", markFailed: "✗", markPending: "…"} {
		if got := p.Mark(m); got != want {
			t.Errorf("plain Mark(%d) = %q, want %q", m, got, want)
		}
	}
}

func TestNewHumanOnAPipeIsPlain(t *testing.T) {
	var buf bytes.Buffer

	h := newHuman(&buf)
	if h.Mark(markOK) != "✓" {
		t.Errorf("a buffer is not a terminal, got %q", h.Mark(markOK))
	}
}

func TestHumanEnumStripsTheTypePrefix(t *testing.T) {
	for in, want := range map[string]string{
		"MACHINE_ROLE_CONTROL_PLANE":      humanControlPlane,
		"MACHINE_ROLE_WORKER":             "worker",
		"MACHINE_APPROVAL_STATE_APPROVED": "approved",
		"MACHINE_APPROVAL_STATE_PENDING":  "pending",
		"HANDOVER_PHASE_HANDED_OVER":      "handed-over",
		"ROLLOUT_KIND_KUBERNETES":         compKubernetes,
		"MACHINE_ROLE_UNSPECIFIED":        "",
		"NOT_AN_ENUM_AT_ALL":              "not-an-enum-at-all",
		"":                                "",
	} {
		if got := humanEnum(in); got != want {
			t.Errorf("humanEnum(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHumanAgeAndUntil(t *testing.T) {
	fixedHumanClock(t)

	ago := func(d time.Duration) *timestamppb.Timestamp { return timestamppb.New(humanTestNow.Add(-d)) }

	for d, want := range map[time.Duration]string{
		30 * time.Second: "30s", 3 * time.Minute: "3m", 59 * time.Minute: "59m", 2 * time.Hour: "2h", 49 * time.Hour: "2d", 5 * 24 * time.Hour: "5d",
	} {
		if got := humanAge(ago(d)); got != want {
			t.Errorf("humanAge(%v) = %q, want %q", d, got, want)
		}
	}

	if humanAge(nil) != "" || humanUntil(nil) != "" {
		t.Error("an unset time has no age")
	}

	if got := humanUntil(ago(-364 * 24 * time.Hour)); got != "in 364d" {
		t.Errorf("humanUntil = %q", got)
	}

	if got := humanUntil(ago(2 * 24 * time.Hour)); got != "expired 2d ago" {
		t.Errorf("humanUntil past = %q", got)
	}
}

func TestConditionMarkKnowsWhichWayAConditionReads(t *testing.T) {
	for _, tc := range []struct {
		typ, status string
		want        mark
	}{
		{conditionReady, condTrue, markOK},
		{conditionReady, condFalse, markFailed},
		{conditionReady, "Unknown", markPending},
		{"ConfigWarnings", condFalse, markOK},
		{"ConfigWarnings", condTrue, markWarning},
		{"CertificatesExpiring", condTrue, markWarning},
		{"Transitioning", condTrue, markPending},
		{"Transitioning", condFalse, markOK},
		{"CNIHandover", condTrue, markOK},
		{"CNIHandover", condFalse, markPending},
		{"NetworkPoliciesApplied", condFalse, markWarning},
	} {
		if got := conditionMark(tc.typ, tc.status); got != tc.want {
			t.Errorf("conditionMark(%s, %s) = %d, want %d", tc.typ, tc.status, got, tc.want)
		}
	}
}

func TestShortNamesShortenOnlyWhatStaysUnique(t *testing.T) {
	got := shortNames([]string{"fa70962a-4fd9-4d01-88c5-f9966adcda99", "0b1c2d3e-aaaa-bbbb-cccc-111122223333", testMachineName})
	if want := []string{"fa70962a", "0b1c2d3e", "node-a"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}

	got = shortNames([]string{"fa70962a-1111", "fa70962a-2222", "other-name-long"})
	if want := []string{"fa70962a-1111", "fa70962a-2222", "other-na"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("a clash keeps the full names: got %v, want %v", got, want)
	}
}

func TestMachineFormatFallsBackToYAMLCentrally(t *testing.T) {
	if machineFormat(outputHuman) != outputYAML || machineFormat(outputJSON) != outputJSON || machineFormat(outputYAML) != outputYAML {
		t.Error("the unset -o must mean YAML for a command without a human view")
	}

	if err := validateOutputFormat(outputHuman); err != nil {
		t.Errorf("the unset -o is valid: %v", err)
	}

	if err := validateOutputFormat("xml"); err == nil {
		t.Error("xml must be refused")
	}
}
