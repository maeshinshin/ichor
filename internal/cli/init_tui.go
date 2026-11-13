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
	"maps"
	"os"
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// errPromptCancelled is what Esc, Ctrl-C and a closed stdin end the prompts with. Nothing has been
// written by then: init asks before it creates anything.
var errPromptCancelled = errors.New("cancelled; nothing was written")

// promptQuestion is one thing init asks: a line of text checked by check, or, when choices is set, a
// pick from a list the typed text filters. The answer is stored under flag.
type promptQuestion struct {
	flag    string
	title   func(got map[string]string) string // the answers so far, for a title that depends on them
	check   func(answer string) error
	choices []promptChoice
	// noDefault starts the list on a row that cannot be confirmed, so Enter alone never picks a choice.
	noDefault bool
}

// promptChoice is one row of a list; an empty value marks the row that is not a choice.
type promptChoice struct{ label, value string }

func (c promptChoice) Title() string       { return c.label }
func (c promptChoice) Description() string { return "" }
func (c promptChoice) FilterValue() string {
	if c.value == "" {
		return "" // never matches, so a filter drops it
	}

	return c.label
}

const (
	promptRunning = iota
	promptDone
	promptCancelled
)

// promptModel asks its questions one after another.
type promptModel struct {
	qs    []promptQuestion
	at    int
	got   map[string]string
	input textinput.Model
	pick  list.Model
	why   string // why the last Enter was refused
	state int
}

type promptEOF struct{}

var (
	slash = tea.KeyPressMsg{Code: '/', Text: "/"} // the list's key for starting a filter
	enter = tea.KeyPressMsg{Code: tea.KeyEnter}
)

func newPromptModel(qs []promptQuestion, seed map[string]string) promptModel {
	m := promptModel{qs: qs, got: map[string]string{}}
	maps.Copy(m.got, seed)

	m.open()

	return m
}

// open sets up the widget for the current question.
func (m *promptModel) open() {
	q := m.qs[m.at]
	m.why = ""

	if q.choices == nil {
		m.input = textinput.New()
		m.input.Focus()

		return
	}

	items := make([]list.Item, 0, len(q.choices)+1)
	if q.noDefault {
		items = append(items, promptChoice{label: "(nothing selected: type to filter or use the arrow keys)"})
	}

	for _, c := range q.choices {
		items = append(items, c)
	}

	d := list.NewDefaultDelegate()
	d.ShowDescription = false
	d.SetSpacing(0)

	m.pick = list.New(items, d, 80, min(len(items), 8)+4)
	m.pick.SetShowTitle(false)
	m.pick.SetShowStatusBar(false)
	m.pick.SetShowHelp(false)
	m.pick.DisableQuitKeybindings()
}

func (m promptModel) Init() tea.Cmd { return nil }

func (m promptModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.state != promptRunning {
		return m, nil // a message that was already on its way when the program was told to quit
	}

	switch msg := msg.(type) {
	case promptEOF:
		m.state = promptCancelled

		return m, tea.Quit
	case tea.WindowSizeMsg:
		if m.qs[m.at].choices != nil {
			m.pick.SetWidth(msg.Width)
		}

		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc", "ctrl+c":
			m.state = promptCancelled

			return m, tea.Quit
		case "enter":
			if m.qs[m.at].choices != nil && m.pick.SettingFilter() {
				break // the first Enter fixes the filter, so the highlighted row is the one a second Enter takes
			}

			return m.confirm()
		case "up", "down":
			if m.qs[m.at].choices != nil {
				m.pick, _ = m.pick.Update(enter) // fix a filter being typed, whose rows show no highlight
				if msg.String() == "up" {
					m.pick.CursorUp()
				} else {
					m.pick.CursorDown()
				}

				return m, nil
			}
		}

		m.why = ""
	}

	var cmd tea.Cmd
	if m.qs[m.at].choices != nil {
		// Typing filters at once, as if "/" had been pressed first.
		if k, ok := msg.(tea.KeyPressMsg); ok && k.Text != "" && !m.pick.SettingFilter() {
			m.pick, _ = m.pick.Update(slash)
		}

		m.pick, cmd = m.pick.Update(msg)
		if m.pick.SettingFilter() && m.pick.FilterValue() == "" {
			m.pick.ResetFilter() // an emptied filter shows no highlight either
		}
	} else {
		m.input, cmd = m.input.Update(msg)
	}

	return m, cmd
}

// confirm takes the answer on screen, or says why it cannot, and moves on.
func (m promptModel) confirm() (tea.Model, tea.Cmd) {
	q := m.qs[m.at]

	var answer string

	if q.choices == nil {
		answer = strings.TrimSpace(m.input.Value())
		if err := q.check(answer); err != nil {
			m.why = err.Error()

			return m, nil
		}
	} else {
		c, ok := m.pick.SelectedItem().(promptChoice)

		switch {
		case !ok:
			m.why = "nothing matches"

			return m, nil
		case c.value == "":
			m.why = "choose one first"

			return m, nil
		}

		answer = c.value
	}

	m.got[q.flag] = answer

	if m.at++; m.at == len(m.qs) {
		m.state = promptDone

		return m, tea.Quit
	}

	m.open()

	return m, nil
}

func (m promptModel) View() tea.View {
	if m.state == promptCancelled {
		return tea.NewView("") // leave nothing of the prompt on screen
	}

	var b strings.Builder

	for _, q := range m.qs[:m.at] {
		fmt.Fprintf(&b, "%s: %s\n", q.title(m.got), m.got[q.flag])
	}

	if m.state == promptDone {
		return tea.NewView(b.String())
	}

	q := m.qs[m.at]
	b.WriteString(q.title(m.got))

	if q.choices == nil {
		b.WriteString(": " + m.input.View() + "\n")
	} else {
		b.WriteString("\n" + m.pick.View() + "\n")
	}

	if m.why != "" {
		b.WriteString(m.why + "\n")
	}

	b.WriteString("enter confirms, esc cancels\n")

	return tea.NewView(b.String())
}

// eofCancel turns the end of a non-terminal input into a cancel; a terminal in raw mode sends Ctrl-D
// as a key, never as the end of input, so only a reader that is not a file needs it.
type eofCancel struct {
	io.Reader
	p *tea.Program
}

func (r *eofCancel) Read(b []byte) (int, error) {
	n, err := r.Reader.Read(b)
	if errors.Is(err, io.EOF) {
		r.p.Send(promptEOF{})
	}

	return n, err
}

// askPrompts runs the questions as a terminal program drawn on out and read from in, and returns
// every answer by flag. It is the only place a program starts, and it refuses unless stdin is a
// terminal: a program started without one would wait for keys that never come.
func askPrompts(qs []promptQuestion, seed map[string]string, in io.Reader, out io.Writer) (map[string]string, error) {
	if !bundleTerminal() {
		return nil, errors.New("asking needs a terminal on standard input")
	}

	rd, eof := in, (*eofCancel)(nil)
	if _, isFile := in.(*os.File); !isFile {
		eof = &eofCancel{Reader: in}
		rd = eof
	}

	p := tea.NewProgram(newPromptModel(qs, seed), tea.WithInput(rd), tea.WithOutput(out))
	if eof != nil {
		eof.p = p
	}

	final, err := p.Run()
	if err != nil {
		if errors.Is(err, tea.ErrInterrupted) {
			return nil, errPromptCancelled
		}

		return nil, fmt.Errorf("asking: %w", err)
	}

	m, _ := final.(promptModel)
	if m.state != promptDone {
		return nil, errPromptCancelled
	}

	return m.got, nil
}
