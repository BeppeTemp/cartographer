// pathsform.go (D262) — the interactive placeholder step `connect` shows after
// the first materialization, when some {{repo:…}}/{{path:…}} key the bound
// KBs cite could not be resolved on this machine.
//
// It is a separate step, modelled on kbselectform.go (D190), for the same
// reason: the keys are not known until the server has been pulled, which
// happens after every other question has been answered.

package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type pathsFormModel struct {
	rows   []placeholderRow
	inputs []textinput.Model
	cursor int
	done   bool
}

func newPathsFormModel(rows []placeholderRow) pathsFormModel {
	inputs := make([]textinput.Model, len(rows))
	for i := range rows {
		in := textinput.New()
		in.Placeholder = "local path, empty to skip"
		in.CharLimit = 4096
		in.Width = 60
		inputs[i] = in
	}
	m := pathsFormModel{rows: rows, inputs: inputs}
	if len(inputs) > 0 {
		m.inputs[0].Focus()
	}
	return m
}

func (m pathsFormModel) Init() tea.Cmd { return textinput.Blink }

func (m pathsFormModel) focus(i int) pathsFormModel {
	m.inputs[m.cursor].Blur()
	m.cursor = i
	m.inputs[m.cursor].Focus()
	return m
}

func (m pathsFormModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "ctrl+c", "esc":
			// Skipping is always allowed: an unresolved key never blocks a
			// connect (D75 WP3), it is only reported.
			for i := range m.inputs {
				m.inputs[i].SetValue("")
			}
			m.done = true
			return m, tea.Quit
		case "up", "shift+tab":
			if m.cursor > 0 {
				return m.focus(m.cursor - 1), nil
			}
			return m, nil
		case "down", "tab":
			if m.cursor < len(m.inputs)-1 {
				return m.focus(m.cursor + 1), nil
			}
			return m, nil
		case "enter":
			if m.cursor < len(m.inputs)-1 {
				return m.focus(m.cursor + 1), nil
			}
			m.done = true
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	if len(m.inputs) > 0 {
		m.inputs[m.cursor], cmd = m.inputs[m.cursor].Update(msg)
	}
	return m, cmd
}

// answers returns the non-empty paths entered, by "kind:key".
func (m pathsFormModel) answers() map[string]string {
	out := map[string]string{}
	for i, r := range m.rows {
		if v := strings.TrimSpace(m.inputs[i].Value()); v != "" {
			out[r.Key] = v
		}
	}
	return out
}

func (m pathsFormModel) View() string {
	var b strings.Builder
	b.WriteString("Some placeholders the KBs cite do not resolve on this machine.\n")
	b.WriteString("Where do they live? Agents read the answers from the instructions block instead of guessing.\n\n")
	for i, r := range m.rows {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		kbs := strings.Join(r.KBs, ", ")
		if kbs == "" {
			kbs = "-"
		}
		fmt.Fprintf(&b, "%s{{%s}}  [%s]\n    %s\n    %s\n\n", cursor, r.Key, kbs, r.Reason, m.inputs[i].View())
	}
	b.WriteString("tab/↓ next · shift+tab/↑ previous · enter next/confirm · empty skips · esc skip all\n")
	return b.String()
}

// runPathsForm asks for a local path for each unresolved row and returns the
// answers by "kind:key". A var so tests can stand in for the terminal.
var runPathsForm = func(rows []placeholderRow) (map[string]string, error) {
	result, err := tea.NewProgram(newPathsFormModel(rows)).Run()
	if err != nil {
		return nil, err
	}
	m, _ := result.(pathsFormModel)
	return m.answers(), nil
}
