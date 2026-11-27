package provisioning

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// #2449: a CLAUDE.md that links to AGENTS.md is AGENTS.md: no @AGENTS.md import.
func TestNeedsAgentsMDImport_LinkedClaudeMD(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := ApplyOptions{Provider: configurator.ProviderClaudeCode, Scope: contract.ScopeProject, BaseDir: dir}
	if !needsAgentsMDImport(opts) {
		t.Fatal("a plain project with AGENTS.md wants the import")
	}
	if err := os.Symlink("AGENTS.md", filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if needsAgentsMDImport(opts) {
		t.Error("CLAUDE.md linked to AGENTS.md must not import itself")
	}
}
