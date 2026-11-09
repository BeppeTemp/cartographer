package main

import (
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/mcpbridge"
	"github.com/BeppeTemp/cartographer/internal/testtree"
)

func TestCallDescribeSeveralTools(t *testing.T) {
	cs, _, ws := newCallEnv(t, func(method string, p map[string]any) any {
		return map[string]any{"tools": []any{
			map[string]any{"name": "zeta", "description": "Last", "inputSchema": map[string]any{"type": "object"}},
			map[string]any{"name": "alpha", "description": "First", "inputSchema": map[string]any{"type": "object"}},
		}}
	})
	code, out, e := runCall(t, "", "--provider", "claude", "--dir", ws, "--describe", "zeta,alpha")
	var arr []map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &arr) != nil || len(arr) != 2 || arr[0]["name"] != "zeta" || arr[1]["name"] != "alpha" {
		t.Fatalf("two names: %d %q %q", code, out, e)
	}
	if cs.count() != 1 {
		t.Errorf("%d requests, want one tools/list", cs.count())
	}
	code, out, _ = runCall(t, "", "--provider", "claude", "--dir", ws, "--describe", "alpha")
	var one map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &one) != nil || one["name"] != "alpha" {
		t.Errorf("one name is an object: %d %q", code, out)
	}
	code, out, e = runCall(t, "", "--provider", "claude", "--dir", ws, "--describe", "alpha,nope,zeta")
	if code != 2 || out != "" || !strings.Contains(e, `"nope"`) || strings.Contains(e, `"alpha"`) {
		t.Errorf("unknown among known: %d %q %q", code, out, e)
	}
	for _, spec := range []string{"alpha,,zeta", "alpha,", "alpha,alpha"} {
		if code, out, _ := runCall(t, "", "--provider", "claude", "--dir", ws, "--describe", spec); code != 2 || out != "" {
			t.Errorf("--describe %q: %d %q", spec, code, out)
		}
	}
}

func TestCallInstructions(t *testing.T) {
	cs, _, ws := newCallEnv(t, func(method string, p map[string]any) any {
		if method == "initialize" {
			return map[string]any{"protocolVersion": "x", "instructions": "Guide for kb-a"}
		}
		return textResult("ok", false)
	})
	code, out, e := runCall(t, "", "--provider", "claude", "--dir", ws, "--instructions")
	if code != 0 || out != "Guide for kb-a\n" {
		t.Fatalf("text: %d %q %q", code, out, e)
	}
	if cs.reqs[0].URL.Query().Has("provisioned") || cs.reqs[0].URL.Query().Get("kbs") != "kb-a" {
		t.Errorf("--instructions URL %s must ask for the guide", cs.reqs[0].URL)
	}
	code, out, _ = runCall(t, "", "--provider", "claude", "--dir", ws, "--instructions", "--output", "json")
	if code != 0 || !strings.Contains(out, "\n  \"instructions\": \"Guide for kb-a\"") {
		t.Errorf("json: %d %q", code, out)
	}
	// A normal call keeps the marker.
	cs.mu.Lock()
	cs.reqs = nil
	cs.mu.Unlock()
	if code, _, e := runCall(t, "", "--provider", "claude", "--dir", ws, "kb_status"); code != 0 || cs.reqs[0].URL.Query().Get("provisioned") != "1" {
		t.Errorf("a tool call lost the provisioned marker: %d %s %v", code, e, cs.reqs)
	}
	for _, args := range [][]string{{"--instructions", "x"}, {"--instructions", "--list"}, {"--instructions", "--describe", "a"}, {"--instructions", "--batch", "-"}} {
		if code, _, _ := runCall(t, "", append([]string{"--provider", "claude", "--dir", ws}, args...)...); code != 2 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}

func TestCallInstructionsEscapesControlCharacters(t *testing.T) {
	_, _, ws := newCallEnv(t, func(string, map[string]any) any {
		return map[string]any{"instructions": "line 1\n\tindented \x1b]0;title\x07 end\nline 3"}
	})
	code, out, e := runCall(t, "", "--provider", "claude", "--dir", ws, "--instructions")
	if code != 0 || strings.ContainsAny(out, "\x1b\x07") || !strings.Contains(out, `\x1b]0;title\x07`) || !strings.HasPrefix(out, "line 1\n\tindented") || !strings.HasSuffix(out, "\nline 3\n") {
		t.Errorf("%d %q %q", code, out, e)
	}
}

func TestCallInstructionsEmptyAndUnbound(t *testing.T) {
	_, _, ws := newCallEnv(t, func(method string, p map[string]any) any {
		return map[string]any{"protocolVersion": "x"}
	})
	code, out, e := runCall(t, "", "--provider", "claude", "--dir", ws, "--instructions")
	if code != 2 || out != "" || !strings.Contains(e, "Error:") {
		t.Errorf("empty guide: %d %q %q", code, out, e)
	}
	home := os.Getenv("HOME")
	cfg, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.BindWorkspace("claude", ws, nil); err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	code, out, e = runCall(t, "", "--provider", "claude", "--dir", ws, "--instructions")
	if code != 0 || strings.TrimSpace(out) == "" {
		t.Errorf("no KB: the bridge's local answer is printed: %d %q %q", code, out, e)
	}
}

// batchLines runs a batch and decodes its stdout lines.
func batchLines(t *testing.T, stdin string, args ...string) (int, []map[string]any, string, string) {
	t.Helper()
	code, out, e := runCall(t, stdin, append([]string{"--batch", "-"}, args...)...)
	var res []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("stdout line %q is not JSON: %v", l, err)
		}
		res = append(res, m)
	}
	return code, res, out, e
}

func errCode(m map[string]any) float64 {
	e, _ := m["error"].(map[string]any)
	c, _ := e["code"].(float64)
	return c
}

func TestCallBatchRunsInOrderThroughOneBridge(t *testing.T) {
	var inflight, overlap atomic.Int32
	cs, _, ws := newCallEnv(t, func(method string, p map[string]any) any {
		if inflight.Add(1) > 1 {
			overlap.Add(1)
		}
		defer inflight.Add(-1)
		if p["name"] == "slow" {
			time.Sleep(200 * time.Millisecond)
		}
		return textResult("did "+p["name"].(string), false)
	})
	in := `{"tool":"slow","id":"a"}` + "\r\n\n" + `{"tool":"search","arguments":{"kb":"kb-a","limit":5},"id":7}` + "\n" + `{"tool":"kb_status","id":{"k":1}}`
	code, res, _, e := batchLines(t, in, "--provider", "claude", "--dir", ws)
	if code != 0 || len(res) != 3 {
		t.Fatalf("exit %d, %d results, stderr %q", code, len(res), e)
	}
	for i, want := range []struct {
		line float64
		id   any
		text string
	}{{1, "a", "did slow"}, {3, 7.0, "did search"}, {4, map[string]any{"k": 1.0}, "did kb_status"}} {
		r := res[i]
		if r["line"] != want.line || !reflect.DeepEqual(r["id"], want.id) || r["ok"] != true || r["text"] != want.text {
			t.Errorf("result %d = %v, want %+v", i, r, want)
		}
	}
	if overlap.Load() != 0 {
		t.Error("two requests were in flight at once")
	}
	posts, deletes := 0, 0
	for _, r := range cs.reqs {
		switch r.Method {
		case http.MethodPost:
			posts++
		case http.MethodDelete:
			deletes++
		}
	}
	if posts != 3 || deletes > 1 {
		t.Errorf("%d POST and %d DELETE, want 3 tools/call and at most one closing DELETE", posts, deletes)
	}
	args := cs.bodies[1]["params"].(map[string]any)["arguments"].(map[string]any)
	if args["limit"] != 5.0 || args["kb"] != "kb-a" {
		t.Errorf("arguments = %v", args)
	}
	for _, r := range cs.reqs {
		if r.Header.Get("Authorization") != "Bearer tok-123" {
			t.Errorf("a request without the token: %v", r.Header)
		}
	}
}

func TestCallBatchOutputJSON(t *testing.T) {
	_, _, ws := newCallEnv(t, func(string, map[string]any) any { return textResult("hi", false) })
	code, res, _, e := batchLines(t, `{"tool":"x"}`, "--provider", "claude", "--dir", ws, "--output", "json")
	if code != 0 || len(res) != 1 || res[0]["text"] != nil || res[0]["result"] == nil {
		t.Errorf("json output: %d %v %q", code, res, e)
	}
}

func TestCallBatchFailuresDoNotStopIt(t *testing.T) {
	_, _, ws := newCallEnv(t, func(method string, p map[string]any) any {
		switch p["name"] {
		case "bad":
			return textResult("refused\x1b[0m", true)
		case "unknown":
			return rpcInvalid("unknown tool unknown")
		case "args":
			return rpcInvalid("invalid params: unknown argument(s) z for tool args")
		}
		return textResult("ok", false)
	})
	base := []string{"--provider", "claude", "--dir", ws}
	code, res, _, _ := batchLines(t, `{"tool":"a"}`+"\n"+`{"tool":"bad"}`+"\n"+`{"tool":"a"}`, base...)
	if code != 1 || len(res) != 3 || res[1]["ok"] != false || errCode(res[1]) != 1 || res[2]["ok"] != true {
		t.Errorf("tool error: exit %d %v", code, res)
	}
	if msg := res[1]["error"].(map[string]any)["message"].(string); strings.Contains(msg, "\x1b") {
		t.Errorf("message not escaped: %q", msg)
	}
	code, res, _, _ = batchLines(t, `{"tool":"unknown"}`+"\n"+`{"tool":"args"}`, base...)
	if code != 2 || errCode(res[0]) != 2 || errCode(res[1]) != 1 {
		t.Errorf("unknown tool and invalid params: exit %d %v", code, res)
	}
	code, _, _, _ = batchLines(t, `{"tool":"bad"}`+"\n"+`{"tool":"unknown"}`, base...)
	if code != 2 {
		t.Errorf("mix of 1 and 2: exit %d", code)
	}
}

func TestCallBatchMalformedLines(t *testing.T) {
	cs, _, ws := newCallEnv(t, func(string, map[string]any) any { return textResult("ok", false) })
	bad := []string{`garbage`, `[1]`, `{}`, `{"tool":""}`, `{"tool":"x","argument":{}}`, `{"tool":"x","arguments":[]}`, `{"tool":"x"} trailing`, `{"tool":5,"id":"keep"}`}
	in := strings.Join(bad, "\n") + "\n" + `{"tool":"x"}`
	code, res, _, _ := batchLines(t, in, "--provider", "claude", "--dir", ws)
	if code != 2 || len(res) != len(bad)+1 {
		t.Fatalf("exit %d, %d results", code, len(res))
	}
	for i := range bad {
		msg, _ := res[i]["error"].(map[string]any)["message"].(string)
		if res[i]["ok"] != false || errCode(res[i]) != 2 || !strings.HasPrefix(msg, "line "+string(rune('1'+i))+":") {
			t.Errorf("line %d: %v", i+1, res[i])
		}
	}
	if res[len(bad)-1]["id"] != "keep" {
		t.Errorf("the id of a malformed object is echoed: %v", res[len(bad)-1])
	}
	if res[len(bad)]["ok"] != true || cs.count() < 1 {
		t.Errorf("the batch did not continue: %v", res[len(bad)])
	}
	posts := 0
	for _, r := range cs.reqs {
		if r.Method == http.MethodPost {
			posts++
		}
	}
	if posts != 1 {
		t.Errorf("%d requests sent, only the valid line goes out", posts)
	}
}

func TestCallBatchEmptyInputStartsNoBridge(t *testing.T) {
	cs, _, ws := newCallEnv(t, func(string, map[string]any) any { return textResult("ok", false) })
	for _, in := range []string{"", " \n\r\n  \n"} {
		code, out, e := runCall(t, in, "--provider", "claude", "--dir", ws, "--batch", "-")
		if code != 2 || out != "" || !strings.Contains(e, "--batch - read no request") || cs.count() != 0 {
			t.Errorf("empty %q: %d %q %q, %d requests", in, code, out, e, cs.count())
		}
	}
}

func TestCallBatchAuthorAndHintRules(t *testing.T) {
	cs, _, ws := newCallEnv(t, func(method string, p map[string]any) any {
		if p["name"] == "kb_status" {
			return textResult(`{"connection":{"hint":"Run the command`+mcpbridge.SessionHintTail+`"}}`, false)
		}
		return textResult("ok", false)
	})
	in := `{"tool":"search","arguments":{"query":"q"}}` + "\n" + `{"tool":"concept_write","arguments":{"kb":"kb-a"}}` + "\n" + `{"tool":"kb_status"}`
	code, res, _, e := batchLines(t, in, "--provider", "claude", "--dir", ws)
	if code != 0 || len(res) != 3 {
		t.Fatalf("exit %d %v %q", code, res, e)
	}
	var authors []string
	for _, r := range cs.reqs {
		if r.Method == http.MethodPost {
			authors = append(authors, r.Header.Get("Cartographer-Author"))
		}
	}
	if len(authors) != 3 || authors[0] != "" || authors[1] == "" || authors[2] != "" {
		t.Errorf("authors per request = %q: only the write names one", authors)
	}
	if txt, _ := res[2]["text"].(string); strings.Contains(txt, "agent session") || !strings.Contains(txt, "Run the command.") {
		t.Errorf("one-shot hint names a session: %q", txt)
	}
}

func TestCallBatchUnboundFolderIsReadOnly(t *testing.T) {
	cs, _, _ := newCallEnv(t, func(string, map[string]any) any { return textResult("ok", false) })
	code, res, _, e := batchLines(t, `{"tool":"kb_status"}`+"\n"+`{"tool":"kb_status"}`, "--provider", "claude", "--dir", t.TempDir())
	if code != 0 || len(res) != 2 {
		t.Fatalf("exit %d %v %q", code, res, e)
	}
	for _, r := range cs.reqs {
		if r.Method == http.MethodPost && r.URL.Query().Get("access") != "read" {
			t.Errorf("unbound request %s is not read-only", r.URL)
		}
	}
}

func TestCallBatchTimeoutAnswersTheRest(t *testing.T) {
	cs, _, ws := newCallEnv(t, func(method string, p map[string]any) any {
		if p["name"] == "slow" {
			time.Sleep(1500 * time.Millisecond)
		}
		return textResult("ok", false)
	})
	in := `{"tool":"fast"}` + "\n" + `{"tool":"slow"}` + "\n" + `{"tool":"fast"}` + "\n" + `{"tool":"fast"}`
	code, res, _, _ := batchLines(t, in, "--provider", "claude", "--dir", ws, "--timeout", "500ms")
	if code != 2 || len(res) != 3 || res[0]["ok"] != true || errCode(res[1]) != 2 || errCode(res[2]) != 2 {
		t.Fatalf("exit %d %v", code, res)
	}
	if m := res[1]["error"].(map[string]any)["message"].(string); !strings.Contains(m, "timed out") {
		t.Errorf("message %q", m)
	}
	posts := 0
	for _, r := range cs.reqs {
		if r.Method == http.MethodPost {
			posts++
		}
	}
	if posts != 2 {
		t.Errorf("%d requests sent; line 3 is answered without being sent", posts)
	}
}

// After the deadline reading stops, malformed lines included.
func TestCallBatchTimeoutStopsOnMalformedLines(t *testing.T) {
	_, _, ws := newCallEnv(t, func(string, map[string]any) any {
		time.Sleep(1500 * time.Millisecond)
		return textResult("ok", false)
	})
	in := `{"tool":"slow"}` + "\n" + "garbage\n" + "garbage\n" + "garbage"
	code, res, _, _ := batchLines(t, in, "--provider", "claude", "--dir", ws, "--timeout", "300ms")
	if code != 2 || len(res) != 2 || errCode(res[1]) != 2 || !strings.Contains(res[1]["error"].(map[string]any)["message"].(string), "timed out") {
		t.Errorf("exit %d %v", code, res)
	}
}

func TestCallBatchOverLongLineStops(t *testing.T) {
	_, _, ws := newCallEnv(t, func(string, map[string]any) any { return textResult("ok", false) })
	in := `{"tool":"x"}` + "\n" + `{"tool":"x","arguments":{"b":"` + strings.Repeat("a", maxCallJSON+10) + `"}}` + "\n" + `{"tool":"x"}`
	code, res, _, _ := batchLines(t, in, "--provider", "claude", "--dir", ws)
	if code != 2 || len(res) != 2 || res[0]["ok"] != true || errCode(res[1]) != 2 || res[1]["line"] != 2.0 ||
		!strings.Contains(res[1]["error"].(map[string]any)["message"].(string), "larger than 16 MiB") {
		t.Errorf("exit %d %v", code, res)
	}
}

func TestCallBatchUsageErrors(t *testing.T) {
	cs, _, ws := newCallEnv(t, func(string, map[string]any) any { return textResult("ok", false) })
	base := []string{"--provider", "claude", "--dir", ws}
	for _, args := range [][]string{
		{"--batch", "x"}, {"--batch", "-", "tool"}, {"--batch", "-", "--json", "{}"}, {"--batch", "-", "--list"}, {"--batch", "-", "--describe", "a"},
	} {
		if code, out, _ := runCall(t, `{"tool":"x"}`, append(append([]string{}, base...), args...)...); code != 2 || out != "" {
			t.Errorf("%v: exit %d stdout %q", args, code, out)
		}
	}
	if code, out, _ := runCall(t, `{"tool":"x"}`, "--provider", "nope", "--dir", ws, "--batch", "-"); code != 2 || out != "" {
		t.Errorf("unknown provider: %d %q", code, out)
	}
	if cs.count() != 0 {
		t.Errorf("%d requests for refused commands", cs.count())
	}
}

// A batch and --instructions write nothing and work with a read-only $HOME,
// plugin tools included.
func TestCallBatchWritesNothing(t *testing.T) {
	_, home, ws := newCallEnv(t, func(method string, p map[string]any) any {
		if method == "initialize" {
			return map[string]any{"instructions": "guide"}
		}
		return textResult("ok", false)
	})
	installCallPlugin(t, "team-cmds", toolScript)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	snap := func() []testtree.Tree {
		var all []testtree.Tree
		for _, d := range []string{ws, tmp} {
			tr, err := testtree.Capture(d)
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, tr)
		}
		return all
	}
	in := `{"tool":"x"}` + "\n" + `{"tool":"team_cmds_hi","arguments":{"who":"ann"}}`
	before := snap()
	code, res, _, e := batchLines(t, in, "--provider", "claude", "--dir", ws)
	if code != 0 || len(res) != 2 || res[1]["text"] != "hello ann" {
		t.Fatalf("batch with a plugin tool: %d %v %q", code, res, e)
	}
	runCall(t, "", "--provider", "claude", "--dir", ws, "--instructions")
	if !reflect.DeepEqual(before, snap()) {
		t.Error("a batch or --instructions wrote under the working directory or $TMPDIR")
	}
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		return
	}
	if err := os.Chmod(home, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0o755) })
	if code, _, e := runCall(t, in, "--provider", "claude", "--dir", ws, "--batch", "-"); code != 0 {
		t.Errorf("read-only HOME: exit %d %s", code, e)
	}
	if code, _, e := runCall(t, "", "--provider", "claude", "--dir", ws, "--instructions"); code != 0 {
		t.Errorf("read-only HOME, --instructions: exit %d %s", code, e)
	}
}
