package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/testtree"
)

const listerSecret = "lister-secret-4d2b"

// sourceCfg writes a worker config with one exec recipe whose lister is the test
// binary itself (see TestMain); mode is "ok" or "fail".
func sourceCfg(t *testing.T, mode, serverURL string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LISTER_VAR", listerSecret)
	if err := os.WriteFile(filepath.Join(dir, "feed.md"), []byte("An entry is a page."), 0o600); err != nil {
		t.Fatal(err)
	}
	body := `name: docs-night
server_url: ` + serverURL + `/mcp
token_env: WORKER_TOKEN
kbs: [kb-a, work-kb]
models:
  - name: cloud
    provider: anthropic
    model: some-model
    api_key_env: CLOUD_API_KEY
    budget: {daily_tokens: 1000}
schedule:
  window: {start: "23:00", end: "06:00", timezone: UTC}
tasks:
  - kind: poll
    model: cloud
recipes:
  my-feed:
    instructions_file: feed.md
    exec:
      command: ` + os.Args[0] + `
      args: ["--exec-helper", "` + mode + `"]
      env: {LISTER_VALUE: LISTER_VAR}
`
	p := filepath.Join(dir, "worker.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func runSource(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	stderr = withStderr(t, func() { stdout = withStdout(t, func() { code = cmdWorker(append([]string{"source"}, args...)) }) })
	return
}

func TestWorkerSourceDispatchAndUsage(t *testing.T) {
	if code, _, _ := runSource(t); code != 2 {
		t.Errorf("no subcommand: exit %d", code)
	}
	code, out, _ := runSource(t, "help")
	if code != 0 || !strings.Contains(out, "worker source test") {
		t.Errorf("help: %d %q", code, out)
	}
	if code, _, errs := runSource(t, "bogus"); code != 2 || !strings.Contains(errs, "bogus") {
		t.Errorf("unknown: %d %q", code, errs)
	}
	if code, _, _ := runSource(t, "test"); code != 2 {
		t.Errorf("test with no flag: exit %d", code)
	}
	if code := withExit(t, func() int { return cmdWorker(nil) }); code != 2 {
		t.Errorf("worker alone: exit %d", code)
	}
}

func withExit(t *testing.T, f func() int) (code int) {
	t.Helper()
	withStderr(t, func() { code = f() })
	return
}

func TestWorkerSourceFlagRules(t *testing.T) {
	cfg := sourceCfg(t, "ok", "http://127.0.0.1:1")
	for name, args := range map[string][]string{
		"both modes":      {"test", "--config", cfg, "--source", "s/x", "--recipe", "my-feed", "--locator", "l"},
		"neither":         {"test", "--config", cfg},
		"no locator":      {"test", "--config", cfg, "--recipe", "my-feed"},
		"kb with recipe":  {"test", "--config", cfg, "--recipe", "my-feed", "--locator", "l", "--kb", "kb-a"},
		"include source":  {"test", "--config", cfg, "--source", "s/x", "--include", "a"},
		"positional":      {"test", "--config", cfg, "--recipe", "my-feed", "--locator", "l", "extra"},
		"unknown recipe":  {"test", "--config", cfg, "--recipe", "ghost", "--locator", "l"},
		"missing config":  {"test", "--config", filepath.Join(t.TempDir(), "nope.yaml"), "--recipe", "my-feed", "--locator", "l"},
		"several kbs":     {"test", "--config", cfg, "--source", "s/x"},
		"kb not in file":  {"test", "--config", cfg, "--source", "s/x", "--kb", "other"},
		"no token in env": {"test", "--config", cfg, "--source", "s/x", "--kb", "kb-a"},
	} {
		t.Run(name, func(t *testing.T) {
			if code, _, errs := runSource(t, args...); code != 2 || errs == "" {
				t.Errorf("exit %d, stderr %q", code, errs)
			}
		})
	}
}

func TestWorkerSourceRecipeModeNeedsNoTokenAndNoModelKey(t *testing.T) {
	t.Setenv("WORKER_TOKEN", "")
	t.Setenv("CLOUD_API_KEY", "")
	cfg := sourceCfg(t, "ok", "http://127.0.0.1:1")
	code, out, errs := runSource(t, "test", "--config", cfg, "--recipe", "my-feed", "--locator", "wiki:space/ops", "--show", "a.md", "--cursor", "c0")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{"recipe:\tmy-feed", "source:\t(not registered)", "cursor_in:\tc0", "added\ta.md\tr1\t", "next_cursor:\tc1", "--- content of a.md ---"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out+errs, "--exec-helper") {
		t.Error("a command's arguments must not be printed")
	}
}

func TestWorkerSourceUnsetVariableIsNamedNotItsValue(t *testing.T) {
	cfg := sourceCfg(t, "ok", "http://127.0.0.1:1")
	t.Setenv("LISTER_VAR", "")
	code, _, errs := runSource(t, "test", "--config", cfg, "--recipe", "my-feed", "--locator", "l")
	if code != 2 || !strings.Contains(errs, "LISTER_VAR") {
		t.Errorf("exit %d: %q", code, errs)
	}
}

func TestWorkerSourceListFailureExits1(t *testing.T) {
	cfg := sourceCfg(t, "fail", "http://127.0.0.1:1")
	code, _, errs := runSource(t, "test", "--config", cfg, "--recipe", "my-feed", "--locator", "l")
	if code != 1 || !strings.Contains(errs, "list_failed") || !strings.Contains(errs, "lister broke") {
		t.Errorf("exit %d: %q", code, errs)
	}
}

func TestWorkerSourceNeverPrintsTheSecret(t *testing.T) {
	cfg := sourceCfg(t, "ok", "http://127.0.0.1:1")
	_, out, errs := runSource(t, "test", "--config", cfg, "--recipe", "my-feed", "--locator", "l")
	// The lister echoes its variable as content, but only --show prints content.
	if strings.Contains(out+errs, listerSecret) {
		t.Errorf("secret on the output:\n%s%s", out, errs)
	}
}

// --source against a server: a 401 names token_env, and a missing Source is class 2.
func TestWorkerSourceAgainstAServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Write([]byte(`{"status":"ok"}`))
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()
	t.Setenv("WORKER_TOKEN", "wrong")
	cfg := sourceCfg(t, "ok", srv.URL)
	code, _, errs := runSource(t, "test", "--config", cfg, "--source", "s/x", "--kb", "kb-a")
	if code != 2 || !strings.Contains(errs, "WORKER_TOKEN") {
		t.Errorf("exit %d: %q", code, errs)
	}
}

var durationRE = regexp.MustCompile(`, [0-9.]+(µs|ms|s)\n`)

func TestWorkerSourceGoldenOutput(t *testing.T) {
	cfg := sourceCfg(t, "ok", "http://127.0.0.1:1")
	code, out, errs := runSource(t, "test", "--config", cfg, "--recipe", "my-feed", "--locator", "wiki:space/ops", "--kind", "wiki")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	want := "recipe:\tmy-feed\nshape:\tfeed\nkind:\twiki\nsource:\t(not registered)\nlocator:\twiki:space/ops\ncursor_in:\t\nconfig:\tworker.yaml\n" +
		"added\ta.md\tr1\t18\tA\n" +
		"counts:\tadded 1, modified 0, removed 0\nnext_cursor:\tc1\nsummary:\t1 entries, 18 content bytes, ~5 tokens, 0s\n"
	got := durationRE.ReplaceAllString(out, ", 0s\n")
	if got != want {
		t.Errorf("stdout:\n%q\nwant:\n%q", got, want)
	}
	// The poll task is absent from this config's defaults note only when it has one: the config has a poll task.
	if strings.Contains(errs, "warning") {
		t.Errorf("unexpected warning: %q", errs)
	}
}

func TestWorkerSourceGoldenEmptyListing(t *testing.T) {
	cfg := sourceCfg(t, "empty", "http://127.0.0.1:1")
	code, out, _ := runSource(t, "test", "--config", cfg, "--recipe", "my-feed", "--locator", "l", "--cursor", "c9")
	want := "counts:\tadded 0, modified 0, removed 0\nnext_cursor:\tc9\nsummary:\t0 entries, 0 content bytes, ~0 tokens, 0s\n"
	if code != 0 || !strings.HasSuffix(durationRE.ReplaceAllString(out, ", 0s\n"), want) {
		t.Errorf("exit %d:\n%q", code, out)
	}
}

func TestWorkerSourceCapsTheTableAt50Rows(t *testing.T) {
	cfg := sourceCfg(t, "many", "http://127.0.0.1:1")
	code, out, _ := runSource(t, "test", "--config", cfg, "--recipe", "my-feed", "--locator", "l")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if n := strings.Count(out, "added\tk"); n != 50 || !strings.Contains(out, "(+10 more)\n") || strings.Contains(out, "k50") {
		t.Errorf("%d rows:\n%s", n, out)
	}
	if !strings.Contains(out, "counts:\tadded 60") || !strings.Contains(out, "summary:\t60 entries") {
		t.Errorf("counts must cover every entry:\n%s", out)
	}
}

func TestWorkerSourceShowIsCappedWithAMarker(t *testing.T) {
	cfg := sourceCfg(t, "ok", "http://127.0.0.1:1")
	t.Setenv("LISTER_VAR", strings.Repeat("x", 21<<10)) // 21 KiB of content; the helper's script cap is 128 KiB
	code, out, _ := runSource(t, "test", "--config", cfg, "--recipe", "my-feed", "--locator", "l", "--show", "a.md")
	if code != 0 || !strings.Contains(out, "[truncated at 20480 bytes]") || !strings.Contains(out, strings.Repeat("x", 20<<10)+"\n") || strings.Contains(out, strings.Repeat("x", 20<<10+1)) {
		t.Errorf("exit %d: content not capped at 20 KiB", code)
	}
	code, _, errs := runSource(t, "test", "--config", cfg, "--recipe", "my-feed", "--locator", "l", "--show", "nope")
	if code != 2 || !strings.Contains(errs, "nope") {
		t.Errorf("a --show key outside the listing: exit %d %q", code, errs)
	}
}

// mcpMock is a recording MCP server over HTTP: bearer-checked, it answers the
// read tools from pages and fails the test on any other tool.
type mcpMock struct {
	t      *testing.T
	token  string
	pages  map[string]map[string]any // Source and plan frontmatter by ID
	mu     sync.Mutex
	tools  []string
	failed bool
}

func (m *mcpMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/health" {
		w.Write([]byte(`{"status":"ok"}`))
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+m.token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"params"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	reply := func(result any) {
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}
	if req.Method != "tools/call" {
		reply(map[string]any{})
		return
	}
	m.mu.Lock()
	m.tools = append(m.tools, req.Params.Name)
	m.mu.Unlock()
	text := func(v any) {
		b, _ := json.Marshal(v)
		reply(map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}})
	}
	switch req.Params.Name {
	case "concept_read":
		id, _ := req.Params.Arguments["id"].(string)
		fm, ok := m.pages[id]
		if !ok {
			reply(map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": "not found"}}})
			return
		}
		text(map[string]any{"id": id, "content_hash": "h1", "frontmatter": fm, "body": ""})
	case "concept_list":
		var res []map[string]any
		for id, fm := range m.pages {
			if fm["type"] == "IngestPlan" {
				res = append(res, map[string]any{"id": id, "fields": map[string]any{"status": "open"}})
			}
		}
		text(map[string]any{"count": len(res), "results": res})
	default:
		m.failed = true
		m.t.Errorf("the dry run called %s", req.Params.Name)
		reply(map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": "refused"}}})
	}
}

func sourceMock(t *testing.T, withPlan bool) (*mcpMock, *httptest.Server) {
	t.Helper()
	m := &mcpMock{t: t, token: "tok-ok", pages: map[string]map[string]any{
		"sources/wiki": {"type": "Source", "source_shape": "feed", "recipe": "my-feed", "locator": "wiki:space/ops", "source_kind": "wiki", "feed_cursor": "stored-1"},
		"sources/doc":  {"type": "Source", "source_shape": "document", "locator": "https://example.com/a"},
		"notes/page":   {"type": "Concept"},
	}}
	if withPlan {
		m.pages["ingest/plan"] = map[string]any{"type": "IngestPlan", "sources": []string{"sources/wiki"}}
	}
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	return m, srv
}

func TestWorkerSourceAgainstAMockServerReadsOnlyAndWritesNothing(t *testing.T) {
	m, srv := sourceMock(t, true)
	t.Setenv("WORKER_TOKEN", m.token)
	cfg := sourceCfg(t, "ok", srv.URL)
	t.Setenv("WORKER_TOKEN", m.token)
	dir := filepath.Dir(cfg)
	before, err := testtree.Capture(dir)
	if err != nil {
		t.Fatal(err)
	}
	code, out, errs := runSource(t, "test", "--config", cfg, "--source", "sources/wiki", "--kb", "kb-a")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{"source:\tsources/wiki", "cursor_in:\tstored-1", "kind:\twiki", "next_cursor:\tc1"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(errs, "no_ingest_plan") {
		t.Errorf("the plan lists the Source: %q", errs)
	}
	for _, tool := range m.tools {
		if tool != "concept_read" && tool != "concept_list" {
			t.Errorf("tool %s is not a read tool", tool)
		}
	}
	if len(m.tools) == 0 || m.failed {
		t.Errorf("tools called: %v", m.tools)
	}
	after, err := testtree.Capture(dir)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Errorf("the config directory changed (%v)", err)
	}
}

func TestWorkerSourceWarnsWhenNoPlanListsTheSource(t *testing.T) {
	m, srv := sourceMock(t, false)
	cfg := sourceCfg(t, "ok", srv.URL)
	t.Setenv("WORKER_TOKEN", m.token)
	code, _, errs := runSource(t, "test", "--config", cfg, "--source", "sources/wiki", "--kb", "kb-a")
	if code != 0 || !strings.Contains(errs, "warning: no_ingest_plan") {
		t.Errorf("exit %d: %q", code, errs)
	}
}

func TestWorkerSourceSourcesThatAreNotFeeds(t *testing.T) {
	m, srv := sourceMock(t, true)
	cfg := sourceCfg(t, "ok", srv.URL)
	t.Setenv("WORKER_TOKEN", m.token)
	for _, id := range []string{"sources/doc", "notes/page", "sources/missing"} {
		if code, _, errs := runSource(t, "test", "--config", cfg, "--source", id, "--kb", "kb-a"); code != 2 || errs == "" {
			t.Errorf("%s: exit %d %q", id, code, errs)
		}
	}
	// A feed whose recipe the worker does not know is what the poll records: class 1.
	m.pages["sources/wiki"]["recipe"] = "ghost"
	code, _, errs := runSource(t, "test", "--config", cfg, "--source", "sources/wiki", "--kb", "kb-a")
	if code != 1 || !strings.Contains(errs, "recipe_unknown") {
		t.Errorf("exit %d: %q", code, errs)
	}
	// An invalid field is a poll refusal too (bad_field).
	m.pages["sources/wiki"]["recipe"] = "my-feed"
	m.pages["sources/wiki"]["feed_interval"] = "1m"
	code, _, errs = runSource(t, "test", "--config", cfg, "--source", "sources/wiki", "--kb", "kb-a")
	if code != 1 || !strings.Contains(errs, "bad_field") {
		t.Errorf("exit %d: %q", code, errs)
	}
}
