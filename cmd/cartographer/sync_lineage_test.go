package main

import (
	"bytes"
	"encoding/base64"
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
	"github.com/BeppeTemp/cartographer/internal/provisioning"
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
	noRead    map[string]bool           // relations_unavailable: the server could not read them
	registry  map[string]map[string]any // KB -> path_registry
	pulls     map[string]int            // sync_pull calls per KB
	routed    string                    // routed endpoint path in /health, "" for none
	// instructions: every KB's pull also carries an instructions artifact.
	instructions bool
	// skillsLine: every KB's instructions carry the generated `Skills:` line
	// after the routing paragraph, as the server writes it.
	skillsLine bool
	// hookKB: the KB whose pull also carries a hook.
	hookKB string
	// agents: every KB's pull also carries an agent named agent-<kb>.
	agents bool
	// directives: every KB's instructions also declare a directive whose value
	// is the KB's name, so two KBs collide on it.
	directives bool
	// skillText: per KB, text its skill's SKILL.md carries.
	skillText map[string]string
	// issues: per KB, what its pull says it left out.
	issues map[string][]string
	// skillName: every KB's pull serves its skill under this name, so two KBs collide.
	skillName string
}

func newLineageServer(t *testing.T, mounted ...string) *lineageServer {
	t.Helper()
	s := &lineageServer{mounted: mounted, relations: map[string]*contract.Relations{}, errKB: map[string]bool{}, noRead: map[string]bool{}, registry: map[string]map[string]any{}, pulls: map[string]int{}}
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
			h := map[string]any{"status": "ok", "kbs": kbs, "skipped_kbs": skipped}
			if s.routed != "" {
				h["routed_path"], h["mount_mode"] = s.routed, "routed"
			}
			_ = json.NewEncoder(w).Encode(h)
			return
		}
		var req struct {
			ID int `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		kb := r.URL.Query().Get("kb")
		text, isErr := "", false
		s.pulls[kb]++
		if s.errKB[kb] {
			text, isErr = "relations unavailable", true
		} else {
			var m map[string]any
			skill := "skill-" + kb
			if s.skillName != "" {
				skill = s.skillName
			}
			_ = json.Unmarshal([]byte(syncPullPayload(t, kb, skill)), &m)
			if txt := s.skillText[kb]; txt != "" {
				files := []contract.ArtifactFile{{Path: "SKILL.md", Content: []byte("# " + skill + "\n" + txt)}}
				m["artifacts"] = []any{map[string]any{"kind": "skill", "name": skill, "source": "kb:" + kb,
					"content_hash": provisioning.ContentHashFiles(files),
					"files":        []map[string]any{{"path": "SKILL.md", "content_b64": base64.StdEncoding.EncodeToString(files[0].Content)}}}}
			}
			if is := s.issues[kb]; is != nil {
				m["issues"] = is
			}
			if s.instructions {
				body := []byte("# section of " + kb + "\n")
				if s.skillsLine {
					body = []byte("# section of " + kb + "\n\nSkills: skill-" + kb + "\n")
				}
				if s.directives {
					body = append(body, []byte("<!-- cartographer:directive:language:"+kb+" -->\n")...)
				}
				files := []contract.ArtifactFile{{Path: "instructions.md", Content: body}}
				arts, _ := m["artifacts"].([]any)
				m["artifacts"] = append(arts, map[string]any{"kind": "instructions", "name": kb, "source": "kb:" + kb,
					"content_hash": provisioning.ContentHashFiles(files),
					"files":        []map[string]any{{"path": "instructions.md", "content_b64": base64.StdEncoding.EncodeToString(body)}}})
			}
			if s.agents {
				files := []contract.ArtifactFile{{Path: "agent.md", Content: []byte("---\nname: agent-" + kb + "\ndescription: d\n---\nbody\n")}}
				arts, _ := m["artifacts"].([]any)
				m["artifacts"] = append(arts, map[string]any{"kind": "agent", "name": "agent-" + kb, "source": "kb:" + kb,
					"content_hash": provisioning.ContentHashFiles(files),
					"files":        []map[string]any{{"path": "agent.md", "content_b64": base64.StdEncoding.EncodeToString(files[0].Content)}}})
			}
			if kb != "" && kb == s.hookKB {
				files := []contract.ArtifactFile{
					{Path: "hook.json", Content: []byte(`{"event":"SessionStart","command":"./run.sh"}`)},
					{Path: "run.sh", Content: []byte("#!/bin/sh\n"), Executable: true},
				}
				var fs []map[string]any
				for _, f := range files {
					fs = append(fs, map[string]any{"path": f.Path, "content_b64": base64.StdEncoding.EncodeToString(f.Content), "executable": f.Executable})
				}
				arts, _ := m["artifacts"].([]any)
				m["artifacts"] = append(arts, map[string]any{"kind": "hook", "name": "guard", "source": "kb:" + kb,
					"content_hash": provisioning.ContentHashFiles(files), "files": fs})
			}
			if reg := s.registry[kb]; reg != nil {
				m["path_registry"] = reg
			}
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

// registryOf is a path_registry that declares one repository.
func registryOf(key, remote string) map[string]any {
	return map[string]any{"repos": map[string]any{key: map[string]any{"description": "d", "remote": remote}}}
}

// workspaceLineageEnv is a workspace-scoped claude whose KB client-kb declares
// a repository on this machine and depends on core-kb.
func workspaceLineageEnv(t *testing.T) (*lineageEnv, string) {
	t.Helper()
	repo := cloneOf(t, "example.com/owner/worked-on")
	srv := newLineageServer(t, "client-kb", "core-kb")
	srv.registry["client-kb"] = registryOf("worked-on", "example.com/owner/worked-on")
	srv.relations["client-kb"] = closureOf("core-kb")
	srv.relations["core-kb"] = closureOf()
	e := newLineageEnv(t, srv, claudeBound("client-kb"))
	e.cfg.Paths = map[string]string{"worked-on": repo}
	if err := e.cfg.SetWorkspaceScope("claude", clientconfig.ScopeWorkspace); err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(e.dir, e.cfg); err != nil {
		t.Fatal(err)
	}
	return e, repo
}

func (e *lineageEnv) syncOut(t *testing.T, opts syncOptions) (stdout, stderr string, err error) {
	t.Helper()
	cfg, err := clientconfig.Load(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	opts.Streams = syncStreams{Out: &out, Err: &errw}
	_, err = runSync(e.dir, cfg, opts)
	return out.String(), errw.String(), err
}

func repoSkill(repo, kb string) bool {
	_, err := os.Stat(filepath.Join(repo, ".claude", "skills", "skill-"+kb, "SKILL.md"))
	return err == nil
}

// AC-lineage-14: one sync binds a derived repository to its KB and to the
// KB's closure, and the lineage KB's skills are projected into it.
func TestSyncBindsLineageInOneRun(t *testing.T) {
	e, repo := workspaceLineageEnv(t)
	out, _, err := e.syncOut(t, syncOptions{})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !repoSkill(repo, "client-kb") || !repoSkill(repo, "core-kb") {
		t.Errorf("the repository did not receive both KBs (client-kb %v, core-kb %v)", repoSkill(repo, "client-kb"), repoSkill(repo, "core-kb"))
	}
	if !strings.Contains(out, "bound by lineage: core-kb (from client-kb) in "+repo) {
		t.Errorf("no lineage line:\n%s", out)
	}
}

// AC-lineage-15: the relation going is applied by the next sync, which prunes
// the lineage KB's artifacts and says so.
func TestSyncUnbindsLineageWhenRelationGoes(t *testing.T) {
	e, repo := workspaceLineageEnv(t)
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	e.srv.set(func(s *lineageServer) { s.relations["client-kb"] = closureOf() })
	out, _, err := e.syncOut(t, syncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if repoSkill(repo, "core-kb") || !repoSkill(repo, "client-kb") {
		t.Errorf("after the relation went: core-kb %v, client-kb %v", repoSkill(repo, "core-kb"), repoSkill(repo, "client-kb"))
	}
	if !strings.Contains(out, "no longer bound by lineage: core-kb in "+repo) {
		t.Errorf("no removal line:\n%s", out)
	}
}

// AC-lineage-16: a lineage KB whose pull fails skips the projections that hold
// it, and the seed's previous lineage entry is kept.
func TestSyncLineagePullFailureSkipsHolders(t *testing.T) {
	e, repo := workspaceLineageEnv(t)
	e.srv.set(func(s *lineageServer) { s.errKB["core-kb"] = true })
	_, stderr, err := e.syncOut(t, syncOptions{})
	if err == nil || !strings.Contains(err.Error()+stderr, "core-kb") {
		t.Fatalf("err = %v, stderr = %s: want the failed KB reported", err, stderr)
	}
	if repoSkill(repo, "client-kb") || repoSkill(repo, "core-kb") {
		t.Error("the projection holding the failed KB was applied")
	}
	if got := e.lineage()["client-kb"].Closure; len(got) != 0 {
		t.Errorf("stored closure = %v, want the previous (none)", got)
	}
}

// A lineage KB another provider already pulls as its own seed is covered by
// the candidates, not by the lineage pull: its failure still puts the stored
// entry of the seed naming it back.
func TestSyncLineagePullFailureOfSeedElsewhereKeepsStoredEntry(t *testing.T) {
	// opencode holds only plain-kb, so the run survives the failed pull and
	// reaches the commit of the derived state.
	e, _ := workspaceLineageEnv(t)
	e.srv.mounted = append(e.srv.mounted, "plain-kb")
	e.srv.set(func(s *lineageServer) { s.relations["client-kb"] = closureOf(); s.relations["plain-kb"] = closureOf() })
	e.cfg.KnownKBs = e.srv.mounted
	e.cfg.Clients["codex"] = clientconfig.ClientBinding{KBs: []string{"core-kb"}, KBsSet: true}
	e.cfg.Clients["opencode"] = clientconfig.ClientBinding{KBs: []string{"plain-kb"}, KBsSet: true}
	e.cfg.Agents = append(e.cfg.Agents, "codex", "opencode")
	if err := clientconfig.Save(e.dir, e.cfg); err != nil {
		t.Fatal(err)
	}
	_, _, err := e.syncOut(t, syncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	e.srv.set(func(s *lineageServer) {
		s.relations["client-kb"] = closureOf("core-kb")
		s.errKB["core-kb"] = true
	})
	_, _, err = e.syncOut(t, syncOptions{})
	if err == nil {
		t.Fatal("want the failed pull reported")
	}
	if got := e.lineage()["client-kb"].Closure; len(got) != 0 {
		t.Errorf("stored closure = %v, want the previous (none)", got)
	}
}

// AC-lineage-17: each KB is pulled once per sync.
func TestSyncPullsEachKBOnce(t *testing.T) {
	e, _ := workspaceLineageEnv(t)
	for i := 0; i < 2; i++ {
		e.srv.set(func(s *lineageServer) { s.pulls = map[string]int{} })
		if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
			t.Fatal(err)
		}
		e.srv.set(func(s *lineageServer) {
			for _, kb := range []string{"client-kb", "core-kb"} {
				if s.pulls[kb] != 1 {
					t.Errorf("sync %d pulled %s %d times, want once", i+1, kb, s.pulls[kb])
				}
			}
		})
	}
}

// AC-lineage-18, and the carry-over of the usage test: a provider-scope
// provider bound to client-kb gets core-kb in the entry and in the provisioned
// content (artifacts, instructions, revision) of the sync that learns it.
func TestSyncProviderScopeEntryCarriesLineage(t *testing.T) {
	for _, routed := range []string{"/mcp/kb", ""} {
		srv := newLineageServer(t, "client-kb", "core-kb")
		srv.routed, srv.instructions = routed, true
		srv.relations["client-kb"] = closureOf("core-kb")
		srv.relations["core-kb"] = closureOf()
		e := newLineageEnv(t, srv, claudeBound("client-kb"))
		out, _, err := e.syncOut(t, syncOptions{})
		if err != nil {
			t.Fatalf("routed=%q: %v", routed, err)
		}
		if !e.skill("client-kb") || !e.skill("core-kb") {
			t.Errorf("routed=%q: artifacts client-kb %v, core-kb %v", routed, e.skill("client-kb"), e.skill("core-kb"))
		}
		lock, err := os.ReadFile(lockFilePath(e.dir))
		if err != nil || !strings.Contains(string(lock), "core-kb") {
			t.Errorf("routed=%q: the lock does not name core-kb (%v)", routed, err)
		}
		if !strings.Contains(out, "claude: core-kb bound by lineage (from client-kb)") {
			t.Errorf("routed=%q: no provider line:\n%s", routed, out)
		}
		mcp := e.mcpConfig(t)
		if routed != "" && !strings.Contains(mcp, "kbs=client-kb%2Ccore-kb") && !strings.Contains(mcp, "kbs=client-kb,core-kb") {
			t.Errorf("routed entry does not carry core-kb:\n%s", mcp)
		}
		if routed == "" && !strings.Contains(mcp, "cartographer-core-kb") {
			t.Errorf("no per-KB entry for core-kb:\n%s", mcp)
		}
		if !strings.Contains(e.instructions(t), "core-kb") {
			t.Errorf("routed=%q: the instructions block does not name core-kb", routed)
		}
	}
}

// mcpConfig is every JSON file under the config dir and HOME that holds an MCP
// entry, concatenated.
func (e *lineageEnv) mcpConfig(t *testing.T) string {
	t.Helper()
	var sb strings.Builder
	for _, root := range []string{e.dir, os.Getenv("HOME")} {
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".json") && !strings.Contains(p, "workspace-derived") {
				b, _ := os.ReadFile(p)
				sb.Write(b)
			}
			return nil
		})
	}
	return sb.String()
}

// instructions is the managed instructions files under the config dir.
func (e *lineageEnv) instructions(t *testing.T) string {
	t.Helper()
	var sb strings.Builder
	_ = filepath.WalkDir(e.dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") && !strings.Contains(p, "/skills/") {
			b, _ := os.ReadFile(p)
			sb.Write(b)
		}
		return nil
	})
	return sb.String()
}

// AC-lineage-19: a KB that appears with a paths.yaml and a depends_on yields
// the derived-binding line, the lineage line and the sum in one run; a second
// identical sync prints none; --dry-run prints them with `would` and writes
// nothing.
func TestSyncCombinedEffectOneRun(t *testing.T) {
	e, repo := workspaceLineageEnv(t)
	before := e.state(t)
	out, _, err := e.syncOut(t, syncOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[dry-run] bound by paths.yaml: client-kb in " + repo, "[dry-run] bound by lineage: core-kb (from client-kb) in " + repo, "[dry-run] this sync would widen what is bound: 1 repository(ies) bound by paths.yaml, 2 KB(s) brought by lineage"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run misses %q:\n%s", want, out)
		}
	}
	if !reflect.DeepEqual(before, e.state(t)) || repoSkill(repo, "client-kb") {
		t.Error("the dry run wrote something")
	}
	out, _, err = e.syncOut(t, syncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"bound by paths.yaml: client-kb in " + repo, "bound by lineage: core-kb (from client-kb) in " + repo, "this sync widened what is bound: 1 repository(ies) bound by paths.yaml, 2 KB(s) brought by lineage; review with cartographer status"} {
		if strings.Count(out, want) != 1 {
			t.Errorf("sync misses %q once:\n%s", want, out)
		}
	}
	if lc := clientconfig.LoadDerived(e.dir).LastChange; lc == nil || len(lc.Added) != 3 {
		t.Errorf("last_change = %+v, want the three additions", lc)
	}
	out, _, err = e.syncOut(t, syncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "bound by") || strings.Contains(out, "widened") {
		t.Errorf("an unchanged sync announced something:\n%s", out)
	}
}

// AC-lineage-20: a hook of a lineage KB is not installed by the relation
// appearing: it waits for `approve --kb <lineage-kb>`, like a hand-bound KB's.
func TestSyncLineageApprovals(t *testing.T) {
	e, repo := workspaceLineageEnv(t)
	e.srv.set(func(s *lineageServer) { s.hookKB = "core-kb" })
	out, _, err := e.syncOut(t, syncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "needs_approval: hook/guard [kb:core-kb]") {
		t.Errorf("the lineage KB's hook is not waiting for approval:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(repo, ".claude", "hooks", "guard", "run.sh")); err == nil {
		t.Error("the lineage KB's hook was installed with no approval")
	}
	if !repoSkill(repo, "core-kb") {
		t.Error("the lineage KB's skill was not applied")
	}
}

// trackedLineageEnv is a claude bound explicitly (workspace scope) to a
// repository that tracks CLAUDE.md, with client-kb depending on core-kb once
// the relation is set. Every KB's pull carries an instructions section.
func trackedLineageEnv(t *testing.T, tracked ...string) (*lineageEnv, string) {
	t.Helper()
	repo := cloneOf(t, "example.com/owner/worked-on", tracked...)
	srv := newLineageServer(t, "client-kb", "core-kb")
	srv.instructions = true
	srv.relations["client-kb"] = closureOf()
	srv.relations["core-kb"] = closureOf()
	e := newLineageEnv(t, srv, claudeBound("client-kb"))
	e.cfg.Workspaces = map[string][]clientconfig.WorkspaceBinding{"claude": {{Path: repo, KBs: []string{"client-kb"}}}}
	if err := e.cfg.SetWorkspaceScope("claude", clientconfig.ScopeWorkspace); err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(e.dir, e.cfg); err != nil {
		t.Fatal(err)
	}
	return e, repo
}

func readFileT(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

// AC-lineage-31: with a tracked instructions file, a relation appearing and
// disappearing changes no byte of it, while the lineage KB's skill arrives.
func TestTrackedAgentsMDUntouchedByLineage(t *testing.T) {
	e, repo := trackedLineageEnv(t, "CLAUDE.md")
	file := filepath.Join(repo, "CLAUDE.md")
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	before := readFileT(t, file)
	if !strings.Contains(before, "client-kb") || repoSkill(repo, "core-kb") {
		t.Fatalf("setup: the hand-bound KB is not in the block or core-kb is already bound:\n%s", before)
	}
	e.srv.set(func(s *lineageServer) { s.relations["client-kb"] = closureOf("core-kb") })
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := readFileT(t, file); got != before {
		t.Errorf("the tracked file changed when a relation appeared:\n%s", got)
	}
	if !repoSkill(repo, "core-kb") {
		t.Error("the lineage KB's skill did not arrive")
	}
	e.srv.set(func(s *lineageServer) { s.relations["client-kb"] = closureOf() })
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := readFileT(t, file); got != before {
		t.Errorf("the tracked file changed when the relation went:\n%s", got)
	}
	// status and sync agree: nothing is pending after the sync.
	if out := gitIn(t, repo, "status", "--porcelain", "CLAUDE.md"); out != "" && !strings.Contains(out, "CLAUDE.md") {
		t.Errorf("unexpected status %q", out)
	}
}

// AC-lineage-32: an untracked destination carries the lineage KB's section.
func TestUntrackedInstructionsCarryLineage(t *testing.T) {
	e, repo := trackedLineageEnv(t)
	e.srv.set(func(s *lineageServer) { s.relations["client-kb"] = closureOf("core-kb") })
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := readFileT(t, filepath.Join(repo, "CLAUDE.md")); !strings.Contains(got, "core-kb") {
		t.Errorf("the block does not name the lineage KB:\n%s", got)
	}
}

// AC-lineage-33: a derived binding with lineage writes nothing into the
// tracked file; Claude Code's CLAUDE.local.md carries the lineage KB.
func TestDerivedLineageInstructions(t *testing.T) {
	e, repo := workspaceLineageEnv(t)
	e.srv.set(func(s *lineageServer) { s.instructions = true })
	tracked := cloneOf(t, "example.com/owner/worked-on", "CLAUDE.md")
	e.cfg.Paths = map[string]string{"worked-on": tracked}
	if err := clientconfig.Save(e.dir, e.cfg); err != nil {
		t.Fatal(err)
	}
	_ = repo
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := readFileT(t, filepath.Join(tracked, "CLAUDE.md")); got != "# team rules\n" {
		t.Errorf("the tracked file was edited:\n%s", got)
	}
	if got := readFileT(t, filepath.Join(tracked, "CLAUDE.local.md")); !strings.Contains(got, "core-kb") {
		t.Errorf("CLAUDE.local.md does not carry the lineage KB:\n%s", got)
	}
}

// AC-lineage-34: a collision with a lineage KB refuses as before and says where
// the KB came from and how to opt out, for a workspace and a provider scope.
func TestLineageCollisionMessage(t *testing.T) {
	for _, scope := range []string{"workspace", "provider"} {
		var e *lineageEnv
		if scope == "workspace" {
			e, _ = trackedLineageEnv(t)
		} else {
			srv := newLineageServer(t, "client-kb", "core-kb")
			srv.relations["core-kb"] = closureOf()
			e = newLineageEnv(t, srv, claudeBound("client-kb"))
		}
		e.srv.set(func(s *lineageServer) {
			s.skillName = "shared"
			s.relations["client-kb"] = closureOf("core-kb")
		})
		stdout, stderr, err := e.syncOut(t, syncOptions{})
		all := stdout + stderr
		if err != nil {
			all += err.Error()
		}
		cmd := map[string]string{"workspace": "cartographer workspace bind claude ", "provider": "cartographer client bind claude client-kb --exclude core-kb"}[scope]
		for _, want := range []string{"claimed by", "core-kb comes by lineage from client-kb; opt out with: " + cmd, "--exclude core-kb"} {
			if !strings.Contains(all, want) {
				t.Errorf("%s: output misses %q:\n%s", scope, want, all)
			}
		}
	}
}

// The environment lines of a block written into a tracked file leave out the
// lineage KB and its artifacts' names; an untracked destination names them.
func TestTrackedBlockEnvironmentLeavesOutLineageKBs(t *testing.T) {
	for _, tracked := range []bool{true, false} {
		var e *lineageEnv
		var repo string
		if tracked {
			e, repo = trackedLineageEnv(t, "CLAUDE.md")
		} else {
			e, repo = trackedLineageEnv(t)
		}
		e.srv.set(func(s *lineageServer) {
			s.agents = true
			s.relations["client-kb"] = closureOf("core-kb")
		})
		if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
			t.Fatal(err)
		}
		got := readFileT(t, filepath.Join(repo, "CLAUDE.md"))
		if !strings.Contains(got, "agent-client-kb") {
			t.Errorf("tracked=%v: the hand-bound KB's agent is not named:\n%s", tracked, got)
		}
		named := strings.Contains(got, "core-kb") || strings.Contains(got, "agent-core-kb")
		if tracked && named {
			t.Errorf("the tracked block names the lineage KB or its agent:\n%s", got)
		}
		if !tracked && !strings.Contains(got, "agent-core-kb") {
			t.Errorf("the untracked block does not name the lineage agent:\n%s", got)
		}
		if _, err := os.Stat(filepath.Join(repo, ".claude", "agents", "agent-core-kb.md")); err != nil {
			t.Errorf("tracked=%v: the lineage agent is not installed: %v", tracked, err)
		}
	}
}

// A directive collision with a lineage KB (untracked destination, both
// instructions declare the key) ends with the lineage hint like a name does.
func TestLineageDirectiveCollisionHint(t *testing.T) {
	e, _ := trackedLineageEnv(t)
	e.srv.set(func(s *lineageServer) {
		s.directives = true
		s.relations["client-kb"] = closureOf("core-kb")
	})
	stdout, stderr, err := e.syncOut(t, syncOptions{})
	if err == nil {
		t.Fatalf("no collision:\n%s%s", stdout, stderr)
	}
	all := stdout + stderr + err.Error()
	for _, want := range []string{"directive key", "core-kb comes by lineage from client-kb; opt out with: cartographer workspace bind claude "} {
		if !strings.Contains(all, want) {
			t.Errorf("output misses %q:\n%s", want, all)
		}
	}
}

// A lineage KB's skill is expanded with the KB's own declared default, and its
// pull issues are reported, though its instructions stay out of a tracked file.
func TestTrackedDestinationKeepsLineageRegistryAndIssues(t *testing.T) {
	e, repo := trackedLineageEnv(t, "CLAUDE.md")
	home, _ := os.UserHomeDir()
	if err := os.MkdirAll(filepath.Join(home, "lin-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.srv.set(func(s *lineageServer) {
		s.relations["client-kb"] = closureOf("core-kb")
		s.registry["core-kb"] = map[string]any{"paths": map[string]any{"lin": map[string]any{"description": "d", "default": "~/lin-dir"}}}
		s.skillText = map[string]string{"core-kb": "see {{path:lin}}\n"}
		s.issues = map[string][]string{"core-kb": {"an artifact of core-kb was left out"}}
	})
	stdout, stderr, err := e.syncOut(t, syncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := readFileT(t, filepath.Join(repo, ".claude", "skills", "skill-core-kb", "SKILL.md"))
	if !strings.Contains(got, filepath.Join(home, "lin-dir")) || strings.Contains(got, "{{path:lin}}") {
		t.Errorf("the lineage skill was not expanded with its KB's default:\n%s", got)
	}
	if !strings.Contains(stdout+stderr, "an artifact of core-kb was left out") {
		t.Errorf("the lineage KB's pull issue is silent:\n%s%s", stdout, stderr)
	}
	if strings.Contains(readFileT(t, filepath.Join(repo, "CLAUDE.md")), home) {
		t.Error("the tracked block names a path of this machine")
	}
}
