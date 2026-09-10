package main

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/BeppeTemp/cartographer/internal/agents"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestViewResponsiveServerPanelAndWidth(t *testing.T) {
	ready := true
	for _, width := range []int{60, 80, 120} {
		m := testModel()
		m.width = width
		m.snapshot = &statusSnapshot{Schema: statusSchema, ServerURL: "https://very-long.example.test/a/really/long/mcp/endpoint", Reachable: true, Ready: &ready, Client: "v1", Server: "v2", State: "version_skew"}
		out := ansiRE.ReplaceAllString(m.View(), "")
		if !strings.Contains(out, "server") || !strings.Contains(out, "ready") {
			t.Fatalf("width %d misses server/readiness: %s", width, out)
		}
		for _, line := range strings.Split(out, "\n") {
			if got := utf8.RuneCountInString(line); got > width {
				t.Fatalf("width %d overflow %d: %q", width, got, line)
			}
		}
	}
}

func TestViewServerFailureOnceAndSyncAll(t *testing.T) {
	m := testModel()
	m.width = 80
	m.snapshot = &statusSnapshot{Schema: statusSchema, ServerURL: "http://127.0.0.1:39273/mcp", State: "unavailable", Error: &statusError{Message: "could not reach server; check service status"}}
	out := ansiRE.ReplaceAllString(m.View(), "")
	if strings.Count(out, "could not reach server") != 1 {
		t.Fatalf("server error repeated: %s", out)
	}
	next, cmd := m.Update(keyMsg("S"))
	m = next.(Model)
	if !m.loading || cmd == nil {
		t.Fatal("S must sync all connected providers")
	}
}

func TestNarrowFailureAndConfirmationDoNotOverflow(t *testing.T) {
	for _, width := range []int{60, 80, 120} {
		m := testModel()
		m.width = width
		m.snapshot = &statusSnapshot{Schema: statusSchema, State: "unavailable", Error: &statusError{Message: "could not reach https://a-very-long-endpoint.example.test/with/a/long/path; check cartographer service status"}}
		m.screen, m.confirmProvider = screenConfirmDisconnect, "a-provider-with-a-long-name"
		out := ansiRE.ReplaceAllString(m.View(), "")
		for _, line := range strings.Split(out, "\n") {
			if got := utf8.RuneCountInString(line); got > width {
				t.Fatalf("width %d overflow %d: %q", width, got, line)
			}
		}
		if !strings.Contains(out, "yes") {
			t.Fatalf("width %d hides primary confirmation", width)
		}
	}
}

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
				BoundKBs:    []string{"kb-uno"},
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

func TestFormatTUIProviderStatusShowsMCPApprovalStates(t *testing.T) {
	p := providerStatus{Name: "claude", Connected: true, State: "drift", Added: []statusArtifact{{Kind: "mcp", Trust: "approved"}, {Kind: "mcp", Trust: "approval_stale"}, {Kind: "mcp", Trust: "needs_approval"}}, Updated: []statusArtifact{{Kind: "mcp", Trust: "verified"}}}
	got := formatTUIProviderStatus(p)
	for _, want := range []string{"approved 1", "approval-stale 1", "needs-approval 1", "verified 1"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q missing %q", got, want)
		}
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

	got := formatKindStatus(m, lock, false)
	want := "skill 1/2 · agent 2/2 · hook 0/1"
	if got != want {
		t.Errorf("formatKindStatus: got %q, want %q", got, want)
	}
}

func TestFormatKindStatus_Empty(t *testing.T) {
	if got := formatKindStatus(provisioning.Manifest{}, provisioning.Lock{}, false); got != "" {
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
	got := formatKindStatus(m, provisioning.Lock{}, false)
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
			{Agent: agents.Agent{Name: "Claude Code", Installed: true, Evidence: "/bin/claude"}, Connected: true, MCPConfigState: mcpConfigInSync, SkillStatus: "in-sync"},
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
// [mcp_servers.<name>] table emitCodex writes. Zero/one-KB set: exactly one
// expected entry (the bare server name), so the badge is binary missing/in-sync.
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
	if got := mcpConfigStatus(dir, configurator.ProviderCodex, "cartographer", nil, nil); got != mcpConfigMissing {
		t.Errorf("state = %v, want mcpConfigMissing: config.toml has no [mcp_servers.cartographer] table", got)
	}

	// With the managed block → in-sync.
	body := "model = \"gpt-5.5\"\n[mcp_servers.cartographer]\nurl = \"http://x\"\n"
	if err := os.WriteFile(tomlPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := mcpConfigStatus(dir, configurator.ProviderCodex, "cartographer", nil, nil); got != mcpConfigInSync {
		t.Errorf("state = %v, want mcpConfigInSync: config.toml declares [mcp_servers.cartographer]", got)
	}
}

// TestMCPConfigStatus_CodexTOML_MultiKB covers the three-state badge (D120)
// on the TOML provider across a multi-KB set: none, some, and all of the
// per-KB [mcp_servers.cartographer-<kb>] tables present.
func TestMCPConfigStatus_CodexTOML_MultiKB(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	tomlPath := filepath.Join(dir, ".codex", "config.toml")
	kbs := []string{"alpha", "beta"}

	if err := os.WriteFile(tomlPath, []byte("model = \"gpt-5.5\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := mcpConfigStatus(dir, configurator.ProviderCodex, "cartographer", kbs, kbs); got != mcpConfigMissing {
		t.Errorf("none present: state = %v, want mcpConfigMissing", got)
	}

	if err := os.WriteFile(tomlPath, []byte("[mcp_servers.cartographer-alpha]\nurl = \"http://x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := mcpConfigStatus(dir, configurator.ProviderCodex, "cartographer", kbs, kbs); got != mcpConfigPartial {
		t.Errorf("one of two present: state = %v, want mcpConfigPartial", got)
	}

	body := "[mcp_servers.cartographer-alpha]\nurl = \"http://x\"\n[mcp_servers.cartographer-beta]\nurl = \"http://y\"\n"
	if err := os.WriteFile(tomlPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := mcpConfigStatus(dir, configurator.ProviderCodex, "cartographer", kbs, kbs); got != mcpConfigInSync {
		t.Errorf("both present: state = %v, want mcpConfigInSync", got)
	}
}

// TestMCPConfigStatus_JSONProviders guards the untouched JSON path
// (claude/opencode): presence of the server under the right key means
// in-sync; a wrong name means missing.
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
	if got := mcpConfigStatus(dir, configurator.ProviderOpenCode, "cartographer", nil, nil); got != mcpConfigInSync {
		t.Errorf("state = %v, want mcpConfigInSync: opencode config declares the server under \"mcp\"", got)
	}
	if got := mcpConfigStatus(dir, configurator.ProviderOpenCode, "other", nil, nil); got != mcpConfigMissing {
		t.Errorf("state = %v, want mcpConfigMissing: server name not present", got)
	}
}

// TestMCPConfigStatus_JSONProviders_MultiKB covers the three-state badge on
// the JSON path across a multi-KB set: none, some, and all of the per-KB
// entries present under "mcp".
func TestMCPConfigStatus_JSONProviders_MultiKB(t *testing.T) {
	dir := t.TempDir()
	r, err := configurator.Emit(&configurator.ServerConfig{Name: "cartographer", URL: "http://x"}, configurator.ProviderOpenCode)
	if err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(dir, r.FilePath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	kbs := []string{"alpha", "beta"}

	if err := os.WriteFile(full, []byte(`{"mcp":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := mcpConfigStatus(dir, configurator.ProviderOpenCode, "cartographer", kbs, kbs); got != mcpConfigMissing {
		t.Errorf("none present: state = %v, want mcpConfigMissing", got)
	}

	if err := os.WriteFile(full, []byte(`{"mcp":{"cartographer-alpha":{"type":"remote"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := mcpConfigStatus(dir, configurator.ProviderOpenCode, "cartographer", kbs, kbs); got != mcpConfigPartial {
		t.Errorf("one of two present: state = %v, want mcpConfigPartial", got)
	}

	body := `{"mcp":{"cartographer-alpha":{"type":"remote"},"cartographer-beta":{"type":"remote"}}}`
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := mcpConfigStatus(dir, configurator.ProviderOpenCode, "cartographer", kbs, kbs); got != mcpConfigInSync {
		t.Errorf("both present: state = %v, want mcpConfigInSync", got)
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

// --- D175 WP1: per-provider KB attribution ---------------------------------

// TestFormatBoundKBs pins the three states BoundKBs distinguishes as three
// different sentences: an explicit empty binding and an absent one are
// opposite instructions and must never render the same way.
func TestFormatBoundKBs(t *testing.T) {
	tests := []struct {
		name     string
		kbs      []string
		explicit bool
		width    int
		want     string
	}{
		{"explicit list", []string{"kb-uno", "kb-due"}, true, 0, "kb-uno, kb-due  (explicit)"},
		{"default", []string{"kb-uno", "kb-due"}, false, 0, "all known  (default)"},
		{"explicit empty", nil, true, 0, "none  (explicit)"},
		{"truncated with a counter", []string{"kb-uno", "kb-due", "kb-tre", "kb-quattro"}, true, 32, "kb-uno, kb-due +2  (explicit)"},
		{"not even one name fits", []string{"a-very-long-kb-name", "another-one"}, true, 18, "2 KBs  (explicit)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatBoundKBs(tt.kbs, tt.explicit, tt.width); got != tt.want {
				t.Errorf("formatBoundKBs = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestJoinWithCounterNeverWraps: the value must stay on one line whatever the
// width — a wrapped value breaks the column the card is read by.
func TestJoinWithCounterNeverWraps(t *testing.T) {
	for _, width := range []int{0, 5, 12, 40, 200} {
		got := joinWithCounter([]string{"kb-uno", "kb-due", "kb-tre"}, width)
		if strings.Contains(got, "\n") {
			t.Fatalf("width %d wrapped: %q", width, got)
		}
	}
}

// TestViewList_KBLineStates renders the three binding states through the real
// view, on the grid, under mcp-config.
func TestViewList_KBLineStates(t *testing.T) {
	tests := []struct {
		name string
		row  dashboardAgent
		want string
	}{
		{"explicit", dashboardAgent{Agent: agents.Agent{Name: "Claude Code"}, Connected: true, BoundKBs: []string{"kb-uno", "kb-due"}, BindingExplicit: true}, "kbs         kb-uno, kb-due  (explicit)"},
		{"default", dashboardAgent{Agent: agents.Agent{Name: "Claude Code"}, Connected: true, BoundKBs: []string{"kb-uno"}}, "kbs         all known  (default)"},
		{"explicit empty", dashboardAgent{Agent: agents.Agent{Name: "Claude Code"}, Connected: true, BindingExplicit: true}, "kbs         none  (explicit)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{version: "test", rows: []dashboardAgent{tt.row}, screen: screenList}
			out := ansiRE.ReplaceAllString(m.viewList(), "")
			if !strings.Contains(out, tt.want) {
				t.Errorf("viewList missing %q in:\n%s", tt.want, out)
			}
		})
	}
}

// TestBuildRowsResolvesBindingLocally is the WP1 invariant: the binding is
// local data (.cartographer.yaml), so it is in the first frame — buildRows
// contacts nothing.
func TestBuildRowsResolvesBindingLocally(t *testing.T) {
	dir := t.TempDir()
	cfg := &clientconfig.Config{
		ServerURL: "http://127.0.0.1:1/mcp",
		Agents:    []string{string(configurator.ProviderClaudeCode), string(configurator.ProviderOpenCode)},
		KnownKBs:  []string{"kb-uno", "kb-due"},
		Clients: map[string]clientconfig.ClientBinding{
			string(configurator.ProviderClaudeCode): {KBs: []string{"kb-due"}},
		},
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}

	for _, row := range buildRows(dir) {
		if !row.Connected {
			continue
		}
		switch row.Provider {
		case configurator.ProviderClaudeCode:
			if !row.BindingExplicit || len(row.BoundKBs) != 1 || row.BoundKBs[0] != "kb-due" {
				t.Errorf("claude binding = %v explicit=%t, want [kb-due] explicit", row.BoundKBs, row.BindingExplicit)
			}
		case configurator.ProviderOpenCode:
			if row.BindingExplicit || len(row.BoundKBs) != 2 {
				t.Errorf("opencode binding = %v explicit=%t, want the two known KBs by default", row.BoundKBs, row.BindingExplicit)
			}
		}
	}
}

// --- D175 WP2: the per-kind breakdown, and honesty about not knowing -------

// TestViewList_KindsUnknownBeforeAndAfterAFailedFetch: an unmeasured breakdown
// must read as unknown. Showing the previous poll's counts — or nothing, which
// reads as "clean" — would report a verdict computed against a manifest that
// was never fetched.
func TestViewList_KindsUnknownBeforeAndAfterAFailedFetch(t *testing.T) {
	row := dashboardAgent{Agent: agents.Agent{Name: "Claude Code"}, Connected: true, SkillStatus: "checking…"}
	m := Model{version: "test", rows: []dashboardAgent{row}, screen: screenList}
	if out := ansiRE.ReplaceAllString(m.viewList(), ""); !strings.Contains(out, "kinds       unknown") {
		t.Errorf("before the fetch the breakdown must be unknown:\n%s", out)
	}

	// A fetch that failed: the provider is absent from the kinds map, so a
	// breakdown left over from an earlier poll must be cleared.
	m.rows[0].KindStatus, m.rows[0].KindsKnown = "skill 5/5", true
	next, _ := m.Update(remoteStatusMsg{
		statuses: map[string]string{"": "unknown"},
		kinds:    map[string]string{},
		snapshot: statusSnapshot{Schema: statusSchema, State: "unavailable"},
	})
	m = next.(Model)
	if m.rows[0].KindsKnown {
		t.Error("a failed fetch must clear the breakdown, not keep the stale one")
	}
	if out := ansiRE.ReplaceAllString(m.viewList(), ""); !strings.Contains(out, "kinds       unknown") {
		t.Errorf("after a failed fetch the breakdown must be unknown:\n%s", out)
	}
}

// TestViewList_KindsAfterFetch: a successful fetch shows the breakdown, and an
// empty-but-read manifest is not rendered as unknown.
func TestViewList_KindsAfterFetch(t *testing.T) {
	m := testModel()
	next, _ := m.Update(remoteStatusMsg{
		statuses: map[string]string{string(configurator.ProviderOpenCode): "in-sync"},
		kinds:    map[string]string{string(configurator.ProviderOpenCode): "skill 5/5 · agent 4/4"},
	})
	m = next.(Model)
	out := ansiRE.ReplaceAllString(m.viewList(), "")
	if !strings.Contains(out, "kinds       skill 5/5 · agent 4/4") {
		t.Errorf("missing the breakdown after the fetch:\n%s", out)
	}

	// Read, and empty: known, with nothing in it.
	next, _ = m.Update(remoteStatusMsg{
		statuses: map[string]string{string(configurator.ProviderOpenCode): "in-sync"},
		kinds:    map[string]string{string(configurator.ProviderOpenCode): ""},
	})
	out = ansiRE.ReplaceAllString(next.(Model).viewList(), "")
	if !strings.Contains(out, "kinds       no artifacts") {
		t.Errorf("an empty but successfully read manifest must not read as unknown:\n%s", out)
	}
}

// TestViewList_SpinnerDuringTheFetch: the artifacts line keeps the spinner
// while loading, and the breakdown below it says unknown.
func TestViewList_SpinnerDuringTheFetch(t *testing.T) {
	m := testModel()
	m.loading = true
	out := ansiRE.ReplaceAllString(m.viewList(), "")
	if !strings.Contains(out, "checking…") || !strings.Contains(out, "kinds       unknown") {
		t.Errorf("expected the spinner state and an unknown breakdown:\n%s", out)
	}
}

// --- D175 WP3: the server panel --------------------------------------------

// TestViewServerPanel_LabelledBlock covers the WP3 acceptance: readable at 100
// columns with four KBs, one labelled line each, and a binding count per KB
// including the zero that matters most.
func TestViewServerPanel_LabelledBlock(t *testing.T) {
	ready := true
	m := testModel()
	m.width = 100
	m.snapshot = &statusSnapshot{
		Schema: statusSchema, ServerURL: "http://localhost:39273/mcp", Reachable: true, Ready: &ready,
		Client: "v0.10.0", Server: "v0.10.0", State: "in_sync",
		KBs: []string{"kb-uno", "kb-due", "kb-tre", "kb-quattro"},
		Providers: []providerStatus{
			{Name: "claude", Connected: true, BoundKBs: []string{"kb-uno", "kb-due"}},
			{Name: "codex", Connected: true, BoundKBs: []string{"kb-uno"}},
			{Name: "kiro", Connected: true, BoundKBs: []string{"kb-uno"}},
			{Name: "hermes", Connected: false, BoundKBs: []string{"kb-tre"}},
		},
		Service: &serviceSnapshot{Installed: true, Running: true, Lifecycle: "loaded"},
	}
	out := ansiRE.ReplaceAllString(m.viewServerPanel(), "")
	for _, want := range []string{
		"server     http://localhost:39273/mcp  in-sync · ready",
		"version    client v0.10.0 · server v0.10.0",
		"service    local: installed · loaded",
		"KBs        kb-uno (3 bound) · kb-due (1) · kb-tre (0) · kb-quattro (0)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("server panel missing %q in:\n%s", want, out)
		}
	}
	for _, line := range strings.Split(ansiRE.ReplaceAllString(m.View(), ""), "\n") {
		if got := utf8.RuneCountInString(line); got > 100 {
			t.Errorf("overflow at 100 columns (%d): %q", got, line)
		}
	}
}

// TestViewServerPanel_NoLocalService: with nothing installed the panel says
// nothing about a service, rather than rendering an absence as a failure
// (D174).
func TestViewServerPanel_NoLocalService(t *testing.T) {
	for _, svc := range []*serviceSnapshot{nil, {Installed: false}} {
		m := testModel()
		m.width = 100
		m.snapshot = &statusSnapshot{Schema: statusSchema, ServerURL: "http://h/mcp", Client: "v1", State: "in_sync", Service: svc}
		if out := ansiRE.ReplaceAllString(m.viewServerPanel(), ""); strings.Contains(out, "service") {
			t.Errorf("no local service must produce no service line:\n%s", out)
		}
	}
}

// TestViewServerPanel_VersionSkewKeepsTheDriftStyle
func TestViewServerPanel_VersionSkewKeepsTheDriftStyle(t *testing.T) {
	m := testModel()
	m.width = 100
	m.snapshot = &statusSnapshot{Schema: statusSchema, ServerURL: "http://h/mcp", Reachable: true, Client: "v0.10.0", Server: "v0.11.0", State: "version_skew"}
	raw := m.viewServerPanel()
	if !strings.Contains(ansiRE.ReplaceAllString(raw, ""), "version    client v0.10.0 · server v0.11.0") {
		t.Errorf("missing the version line:\n%s", raw)
	}
	// Asserted through the style itself, so the check holds whether or not
	// the test environment renders colour: under a no-colour profile
	// styleDrift.Render is the identity and the assertion degrades to the
	// text, under a colour profile it pins the escape sequences.
	if !strings.Contains(raw, styleDrift.Render("client v0.10.0 · server v0.11.0")) {
		t.Errorf("a version skew must keep the drift style:\n%q", raw)
	}
}

// TestViewServerPanel_ReadableAtReducedWidth
func TestViewServerPanel_ReadableAtReducedWidth(t *testing.T) {
	for _, width := range []int{60, 80, 100, 120} {
		m := testModel()
		m.width = width
		m.snapshot = &statusSnapshot{
			Schema: statusSchema, ServerURL: "http://localhost:39273/mcp", Reachable: true, Client: "v0.10.0", Server: "v0.10.0", State: "in_sync",
			KBs:       []string{"kb-uno", "kb-due", "kb-tre", "kb-quattro"},
			Providers: []providerStatus{{Name: "claude", Connected: true, BoundKBs: []string{"kb-uno"}}},
		}
		for _, line := range strings.Split(ansiRE.ReplaceAllString(m.View(), ""), "\n") {
			if got := utf8.RuneCountInString(line); got > width {
				t.Errorf("width %d overflow %d: %q", width, got, line)
			}
		}
	}
}

// --- D175 WP4: actions consistent with the bindings ------------------------

// TestUpdate_SyncAllIsSequentialAndNamesTheProviderInFlight
func TestUpdate_SyncAllIsSequentialAndNamesTheProviderInFlight(t *testing.T) {
	m := testModel()
	m.rows[0].Connected = true // both rows connected

	next, cmd := m.Update(keyMsg("S"))
	m = next.(Model)
	if !m.syncingAll || cmd == nil {
		t.Fatal("S must start a sync-all run")
	}
	if !strings.Contains(m.message, "one at a time") {
		t.Errorf("the message must say the run is sequential, got %q", m.message)
	}

	next, cmd = m.Update(syncProgressMsg{provider: "opencode"})
	m = next.(Model)
	if m.message != "syncing opencode…" {
		t.Errorf("progress message = %q, want the provider in flight", m.message)
	}
	if cmd == nil {
		t.Error("the progress listener must re-arm itself")
	}

	// The outcome must survive a progress message still in flight behind it.
	next, _ = m.Update(syncAllDoneMsg{done: []string{"claude", "opencode"}})
	m = next.(Model)
	if m.syncingAll {
		t.Error("syncingAll must be cleared by the outcome")
	}
	outcome := m.message
	next, cmd = m.Update(syncProgressMsg{provider: "opencode"})
	if got := next.(Model).message; got != outcome {
		t.Errorf("a late progress message overwrote the outcome: %q", got)
	}
	if cmd != nil {
		t.Error("a late progress message must not re-arm the listener")
	}
}

// TestWaitForSyncProgressCmd: one announcement per call, nil once the run has
// closed the channel.
func TestWaitForSyncProgressCmd(t *testing.T) {
	ch := make(chan string, 2)
	ch <- "claude"
	close(ch)

	if msg := waitForSyncProgressCmd(ch)(); msg != (syncProgressMsg{provider: "claude"}) {
		t.Errorf("first read = %#v, want the announced provider", msg)
	}
	if msg := waitForSyncProgressCmd(ch)(); msg != nil {
		t.Errorf("a closed channel must end the chain, got %#v", msg)
	}
}

// TestSyncAllCmdAnnouncesEveryProviderInOrder: sequential, and reported as
// such. The sync itself fails here (no config in the temp dir) — the point is
// that the announcement precedes the work and the run is ordered.
func TestSyncAllCmdAnnouncesEveryProviderInOrder(t *testing.T) {
	dir := t.TempDir()
	progress := make(chan string, 3)
	msg := syncAllCmd([]string{"claude", "codex", "kiro"}, dir, progress)()

	if _, ok := msg.(syncAllDoneMsg); !ok {
		t.Fatalf("syncAllCmd returned %#v, want syncAllDoneMsg", msg)
	}
	var got []string
	for p := range progress { // closed by syncAllCmd: this must terminate
		got = append(got, p)
	}
	if len(got) == 0 || got[0] != "claude" {
		t.Errorf("announcements = %v, want them to start at the first provider", got)
	}
}

// TestUpdate_EnterSyncsOnlyTheSelectedProvider: the selection is what gets
// synced, and no sync-all state is entered.
func TestUpdate_EnterSyncsOnlyTheSelectedProvider(t *testing.T) {
	m := testModel()
	m.cursor = 1 // OpenCode, connected

	next, cmd := m.Update(keyMsg("s"))
	m = next.(Model)
	if cmd == nil || !strings.Contains(m.message, "opencode") {
		t.Errorf("s must sync the selected provider, message = %q", m.message)
	}
	if m.syncingAll {
		t.Error("a single-provider sync must not enter the sync-all run")
	}
}

// TestDisconnectPromptNamesTheKBs: the confirmation must say what leaves this
// machine, which is the one thing it exists to say.
func TestDisconnectPromptNamesTheKBs(t *testing.T) {
	if got := disconnectPrompt("opencode", []string{"kb-uno", "kb-due"}); !strings.Contains(got, "kb-uno, kb-due") {
		t.Errorf("prompt must name the bound KBs, got %q", got)
	}
	if got := disconnectPrompt("opencode", nil); !strings.Contains(got, "bound to no KB") {
		t.Errorf("a provider bound to nothing must be said so, got %q", got)
	}
}

// TestUpdate_ConfirmDisconnectCapturesTheBinding
func TestUpdate_ConfirmDisconnectCapturesTheBinding(t *testing.T) {
	m := testModel()
	m.cursor = 1
	m.rows[1].BoundKBs = []string{"kb-uno", "kb-due"}

	next, _ := m.Update(keyMsg("d"))
	m = next.(Model)
	if m.screen != screenConfirmDisconnect {
		t.Fatalf("screen = %v, want the confirmation", m.screen)
	}
	out := ansiRE.ReplaceAllString(m.viewConfirmDisconnect(), "")
	if !strings.Contains(out, "kb-uno") || !strings.Contains(out, "kb-due") {
		t.Errorf("the confirmation must name the provider's KBs:\n%s", out)
	}
}

// TestFooterDistinguishesTheTwoSyncKeys: s and S do different things and the
// footer has to say which.
func TestFooterDistinguishesTheTwoSyncKeys(t *testing.T) {
	m := testModel()
	m.cursor = 1 // connected: the footer shows the sync keys
	out := ansiRE.ReplaceAllString(m.View(), "")
	if !strings.Contains(out, "sync selected") || !strings.Contains(out, "sync all") {
		t.Errorf("footer must distinguish the two sync keys:\n%s", out)
	}
}
