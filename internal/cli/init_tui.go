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
	"cmp"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// errPromptCancelled is what Esc, Ctrl-C and a closed stdin end the prompts with. Nothing has been
// written by then: init asks before it creates anything.
var errPromptCancelled = errors.New("cancelled; nothing was written")

// promptQuestion is one line of the form init asks for: a text field checked by check, or, when
// choices is set, a pick from a list the typed text filters. The answer is stored under flag.
type promptQuestion struct {
	flag  string
	label string                             // a plain noun
	help  func(got map[string]string) string // shown under the field; may depend on the answers so far
	check func(answer string) error
	// def, when set, is what a text field starts with: Enter takes it as it is.
	def string
	// choices, when set, are the rows to pick from.
	choices []promptChoice
	// noDefault starts the list on a row that cannot be confirmed, so Enter alone never picks a choice.
	noDefault bool
}

// promptChoice is one row of a list; an empty value marks the row that is not a choice. desc, when set,
// is a second line under the row: the row's details, or for a row that is not a choice, why it is not.
type promptChoice struct{ label, value, desc string }

func (c promptChoice) Title() string       { return c.label }
func (c promptChoice) Description() string { return c.desc }
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

const (
	marker      = "›" // one cell wide in every terminal, unlike the arrows and the dot in the hints
	gutter      = 2   // the marker and a space; every other line is indented the same
	labelGap    = 3
	defaultCols = 80
)

// The only colours: a dim label and hint, one accent for the markers, one warning for the reason.
// Faint is an attribute, so it survives NO_COLOR; the program drops the colours itself.
var (
	dimStyle    = lipgloss.NewStyle().Faint(true)
	accentStyle = lipgloss.NewStyle().Foreground(lipgloss.Cyan)
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Yellow)
)

// promptModel asks its questions one after another, as lines of a form.
type promptModel struct {
	qs     []promptQuestion
	at     int
	got    map[string]string
	input  textinput.Model
	pick   list.Model
	why    string // why the value is not acceptable
	width  int
	labelW int // the longest label, so the values form a column
	state  int
}

type promptEOF struct{}

var (
	slash = tea.KeyPressMsg{Code: '/', Text: "/"} // the list's key for starting a filter
	enter = tea.KeyPressMsg{Code: tea.KeyEnter}
)

func newPromptModel(qs []promptQuestion, seed map[string]string) promptModel {
	m := promptModel{qs: qs, got: map[string]string{}, width: defaultCols}
	maps.Copy(m.got, seed)

	for _, q := range qs {
		m.labelW = max(m.labelW, utf8.RuneCountInString(q.label))
	}

	m.open()

	return m
}

// open sets up the widget for the current question.
func (m *promptModel) open() {
	q := m.qs[m.at]
	m.why = ""

	if q.choices == nil {
		m.input = textinput.New()
		m.input.Prompt = ""
		m.input.Focus()
		m.input.SetValue(q.def)
		m.input.CursorEnd()

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
	d.ShowDescription = slices.ContainsFunc(q.choices, func(c promptChoice) bool { return c.desc != "" })
	d.SetSpacing(0)
	// Rows sit under the value column's start; the selected one takes the marker, since a colour is not
	// seen everywhere and this is the row Enter takes.
	d.Styles.NormalTitle = lipgloss.NewStyle().PaddingLeft(2) //nolint:mnd
	d.Styles.DimmedTitle = d.Styles.NormalTitle
	d.Styles.SelectedTitle = lipgloss.NewStyle().
		Border(lipgloss.Border{Left: marker}, false, false, false, true).
		BorderForeground(lipgloss.Cyan).
		PaddingLeft(1)

	// A page is eight rows (of two lines when the rows have details); the two lines more are for the page dots when there are more.
	m.pick = list.New(items, d, m.width-gutter, min(len(items), 8)*d.Height()+2)
	m.pick.SetShowTitle(false)
	m.pick.SetShowFilter(false) // the filter text is shown as the question's value
	m.pick.SetShowStatusBar(false)
	m.pick.SetShowHelp(false)
	m.pick.DisableQuitKeybindings()
	m.pick.FilterInput.Prompt = ""
}

func (m promptModel) Init() tea.Cmd { return nil }

func (m promptModel) isList() bool { return m.qs[m.at].choices != nil }

func (m promptModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.state != promptRunning {
		return m, nil // a message that was already on its way when the program was told to quit
	}

	switch msg := msg.(type) {
	case promptEOF:
		m.state = promptCancelled

		return m, tea.Quit
	case tea.WindowSizeMsg:
		m.width = msg.Width
		if m.isList() {
			m.pick.SetWidth(msg.Width - gutter)
		}

		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc", "ctrl+c":
			m.state = promptCancelled

			return m, tea.Quit
		case "enter":
			if m.isList() && m.pick.SettingFilter() {
				if len(m.pick.VisibleItems()) != 1 {
					break // the first Enter fixes the filter, so the highlighted row is the one a second Enter takes
				}

				// One candidate is left: taking it is what the operator is after.
				m.pick, _ = m.pick.Update(enter)
				m.pick.GoToStart()
			}

			return m.confirm()
		case "up", "down":
			if m.isList() {
				m.pick, _ = m.pick.Update(enter) // fix a filter being typed, whose rows show no highlight
				if msg.String() == "up" {
					m.pick.CursorUp()
				} else {
					m.pick.CursorDown()
				}

				m.why = ""

				return m, nil
			}
		}
	}

	var cmd tea.Cmd

	if m.isList() {
		if _, ok := msg.(tea.KeyPressMsg); ok {
			m.why = ""
		}

		// Typing filters at once, as if "/" had been pressed first.
		if k, ok := msg.(tea.KeyPressMsg); ok && k.Text != "" && !m.pick.SettingFilter() {
			m.pick, _ = m.pick.Update(slash)
		}

		m.pick, cmd = m.pick.Update(msg)
		if m.pick.SettingFilter() && m.pick.FilterValue() == "" {
			m.pick.ResetFilter() // an emptied filter shows no highlight either
		}

		return m, cmd
	}

	before := m.input.Value()
	m.input, cmd = m.input.Update(msg)

	// The value is checked as it is typed, and kept whatever the verdict.
	if v := m.input.Value(); v != before {
		m.why = ""
		if err := m.qs[m.at].check(strings.TrimSpace(v)); err != nil {
			m.why = err.Error()
		}
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
			m.why = cmp.Or(c.desc, "choose one first")

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

// line is one line of the form: the gutter (the marker on the current question), the label padded to
// the column, and the value.
func (m promptModel) line(mark, label, value string) string {
	pad := strings.Repeat(" ", m.labelW-utf8.RuneCountInString(label)+labelGap)

	return mark + " " + dimStyle.Render(label) + pad + value
}

// under indents text to the gutter and styles every line of it, wrapped to the terminal's width.
func (m promptModel) under(style lipgloss.Style, text string) string {
	var b strings.Builder

	for l := range strings.SplitSeq(ansi.Wrap(text, max(m.width-gutter, 20), ""), "\n") { //nolint:mnd
		b.WriteString(strings.Repeat(" ", gutter) + style.Render(l) + "\n")
	}

	return b.String()
}

func (m promptModel) View() tea.View {
	if m.state == promptCancelled {
		return tea.NewView("") // leave nothing of the prompt on screen
	}

	var b strings.Builder

	for _, q := range m.qs[:m.at] {
		b.WriteString(m.line(" ", q.label, m.got[q.flag]) + "\n")
	}

	if m.state == promptDone {
		return tea.NewView(b.String())
	}

	q := m.qs[m.at]

	if !m.isList() {
		b.WriteString(m.line(accentStyle.Render(marker), q.label, m.input.View()) + "\n")
	} else {
		typed := m.pick.FilterValue()
		if m.pick.SettingFilter() {
			typed = m.pick.FilterInput.View()
		}

		b.WriteString(m.line(accentStyle.Render(marker), q.label, typed) + "\n")

		for l := range strings.SplitSeq(strings.TrimRight(m.pick.View(), " \n"), "\n") {
			b.WriteString(strings.Repeat(" ", gutter) + l + "\n")
		}
	}

	if q.help != nil {
		b.WriteString(m.under(dimStyle, q.help(m.got)))
	}

	if m.why != "" {
		b.WriteString(m.under(warnStyle, m.why))
	}

	hint := "enter confirm · esc cancel"
	if m.isList() {
		hint = "type to filter · ↑↓ move · enter select · esc cancel"
	}

	b.WriteString("\n" + m.under(dimStyle, hint))

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
