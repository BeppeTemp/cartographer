package mcpserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/audit"
	"github.com/BeppeTemp/cartographer/internal/auth"
)

func panicTool(name string) Tool {
	return Tool{
		Name:        name,
		Description: "test-only tool whose handler panics",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		ReadOnly:    true,
		Handler: func(requestContext, json.RawMessage) (ToolResult, error) {
			panic("boom")
		},
	}
}

func assertInternalError(t *testing.T, res ToolResult) {
	t.Helper()
	if !res.IsError || len(res.Content) == 0 || !strings.HasPrefix(res.Content[0].Text, "internal error") {
		t.Fatalf("want an internal error result, got %+v", res)
	}
}

// A handler panic is an error result on the stdio-equivalent HTTP path, and
// the next call on the same server works.
func TestHandlerPanicIsAnErrorResult(t *testing.T) {
	t.Parallel()
	s := newEraServer(t)
	s.RegisterTool(panicTool("kb_status")) // an authorized name, handler swapped
	h := openHandler(s)
	call := func(tool string) ToolResult {
		rr := postMCP(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+tool+`","arguments":{}}}`, nil)
		var resp Response
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v; %s", err, rr.Body.String())
		}
		return decodeToolResult(t, resp)
	}
	assertInternalError(t, call("kb_status"))
	if res := call("lint"); res.IsError && strings.HasPrefix(res.Content[0].Text, "internal error") {
		t.Fatalf("second call failed: %+v", res)
	}
	assertInternalError(t, call("kb_status"))
}

// The same on the routed mount, which goes through the per-KB callTool.
func TestHandlerPanicOnRoutedMount(t *testing.T) {
	t.Parallel()
	multi := NewMultiKBServer("test")
	k := setupNamedTestKB(t, "kb-a")
	multi.MountKB("kb-a", func(s *Server) {
		RegisterKBTools(s, k, Deps{})
		orig := s.tools["log_tail"]
		p := panicTool("log_tail")
		p.AcceptsKB = orig.AcceptsKB
		p.InputSchema = orig.InputSchema
		s.RegisterTool(p)
	})
	if err := multi.EnableRoutedMount("test", nil); err != nil {
		t.Fatal(err)
	}
	h := routedTokenStore("kb-a").Middleware(multi.Handler())
	assertInternalError(t, aliasCall(t, h, RoutedMountPath, "log_tail", map[string]any{"kb": "kb-a"}))
	res := aliasCall(t, h, RoutedMountPath, "lint", map[string]any{"kb": "kb-a"})
	if res.IsError && strings.HasPrefix(res.Content[0].Text, "internal error") {
		t.Fatalf("second call failed: %+v", res)
	}
}

// A panic in the doctor nudge, after the tool ran, is an error result too.
func TestDoctorNudgePanicIsAnErrorResult(t *testing.T) {
	t.Parallel()
	s, _ := nudgeKB(t)
	s.now = func() time.Time { panic("clock") }
	assertInternalError(t, s.callTool(provisionedLocal(), "search", json.RawMessage(`{"query":"N0"}`)))
}

// The audit of a call whose nudge panicked says internal_error, like the
// client's result, not success (D1516).
func TestDoctorNudgePanicIsAuditedAsInternalError(t *testing.T) {
	t.Parallel()
	s, _ := nudgeKB(t)
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	l, err := audit.OpenWithOptions(path, audit.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	s.SetAuditLog(l)
	s.now = func() time.Time { panic("clock") }
	assertInternalError(t, s.callTool(provisionedLocal(), "search", json.RawMessage(`{"query":"N0"}`)))
	entries := auditEntries(t, path)
	if len(entries) != 2 || entries[1].Phase != audit.PhaseCompletion || entries[1].Outcome != audit.OutcomeInternalErr {
		t.Fatalf("want attempt + completion/internal_error, got %+v", entries)
	}
}

// A panicking write tool leaves the KB lock free for the next write.
func TestWritePanicLeavesTheLockFree(t *testing.T) {
	t.Parallel()
	k, s := repairKB(t, 1)
	p := panicTool("snapshot")
	p.ReadOnly = false
	s.RegisterTool(gitWrap(k, p))
	ctx := auth.ContextWithPrincipal(context.Background(), auth.LocalAdminPrincipal())
	assertInternalError(t, s.callTool(ctx, "snapshot", json.RawMessage(`{}`)))
	done := make(chan struct{})
	go func() {
		k.WithGitLock(func() error { return nil })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the KB lock is still held after the panic")
	}
}
