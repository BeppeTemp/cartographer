package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// followServer is a /health that a test changes between syncs, over a
// sync_pull that answers one skill per KB. A KB that /health does not list
// refuses its pull, so a sync that pulls it fails loudly.
type followServer struct {
	*httptest.Server
	mu     sync.Mutex
	status int      // HTTP status of /health (0 = 200)
	body   string   // the /health body when status is 200; "" builds it from kbs
	kbs    []string // listed KBs, nil omits the key
	// skipped are the KBs the server could not mount (health skipped_kbs).
	skipped []string
	listed  bool
	pulled  map[string]int
	// payload, when set, is the sync_pull answer of a KB instead of its skill.
	payload func(kb string) string
}

func newFollowServer(t *testing.T, kbs ...string) *followServer {
	t.Helper()
	f := &followServer{kbs: kbs, listed: true, pulled: map[string]int{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.Path == "/health" {
			if f.status != 0 {
				http.Error(w, "down", f.status)
				return
			}
			if f.body != "" {
				_, _ = w.Write([]byte(f.body))
				return
			}
			h := map[string]any{"status": "ok", "version": "v0.30.0"}
			if len(f.skipped) > 0 {
				var sk []map[string]any
				for _, n := range f.skipped {
					sk = append(sk, map[string]any{"name": n, "reason": "mount failed: the path moved"})
				}
				h["skipped_kbs"] = sk
			}
			if f.listed {
				list := []map[string]any{}
				for _, k := range f.kbs {
					list = append(list, map[string]any{"name": k})
				}
				h["kbs"] = list
			}
			_ = json.NewEncoder(w).Encode(h)
			return
		}
		var req struct {
			ID int `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		kb := r.URL.Query().Get("kb")
		f.pulled[kb]++
		text, isErr := issuesPayload(t, kb, "skill-"+kb), false
		if f.payload != nil {
			text = f.payload(kb)
		}
		if !slices.Contains(f.kbs, kb) {
			text, isErr = "no such KB", true
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
			"content": []map[string]string{{"type": "text", "text": text}},
			"isError": isErr,
		}})
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *followServer) set(fn func(f *followServer)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

// followHome is a machine with claude connected to srv, bound as bind says
// ("all" for the marker, "default" for no binding, else the list), the way the
// sync reads it: HOME is the client dir.
func followHome(t *testing.T, srv *followServer, bind string, known ...string) string {
	t.Helper()
	dir := t.TempDir()
	setHome(t, dir)
	t.Setenv("FOLLOW_TOKENS", "tok")
	cfg := &clientconfig.Config{ServerURL: srv.URL + "/mcp", ServerName: "cartographer", Agents: []string{"claude"}, KnownKBs: known,
		Auth: true, Token: "${FOLLOW_TOKENS}"}
	switch bind {
	case "all":
		if err := cfg.BindAll("claude"); err != nil {
			t.Fatal(err)
		}
	case "default":
	default:
		for _, kb := range strings.Split(bind, ",") {
			if err := cfg.Bind("claude", kb); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	return dir
}

// followSync runs one sync over what the file holds now, as the command does.
func followSync(t *testing.T, dir string, opts syncOptions) (stdout, stderr string, err error) {
	t.Helper()
	cfg, lerr := clientconfig.Load(dir)
	if lerr != nil {
		t.Fatal(lerr)
	}
	var o, e bytes.Buffer
	opts.Streams = syncStreams{Out: &o, Err: &e}
	_, err = runSync(dir, cfg, opts)
	return o.String(), e.String(), err
}

func hasSkill(dir, kb string) bool {
	_, err := os.Stat(filepath.Join(dir, ".claude", "skills", "skill-"+kb, "SKILL.md"))
	return err == nil
}

func hasMCPEntry(t *testing.T, dir, name string) bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".claude.json"))
	if err != nil {
		return false
	}
	var doc struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	_, ok := doc.MCPServers[name]
	return ok
}

func knownKBsOf(t *testing.T, dir string) string {
	t.Helper()
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(cfg.KnownKBs, ",")
}

// AC-cad-10: after `connect --kb all`, a KB mounted later is added at the next
// sync: its artifacts are materialized and its name is in known_kbs.
func TestSyncAddsMountedKB(t *testing.T) {
	srv := newFollowServer(t, "one", "two")
	dir := followHome(t, srv, "all", "one", "two")
	if _, _, err := followSync(t, dir, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if !hasSkill(dir, "one") || hasSkill(dir, "three") {
		t.Fatalf("first sync: one=%v three=%v", hasSkill(dir, "one"), hasSkill(dir, "three"))
	}
	srv.set(func(f *followServer) { f.kbs = []string{"one", "two", "three"} })
	if _, _, err := followSync(t, dir, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if !hasSkill(dir, "three") {
		t.Error("the KB mounted later was not materialized")
	}
	if got := knownKBsOf(t, dir); got != "one,three,two" && got != "one,two,three" {
		t.Errorf("known_kbs = %q", got)
	}
	raw, _ := os.ReadFile(clientconfig.Path(dir))
	if !strings.Contains(string(raw), "kbs: all") || strings.Contains(string(raw), "three") {
		t.Errorf("sync rewrote the user-owned binding:\n%s", raw)
	}
}

// AC-cad-11: a KB unmounted later is removed at the next sync (its managed
// files pruned, its per-KB MCP entry removed on a non-routed server) and the
// provider is not skipped as a stale selection.
func TestSyncRemovesUnmountedKB(t *testing.T) {
	srv := newFollowServer(t, "one", "two")
	dir := followHome(t, srv, "all", "one", "two")
	if _, _, err := followSync(t, dir, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if !hasSkill(dir, "two") || !hasMCPEntry(t, dir, "cartographer-two") {
		t.Fatalf("first sync: skill=%v entry=%v", hasSkill(dir, "two"), hasMCPEntry(t, dir, "cartographer-two"))
	}
	srv.set(func(f *followServer) { f.kbs = []string{"one"} })
	_, stderr, err := followSync(t, dir, syncOptions{})
	if err != nil {
		t.Fatalf("the provider was skipped or the sync failed: %v\n%s", err, stderr)
	}
	if hasSkill(dir, "two") {
		t.Error("the unmounted KB's skill was not pruned")
	}
	if hasMCPEntry(t, dir, "cartographer-two") {
		t.Error("the unmounted KB's MCP entry was not removed")
	}
	if !hasSkill(dir, "one") {
		t.Error("the KB still mounted lost its skill")
	}
	if got := knownKBsOf(t, dir); got != "one" {
		t.Errorf("known_kbs = %q", got)
	}
}

// AC-cad-12: only an answer about the KBs the token reads changes known_kbs
// and the managed files: a failed call (500, 401, bootstrapping) and an
// anonymous reply with auth on leave them as they were; `kbs: []` removes every
// followed KB and says so on a line of its own.
func TestSyncHealthShapesChangeOnlyOnAnAnswer(t *testing.T) {
	cases := []struct {
		name     string
		set      func(f *followServer)
		warnWith string
	}{
		{"500", func(f *followServer) { f.status = 500 }, "MCP entry reconciliation skipped"},
		{"401", func(f *followServer) { f.status = 401 }, "MCP entry reconciliation skipped"},
		{"bootstrapping", func(f *followServer) { f.body = `{"status":"ok","version":"v0.30.0","bootstrapping":true}` }, "MCP entry reconciliation skipped"},
		{"anonymous", func(f *followServer) { f.listed = false }, "the server did not list its KBs (is the token set and valid?): the KBs this client follows are left as they were"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newFollowServer(t, "one", "two")
			dir := followHome(t, srv, "all", "one", "two")
			if _, _, err := followSync(t, dir, syncOptions{}); err != nil {
				t.Fatal(err)
			}
			srv.set(tc.set)
			_, stderr, _ := followSync(t, dir, syncOptions{})
			if !strings.Contains(stderr, tc.warnWith) {
				t.Errorf("no %q in:\n%s", tc.warnWith, stderr)
			}
			if got := knownKBsOf(t, dir); got != "one,two" {
				t.Errorf("known_kbs = %q, want it unchanged", got)
			}
			if !hasSkill(dir, "one") || !hasSkill(dir, "two") || !hasMCPEntry(t, dir, "cartographer-two") {
				t.Errorf("managed files changed: one=%v two=%v entry=%v", hasSkill(dir, "one"), hasSkill(dir, "two"), hasMCPEntry(t, dir, "cartographer-two"))
			}
		})
	}

	// Nothing mounted because every mount failed: the server lists `kbs: []` with
	// the KBs it could not mount in skipped_kbs, and nothing is removed (WP5).
	t.Run("listed empty with skipped", func(t *testing.T) {
		srv := newFollowServer(t, "one", "two")
		dir := followHome(t, srv, "all", "one", "two")
		if _, _, err := followSync(t, dir, syncOptions{}); err != nil {
			t.Fatal(err)
		}
		srv.set(func(f *followServer) { f.kbs, f.skipped = []string{}, []string{"one", "two"} })
		out, errOut, err := followSync(t, dir, syncOptions{})
		if err != nil {
			t.Fatalf("sync: %v\n%s", err, errOut)
		}
		if !hasSkill(dir, "one") || !hasSkill(dir, "two") || knownKBsOf(t, dir) != "one,two" {
			t.Errorf("a failed mount removed what it held: known=%q\n%s", knownKBsOf(t, dir), out)
		}
		if strings.Contains(out, "every KB this client follows leaves") {
			t.Errorf("the KBs held for a failed mount are said to leave:\n%s", out)
		}
		if !strings.Contains(out, "held one:") || !strings.Contains(out, "held two:") {
			t.Errorf("the hold is not reported:\n%s", out)
		}
	})

	t.Run("listed empty", func(t *testing.T) {
		srv := newFollowServer(t, "one", "two")
		dir := followHome(t, srv, "all", "one", "two")
		if _, _, err := followSync(t, dir, syncOptions{}); err != nil {
			t.Fatal(err)
		}
		srv.set(func(f *followServer) { f.kbs = []string{} })
		out, _, _ := followSync(t, dir, syncOptions{DryRun: true})
		if !strings.Contains(out, "[dry-run] would remove: the server lists no KB for this token") || !hasSkill(dir, "one") {
			t.Errorf("dry run:\n%s (skill kept: %v)", out, hasSkill(dir, "one"))
		}
		out, errOut, serr := followSync(t, dir, syncOptions{})
		if serr != nil {
			t.Fatalf("sync: %v\n%s", serr, errOut)
		}
		if !strings.Contains(out, "the server lists no KB for this token: every KB this client follows leaves\n") {
			t.Errorf("no line of its own:\n%s", out)
		}
		if hasSkill(dir, "one") || hasSkill(dir, "two") || knownKBsOf(t, dir) != "" {
			t.Errorf("the followed KBs did not leave: known=%q", knownKBsOf(t, dir))
		}
	})

	// #2401: with explicit bindings no provider follows the server, so nothing
	// leaves and the line must not be printed.
	t.Run("listed empty with explicit bindings", func(t *testing.T) {
		srv := newFollowServer(t, "one", "two")
		dir := followHome(t, srv, "one,two", "one", "two")
		if _, _, err := followSync(t, dir, syncOptions{}); err != nil {
			t.Fatal(err)
		}
		srv.set(func(f *followServer) { f.kbs = []string{} })
		for _, dry := range []bool{true, false} {
			out, _, _ := followSync(t, dir, syncOptions{DryRun: dry})
			if strings.Contains(out, "every KB this client follows leaves") {
				t.Errorf("dry=%v: a client with explicit bindings is said to follow the server:\n%s", dry, out)
			}
		}
	})
}

// AC-cad-13: a KB the token cannot read is not listed, so the marker never adds
// it (no pull, no file), and revoking the grant of a followed KB removes it at
// the next sync like an unmounted one.
func TestSyncUnreadableKBIsNotFollowed(t *testing.T) {
	srv := newFollowServer(t, "one") // the server also mounts "two", but this token does not read it
	dir := followHome(t, srv, "all")
	if _, _, err := followSync(t, dir, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if hasSkill(dir, "two") || srv.pulled["two"] != 0 {
		t.Errorf("the unreadable KB was pulled or written (pulls %d)", srv.pulled["two"])
	}
	srv.set(func(f *followServer) { f.kbs = []string{"one", "two"} }) // granted
	if _, _, err := followSync(t, dir, syncOptions{}); err != nil || !hasSkill(dir, "two") {
		t.Fatalf("grant: %v two=%v", err, hasSkill(dir, "two"))
	}
	srv.set(func(f *followServer) { f.kbs = []string{"one"} }) // revoked
	if _, _, err := followSync(t, dir, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if hasSkill(dir, "two") || knownKBsOf(t, dir) != "one" {
		t.Errorf("revoked KB stays: skill=%v known=%q", hasSkill(dir, "two"), knownKBsOf(t, dir))
	}
}

// AC-cad-14: a provider with an explicit list is unaffected by a mounted or an
// unmounted KB.
func TestSyncExplicitListUnaffected(t *testing.T) {
	srv := newFollowServer(t, "one", "two")
	dir := followHome(t, srv, "one", "one", "two")
	if _, _, err := followSync(t, dir, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	srv.set(func(f *followServer) { f.kbs = []string{"one", "two", "three"} })
	if _, _, err := followSync(t, dir, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if hasSkill(dir, "three") || hasSkill(dir, "two") || !hasSkill(dir, "one") {
		t.Errorf("a mounted KB reached the list: one=%v two=%v three=%v", hasSkill(dir, "one"), hasSkill(dir, "two"), hasSkill(dir, "three"))
	}
	srv.set(func(f *followServer) { f.kbs = []string{"two"} })
	if _, _, err := followSync(t, dir, syncOptions{}); err == nil {
		t.Error("an unmounted KB of an explicit list must still be a stale selection")
	}
	if !hasSkill(dir, "one") {
		t.Error("the stale selection pruned the provider")
	}
}

func TestKBListAnswerShapes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		facts serverFacts
		err   error
		want  kbAnswer
	}{
		{"call failed", serverFacts{}, os.ErrDeadlineExceeded, kbAnswerNone},
		{"anonymous", serverFacts{}, nil, kbAnswerAnonymous},
		{"listed", serverFacts{Listed: true, Names: []string{"kb-a"}}, nil, kbAnswerListed},
		{"listed empty", serverFacts{Listed: true, Names: []string{}}, nil, kbAnswerEmpty},
	} {
		if got := kbListAnswer(tc.facts, tc.err); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

// AC-cad-16: the sync says which KB it added and removed for the providers that
// follow the server (and says it as a plan under --dry-run), and an added KB
// counts in the closing sum line.
func TestSyncReportsMountedAndUnmounted(t *testing.T) {
	srv := newFollowServer(t, "one", "two")
	dir := followHome(t, srv, "all", "one", "two")
	if _, _, err := followSync(t, dir, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	srv.set(func(f *followServer) { f.kbs = []string{"one", "three"} })

	out, _, err := followSync(t, dir, syncOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[dry-run] would add three for claude", "[dry-run] would remove two for claude", "[dry-run] this sync would widen what is bound: 1 KB(s) mounted"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run lacks %q:\n%s", want, out)
		}
	}
	if hasSkill(dir, "three") || !hasSkill(dir, "two") {
		t.Error("the dry run wrote")
	}

	out, _, err = followSync(t, dir, syncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"three mounted: added for claude\n", "two not mounted any more: removed for claude\n", "this sync widened what is bound: 1 KB(s) mounted"} {
		if !strings.Contains(out, want) {
			t.Errorf("sync lacks %q:\n%s", want, out)
		}
	}
	// A provider with a list sees none of it.
	list := followHome(t, srv, "one", "one", "two")
	srv.set(func(f *followServer) { f.kbs = []string{"one", "two", "four"} })
	out, _, _ = followSync(t, list, syncOptions{})
	if strings.Contains(out, "mounted: added") {
		t.Errorf("an explicit list was told about a mount:\n%s", out)
	}
}

// AC-cad-17: a KB mounted later that ships a hook, an executable file, a skill
// declaring hooks and an MCP descriptor installs none of them before
// `cartographer approve`, installs its plain skill, and the sync names how many
// wait and the command; nothing is marked approved.
func TestNewKBHookAndMCPNeedApproval(t *testing.T) {
	srv := newFollowServer(t, "one")
	dir := followHome(t, srv, "all", "one")
	if _, _, err := followSync(t, dir, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	arrival := func(kb string) string {
		skill := func(name, body string, exec bool) contract.Artifact {
			files := []contract.ArtifactFile{{Path: "SKILL.md", Content: []byte(body), Executable: exec}}
			return contract.Artifact{Kind: "skill", Name: name, Source: "kb:" + kb, ContentHash: provisioning.ContentHashFiles(files), Files: files}
		}
		mcpFiles := []contract.ArtifactFile{{Path: "tools.json", Content: []byte(`{"type":"http","url":"https://example.com/mcp","headers":{"X":"${VAR}"}}`)}}
		hookFiles := []contract.ArtifactFile{
			{Path: "hook.json", Content: []byte(`{"event":"SessionStart","command":"./run.sh"}`)},
			{Path: "run.sh", Content: []byte("#!/bin/sh\n"), Executable: true},
		}
		return pulledHookPayload(t, kb,
			skill("plain", "# plain", false),
			skill("hooked", "---\nname: hooked\ndescription: d\nhooks:\n  PreToolUse:\n    - hooks:\n        - type: command\n          command: ./x.sh\n---\n# hooked\n", false),
			skill("runner", "# runner", true),
			contract.Artifact{Kind: "hook", Name: "guard", Source: "kb:" + kb, ContentHash: provisioning.ContentHashFiles(hookFiles), Files: hookFiles},
			contract.Artifact{Kind: "mcp", Name: "tools", Source: "kb:" + kb, ContentHash: provisioning.ContentHashFiles(mcpFiles), Files: mcpFiles},
		)
	}
	srv.set(func(f *followServer) {
		f.kbs = []string{"one", "newkb"}
		f.payload = func(kb string) string {
			if kb == "newkb" {
				return arrival(kb)
			}
			return issuesPayload(t, kb, "skill-"+kb)
		}
	})
	out, _, err := followSync(t, dir, syncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	skills := filepath.Join(dir, ".claude", "skills")
	for _, name := range []string{"hooked", "runner", "guard", "tools"} {
		if _, err := os.Stat(filepath.Join(skills, name)); err == nil {
			t.Errorf("%s was installed before an approval", name)
		}
	}
	if _, err := os.Stat(filepath.Join(skills, "plain", "SKILL.md")); err != nil {
		t.Errorf("the plain skill was not installed: %v", err)
	}
	if hasMCPEntry(t, dir, "tools") {
		t.Error("the MCP descriptor was registered")
	}
	if !strings.Contains(out, "newkb: 4 artifact(s) need approval: ") || !strings.Contains(out, "cartographer approve hook guard --kb newkb") {
		t.Errorf("the arrival does not name what waits:\n%s", out)
	}
	if cfg, _ := clientconfig.Load(dir); len(cfg.ApprovedMCPHashes()) != 0 {
		t.Errorf("the sync approved something: %v", cfg.ApprovedMCPHashes())
	}
}
