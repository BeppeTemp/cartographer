package provisioning_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// skillArtifact builds a built-in skill with the given files, so Apply
// materializes it without an approval step.
func skillArtifact(name, hash string, files map[string]string) provisioning.Artifact {
	a := provisioning.Artifact{Kind: "skill", Name: name, ContentHash: hash, Source: "bundle", BuiltIn: true}
	for _, path := range sortedKeys(files) {
		a.Files = append(a.Files, provisioning.ArtifactFile{Path: path, Content: []byte(files[path])})
	}
	return a
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// TestApply_RemovesFilesAnArtifactNoLongerOwns is D178's core: a file dropped
// upstream used to stay on disk AND leave the lock — an orphan nothing prunes,
// nothing reports, and that an agent keeps reading because it still sits
// inside a live skill directory.
func TestApply_RemovesFilesAnArtifactNoLongerOwns(t *testing.T) {
	base := t.TempDir()
	first := provisioning.Manifest{Revision: "r1", Artifacts: []provisioning.Artifact{
		skillArtifact("alpha", "h1", map[string]string{"SKILL.md": "# alpha\n", "reference.md": "old reference\n"}),
	}}
	res, err := provisioning.Apply(first, provisioning.ApplyOptions{Provider: "claude", BaseDir: base, SkipLockWrite: true})
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	skillDir := filepath.Join(base, ".claude", "skills", "alpha")
	if _, err := os.Stat(filepath.Join(skillDir, "reference.md")); err != nil {
		t.Fatalf("the first apply did not write reference.md: %v", err)
	}

	// A file the user put in the managed directory themselves: it must
	// survive, because the removal set comes from the lock and never from a
	// directory listing.
	userFile := filepath.Join(skillDir, "my-notes.md")
	if err := os.WriteFile(userFile, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	second := provisioning.Manifest{Revision: "r2", Artifacts: []provisioning.Artifact{
		skillArtifact("alpha", "h2", map[string]string{"SKILL.md": "# alpha v2\n"}),
	}}
	res2, err := provisioning.Apply(second, provisioning.ApplyOptions{Provider: "claude", BaseDir: base, SkipLockWrite: true, Lock: res.NewLock})
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}

	if _, err := os.Stat(filepath.Join(skillDir, "reference.md")); !os.IsNotExist(err) {
		t.Errorf("the dropped file survived (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(skillDir, "SKILL.md")); err != nil {
		t.Errorf("the remaining file was removed too: %v", err)
	}
	if _, err := os.Stat(userFile); err != nil {
		t.Errorf("a user's own file inside the managed directory was deleted: %v", err)
	}

	var prunedPaths []string
	for _, mf := range res2.Pruned {
		prunedPaths = append(prunedPaths, mf.Path)
	}
	if !containsSuffix(prunedPaths, "reference.md") {
		t.Errorf("the removal was not reported: %v", prunedPaths)
	}
	for _, mf := range res2.NewLock.Managed {
		if strings.HasSuffix(mf.Path, "reference.md") {
			t.Errorf("the lock still claims the removed file: %+v", mf)
		}
	}
}

// TestApply_DryRunReportsRemovalWithoutDeleting: the plan must show it, and
// nothing may be deleted — a silent deletion is not an improvement over a
// silent orphan.
func TestApply_DryRunReportsRemovalWithoutDeleting(t *testing.T) {
	base := t.TempDir()
	first := provisioning.Manifest{Revision: "r1", Artifacts: []provisioning.Artifact{
		skillArtifact("alpha", "h1", map[string]string{"SKILL.md": "# alpha\n", "reference.md": "old\n"}),
	}}
	res, err := provisioning.Apply(first, provisioning.ApplyOptions{Provider: "claude", BaseDir: base, SkipLockWrite: true})
	if err != nil {
		t.Fatal(err)
	}

	second := provisioning.Manifest{Revision: "r2", Artifacts: []provisioning.Artifact{
		skillArtifact("alpha", "h2", map[string]string{"SKILL.md": "# alpha v2\n"}),
	}}
	res2, err := provisioning.Apply(second, provisioning.ApplyOptions{Provider: "claude", BaseDir: base, SkipLockWrite: true, DryRun: true, Lock: res.NewLock})
	if err != nil {
		t.Fatal(err)
	}
	var pruned []string
	for _, mf := range res2.Pruned {
		pruned = append(pruned, mf.Path)
	}
	if !containsSuffix(pruned, "reference.md") {
		t.Errorf("the dry-run plan hides the removal: %v", pruned)
	}
	if !containsSuffix(pruned, "SKILL.md") {
		// SKILL.md is rewritten, not removed: it must NOT be in the plan.
	} else {
		t.Errorf("a file the artifact still declares was planned for removal: %v", pruned)
	}
	if _, err := os.Stat(filepath.Join(base, ".claude", "skills", "alpha", "reference.md")); err != nil {
		t.Errorf("a dry run deleted the file: %v", err)
	}
}

func containsSuffix(paths []string, suffix string) bool {
	for _, p := range paths {
		if strings.HasSuffix(p, suffix) {
			return true
		}
	}
	return false
}
