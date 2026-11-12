package mcpserver

import (
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/audit"
	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/hooks"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

const vetoWriteArgs = `{"id":"test/veto","frontmatter":{"type":"Note","title":"Veto"},"body":"# V\n\n[[anchor]]\n"}`

type vetoHarness struct {
	k     *kb.KB
	s     *Server
	hits  atomic.Int32
	audit string

	mu       sync.Mutex
	payloads []map[string]any
}

// vetoServer wires a real KB server to a hook server answering with respond
// (nil: down, a closed port). mut adjusts the hook entry.
func vetoServer(t *testing.T, respond http.HandlerFunc, mut func(*hooks.Hook)) *vetoHarness {
	t.Helper()
	k, _ := setupGitKB(t)
	h := &vetoHarness{k: k}
	url := "http://127.0.0.1:1/veto"
	if respond != nil {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h.hits.Add(1)
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			h.mu.Lock()
			h.payloads = append(h.payloads, body)
			h.mu.Unlock()
			respond(w, r)
		}))
		t.Cleanup(srv.Close)
		url = srv.URL
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "acme.token"), []byte("tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hk := hooks.Hook{Name: "acme/review", Point: hooks.PointWriteVeto, Plugin: "acme", URL: url, Credential: "acme.token", FailurePolicy: hooks.FailClosed}
	if mut != nil {
		mut(&hk)
	}
	reg, err := hooks.NewRegistry([]hooks.Hook{hk}, hooks.Options{
		CredentialsDir: dir, Approved: func(string, string) bool { return true }, Logf: func(string, ...any) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.audit = filepath.Join(t.TempDir(), "audit.jsonl")
	l, err := audit.OpenWithOptions(h.audit, audit.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	h.s = New("test")
	RegisterKBTools(h.s, k, Deps{})
	h.s.SetAuditLog(l)
	h.s.SetKBName("kb-a")
	h.s.SetHooks(reg)
	return h
}

func answer(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }
}

func (h *vetoHarness) write(p auth.Principal) ToolResult {
	ctx := auth.ContextWithPrincipal(context.Background(), p)
	return h.s.callTool(ctx, "concept_write", json.RawMessage(vetoWriteArgs))
}

func (h *vetoHarness) head(t *testing.T) string {
	t.Helper()
	sha, err := gitx.HeadSHA(h.k.Root)
	if err != nil {
		t.Fatal(err)
	}
	return sha
}

func resultText(r ToolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		b.WriteString(c.Text)
	}
	return b.String()
}

func TestWriteVeto_DenyRefusesAndLeavesTheTreeUntouched(t *testing.T) {
	t.Parallel()
	h := vetoServer(t, answer(`{"version":1,"decision":"deny","reason":"frozen for the audit"}`), nil)
	before := h.head(t)
	// An admin is vetoed too: the shortcut lives in the authorizer, the veto sits outside it.
	res := h.write(botPrincipal)
	if !res.IsError || !strings.Contains(resultText(res), "forbidden: denied_by_hook") || !strings.Contains(resultText(res), "frozen for the audit") {
		t.Fatalf("result = %q", resultText(res))
	}
	if h.head(t) != before {
		t.Fatal("a vetoed write committed")
	}
	if _, err := os.Stat(filepath.Join(h.k.DataRoot(), "test", "veto.md")); err == nil {
		t.Fatal("a vetoed write left a file")
	}
	es := auditEntries(t, h.audit)
	if len(es) != 2 || es[1].Outcome != audit.OutcomeUnauthorized || es[1].Resources["hook"] != "acme/review" || es[1].Resources["hook_verdict"] != "deny" {
		t.Fatalf("audit = %+v", es)
	}
	// The payload: the principal, the KB, the tool, the class and the audit's resource ids.
	h.mu.Lock()
	p := h.payloads[0]
	h.mu.Unlock()
	pr, _ := p["principal"].(map[string]any)
	tgt, _ := p["target"].(map[string]any)
	if p["tool"] != "concept_write" || p["class"] != "write" || p["kb"] != "kb-a" || pr["id"] != "reviewer-bot" || tgt["id"] != "test/veto" {
		t.Fatalf("payload = %v", p)
	}
	if strings.Contains(vetoJSON(p), "# V") || strings.Contains(vetoJSON(p), "frontmatter") {
		t.Fatalf("the payload carries content: %v", p)
	}
}

func vetoJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestWriteVeto_NoOpinionWritesAsToday(t *testing.T) {
	t.Parallel()
	for _, body := range []string{`{"version":1,"decision":"no_opinion"}`, `{"version":1,"decision":"allow"}`} {
		h := vetoServer(t, answer(body), nil)
		before := h.head(t)
		if res := h.write(botPrincipal); res.IsError {
			t.Fatalf("%s: %q", body, resultText(res))
		}
		if h.head(t) == before || h.hits.Load() != 1 {
			t.Fatalf("%s: head unchanged or hook hits = %d", body, h.hits.Load())
		}
	}
}

func TestWriteVeto_ReadsAndCoreRefusalsNeverReachTheHook(t *testing.T) {
	t.Parallel()
	h := vetoServer(t, answer(`{"version":1,"decision":"deny"}`), nil)
	ctx := auth.ContextWithPrincipal(context.Background(), botPrincipal)
	if res := h.s.callTool(ctx, "concept_read", json.RawMessage(`{"id":"anchor"}`)); res.IsError {
		t.Fatalf("read: %q", resultText(res))
	}
	// A principal the core refuses (no policy, no authorizer) is denied before any hook.
	res := h.write(auth.Principal{ID: "nobody"})
	if !res.IsError || strings.Contains(resultText(res), "denied_by_hook") {
		t.Fatalf("core refusal = %q", resultText(res))
	}
	if n := h.hits.Load(); n != 0 {
		t.Fatalf("hook called %d times for a read and a core refusal", n)
	}
}

func TestWriteVeto_FailurePolicy(t *testing.T) {
	t.Parallel()
	h := vetoServer(t, nil, nil) // down, fail_closed
	before := h.head(t)
	res := h.write(botPrincipal)
	if !res.IsError || !strings.Contains(resultText(res), "hook_unavailable") || h.head(t) != before {
		t.Fatalf("fail_closed: %q", resultText(res))
	}
	if es := auditEntries(t, h.audit); es[1].Resources["hook_verdict"] != "error" {
		t.Fatalf("audit = %+v", es)
	}
	h = vetoServer(t, nil, func(hk *hooks.Hook) { hk.FailurePolicy = hooks.FailOpen })
	before = h.head(t)
	if res := h.write(botPrincipal); res.IsError || h.head(t) == before {
		t.Fatalf("fail_open: %q", resultText(res))
	}
	es := auditEntries(t, h.audit)
	if es[0].Resources["hook_verdict"] != "error" || es[0].Resources["hook"] != "acme/review" {
		t.Fatalf("fail_open audit = %+v", es)
	}
}

func TestWriteVeto_DryRunWritesAndRecords(t *testing.T) {
	t.Parallel()
	h := vetoServer(t, answer(`{"version":1,"decision":"deny","reason":"x"}`), func(hk *hooks.Hook) { hk.Mode = hooks.ModeDryRun })
	before := h.head(t)
	if res := h.write(botPrincipal); res.IsError || h.head(t) == before {
		t.Fatalf("dry run blocked: %q", resultText(res))
	}
	es := auditEntries(t, h.audit)
	if es[0].Resources["hook_verdict"] != "dry_run_deny" || es[0].Resources["hook"] != "acme/review" {
		t.Fatalf("audit = %+v", es)
	}
}

func TestWriteVeto_KBsNarrowing(t *testing.T) {
	t.Parallel()
	h := vetoServer(t, answer(`{"version":1,"decision":"deny"}`), func(hk *hooks.Hook) { hk.KBs = []string{"kb-other"} })
	if res := h.write(botPrincipal); res.IsError || h.hits.Load() != 0 {
		t.Fatalf("a hook narrowed to another KB ran: %q", resultText(res))
	}
}

// A routed view leaves the veto to the target KB's server, so a routed write
// calls the hook once.
func TestWriteVeto_RoutedViewDoesNotCallIt(t *testing.T) {
	t.Parallel()
	h := vetoServer(t, answer(`{"version":1,"decision":"deny"}`), nil)
	h.s.SetRefusalAuditLog(nil)
	if res := h.write(botPrincipal); res.IsError || h.hits.Load() != 0 {
		t.Fatalf("a refusals-only view called the hook: %q", resultText(res))
	}
}

// The lock rule: a slow hook delays its own request and no other writer.
func TestWriteVeto_SlowHookIsNotHeldUnderTheKBLock(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	var first atomic.Bool
	h := vetoServer(t, func(w http.ResponseWriter, _ *http.Request) {
		if first.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
		_, _ = w.Write([]byte(`{"version":1,"decision":"no_opinion"}`))
	}, func(hk *hooks.Hook) { hk.Timeout = 10 * time.Second })
	done := make(chan ToolResult, 1)
	go func() { done <- h.write(botPrincipal) }()
	<-entered
	lockTaken := make(chan struct{})
	go func() { h.k.WithGitLock(func() error { return nil }); close(lockTaken) }()
	select {
	case <-lockTaken:
	case <-time.After(5 * time.Second):
		t.Fatal("the KB lock is held while a hook is pending")
	}
	// A second writer completes while the first is still in its hook.
	ctx := auth.ContextWithPrincipal(context.Background(), botPrincipal)
	res := h.s.callTool(ctx, "concept_write", json.RawMessage(`{"id":"test/second","frontmatter":{"type":"Note","title":"S"},"body":"# S\n\n[[anchor]]\n"}`))
	if res.IsError {
		t.Fatalf("second writer: %q", resultText(res))
	}
	close(release)
	if res := <-done; res.IsError {
		t.Fatalf("first writer: %q", resultText(res))
	}
}

// Nothing under the KB lock may call a hook: the files that hold the lock do not
// import internal/hooks (AGENTS.md of this package).
func TestNoHookUnderTheLock(t *testing.T) {
	t.Parallel()
	for _, f := range []string{"gitwrap.go", "policy.go", "twokbmove.go"} {
		af, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range af.Imports {
			if p, _ := strconv.Unquote(im.Path.Value); strings.HasSuffix(p, "/internal/hooks") {
				t.Errorf("%s imports %s: a hook is never called under the KB lock", f, p)
			}
		}
	}
}

// A move into another KB is judged by the hooks that cover the target KB too.
func TestWriteVeto_TransferIsJudgedOnTheTargetKB(t *testing.T) {
	t.Parallel()
	h := vetoServer(t, answer(`{"version":1,"decision":"deny","reason":"target frozen"}`), func(hk *hooks.Hook) { hk.KBs = []string{"kb-b"} })
	ctx := auth.ContextWithPrincipal(context.Background(), botPrincipal)
	// A plain write in kb-a is not covered by a hook scoped to kb-b.
	if res := h.write(botPrincipal); res.IsError || h.hits.Load() != 0 {
		t.Fatalf("a kb-b hook ran for a kb-a write: %q", resultText(res))
	}
	for _, tc := range []struct{ tool, args string }{
		{"concept_transfer", `{"target_kb":"kb-b","moves":[{"id":"anchor"}]}`},
		{"artifact_move", `{"target_kb":"kb-b","paths":["skills/x"]}`},
	} {
		before := h.hits.Load()
		res := h.s.callTool(ctx, tc.tool, json.RawMessage(tc.args))
		if !res.IsError || !strings.Contains(resultText(res), "denied_by_hook") || !strings.Contains(resultText(res), "target frozen") {
			t.Fatalf("%s: %q", tc.tool, resultText(res))
		}
		if h.hits.Load() != before+1 {
			t.Fatalf("%s: hook hits %d, want %d", tc.tool, h.hits.Load(), before+1)
		}
	}
	h.mu.Lock()
	kbs := []any{h.payloads[0]["kb"], h.payloads[1]["kb"]}
	h.mu.Unlock()
	if kbs[0] != "kb-b" || kbs[1] != "kb-b" {
		t.Fatalf("payload kb = %v, want the target", kbs)
	}
}

func TestVetoKBs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		tool, args string
		want       []string
	}{
		{"concept_write", `{"target_kb":"kb-b"}`, []string{"kb-a"}},
		{"concept_transfer", `{"target_kb":"kb-b"}`, []string{"kb-a", "kb-b"}},
		{"artifact_move", `{"target_kb":"kb-a"}`, []string{"kb-a"}},
		{"artifact_move", `{}`, []string{"kb-a"}},
	} {
		if got := vetoKBs("kb-a", tc.tool, json.RawMessage(tc.args)); strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s %s: %v, want %v", tc.tool, tc.args, got, tc.want)
		}
	}
}

// Of several recorded verdicts the audit keeps the strongest, with its hook.
func TestWriteVeto_AuditKeepsTheStrongestVerdict(t *testing.T) {
	t.Parallel()
	h := vetoServer(t, answer(`{"version":1,"decision":"deny"}`), func(hk *hooks.Hook) { hk.Mode = hooks.ModeDryRun })
	first, _ := h.s.hooks.Hook("acme/review")
	second := hooks.Hook{Name: "acme/second", Point: hooks.PointWriteVeto, Plugin: "acme", URL: "http://127.0.0.1:1/x", Credential: "b.token", FailurePolicy: hooks.FailOpen}
	reg, err := hooks.NewRegistry([]hooks.Hook{first, second}, hooks.Options{
		CredentialsDir: dirWith(t, "acme.token", "b.token"), Approved: func(string, string) bool { return true }, Logf: func(string, ...any) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.s.SetHooks(reg)
	if res := h.write(botPrincipal); res.IsError {
		t.Fatalf("%q", resultText(res))
	}
	es := auditEntries(t, h.audit)
	if es[0].Resources["hook"] != "acme/review" || es[0].Resources["hook_verdict"] != "dry_run_deny" {
		t.Fatalf("audit = %+v, want the dry_run_deny of acme/review kept over acme/second's error", es[0].Resources)
	}
}

func dirWith(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("tok\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
