package star

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
)

// fakeKB records what kb.call hands the adapter.
type fakeKB struct {
	ro      map[string]bool
	reply   map[string]string
	calls   []string
	args    []map[string]any
	askedRO atomic.Int32
}

func (f *fakeKB) ReadOnly(_ context.Context, tool string) (bool, error) {
	f.askedRO.Add(1)
	return f.ro[tool], nil
}

func (f *fakeKB) Call(_ context.Context, tool string, args map[string]any) (string, error) {
	f.calls = append(f.calls, tool)
	f.args = append(f.args, args)
	r, ok := f.reply[tool]
	if !ok {
		return "", errors.New("tool failed:\nbad line")
	}
	return r, nil
}

func newFakeKB() *fakeKB {
	return &fakeKB{
		ro:    map[string]bool{"concept_read": true, "search": true, "concept_write": false},
		reply: map[string]string{"concept_read": `{"title":"T","n":3}`, "search": `plain text`, "concept_write": `"done"`},
	}
}

func kbCallWith(t *testing.T, kb KBCaller, mode string, readOnly bool, body string) (Result, error) {
	t.Helper()
	src := "def go(a):\n    " + strings.ReplaceAll(strings.TrimSpace(body), "\n", "\n    ") + "\ntool(\"go\", go, \"go\")\n"
	r := toolReq("go", `{}`)
	r.Permissions = &core.Permissions{KB: mode}
	r.ReadOnly = readOnly
	r.Host.KB = kb
	return mustLoad(t, src).call(context.Background(), r, callOpts{})
}

func TestKBCallReadGate(t *testing.T) {
	f := newFakeKB()
	res, err := kbCallWith(t, f, "read", false, `return kb.call("concept_read", kb="kb-a", path="x", limit=2, deep=True, tags=["a"])`)
	if err != nil {
		t.Fatal(err)
	}
	if got := decode(t, res); got["title"] != "T" || got["n"].(float64) != 3 {
		t.Errorf("result %v", got)
	}
	want := map[string]any{"kb": "kb-a", "path": "x", "limit": float64(2), "deep": true, "tags": []any{"a"}}
	if !reflect.DeepEqual(f.args[0], want) {
		t.Errorf("args %v, want %v", f.args[0], want)
	}
	// Plain text stays a string; a positional dict is the arguments.
	res, err = kbCallWith(t, f, "read", false, `return kb.call("search", {"kb": "kb-a", "q": "x"})`)
	if err != nil || res.Text != "plain text" || !reflect.DeepEqual(f.args[1], map[string]any{"kb": "kb-a", "q": "x"}) {
		t.Errorf("text %q, args %v, %v", res.Text, f.args[1], err)
	}
}

// A read manifest cannot reach a write tool whatever shape the name takes.
func TestKBCallReadRefusesWriteAndUnknown(t *testing.T) {
	for name, body := range map[string]string{
		"unannotated": `kb.call("concept_write", kb="kb-a")`,
		"unknown":     `kb.call("no_such_tool")`,
		"variable":    "t = \"concept\" + \"_write\"\nkb.call(t)",
		"dict key":    "d = {\"concept_write\": 1}\nfor k in d:\n    kb.call(k, kb=\"kb-a\")",
		"case":        `kb.call("Concept_Read")`,
		"padded":      `kb.call("concept_read ")`,
		"dict arg":    `kb.call("concept_write", {"kb": "kb-a"})`,
	} {
		f := newFakeKB()
		_, err := kbCallWith(t, f, "read", false, body)
		callErr(t, err, Denied)
		if len(f.calls) != 0 {
			t.Errorf("%s: the adapter was called: %v", name, f.calls)
		}
	}
}

func TestKBCallWriteAndReadOnlySession(t *testing.T) {
	f := newFakeKB()
	res, err := kbCallWith(t, f, "write", false, `return kb.call("concept_write", kb="kb-a")`)
	if err != nil || res.Text != "done" || f.askedRO.Load() != 0 {
		t.Fatalf("write: %q, %v, asked %d", res.Text, err, f.askedRO.Load())
	}
	// A read-only session is read whatever the manifest says.
	f = newFakeKB()
	_, err = kbCallWith(t, f, "write", true, `kb.call("concept_write", kb="kb-a")`)
	if ce := callErr(t, err, Denied); !strings.Contains(ce.Msg, "read-only") {
		t.Errorf("message %q", ce.Msg)
	}
	if len(f.calls) != 0 {
		t.Error("a write reached the adapter in a read-only session")
	}
	if _, err := kbCallWith(t, f, "write", true, `return kb.call("search")`); err != nil {
		t.Errorf("a read tool in a read-only session: %v", err)
	}
}

func TestKBCallReadOnlyForAToolRegisteredWritesFalse(t *testing.T) {
	f := newFakeKB()
	src := "def go(a):\n    return kb.call(\"concept_write\", kb=\"kb-a\")\ntool(\"go\", go, \"go\")\n"
	r := toolReq("go", `{}`)
	r.Permissions = &core.Permissions{KB: "write"}
	r.KBReadOnly = true
	r.Host.KB = f
	_, err := mustLoad(t, src).call(context.Background(), r, callOpts{})
	if ce := callErr(t, err, Denied); !strings.Contains(ce.Msg, "writes=False") {
		t.Errorf("message %q", ce.Msg)
	}
	if len(f.calls) != 0 {
		t.Error("a write reached the adapter from a writes=False tool")
	}
}

func TestKBCallAbsentAndErrors(t *testing.T) {
	f := newFakeKB()
	_, err := kbCallWith(t, f, "", false, `kb.call("search")`)
	if ce := callErr(t, err, Denied); !strings.Contains(ce.Msg, "permissions.kb") {
		t.Errorf("message %q", ce.Msg)
	}
	if len(f.calls) != 0 {
		t.Error("the adapter was called without a grant")
	}
	// A server error is an escaped error carrying the server's message.
	_, err = kbCallWith(t, f, "write", false, `kb.call("other")`)
	if ce := callErr(t, err, Failed); !strings.Contains(ce.Msg, `tool failed:\nbad line`) || strings.Contains(ce.Msg, "\n") {
		t.Errorf("message %q", ce.Msg)
	}
	_, err = kbCallWith(t, nil, "write", false, `kb.call("search")`)
	callErr(t, err, Failed)
	for _, body := range []string{`kb.call()`, `kb.call(1)`, `kb.call("search", 3)`, `kb.call("search", {1: 2})`, `kb.call("search", 1, 2)`} {
		if _, err := kbCallWith(t, newFakeKB(), "read", false, body); err == nil {
			t.Errorf("%s: no error", body)
		}
	}
}

func TestKBCallDuplicateArgument(t *testing.T) {
	f := newFakeKB()
	_, err := kbCallWith(t, f, "read", false, `kb.call("search", {"q": "a"}, q="b")`)
	if err == nil || !strings.Contains(err.Error(), "given twice") || len(f.calls) != 0 {
		t.Errorf("err %v, calls %v", err, f.calls)
	}
}

func TestKBCallResultCap(t *testing.T) {
	f := newFakeKB()
	f.reply["search"] = strings.Repeat("x", MaxResultBytes+1)
	_, err := kbCallWith(t, f, "read", false, `kb.call("search")`)
	callErr(t, err, LimitExceeded)
	f.reply["search"] = strings.Repeat("x", MaxResultBytes)
	if _, err := kbCallWith(t, f, "read", false, `kb.call("search")`); err != nil {
		t.Errorf("a result at the cap: %v", err)
	}
}

func TestKBCallNotWhileLoading(t *testing.T) {
	_, err := Load(entry("kb.call(\"search\")\n"))
	if err == nil || !strings.Contains(err.Error(), "kb.call is not available while plugin.star loads") {
		t.Errorf("load: %v", err)
	}
}

// fakeTools is the ToolClient under MCPKB.
type fakeTools struct {
	tools    []client.ToolInfo
	listErr  error
	lists    int
	calls    int
	callCtxs []context.Context
}

func (f *fakeTools) ListToolsContext(ctx context.Context) ([]client.ToolInfo, error) {
	f.lists++
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.tools, nil
}

func (f *fakeTools) CallContext(ctx context.Context, tool string, args any) (json.RawMessage, error) {
	f.calls++
	f.callCtxs = append(f.callCtxs, ctx)
	return json.RawMessage(`{"ok":true}`), nil
}

func TestMCPKBFetchesOnceAndDoesNotCacheErrors(t *testing.T) {
	ft := &fakeTools{listErr: errors.New("down"), tools: []client.ToolInfo{{Name: "search", ReadOnly: true}, {Name: "w"}}}
	kb := NewMCPKB(ft)
	ctx := context.Background()
	if _, err := kb.ReadOnly(ctx, "search"); err == nil {
		t.Fatal("a failed list answered")
	}
	ft.listErr = nil
	for tool, want := range map[string]bool{"search": true, "w": false} {
		if got, err := kb.ReadOnly(ctx, tool); err != nil || got != want {
			t.Errorf("ReadOnly(%q) = %v, %v", tool, got, err)
		}
	}
	if ft.lists != 2 { // the failure, then one good fetch
		t.Errorf("tools/list fetched %d times", ft.lists)
	}
}

func TestMCPKBRefreshesOnTTLAndNewTool(t *testing.T) {
	ft := &fakeTools{tools: []client.ToolInfo{{Name: "old", ReadOnly: true}}}
	kb := NewMCPKB(ft)
	now := time.Unix(1000, 0)
	kb.now = func() time.Time { return now }
	ctx := context.Background()
	if ro, _ := kb.ReadOnly(ctx, "old"); !ro || ft.lists != 1 {
		t.Fatalf("first: lists %d", ft.lists)
	}
	// An unknown name right after a fetch does not refetch.
	if ro, _ := kb.ReadOnly(ctx, "new"); ro || ft.lists != 1 {
		t.Fatalf("unknown at once: ro %v lists %d", ro, ft.lists)
	}
	// Later, a tool added since is found without waiting for the TTL.
	ft.tools = []client.ToolInfo{{Name: "old", ReadOnly: true}, {Name: "new", ReadOnly: true}}
	now = now.Add(toolsMinRefresh)
	if ro, _ := kb.ReadOnly(ctx, "new"); !ro || ft.lists != 2 {
		t.Fatalf("new tool: lists %d", ft.lists)
	}
	// A tool reclassified as a write is picked up after the TTL, not before.
	ft.tools = []client.ToolInfo{{Name: "old"}, {Name: "new", ReadOnly: true}}
	now = now.Add(toolsTTL / 2)
	if ro, _ := kb.ReadOnly(ctx, "old"); !ro || ft.lists != 2 {
		t.Fatalf("inside the TTL: ro %v lists %d", ro, ft.lists)
	}
	now = now.Add(toolsTTL)
	if ro, _ := kb.ReadOnly(ctx, "old"); ro || ft.lists != 3 {
		t.Fatalf("after the TTL: ro %v lists %d", ro, ft.lists)
	}
}

func TestMCPKBPassesContext(t *testing.T) {
	ft := &fakeTools{}
	kb := NewMCPKB(ft)
	type k struct{}
	ctx := context.WithValue(context.Background(), k{}, 1)
	if _, err := kb.Call(ctx, "t", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if ft.callCtxs[0].Value(k{}) != 1 {
		t.Error("the call's context did not reach the client")
	}
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := kb.ReadOnly(cctx, "t"); err == nil || ft.lists != 0 {
		t.Errorf("a cancelled context fetched: %v, %d", err, ft.lists)
	}
}

// The adapter over a real client: the annotation decides, the call reaches the
// server with the exact arguments, a cancelled context sends nothing.
func TestMCPKBOverClient(t *testing.T) {
	var lists, calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var res any
		switch req.Method {
		case "tools/list":
			lists.Add(1)
			res = map[string]any{"tools": []map[string]any{
				{"name": "search", "annotations": map[string]any{"readOnlyHint": true}},
				{"name": "concept_write"},
			}}
		case "tools/call":
			calls.Add(1)
			res = map[string]any{"content": []map[string]any{{"type": "text", "text": `{"got":"` + req.Params.Name + `","q":"` + req.Params.Arguments["q"].(string) + `"}`}}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": res})
	}))
	defer srv.Close()
	kb := NewMCPKB(client.New(srv.URL, ""))
	res, err := kbCallWith(t, kb, "read", false, `return kb.call("search", q="hi")`)
	if err != nil {
		t.Fatal(err)
	}
	if got := decode(t, res); got["got"] != "search" || got["q"] != "hi" {
		t.Errorf("result %v", got)
	}
	_, err = kbCallWith(t, kb, "read", false, `kb.call("concept_write", q="x")`)
	callErr(t, err, Denied)
	if calls.Load() != 1 {
		t.Errorf("calls %d", calls.Load())
	}
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := kb.Call(cctx, "search", map[string]any{"q": "x"}); err == nil || calls.Load() != 1 {
		t.Errorf("a cancelled call: %v, calls %d", err, calls.Load())
	}
}
