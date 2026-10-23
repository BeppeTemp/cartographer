package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// nudgeKB is a KB with debt (two nonstandard_field findings), no kb-doctor
// session ever, and the default interval, on a server whose clock tests move.
func nudgeKB(t *testing.T) (*Server, *time.Time) {
	t.Helper()
	_, s := repairKB(t, 2)
	now := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	return s, &now
}

func nudged(res ToolResult) bool {
	for _, b := range res.Content[1:] {
		if strings.HasPrefix(b.Text, "cartographer: KB ") {
			return true
		}
	}
	return false
}

func asPrincipal(p auth.Policy) context.Context {
	return auth.ContextWithPrincipal(context.Background(), auth.Principal{ID: "u", Policy: p})
}

func TestDoctorNudgeOncePerWindow(t *testing.T) {
	t.Parallel()
	s, now := nudgeKB(t)
	first := s.callTool(provisionedLocal(), "search", json.RawMessage(`{"query":"N0"}`))
	if !nudged(first) {
		t.Fatalf("no nudge on the first call: %+v", first.Content)
	}
	text := first.Content[len(first.Content)-1].Text
	if len(text) > 380 || !strings.Contains(text, "never") || !strings.Contains(text, " lint findings, ") || strings.Contains(text, "warning") {
		t.Fatalf("notice = %q (%d bytes)", text, len(text))
	}
	if nudged(s.callTool(provisionedLocal(), "search", json.RawMessage(`{"query":"N0"}`))) {
		t.Fatal("nudged twice inside the window")
	}
	*now = now.Add(doctorNudgeWindow)
	if !nudged(s.callTool(provisionedLocal(), "search", json.RawMessage(`{"query":"N0"}`))) {
		t.Fatal("no nudge after the window")
	}
}

func TestDoctorNudgeSkips(t *testing.T) {
	t.Parallel()
	s, _ := nudgeKB(t)
	name := kbName(s.kbRef)
	reader := asPrincipal(auth.Policy{Permissions: []auth.Permission{{KB: name}}})
	if res := s.callTool(reader, "search", json.RawMessage(`{"query":"N0"}`)); res.IsError || nudged(res) {
		t.Fatalf("a read-only caller was nudged or denied: %+v", res)
	}
	if res := s.callTool(provisionedLocal(), "kb_status", json.RawMessage(`{}`)); len(res.Content) != 1 {
		t.Fatal("kb_status carried the nudge")
	}
	if res := s.callTool(provisionedLocal(), "concept_read", json.RawMessage(`{"id":"ops/missing"}`)); !res.IsError || len(res.Content) != 1 {
		t.Fatalf("an error result carried the nudge: %+v", res)
	}
	cli := context.WithValue(provisionedLocal(), callerClientKey{}, client.ClientName)
	if nudged(s.callTool(cli, "search", json.RawMessage(`{"query":"N0"}`))) {
		t.Fatal("the CLI was nudged: client.Call would return a JSON array")
	}
	// None of the above consumed the window.
	if !nudged(s.callTool(provisionedLocal(), "search", json.RawMessage(`{"query":"N0"}`))) {
		t.Fatal("a skipped call consumed the window")
	}
}

func TestDoctorNudgeNotDueOrDisabled(t *testing.T) {
	t.Parallel()
	s, _ := nudgeKB(t)
	if err := s.kbRef.AppendLog("kb-doctor: warnings 2 -> 2", time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if nudged(s.callTool(provisionedLocal(), "search", json.RawMessage(`{"query":"N0"}`))) {
		t.Fatal("nudged two days after a kb-doctor session")
	}
	// D907: the interval is the constant, 14 days after the last session.
	if kb.DoctorSuggestionDays != 14 {
		t.Fatalf("DoctorSuggestionDays = %d, want 14", kb.DoctorSuggestionDays)
	}
}

func TestDoctorNudgeConcurrentCallsGetOne(t *testing.T) {
	t.Parallel()
	s, _ := nudgeKB(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	count := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if nudged(s.callTool(provisionedLocal(), "search", json.RawMessage(`{"query":"N0"}`))) {
				mu.Lock()
				count++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if count != 1 {
		t.Fatalf("%d calls were nudged, want 1", count)
	}
}

func inSession(id string) context.Context {
	return context.WithValue(provisionedLocal(), callerSessionKey{}, id)
}

// D358: the window is per MCP session. Two sessions on one process each get a
// nudge, and one session gets one per window.
func TestDoctorNudgePerMCPSession(t *testing.T) {
	t.Parallel()
	s, now := nudgeKB(t)
	call := func(id string) ToolResult {
		return s.callTool(inSession(id), "search", json.RawMessage(`{"query":"N0"}`))
	}
	if !nudged(call("a")) {
		t.Fatal("session a: no nudge")
	}
	if !nudged(call("b")) {
		t.Fatal("session b was silenced by session a's nudge")
	}
	if nudged(call("a")) || nudged(call("b")) {
		t.Fatal("a session was nudged twice inside the window")
	}
	*now = now.Add(doctorNudgeWindow)
	if !nudged(call("a")) {
		t.Fatal("session a: no nudge after the window")
	}
	if len(s.nudgedAt) > 2 {
		t.Fatalf("nudgedAt keeps %d sessions", len(s.nudgedAt))
	}
}

// TestDoctorNudgeOnlyProposes: the nudge never tells the agent to start a
// session by itself (D704).
func TestDoctorNudgeOnlyProposes(t *testing.T) {
	t.Parallel()
	s, _ := nudgeKB(t)
	res := s.callTool(inSession("a"), "search", json.RawMessage(`{"query":"N0"}`))
	text := res.Content[len(res.Content)-1].Text
	if !strings.Contains(text, "Propose a cartographer-kb-doctor session to the operator") || !strings.Contains(text, "do not start it") {
		t.Fatalf("nudge text = %q", text)
	}
}

// provisionedLocal is a local caller whose connection declared ?provisioned=1
// (D1097): the only kind that may receive the nudge (D1101).
func provisionedLocal() context.Context { return withProvisioned(authLocalContext()) }

// D1101: a connection that did not declare itself provisioned is never nudged,
// and its call must not consume the window the provisioned clients of the same
// (stateless, one-bucket) server share.
func TestDoctorNudgeGenericConnectionNeitherNudgedNorClaiming(t *testing.T) {
	t.Parallel()
	s, _ := nudgeKB(t)
	args := json.RawMessage(`{"query":"N0"}`)
	if nudged(s.callTool(authLocalContext(), "search", args)) {
		t.Fatal("a generic connection was nudged")
	}
	if len(s.nudgedAt) != 0 {
		t.Fatal("a generic call claimed the nudge window")
	}
	if !nudged(s.callTool(provisionedLocal(), "search", args)) {
		t.Fatal("the generic call silenced the provisioned one")
	}
	// The Atlas (operator surface) is addressed too.
	s2, _ := nudgeKB(t)
	if !nudged(s2.callTool(withOperator(authLocalContext()), "search", args)) {
		t.Fatal("the operator surface was not nudged")
	}
}
