package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/testtree"
)

const fbToken = "tok-abcdef123456"

// newFeedbackEnv serves /health and the two tools the command reads. status is
// the kb_status feedback object; ids the concept IDs; truncated sets the flag.
func newFeedbackEnv(t *testing.T, status map[string]any, ids []string, truncated bool) (home string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"status":"ok","version":"1.2.3","kbs":["kb-secret-name"]}`))
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		params, _ := body["params"].(map[string]any)
		var res any
		switch params["name"] {
		case "kb_status":
			res = map[string]any{"server_version": "1.2.3", "feedback": status}
		case "concept_list":
			var cs []any
			for _, id := range ids {
				cs = append(cs, map[string]any{"id": id})
			}
			res = map[string]any{"concepts": cs, "truncated": truncated}
		}
		text, _ := json.Marshal(res)
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": body["id"], "result": textResult(string(text), false)})
	}))
	t.Cleanup(srv.Close)
	home = t.TempDir()
	setHome(t, home)
	cfg := clientconfig.Default()
	cfg.ServerURL = srv.URL + "/mcp"
	cfg.Auth = true
	cfg.Token = fbToken
	cfg.KnownKBs = []string{"kb-secret-name"}
	cfg.Agents = []string{"claude"}
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv(cfg.TokenEnv(), "")
	return home
}

func runFeedback(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	old := mcpStdin
	mcpStdin = strings.NewReader(stdin)
	t.Cleanup(func() { mcpStdin = old })
	var code int
	var errOut string
	out := withStdout(t, func() { errOut = withStderr(t, func() { code = run(append([]string{"feedback"}, args...)) }) })
	return code, out, errOut
}

func TestFeedbackRemovesEveryKnownValue(t *testing.T) {
	home := newFeedbackEnv(t, map[string]any{"destination": "github"}, []string{"area/secret-page"}, false)
	draft := "kb-secret-name failed on " + home + "/notes, see area/secret-page; token " + fbToken + " ghp_" + strings.Repeat("a1B2", 9)
	code, out, errOut := runFeedback(t, draft, "--provider", "claude", "--kind", "bug", "--title", "kb-secret-name broke", "--dir", t.TempDir())
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, leak := range []string{"kb-secret-name", home, "area/secret-page", fbToken, "ghp_a1B2"} {
		if strings.Contains(out, leak) {
			t.Errorf("output carries %q:\n%s", leak, out)
		}
	}
	for _, want := range []string{"Removed:", "Search first:", "Submit: https://github.com/", "Nothing has been sent", "server version: 1.2.3"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "Search first:") > strings.Index(out, "Submit:") {
		t.Error("the search link must come before the submit link")
	}
}

func TestFeedbackJSONIsExactlyWhatTheURLCarries(t *testing.T) {
	newFeedbackEnv(t, map[string]any{"destination": "github"}, nil, true)
	code, out, errOut := runFeedback(t, "## Version and transport\nplain text", "--provider", "claude", "--kind", "idea", "--title", "t", "--output", "json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var o feedbackOut
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatal(err)
	}
	if !o.BodyInURL || !strings.Contains(o.URL, "template=idea.md") || o.Destination != "github" || len(o.Notes) == 0 {
		t.Errorf("unexpected json: %+v", o)
	}
	if strings.Contains(o.Body, "agent client") {
		t.Error("the context block must not be added when the draft has its own Version and transport section")
	}
	if want := "https://github.com/BeppeTemp/cartographer/issues/new?template=idea.md&title=t&body="; !strings.HasPrefix(o.URL, want) {
		t.Errorf("url %q", o.URL)
	}
}

func TestFeedbackOffPrintsNoDraftAndExits3(t *testing.T) {
	newFeedbackEnv(t, map[string]any{"destination": "off"}, nil, false)
	code, out, errOut := runFeedback(t, "draft", "--provider", "claude", "--kind", "bug")
	if code != 3 || out != "" || !strings.Contains(errOut, "switched off") {
		t.Errorf("exit %d stdout %q stderr %q", code, out, errOut)
	}
}

func TestFeedbackCustomUsesTheOperatorTemplate(t *testing.T) {
	newFeedbackEnv(t, map[string]any{"destination": "custom", "url_template": "https://tracker.example.com/new?k={kind}&t={title}&b={body}"}, nil, false)
	code, out, _ := runFeedback(t, "draft", "--provider", "claude", "--kind", "bug", "--title", "x")
	if code != 0 || !strings.Contains(out, "Submit: https://tracker.example.com/new?k=bug&t=x&b=") || strings.Contains(out, "Search first") {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

func TestFeedbackServerDownFallsBackToGitHub(t *testing.T) {
	home := newFeedbackEnv(t, nil, nil, false)
	cfg, _ := clientconfig.Load(home)
	cfg.ServerURL = "http://127.0.0.1:1/mcp"
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runFeedback(t, "draft", "--provider", "claude", "--kind", "bug")
	if code != 0 || !strings.Contains(out, "not collected: the server") || !strings.Contains(out, "server version: unknown") {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

func TestFeedbackUsageEmptyAndOversizedDraft(t *testing.T) {
	newFeedbackEnv(t, nil, nil, false)
	if code, _, _ := runFeedback(t, "x", "--provider", "claude"); code != 2 {
		t.Errorf("missing --kind: exit %d", code)
	}
	if code, _, _ := runFeedback(t, "  \n", "--provider", "claude", "--kind", "bug"); code != 4 {
		t.Errorf("empty draft: exit %d", code)
	}
	code, _, errOut := runFeedback(t, strings.Repeat("a ", 40*1024), "--provider", "claude", "--kind", "bug")
	if code != 2 || !strings.Contains(errOut, "64 KiB") {
		t.Errorf("oversized draft: exit %d %q", code, errOut)
	}
}

func TestFeedbackWritesNothing(t *testing.T) {
	home := newFeedbackEnv(t, map[string]any{"destination": "github"}, nil, false)
	ws, tmp := t.TempDir(), t.TempDir()
	t.Setenv("TMPDIR", tmp)
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
	runFeedback(t, "draft", "--provider", "claude", "--kind", "bug", "--dir", ws)
	runFeedback(t, "", "--provider", "claude", "--kind", "bug")
	if !reflect.DeepEqual(before, snap()) {
		t.Error("cartographer feedback changed a file under $HOME, the working directory or $TMPDIR")
	}
}

func TestFeedbackTitleFromTheDraftsFirstLine(t *testing.T) {
	newFeedbackEnv(t, map[string]any{"destination": "github"}, nil, false)
	code, out, errOut := runFeedback(t, "# It's broken; $(rm -rf x)\nbody text", "--provider", "claude", "--kind", "bug", "--output", "json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var o feedbackOut
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatal(err)
	}
	if o.Title != "It's broken; $(rm -rf x)" || strings.Contains(o.Body, "It's broken") || !strings.Contains(o.Body, "body text") {
		t.Errorf("title %q body %q", o.Title, o.Body)
	}
}
