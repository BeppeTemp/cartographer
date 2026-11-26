package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// transversalEnv is the workspace-scope lineage environment plus kb-a, a KB
// bound to no folder that the person declared transversal.
func transversalEnv(t *testing.T) *lineageEnv {
	t.Helper()
	e, _ := workspaceLineageEnv(t)
	e.srv.set(func(s *lineageServer) {
		s.mounted = append(s.mounted, "kb-a")
		s.instructions, s.agents = true, true
		s.relations["kb-a"] = closureOf("core-kb") // read by nothing: a transversal KB is no seed
	})
	e.cfg.KnownKBs = e.srv.mounted
	e.cfg.SetKBTransversal("kb-a", true)
	if err := clientconfig.Save(e.dir, e.cfg); err != nil {
		t.Fatal(err)
	}
	return e
}

// globalManaged is the managed entries of the claude global projection.
func (e *lineageEnv) globalManaged(t *testing.T) []provisioning.ManagedFile {
	t.Helper()
	lf, err := provisioning.ReadLockFile(lockFilePath(e.dir))
	if err != nil {
		t.Fatal(err)
	}
	return lf.Providers["claude"].Managed
}

// AC-transv-10: a transversal KB bound to nowhere is pulled in the same round
// as every other KB: once per sync, the manifest build reusing the prefetch.
func TestTransversalKBPulledOnce(t *testing.T) {
	e := transversalEnv(t)
	for i := 0; i < 2; i++ {
		e.srv.set(func(s *lineageServer) { s.pulls = map[string]int{} })
		if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
			t.Fatal(err)
		}
		e.srv.set(func(s *lineageServer) {
			for _, kb := range []string{"kb-a", "client-kb", "core-kb"} {
				if s.pulls[kb] != 1 {
					t.Errorf("sync %d pulled %s %d times, want once", i+1, kb, s.pulls[kb])
				}
			}
		})
	}
}

// AC-transv-11: the bundle-only projection holds the transversal KB's
// instructions and no other artifact of it; a server that does not identify
// its KBs adds nothing.
func TestBundleOnlyCarriesInstructionsOnly(t *testing.T) {
	e := transversalEnv(t)
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, mf := range e.globalManaged(t) {
		if mf.Source == "kb:kb-a" {
			kinds = append(kinds, mf.Kind)
		}
	}
	if len(kinds) != 1 || kinds[0] != "instructions" {
		t.Fatalf("kb-a entries of the global projection = %v, want its instructions only", kinds)
	}
	if !e.skillMissing("kb-a") {
		t.Error("a transversal KB's skill must not be installed")
	}
	if _, err := os.Stat(e.dir + "/.claude/agents/agent-kb-a.md"); err == nil {
		t.Error("a transversal KB's agent must not be installed")
	}
	if !strings.Contains(e.instructions(t), "section of kb-a") {
		t.Error("the instructions block does not carry kb-a's region")
	}

	// A server that names no KB: nothing of any KB is added.
	cs := candidateSet{HasBare: true}
	if got := cs.forProjection(e.cfg, syncProjection{Provider: "claude", BundleOnly: true, KBs: []string{"kb-a"}}); len(got) != 0 {
		t.Errorf("bare server: bundle-only projection holds %v", got)
	}
}

func (e *lineageEnv) skillMissing(kb string) bool { return !e.skill(kb) }

// AC-transv-12: a transversal KB's relations are not stored as lineage, and it
// is no seed of anything.
func TestTransversalKBNeverASeed(t *testing.T) {
	e := transversalEnv(t)
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.lineage()["kb-a"]; ok {
		t.Errorf("a transversal KB's relations were stored: %+v", e.lineage())
	}
	cfg, err := clientconfig.Load(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	if kbs, _ := cfg.BoundOwnKBs("claude"); strings.Join(kbs, ",") != "client-kb" {
		t.Errorf("the flag changed the provider's binding: %v", kbs)
	}
	if seeds := lineageSeedsOf(cfg, nil); seeds["kb-a"] {
		t.Error("kb-a is a lineage seed")
	}
	if _, candidates := derivationScope(cfg, []string{"claude"}); !strings.Contains(strings.Join(candidates, ","), "kb-a") {
		t.Errorf("kb-a must be a pull candidate: %v", candidates)
	}
}

// AC-transv-21: a transversal KB in skipped_kbs is held, not pulled: the
// provider's bundle and plugin updates go on, its region and lock entry stay
// byte for byte, and status reports no drift.
func TestTransversalKBInSkippedKBsIsHeld(t *testing.T) {
	e := transversalEnv(t)
	setHome(t, e.dir) // `status` reads the client directory from HOME
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	region := e.instructions(t)
	if !strings.Contains(region, "section of kb-a") {
		t.Fatal("setup: no region of kb-a")
	}
	entries := entriesOf(e.globalManaged(t), "kb:kb-a")
	e.srv.set(func(s *lineageServer) {
		s.mounted = []string{"client-kb", "core-kb"}
		s.skipped = []string{"kb-a"}
		s.pulls = map[string]int{}
	})
	out, _, err := e.syncOut(t, syncOptions{})
	if err != nil {
		t.Fatalf("a held transversal KB must not fail the sync: %v", err)
	}
	e.srv.set(func(s *lineageServer) {
		if s.pulls["kb-a"] != 0 {
			t.Errorf("a held KB was pulled %d times", s.pulls["kb-a"])
		}
	})
	if !strings.Contains(out, "held kb-a") {
		t.Errorf("no `held kb-a` line:\n%s", out)
	}
	if got := e.instructions(t); !strings.Contains(got, "section of kb-a") || got != region {
		t.Errorf("the held region changed:\n%s", got)
	}
	if got := entriesOf(e.globalManaged(t), "kb:kb-a"); got != entries || got == "" {
		t.Errorf("held lock entries = %q, want %q", got, entries)
	}
	// status judges the projection as sync applied it: no drift while held.
	statusState := func() string {
		out := withStdout(t, func() { cmdStatus([]string{"--output", "json"}) })
		var st statusSnapshot
		if err := json.Unmarshal([]byte(out), &st); err != nil {
			t.Fatalf("status json: %v\n%s", err, out)
		}
		return st.State
	}
	if st := statusState(); st != "in_sync" {
		t.Errorf("a held transversal KB reads as drift: %s", st)
	}
	// Not listed at all: the region and the entry leave.
	e.srv.set(func(s *lineageServer) { s.skipped = nil })
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.instructions(t), "section of kb-a") || entriesOf(e.globalManaged(t), "kb:kb-a") != "" {
		t.Error("a KB in neither list must leave")
	}
}

func entriesOf(managed []provisioning.ManagedFile, source string) string {
	var sb strings.Builder
	for _, mf := range managed {
		if mf.Source == source {
			sb.WriteString(mf.Kind + ":" + mf.Name + ":" + mf.Path + ":" + mf.ContentHash + "\n")
		}
	}
	return sb.String()
}

// Config.HeldKBs is runtime state both sync and status set: the two must leave
// the same transversal KBs in the pull set and the projection. sync sets
// KnownKBs to the live list; status filters the persisted one to it.
func TestTransversalKBsAgreeBetweenSyncAndStatusWhenHeld(t *testing.T) {
	newCfg := func() *clientconfig.Config {
		cfg := &clientconfig.Config{KnownKBs: []string{"kb-a", "kb-b", "kb-c"}}
		if err := cfg.SetWorkspaceScope("claude", clientconfig.ScopeWorkspace); err != nil {
			t.Fatal(err)
		}
		cfg.SetKBTransversal("kb-a", true)
		cfg.SetKBTransversal("kb-b", true)
		return cfg
	}
	live := []string{"kb-b", "kb-c"}
	skipped := []client.SkippedKB{{Name: "kb-a", Reason: "clone failed"}}
	previous := []string{"kb-a", "kb-b", "kb-c"}

	// sync: the live list is KnownKBs, HeldKBs the names heldKBs returns.
	syncCfg := newCfg()
	syncCfg.HeldKBs = heldNames(heldKBs(previous, live, skipped, declaredTransversal(syncCfg, []string{"claude"})))
	syncCfg.KnownKBs = live
	// status: the persisted list filtered to the live one.
	statusCfg := newCfg()
	statusCfg.HeldKBs = heldNames(heldKBs(statusCfg.KnownKBs, live, skipped, declaredTransversal(statusCfg, []string{"claude"})))
	statusCfg.KnownKBs = slices.DeleteFunc(slices.Clone(statusCfg.KnownKBs), func(kb string) bool { return !slices.Contains(live, kb) })

	got1, got2 := syncCfg.TransversalKBs("claude"), statusCfg.TransversalKBs("claude")
	if !slices.Equal(got1, got2) || !slices.Equal(got1, []string{"kb-b"}) {
		t.Errorf("sync %v, status %v, want [kb-b] for both", got1, got2)
	}
	if h1, h2 := syncCfg.HeldTransversalKBs("claude"), statusCfg.HeldTransversalKBs("claude"); !slices.Equal(h1, h2) || !slices.Equal(h1, []string{"kb-a"}) {
		t.Errorf("held: sync %v, status %v, want [kb-a] for both", h1, h2)
	}
}

// transversalHome is a provider in workspace scope bound to no KB, with kb-a
// declared transversal, in a client directory that is also HOME.
func transversalHome(t *testing.T, provider string) *lineageEnv {
	t.Helper()
	srv := newLineageServer(t, "kb-a")
	srv.instructions, srv.skillsLine = true, true
	e := newLineageEnv(t, srv, map[string]clientconfig.ClientBinding{provider: {KBs: []string{}, KBsSet: true}})
	setHome(t, e.dir)
	if err := e.cfg.SetWorkspaceScope(provider, clientconfig.ScopeWorkspace); err != nil {
		t.Fatal(err)
	}
	e.cfg.SetKBTransversal("kb-a", true)
	if err := clientconfig.Save(e.dir, e.cfg); err != nil {
		t.Fatal(err)
	}
	return e
}

// filesWith lists, relative to dir and sorted, the files holding text.
func filesWith(t *testing.T, dir, text string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if b, rerr := os.ReadFile(p); rerr == nil && strings.Contains(string(b), text) {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// AC-transv-13: for each client that can be in workspace scope, the region of
// the transversal KB lands, without a Skills: line, in exactly the user-level
// instructions file docs/harnesses.md names for it, and no other KB-sourced
// artifact is written.
func TestTransversalRegionInEachClientsFile(t *testing.T) {
	// Each path is the file the ledger section of that client names as its
	// user-level instructions file (docs/harnesses.md, the table of the
	// transversal KB), written with forward slashes under the client base dir.
	want := []struct{ provider, file string }{
		{"claude", ".claude/CLAUDE.md"},
		{"codex", ".codex/AGENTS.md"},
		{"opencode", ".config/opencode/AGENTS.md"},
		{"kiro", ".kiro/steering/cartographer.md"},
		{"antigravity", ".gemini/GEMINI.md"},
		{"copilot", ".copilot/copilot-instructions.md"},
		{"pi", ".pi/agent/AGENTS.md"},
		{"crush", ".config/crush/CRUSH.md"},
	}
	for _, c := range want {
		t.Run(c.provider, func(t *testing.T) {
			if cell, ok := contract.CellFor(contract.ScopeGlobal, "instructions", configurator.Provider(c.provider)); !ok || path.Join(cell.Dir...) != c.file {
				t.Fatalf("the destination matrix says %v (%v), the ledger %s", cell.Dir, ok, c.file)
			}
			e := transversalHome(t, c.provider)
			if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
				t.Fatal(err)
			}
			got := filesWith(t, e.dir, "section of kb-a")
			if len(got) != 1 || got[0] != c.file {
				t.Fatalf("the region is in %v, want only %s", got, c.file)
			}
			if b, _ := os.ReadFile(filepath.Join(e.dir, filepath.FromSlash(c.file))); strings.Contains(string(b), "Skills: skill-kb-a") {
				t.Errorf("the region names skills it does not install:\n%s", b)
			}
			lf, err := provisioning.ReadLockFile(lockFilePath(e.dir))
			if err != nil {
				t.Fatal(err)
			}
			for _, mf := range lf.Providers[c.provider].Managed {
				if mf.Source == "kb:kb-a" && mf.Kind != "instructions" {
					t.Errorf("a %s of kb-a was written: %+v", mf.Kind, mf)
				}
			}
		})
	}
}

const lonePluginProvider = `schema: 1
name: lite-cli
display_name: Lite CLI
base: crush
detect:
  binaries: [lite-cli]
mcp:
  mode: cli
paths:
  global:
    skill: {dir: "{home}/.lite-cli/skills"}
  project:
    skill: {dir: ".lite-cli/skills"}
`

// AC-transv-14: a plugin provider whose descriptor declares a global
// instructions cell gets the region there; one without the cell gets no file.
func TestTransversalRegionPluginProvider(t *testing.T) {
	home := pluginFixture(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	freshProcess(t, home)
	dir := writePlugin(t, "team-forks", map[string]string{
		"providers/fork-cli.yaml": forkProviderYAML,
		"providers/lite-cli.yaml": lonePluginProvider,
	})
	if code, out := runPlugin(t, "install", dir, "--yes"); code != 0 {
		t.Fatalf("install: %d\n%s", code, out)
	}
	registerPluginProviders(&bytes.Buffer{})

	srv := newLineageServer(t, "kb-a")
	srv.instructions, srv.skillsLine = true, true
	cfg := &clientconfig.Config{
		ServerURL: srv.URL + "/mcp", ServerName: "cartographer", KnownKBs: srv.mounted,
		Agents: []string{"fork-cli", "lite-cli"},
		Clients: map[string]clientconfig.ClientBinding{
			"fork-cli": {KBs: []string{}, KBsSet: true}, "lite-cli": {KBs: []string{}, KBsSet: true},
		},
	}
	for _, p := range cfg.Agents {
		if err := cfg.SetWorkspaceScope(p, clientconfig.ScopeWorkspace); err != nil {
			t.Fatal(err)
		}
	}
	cfg.SetKBTransversal("kb-a", true)
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if _, err := runSync(home, loaded, syncOptions{Streams: syncStreams{Out: &out, Err: &errw}}); err != nil {
		t.Fatalf("%v\n%s", err, errw.String())
	}
	got := filesWith(t, home, "section of kb-a")
	if len(got) != 1 || got[0] != ".fork-cli/FORK-CLI.md" {
		t.Errorf("the region is in %v, want only .fork-cli/FORK-CLI.md", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".lite-cli")); err == nil {
		if got := filesWith(t, filepath.Join(home, ".lite-cli"), "kb-a"); len(got) != 0 {
			t.Errorf("a provider without the cell got %v", got)
		}
	}
}

// AC-transv-16: a second sync writes nothing, and clearing the flag removes the
// region and, it being the last one, the block.
func TestTransversalSecondSyncNoOpAndClear(t *testing.T) {
	e := transversalHome(t, "claude")
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(e.dir, ".claude", "CLAUDE.md")
	first, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(file)
	lockFirst, _ := os.ReadFile(lockFilePath(e.dir))
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(file)
	fi2, _ := os.Stat(file)
	lockSecond, _ := os.ReadFile(lockFilePath(e.dir))
	if string(first) != string(second) || !fi.ModTime().Equal(fi2.ModTime()) || string(lockFirst) != string(lockSecond) {
		t.Error("the second sync rewrote the block or the lock")
	}
	cfg, err := clientconfig.Load(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.SetKBTransversal("kb-a", false)
	if err := clientconfig.Save(e.dir, cfg); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(file); strings.Contains(string(b), "section of kb-a") || strings.Contains(string(b), "cartographer:instructions:begin") {
		t.Errorf("clearing the flag left the region or the block:\n%s", b)
	}
}

// AC-transv-17: a failed pull of the transversal KB leaves the block as it was
// and is reported like any failed KB.
func TestTransversalPullFailureKeepsBlock(t *testing.T) {
	e := transversalHome(t, "claude")
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(e.dir, ".claude", "CLAUDE.md")
	before, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(before), "section of kb-a") {
		t.Fatalf("setup: %v", err)
	}
	e.srv.set(func(s *lineageServer) { s.errKB["kb-a"] = true })
	_, _, err = e.syncOut(t, syncOptions{})
	if err == nil || !strings.Contains(err.Error(), "kb-a") {
		t.Errorf("the failed pull is not reported: %v", err)
	}
	if after, _ := os.ReadFile(file); string(after) != string(before) {
		t.Errorf("the block changed:\n%s", after)
	}
}

// AC-transv-18: a KB bound to a folder and transversal keeps its full region
// (with skills) in the folder's file and gets the brief one at user level;
// status reports no drift.
func TestTransversalAndBoundKeepsFullRegion(t *testing.T) {
	srv := newLineageServer(t, "kb-b")
	srv.instructions, srv.skillsLine = true, true
	e := newLineageEnv(t, srv, map[string]clientconfig.ClientBinding{"claude": {KBs: []string{}, KBsSet: true}})
	setHome(t, e.dir)
	ws := t.TempDir()
	if err := e.cfg.BindWorkspace("claude", ws, []string{"kb-b"}); err != nil {
		t.Fatal(err)
	}
	if err := e.cfg.SetWorkspaceScope("claude", clientconfig.ScopeWorkspace); err != nil {
		t.Fatal(err)
	}
	e.cfg.SetKBTransversal("kb-b", true)
	if err := clientconfig.Save(e.dir, e.cfg); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.syncOut(t, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	user := filesWith(t, e.dir, "section of kb-b")
	folder := filesWith(t, ws, "section of kb-b")
	if len(folder) != 1 || len(user) == 0 {
		t.Fatalf("folder files %v, user-level files %v", folder, user)
	}
	if len(filesWith(t, ws, "Skills: skill-kb-b")) != 1 {
		t.Error("the folder's region lost its skills line")
	}
	if b, _ := os.ReadFile(filepath.Join(e.dir, ".claude", "CLAUDE.md")); !strings.Contains(string(b), "section of kb-b") || strings.Contains(string(b), "Skills: skill-kb-b") {
		t.Errorf("the user-level region is not the brief one:\n%s", b)
	}
	out := withStdout(t, func() { cmdStatus([]string{"--output", "json"}) })
	var st statusSnapshot
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("status json: %v\n%s", err, out)
	}
	if st.State != "in_sync" {
		t.Errorf("status = %s after the sync:\n%s", st.State, out)
	}
}
