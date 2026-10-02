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
)

// nudgeKB is a KB with debt (two nonstandard_field warnings), no kb-doctor
// session ever, and the default interval, on a server whose clock tests move.
func nudgeKB(t *testing.T) (*Server, *time.Time) {
	t.Helper()
	k, s := repairKB(t, 2)
	k.DoctorIntervalDays = 14
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
	s, now := nudgeKB(t)
	first := s.callTool(authLocalContext(), "search", json.RawMessage(`{"query":"N0"}`))
	if !nudged(first) {
		t.Fatalf("no nudge on the first call: %+v", first.Content)
	}
	text := first.Content[len(first.Content)-1].Text
	if len(text) > 300 || !strings.Contains(text, "never") || !strings.Contains(text, "2 warnings") {
		t.Fatalf("notice = %q (%d bytes)", text, len(text))
	}
	if nudged(s.callTool(authLocalContext(), "search", json.RawMessage(`{"query":"N0"}`))) {
		t.Fatal("nudged twice inside the window")
	}
	*now = now.Add(doctorNudgeWindow)
	if !nudged(s.callTool(authLocalContext(), "search", json.RawMessage(`{"query":"N0"}`))) {
		t.Fatal("no nudge after the window")
	}
}

func TestDoctorNudgeSkips(t *testing.T) {
	s, _ := nudgeKB(t)
	name := kbName(s.kbRef)
	reader := asPrincipal(auth.Policy{Permissions: []auth.Permission{{KB: name}}})
	if res := s.callTool(reader, "search", json.RawMessage(`{"query":"N0"}`)); res.IsError || nudged(res) {
		t.Fatalf("a read-only caller was nudged or denied: %+v", res)
	}
	if res := s.callTool(authLocalContext(), "kb_status", json.RawMessage(`{}`)); len(res.Content) != 1 {
		t.Fatal("kb_status carried the nudge")
	}
	if res := s.callTool(authLocalContext(), "concept_read", json.RawMessage(`{"id":"ops/missing"}`)); !res.IsError || len(res.Content) != 1 {
		t.Fatalf("an error result carried the nudge: %+v", res)
	}
	cli := context.WithValue(authLocalContext(), callerClientKey{}, client.ClientName)
	if nudged(s.callTool(cli, "search", json.RawMessage(`{"query":"N0"}`))) {
		t.Fatal("the CLI was nudged: client.Call would return a JSON array")
	}
	// None of the above consumed the window.
	if !nudged(s.callTool(authLocalContext(), "search", json.RawMessage(`{"query":"N0"}`))) {
		t.Fatal("a skipped call consumed the window")
	}
}

func TestDoctorNudgeNotDueOrDisabled(t *testing.T) {
	s, _ := nudgeKB(t)
	if err := s.kbRef.AppendLog("kb-doctor: warnings 2 -> 2", time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if nudged(s.callTool(authLocalContext(), "search", json.RawMessage(`{"query":"N0"}`))) {
		t.Fatal("nudged two days after a kb-doctor session")
	}
	s2, _ := nudgeKB(t)
	s2.kbRef.DoctorIntervalDays = 0
	if nudged(s2.callTool(authLocalContext(), "search", json.RawMessage(`{"query":"N0"}`))) {
		t.Fatal("nudged with doctor_interval 0")
	}
}

func TestDoctorNudgeConcurrentCallsGetOne(t *testing.T) {
	s, _ := nudgeKB(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	count := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if nudged(s.callTool(authLocalContext(), "search", json.RawMessage(`{"query":"N0"}`))) {
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
