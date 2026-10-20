package mcpbridge

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// gitAnswers stands for git: the identity it resolves in each folder.
func gitAnswers(byDir map[string]string) func(string) string {
	return func(dir string) string { return byDir[dir] }
}

func callParams(args map[string]any) json.RawMessage {
	return mustJSON(map[string]any{"name": "concept_write", "arguments": args})
}

// The author goes on tools/call only: the handshake and the tool list never
// name a person, though the session's one KB is known.
func TestBridgeSendsTheAuthorOnToolCallsOnly(t *testing.T) {
	rs := newRoutedServer(t, "kb-a", "kb-b")
	cfg := bridgeConfig(rs)
	ws := mkdir(t, t.TempDir())
	bind(t, cfg, ws, "kb-a")
	b := &Bridge{Cfg: cfg, Provider: "claude", Dir: ws, Token: testToken,
		workTreeIdent: gitAnswers(map[string]string{ws: "Git Person <git@example.test>"})}
	exchange(t, b, initMsg, initedMsg, listMsg, callMsg(3, "kb_status", "kb-a"), callMsg(4, "kb_status", ""))

	rs.mu.Lock()
	defer rs.mu.Unlock()
	for key, want := range map[string]string{
		"initialize 1": "",
		"tools/list 2": "",
		"tools/call 3": "Git Person <git@example.test>",
		"tools/call 4": "Git Person <git@example.test>", // the session's only KB
	} {
		got, seen := rs.authors[key]
		if !seen {
			t.Errorf("%s never reached the server (saw %v)", key, rs.authors)
			continue
		}
		if strings.Join(got, "|") != want {
			t.Errorf("%s carried the author %q, want %q", key, got, want)
		}
	}
}

func TestBridgeAuthorPicksTheKBTheServerWill(t *testing.T) {
	dir := t.TempDir()
	git := gitAnswers(map[string]string{dir: "Git Person <git@example.test>"})
	const want = "Git Person <git@example.test>"
	for _, tc := range []struct {
		name  string
		scope Scope
		known []string
		args  map[string]any
		want  string
	}{
		{"named KB", Scope{KBs: []string{"kb-a", "kb-b"}}, nil, map[string]any{"kb": " kb-a "}, want},
		{"the only KB of the scope", Scope{KBs: []string{"kb-a"}}, nil, map[string]any{}, want},
		{"several KBs, none named", Scope{KBs: []string{"kb-a", "kb-b"}}, nil, map[string]any{}, ""},
		{"unrestricted, one KB mounted", Scope{All: true}, []string{"kb-a"}, map[string]any{}, want},
		{"unrestricted, several mounted", Scope{All: true}, []string{"kb-a", "kb-b"}, map[string]any{}, ""},
	} {
		b := &Bridge{Cfg: &clientconfig.Config{KnownKBs: tc.known}, Dir: dir, workTreeIdent: git}
		if got := b.author(tc.scope, callParams(tc.args)); got != tc.want {
			t.Errorf("%s: author = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A present identities entry decides before git does, an empty one included;
// an invalid one sends nothing and is reported once.
func TestBridgeAuthorIdentitiesDecideBeforeGit(t *testing.T) {
	dir := t.TempDir()
	var logs bytes.Buffer
	b := &Bridge{Dir: dir, Log: &logs,
		workTreeIdent: gitAnswers(map[string]string{dir: "Git Person <git@example.test>"}),
		Cfg: &clientconfig.Config{Identities: map[string]string{
			"kb-a": "Ada <ada@example.test>",
			"kb-b": "",
			"kb-c": "Ada <ada@example.test",
		}}}
	scope := Scope{KBs: []string{"kb-a", "kb-b", "kb-c", "kb-d"}}
	for kb, want := range map[string]string{
		"kb-a": "Ada <ada@example.test>",
		"kb-b": "",
		"kb-c": "",
		"kb-d": "Git Person <git@example.test>",
	} {
		if got := b.author(scope, callParams(map[string]any{"kb": kb})); got != want {
			t.Errorf("author for %s = %q, want %q", kb, got, want)
		}
	}
	b.author(scope, callParams(map[string]any{"kb": "kb-c"}))
	if n := strings.Count(logs.String(), "kb-c is not a valid"); n != 1 {
		t.Errorf("the invalid entry was reported %d times, want once:\n%s", n, logs.String())
	}
}

// One call that writes to two KBs carries one author for both commits: it is
// sent only when both KBs resolve to it.
func TestBridgeAuthorOfATwoKBCall(t *testing.T) {
	scope := Scope{KBs: []string{"kb-a", "kb-b"}}
	args := map[string]any{"kb": "kb-a", "target_kb": "kb-b"}
	for _, tc := range []struct {
		name       string
		identities map[string]string
		want       string
	}{
		{"same identity", map[string]string{"default": "Ada <ada@example.test>"}, "Ada <ada@example.test>"},
		{"different identities", map[string]string{"kb-a": "Ada <ada@example.test>", "kb-b": "Bob <bob@example.test>"}, ""},
		{"the target opts out", map[string]string{"default": "Ada <ada@example.test>", "kb-b": ""}, ""},
	} {
		b := &Bridge{Dir: t.TempDir(), workTreeIdent: gitAnswers(nil), Cfg: &clientconfig.Config{Identities: tc.identities}}
		if got := b.author(scope, callParams(args)); got != tc.want {
			t.Errorf("%s: author = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A scope from roots takes each KB's identity from the roots that bound it,
// not from the folder the bridge was started in; roots that disagree send none.
func TestBridgeAuthorFromTheRootsThatBoundTheKB(t *testing.T) {
	git := gitAnswers(map[string]string{
		"/cwd": "Home <home@example.test>",
		"/r1":  "Work <work@example.test>",
		"/r2":  "Other <other@example.test>",
	})
	b := &Bridge{Dir: "/cwd", Cfg: &clientconfig.Config{}, workTreeIdent: git}
	scope := Scope{KBs: []string{"kb-a", "kb-b"}, Dirs: map[string][]string{"kb-a": {"/r1"}, "kb-b": {"/r1", "/r2"}}}
	if got := b.author(scope, callParams(map[string]any{"kb": "kb-a"})); got != "Work <work@example.test>" {
		t.Errorf("author for kb-a = %q, want the root's identity", got)
	}
	if got := b.author(scope, callParams(map[string]any{"kb": "kb-b"})); got != "" {
		t.Errorf("author for kb-b = %q, want none: its roots disagree", got)
	}
}

// git is asked once per folder for the session; a folder where it named no
// author is asked again after authorRetry.
func TestBridgeAuthorAsksGitOncePerFolder(t *testing.T) {
	asked := map[string]int{}
	b := &Bridge{Dir: "/ws", Cfg: &clientconfig.Config{}, workTreeIdent: func(dir string) string {
		asked[dir]++
		if dir == "/ws" {
			return "Git Person <git@example.test>"
		}
		return ""
	}}
	for range 3 {
		b.gitAuthor("/ws")
		b.gitAuthor("/none")
	}
	if asked["/ws"] != 1 || asked["/none"] != 1 {
		t.Fatalf("git asked %v, want once per folder", asked)
	}
	b.authorMu.Lock()
	for dir, c := range b.gitAuthors {
		c.at = c.at.Add(-authorRetry - time.Second)
		b.gitAuthors[dir] = c
	}
	b.authorMu.Unlock()
	b.gitAuthor("/ws")
	b.gitAuthor("/none")
	if asked["/ws"] != 1 || asked["/none"] != 2 {
		t.Errorf("after authorRetry git asked %v, want only the folder with no author again", asked)
	}
}

// A name that is not plain ASCII goes in MCP's encoded form (SEP-2243).
func TestBridgeEncodesANonASCIIAuthor(t *testing.T) {
	rs := newRoutedServer(t, "kb-a")
	cfg := bridgeConfig(rs)
	ws := mkdir(t, t.TempDir())
	bind(t, cfg, ws, "kb-a")
	const who = "Zoë Pérez <zoe@example.test>"
	b := &Bridge{Cfg: cfg, Provider: "claude", Dir: ws, Token: testToken,
		workTreeIdent: gitAnswers(map[string]string{ws: who})}
	exchange(t, b, initMsg, initedMsg, callMsg(2, "kb_status", "kb-a"))

	rs.mu.Lock()
	defer rs.mu.Unlock()
	want := "=?base64?" + base64.StdEncoding.EncodeToString([]byte(who)) + "?="
	if got := rs.authors["tools/call 2"]; len(got) != 1 || got[0] != want {
		t.Errorf("author header = %q, want %q", got, want)
	}
}
