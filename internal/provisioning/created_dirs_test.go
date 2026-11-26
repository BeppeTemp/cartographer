package provisioning

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

func ledgerDirs(t *testing.T, base string) []string {
	t.Helper()
	var out []string
	for _, d := range configurator.RootDirs() {
		if configurator.WasDirCreated(base, d) {
			out = append(out, d)
		}
	}
	return out
}

func ledgerPresent(base string) bool {
	_, err := os.Lstat(filepath.Join(base, configurator.CreatedLedgerName))
	return err == nil
}

func skillManifest() contract.Manifest {
	return contract.Manifest{
		Revision: "r",
		Artifacts: []contract.Artifact{{
			Kind: "skill", Name: "demo", Source: "kb:test", ContentHash: "h", Signed: true,
			Files: []contract.ArtifactFile{{Path: "SKILL.md", Content: []byte("---\nname: demo\ndescription: d\n---\n")}},
		}},
	}
}

func applySkill(t *testing.T, base string) AppliedResult {
	t.Helper()
	res, err := Apply(skillManifest(), ApplyOptions{Provider: configurator.ProviderClaudeCode, BaseDir: base, Lock: Lock{}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return res
}

// AC-sdc-5
func TestSkillWriteRecordsCreatedRoot(t *testing.T) {
	base := t.TempDir()
	applySkill(t, base)
	got := ledgerDirs(t, base)
	if len(got) != 1 || got[0] != ".claude" {
		t.Fatalf("ledger dirs = %v, want only .claude (no lower level)", got)
	}
	if _, err := os.Stat(filepath.Join(base, ".claude", "skills", "demo", "SKILL.md")); err != nil {
		t.Fatalf("skill not written: %v", err)
	}

	existing := t.TempDir()
	if err := os.Mkdir(filepath.Join(existing, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	applySkill(t, existing)
	if got := ledgerDirs(t, existing); len(got) != 0 {
		t.Fatalf("a root that existed was recorded: %v", got)
	}
}

// AC-sdc-7
func TestUnrecordedCreators(t *testing.T) {
	docs := t.TempDir()
	if err := mkdirAllNoFollow(docs, filepath.Join(docs, "Cline", "Hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if ledgerPresent(docs) {
		t.Fatal("a Cline hook directory in Documents was recorded")
	}
	repo := t.TempDir()
	if err := mkdirAllNoFollow(repo, filepath.Join(repo, ".git", "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	if ledgerPresent(repo) {
		t.Fatal(".git/info was recorded")
	}
	// A nested root is recorded by its own path: .config is made too.
	home := t.TempDir()
	if err := mkdirAllNoFollow(home, filepath.Join(home, ".config", "opencode", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := ledgerDirs(t, home)
	if len(got) != 2 || got[0] != ".config" || got[1] != ".config/opencode" {
		t.Fatalf("ledger dirs = %v, want .config and .config/opencode", got)
	}
}

// AC-sdc-8
func TestSymlinkedRootRefusedRecordsNothing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	base := t.TempDir()
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(base, ".claude")); err != nil {
		t.Skip(err)
	}
	res := applySkill(t, base)
	if len(res.Refused) == 0 {
		t.Fatal("a symlinked .claude was not refused")
	}
	if ledgerPresent(base) {
		t.Fatal("a refused root was recorded")
	}
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Fatalf("the link target was written: %v", entries)
	}
}
