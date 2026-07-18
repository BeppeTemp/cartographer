package main

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestConnectFormModel_Prefill(t *testing.T) {
	prefill := connectOptions{ServerURL: "https://wiki.example.com/mcp", Name: "kb", Auth: true, TokenEnv: "MY_TOKEN", Trust: true}
	m := newConnectFormModel("Connect claude", prefill, false)

	if m.focus != fieldServerURL {
		t.Fatalf("focus = %v, want fieldServerURL", m.focus)
	}
	if !m.url.Focused() {
		t.Error("url field should be focused on open")
	}
	if got := m.url.Value(); got != prefill.ServerURL {
		t.Errorf("url = %q, want %q", got, prefill.ServerURL)
	}
	if got := m.serverName; got != prefill.Name {
		t.Errorf("serverName = %q, want %q (carried through, not a form field)", got, prefill.Name)
	}
	if got := m.tokenEnv.Value(); got != prefill.TokenEnv {
		t.Errorf("tokenEnv = %q, want %q", got, prefill.TokenEnv)
	}
	if !m.auth {
		t.Error("auth = false, want true (from prefill)")
	}
	if !m.trust {
		t.Error("trust = false, want true (from prefill)")
	}
}

func TestConnectFormModel_TabCyclesFocus(t *testing.T) {
	m := newConnectFormModel("Connect claude", connectOptions{}, false)

	order := []connectField{fieldTokenEnv, fieldAuth, fieldTrust, fieldSubmit, fieldServerURL}
	for i, want := range order {
		tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
		m = tm.(connectFormModel)
		if m.focus != want {
			t.Fatalf("tab #%d: focus = %v, want %v", i+1, m.focus, want)
		}
	}
}

func TestConnectFormModel_TypeIntoFocusedField(t *testing.T) {
	m := newConnectFormModel("Connect claude", connectOptions{}, false)

	for _, r := range "http://host:9090/mcp" {
		tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = tm.(connectFormModel)
	}
	if got := m.url.Value(); got != "http://host:9090/mcp" {
		t.Errorf("url = %q, want %q", got, "http://host:9090/mcp")
	}
}

func TestConnectFormModel_ToggleAuthWithSpace(t *testing.T) {
	m := newConnectFormModel("Connect claude", connectOptions{}, false)
	m.setFormFocus(fieldAuth)

	tm, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = tm.(connectFormModel)
	if !m.auth {
		t.Fatal("expected auth = true after space on fieldAuth")
	}

	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = tm.(connectFormModel)
	if m.auth {
		t.Fatal("expected auth = false after toggling twice")
	}
}

func TestConnectFormModel_ToggleTrustWithSpace(t *testing.T) {
	m := newConnectFormModel("Connect claude", connectOptions{Trust: true}, false)
	m.setFormFocus(fieldTrust)

	tm, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = tm.(connectFormModel)
	if m.trust {
		t.Fatal("expected trust = false after space on fieldTrust")
	}

	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = tm.(connectFormModel)
	if !m.trust {
		t.Fatal("expected trust = true after toggling twice")
	}
}

func TestConnectFormModel_SubmitOnEnterAtSubmitField(t *testing.T) {
	prefill := connectOptions{ServerURL: "http://host/mcp", Name: "kb", Auth: true, TokenEnv: "TOK", Trust: true}
	m := newConnectFormModel("Connect claude", prefill, false)
	m.setFormFocus(fieldSubmit)

	tm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = tm.(connectFormModel)

	if !m.Submitted() {
		t.Fatal("expected Submitted() = true after enter on fieldSubmit")
	}
	if m.Cancelled() {
		t.Error("expected Cancelled() = false")
	}
	// Embedded (standalone=false): must not quit the parent program.
	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Error("embedded form must not issue tea.Quit on submit")
		}
	}

	got := m.Values()
	if !reflect.DeepEqual(got, prefill) {
		t.Errorf("Values() = %+v, want %+v", got, prefill)
	}
}

func TestConnectFormModel_ValuesDefaultsBlankFields(t *testing.T) {
	m := newConnectFormModel("Connect claude", connectOptions{}, false)

	got := m.Values()
	want := connectOptions{ServerURL: "http://localhost:8080/mcp", Name: "cartographer", TokenEnv: "CARTOGRAPHER_TOKENS"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Values() = %+v, want %+v", got, want)
	}
}

func TestConnectFormModel_EscCancels(t *testing.T) {
	m := newConnectFormModel("Connect claude", connectOptions{}, false)

	tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = tm.(connectFormModel)

	if !m.Cancelled() {
		t.Fatal("expected Cancelled() = true after esc")
	}
	if m.Submitted() {
		t.Error("expected Submitted() = false")
	}
}

func TestConnectFormModel_HintFollowsFocus(t *testing.T) {
	m := newConnectFormModel("Connect claude", connectOptions{Auth: true}, false)

	// Focus on Server URL: its hint is rendered, the token-env one is not.
	out := m.View()
	if !strings.Contains(out, "MCP endpoint") {
		t.Errorf("hint for Server URL missing with focus on it:\n%s", out)
	}
	if strings.Contains(out, "environment variable that holds the bearer token") {
		t.Errorf("token-env hint must not show while Server URL is focused:\n%s", out)
	}

	m.setFormFocus(fieldTokenEnv)
	out = m.View()
	if !strings.Contains(out, "environment variable that holds the bearer token") {
		t.Errorf("hint for Token env var missing with focus on it (auth on):\n%s", out)
	}
	if !strings.Contains(out, "never written to disk") {
		t.Errorf("token-env hint must clarify the token is never written to disk:\n%s", out)
	}

	m.setFormFocus(fieldSubmit)
	out = m.View()
	if strings.Contains(out, "MCP endpoint") || strings.Contains(out, "environment variable") {
		t.Errorf("no field hint should show while Submit is focused:\n%s", out)
	}
}

func TestConnectFormModel_TokenEnvHintWithAuthOff(t *testing.T) {
	m := newConnectFormModel("Connect claude", connectOptions{Auth: false}, false)
	m.setFormFocus(fieldTokenEnv)
	out := m.View()
	if !strings.Contains(out, "ignored while Auth is disabled") {
		t.Errorf("with auth off the token-env hint must say the field is unused:\n%s", out)
	}
}

func TestConnectFormModel_LabelIsTokenEnvVar(t *testing.T) {
	m := newConnectFormModel("Connect claude", connectOptions{}, false)
	out := m.View()
	if !strings.Contains(out, "Token env var") {
		t.Errorf("field label should read 'Token env var', got:\n%s", out)
	}
}

func TestConnectFormModel_WithErrorPreservesValuesAndRendersInline(t *testing.T) {
	prefill := connectOptions{ServerURL: "https://x/mcp", TokenEnv: "TOK", Auth: true, Trust: true}
	m := newConnectFormModel("Connect claude", prefill, false)
	m.submitted = true

	m = m.withError("server unreachable: dial tcp: connection refused", true)

	if m.Submitted() {
		t.Fatal("withError must reset submitted so the form can be re-submitted")
	}
	if !m.forceRetry {
		t.Error("forceRetry should carry through withError")
	}
	if got := m.url.Value(); got != "https://x/mcp" {
		t.Errorf("url value lost: %q", got)
	}
	out := m.View()
	if !strings.Contains(out, "server unreachable") {
		t.Errorf("inline error not rendered:\n%s", out)
	}
}

func TestConnectFormModel_EditClearsErrorAndForceRetry(t *testing.T) {
	m := newConnectFormModel("Connect claude", connectOptions{}, false)
	m = m.withError("boom", true)

	tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = tm.(connectFormModel)

	if m.errMsg != "" {
		t.Error("typing into a field must clear the inline error")
	}
	if m.forceRetry {
		t.Error("typing into a field must disarm forceRetry")
	}
}

func TestConnectFormModel_SubmitClearsStaleError(t *testing.T) {
	m := newConnectFormModel("Connect claude", connectOptions{}, false)
	m = m.withError("stale", false)
	m.setFormFocus(fieldSubmit)

	tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = tm.(connectFormModel)

	if !m.Submitted() {
		t.Fatal("expected Submitted() after enter on Submit")
	}
	if m.errMsg != "" {
		t.Error("submit must clear the stale inline error")
	}
}

func TestConnectFormModel_StandaloneQuitsOnSubmitAndCancel(t *testing.T) {
	m := newConnectFormModel("Connect claude", connectOptions{}, true)
	m.setFormFocus(fieldSubmit)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a tea.Quit cmd on submit for a standalone form")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("expected tea.QuitMsg, got %T", cmd())
	}

	m2 := newConnectFormModel("Connect claude", connectOptions{}, true)
	_, cmd2 := m2.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd2 == nil {
		t.Fatal("expected a tea.Quit cmd on esc for a standalone form")
	}
	if _, ok := cmd2().(tea.QuitMsg); !ok {
		t.Fatalf("expected tea.QuitMsg, got %T", cmd2())
	}
}
