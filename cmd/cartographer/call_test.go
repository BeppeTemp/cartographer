package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/testtree"
)

// callServer stands for the routed endpoint: it records every request and
// answers with reply(method, params).
type callServer struct {
	mu     sync.Mutex
	reqs   []*http.Request
	bodies []map[string]any
}

func (s *callServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reqs)
}

// newCallEnv writes a client config pointing at a fake server and binds a
// workspace to kb-a. reply returns the JSON-RPC result for a request.
func newCallEnv(t *testing.T, reply func(method string, params map[string]any) any) (*callServer, string, string) {
	t.Helper()
	cs := &callServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		cs.mu.Lock()
		cs.reqs = append(cs.reqs, r)
		cs.bodies = append(cs.bodies, body)
		cs.mu.Unlock()
		if r.URL.Query().Get("access") == "read" {
			w.Header().Set("Cartographer-Access", "read")
		}
		params, _ := body["params"].(map[string]any)
		method, _ := body["method"].(string)
		out := map[string]any{"jsonrpc": "2.0", "id": body["id"]}
		switch v := reply(method, params).(type) {
		case rpcErr:
			out["error"] = map[string]any{"code": -32000, "message": string(v)}
		default:
			out["result"] = v
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)

	home := t.TempDir()
	setHome(t, home)
	ws := t.TempDir()
	cfg := clientconfig.Default()
	cfg.ServerURL = srv.URL + "/mcp"
	cfg.ServerRoutedPath = "/mcp/routed"
	cfg.Auth = true
	cfg.Token = "tok-123"
	cfg.KnownKBs = []string{"kb-a"}
	cfg.Agents = []string{"claude"}
	cfg.Identities = map[string]string{"kb-a": "User <user@example.com>"}
	if err := cfg.BindWorkspace("claude", ws, []string{"kb-a"}); err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv(cfg.TokenEnv(), "")
	return cs, home, ws
}

type rpcErr string

func textResult(s string, isErr bool) map[string]any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": s}}, "isError": isErr}
}

func runCall(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	old := mcpStdin
	mcpStdin = strings.NewReader(stdin)
	t.Cleanup(func() { mcpStdin = old })
	var code int
	var errOut string
	out := withStdout(t, func() { errOut = withStderr(t, func() { code = run(append([]string{"call"}, args...)) }) })
	return code, out, errOut
}

func TestCallTypedArgumentsAndRequestShape(t *testing.T) {
	cs, _, ws := newCallEnv(t, func(string, map[string]any) any { return textResult("hello", false) })
	code, out, errOut := runCall(t, "", "--provider", "claude", "--dir", ws,
		"concept_list", "kb=kb-a", "limit=5", "flag=true", `title="2026"`, "query=hello world", `ids=["a","b"]`, "empty=")
	if code != 0 || out != "hello\n" {
		t.Fatalf("exit %d stdout %q stderr %q", code, out, errOut)
	}
	if cs.count() != 1 {
		t.Fatalf("%d requests, want exactly one (no initialize)", cs.count())
	}
	r, body := cs.reqs[0], cs.bodies[0]
	if r.Header.Get("Authorization") != "Bearer tok-123" || r.URL.Path != "/mcp/routed" || r.URL.Query().Get("kbs") != "kb-a" {
		t.Errorf("request %s %s auth %q", r.Method, r.URL, r.Header.Get("Authorization"))
	}
	if r.Header.Get("Cartographer-Author") == "" || r.Header.Get("MCP-Protocol-Version") == "" || r.Header.Get("Mcp-Name") != "concept_list" {
		t.Errorf("headers: %v", r.Header)
	}
	params := body["params"].(map[string]any)
	want := map[string]any{"kb": "kb-a", "limit": 5.0, "flag": true, "title": "2026", "query": "hello world", "ids": []any{"a", "b"}, "empty": ""}
	if !reflect.DeepEqual(params["arguments"], want) {
		t.Errorf("arguments = %#v, want %#v", params["arguments"], want)
	}
	if _, ok := params["_meta"]; !ok || body["method"] != "tools/call" {
		t.Errorf("body = %v", body)
	}
}

func TestCallUnboundFolderIsReadOnly(t *testing.T) {
	cs, _, _ := newCallEnv(t, func(string, map[string]any) any { return textResult("ok", false) })
	other := t.TempDir()
	if code, _, e := runCall(t, "", "--provider", "claude", "--dir", other, "kb_status"); code != 0 {
		t.Fatalf("exit %d: %s", code, e)
	}
	if cs.count() != 1 || cs.reqs[0].URL.Query().Get("access") != "read" {
		t.Errorf("unbound request = %v", cs.reqs)
	}
}

func TestCallExitCodes(t *testing.T) {
	cases := []struct {
		name  string
		reply any
		args  []string
		code  int
		out   string
		err   string
	}{
		{"tool error", textResult("refused", true), []string{"x"}, 1, "", "refused"},
		{"rpc error", rpcErr("boom"), []string{"x"}, 2, "", "boom"},
		{"no content", map[string]any{"content": []any{}}, []string{"x"}, 2, "", "returned no content"},
		{"json output", textResult("hi", false), []string{"--output", "json", "x"}, 0, `"text": "hi"`, ""},
		{"no tool", nil, nil, 2, "", "usage"},
		{"two modes", nil, []string{"--list", "x"}, 2, "", "usage"},
		{"bare word", nil, []string{"x", "kb"}, 2, "", `"kb" is not key=value`},
		{"duplicate", nil, []string{"x", "a=1", "a=2"}, 2, "", "given twice"},
		{"json conflict", nil, []string{"x", "a=1", "--json", `{"a":2}`}, 2, "", "given twice"},
		{"json empty", nil, []string{"x", "--json", ""}, 2, "", "JSON object"},
		{"json invalid", nil, []string{"x", "--json", "{"}, 2, "", "JSON object"},
		{"json without tool", nil, []string{"--list", "--json", "{}"}, 2, "", "goes with a tool call"},
		{"json not object", nil, []string{"x", "--json", `[1]`}, 2, "", "JSON object"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, ws := newCallEnv(t, func(string, map[string]any) any { return c.reply })
			args := append([]string{"--provider", "claude", "--dir", ws}, c.args...)
			code, out, errOut := runCall(t, "", args...)
			if code != c.code || !strings.Contains(out, c.out) || !strings.Contains(errOut, c.err) {
				t.Errorf("exit %d stdout %q stderr %q; want %d, %q, %q", code, out, errOut, c.code, c.out, c.err)
			}
			if c.code != 0 && c.out == "" && out != "" {
				t.Errorf("stdout %q on a failure", out)
			}
		})
	}
}

func TestCallJSONFromStdinMergesWithPairs(t *testing.T) {
	cs, _, ws := newCallEnv(t, func(string, map[string]any) any { return textResult("ok", false) })
	code, _, e := runCall(t, `{"body":"multi\nline"}`, "--provider", "claude", "--dir", ws, "concept_write", "kb=kb-a", "--json", "-")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, e)
	}
	args := cs.bodies[0]["params"].(map[string]any)["arguments"].(map[string]any)
	if args["body"] != "multi\nline" || args["kb"] != "kb-a" {
		t.Errorf("arguments = %v", args)
	}
	if code, _, e := runCall(t, "  ", "--provider", "claude", "--dir", ws, "x", "--json", "-"); code != 2 || !strings.Contains(e, "empty stdin") {
		t.Errorf("empty stdin: %d %q", code, e)
	}
}

func TestCallListAndDescribe(t *testing.T) {
	_, _, ws := newCallEnv(t, func(method string, p map[string]any) any {
		if p["cursor"] == "next" {
			return map[string]any{"tools": []any{map[string]any{"name": "alpha", "description": "First. More."}}}
		}
		return map[string]any{"nextCursor": "next", "tools": []any{
			map[string]any{"name": "zeta", "description": "Last tool", "inputSchema": map[string]any{"type": "object"}}}}
	})
	code, out, e := runCall(t, "", "--provider", "claude", "--dir", ws, "--list")
	if code != 0 || out != "alpha\tFirst.\nzeta\tLast tool\n" {
		t.Fatalf("list: %d %q %q", code, out, e)
	}
	code, out, _ = runCall(t, "", "--provider", "claude", "--dir", ws, "--describe", "zeta")
	if code != 0 || !strings.Contains(out, `"inputSchema"`) || !strings.Contains(out, `"type": "object"`) {
		t.Errorf("describe: %d %q", code, out)
	}
	code, _, e = runCall(t, "", "--provider", "claude", "--dir", ws, "--describe", "nope")
	if code != 2 || !strings.Contains(e, `unknown tool "nope"; run "cartographer call --list"`) {
		t.Errorf("describe unknown: %d %q", code, e)
	}
}

// A slow tool is answered, and the pipe is closed only by the response: the
// bridge aborts in-flight calls a grace after its stdin ends (see callBridge).
func TestCallBridgeKeepsThePipeOpen(t *testing.T) {
	closed := false
	w := &responseWatcher{done: func() { closed = true }}
	_, _ = w.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/message","params":{}}` + "\n"))
	_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"result":{}}` + "\n"))
	if closed || w.response() != nil {
		t.Fatal("the pipe closed before the response with the request's id")
	}
	_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,`))
	_, _ = w.Write([]byte(`"result":{}}` + "\n"))
	if !closed || w.response() == nil {
		t.Fatal("the response did not close the pipe")
	}

	_, _, ws := newCallEnv(t, func(string, map[string]any) any {
		time.Sleep(300 * time.Millisecond)
		return textResult("late", false)
	})
	if code, out, e := runCall(t, "", "--provider", "claude", "--dir", ws, "slow"); code != 0 || out != "late\n" {
		t.Errorf("slow call: %d %q %q", code, out, e)
	}
}

func TestCallTimeout(t *testing.T) {
	_, _, ws := newCallEnv(t, func(string, map[string]any) any {
		time.Sleep(2 * time.Second)
		return textResult("late", false)
	})
	code, _, e := runCall(t, "", "--provider", "claude", "--dir", ws, "--timeout", "100ms", "slow")
	if code != 2 || !strings.Contains(e, "timed out") {
		t.Errorf("timeout: %d %q", code, e)
	}
}

// cartographer call writes no file anywhere and works with a read-only $HOME.
func TestCallWritesNothing(t *testing.T) {
	reply := func(method string, p map[string]any) any {
		if p["name"] == "bad" {
			return textResult("refused", true)
		}
		return textResult("ok", false)
	}
	_, home, ws := newCallEnv(t, reply)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	snap := func() []testtree.Tree {
		var all []testtree.Tree
		for _, d := range []string{home, ws, tmp} {
			tr, err := testtree.Capture(d)
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, tr)
		}
		return all
	}
	before := snap()
	for _, args := range [][]string{{"good"}, {"bad"}, {"--list"}, {"--describe", "nope"}} {
		runCall(t, "", append([]string{"--provider", "claude", "--dir", ws}, args...)...)
	}
	runCall(t, "", "--provider", "nobody") // usage failure
	if !reflect.DeepEqual(before, snap()) {
		t.Error("cartographer call changed a file under $HOME, the working directory or $TMPDIR")
	}

	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("the read-only HOME half needs POSIX permissions and a non-root user")
	}
	files, _ := filepath.Glob(filepath.Join(home, "*"))
	_ = files
	if err := os.Chmod(home, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0o755) })
	if code, _, e := runCall(t, "", "--provider", "claude", "--dir", ws, "good"); code != 0 {
		t.Errorf("read-only HOME: exit %d: %s", code, e)
	}
}

// A folder with no KB in scope gets an empty tool list from the bridge: --list
// and --describe fail with exit 2 instead of printing nothing.
func TestCallNoKBInScope(t *testing.T) {
	_, _, ws := newCallEnv(t, func(string, map[string]any) any { return textResult("ok", false) })
	cfgDir := os.Getenv("HOME")
	cfg, err := clientconfig.Load(cfgDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.BindWorkspace("claude", ws, nil); err != nil { // --no-kb
		t.Fatal(err)
	}
	if err := clientconfig.Save(cfgDir, cfg); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--list"}, {"--describe", "concept_read"}} {
		code, out, e := runCall(t, "", append([]string{"--provider", "claude", "--dir", ws}, args...)...)
		if code != 2 || out != "" || !strings.Contains(e, "no KB is served") {
			t.Errorf("%v: exit %d stdout %q stderr %q", args, code, out, e)
		}
	}
}
