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
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"k8s.io/apimachinery/pkg/util/validation"

	ichorv1alpha1 "github.com/maeshinshin/ichor/api/v1alpha1"
)

// send feeds msgs to the model one at a time, and feeds back the filter results that the commands it
// returns produce, as the program would. It returns the model and the last command.
func send(t *testing.T, m promptModel, msgs ...tea.Msg) (promptModel, tea.Cmd) {
	t.Helper()

	var (
		last tea.Cmd
		feed func(msg tea.Msg)
		run  func(cmd tea.Cmd)
	)

	run = func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}

		out := make(chan tea.Msg, 1)

		go func() { out <- cmd() }()

		select {
		case msg := <-out:
			switch msg := msg.(type) {
			case tea.BatchMsg:
				for _, c := range msg {
					run(c)
				}
			case list.FilterMatchesMsg:
				feed(msg)
			}
		case <-time.After(50 * time.Millisecond): // a timer, such as a cursor blink: not needed here
		}
	}

	feed = func(msg tea.Msg) {
		next, cmd := m.Update(msg)
		m, last = next.(promptModel), cmd

		run(cmd)
	}

	for _, msg := range msgs {
		if text, ok := msg.(typedText); ok {
			for _, r := range text {
				feed(tea.KeyPressMsg{Code: r, Text: string(r)})
			}

			continue
		}

		feed(msg)
	}

	return m, last
}

const (
	tuiCluster   = "demo"
	tuiLB        = "loadBalancer"
	tuiSata      = "sata"
	tuiNVMe      = "nvme"
	tuiSdb       = "/dev/sdb"
	tuiSerialSel = "serial:S-b"
	tuiWWIDSel   = "wwid:naa.tui"
)

// typedText stands for one key press per character; send expands it.
type typedText string

// shown is what the model draws, without the colour the list gives its matches.
func shown(m promptModel) string { return ansi.Strip(m.View().Content) }

func typedKeys(s string) tea.Msg { return typedText(s) }
func keyOf(code rune) tea.Msg    { return tea.KeyPressMsg{Code: code} }
func ctrl(r rune) tea.Msg        { return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl} }

func typeQuestion(t *testing.T) promptModel {
	t.Helper()

	for _, q := range initQuestions(newInitFlags(t)) {
		if q.flag == flagEndpointType {
			return newPromptModel([]promptQuestion{q}, nil)
		}
	}

	t.Fatal("no endpoint type question")

	return promptModel{}
}

// The prompt offers the types the CRD takes: a hostname is loadBalancer, so there is no dns.
func TestPromptModel_OffersNoDNSEndpointType(t *testing.T) {
	if v := shown(typeQuestion(t)); strings.Contains(v, "dns") {
		t.Fatalf("the endpoint type prompt offers dns:\n%s", v)
	}
}

func TestPromptModel_TypingNarrowsTheEndpointTypes(t *testing.T) {
	m := typeQuestion(t)

	for _, e := range endpointTypes {
		if !strings.Contains(shown(m), e.Name) {
			t.Fatalf("%s is not listed:\n%s", e.Name, shown(m))
		}
	}

	m, _ = send(t, m, typedKeys("load"))

	if v := shown(m); !strings.Contains(v, tuiLB) || strings.Contains(v, "dns") || strings.Contains(v, "vip") {
		t.Errorf("typing did not narrow the list to loadBalancer:\n%s", v)
	}
}

func TestPromptModel_EndpointTypeDefaultsToVip(t *testing.T) {
	m, _ := send(t, typeQuestion(t), keyOf(tea.KeyEnter))
	if m.state != promptDone || m.got[flagEndpointType] != endpointVIP {
		t.Errorf("state %d, got %v", m.state, m.got)
	}
}

func TestPromptModel_EndpointAddressSaysWhatKindFollowsTheType(t *testing.T) {
	flags := newInitFlags(t)

	m := newPromptModel(initQuestions(flags), map[string]string{flagEndpointType: "vip"})
	m, _ = send(t, m, typedKeys(tuiCluster), keyOf(tea.KeyEnter), keyOf(tea.KeyDown), keyOf(tea.KeyEnter)) // loadBalancer

	if v := shown(m); !strings.Contains(v, "a hostname is expected here") {
		t.Errorf("a loadBalancer address is a hostname:\n%s", v)
	}

	m = newPromptModel(initQuestions(flags), map[string]string{flagEndpointType: "vip"})
	m, _ = send(t, m, typedKeys(tuiCluster), keyOf(tea.KeyEnter), keyOf(tea.KeyEnter)) // vip

	if v := shown(m); !strings.Contains(v, "an IP address is expected here") {
		t.Errorf("a vip address is an IP:\n%s", v)
	}
}

func diskQuestion(disks ...ichorv1alpha1.TalosMachineDiscoveredDisk) promptModel {
	return newPromptModel([]promptQuestion{{
		flag: flagInstallDisk, label: "Install disk",
		choices: diskChoices(disks), noDefault: true,
	}}, nil)
}

var testDisks = []ichorv1alpha1.TalosMachineDiscoveredDisk{
	{WWID: "naa.tui", DevPath: testInstallDisk, Model: "FastDisk", SizeBytes: 1 << 30, Transport: tuiSata},
	{Serial: "S-b", DevPath: tuiSdb, Model: "SlowDisk", SizeBytes: 1 << 31, Transport: tuiNVMe},
}

func TestPromptModel_DiskListRefusesEnterWithNothingSelected(t *testing.T) {
	m := diskQuestion(testDisks...)

	m, _ = send(t, m, keyOf(tea.KeyEnter))
	if m.state != promptRunning || len(m.got) != 0 {
		t.Fatalf("Enter on an untouched list picked a disk: state %d, got %v", m.state, m.got)
	}

	if !strings.Contains(shown(m), "choose one first") {
		t.Errorf("the refusal is not shown:\n%s", shown(m))
	}

	// A disk is chosen by moving onto it ...
	picked, _ := send(t, m, keyOf(tea.KeyDown), keyOf(tea.KeyDown), keyOf(tea.KeyEnter))
	if picked.state != promptDone || picked.got[flagInstallDisk] != tuiSerialSel {
		t.Errorf("state %d, got %v", picked.state, picked.got)
	}

	// ... or by typing something that matches only it.
	picked, _ = send(t, m, typedKeys("SlowDisk"), keyOf(tea.KeyEnter), keyOf(tea.KeyEnter))
	if picked.state != promptDone || picked.got[flagInstallDisk] != tuiSerialSel {
		t.Errorf("state %d, got %v", picked.state, picked.got)
	}

	// Emptying the filter again leaves nothing selected.
	back, _ := send(t, m, typedKeys("x"), keyOf(tea.KeyBackspace), keyOf(tea.KeyEnter))
	if back.state != promptRunning {
		t.Errorf("an emptied filter picked a disk: %v", back.got)
	}
}

func TestPromptModel_DiskRowsShowPathModelSizeAndTransport(t *testing.T) {
	v := shown(diskQuestion(testDisks...))
	for _, want := range []string{"/dev/sda  FastDisk", "1.0 GiB", tuiSata, "/dev/sdb  SlowDisk", "2.0 GiB", tuiNVMe} {
		if !strings.Contains(v, want) {
			t.Errorf("%q is not shown:\n%s", want, v)
		}
	}
}

func TestDiskChoices_SelectorWithAndWithoutAWWID(t *testing.T) {
	c := diskChoices(testDisks)
	if c[0].value != tuiWWIDSel || c[1].value != tuiSerialSel {
		t.Errorf("values %q, %q", c[0].value, c[1].value)
	}
}

func TestPromptModel_ClusterNameIsValidatedInline(t *testing.T) {
	m := newPromptModel(initQuestions(newInitFlags(t)), nil)

	m, _ = send(t, m, typedKeys("Not_Valid"), keyOf(tea.KeyEnter))

	v := shown(m)
	if m.state != promptRunning || m.at != 0 || !strings.Contains(v, `"Not_Valid" is not a valid name`) {
		t.Fatalf("a bad name was not refused inline (at %d):\n%s", m.at, v)
	}

	if !strings.Contains(v, "Not_Valid") || m.input.Value() != "Not_Valid" {
		t.Errorf("the typed name was cleared:\n%s", v)
	}

	// Editing the text clears the message; a good name is accepted.
	m, _ = send(t, m, ctrl('u'), typedKeys(tuiCluster))
	if strings.Contains(shown(m), "is not a valid name") {
		t.Errorf("the message outlived the edit:\n%s", shown(m))
	}

	if m, _ = send(t, m, keyOf(tea.KeyEnter)); m.at != 1 || m.got[flagCluster] != tuiCluster {
		t.Errorf("at %d, got %v", m.at, m.got)
	}
}

func TestPromptModel_EscAndCtrlCCancelAndLeaveNothingDrawn(t *testing.T) {
	for name, k := range map[string]tea.Msg{"esc": keyOf(tea.KeyEscape), "ctrl+c": ctrl('c')} {
		for what, m := range map[string]promptModel{
			"text": newPromptModel(initQuestions(newInitFlags(t)), nil),
			"list": diskQuestion(testDisks...),
		} {
			m, cmd := send(t, m, typedKeys("x"), k)
			if m.state != promptCancelled || shown(m) != "" {
				t.Errorf("%s on a %s question: state %d, view %q", name, what, m.state, shown(m))
			}

			if _, quit := cmd().(tea.QuitMsg); !quit {
				t.Errorf("%s on a %s question did not end the program", name, what)
			}
		}
	}
}

func TestAskPrompts_NeverStartsWithoutATerminal(t *testing.T) {
	setTerminal(t, false)

	var out bytes.Buffer

	_, err := askPrompts(initQuestions(newInitFlags(t)), nil, errReader{t}, &out)
	if err == nil || out.Len() != 0 {
		t.Errorf("err %v, output %q", err, out.String())
	}
}

type errReader struct{ t *testing.T }

func (r errReader) Read([]byte) (int, error) {
	r.t.Error("stdin was read without a terminal")

	return 0, errors.New("unreadable")
}

func TestAskPrompts_CtrlCOnTheTerminalCancels(t *testing.T) {
	setTerminal(t, true)

	_, err := askPrompts(initQuestions(newInitFlags(t)), nil, typed(t, "\x03"), &bytes.Buffer{})
	if !errors.Is(err, errPromptCancelled) {
		t.Errorf("err = %v", err)
	}
}

func TestPromptModel_EnterTakesTheOnlyRemainingCandidate(t *testing.T) {
	m, _ := send(t, typeQuestion(t), typedKeys("load"), keyOf(tea.KeyEnter))
	if m.state != promptDone || m.got[flagEndpointType] != tuiLB {
		t.Errorf("state %d, got %v", m.state, m.got)
	}

	// The same for a disk, which is wiped: one match typed for is a choice made.
	m, _ = send(t, diskQuestion(testDisks...), typedKeys("SlowDisk"), keyOf(tea.KeyEnter))
	if m.state != promptDone || m.got[flagInstallDisk] != tuiSerialSel {
		t.Errorf("state %d, got %v", m.state, m.got)
	}
}

func TestPromptModel_EnterNeedsTwoPressesWhileSeveralRemain(t *testing.T) {
	m, _ := send(t, typeQuestion(t), typedKeys("d")) // loadBalancer and node have a d

	if n := len(m.pick.VisibleItems()); n < 2 {
		t.Fatalf("%d candidates match", n)
	}

	// The first Enter fixes the filter, the second takes the highlighted row.
	if m, _ = send(t, m, keyOf(tea.KeyEnter)); m.state != promptRunning {
		t.Fatal("the first Enter took an answer")
	}

	if m, _ = send(t, m, keyOf(tea.KeyEnter)); m.state != promptDone || m.got[flagEndpointType] == "" {
		t.Errorf("state %d, got %v", m.state, m.got)
	}
}

func TestPromptModel_ValidatesWhileTyping(t *testing.T) {
	m := newPromptModel(initQuestions(newInitFlags(t)), nil)

	if strings.Contains(shown(m), "is not a valid name") {
		t.Fatalf("a message before anything was typed:\n%s", shown(m))
	}

	m, _ = send(t, m, typedKeys("N"))
	if !strings.Contains(shown(m), `"N" is not a valid name`) || m.input.Value() != "N" {
		t.Errorf("an invalid value was not flagged as typed, or was not kept:\n%s", shown(m))
	}

	m, _ = send(t, m, ctrl('u'), typedKeys(tuiCluster))
	if strings.Contains(shown(m), "is not a valid name") || m.input.Value() != tuiCluster {
		t.Errorf("the message outlived the fix, or the value was lost:\n%s", shown(m))
	}

	// Emptying the field is flagged too, and nothing is cleared for the operator.
	m, _ = send(t, m, keyOf(tea.KeyBackspace), keyOf(tea.KeyBackspace), keyOf(tea.KeyBackspace), keyOf(tea.KeyBackspace))
	if !strings.Contains(shown(m), "is not a valid name") {
		t.Errorf("an emptied field was not flagged:\n%s", shown(m))
	}
}

func TestPromptModel_ValidationReasonWrapsToTheWidth(t *testing.T) {
	m := newPromptModel(initQuestions(newInitFlags(t)), nil)
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 40, Height: 24}, typedKeys("Not_Valid"))

	want := `"Not_Valid" is not a valid name: ` + strings.Join(validation.IsDNS1123Subdomain("Not_Valid"), "; ")

	lines := strings.Split(shown(m), "\n")
	for _, l := range lines {
		if w := ansi.StringWidth(l); w > 40 {
			t.Errorf("a line is %d cells wide, past the 40 of the terminal: %q", w, l)
		}
	}

	if got := strings.Join(strings.Fields(strings.Join(lines, " ")), ""); !strings.Contains(got, strings.Join(strings.Fields(want), "")) {
		t.Errorf("the reason is cut:\n%s", shown(m))
	}
}

func TestPromptModel_IsAFormWithAnsweredQuestionsAbove(t *testing.T) {
	m := newPromptModel(initQuestions(newInitFlags(t)), map[string]string{flagEndpointType: endpointVIP})
	m, _ = send(t, m, typedKeys(tuiCluster), keyOf(tea.KeyEnter), keyOf(tea.KeyEnter), typedKeys("10.0.0.1"))

	lines := strings.Split(shown(m), "\n")
	if len(lines) < 3 {
		t.Fatalf("the form is too short:\n%s", shown(m))
	}

	// Answered questions are one line each, indented to the gutter; only the current one has the marker.
	for i, want := range []string{"  Cluster name", "  Endpoint type", "› Endpoint address"} {
		if !strings.HasPrefix(lines[i], want) {
			t.Errorf("line %d = %q, want it to begin %q", i, lines[i], want)
		}
	}

	if strings.Count(shown(m), "›") != 1 {
		t.Errorf("more than one marker:\n%s", shown(m))
	}

	// The values start in one column, set by the longest label.
	col := gutter + len("Endpoint address") + labelGap
	for i, want := range []string{tuiCluster, endpointVIP, "10.0.0.1"} {
		if r := []rune(lines[i]); len(r) < col || !strings.HasPrefix(string(r[col:]), want) {
			t.Errorf("line %d = %q: the value is not at column %d", i, lines[i], col)
		}
	}

	if strings.Contains(shown(m), "Cluster name:") || strings.Contains(shown(m), "Cluster name >") {
		t.Errorf("a label carries a colon or a prompt:\n%s", shown(m))
	}
}

func TestPromptModel_TheHintNamesTheKeysThatWork(t *testing.T) {
	text := shown(newPromptModel(initQuestions(newInitFlags(t)), nil))
	if !strings.Contains(text, "enter") || !strings.Contains(text, "esc") || strings.Contains(text, "filter") {
		t.Errorf("a text field's hint:\n%s", text)
	}

	for _, want := range []string{"type to filter", "↑↓", "enter", "esc"} {
		if !strings.Contains(shown(typeQuestion(t)), want) {
			t.Errorf("a list's hint lacks %q:\n%s", want, shown(typeQuestion(t)))
		}
	}
}

func TestDiskChoices_ColumnsLineUpAndEmptyOnesAreLeftOut(t *testing.T) {
	disks := []ichorv1alpha1.TalosMachineDiscoveredDisk{
		{DevPath: classADisk, SizeBytes: 100 << 30, Transport: "virtio"},
		{DevPath: "/dev/nvme0n1", SizeBytes: 2000 << 30, Transport: tuiNVMe},
	}

	c := diskChoices(disks)

	// No model anywhere: no model column, so no gap where it would be.
	if want := "/dev/vda       100.0 GiB  virtio"; c[0].label != want {
		t.Errorf("row 0 = %q, want %q", c[0].label, want)
	}

	if want := "/dev/nvme0n1  2000.0 GiB  nvme"; c[1].label != want {
		t.Errorf("row 1 = %q, want %q", c[1].label, want)
	}

	// Each column starts at the same offset in every row.
	if strings.Index(c[0].label, "  virtio") != strings.Index(c[1].label, "  nvme") {
		t.Errorf("the transport column does not line up:\n%s\n%s", c[0].label, c[1].label)
	}

	// A model on one disk brings the column back for all.
	disks[0].Model = "QEMU"
	if c = diskChoices(disks); !strings.Contains(c[0].label, "QEMU") || strings.Index(c[0].label, "GiB") != strings.Index(c[1].label, "GiB") {
		t.Errorf("rows:\n%s\n%s", c[0].label, c[1].label)
	}
}
