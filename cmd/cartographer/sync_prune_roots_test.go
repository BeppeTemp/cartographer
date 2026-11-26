package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// pruneSyncResult runs the Apply of a sync that drops every managed file in rels
// (an empty manifest against a lock holding them).
func pruneSyncResult(t *testing.T, provider configurator.Provider, base string, dry bool, rels ...string) provisioning.AppliedResult {
	t.Helper()
	var managed []provisioning.ManagedFile
	for _, rel := range rels {
		full := filepath.Join(base, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		managed = append(managed, provisioning.ManagedFile{Kind: "skill", Name: filepath.Base(filepath.Dir(rel)), Path: rel, ContentHash: "h", Source: "kb:kb-a"})
	}
	res, err := provisioning.Apply(contract.Manifest{Revision: "r"}, provisioning.ApplyOptions{
		Provider: provider, BaseDir: base, DryRun: dry, Lock: provisioning.Lock{Managed: managed},
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// AC-sdc-15: a sync that prunes the last managed file under two created roots
// prints one `removed empty directory` line per root and none for a kept root;
// --dry-run prints the same roots with `would remove` and removes nothing.
func TestSyncAnnouncesEveryRemovedRoot(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, ".claude", "mine.md"), []byte("user"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := configurator.NoteCreatedDirs(base, []string{".config", ".config/opencode", ".claude"}); err != nil {
		t.Fatal(err)
	}
	const opencodeSkill = ".config/opencode/skills/x/SKILL.md"
	const claudeSkill = ".claude/skills/y/SKILL.md"

	summary := func(dry bool) string {
		var b bytes.Buffer
		results := map[string]provisioning.AppliedResult{
			"opencode": pruneSyncResult(t, configurator.ProviderOpenCode, base, dry, opencodeSkill),
			"claude":   pruneSyncResult(t, configurator.ProviderClaudeCode, base, dry, claudeSkill),
		}
		printApplySummary(&b, base, results, dry, nil)
		return b.String()
	}

	dryOut := summary(true)
	for _, root := range []string{".config/opencode", ".config"} {
		if !strings.Contains(dryOut, "[opencode] would remove empty directory "+root+"\n") {
			t.Errorf("dry run does not list %s:\n%s", root, dryOut)
		}
	}
	if strings.Contains(dryOut, "[claude] would remove empty directory") {
		t.Errorf("dry run lists a root that holds a user file:\n%s", dryOut)
	}
	if _, err := os.Stat(filepath.Join(base, ".config", "opencode")); err != nil {
		t.Fatalf("a dry run removed a root: %v", err)
	}

	out := summary(false)
	for _, root := range []string{".config/opencode", ".config"} {
		if got := strings.Count(out, "[opencode] removed empty directory "+root+"\n"); got != 1 {
			t.Errorf("real run printed %d lines for %s:\n%s", got, root, out)
		}
	}
	if strings.Count(out, "empty directory") != 2 {
		t.Errorf("want exactly two root lines (none for the kept .claude):\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(base, ".claude", "mine.md")); err != nil {
		t.Errorf("the user's file was touched: %v", err)
	}
}
