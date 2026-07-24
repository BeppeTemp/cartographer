// tui.go implements the interactive dashboard launched by `cartographer` with no
// subcommand (in a terminal). It is presentation-only: all business logic —
// detecting agents, connecting, syncing, disconnecting, diffing against the
// server — lives in internal/agents, internal/clientconfig,
// internal/provisioning and the shared helpers in
// connect.go/disconnect.go/clientsync.go, reused verbatim by both the CLI
// subcommands and this dashboard.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xterm "github.com/charmbracelet/x/term"

	"github.com/BeppeTemp/cartographer/internal/agents"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// --- styles ---

var (
	styleTitle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	styleSubtitle     = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	styleBorder       = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("62")).Padding(0, 1)
	styleSelected     = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Bold(true)
	styleInstalled    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleNotInstalled = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	styleConnected    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleNotConnected = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	styleDrift        = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	styleErrorText    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	styleFooter       = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	styleEvidence     = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Italic(true)
)

// --- model ---

// screen identifies which pane the dashboard is showing.
type screen int

const (
	screenList screen = iota
	screenConnect
	screenConfirmDisconnect
)

// dashboardAgent is one row of the agent list: detection (internal/agents) plus
// connection/sync state, the latter filled in asynchronously once the remote
// manifest has been fetched.
type dashboardAgent struct {
	agents.Agent
	Connected   bool
	MCPConfigOK bool
	// SkillStatus is one of: "not connected", "checking…", "in-sync",
	// "drift ..." (see formatDiffStatus), "server unreachable", or "error: ...".
	SkillStatus string
}

// Model is the bubbletea model for the dashboard. It holds only data and
// transitions; every actual side effect (network call, file read/write) is
// wrapped in a tea.Cmd built from the shared CLI helpers, never performed
// directly in Update/View.
type Model struct {
	version string

	dir string

	// width is the terminal width in columns, from the most recent
	// tea.WindowSizeMsg (0 until bubbletea reports one, which happens once
	// right after Init on every real terminal). Used to stretch the bordered
	// box to fill the available width instead of shrink-wrapping content.
	width int

	rows    []dashboardAgent
	cursor  int
	loading bool
	spinner spinner.Model

	screen  screen
	message string
	err     error

	formProvider string
	connectForm  connectFormModel
	// probing is true while the pre-connect reachability probe (D64) is in
	// flight; submitting is true while the real connect (doConnect) is. They
	// are mutually exclusive phases of the same submit, kept separate so the
	// form's Submitting overlay can show a different label for each (see
	// View, connectForm.SubmittingLabel).
	probing    bool
	submitting bool

	// confirmProvider is the provider awaiting a disconnect confirmation
	// (screenConfirmDisconnect). confirmYes tracks the selected option in the
	// yes/no picker (false = "no", the safe default). disconnecting mirrors
	// submitting for the connect form.
	confirmProvider string
	confirmYes      bool
	disconnecting   bool
}

// --- messages ---

// remoteStatusMsg carries the result of an async fetchMergedManifest + diff pass,
// keyed by provider name. Missing keys mean "leave SkillStatus unchanged".
type remoteStatusMsg struct {
	statuses map[string]string
}

// connectDoneMsg carries the result of a connectCmd run.
type connectDoneMsg struct {
	provider string
	res      connectResult
	err      error
}

// probeDoneMsg carries the result of a probeCmd run (D64): the pre-connect
// reachability check kicked off when the connect form is submitted, before
// any file is written. opts carries the exact values the probe was run
// against, so the caller can hand them straight to connectCmd on success
// without re-reading the (by-then-possibly-stale) connectForm.
type probeDoneMsg struct {
	provider string
	opts     connectOptions
	state    probeState
	err      error
}

// syncDoneMsg carries the result of a syncCmd run.
type syncDoneMsg struct {
	provider string
	applied  provisioning.AppliedResult
	err      error
}

// disconnectDoneMsg carries the result of a disconnectCmd run.
type disconnectDoneMsg struct {
	provider string
	res      disconnectResult
	err      error
}

// --- construction ---

// newModel builds the initial dashboard state: rows are populated synchronously
// from local-only data (agents.Detect + .cartographer.yaml + mcp-config file
// presence), so the list renders immediately; remote sync status is fetched
// asynchronously by Init.
func newModel(version string) Model {
	dir, err := clientconfig.TargetDir()
	if err != nil {
		dir = "."
	}
	m := Model{
		version: version,
		dir:     dir,
		spinner: spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		screen:  screenList,
	}
	m.rows = buildRows(dir)
	m.loading = hasConnectedAgent(m.rows)
	return m
}

// buildRows computes the local-only part of the dashboard: agent detection,
// connection state from .cartographer.yaml, and mcp-config file presence.
// SkillStatus is left as "checking…" for connected agents (filled in later by
// loadRemoteStatusCmd) and "not connected" otherwise.
func buildRows(dir string) []dashboardAgent {
	detected := agents.Detect()

	connected := map[string]bool{}
	serverName := "cartographer"
	if cfg, err := clientconfig.Load(dir); err == nil {
		serverName = cfg.ServerName
		for _, a := range cfg.Agents {
			connected[a] = true
		}
	}

	rows := make([]dashboardAgent, len(detected))
	for i, a := range detected {
		row := dashboardAgent{Agent: a}
		if connected[string(a.Provider)] {
			row.Connected = true
			row.MCPConfigOK = mcpConfigStatus(dir, a.Provider, serverName)
			row.SkillStatus = "checking…"
		} else {
			row.SkillStatus = "not connected"
		}
		rows[i] = row
	}
	return rows
}

func hasConnectedAgent(rows []dashboardAgent) bool {
	for _, r := range rows {
		if r.Connected {
			return true
		}
	}
	return false
}

// mcpConfigStatus reports whether provider's MCP config file exists under dir and
// declares an MCP server named serverName. Dashboard-only presentation check: the
// actual config generation/writing lives in configurator.Emit/Apply — this just
// reads back what Emit would have written, to find the file path, and checks the
// on-disk content.
func mcpConfigStatus(dir string, provider configurator.Provider, serverName string) bool {
	r, err := configurator.Emit(&configurator.ServerConfig{Name: serverName, URL: "http://placeholder"}, provider)
	if err != nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join(dir, r.FilePath))
	if err != nil {
		return false
	}
	// TOML providers (codex) store the server as a [mcp_servers.<name>] table
	// merged into config.toml — not JSON, so json.Unmarshal below would always
	// fail. Match the table header emitCodex writes instead.
	if strings.HasSuffix(r.FilePath, ".toml") {
		return strings.Contains(string(data), "[mcp_servers."+serverName+"]")
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return false
	}
	// "mcpServers" (claude/kiro) or "mcp" (opencode) — see configurator.go.
	for _, key := range []string{"mcpServers", "mcp"} {
		raw, ok := root[key]
		if !ok {
			continue
		}
		var servers map[string]json.RawMessage
		if err := json.Unmarshal(raw, &servers); err != nil {
			continue
		}
		if _, ok := servers[serverName]; ok {
			return true
		}
	}
	return false
}

// --- tea.Cmd builders ---

// loadRemoteStatusCmd fetches the merged manifest once and computes, for every
// connected provider, the same in-sync/drift diff cmdStatus uses.
func loadRemoteStatusCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		cfg, err := clientconfig.Load(dir)
		if err != nil || len(cfg.Agents) == 0 {
			return remoteStatusMsg{}
		}

		statuses := make(map[string]string, len(cfg.Agents))
		m, err := fetchMergedManifest(cfg)
		if err != nil {
			for _, p := range cfg.Agents {
				statuses[p] = "server unreachable"
			}
			return remoteStatusMsg{statuses: statuses}
		}
		// cfg.Trust (persisted at connect time, D54) upgrades kb:-sourced
		// artifacts to Signed:true before the diff, matching cmdStatus.
		m = upgradeTrustedManifest(m, cfg.Trust)

		lockFile, err := provisioning.ReadLockFile(lockFilePath(dir))
		if err != nil {
			for _, p := range cfg.Agents {
				statuses[p] = fmt.Sprintf("error: %v", err)
			}
			return remoteStatusMsg{statuses: statuses}
		}

		for _, p := range cfg.Agents {
			lock := lockFile.ForProvider(p)
			// Only the kinds the provider supports (see FilterForProvider): a
			// hook opencode cannot materialize (or an agent for codex/kiro —
			// D55) is not drift.
			pm := provisioning.FilterForProvider(m, configurator.Provider(p))
			d := provisioning.ComputeDiff(pm, lock)
			status := formatDiffStatus(d)
			if kindLine := formatKindStatus(pm, lock); kindLine != "" {
				status += "  (" + kindLine + ")"
			}
			statuses[p] = status
		}
		return remoteStatusMsg{statuses: statuses}
	}
}

// probeCmd runs probeServer (connect.go) against opts for provider, before
// anything is written to disk (D64) — the TUI's pre-connect reachability
// check, kicked off on form submit.
func probeCmd(provider string, opts connectOptions) tea.Cmd {
	return func() tea.Msg {
		state, err := probeServer(opts)
		return probeDoneMsg{provider: provider, opts: opts, state: state, err: err}
	}
}

// connectCmd runs doConnect for a single provider (the shared logic behind the
// `connect` subcommand) and reports the outcome.
func connectCmd(provider, dir, serverURL, name, tokenEnv string, auth, trust bool) tea.Cmd {
	return func() tea.Msg {
		res, err := doConnect(connectOptions{
			Providers: []string{provider},
			Dir:       dir,
			ServerURL: serverURL,
			Name:      name,
			Auth:      auth,
			TokenEnv:  tokenEnv,
			Trust:     trust,
		})
		return connectDoneMsg{provider: provider, res: res, err: err}
	}
}

// syncCmd re-fetches the manifest and re-applies it for a single provider (the
// shared logic behind the `sync` subcommand). The dashboard never exposes
// --dry-run/--auto-trust — that subset is CLI-only (see mandate) — but does
// honor the persisted cfg.Trust (D54), same as `cartographer sync`.
func syncCmd(provider, dir string) tea.Cmd {
	return func() tea.Msg {
		cfg, err := clientconfig.Load(dir)
		if err != nil {
			return syncDoneMsg{provider: provider, err: err}
		}
		m, err := fetchMergedManifest(cfg)
		if err != nil {
			return syncDoneMsg{provider: provider, err: err}
		}
		applied, err := materializeForProviders(m, []string{provider}, dir, cfg.Trust, false, cfg.SearchRoots, cfg.Paths)
		if err != nil {
			return syncDoneMsg{provider: provider, err: err}
		}
		return syncDoneMsg{provider: provider, applied: applied[provider]}
	}
}

// disconnectCmd runs doDisconnect for a single provider (the shared logic
// behind the `disconnect` subcommand) and reports the outcome.
func disconnectCmd(provider, dir string) tea.Cmd {
	return func() tea.Msg {
		res, err := doDisconnect(disconnectOptions{Providers: []string{provider}, Dir: dir})
		return disconnectDoneMsg{provider: provider, res: res, err: err}
	}
}

// formatDiffStatus renders a provisioning.Diff as a short status string, used by
// both loadRemoteStatusCmd (dashboard) and mirrors cmdStatus's own reasoning.
func formatDiffStatus(d provisioning.Diff) string {
	if d.InSync {
		return "in-sync"
	}
	needsApproval := 0
	for _, a := range d.Added {
		if !a.Signed {
			needsApproval++
		}
	}
	for _, a := range d.Updated {
		if !a.Signed {
			needsApproval++
		}
	}
	base := fmt.Sprintf("drift +%d ~%d -%d", len(d.Added), len(d.Updated), len(d.Removed))
	if needsApproval > 0 {
		return fmt.Sprintf("%s (%d needs approval, use CLI --auto-trust)", base, needsApproval)
	}
	return base
}

// knownProvisioningKinds fixes the display order of formatKindStatus: the kinds
// provisioning supports today (skill, agent, hook — D48; instructions — D56; mcp
// — D69), in the order the mandate example uses. Kinds outside this list (future
// extensions) are appended alphabetically, so the function never silently drops
// a kind.
var knownProvisioningKinds = []string{"skill", "agent", "hook", "instructions", "mcp"}

// formatKindStatus renders provisioning.KindCounts(m, lock) as a compact per-kind
// summary string, e.g. "skill 4/5 · agent 2/2 · hook 1/1". Kinds with zero
// artifacts in the manifest are omitted. Returns "" if there is nothing to show
// (empty manifest). Used by both `cartographer status` and the TUI dashboard
// (loadRemoteStatusCmd), alongside — not in place of — formatDiffStatus.
func formatKindStatus(m provisioning.Manifest, lock provisioning.Lock) string {
	counts := provisioning.KindCounts(m, lock)

	seen := make(map[string]bool, len(counts))
	var parts []string
	for _, k := range knownProvisioningKinds {
		if c, ok := counts[k]; ok {
			parts = append(parts, fmt.Sprintf("%s %d/%d", k, c.Installed, c.Total))
			seen[k] = true
		}
	}
	var extra []string
	for k := range counts {
		if !seen[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		c := counts[k]
		parts = append(parts, fmt.Sprintf("%s %d/%d", k, c.Installed, c.Total))
	}

	return strings.Join(parts, " · ")
}

// summarizeApplied renders a provisioning.AppliedResult as a short summary line.
func summarizeApplied(r provisioning.AppliedResult) string {
	var parts []string
	if n := len(r.Written); n > 0 {
		parts = append(parts, fmt.Sprintf("%d written", n))
	}
	if n := len(r.Pruned); n > 0 {
		parts = append(parts, fmt.Sprintf("%d pruned", n))
	}
	if n := len(r.NeedsApproval); n > 0 {
		parts = append(parts, fmt.Sprintf("%d needs approval — run: %s", n, autoTrustCommand()))
	}
	if n := len(r.Unsupported); n > 0 {
		parts = append(parts, fmt.Sprintf("%d not supported by provider", n))
	}
	if n := len(r.Warnings); n > 0 {
		parts = append(parts, fmt.Sprintf("%d warning(s)", n))
	}
	if len(parts) == 0 {
		return "up to date"
	}
	return strings.Join(parts, ", ")
}

// summarizeDisconnect renders a disconnectResult for a single provider (the TUI
// never batches disconnects) as a short status string.
func summarizeDisconnect(res disconnectResult) string {
	if len(res.Providers) == 0 {
		return "nothing to disconnect"
	}
	pr := res.Providers[0]
	var parts []string
	if pr.ConfigRemoved {
		parts = append(parts, "mcp config removed")
	}
	if n := len(pr.Pruned); n > 0 {
		parts = append(parts, fmt.Sprintf("%d skill file(s) pruned", n))
	}
	if len(parts) == 0 {
		return "already disconnected"
	}
	return strings.Join(parts, ", ")
}

// --- bubbletea.Model ---

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick}
	if m.loading {
		cmds = append(cmds, loadRemoteStatusCmd(m.dir))
	}
	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	case tea.KeyMsg:
		switch m.screen {
		case screenConnect:
			return m.updateConnectForm(msg)
		case screenConfirmDisconnect:
			return m.updateConfirmDisconnect(msg)
		default:
			return m.updateList(msg)
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case remoteStatusMsg:
		m.loading = false
		for i := range m.rows {
			if !m.rows[i].Connected {
				continue
			}
			if s, ok := msg.statuses[string(m.rows[i].Provider)]; ok {
				m.rows[i].SkillStatus = s
			}
		}
		return m, nil

	case probeDoneMsg:
		m.probing = false
		if msg.err != nil || msg.state != probeReady {
			// Stay on screenConnect (never touched here) with the values just
			// entered still in connectForm: withError resets submitted so
			// Enter on Submit fires again, and forceRetry=true (D64) means
			// that — unless the user edits a field first, which clears it —
			// the *next* submit skips the probe and goes straight to connect.
			errText := probeErrorMessage(msg.state, msg.err) + " — press Connect again to force"
			m.connectForm = m.connectForm.withError(errText, true)
			m.err = nil
			m.message = ""
			return m, nil
		}
		m.submitting = true
		m.message = fmt.Sprintf("connecting %s…", msg.provider)
		return m, connectCmd(msg.provider, m.dir, msg.opts.ServerURL, msg.opts.Name, msg.opts.TokenEnv, msg.opts.Auth, msg.opts.Trust)

	case connectDoneMsg:
		m.submitting = false
		if msg.err != nil {
			// Same idea as the probe failure above: redisplay screenConnect
			// (untouched) with connectForm still populated, plus an inline
			// error clarifying that a retry needs no disconnect first — connect
			// is idempotent (mandate D64).
			errText := fmt.Sprintf("connect %s failed: %v (connect is idempotent: no need to disconnect — fix the values and press Connect again)", msg.provider, msg.err)
			m.connectForm = m.connectForm.withError(errText, false)
			m.err = nil
			m.message = ""
			return m, nil
		}
		m.err = nil
		configsMsg := fmt.Sprintf("%d config(s) written", len(msg.res.ConfigsWritten))
		if msg.res.Deferred {
			m.message = fmt.Sprintf("connected %s: %s (skill sync deferred: server unreachable)", msg.provider, configsMsg)
		} else {
			m.message = fmt.Sprintf("connected %s: %s, %s", msg.provider, configsMsg, summarizeApplied(msg.res.Applied[msg.provider]))
		}
		m.screen = screenList
		m.rows = buildRows(m.dir)
		m.loading = true
		return m, loadRemoteStatusCmd(m.dir)

	case syncDoneMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			m.message = fmt.Sprintf("sync %s failed: %v", msg.provider, msg.err)
			return m, nil
		}
		m.err = nil
		m.message = fmt.Sprintf("synced %s: %s", msg.provider, summarizeApplied(msg.applied))
		m.rows = buildRows(m.dir)
		m.loading = true
		return m, loadRemoteStatusCmd(m.dir)

	case disconnectDoneMsg:
		m.disconnecting = false
		m.screen = screenList
		if msg.err != nil {
			m.err = msg.err
			m.message = fmt.Sprintf("disconnect %s failed: %v", msg.provider, msg.err)
			return m, nil
		}
		m.err = nil
		m.message = fmt.Sprintf("disconnected %s: %s", msg.provider, summarizeDisconnect(msg.res))
		m.rows = buildRows(m.dir)
		m.loading = hasConnectedAgent(m.rows)
		if m.loading {
			return m, loadRemoteStatusCmd(m.dir)
		}
		return m, nil
	}

	return m, nil
}

// updateList handles key input on the main agent-list screen.
func (m Model) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil

	case "down", "j":
		if m.cursor < len(m.rows)-1 {
			m.cursor++
		}
		return m, nil

	case "r":
		m.rows = buildRows(m.dir)
		m.err = nil
		m.loading = hasConnectedAgent(m.rows)
		if m.loading {
			return m, loadRemoteStatusCmd(m.dir)
		}
		return m, nil

	case "enter", "s":
		if len(m.rows) == 0 {
			return m, nil
		}
		row := m.rows[m.cursor]
		if !row.Connected {
			if msg.String() != "enter" {
				return m, nil
			}
			return m.openConnectForm(row), nil
		}
		m.loading = true
		m.err = nil
		m.message = fmt.Sprintf("syncing %s…", row.Provider)
		return m, syncCmd(string(row.Provider), m.dir)

	case "d":
		if len(m.rows) == 0 {
			return m, nil
		}
		row := m.rows[m.cursor]
		if !row.Connected {
			return m, nil
		}
		return m.openConfirmDisconnect(row), nil
	}
	return m, nil
}

// openConfirmDisconnect switches to the yes/no disconnect confirmation for row.
func (m Model) openConfirmDisconnect(row dashboardAgent) Model {
	m.screen = screenConfirmDisconnect
	m.confirmProvider = string(row.Provider)
	m.confirmYes = false // default to the safe option
	m.disconnecting = false
	m.message = ""
	m.err = nil
	return m
}

// updateConfirmDisconnect handles key input on the yes/no disconnect
// confirmation picker: arrows/tab move the selection, enter confirms it,
// y/n are shortcuts, esc/q cancel back to the list without side effects.
func (m Model) updateConfirmDisconnect(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.disconnecting {
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
	}

	confirm := func() (tea.Model, tea.Cmd) {
		m.disconnecting = true
		m.message = fmt.Sprintf("disconnecting %s…", m.confirmProvider)
		return m, disconnectCmd(m.confirmProvider, m.dir)
	}
	cancel := func() (tea.Model, tea.Cmd) {
		m.screen = screenList
		m.message = ""
		m.err = nil
		return m, nil
	}

	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "left", "right", "tab", "up", "down", "h", "l":
		m.confirmYes = !m.confirmYes
		return m, nil
	case "enter":
		if m.confirmYes {
			return confirm()
		}
		return cancel()
	case "y":
		return confirm()
	case "n", "esc", "q":
		return cancel()
	}
	return m, nil
}

// openConnectForm switches to the connect form for row, prefilled from the
// existing .cartographer.yaml (or configurator defaults if there is none yet).
// The form itself lives in connectform.go (connectFormModel), shared with the
// standalone `cartographer connect` interactive prompt.
func (m Model) openConnectForm(row dashboardAgent) Model {
	cfg, err := clientconfig.Load(m.dir)
	if err != nil {
		cfg = clientconfig.Default()
	}
	prefill := connectOptions{Providers: []string{string(row.Provider)}, ServerURL: cfg.ServerURL, Name: cfg.ServerName, TokenEnv: cfg.TokenEnv, Auth: cfg.Auth, Trust: cfg.Trust}

	m.screen = screenConnect
	m.formProvider = string(row.Provider)
	m.connectForm = newConnectFormModel(fmt.Sprintf("Connect %s", row.Provider), prefill, false)
	m.connectForm.selectAgents = false
	m.probing = false
	m.submitting = false
	m.message = ""
	m.err = nil
	return m
}

// updateConnectForm handles key input on the connect form: ctrl+c always
// quits the dashboard, everything else while a probe or connect is in flight
// (m.probing/m.submitting) is ignored to avoid double-submitting, otherwise
// key input is delegated to the shared connectFormModel and the TUI reacts to
// Cancelled()/Submitted() by returning to the list or kicking off the
// pre-connect probe (probeCmd, D64) — or connectCmd directly, skipping the
// probe, when the form carries forceRetry (a second submit on the exact same
// values after a probe failure).
func (m Model) updateConnectForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.probing || m.submitting {
		return m, nil
	}

	forceRetry := m.connectForm.forceRetry

	tm, cmd := m.connectForm.Update(msg)
	m.connectForm = tm.(connectFormModel)

	if m.connectForm.Cancelled() {
		m.screen = screenList
		m.message = ""
		m.err = nil
		return m, nil
	}
	if m.connectForm.Submitted() {
		opts := m.connectForm.Values()
		opts.Providers = []string{m.formProvider}
		opts.Dir = m.dir
		m.err = nil
		m.message = ""
		if forceRetry {
			// Second consecutive submit after a probe failure, no field
			// touched in between: the user chose to force — skip the probe.
			m.submitting = true
			m.message = fmt.Sprintf("connecting %s…", m.formProvider)
			return m, connectCmd(m.formProvider, m.dir, opts.ServerURL, opts.Name, opts.TokenEnv, opts.Auth, opts.Trust)
		}
		m.probing = true
		return m, probeCmd(m.formProvider, opts)
	}
	return m, cmd
}

func (m Model) View() string {
	var b strings.Builder

	fmt.Fprintf(&b, "%s %s\n%s\n\n",
		styleTitle.Render("Cartographer"),
		styleSubtitle.Render(displayVersion(m.version)),
		styleSubtitle.Render(m.dir))

	switch m.screen {
	case screenConnect:
		form := m.connectForm
		form.Submitting = m.probing || m.submitting
		if m.probing {
			form.SubmittingLabel = "probing the server…"
		}
		form.SpinnerView = m.spinner.View()
		b.WriteString(form.View())
	case screenConfirmDisconnect:
		b.WriteString(m.viewConfirmDisconnect())
	default:
		b.WriteString(m.viewList())
	}
	b.WriteString("\n\n")

	switch {
	case m.err != nil:
		b.WriteString(styleErrorText.Render("error: " + m.err.Error()))
	case m.message != "":
		b.WriteString(m.message)
	}
	b.WriteString("\n\n")

	switch m.screen {
	case screenConnect:
		b.WriteString(styleFooter.Render("tab/shift+tab move · space toggle auth/trust · enter next/submit · esc cancel · ctrl+c quit"))
	case screenConfirmDisconnect:
		b.WriteString(styleFooter.Render("←/→ select · enter confirm · y/n shortcut · esc cancel · ctrl+c quit"))
	default:
		b.WriteString(styleFooter.Render("↑/↓ move · enter connect/sync · s sync · d disconnect · r refresh · q quit"))
	}

	box := styleBorder
	if m.width > 0 {
		// lipgloss.Style.Width already includes the horizontal padding in the
		// rendered total (it must not be subtracted twice): subtract only the
		// border to get a box exactly m.width columns wide.
		if w := m.width - styleBorder.GetHorizontalBorderSize(); w > 0 {
			box = styleBorder.Width(w)
		}
	}
	return box.Render(b.String())
}

func (m Model) viewList() string {
	var lines []string
	for i, row := range m.rows {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}

		// A single, explicit status badge: connected / not connected /
		// not installed (the old "—" for installed-but-not-connected read
		// as unknown state).
		var badge string
		switch {
		case row.Connected:
			badge = styleConnected.Render("connected")
		case row.Installed:
			badge = styleNotConnected.Render("not connected")
		default:
			badge = styleNotInstalled.Render("not installed")
		}

		line := fmt.Sprintf("%s%-14s %s", cursor, row.Name, badge)
		if i == m.cursor {
			line = styleSelected.Render(line)
		}
		lines = append(lines, line)

		// Details stacked vertically under each provider.
		if row.Installed && row.Evidence != "" {
			lines = append(lines, "      "+styleSubtitle.Render("binary      ")+styleEvidence.Render(row.Evidence))
		}
		if row.Connected {
			mcpBadge := styleDrift.Render("missing")
			if row.MCPConfigOK {
				mcpBadge = styleConnected.Render("in-sync")
			}
			skill := row.SkillStatus
			if skill == "checking…" && m.loading {
				skill = m.spinner.View() + " checking…"
			}
			lines = append(lines,
				"      "+styleSubtitle.Render("mcp-config  ")+mcpBadge,
				"      "+styleSubtitle.Render("artifacts   ")+skill)
		}
		if i < len(m.rows)-1 {
			lines = append(lines, "")
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "no agents detected")
	}
	return strings.Join(lines, "\n")
}

func (m Model) viewConfirmDisconnect() string {
	if m.disconnecting {
		return fmt.Sprintf("Disconnect %s?\n\n%s disconnecting…", m.confirmProvider, m.spinner.View())
	}
	yes, no := "  yes  ", "  no  "
	if m.confirmYes {
		yes = styleSelected.Render("> yes <")
	} else {
		no = styleSelected.Render("> no <")
	}
	return fmt.Sprintf("Disconnect %s? This removes its MCP config entry and managed artifacts.\n\n   %s   %s",
		m.confirmProvider, yes, no)
}

// displayVersion normalizes the build version for the title bar: the
// ldflags-injected tag already carries the "v" prefix (v1.1.0) and the default
// build value is "dev" — never double the prefix.
func displayVersion(v string) string {
	if v == "" || v == "dev" || strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

// --- entry point ---

// isInteractive reports whether both stdin and stdout are attached to a
// terminal. The dashboard needs an interactive TTY on both ends (keyboard input +
// rendering); anything else (pipes, redirection, non-interactive CI) falls back to
// the usage text, exactly like the pre-dashboard `cartographer` (no args) did.
func isInteractive() bool {
	return xterm.IsTerminal(os.Stdout.Fd()) && xterm.IsTerminal(os.Stdin.Fd())
}

// runTUI launches the interactive dashboard, operating on the machine-wide
// client config in the user's home directory (see clientconfig.TargetDir).
func runTUI() int {
	p := tea.NewProgram(newModel(version))
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	return 0
}
