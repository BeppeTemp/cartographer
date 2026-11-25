package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// lineageServer is a server whose sync_pull carries `relations` per KB and
// whose /health lists mounted and skipped KBs; a test changes it between syncs.
type lineageServer struct {
	*httptest.Server
	mu        sync.Mutex
	mounted   []string
	skipped   []string
	relations map[string]*contract.Relations // nil value: the field is absent (an older server)
	errKB     map[string]bool
	noRead    map[string]bool // relations_unavailable: the server could not read them
}

func newLineageServer(t *testing.T, mounted ...string) *lineageServer {
	t.Helper()
	s := &lineageServer{mounted: mounted, relations: map[string]*contract.Relations{}, errKB: map[string]bool{}, noRead: map[string]bool{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if r.URL.Path == "/health" {
			kbs := []map[string]any{}
			for _, n := range s.mounted {
				kbs = append(kbs, map[string]any{"name": n})
			}
			skipped := []map[string]any{}
			for _, n := range s.skipped {
				skipped = append(skipped, map[string]any{"name": n, "reason": "clone failed"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "kbs": kbs, "skipped_kbs": skipped})
			return
		}
		var req struct {
			ID int `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		kb := r.URL.Query().Get("kb")
		text, isErr := "", false
		if s.errKB[kb] {
			text, isErr = "relations unavailable", true
		} else {
			var m map[string]any
			_ = json.Unmarshal([]byte(syncPullPayload(t, kb, "skill-"+kb)), &m)
			if s.noRead[kb] {
				m["relations_unavailable"] = true
			} else if rel := s.relations[kb]; rel != nil {
				m["relations"] = rel
			}
			b, _ := json.Marshal(m)
			text = string(b)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
			"content": []map[string]string{{"type": "text", "text": text}},
			"isError": isErr,
		}})
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *lineageServer) set(f func(*lineageServer)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

// closureOf is a `relations` answer whose closure is names.
func closureOf(names ...string) *contract.Relations {
	r := &contract.Relations{Declared: []contract.Relation{}, Resolved: []string{}, Closure: names, Unresolved: []contract.RelationUnresolved{}}
	if r.Closure == nil {
		r.Closure = []string{}
	}
	return r
}

type lineageEnv struct {
	dir string
	cfg *clientconfig.Config
	srv *lineageServer
}

func newLineageEnv(t *testing.T, srv *lineageServer, clients map[string]clientconfig.ClientBinding) *lineageEnv {
	t.Helper()
	setHome(t, t.TempDir())
	dir := t.TempDir()
	agents := []string{}
	for p := range clients {
		agents = append(agents, p)
	}
	cfg := &clientconfig.Config{ServerURL: srv.URL + "/mcp", ServerName: "cartographer", Agents: agents, KnownKBs: srv.mounted, Clients: clients}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	return &lineageEnv{dir: dir, cfg: cfg, srv: srv}
}

// sync runs one sync with a fresh configuration read, returning stderr.
func (e *lineageEnv) sync(t *testing.T, opts syncOptions) (string, error) {
	t.Helper()
	cfg, err := clientconfig.Load(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	opts.Streams = syncStreams{Out: &out, Err: &errw}
	_, err = runSync(e.dir, cfg, opts)
	return errw.String(), err
}

func (e *lineageEnv) lineage() map[string]clientconfig.DerivedLineage {
	return clientconfig.LoadDerived(e.dir).Lineage
}

func (e *lineageEnv) skill(kb string) bool {
	_, err := os.Stat(filepath.Join(e.dir, ".claude", "skills", "skill-"+kb, "SKILL.md"))
	return err == nil
}

func (e *lineageEnv) state(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, p := range []string{filepath.Join(e.dir, clientconfig.FileName), lockFilePath(e.dir)} {
		b, _ := os.ReadFile(p)
		out[filepath.Base(p)] = string(b)
	}
	dp, _ := clientconfig.DerivedPath()
	b, _ := os.ReadFile(dp)
	out["derived"] = string(b)
	return out
}

func claudeBound(kbs ...string) map[string]clientconfig.ClientBinding {
	return map[string]clientconfig.ClientBinding{"claude": {KBs: kbs, KBsSet: true}}
}

// AC-lineage-10: the closure and the unresolved list of a pulled seed are
// stored after cleaning; what the server sends is untrusted.
func TestLineageStoredFromRelations(t *testing.T) {
	srv := newLineageServer(t, "client-kb", "core-kb")
	rel := closureOf("core-kb", "client-kb", "ghost-kb", "bad\x01name", "core-kb")
	rel.Unresolved = []contract.RelationUnresolved{{Name: "ghost-kb", Reason: "not_mounted"}, {Name: "bad\x01name", Reason: "x"}}
	srv.relations["client-kb"] = rel
	e := newLineageEnv(t, srv, claudeBound("client-kb"))
	if _, err := e.sync(t, syncOptions{}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	want := map[string]clientconfig.DerivedLineage{"client-kb": {
		Closure:    []string{"core-kb"},
		Unresolved: []clientconfig.LineageUnresolved{{Name: "ghost-kb", Reason: "not_mounted"}},
	}}
	if got := e.lineage(); !reflect.DeepEqual(got, want) {
		t.Errorf("lineage = %+v, want %+v", got, want)
	}
}

// AC-lineage-11: an error answer of the seed's sync_pull changes nothing.
func TestLineagePullErrorChangesNothing(t *testing.T) {
	srv := newLineageServer(t, "client-kb", "core-kb")
	srv.relations["client-kb"] = closureOf("core-kb")
	srv.relations["core-kb"] = closureOf()
	e := newLineageEnv(t, srv, claudeBound("client-kb"))
	if _, err := e.sync(t, syncOptions{}); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if !e.skill("core-kb") {
		t.Fatal("the lineage KB was not applied")
	}
	before := e.state(t)
	srv.set(func(s *lineageServer) { s.errKB["client-kb"] = true; s.relations["client-kb"] = closureOf() })
	_, err := e.sync(t, syncOptions{})
	if err == nil || !strings.Contains(err.Error(), "client-kb") {
		t.Fatalf("err = %v, want the failed KB reported", err)
	}
	if after := e.state(t); !reflect.DeepEqual(before, after) {
		t.Errorf("state changed after a failed pull:\nbefore %v\nafter  %v", before, after)
	}
	if !e.skill("core-kb") {
		t.Error("the lineage KB's skill was pruned by a failed pull")
	}
}

// AC-lineage-12: an answer with no `relations` (an older server) is not an
// answer: the entry stays and the notice is printed once; empty arrays remove it.
func TestLineageAbsentIsNotNoneOnOldServer(t *testing.T) {
	srv := newLineageServer(t, "client-kb", "core-kb")
	srv.relations["client-kb"] = closureOf("core-kb")
	e := newLineageEnv(t, srv, claudeBound("client-kb"))
	if _, err := e.sync(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	srv.set(func(s *lineageServer) { s.relations["client-kb"] = nil })
	stderr, err := e.sync(t, syncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := e.lineage()["client-kb"].Closure; !reflect.DeepEqual(got, []string{"core-kb"}) {
		t.Errorf("closure after an older server's answer = %v, want it kept", got)
	}
	if n := strings.Count(stderr, "server predates KB relations: lineage left as it was"); n != 1 {
		t.Errorf("notice printed %d times, want once:\n%s", n, stderr)
	}
	if !e.skill("core-kb") {
		t.Error("the lineage KB was unbound by an absent answer")
	}
	srv.set(func(s *lineageServer) { s.relations["client-kb"] = closureOf() })
	if _, err := e.sync(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := e.lineage(); len(got) != 0 {
		t.Errorf("lineage after an explicit empty relations = %+v, want none", got)
	}
	if e.skill("core-kb") {
		t.Error("the lineage KB stayed after the server declared no relation")
	}
}

// AC-lineage-13: `sync --client` keeps the entries of seeds only other
// providers hold.
func TestLineageClientKeepsOtherProviders(t *testing.T) {
	srv := newLineageServer(t, "client-kb", "core-kb", "other-kb")
	srv.relations["client-kb"] = closureOf("core-kb")
	srv.relations["other-kb"] = closureOf("core-kb")
	clients := map[string]clientconfig.ClientBinding{
		"claude": {KBs: []string{"client-kb"}, KBsSet: true},
		"codex":  {KBs: []string{"other-kb"}, KBsSet: true},
	}
	e := newLineageEnv(t, srv, clients)
	if _, err := e.sync(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	srv.set(func(s *lineageServer) { s.relations["other-kb"] = closureOf(); s.relations["client-kb"] = closureOf() })
	if _, err := e.sync(t, syncOptions{Clients: []string{"claude"}}); err != nil {
		t.Fatal(err)
	}
	got := e.lineage()
	if _, ok := got["other-kb"]; !ok {
		t.Errorf("the entry of a seed only codex holds was dropped by --client claude: %+v", got)
	}
	if _, ok := got["client-kb"]; ok {
		t.Errorf("the entry of a seed claude holds was not replaced: %+v", got)
	}
}

// AC-lineage-36: a lineage KB the server reports in skipped_kbs is held, not
// unbound; once it is no longer reported the next sync unbinds it.
func TestLineageKBInSkippedKBsIsHeld(t *testing.T) {
	srv := newLineageServer(t, "client-kb", "core-kb")
	srv.relations["client-kb"] = closureOf("core-kb")
	srv.relations["core-kb"] = closureOf()
	e := newLineageEnv(t, srv, claudeBound("client-kb"))
	if _, err := e.sync(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if !e.skill("core-kb") {
		t.Fatal("the lineage KB was not applied")
	}
	before := e.state(t)

	srv.set(func(s *lineageServer) {
		s.mounted, s.skipped = []string{"client-kb"}, []string{"core-kb"}
		r := closureOf()
		r.Unresolved = []contract.RelationUnresolved{{Name: "core-kb", Reason: "not_mounted"}}
		s.relations["client-kb"] = r
	})
	stderr, err := e.sync(t, syncOptions{})
	if err == nil {
		t.Error("the projection holding the unmounted KB was not reported as skipped")
	}
	if n := strings.Count(stderr, "held core-kb: the server could not mount it"); n != 1 {
		t.Errorf("held line printed %d times, want once:\n%s", n, stderr)
	}
	if after := e.state(t); !reflect.DeepEqual(before, after) {
		t.Errorf("state changed while the KB was held:\nbefore %v\nafter  %v", before, after)
	}
	if !e.skill("core-kb") {
		t.Error("the held KB's skill was pruned")
	}

	srv.set(func(s *lineageServer) { s.skipped = nil })
	if _, err := e.sync(t, syncOptions{}); err != nil {
		t.Fatalf("sync after the KB left skipped_kbs: %v", err)
	}
	if e.skill("core-kb") {
		t.Error("the KB stayed bound after it left the closure and skipped_kbs")
	}
	if got := e.lineage()["client-kb"].Closure; len(got) != 0 {
		t.Errorf("closure = %v, want it removed", got)
	}
}

// The explicit workspace KBs of a provider are seeds too, and a provider's
// lineage KB derives nothing (it is not a seed of derivation).
func TestLineageSeedsIncludeExplicitWorkspaceKBs(t *testing.T) {
	cfg := workspaceScoped("claude", "kb-a")
	cfg.Workspaces = map[string][]clientconfig.WorkspaceBinding{"claude": {{Path: t.TempDir(), KBs: []string{"kb-w"}}}}
	if cfg.Clients == nil {
		cfg.Clients = map[string]clientconfig.ClientBinding{}
	}
	cfg.Clients["codex"] = clientconfig.ClientBinding{KBs: []string{"kb-p"}, KBsSet: true}
	_, cands := derivationScope(cfg, []string{"claude", "codex"})
	for _, want := range []string{"kb-a", "kb-w", "kb-p"} {
		if !containsString(cands, want) {
			t.Errorf("candidates %v miss the seed %s", cands, want)
		}
	}
}

// AC-lineage-10 (workspace and derived seeds): the closure of an explicit
// workspace KB and of a KB whose paths.yaml derived a binding is stored, and an
// unreadable target and an unmounted one produce the same entry.
func TestLineageStoredForWorkspaceAndDerivedSeeds(t *testing.T) {
	cfg := workspaceScoped("claude", "kb-a")
	cfg.KnownKBs = []string{"kb-a", "kb-w", "core-kb"}
	cfg.Workspaces = map[string][]clientconfig.WorkspaceBinding{"claude": {{Path: t.TempDir(), KBs: []string{"kb-w"}}}}
	rel := func(reason string) contract.Relations {
		r := *closureOf("core-kb")
		r.Unresolved = []contract.RelationUnresolved{{Name: "hidden", Reason: reason}}
		return r
	}
	pre := &prefetched{
		cs: candidateSet{
			Named:     map[string][]contract.Artifact{"kb-a": nil, "kb-w": nil},
			Relations: map[string]contract.Relations{"kb-a": rel("not_mounted"), "kb-w": rel("not_mounted")},
		},
		kbs: map[string]bool{"kb-a": true, "kb-w": true},
	}
	next := clientconfig.DerivedState{Bindings: map[string][]clientconfig.DerivedBinding{
		"claude": {{Path: t.TempDir(), KBs: []string{"kb-a"}}},
	}}
	if notes := computeLineage(cfg, pre, &next, nil); len(notes.Held) != 0 || notes.Predates || len(notes.Unavailable) != 0 {
		t.Fatalf("notes = %+v", notes)
	}
	want := clientconfig.DerivedLineage{Closure: []string{"core-kb"}, Unresolved: []clientconfig.LineageUnresolved{{Name: "hidden", Reason: "not_mounted"}}}
	for _, seed := range []string{"kb-a", "kb-w"} {
		if got := next.Lineage[seed]; !reflect.DeepEqual(got, want) {
			t.Errorf("lineage[%s] = %+v, want %+v", seed, got, want)
		}
	}
}

// A pull that says relations_unavailable (a fresh clone has no origin/HEAD
// yet) is not "no relations" and not an older server: the entry stays, the
// artifacts are applied, and the notice names the KB.
func TestLineageRelationsUnavailableKeepsTheEntry(t *testing.T) {
	srv := newLineageServer(t, "client-kb", "core-kb")
	srv.relations["client-kb"] = closureOf("core-kb")
	e := newLineageEnv(t, srv, claudeBound("client-kb"))
	if _, err := e.sync(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	srv.set(func(s *lineageServer) { s.noRead["client-kb"] = true })
	stderr, err := e.sync(t, syncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := e.lineage()["client-kb"].Closure; !reflect.DeepEqual(got, []string{"core-kb"}) {
		t.Errorf("closure = %v, want it kept", got)
	}
	if !strings.Contains(stderr, "relations of client-kb unavailable on the server: lineage left as it was") || strings.Contains(stderr, "predates") {
		t.Errorf("stderr = %q", stderr)
	}
	if !e.skill("core-kb") {
		t.Error("the lineage KB was unbound")
	}
}

// The server's unresolved list is untrusted: a reason outside the closed set
// (control characters, a long string) and a repeated name are not stored, at
// store time and on load.
func TestLineageUnresolvedIsCleaned(t *testing.T) {
	cfg := workspaceScoped("claude", "kb-a")
	cfg.KnownKBs = []string{"kb-a", "core-kb"}
	rel := *closureOf("core-kb")
	rel.Unresolved = []contract.RelationUnresolved{
		{Name: "ghost", Reason: "not_mounted"},
		{Name: "ghost", Reason: "cycle"},
		{Name: "evil", Reason: "bad\x1b[31m reason"},
		{Name: "long", Reason: strings.Repeat("x", 4096)},
		{Name: "loop", Reason: "too_deep"},
	}
	got := lineageFromRelations(cfg, "kb-a", rel)
	want := []clientconfig.LineageUnresolved{{Name: "ghost", Reason: "not_mounted"}, {Name: "loop", Reason: "too_deep"}}
	if !reflect.DeepEqual(got.Unresolved, want) {
		t.Errorf("unresolved = %+v, want %+v", got.Unresolved, want)
	}
}

// With the server's KB list unknown this run (cfg.KnownKBs empty: /health
// listed none), a stored lineage entry is kept, never filtered to nothing.
func TestLineageKeptWhenKBListUnknown(t *testing.T) {
	cfg := workspaceScoped("claude", "kb-a")
	cfg.KnownKBs = nil
	if err := cfg.Bind("claude", "kb-a"); err != nil {
		t.Fatal(err)
	}
	cfg.SetDerived(clientconfig.DerivedState{Lineage: map[string]clientconfig.DerivedLineage{"kb-a": {Closure: []string{"core-kb"}}}})
	pre := &prefetched{
		cs:  candidateSet{Named: map[string][]contract.Artifact{"kb-a": nil}, Relations: map[string]contract.Relations{"kb-a": *closureOf("core-kb")}},
		kbs: map[string]bool{"kb-a": true},
	}
	next := clientconfig.DerivedState{}
	notes := computeLineage(cfg, pre, &next, nil)
	if got := next.Lineage["kb-a"].Closure; !reflect.DeepEqual(got, []string{"core-kb"}) {
		t.Errorf("closure = %v, want the stored one kept", got)
	}
	if !reflect.DeepEqual(notes.Unlisted, []string{"kb-a"}) {
		t.Errorf("notes = %+v", notes)
	}
}
