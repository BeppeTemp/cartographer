package provisioning_test

// Tests for D293: a project-scope Claude CLAUDE.md imports @AGENTS.md when the
// project root has one, so creating CLAUDE.md never hides the repository's own
// instructions from the agents-md@builtin plugin.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// TestApply_ProjectClaude_ImportsAgentsMD: AGENTS.md present, no CLAUDE.md →
// the created file's block starts with @AGENTS.md.
func TestApply_ProjectClaude_ImportsAgentsMD(t *testing.T) {
	baseDir := t.TempDir()
	// Plant an AGENTS.md in the project root.
	if err := os.WriteFile(filepath.Join(baseDir, "AGENTS.md"), []byte("# Repo instructions\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := instructionsArtifact("kb-a", "KB content.\n")
	m := provisioning.MergeArtifacts([]provisioning.Artifact{a})

	if _, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Scope:    provisioning.ScopeProject,
		Lock:     provisioning.Lock{},
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(baseDir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("CLAUDE.md not created: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "cartographer:instructions:begin") {
		t.Fatal("managed block markers missing")
	}
	// The @AGENTS.md import must be inside the block, right after the begin marker.
	beginIdx := strings.Index(content, "cartographer:instructions:begin")
	blockContent := content[beginIdx:]
	if !strings.Contains(blockContent, "@AGENTS.md") {
		t.Errorf("@AGENTS.md import missing from the managed block:\n%s", content)
	}
}

// TestApply_ProjectClaude_NoImportWithoutAgentsMD: AGENTS.md absent → no import line.
func TestApply_ProjectClaude_NoImportWithoutAgentsMD(t *testing.T) {
	baseDir := t.TempDir()
	// No AGENTS.md in the project root.

	a := instructionsArtifact("kb-a", "KB content.\n")
	m := provisioning.MergeArtifacts([]provisioning.Artifact{a})

	if _, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Scope:    provisioning.ScopeProject,
		Lock:     provisioning.Lock{},
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(baseDir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("CLAUDE.md not created: %v", err)
	}
	if strings.Contains(string(data), "@AGENTS.md") {
		t.Errorf("@AGENTS.md import should not be present without AGENTS.md:\n%s", string(data))
	}
}

// TestApply_ProjectClaude_NoDuplicateImport: CLAUDE.md already importing
// @AGENTS.md outside the block → no duplicate inside.
func TestApply_ProjectClaude_NoDuplicateImport(t *testing.T) {
	baseDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(baseDir, "AGENTS.md"), []byte("# Repo instructions\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pre-existing CLAUDE.md with the user's own @AGENTS.md import (D213 pattern).
	userContent := "@AGENTS.md\n\n# My project notes\n"
	if err := os.WriteFile(filepath.Join(baseDir, "CLAUDE.md"), []byte(userContent), 0o644); err != nil {
		t.Fatal(err)
	}

	a := instructionsArtifact("kb-a", "KB content.\n")
	m := provisioning.MergeArtifacts([]provisioning.Artifact{a})

	if _, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Scope:    provisioning.ScopeProject,
		Lock:     provisioning.Lock{},
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(baseDir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if count := strings.Count(content, "@AGENTS.md"); count != 1 {
		t.Errorf("expected exactly 1 @AGENTS.md import, found %d:\n%s", count, content)
	}
	// The user's import must remain outside the block (prefix preserved).
	if !strings.HasPrefix(content, "@AGENTS.md\n") {
		t.Errorf("user's @AGENTS.md import not preserved as prefix:\n%s", content)
	}
}

// TestApply_ProjectClaude_UnbindRemovesCreatedFile: unbinding removes the file
// Cartographer created (the block was the only content).
func TestApply_ProjectClaude_UnbindRemovesCreatedFile(t *testing.T) {
	baseDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(baseDir, "AGENTS.md"), []byte("# Repo instructions\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := instructionsArtifact("kb-a", "KB content.\n")
	m := provisioning.MergeArtifacts([]provisioning.Artifact{a})

	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Scope:    provisioning.ScopeProject,
		Lock:     provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply (bind): %v", err)
	}

	claudePath := filepath.Join(baseDir, "CLAUDE.md")
	if _, err := os.Stat(claudePath); err != nil {
		t.Fatalf("CLAUDE.md should exist after bind: %v", err)
	}

	// Unbind: empty manifest, lock from the first apply.
	empty := provisioning.MergeArtifacts(nil)
	if _, err := provisioning.Apply(empty, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Scope:    provisioning.ScopeProject,
		Lock:     res.NewLock,
	}); err != nil {
		t.Fatalf("Apply (unbind): %v", err)
	}

	if _, err := os.Stat(claudePath); !os.IsNotExist(err) {
		data, _ := os.ReadFile(claudePath)
		t.Fatalf("CLAUDE.md should be removed on unbind, got: %s", string(data))
	}
}

// TestApply_UserScope_NoImport: user scope (~/.claude/CLAUDE.md) must not get
// the @AGENTS.md import, even if an AGENTS.md exists at BaseDir.
func TestApply_UserScope_NoImport(t *testing.T) {
	baseDir := t.TempDir()
	// Plant AGENTS.md in the base dir (it shouldn't matter for user scope).
	if err := os.WriteFile(filepath.Join(baseDir, "AGENTS.md"), []byte("# Repo instructions\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := instructionsArtifact("kb-a", "KB content.\n")
	m := provisioning.MergeArtifacts([]provisioning.Artifact{a})

	if _, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Scope:    provisioning.ScopeGlobal,
		Lock:     provisioning.Lock{},
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(baseDir, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatalf("CLAUDE.md not created: %v", err)
	}
	if strings.Contains(string(data), "@AGENTS.md") {
		t.Errorf("@AGENTS.md import should not be present in user scope:\n%s", string(data))
	}
}

// TestApply_ProjectClaude_AgentsMDAppearsTriggersRewrite: adding AGENTS.md
// after the initial sync rewrites the block to include the import on the next
// Apply, even when no manifest artifact changed.
func TestApply_ProjectClaude_AgentsMDAppearsTriggersRewrite(t *testing.T) {
	baseDir := t.TempDir()

	a := instructionsArtifact("kb-a", "KB content.\n")
	m := provisioning.MergeArtifacts([]provisioning.Artifact{a})

	// First apply: no AGENTS.md → no import.
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Scope:    provisioning.ScopeProject,
		Lock:     provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply 1: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(baseDir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "@AGENTS.md") {
		t.Fatal("@AGENTS.md should not be present before AGENTS.md exists")
	}

	// Now create AGENTS.md and re-apply with the SAME manifest.
	if err := os.WriteFile(filepath.Join(baseDir, "AGENTS.md"), []byte("# Repo instructions\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res2, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Scope:    provisioning.ScopeProject,
		Lock:     res.NewLock,
	})
	if err != nil {
		t.Fatalf("Apply 2: %v", err)
	}

	data, err = os.ReadFile(filepath.Join(baseDir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "@AGENTS.md") {
		t.Errorf("@AGENTS.md import should appear after AGENTS.md is created:\n%s", string(data))
	}
	if len(res2.Written) == 0 {
		t.Error("expected Written entries for the rewrite")
	}
}

// TestApply_ProjectClaude_AgentsMDDisappearsTriggersRewrite: removing AGENTS.md
// after the initial sync rewrites the block to drop the import.
func TestApply_ProjectClaude_AgentsMDDisappearsTriggersRewrite(t *testing.T) {
	baseDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(baseDir, "AGENTS.md"), []byte("# Repo instructions\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := instructionsArtifact("kb-a", "KB content.\n")
	m := provisioning.MergeArtifacts([]provisioning.Artifact{a})

	// First apply: AGENTS.md present → import in block.
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Scope:    provisioning.ScopeProject,
		Lock:     provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply 1: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(baseDir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "@AGENTS.md") {
		t.Fatal("@AGENTS.md should be present with AGENTS.md")
	}

	// Remove AGENTS.md and re-apply with the SAME manifest.
	os.Remove(filepath.Join(baseDir, "AGENTS.md"))

	if _, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Scope:    provisioning.ScopeProject,
		Lock:     res.NewLock,
	}); err != nil {
		t.Fatalf("Apply 2: %v", err)
	}

	data, err = os.ReadFile(filepath.Join(baseDir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "@AGENTS.md") {
		t.Errorf("@AGENTS.md import should be removed after AGENTS.md disappears:\n%s", string(data))
	}
}

// TestApply_ProjectCodex_NoImport: Codex writes into AGENTS.md itself; the
// import logic must not fire for non-Claude providers.
func TestApply_ProjectCodex_NoImport(t *testing.T) {
	baseDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(baseDir, "AGENTS.md"), []byte("# Repo instructions\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := instructionsArtifact("kb-a", "KB content.\n")
	m := provisioning.MergeArtifacts([]provisioning.Artifact{a})

	if _, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderCodex,
		BaseDir:  baseDir,
		Scope:    provisioning.ScopeProject,
		Lock:     provisioning.Lock{},
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(baseDir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("AGENTS.md not updated: %v", err)
	}
	content := string(data)
	// The block should be in AGENTS.md (Codex's instructions destination).
	if !strings.Contains(content, "cartographer:instructions:begin") {
		t.Fatal("managed block not found in AGENTS.md")
	}
	// But no @AGENTS.md self-import.
	beginIdx := strings.Index(content, "cartographer:instructions:begin")
	blockContent := content[beginIdx:]
	if strings.Contains(blockContent, "@AGENTS.md") {
		t.Errorf("@AGENTS.md import should not appear for Codex:\n%s", content)
	}
}
