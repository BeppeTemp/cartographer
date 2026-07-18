package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/BeppeTemp/cartographer/internal/agents"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// testModel builds a minimal, deterministic Model for Update() tests, bypassing
// newModel (which calls agents.Detect()/clientconfig.Load() against the real
// machine). Row 0 is not connected, row 1 is connected.
func testModel() Model {
	return Model{
		version: "test",
		dir:     "/tmp/does-not-matter",
		rows: []dashboardAgent{
			{
				Agent:       agents.Agent{Provider: configurator.ProviderClaudeCode, Name: "Claude Code", Installed: true},
				Connected:   false,
				SkillStatus: "not connected",
			},
			{
				Agent:       agents.Agent{Provider: configurator.ProviderOpenCode, Name: "OpenCode", Installed: true},
				Connected:   true,
				SkillStatus: "checking…",
			},
		},
		screen: screenList,
	}
}

func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func TestUpdate_ListNavigationBounds(t *testing.T) {
	m := testModel()

	// Already at 0: "up" must not go negative.
	next, _ := m.Update(keyMsg("up"))
	m = next.(Model)
	if m.cursor != 0 {
		t.Fatalf("cursor = %d, want 0", m.cursor)
	}

	next, _ = m.Update(keyMsg("down"))
	m = next.(Model)
	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.cursor)
	}

	// Already at last row: "down" must not overflow.
	next, _ = m.Update(keyMsg("down"))
	m = next.(Model)
	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want 1 (clamped)", m.cursor)
	}
}

func TestUpdate_QuitKey(t *testing.T) {
	m := testModel()
	_, cmd := m.Update(keyMsg("q"))
	if cmd == nil {
		t.Fatal("expected a non-nil tea.Cmd for quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("expected tea.QuitMsg, got %T", cmd())
	}
}

func TestUpdate_EnterOnUnconnectedOpensConnectForm(t *testing.T) {
	m := testModel()
	m.cursor = 0 // row 0 is not connected

	next, _ := m.Update(keyMsg("enter"))
	m = next.(Model)

	if m.screen != screenConnect {
		t.Fatalf("screen = %v, want screenConnect", m.screen)
	}
	if m.formProvider != string(configurator.ProviderClaudeCode) {
		t.Errorf("formProvider = %q, want %q", m.formProvider, configurator.ProviderClaudeCode)
	}
	if m.connectForm.focus != fieldServerURL {
		t.Errorf("connectForm.focus = %v, want fieldServerURL", m.connectForm.focus)
	}
	if !m.connectForm.url.Focused() {
		t.Error("connectForm.url should be focused when the connect form opens")
	}
}

func TestUpdate_SOnUnconnectedIsNoop(t *testing.T) {
	m := testModel()
	m.cursor = 0 // row 0 is not connected

	next, cmd := m.Update(keyMsg("s"))
	m = next.(Model)

	if m.screen != screenList {
		t.Fatalf("screen = %v, want screenList ('s' on an unconnected agent must be a no-op)", m.screen)
	}
	if cmd != nil {
		t.Error("expected no command from 's' on an unconnected agent")
	}
}

func TestUpdate_EnterOnConnectedTriggersSync(t *testing.T) {
	m := testModel()
	m.cursor = 1 // row 1 is connected

	next, cmd := m.Update(keyMsg("enter"))
	m = next.(Model)

	if m.screen != screenList {
		t.Fatalf("screen = %v, want screenList (sync stays on the list view)", m.screen)
	}
	if !m.loading {
		t.Error("expected loading = true while syncing")
	}
	if cmd == nil {
		t.Error("expected a non-nil sync tea.Cmd")
	}
}

func TestUpdate_EscReturnsToList(t *testing.T) {
	m := testModel()
	m = m.openConnectForm(m.rows[0])
	if m.screen != screenConnect {
		t.Fatalf("precondition failed: screen = %v, want screenConnect", m.screen)
	}

	next, _ := m.Update(keyMsg("esc"))
	m = next.(Model)
	if m.screen != screenList {
		t.Fatalf("screen = %v, want screenList after esc", m.screen)
	}
}

func TestUpdate_FormTabCyclesAndWraps(t *testing.T) {
	m := testModel()
	m = m.openConnectForm(m.rows[0])

	if m.connectForm.focus != fieldServerURL {
		t.Fatalf("precondition failed: connectForm.focus = %v, want fieldServerURL", m.connectForm.focus)
	}

	order := []connectField{fieldTokenEnv, fieldAuth, fieldTrust, fieldSubmit, fieldServerURL}
	for i, want := range order {
		next, _ := m.Update(keyMsg("tab"))
		m = next.(Model)
		if m.connectForm.focus != want {
			t.Fatalf("tab #%d: connectForm.focus = %v, want %v", i+1, m.connectForm.focus, want)
		}
	}

	// shift+tab should walk it back.
	next, _ := m.Update(keyMsg("shift+tab"))
	m = next.(Model)
	if m.connectForm.focus != fieldSubmit {
		t.Fatalf("shift+tab: connectForm.focus = %v, want fieldSubmit", m.connectForm.focus)
	}
}

func TestUpdate_FormToggleAuthWithSpace(t *testing.T) {
	m := testModel()
	m = m.openConnectForm(m.rows[0])
	m.connectForm.setFormFocus(fieldAuth)
	m.connectForm.auth = false

	next, _ := m.Update(keyMsg(" "))
	m = next.(Model)
	if !m.connectForm.auth {
		t.Fatal("expected connectForm.auth = true after space on fieldAuth")
	}

	next, _ = m.Update(keyMsg(" "))
	m = next.(Model)
	if m.connectForm.auth {
		t.Fatal("expected connectForm.auth = false after toggling twice")
	}
}

func TestUpdate_ConnectDoneMsgError(t *testing.T) {
	m := testModel()
	m = m.openConnectForm(m.rows[0])
	// Simulate values the user typed before submitting: they must survive the
	// failed connect (D64) — the form is redisplayed, not rebuilt.
	m.connectForm.url.SetValue("https://typed.example.com/mcp")
	m.connectForm.tokenEnv.SetValue("MY_TOKEN")
	m.submitting = true

	next, _ := m.Update(connectDoneMsg{provider: "claude", err: errors.New("boom")})
	m = next.(Model)

	if m.submitting {
		t.Error("submitting should be false after connectDoneMsg")
	}
	if m.screen != screenConnect {
		t.Errorf("screen = %v, want screenConnect to stay on error", m.screen)
	}
	if got := m.connectForm.url.Value(); got != "https://typed.example.com/mcp" {
		t.Errorf("url value lost on error: got %q", got)
	}
	if got := m.connectForm.tokenEnv.Value(); got != "MY_TOKEN" {
		t.Errorf("tokenEnv value lost on error: got %q", got)
	}
	if m.connectForm.errMsg == "" {
		t.Fatal("expected an inline errMsg on the form")
	}
	if !strings.Contains(m.connectForm.errMsg, "boom") {
		t.Errorf("errMsg should carry the cause, got %q", m.connectForm.errMsg)
	}
	if !strings.Contains(m.connectForm.errMsg, "idempotent") {
		t.Errorf("errMsg should clarify no disconnect is needed to retry, got %q", m.connectForm.errMsg)
	}
	if m.connectForm.Submitted() {
		t.Error("form must be re-submittable after an error (submitted reset)")
	}
	if m.connectForm.forceRetry {
		t.Error("a hard connect error must not arm forceRetry (that's probe-only)")
	}
	// The inline error must actually be rendered.
	if out := m.connectForm.View(); !strings.Contains(out, "boom") {
		t.Errorf("form View should render the inline error, got:\n%s", out)
	}
}

func TestUpdate_SubmitTriggersProbeFirst(t *testing.T) {
	m := testModel()
	m = m.openConnectForm(m.rows[0])
	m.connectForm.setFormFocus(fieldSubmit)

	next, cmd := m.Update(keyMsg("enter"))
	m = next.(Model)

	if !m.probing {
		t.Error("expected probing = true right after submit (probe before connect, D64)")
	}
	if m.submitting {
		t.Error("submitting must not be set yet: the real connect only starts after a successful probe")
	}
	if cmd == nil {
		t.Fatal("expected a probe tea.Cmd")
	}
}

func TestUpdate_ProbeDoneMsgErrorKeepsFormWithValues(t *testing.T) {
	m := testModel()
	m = m.openConnectForm(m.rows[0])
	m.connectForm.url.SetValue("https://down.example.com/mcp")
	m.probing = true

	next, cmd := m.Update(probeDoneMsg{provider: "claude", opts: connectOptions{ServerURL: "https://down.example.com/mcp"}, err: errors.New("connection refused")})
	m = next.(Model)

	if m.probing {
		t.Error("probing should be false after probeDoneMsg")
	}
	if m.submitting || cmd != nil {
		t.Error("a failed probe must not start the connect")
	}
	if m.screen != screenConnect {
		t.Fatalf("screen = %v, want screenConnect (form redisplayed)", m.screen)
	}
	if got := m.connectForm.url.Value(); got != "https://down.example.com/mcp" {
		t.Errorf("url value lost after failed probe: got %q", got)
	}
	if !m.connectForm.forceRetry {
		t.Error("a failed probe must arm forceRetry (second submit forces)")
	}
	if !strings.Contains(m.connectForm.errMsg, "unreachable") {
		t.Errorf("errMsg should say the server is unreachable, got %q", m.connectForm.errMsg)
	}
	if !strings.Contains(m.connectForm.errMsg, "force") {
		t.Errorf("errMsg should explain the force-retry escape hatch, got %q", m.connectForm.errMsg)
	}
}

func TestUpdate_SecondSubmitAfterProbeFailureForcesConnect(t *testing.T) {
	m := testModel()
	m = m.openConnectForm(m.rows[0])
	m.probing = true
	next, _ := m.Update(probeDoneMsg{provider: "claude", err: errors.New("timeout")})
	m = next.(Model)

	// Second submit with no edits in between: skip the probe, go straight to connect.
	m.connectForm.setFormFocus(fieldSubmit)
	next, cmd := m.Update(keyMsg("enter"))
	m = next.(Model)

	if m.probing {
		t.Error("second submit must not re-probe")
	}
	if !m.submitting {
		t.Error("second submit must start the real connect (force override)")
	}
	if cmd == nil {
		t.Fatal("expected a connect tea.Cmd")
	}
}

func TestUpdate_EditAfterProbeFailureDisarmsForce(t *testing.T) {
	m := testModel()
	m = m.openConnectForm(m.rows[0])
	m.probing = true
	next, _ := m.Update(probeDoneMsg{provider: "claude", err: errors.New("timeout")})
	m = next.(Model)

	// Editing the URL invalidates the failed probe: next submit must probe again.
	m.connectForm.setFormFocus(fieldServerURL)
	next, _ = m.Update(keyMsg("x"))
	m = next.(Model)
	if m.connectForm.forceRetry {
		t.Fatal("editing a field must disarm forceRetry")
	}
	if m.connectForm.errMsg != "" {
		t.Error("editing a field must clear the inline error")
	}

	m.connectForm.setFormFocus(fieldSubmit)
	next, _ = m.Update(keyMsg("enter"))
	m = next.(Model)
	if !m.probing {
		t.Error("after an edit, submit must probe again, not force the connect")
	}
	if m.submitting {
		t.Error("after an edit, submit must not skip to the connect")
	}
}

func TestUpdate_ProbeDoneMsgSuccessStartsConnect(t *testing.T) {
	m := testModel()
	m = m.openConnectForm(m.rows[0])
	m.probing = true

	next, cmd := m.Update(probeDoneMsg{provider: "claude", opts: connectOptions{ServerURL: "http://ok/mcp", Name: "cartographer", TokenEnv: "TOK"}})
	m = next.(Model)

	if m.probing {
		t.Error("probing should be false after probeDoneMsg")
	}
	if !m.submitting {
		t.Error("a successful probe must start the real connect")
	}
	if cmd == nil {
		t.Fatal("expected a connect tea.Cmd after a successful probe")
	}
}

func TestUpdate_ConnectDoneMsgSuccessReturnsToList(t *testing.T) {
	m := testModel()
	m = m.openConnectForm(m.rows[0])

	res := connectResult{
		Providers: []string{"claude"},
		Applied:   map[string]provisioning.AppliedResult{"claude": {}},
	}
	next, cmd := m.Update(connectDoneMsg{provider: "claude", res: res})
	m = next.(Model)

	if m.screen != screenList {
		t.Fatalf("screen = %v, want screenList after a successful connect", m.screen)
	}
	if m.message == "" {
		t.Error("expected a non-empty message after a successful connect")
	}
	if m.err != nil {
		t.Errorf("err = %v, want nil", m.err)
	}
	if cmd == nil {
		t.Error("expected a refresh command (loadRemoteStatusCmd) after connecting")
	}
}

func TestUpdate_SyncDoneMsgError(t *testing.T) {
	m := testModel()
	m.loading = true

	next, _ := m.Update(syncDoneMsg{provider: "opencode", err: errors.New("network down")})
	m = next.(Model)

	if m.loading {
		t.Error("loading should be false after syncDoneMsg")
	}
	if m.err == nil {
		t.Fatal("expected m.err to be set")
	}
}

func TestUpdate_RemoteStatusMsgUpdatesConnectedRowsOnly(t *testing.T) {
	m := testModel()
	m.loading = true

	next, _ := m.Update(remoteStatusMsg{statuses: map[string]string{
		string(configurator.ProviderOpenCode): "in-sync",
	}})
	m = next.(Model)

	if m.loading {
		t.Error("loading should be false after remoteStatusMsg")
	}
	if m.rows[1].SkillStatus != "in-sync" {
		t.Errorf("connected row SkillStatus = %q, want %q", m.rows[1].SkillStatus, "in-sync")
	}
	if m.rows[0].SkillStatus != "not connected" {
		t.Errorf("unconnected row SkillStatus should be untouched, got %q", m.rows[0].SkillStatus)
	}
}

func TestUpdate_DOnConnectedOpensConfirmDisconnect(t *testing.T) {
	m := testModel()
	m.cursor = 1 // row 1 is connected

	next, cmd := m.Update(keyMsg("d"))
	m = next.(Model)

	if m.screen != screenConfirmDisconnect {
		t.Fatalf("screen = %v, want screenConfirmDisconnect", m.screen)
	}
	if m.confirmProvider != string(configurator.ProviderOpenCode) {
		t.Errorf("confirmProvider = %q, want %q", m.confirmProvider, configurator.ProviderOpenCode)
	}
	if cmd != nil {
		t.Error("opening the confirmation screen should not issue a command yet")
	}
}

func TestUpdate_DOnUnconnectedIsNoop(t *testing.T) {
	m := testModel()
	m.cursor = 0 // row 0 is not connected

	next, cmd := m.Update(keyMsg("d"))
	m = next.(Model)

	if m.screen != screenList {
		t.Fatalf("screen = %v, want screenList ('d' on an unconnected agent must be a no-op)", m.screen)
	}
	if cmd != nil {
		t.Error("expected no command from 'd' on an unconnected agent")
	}
}

func TestUpdate_ConfirmDisconnectYTriggersCmd(t *testing.T) {
	m := testModel()
	m = m.openConfirmDisconnect(m.rows[1])

	next, cmd := m.Update(keyMsg("y"))
	m = next.(Model)

	if !m.disconnecting {
		t.Error("expected disconnecting = true after confirming with y")
	}
	if cmd == nil {
		t.Error("expected a non-nil disconnect tea.Cmd")
	}
}

func TestUpdate_ConfirmDisconnectNCancels(t *testing.T) {
	m := testModel()
	m = m.openConfirmDisconnect(m.rows[1])

	next, cmd := m.Update(keyMsg("n"))
	m = next.(Model)

	if m.screen != screenList {
		t.Fatalf("screen = %v, want screenList after cancelling with n", m.screen)
	}
	if cmd != nil {
		t.Error("expected no command after cancelling")
	}
}

func TestUpdate_ConfirmDisconnectEscCancels(t *testing.T) {
	m := testModel()
	m = m.openConfirmDisconnect(m.rows[1])

	next, _ := m.Update(keyMsg("esc"))
	m = next.(Model)

	if m.screen != screenList {
		t.Fatalf("screen = %v, want screenList after cancelling with esc", m.screen)
	}
}

func TestUpdate_DisconnectDoneMsgSuccessReturnsToList(t *testing.T) {
	m := testModel()
	m = m.openConfirmDisconnect(m.rows[1])
	m.disconnecting = true

	res := disconnectResult{Providers: []disconnectProviderResult{
		{Provider: "opencode", ConfigRemoved: true},
	}}
	next, _ := m.Update(disconnectDoneMsg{provider: "opencode", res: res})
	m = next.(Model)

	if m.disconnecting {
		t.Error("disconnecting should be false after disconnectDoneMsg")
	}
	if m.screen != screenList {
		t.Fatalf("screen = %v, want screenList after a successful disconnect", m.screen)
	}
	if m.message == "" {
		t.Error("expected a non-empty message after a successful disconnect")
	}
	if m.err != nil {
		t.Errorf("err = %v, want nil", m.err)
	}
}

func TestUpdate_DisconnectDoneMsgError(t *testing.T) {
	m := testModel()
	m = m.openConfirmDisconnect(m.rows[1])
	m.disconnecting = true

	next, _ := m.Update(disconnectDoneMsg{provider: "opencode", err: errors.New("boom")})
	m = next.(Model)

	if m.disconnecting {
		t.Error("disconnecting should be false after disconnectDoneMsg")
	}
	if m.err == nil {
		t.Fatal("expected m.err to be set")
	}
}

func TestFormatDiffStatus(t *testing.T) {
	if got := formatDiffStatus(provisioning.Diff{InSync: true}); got != "in-sync" {
		t.Errorf("in-sync diff: got %q", got)
	}

	d := provisioning.Diff{
		Added: []provisioning.Artifact{{Kind: "skill", Name: "a", Signed: false}},
	}
	if got := formatDiffStatus(d); got == "in-sync" || got == "" {
		t.Errorf("drift diff should not report in-sync/empty, got %q", got)
	}
}

func TestFormatKindStatus(t *testing.T) {
	m := provisioning.Manifest{
		Revision: "rev1",
		Artifacts: []provisioning.Artifact{
			{Kind: "skill", Name: "s1", ContentHash: "h1"},
			{Kind: "skill", Name: "s2", ContentHash: "h2"},
			{Kind: "agent", Name: "a1", ContentHash: "h3"},
			{Kind: "agent", Name: "a2", ContentHash: "h4"},
			{Kind: "hook", Name: "hk1", ContentHash: "h5"},
		},
	}
	lock := provisioning.Lock{
		Managed: []provisioning.ManagedFile{
			{Kind: "skill", Name: "s1", Path: "x", ContentHash: "h1"},
			{Kind: "agent", Name: "a1", Path: "y", ContentHash: "h3"},
			{Kind: "agent", Name: "a2", Path: "z", ContentHash: "h4"},
		},
	}

	got := formatKindStatus(m, lock)
	want := "skill 1/2 · agent 2/2 · hook 0/1"
	if got != want {
		t.Errorf("formatKindStatus: got %q, want %q", got, want)
	}
}

func TestFormatKindStatus_Empty(t *testing.T) {
	if got := formatKindStatus(provisioning.Manifest{}, provisioning.Lock{}); got != "" {
		t.Errorf("formatKindStatus on empty manifest: got %q, want \"\"", got)
	}
}

func TestFormatKindStatus_UnknownKindAppendedAlphabetically(t *testing.T) {
	m := provisioning.Manifest{
		Artifacts: []provisioning.Artifact{
			{Kind: "skill", Name: "s1", ContentHash: "h1"},
			{Kind: "zzz-future-kind", Name: "f1", ContentHash: "h2"},
		},
	}
	got := formatKindStatus(m, provisioning.Lock{})
	want := "skill 0/1 · zzz-future-kind 0/1"
	if got != want {
		t.Errorf("formatKindStatus: got %q, want %q", got, want)
	}
}

func TestDisplayVersion(t *testing.T) {
	cases := map[string]string{
		"v1.1.0": "v1.1.0", // ldflags tag: prefix already present, never double it
		"1.1.0":  "v1.1.0",
		"dev":    "dev",
		"":       "",
	}
	for in, want := range cases {
		if got := displayVersion(in); got != want {
			t.Errorf("displayVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestViewList_ExplicitStates(t *testing.T) {
	m := Model{
		version: "test",
		dir:     "/tmp/does-not-matter",
		rows: []dashboardAgent{
			{Agent: agents.Agent{Name: "Claude Code", Installed: true, Evidence: "/bin/claude"}, Connected: true, MCPConfigOK: true, SkillStatus: "in-sync"},
			{Agent: agents.Agent{Name: "OpenCode", Installed: true, Evidence: "/bin/opencode"}},
			{Agent: agents.Agent{Name: "Kiro"}},
		},
		screen: screenList,
	}
	out := m.viewList()
	for _, want := range []string{"connected", "not connected", "not installed", "binary", "mcp-config", "artifacts"} {
		if !strings.Contains(out, want) {
			t.Errorf("viewList: missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "—") {
		t.Errorf("viewList: the ambiguous — badge must no longer appear:\n%s", out)
	}
}

func TestUpdate_ConfirmDisconnectPicker(t *testing.T) {
	m := testModel()
	m.screen = screenConfirmDisconnect
	m.confirmProvider = "opencode"

	// Default: "no" selected → enter cancels with no side effects.
	tm, cmd := m.Update(keyMsg("enter"))
	m2 := tm.(Model)
	if m2.screen != screenList || cmd != nil {
		t.Fatalf("enter on default no: expected return to the list with no cmd")
	}

	// Toggle to yes → enter starts the disconnect.
	m.screen = screenConfirmDisconnect
	tm, _ = m.Update(keyMsg("left"))
	m3 := tm.(Model)
	if !m3.confirmYes {
		t.Fatalf("left must select yes")
	}
	tm, cmd = m3.Update(keyMsg("enter"))
	m4 := tm.(Model)
	if !m4.disconnecting || cmd == nil {
		t.Fatalf("enter on yes must start the disconnect")
	}

	// The n shortcut cancels.
	m.screen = screenConfirmDisconnect
	m.confirmYes = true
	tm, _ = m.Update(keyMsg("n"))
	if tm.(Model).screen != screenList {
		t.Fatalf("n must cancel")
	}
}

func TestView_StretchesToTerminalWidth(t *testing.T) {
	m := testModel()

	// Without a known WindowSizeMsg (width==0) the box must not have a forced
	// width: lipgloss.Width depends on the content alone.
	unbounded := lipgloss.Width(m.View())

	// With a 100-column terminal the box must span the whole row — not stay
	// shrink-wrapped to the content.
	m.width = 100
	got := lipgloss.Width(m.View())
	if got != 100 {
		t.Errorf("View() width = %d, want 100 (m.width=100)", got)
	}
	if unbounded >= 100 {
		t.Skip("content already >=100 wide without width: the test cannot discriminate, but the behavior is still correct")
	}
}

// TestMCPConfigStatus_CodexTOML pins the fix for the JSON-only check that made
// Codex always report mcp-config "missing": config.toml is TOML, so the old
// json.Unmarshal path always failed. mcpConfigStatus must instead match the
// [mcp_servers.<name>] table emitCodex writes.
func TestMCPConfigStatus_CodexTOML(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	tomlPath := filepath.Join(dir, ".codex", "config.toml")

	// No block yet → missing.
	if err := os.WriteFile(tomlPath, []byte("model = \"gpt-5.5\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if mcpConfigStatus(dir, configurator.ProviderCodex, "cartographer") {
		t.Error("expected false: config.toml has no [mcp_servers.cartographer] table")
	}

	// With the managed block → in-sync.
	body := "model = \"gpt-5.5\"\n[mcp_servers.cartographer]\nurl = \"http://x\"\n"
	if err := os.WriteFile(tomlPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if !mcpConfigStatus(dir, configurator.ProviderCodex, "cartographer") {
		t.Error("expected true: config.toml declares [mcp_servers.cartographer]")
	}
}

// TestMCPConfigStatus_JSONProviders guards the untouched JSON path
// (claude/opencode): presence of the server under the right key means in-sync.
func TestMCPConfigStatus_JSONProviders(t *testing.T) {
	dir := t.TempDir()
	r, err := configurator.Emit(&configurator.ServerConfig{Name: "cartographer", URL: "http://x"}, configurator.ProviderOpenCode)
	if err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(dir, r.FilePath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(`{"mcp":{"cartographer":{"type":"remote"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !mcpConfigStatus(dir, configurator.ProviderOpenCode, "cartographer") {
		t.Error("expected true: opencode config declares the server under \"mcp\"")
	}
	if mcpConfigStatus(dir, configurator.ProviderOpenCode, "other") {
		t.Error("expected false: server name not present")
	}
}

func TestUpdate_WindowSizeMsgSetsWidth(t *testing.T) {
	m := testModel()
	tm, cmd := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m2 := tm.(Model)
	if m2.width != 120 {
		t.Errorf("width = %d, want 120", m2.width)
	}
	if cmd != nil {
		t.Errorf("WindowSizeMsg non deve produrre un cmd")
	}
}
