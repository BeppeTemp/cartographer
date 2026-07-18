// connectform.go implements the connect form as a standalone bubbletea
// component: server URL, server name, token env var, auth toggle, and trust
// toggle. It only
// collects and validates input — it never performs the connect itself (see
// doConnect in connect.go) — so the exact same component is reused two ways:
//
//   - embedded in the TUI dashboard (tui.go), which layers its own async
//     connectCmd + spinner on top of the collected values once the form
//     reports Submitted();
//   - run standalone via runConnectForm, used by `cmdConnect` (connect.go)
//     when invoked interactively (TTY, no flags passed).
package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// connectField identifies the focused field of the connect form.
type connectField int

const (
	fieldServerURL connectField = iota
	fieldTokenEnv
	fieldAuth
	fieldTrust
	fieldSubmit
	fieldCount
)

// connectFormModel is the bubbletea model for the connect form. It carries no
// knowledge of providers, KB dirs, or network calls — those stay in
// connectOptions.Providers/Dir, filled in by the caller around the values this
// form collects.
type connectFormModel struct {
	title string
	focus connectField

	url      textinput.Model
	tokenEnv textinput.Model
	auth     bool
	// trust is the "trust KB artifacts" toggle (see connectOptions.Trust):
	// prefilled from .cartographer.yaml when it exists, defaults to true
	// otherwise (see clientconfig.Default). Persisted at connect time by
	// doConnect — see docs/sync.md §Sicurezza.
	trust bool

	// serverName is carried through from the prefill, not collected by the
	// form: the server is always registered as "cartographer" (the project
	// name) — the key exists in .cartographer.yaml only as an escape hatch.
	serverName string

	submitted bool
	cancelled bool

	// errMsg is a probe/connect error to render inline, in red, above the
	// Submit line (D64): set by the embedding caller (tui.go/connect.go) via
	// withError when redisplaying the form after a failed attempt, so the
	// values the user just entered are never lost. Cleared automatically the
	// next time a field actually changes (see Update).
	errMsg string
	// forceRetry is true when errMsg came from a failed reachability probe
	// (not a hard connect error) and no field has changed since: submitting
	// again with forceRetry still true skips the probe and proceeds straight
	// to the real connect — the "press Connect again to force" escape hatch
	// (D64). Any edit to a field clears it, since the config is no longer the
	// one that was just probed.
	forceRetry bool

	// standalone is true when the model drives its own tea.Program
	// (runConnectForm): submit/cancel must then issue tea.Quit themselves.
	// When embedded in the TUI dashboard, standalone is false — the TUI
	// program keeps running and reacts to Submitted()/Cancelled() itself, so
	// the model must never quit the whole program on their behalf (only
	// ctrl+c does, matching the dashboard's existing behavior).
	standalone bool

	// Submitting and SpinnerView let an embedding parent (the TUI dashboard)
	// overlay its own "connecting…" state on top of the Submit line, after
	// intercepting Submitted() and kicking off its own async connectCmd. The
	// standalone form never sets these — it returns before any network call
	// happens.
	Submitting  bool
	SpinnerView string
	// SubmittingLabel overrides the default "connecting…" text shown next to
	// SpinnerView while Submitting is true — e.g. "probing the server…" during
	// the pre-connect probe (D64). Empty means "connecting…".
	SubmittingLabel string
}

// newConnectFormModel builds a connectFormModel titled title (e.g. "Connect
// claude"), prefilled from prefill's ServerURL/Name/TokenEnv/Auth, with focus
// on the first field. standalone controls whether submit/cancel quit their
// own tea.Program (see the standalone field doc).
func newConnectFormModel(title string, prefill connectOptions, standalone bool) connectFormModel {
	url := textinput.New()
	url.Placeholder = "http://localhost:8080/mcp"
	url.SetValue(prefill.ServerURL)

	tokenEnv := textinput.New()
	tokenEnv.Placeholder = "CARTOGRAPHER_TOKENS"
	tokenEnv.SetValue(prefill.TokenEnv)

	m := connectFormModel{
		title:      title,
		url:        url,
		serverName: prefill.Name,
		tokenEnv:   tokenEnv,
		auth:       prefill.Auth,
		trust:      prefill.Trust,
		standalone: standalone,
	}
	m.setFormFocus(fieldServerURL)
	return m
}

// setFormFocus moves focus to f, (un)focusing the underlying textinput.Models.
func (m *connectFormModel) setFormFocus(f connectField) {
	m.focus = f
	m.url.Blur()
	m.tokenEnv.Blur()
	switch f {
	case fieldServerURL:
		m.url.Focus()
	case fieldTokenEnv:
		m.tokenEnv.Focus()
	}
}

// withError returns m ready to be redisplayed after a failed probe/connect
// attempt: submitted is reset to false (so Enter on Submit fires the attempt
// again), every value the user already entered is left untouched, and errMsg
// is shown inline above the Submit line. forceRetry marks errMsg as coming
// from a failed reachability probe: see the forceRetry field doc.
func (m connectFormModel) withError(errMsg string, forceRetry bool) connectFormModel {
	m.submitted = false
	m.errMsg = errMsg
	m.forceRetry = forceRetry
	return m
}

// Submitted reports whether the user confirmed the form (Enter on Submit).
func (m connectFormModel) Submitted() bool { return m.submitted }

// Cancelled reports whether the user aborted the form (Esc).
func (m connectFormModel) Cancelled() bool { return m.cancelled }

// Values returns the collected connectOptions, with the same blank-field
// defaults `cartographer connect` uses today. Providers/Dir/DryRun/AutoTrust
// are left zero-valued — the caller (cmdConnect or the TUI) fills those in
// from context the form never had.
func (m connectFormModel) Values() connectOptions {
	url := m.url.Value()
	if url == "" {
		url = "http://localhost:8080/mcp"
	}
	name := m.serverName
	if name == "" {
		name = "cartographer"
	}
	tokenEnv := m.tokenEnv.Value()
	if tokenEnv == "" {
		tokenEnv = "CARTOGRAPHER_TOKENS"
	}
	return connectOptions{ServerURL: url, Name: name, Auth: m.auth, TokenEnv: tokenEnv, Trust: m.trust}
}

// --- tea.Model ---

func (m connectFormModel) Init() tea.Cmd { return nil }

func (m connectFormModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch keyMsg.String() {
	case "ctrl+c":
		return m, tea.Quit

	case "esc":
		m.cancelled = true
		if m.standalone {
			return m, tea.Quit
		}
		return m, nil

	case "tab", "down":
		m.setFormFocus((m.focus + 1) % fieldCount)
		return m, nil

	case "shift+tab", "up":
		m.setFormFocus((m.focus - 1 + fieldCount) % fieldCount)
		return m, nil

	case " ":
		switch m.focus {
		case fieldAuth:
			m.auth = !m.auth
			m.clearProbeState()
			return m, nil
		case fieldTrust:
			m.trust = !m.trust
			m.clearProbeState()
			return m, nil
		}

	case "enter":
		switch m.focus {
		case fieldAuth:
			m.auth = !m.auth
			m.clearProbeState()
			return m, nil
		case fieldTrust:
			m.trust = !m.trust
			m.clearProbeState()
			return m, nil
		case fieldSubmit:
			if m.submitted {
				return m, nil
			}
			m.submitted = true
			// A retry is starting: drop the stale inline error. An embedding
			// parent that needs the force-retry decision reads m.forceRetry
			// *before* delegating the key to Update (see updateConnectForm in
			// tui.go), so clearing it here is safe.
			m.clearProbeState()
			if m.standalone {
				return m, tea.Quit
			}
			return m, nil
		default:
			m.setFormFocus((m.focus + 1) % fieldCount)
			return m, nil
		}
	}

	var cmd tea.Cmd
	switch m.focus {
	case fieldServerURL:
		m.url, cmd = m.url.Update(keyMsg)
		m.clearProbeState()
	case fieldTokenEnv:
		m.tokenEnv, cmd = m.tokenEnv.Update(keyMsg)
		m.clearProbeState()
	}
	return m, cmd
}

// clearProbeState drops any inline error/force-retry state carried by the
// form: called whenever a field actually changes, since a probe failure only
// licenses forcing the *same* configuration through — editing anything means
// the next submit must probe again for real.
func (m *connectFormModel) clearProbeState() {
	m.errMsg = ""
	m.forceRetry = false
}

func (m connectFormModel) View() string {
	auth := "[ ] disabled"
	if m.auth {
		auth = "[x] enabled"
	}
	trust := "[ ] disabled"
	if m.trust {
		trust = "[x] enabled"
	}

	tokenEnvLine := formFieldLine("Token env var", m.tokenEnv.View(), m.focus == fieldTokenEnv)
	if !m.auth {
		// Secondary/dimmed when auth is off: the field is collected but unused
		// (see resolveToken/probeServer, which only read it when Auth is
		// enabled) — dimming it signals that without demoting it out of the
		// tab order, which would complicate focus handling for no real gain.
		tokenEnvLine = styleSubtitle.Render(tokenEnvLine)
	}

	fields := []string{
		formFieldLine("Server URL", m.url.View(), m.focus == fieldServerURL),
		tokenEnvLine,
		formFieldLine("Auth", auth, m.focus == fieldAuth),
		formFieldLine("Trust KB artifacts", trust, m.focus == fieldTrust),
	}

	submit := "  Connect"
	if m.focus == fieldSubmit {
		submit = styleSelected.Render("> Connect")
	}
	if m.Submitting {
		label := m.SubmittingLabel
		if label == "" {
			label = "connecting…"
		}
		submit = m.SpinnerView + " " + label
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n%s\n", m.title, strings.Join(fields, "\n"))
	if hint := fieldHint(m.focus, m.auth); hint != "" {
		fmt.Fprintf(&b, "\n%s", styleSubtitle.Render("      "+hint))
	}
	b.WriteString("\n\n")
	if m.errMsg != "" {
		fmt.Fprintf(&b, "%s\n\n", styleErrorText.Render("! "+m.errMsg))
	}
	b.WriteString(submit)
	return b.String()
}

func formFieldLine(label, value string, focused bool) string {
	marker := "  "
	if focused {
		marker = styleSelected.Render("> ")
	}
	return fmt.Sprintf("%s%-12s %s", marker, label+":", value)
}

// fieldHint returns the one-line contextual help shown below the fields for
// the currently focused field f (empty for fieldSubmit, which needs none).
// authEnabled changes the Token env hint: the variable is read from the
// environment only when Auth is on (see resolveToken/probeServer), so when
// it's off the hint says so instead of repeating the "never written to disk"
// promise that would otherwise read as misleading reassurance about a field
// that isn't even consulted.
func fieldHint(f connectField, authEnabled bool) string {
	switch f {
	case fieldServerURL:
		return "MCP endpoint of the server, e.g. https://host/mcp"
	case fieldTokenEnv:
		if !authEnabled {
			return "name of the environment variable with the token — ignored while Auth is disabled"
		}
		return "name of the environment variable that holds the bearer token — the token itself is never written to disk"
	case fieldAuth:
		return "when enabled, every call to the server carries the bearer token read from Token env var"
	case fieldTrust:
		return "trust kb: artifacts without requiring --auto-trust on every sync"
	default:
		return ""
	}
}

// --- standalone entry point ---

// runConnectForm launches a standalone bubbletea program with the connect
// form, prefilled from prefill and titled title, and blocks until the user
// submits or cancels. ok is true iff the user submitted (Enter on Submit);
// ok is false on cancel (Esc/Ctrl-C) with a zero connectOptions.
//
// errMsg, if non-empty, is shown inline above Submit right away — used by
// cmdConnect's retry loop (connect.go) to redisplay the form, still populated
// with prefill, after a failed probe or a failed doConnect (D64).
func runConnectForm(title string, prefill connectOptions, errMsg string) (connectOptions, bool, error) {
	m := newConnectFormModel(title, prefill, true)
	if errMsg != "" {
		m = m.withError(errMsg, false)
	}
	p := tea.NewProgram(m)
	result, err := p.Run()
	if err != nil {
		return connectOptions{}, false, err
	}
	fm := result.(connectFormModel)
	if fm.cancelled || !fm.submitted {
		return connectOptions{}, false, nil
	}
	return fm.Values(), true, nil
}
