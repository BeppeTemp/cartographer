package provisioning_test

// Project-local cells for Antigravity and Hermes, and co-owned paths (D364).

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func demoSkill() provisioning.Artifact {
	return provisioning.Artifact{
		Kind: "skill", Name: "demo", Source: "bundle", ContentHash: "h1", Signed: true, BuiltIn: true,
		Files: []provisioning.ArtifactFile{{Path: "SKILL.md", Content: []byte("---\nname: demo\ndescription: A demo\n---\nBody.\n")}},
	}
}

// All five Antigravity surfaces land in the workspace's .agents/ layer and
// AGENTS.md, nothing in ~/.gemini, and prune undoes exactly that.
func TestApplyAndPrune_AntigravityProjectScope(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "guard", "PreToolUse", "bash", "./notify.sh")
	writeMCPFixture(t, kbRoot, "docs", `{"type":"http","url":"https://example.com/mcp"}`)
	skillDir := filepath.Join(kbRoot, "skills", "demo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo\ndescription: A demo\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(kbRoot, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kbRoot, "agents", "reviewer.md"), []byte("---\nname: reviewer\ndescription: Reviews\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, mcpAllow("docs", "https://example.com/mcp"))
	if err != nil {
		t.Fatal(err)
	}
	baseDir := t.TempDir()
	mcp := findMCPArtifact(t, m, "docs")
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		AutoTrust: true, KBRoots: map[string]string{"kb": kbRoot},
		ApprovedMCP: map[string]string{mcp.Source + "\x00docs": mcp.ContentHash},
		Provider:    configurator.ProviderAntigravity, BaseDir: baseDir, Scope: provisioning.ScopeProject,
		Lock: provisioning.Lock{},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Built from segments: a slash path in a string literal here would be read
	// as a citation of a repository document (TestEveryCitedDocumentationPathExists).
	for _, rel := range [][]string{
		{".agents", "skills", "demo", "SKILL.md"}, {".agents", "agents", "reviewer.md"},
		{".agents", "hooks", "guard", "hook.json"}, {".agents", "hooks.json"}, {".agents", "mcp_config.json"}, {"AGENTS.md"},
	} {
		if !exists(filepath.Join(append([]string{baseDir}, rel...)...)) {
			t.Errorf("%s was not written", filepath.Join(rel...))
		}
	}
	if exists(filepath.Join(baseDir, ".gemini")) {
		t.Error("a project projection wrote into the global antigravity directory")
	}
	if readJSONFile(t, filepath.Join(baseDir, ".agents", "mcp_config.json"))["mcpServers"].(map[string]any)["docs"] == nil {
		t.Error("mcp entry missing from .agents/mcp_config.json")
	}
	entry := readJSONFile(t, filepath.Join(baseDir, ".agents", "hooks.json"))["cartographer-guard"].(map[string]any)
	group := entry["PreToolUse"].([]any)[0].(map[string]any)
	cmd := group["hooks"].([]any)[0].(map[string]any)["command"]
	if want := filepath.Join(baseDir, ".agents", "hooks", "guard", "notify.sh"); cmd != want {
		t.Errorf("hook command = %v, want %v", cmd, want)
	}

	if _, err := provisioning.PruneManaged(res.NewLock.Managed, baseDir, false); err != nil {
		t.Fatal(err)
	}
	for _, rel := range [][]string{{".agents", "hooks.json"}, {".agents", "mcp_config.json"}, {".agents", "skills"}, {".agents", "hooks"}, {"AGENTS.md"}} {
		if exists(filepath.Join(append([]string{baseDir}, rel...)...)) {
			t.Errorf("%s survived the prune", filepath.Join(rel...))
		}
	}
}

// A user's own entries in the project hooks.json survive both directions.
func TestAntigravityProjectHooksFileKeepsTheUsersEntries(t *testing.T) {
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "guard", "Stop", "", "./notify.sh")
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	baseDir := t.TempDir()
	hooks := filepath.Join(baseDir, ".agents", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooks), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hooks, []byte(`{"mine":{"enabled":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		AutoTrust: true, KBRoots: map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderAntigravity, BaseDir: baseDir, Scope: provisioning.ScopeProject, Lock: provisioning.Lock{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := readJSONFile(t, hooks); got["mine"] == nil || got["cartographer-guard"] == nil {
		t.Fatalf("hooks.json = %v", got)
	}
	if _, err := provisioning.PruneManaged(res.NewLock.Managed, baseDir, false); err != nil {
		t.Fatal(err)
	}
	if got := readJSONFile(t, hooks); got["mine"] == nil || got["cartographer-guard"] != nil {
		t.Fatalf("after prune hooks.json = %v", got)
	}
}

// A Hermes workspace skill is written to .hermes/skills like any other
// client's, not delivered to an inbox: no SOURCE.md claiming "nothing is active".
func TestApply_HermesProjectSkillIsWrittenNotDelivered(t *testing.T) {
	m := provisioning.MergeArtifacts([]provisioning.Artifact{demoSkill()})
	baseDir := t.TempDir()
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		AutoTrust: true, Provider: configurator.ProviderHermes, BaseDir: baseDir, Scope: provisioning.ScopeProject, Lock: provisioning.Lock{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(baseDir, ".hermes", "skills", "demo", "SKILL.md")) {
		t.Fatalf("skill not written: %+v", res.NewLock.Managed)
	}
	if exists(filepath.Join(baseDir, ".hermes", "skills", "demo", "SOURCE.md")) || exists(filepath.Join(baseDir, "skill-inbox")) {
		t.Error("a workspace skill was treated as an inbox delivery")
	}
}

// Codex and Antigravity both project .agents/skills/<n>/ into one workspace:
// one of them leaving must not delete what the other still records.
func TestApply_CoOwnedSkillSurvivesTheOtherProvidersPrune(t *testing.T) {
	m := provisioning.MergeArtifacts([]provisioning.Artifact{demoSkill()})
	ws := t.TempDir()
	var lf provisioning.LockFile
	for _, p := range []configurator.Provider{configurator.ProviderCodex, configurator.ProviderAntigravity} {
		res, err := provisioning.Apply(m, provisioning.ApplyOptions{
			AutoTrust: true, Provider: p, BaseDir: ws, Scope: provisioning.ScopeProject, Lock: provisioning.Lock{},
		})
		if err != nil {
			t.Fatal(err)
		}
		lf.SetProjection(provisioning.Projection{Provider: string(p), Workspace: ws}, res.NewLock, "")
	}
	skill := filepath.Join(ws, ".agents", "skills", "demo", "SKILL.md")
	codex := provisioning.Projection{Provider: "codex", Workspace: ws}

	co := lf.CoOwnedPaths(codex)
	if !co[path.Join(".agents", "skills", "demo", "SKILL.md")] {
		t.Fatalf("co-owned = %v", co)
	}
	// The skill leaves the KB: codex's sync would prune it.
	if _, err := provisioning.Apply(provisioning.Manifest{}, provisioning.ApplyOptions{
		AutoTrust: true, Provider: configurator.ProviderCodex, BaseDir: ws, Scope: provisioning.ScopeProject,
		Lock: lf.ForProjection(codex), CoOwnedPaths: co,
	}); err != nil {
		t.Fatal(err)
	}
	if !exists(skill) {
		t.Fatal("codex's prune deleted a skill antigravity still records")
	}
	// Control: with nobody else recording it, the same prune removes it.
	if _, err := provisioning.Apply(provisioning.Manifest{}, provisioning.ApplyOptions{
		AutoTrust: true, Provider: configurator.ProviderCodex, BaseDir: ws, Scope: provisioning.ScopeProject,
		Lock: lf.ForProjection(codex),
	}); err != nil {
		t.Fatal(err)
	}
	if exists(skill) {
		t.Error("control: an unguarded prune should remove the skill")
	}

	// The orphan path (unbind) filters the same way.
	for _, mf := range provisioning.WithoutCoOwned(lf.ForProjection(codex).Managed, co) {
		if strings.HasSuffix(mf.Path, "SKILL.md") {
			t.Errorf("WithoutCoOwned kept %s in the prune set", mf.Path)
		}
	}
	if len(lf.CoOwnedPaths(provisioning.Projection{Provider: "codex"})) != 0 {
		t.Error("the global projection shares nothing")
	}
}
