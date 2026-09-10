// kbselectform.go (D190) — the interactive KB selection shown between the
// connect form and the first write, when the server mounts more than one KB
// and the operator did not name any with --kb.
//
// It is a second step rather than a field of connectform.go because the KB
// names are not known until the server has been probed, and the probe happens
// after that form. Making it a step keeps the choice where the information is,
// instead of asking the form to render a list it cannot have yet.
package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type kbSelectModel struct {
	kbs      []string
	selected []bool
	cursor   int
	done     bool
	cancel   bool
	errMsg   string
}

func newKBSelectModel(kbs []string) kbSelectModel {
	// Nothing is pre-selected: the whole point is that the operator states a
	// choice, and a pre-ticked list is how "all of them" becomes the answer
	// again by default.
	return kbSelectModel{kbs: kbs, selected: make([]bool, len(kbs))}
}

func (m kbSelectModel) Init() tea.Cmd { return nil }

func (m kbSelectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "ctrl+c", "esc":
		m.cancel = true
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.kbs)-1 {
			m.cursor++
		}
	case " ", "x":
		m.selected[m.cursor] = !m.selected[m.cursor]
		m.errMsg = ""
	case "a":
		// Selecting every KB is a legitimate answer — it is the difference
		// between an explicit binding to these names and the old implicit
		// "whatever is known", which silently widened as the server grew.
		for i := range m.selected {
			m.selected[i] = true
		}
		m.errMsg = ""
	case "enter":
		if len(m.chosen()) == 0 {
			m.errMsg = "select at least one KB (space to toggle, a for all, esc to cancel)"
			return m, nil
		}
		m.done = true
		return m, tea.Quit
	}
	return m, nil
}

func (m kbSelectModel) chosen() []string {
	var out []string
	for i, ok := range m.selected {
		if ok {
			out = append(out, m.kbs[i])
		}
	}
	return out
}

func (m kbSelectModel) View() string {
	var b strings.Builder
	b.WriteString("Which Knowledge Bases should this client receive?\n")
	b.WriteString("Only the ones you pick are delivered — skills, subagents, hooks and instructions included.\n\n")
	for i, kb := range m.kbs {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		mark := " "
		if m.selected[i] {
			mark = "x"
		}
		fmt.Fprintf(&b, "%s[%s] %s\n", cursor, mark, kb)
	}
	if m.errMsg != "" {
		fmt.Fprintf(&b, "\n%s\n", m.errMsg)
	}
	b.WriteString("\n↑/↓ move · space toggle · a all · enter confirm · esc cancel\n")
	return b.String()
}

// runKBSelectForm asks which of kbs this client may receive. ok is false when
// the operator cancelled, which the caller treats as "do not connect": writing
// a projection nobody chose is the thing this step exists to prevent.
func runKBSelectForm(kbs []string) (selection []string, ok bool, err error) {
	result, err := tea.NewProgram(newKBSelectModel(kbs)).Run()
	if err != nil {
		return nil, false, err
	}
	m, _ := result.(kbSelectModel)
	if m.cancel || !m.done {
		return nil, false, nil
	}
	return m.chosen(), true, nil
}
