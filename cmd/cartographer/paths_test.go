package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/repoindex"
)

// placeholderServer is multiKBServer whose one KB, alpha, cites {{path:x}} in
// a concept: sync_pull lists it (D262) while serving no artifact at all.
func placeholderServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok","kbs":[{"name":"alpha"}]}`))
			return
		}
		var req struct {
			ID int `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
			"content": []map[string]string{{"type": "text", "text": `{"revision":"test","artifacts":[],"placeholders":["path:x"]}`}},
		}})
	}))
}

func TestDoConnect_UnresolvedPlaceholderNonTTYPrintsFix(t *testing.T) {
	srv := placeholderServer(t)
	defer srv.Close()
	dir := t.TempDir()
	opts := connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer", Trust: true, KBs: []string{"alpha"}}
	res, err := doConnect(opts)
	if err != nil {
		t.Fatalf("doConnect: %v", err)
	}
	if _, ok := res.Applied["claude"].NewLock.UnresolvedPlaceholders["path:x"]; !ok {
		t.Fatalf("path:x must be recorded unresolved: %+v", res.Applied["claude"].NewLock)
	}
	if got := res.Applied["claude"].NewLock.PlaceholderSources["path:x"]; !reflect.DeepEqual(got, []string{"alpha"}) {
		t.Errorf("sources = %v, want [alpha]", got)
	}
	out := withStdout(t, func() { printConnectResult(dir, opts.Providers, opts, res) })
	if !strings.Contains(out, "cartographer paths set <key> <path>") || !strings.Contains(out, "1 placeholder(s) not resolved") {
		t.Errorf("non-TTY connect must print the fix command:\n%s", out)
	}
	if strings.Count(out, "path:x") != 1 {
		t.Errorf("the key must be reported once:\n%s", out)
	}
}

func TestDoConnect_PlaceholderStepWritesPaths(t *testing.T) {
	srv := placeholderServer(t)
	defer srv.Close()
	dir := t.TempDir()

	var asked []placeholderRow
	old := runPathsForm
	runPathsForm = func(rows []placeholderRow) (map[string]string, error) {
		asked = rows
		return map[string]string{"path:x": "/srv/x"}, nil
	}
	defer func() { runPathsForm = old }()

	res, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer", Trust: true, KBs: []string{"alpha"}, PromptPaths: true})
	if err != nil {
		t.Fatalf("doConnect: %v", err)
	}
	if len(asked) != 1 || asked[0].Key != "path:x" || !reflect.DeepEqual(asked[0].KBs, []string{"alpha"}) || asked[0].Reason == "" {
		t.Errorf("form rows = %+v", asked)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Paths["x"] != "/srv/x" {
		t.Errorf("paths = %v, want x: /srv/x (stored without the kind prefix)", cfg.Paths)
	}
	lock := res.Applied["claude"].NewLock
	if len(lock.UnresolvedPlaceholders) != 0 || lock.ResolvedPlaceholders["path:x"] != "/srv/x" {
		t.Errorf("re-materialization must resolve path:x: %+v", lock)
	}
}

func TestCmdPaths_SetListUnsetRoundTrip(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	writeClientConfig(t, home, "search_depth: 5\nfuture_key: kept\n")

	lf := provisioning.LockFile{Providers: map[string]provisioning.Lock{
		"claude": {
			Provider:               "claude",
			ResolvedPlaceholders:   map[string]string{"repo:tool": "/srv/tool"},
			UnresolvedPlaceholders: map[string]string{"path:x": "no entry"},
			PlaceholderSources:     map[string][]string{"repo:tool": {"kb-a"}, "path:x": {"kb-a", "work-kb"}},
		},
	}}
	if err := provisioning.WriteLockFile(lockFilePath(home), lf); err != nil {
		t.Fatal(err)
	}

	var listed struct {
		Placeholders []placeholderRow `json:"placeholders"`
	}
	out := withStdout(t, func() {
		if code := cmdPaths([]string{"list", "--json"}); code != 0 {
			t.Errorf("paths list = %d", code)
		}
	})
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("not JSON: %s", out)
	}
	want := []placeholderRow{
		{Key: "path:x", KBs: []string{"kb-a", "work-kb"}, Reason: "no entry"},
		{Key: "repo:tool", KBs: []string{"kb-a"}, Path: "/srv/tool"},
	}
	if !reflect.DeepEqual(listed.Placeholders, want) {
		t.Errorf("list = %+v, want %+v", listed.Placeholders, want)
	}

	out = withStdout(t, func() {
		if code := cmdPaths([]string{"set", "path:x", filepath.Join(home, "not-yet")}); code != 0 {
			t.Errorf("paths set = %d", code)
		}
	})
	if !strings.Contains(out, "cartographer sync") {
		t.Errorf("set must print the command that applies it: %s", out)
	}
	cfg, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Paths["x"] != filepath.Join(home, "not-yet") || cfg.SearchDepth != 5 {
		t.Errorf("after set: paths=%v search_depth=%d", cfg.Paths, cfg.SearchDepth)
	}
	if data, _ := os.ReadFile(clientconfig.Path(home)); !strings.Contains(string(data), "future_key: kept") {
		t.Errorf("an unknown key must survive the save:\n%s", data)
	}

	withStdout(t, func() {
		if code := cmdPaths([]string{"unset", "x"}); code != 0 {
			t.Errorf("paths unset = %d", code)
		}
	})
	cfg, _ = clientconfig.Load(home)
	if _, ok := cfg.Paths["x"]; ok {
		t.Errorf("unset left the entry: %v", cfg.Paths)
	}
	if code := cmdPaths([]string{"unset", "x"}); code != 1 {
		t.Errorf("unset of a missing key = %d, want 1", code)
	}
	if code := cmdPaths([]string{"bogus"}); code != 2 {
		t.Errorf("unknown subcommand = %d, want 2", code)
	}
}

// TestCmdPaths_HelpPrintsEverySubcommand: `paths --help` used to reach the
// default `list`'s flag set, which printed only its own usage and exited 2 —
// never naming `set`, the fix the placeholder warning points at (#434).
func TestCmdPaths_HelpPrintsEverySubcommand(t *testing.T) {
	setHome(t, t.TempDir())
	for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}, {"list", "--help"}, {"set", "-h"}, {"unset", "--help"}} {
		out := withStdout(t, func() {
			if code := cmdPaths(args); code != 0 {
				t.Errorf("paths %v = %d, want 0", args, code)
			}
		})
		for _, want := range []string{"Usage: cartographer paths", "set <key>", "unset <key>"} {
			if !strings.Contains(out, want) {
				t.Errorf("paths %v: stdout lacks %q:\n%s", args, want, out)
			}
		}
	}
}

func TestCmdPaths_SetNeedsAConfig(t *testing.T) {
	setHome(t, t.TempDir())
	if code := cmdPaths([]string{"set", "x", "/srv/x"}); code != 2 {
		t.Errorf("set without a config = %d, want 2", code)
	}
}

func TestPathWarnings(t *testing.T) {
	dir := t.TempDir()
	if w := pathWarnings("path", filepath.Join(dir, "missing")); len(w) != 1 {
		t.Errorf("missing path: %v", w)
	}
	if w := pathWarnings("path", dir); len(w) != 0 {
		t.Errorf("existing path: %v", w)
	}
	if w := pathWarnings("repo", dir); len(w) != 1 || !strings.Contains(w[0], "not a git clone") {
		t.Errorf("repo key on a plain directory: %v", w)
	}
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if w := pathWarnings("repo", dir); len(w) != 0 {
		t.Errorf("repo key on a clone: %v", w)
	}
}

func TestStatusJSON_UnresolvedPlaceholdersOnlyWhenPresent(t *testing.T) {
	home := t.TempDir()
	if rows := snapshotUnresolvedPlaceholders(home); rows != nil {
		t.Errorf("no lockfile: %v", rows)
	}
	data, _ := json.Marshal(statusSnapshot{})
	if strings.Contains(string(data), "unresolved_placeholders") {
		t.Errorf("field must be omitted when empty: %s", data)
	}

	lf := provisioning.LockFile{Providers: map[string]provisioning.Lock{
		"claude": {Provider: "claude", UnresolvedPlaceholders: map[string]string{"path:x": "no entry"}},
	}}
	if err := provisioning.WriteLockFile(lockFilePath(home), lf); err != nil {
		t.Fatal(err)
	}
	s := statusSnapshot{UnresolvedPlaceholders: snapshotUnresolvedPlaceholders(home)}
	data, _ = json.Marshal(s)
	if !strings.Contains(string(data), `"unresolved_placeholders":[{"key":"path:x","reason":"no entry"}]`) {
		t.Errorf("field missing or malformed: %s", data)
	}
}

// The keys follow the binding exactly as the artifacts do (D262): a provider
// never resolves — nor learns — a key only an unbound KB cites.
func TestPlaceholdersForProjection(t *testing.T) {
	cs := candidateSet{Placeholders: map[string][]string{
		"kb-a":    {"path:shared", "repo:a-only"},
		"work-kb": {"path:shared", "path:w-only"},
	}}
	cfg := &clientconfig.Config{KnownKBs: []string{"kb-a", "work-kb"}, Clients: map[string]clientconfig.ClientBinding{"codex": {KBs: []string{"kb-a"}}}}

	if got, want := cs.placeholdersForProjection(cfg, syncProjection{Provider: "claude"}), map[string][]string{
		"path:shared": {"kb-a", "work-kb"}, "repo:a-only": {"kb-a"}, "path:w-only": {"work-kb"},
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("default binding = %v, want %v", got, want)
	}
	if got, want := cs.placeholdersForProjection(cfg, syncProjection{Provider: "codex"}), map[string][]string{
		"path:shared": {"kb-a"}, "repo:a-only": {"kb-a"},
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("explicit binding = %v, want %v", got, want)
	}
	if got, want := cs.placeholdersForProjection(cfg, syncProjection{Provider: "claude", Workspace: "/w", KBs: []string{"work-kb"}}), map[string][]string{
		"path:shared": {"work-kb"}, "path:w-only": {"work-kb"},
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("workspace = %v, want %v", got, want)
	}
	if got := cs.placeholdersForProjection(cfg, syncProjection{Provider: "claude", BundleOnly: true}); got != nil {
		t.Errorf("bundle-only = %v, want nil", got)
	}
}

// TestCmdPaths_IgnoreListUnsetAndSyncWarning: a key marked absent on this
// machine (D282) drops out of the sync warning, `status` and the connect
// step, is listed as ignored, and `unset` brings the warning back.
func TestCmdPaths_IgnoreListUnsetAndSyncWarning(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	writeClientConfig(t, home, "future_key: kept\n")
	lf := provisioning.LockFile{Providers: map[string]provisioning.Lock{
		"claude": {
			Provider:               "claude",
			UnresolvedPlaceholders: map[string]string{"repo:work": "not found", "path:x": "no entry"},
			PlaceholderSources:     map[string][]string{"repo:work": {"work-kb"}, "path:x": {"kb-a"}},
		},
	}}
	if err := provisioning.WriteLockFile(lockFilePath(home), lf); err != nil {
		t.Fatal(err)
	}
	results := map[string]provisioning.AppliedResult{"claude": {NewLock: lf.Providers["claude"]}}
	warn := func() string {
		return withStdout(t, func() { printApplySummary(home, results, false) })
	}

	// Unprefixed and unknown to the lock is refused; prefixed or known is fine.
	if code := cmdPaths([]string{"ignore", "nothing"}); code != 2 {
		t.Errorf("ignore of an unknown unprefixed key = %d, want 2", code)
	}
	if code := cmdPaths([]string{"ignore", "work"}); code != 0 {
		t.Fatalf("ignore work = %d", code)
	}
	cfg, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.IgnoredPaths, []string{"repo:work"}) {
		t.Errorf("IgnoredPaths = %v", cfg.IgnoredPaths)
	}
	if data, _ := os.ReadFile(clientconfig.Path(home)); !strings.Contains(string(data), "future_key: kept") {
		t.Errorf("an unknown key must survive the save:\n%s", data)
	}

	out := warn()
	if !strings.Contains(out, "1 placeholder(s) not resolved") || strings.Contains(out, "repo:work") {
		t.Errorf("an ignored key must leave the sync warning:\n%s", out)
	}
	if rows := snapshotUnresolvedPlaceholders(home); len(rows) != 1 || rows[0].Key != "path:x" {
		t.Errorf("status rows = %+v", rows)
	}

	var listed struct {
		Placeholders []placeholderRow `json:"placeholders"`
	}
	js := withStdout(t, func() { cmdPaths([]string{"list", "--json"}) })
	if err := json.Unmarshal([]byte(js), &listed); err != nil {
		t.Fatalf("not JSON: %s", js)
	}
	if len(listed.Placeholders) != 2 || !listed.Placeholders[1].Ignored || listed.Placeholders[0].Ignored {
		t.Errorf("list --json = %+v", listed.Placeholders)
	}
	if txt := withStdout(t, func() { cmdPaths([]string{"list"}) }); !strings.Contains(txt, "IGNORED") || !strings.Contains(txt, "1 unresolved") {
		t.Errorf("list must show the key as ignored and count one unresolved:\n%s", txt)
	}

	// A mapped key cannot be ignored; mapping an ignored key lifts the ignore.
	if code := cmdPaths([]string{"set", "path:x", "/srv/x"}); code != 0 {
		t.Fatal("set")
	}
	if code := cmdPaths([]string{"ignore", "path:x"}); code != 1 {
		t.Errorf("ignore of a mapped key = %d, want 1", code)
	}
	if code := cmdPaths([]string{"set", "repo:work", "/srv/work"}); code != 0 {
		t.Fatal("set")
	}
	if cfg, _ = clientconfig.Load(home); len(cfg.IgnoredPaths) != 0 {
		t.Errorf("set must supersede the ignore: %v", cfg.IgnoredPaths)
	}

	// unset reverts an ignore.
	cmdPaths([]string{"unset", "work"})
	if code := cmdPaths([]string{"ignore", "repo:work"}); code != 0 {
		t.Fatal("ignore")
	}
	if code := cmdPaths([]string{"unset", "repo:work"}); code != 0 {
		t.Errorf("unset of an ignored key = %d", code)
	}
	if out := warn(); !strings.Contains(out, "2 placeholder(s) not resolved") {
		t.Errorf("unset must restore the warning:\n%s", out)
	}
}

// TestUnresolvedAcross_NewSinceLastSync: only the keys the projection had not
// already recorded are "fresh" (D282).
func TestUnresolvedAcross_NewSinceLastSync(t *testing.T) {
	results := map[string]provisioning.AppliedResult{"claude": {
		NewLock:         provisioning.Lock{UnresolvedPlaceholders: map[string]string{"path:old": "r", "path:new": "r", "path:off": "r"}},
		PriorUnresolved: []string{"path:old"},
	}}
	un, fresh := unresolvedAcross(results, []string{"path:off"})
	if len(un) != 2 || !fresh["path:new"] || fresh["path:old"] || fresh["path:off"] {
		t.Errorf("unresolved=%v fresh=%v", un, fresh)
	}
}

// D320: `paths suggest` proposes the paths.yaml default when it exists here,
// and the `path:` home-relative guesses, without writing anything.
func TestCmdPathsSuggest_FindsDefault(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	writeClientConfig(t, home, "future_key: kept\n")
	def := filepath.Join(t.TempDir(), "testpath")
	if err := os.MkdirAll(def, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	lf := provisioning.LockFile{Providers: map[string]provisioning.Lock{
		"claude": {
			Provider:               "claude",
			UnresolvedPlaceholders: map[string]string{"path:data": "no entry", "path:notes": "no entry", "path:none": "no entry"},
			PlaceholderSources:     map[string][]string{"path:data": {"kb-a"}, "path:notes": {"kb-a"}, "path:none": {"kb-a"}},
			PlaceholderDecls:       map[string]provisioning.PathDecl{"path:data": {Description: "the data dir", Default: def}},
		},
	}}
	if err := provisioning.WriteLockFile(lockFilePath(home), lf); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(clientconfig.Path(home))
	out := withStdout(t, func() {
		if code := cmdPaths([]string{"suggest"}); code != 0 {
			t.Errorf("paths suggest = %d", code)
		}
	})
	for _, want := range []string{
		"cartographer paths set path:data " + def,
		"the data dir",
		"cartographer paths set path:notes " + filepath.Join(home, ".config", "notes"),
		"(no suggestion — use cartographer paths set path:none <path>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("suggest lacks %q:\n%s", want, out)
		}
	}
	if after, _ := os.ReadFile(clientconfig.Path(home)); string(after) != string(before) {
		t.Errorf("suggest must not write the config")
	}
}

func TestCmdPathsSuggest_SkipsIgnored(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	writeClientConfig(t, home, "ignored_paths:\n  - path:gone\n")
	lf := provisioning.LockFile{Providers: map[string]provisioning.Lock{
		"claude": {
			Provider:               "claude",
			UnresolvedPlaceholders: map[string]string{"path:gone": "no entry"},
			PlaceholderSources:     map[string][]string{"path:gone": {"kb-a"}},
		},
	}}
	if err := provisioning.WriteLockFile(lockFilePath(home), lf); err != nil {
		t.Fatal(err)
	}
	out := withStdout(t, func() { cmdPaths([]string{"suggest"}) })
	if strings.Contains(out, "path:gone") || !strings.Contains(out, "nothing to suggest") {
		t.Errorf("an ignored key must not be suggested:\n%s", out)
	}
}

func TestRepoCandidates(t *testing.T) {
	idx := &repoindex.Index{Repos: map[repoindex.RemoteKey][]string{
		"example.com/user/tool":   {"/src/tool"},
		"example.com/user/other":  {"/src/tool-fork"},
		"example.com/user/tools2": {"/src/x"},
	}}
	got := repoCandidates(idx, "tool")
	want := []string{"/src/tool", "/src/x"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("repoCandidates = %v, want %v", got, want)
	}
}
