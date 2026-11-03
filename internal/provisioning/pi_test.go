package provisioning_test

// Pi (1.5.0) as a sync target: global mcp, instructions and skills land under
// $HOME/.pi/agent, a prune removes only what the lock owns, an agent artifact is
// unsupported, and a bound workspace gets AGENTS.md only.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func TestApply_PiGlobalCells(t *testing.T) {
	kbRoot := t.TempDir()
	writeMCPFixture(t, kbRoot, "wiki-tools", `{"type":"http","url":"https://tools.example.com/mcp"}`)
	mcp := findMCPArtifact(t, signedMCPManifest(t, kbRoot, "wiki-tools"), "wiki-tools")
	mcp.Signed = true

	home := t.TempDir()
	piDir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(piDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A server and a user line Cartographer does not own.
	other := `{"mcpServers":{"mine":{"command":"x"}}}`
	if err := os.WriteFile(filepath.Join(piDir, "mcp.json"), []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(piDir, "AGENTS.md"), []byte("# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	userSkill := filepath.Join(piDir, "skills", "user-skill", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(userSkill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userSkill, []byte("user\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	agent := provisioning.Artifact{Kind: "agent", Name: "helper", Source: "bundle", BuiltIn: true, ContentHash: "a",
		Files: []provisioning.ArtifactFile{{Path: "helper.md", Content: []byte("---\nname: helper\n---\n")}}}
	m := provisioning.Manifest{Revision: "r1", Artifacts: []provisioning.Artifact{
		skillArtifact("alpha", "h1", map[string]string{"SKILL.md": "# alpha\n"}),
		instructionsArtifact("kb-a", "KB content.\n"),
		mcp, agent,
	}}
	opts := provisioning.ApplyOptions{Provider: configurator.ProviderPi, BaseDir: home, KBRoots: map[string]string{"kb": kbRoot}, SkipLockWrite: true}
	res, err := provisioning.Apply(m, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Unsupported) != 1 || res.Unsupported[0].Kind != "agent" {
		t.Errorf("Unsupported = %+v, want the agent only", res.Unsupported)
	}
	if _, err := os.Stat(filepath.Join(piDir, "skills", "alpha", "SKILL.md")); err != nil {
		t.Errorf("skill not at .pi/agent/skills/alpha: %v", err)
	}
	md, _ := os.ReadFile(filepath.Join(piDir, "AGENTS.md"))
	if !strings.Contains(string(md), "# mine") || !strings.Contains(string(md), "KB content.") {
		t.Errorf("AGENTS.md = %q, want the user text and the block", md)
	}
	cfg, _ := os.ReadFile(filepath.Join(piDir, "mcp.json"))
	if !strings.Contains(string(cfg), `"mine"`) || !strings.Contains(string(cfg), "wiki-tools") {
		t.Errorf("mcp.json = %s, want the user's server kept and ours added", cfg)
	}

	// Prune: the KB drops everything; only what the lock owns goes.
	opts.Lock = res.NewLock
	if _, err := provisioning.Apply(provisioning.Manifest{Revision: "r2"}, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(piDir, "skills", "alpha")); !os.IsNotExist(err) {
		t.Errorf("owned skill survived the prune: %v", err)
	}
	if _, err := os.Stat(userSkill); err != nil {
		t.Errorf("a skill the user owns was removed: %v", err)
	}
	md, _ = os.ReadFile(filepath.Join(piDir, "AGENTS.md"))
	if strings.Contains(string(md), "KB content.") || !strings.Contains(string(md), "# mine") {
		t.Errorf("AGENTS.md after prune = %q", md)
	}
	cfg, _ = os.ReadFile(filepath.Join(piDir, "mcp.json"))
	if strings.Contains(string(cfg), "wiki-tools") || !strings.Contains(string(cfg), `"mine"`) {
		t.Errorf("mcp.json after prune = %s", cfg)
	}
}

func TestApply_PiProjectScopeWritesAgentsMDOnly(t *testing.T) {
	ws := t.TempDir()
	m := provisioning.Manifest{Revision: "r1", Artifacts: []provisioning.Artifact{
		skillArtifact("alpha", "h1", map[string]string{"SKILL.md": "# alpha\n"}),
		instructionsArtifact("kb-a", "KB content.\n"),
	}}
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{Provider: configurator.ProviderPi, BaseDir: ws, Scope: provisioning.ScopeProject, SkipLockWrite: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws, "AGENTS.md")); err != nil {
		t.Errorf("AGENTS.md missing: %v", err)
	}
	for _, rel := range []string{".pi", ".agents"} {
		if _, err := os.Stat(filepath.Join(ws, rel)); !os.IsNotExist(err) {
			t.Errorf("%s was created in the workspace: %v", rel, err)
		}
	}
	if len(res.Unsupported) != 1 || res.Unsupported[0].Kind != "skill" {
		t.Errorf("Unsupported = %+v, want the skill", res.Unsupported)
	}
}
