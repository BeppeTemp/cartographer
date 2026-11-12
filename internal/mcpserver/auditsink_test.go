package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/audit"
	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/hooks"
)

// blockingSinkServer wires a KB server whose audit log has one blocking
// audit_sink hook answering with respond (nil: down).
func blockingSinkServer(t *testing.T, policy string, respond http.HandlerFunc) (*Server, string, *audit.Log) {
	t.Helper()
	url := "http://127.0.0.1:1/sink"
	if respond != nil {
		srv := httptest.NewServer(respond)
		t.Cleanup(srv.Close)
		url = srv.URL
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "acme.token"), []byte("tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := hooks.NewRegistry([]hooks.Hook{{
		Name: "acme/siem", Point: hooks.PointAuditSink, Plugin: "acme", URL: url, Credential: "acme.token",
		FailurePolicy: policy, Blocking: true,
	}}, hooks.Options{CredentialsDir: dir, Approved: func(string, string) bool { return true }, Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	logDir := t.TempDir()
	path := filepath.Join(logDir, "audit.jsonl")
	l, err := audit.OpenWithOptions(path, audit.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	src := func(cursor string, limit int) ([]hooks.SinkEvent, bool, error) {
		es, gap, err := l.EventsAfter(cursor, limit)
		out := make([]hooks.SinkEvent, len(es))
		for i, e := range es {
			b, _ := json.Marshal(e)
			out[i] = hooks.SinkEvent{Hash: e.Hash, Entry: b}
		}
		return out, gap, err
	}
	sk, err := reg.NewSink("acme/siem", src, nil, logDir)
	if err != nil {
		t.Fatal(err)
	}
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	s.SetAuditLog(l)
	s.SetKBName("kb-a")
	s.SetHooks(reg)
	s.SetBlockingAuditSinks([]*hooks.Sink{sk})
	return s, path, l
}

func ackAll(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LastHash string `json:"last_hash"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	_ = json.NewEncoder(w).Encode(map[string]any{"version": 1, "ack": body.LastHash})
}

func callRead(s *Server) ToolResult {
	ctx := auth.ContextWithPrincipal(context.Background(), botPrincipal)
	return s.callTool(ctx, "kb_status", json.RawMessage(`{}`))
}

func TestBlockingAuditSink_FailClosedRejectsAndKeepsThePair(t *testing.T) {
	t.Parallel()
	s, path, _ := blockingSinkServer(t, hooks.FailClosed, nil)
	res := callRead(s)
	if !res.IsError || !strings.Contains(resultText(res), "audit sink acme/siem unavailable") {
		t.Fatalf("result = %q", resultText(res))
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"outcome":"audit_unavailable"`) || strings.Count(string(b), "\n") != 2 {
		t.Fatalf("want an attempt and an audit_unavailable completion:\n%s", b)
	}
	if _, err := audit.VerifyAudit(path, "", nil, audit.VerifyOptions{}); err != nil {
		t.Fatalf("the chain does not verify: %v", err)
	}
}

func TestBlockingAuditSink_FailOpenContinues(t *testing.T) {
	t.Parallel()
	s, _, _ := blockingSinkServer(t, hooks.FailOpen, nil)
	if res := callRead(s); res.IsError {
		t.Fatalf("fail_open: %q", resultText(res))
	}
}

func TestBlockingAuditSink_AcknowledgedCallRuns(t *testing.T) {
	t.Parallel()
	s, path, _ := blockingSinkServer(t, hooks.FailClosed, ackAll)
	if res := callRead(s); res.IsError {
		t.Fatalf("acknowledged: %q", resultText(res))
	}
	if _, err := audit.VerifyAudit(path, "", nil, audit.VerifyOptions{}); err != nil {
		t.Fatalf("the chain does not verify with the sink running: %v", err)
	}
}
