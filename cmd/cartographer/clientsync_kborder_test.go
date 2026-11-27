package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

func kbOrderInstructions(name string) contract.Artifact {
	body := name + " section.\n"
	return contract.Artifact{
		Kind: "instructions", Name: name, Source: "kb:" + name, Signed: true,
		ContentHash: fmt.Sprintf("%x", sha256.Sum256([]byte(body))),
		Files:       []contract.ArtifactFile{{Path: "instructions.md", Content: []byte(body)}},
	}
}

// projectionKBOrder: a workspace projection follows its folder binding, a
// global one the provider's binding (D182) (#1809).
func TestProjectionKBOrder(t *testing.T) {
	global := map[string][]string{"codex": {"kb-b", "kb-a"}}
	ws := syncProjection{Provider: "codex", Workspace: "/ws", KBs: []string{"kb-a", "kb-b"}}
	if got := projectionKBOrder(ws, global); !reflect.DeepEqual(got, []string{"kb-a", "kb-b"}) {
		t.Errorf("workspace projection order = %v, want the folder binding's", got)
	}
	gl := syncProjection{Provider: "codex"}
	if got := projectionKBOrder(gl, global); !reflect.DeepEqual(got, []string{"kb-b", "kb-a"}) {
		t.Errorf("global projection order = %v, want the provider binding's", got)
	}
}

// Codex and OpenCode bound to one folder with the same KBs but different
// global orders write the same AGENTS.md: the second sync writes nothing.
func TestSharedWorkspaceAgentsMD_NotRewrittenAcrossProviders(t *testing.T) {
	dir := t.TempDir()
	ws := t.TempDir()
	m := contract.MergeArtifacts([]contract.Artifact{kbOrderInstructions("kb-a"), kbOrderInstructions("kb-b"), kbOrderInstructions("kb-c")})
	mk := func(p string) syncProjection {
		return syncProjection{Provider: p, Workspace: ws, BaseDir: ws, KBs: []string{"kb-a", "kb-b", "kb-c"}, Scope: contract.ScopeProject}
	}
	ps := []syncProjection{mk("codex"), mk("opencode")}
	manifests := map[string]contract.Manifest{projectionKey(ps[0]): m, projectionKey(ps[1]): m}
	kbOrder := map[string][]string{"codex": {"kb-c", "kb-a", "kb-b"}, "opencode": {"kb-b", "kb-c", "kb-a"}}

	run := func() string {
		res, err := materializeForProviders(manifests, ps, dir, "", false, false, portabilityOptions{}, kbOrder)
		if err != nil {
			t.Fatalf("materialize: %v", err)
		}
		var wrote []string
		for k, r := range res {
			for _, w := range r.Written {
				if w.Kind == "instructions" {
					wrote = append(wrote, k)
				}
			}
		}
		return strings.Join(wrote, ",")
	}
	if run() == "" {
		t.Fatal("first sync wrote no instructions")
	}
	if got := run(); got != "" {
		t.Errorf("second sync rewrote the shared file for %q", got)
	}
	if _, err := os.Stat(filepath.Join(ws, "AGENTS.md")); err != nil {
		t.Fatalf("AGENTS.md: %v", err)
	}
}

// #2449: CLAUDE.md is a link to AGENTS.md (listed in allow_symlinks) and claude
// and codex are both bound to the folder: the two projections render the same
// file, and each used to overwrite the other's block on every sync.
func TestSymlinkedClaudeMD_SecondSyncWritesNothing(t *testing.T) {
	dir := t.TempDir()
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "AGENTS.md"), []byte("# repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("AGENTS.md", filepath.Join(ws, "CLAUDE.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	safewrite.SetAllowedLinks([]string{filepath.Join(ws, "CLAUDE.md")})
	t.Cleanup(safewrite.ResetAllowedLinks)

	m := contract.MergeArtifacts([]contract.Artifact{kbOrderInstructions("kb-a")})
	mk := func(p string) syncProjection {
		return syncProjection{Provider: p, Workspace: ws, BaseDir: ws, KBs: []string{"kb-a"}, Scope: contract.ScopeProject}
	}
	ps := []syncProjection{mk("claude"), mk("codex")}
	manifests := map[string]contract.Manifest{projectionKey(ps[0]): m, projectionKey(ps[1]): m}

	run := func() []string {
		res, err := materializeForProviders(manifests, ps, dir, "", false, false, portabilityOptions{}, nil)
		if err != nil {
			t.Fatalf("materialize: %v", err)
		}
		var wrote []string
		for k, r := range res {
			for _, w := range r.Written {
				if w.Kind == "instructions" {
					wrote = append(wrote, k+":"+w.Path)
				}
			}
		}
		sort.Strings(wrote)
		return wrote
	}
	if got := run(); len(got) == 0 {
		t.Fatal("first sync wrote no instructions")
	}
	first, _ := os.ReadFile(filepath.Join(ws, "AGENTS.md"))
	if got := run(); len(got) != 0 {
		t.Errorf("second sync wrote %v, want nothing", got)
	}
	second, _ := os.ReadFile(filepath.Join(ws, "AGENTS.md"))
	if string(first) != string(second) {
		t.Errorf("AGENTS.md changed between syncs:\n%s\n---\n%s", first, second)
	}
	if n := strings.Count(string(second), "cartographer:instructions:begin"); n > 1 {
		t.Errorf("managed block appears %d times", n)
	}
}

// sharedLinkEnv is a workspace whose CLAUDE.md links to AGENTS.md, with claude
// bound to kb-a and codex to kb-a and kb-b (#2449).
type sharedLinkEnv struct {
	dir, ws   string
	manifests map[string]contract.Manifest
	claude    syncProjection
	codex     syncProjection
}

func newSharedLinkEnv(t *testing.T, listed bool) sharedLinkEnv {
	t.Helper()
	e := sharedLinkEnv{dir: t.TempDir(), ws: t.TempDir()}
	if err := os.WriteFile(filepath.Join(e.ws, "AGENTS.md"), []byte("# repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("AGENTS.md", filepath.Join(e.ws, "CLAUDE.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	safewrite.ResetAllowedLinks()
	if listed {
		safewrite.SetAllowedLinks([]string{filepath.Join(e.ws, "CLAUDE.md")})
	}
	t.Cleanup(safewrite.ResetAllowedLinks)
	mk := func(p string, kbs ...string) syncProjection {
		return syncProjection{Provider: p, Workspace: e.ws, BaseDir: e.ws, KBs: kbs, Scope: contract.ScopeProject}
	}
	e.claude, e.codex = mk("claude", "kb-a"), mk("codex", "kb-a", "kb-b")
	e.manifests = map[string]contract.Manifest{
		projectionKey(e.claude): contract.MergeArtifacts([]contract.Artifact{kbOrderInstructions("kb-a")}),
		projectionKey(e.codex):  contract.MergeArtifacts([]contract.Artifact{kbOrderInstructions("kb-a"), kbOrderInstructions("kb-b")}),
	}
	return e
}

func (e sharedLinkEnv) sync(t *testing.T, ps ...syncProjection) map[string]provisioning.AppliedResult {
	t.Helper()
	res, err := materializeForProviders(e.manifests, ps, e.dir, "", false, false, portabilityOptions{}, nil)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	return res
}

func writtenInstructions(res map[string]provisioning.AppliedResult) []string {
	var out []string
	for k, r := range res {
		for _, w := range r.Written {
			if w.Kind == "instructions" {
				out = append(out, k)
			}
		}
	}
	return out
}

func (e sharedLinkEnv) agents(t *testing.T) string {
	b, err := os.ReadFile(filepath.Join(e.ws, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Providers with different KBs on one file write the union once, and the
// second sync, in either order, writes nothing.
func TestSharedInstructionsFile_MergedAndStable(t *testing.T) {
	for name, order := range map[string]func(sharedLinkEnv) []syncProjection{
		"claude first": func(e sharedLinkEnv) []syncProjection { return []syncProjection{e.claude, e.codex} },
		"codex first":  func(e sharedLinkEnv) []syncProjection { return []syncProjection{e.codex, e.claude} },
	} {
		t.Run(name, func(t *testing.T) {
			e := newSharedLinkEnv(t, true)
			ps := order(e)
			e.sync(t, ps...)
			got := e.agents(t)
			for _, want := range []string{"kb-a section.", "kb-b section."} {
				if strings.Count(got, want) != 1 {
					t.Errorf("%q appears %d times in:\n%s", want, strings.Count(got, want), got)
				}
			}
			if strings.Count(got, "cartographer:instructions:begin") != 1 {
				t.Errorf("want one block:\n%s", got)
			}
			if w := writtenInstructions(e.sync(t, ps...)); len(w) != 0 {
				t.Errorf("second sync wrote %v", w)
			}
			if e.agents(t) != got {
				t.Error("AGENTS.md changed on the second sync")
			}
			lock, err := provisioning.ReadLockFile(lockFilePath(e.dir))
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range ps {
				n := 0
				for _, mf := range lock.ForProjection(p.Key()).Managed {
					if mf.Kind == "instructions" {
						n++
					}
				}
				if n != len(p.KBs) {
					t.Errorf("%s records %d instructions entries, want %d", p.Provider, n, len(p.KBs))
				}
			}
		})
	}
}

// A link the allow set does not list is no sharing: claude is refused, codex
// writes AGENTS.md with its own content.
func TestSharedInstructionsFile_UnlistedLinkKeepsOldBehaviour(t *testing.T) {
	e := newSharedLinkEnv(t, false)
	res := e.sync(t, e.claude, e.codex)
	if len(res[projectionKey(e.claude)].Refused) == 0 {
		t.Error("claude's write through an unlisted link must be refused")
	}
	if got := e.agents(t); !strings.Contains(got, "kb-b section.") {
		t.Errorf("codex did not write AGENTS.md:\n%s", got)
	}
	if fi, err := os.Lstat(filepath.Join(e.ws, "CLAUDE.md")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Error("CLAUDE.md link was replaced")
	}
}

// Every write refused (a corrupted marker block in the shared file): nothing is
// recorded as applied for either sharer.
func TestSharedInstructionsFile_AllWritesRefusedRecordNothing(t *testing.T) {
	e := newSharedLinkEnv(t, true)
	bad := "# repo\n<!-- cartographer:instructions:begin -->\nx\n"
	if err := os.WriteFile(filepath.Join(e.ws, "AGENTS.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	res := e.sync(t, e.claude, e.codex)
	if w := writtenInstructions(res); len(w) != 0 {
		t.Errorf("wrote %v despite the corrupted block", w)
	}
	for _, p := range []syncProjection{e.claude, e.codex} {
		if len(res[projectionKey(p)].Refused) == 0 {
			t.Errorf("%s did not record its refusal", p.Provider)
		}
		for _, mf := range res[projectionKey(p)].NewLock.Managed {
			if mf.Kind == "instructions" {
				t.Errorf("%s records %s as applied", p.Provider, mf.Name)
			}
		}
	}
	if e.agents(t) != bad {
		t.Error("the corrupted file was edited")
	}
}

// One sharer leaves: the next sync leaves the block with the remaining
// provider's content.
func TestSharedInstructionsFile_DisconnectOneRecovers(t *testing.T) {
	e := newSharedLinkEnv(t, true)
	e.sync(t, e.claude, e.codex)
	e.sync(t, e.claude)
	e.sync(t, e.claude)
	got := e.agents(t)
	if !strings.Contains(got, "kb-a section.") || strings.Contains(got, "kb-b section.") || strings.Count(got, "cartographer:instructions:begin") != 1 {
		t.Errorf("block after codex left:\n%s", got)
	}
}

// Codex and opencode on one AGENTS.md whose client texts differ: the first sync
// writes one block with each client's text labelled, in either order of the
// projections the same bytes, and the second sync writes nothing.
func TestSharedInstructionsFile_CodexAndOpenCodeLabelledClientTexts(t *testing.T) {
	var contents []string
	for _, reverse := range []bool{false, true} {
		ws, dir := t.TempDir(), t.TempDir()
		agentBody := "---\nname: helper\ndescription: d\n---\nbody\n"
		agent := contract.Artifact{
			Kind: "agent", Name: "helper", Source: "kb:kb-a", Signed: true,
			ContentHash: fmt.Sprintf("%x", sha256.Sum256([]byte(agentBody))),
			Files:       []contract.ArtifactFile{{Path: "helper.md", Content: []byte(agentBody)}},
		}
		m := contract.MergeArtifacts([]contract.Artifact{kbOrderInstructions("kb-a"), agent})
		bare := contract.MergeArtifacts([]contract.Artifact{kbOrderInstructions("kb-a")})
		mk := func(p string) syncProjection {
			return syncProjection{Provider: p, Workspace: ws, BaseDir: ws, KBs: []string{"kb-a"}, Scope: contract.ScopeProject}
		}
		ps := []syncProjection{mk("codex"), mk("opencode")}
		if reverse {
			ps = []syncProjection{ps[1], ps[0]}
		}
		manifests := map[string]contract.Manifest{}
		for _, p := range ps {
			manifests[projectionKey(p)] = bare
			if p.Provider == "codex" {
				manifests[projectionKey(p)] = m // only codex has the subagent line
			}
		}
		run := func() []string {
			res, err := materializeForProviders(manifests, ps, dir, "", false, false, portabilityOptions{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			return writtenInstructions(res)
		}
		if len(run()) == 0 {
			t.Fatal("first sync wrote nothing")
		}
		b, _ := os.ReadFile(filepath.Join(ws, "AGENTS.md"))
		got := string(b)
		if !strings.Contains(got, "For codex:\nSubagents installed") || strings.Count(got, "kb-a section.") != 1 {
			t.Errorf("client texts not labelled once:\n%s", got)
		}
		if w := run(); len(w) != 0 {
			t.Errorf("second sync wrote %v", w)
		}
		contents = append(contents, strings.ReplaceAll(got, ws, "<ws>"))
	}
	if contents[0] != contents[1] {
		t.Error("the merged block depends on the order of the projections")
	}
}

// A sharer whose dry Apply fails leaves its file out of the merge: nobody gets a
// block that misses its sections while its lock claims them.
func TestSharedInstructionsFile_FailedDryApplyFailsClosed(t *testing.T) {
	e := newSharedLinkEnv(t, true)
	broken := contract.Artifact{Kind: "instructions", Name: "kb-b", Source: "kb:kb-b", Signed: true, ContentHash: "x"}
	plan := func(p syncProjection) applyPlan {
		m := e.manifests[projectionKey(p)]
		if p.Provider == "codex" {
			m = contract.Manifest{Artifacts: []contract.Artifact{broken}}
		}
		return applyPlan{projected: m, opts: provisioning.ApplyOptions{Provider: configurator.Provider(p.Provider), BaseDir: p.BaseDir, Scope: p.Scope}}
	}
	if got := sharedInstructions([]syncProjection{e.claude, e.codex}, nil, plan); len(got) != 0 {
		t.Errorf("merge computed despite a failed sharer: %v", got)
	}
}
