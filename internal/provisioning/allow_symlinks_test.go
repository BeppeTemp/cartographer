package provisioning_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

// A listed directory link is followed: the write lands in the target, the link
// stays, a dry run says so and writes nothing, and the lockfile holds the
// logical path (base-relative), never the resolved target.
func TestApply_ListedDirLinkIsFollowedAndTheLockStaysLogical(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	home, target := t.TempDir(), t.TempDir()
	link := filepath.Join(home, ".cline", "rules")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	m := provisioning.Manifest{Revision: "r1", Artifacts: []provisioning.Artifact{instructionsArtifact("kb-a", "KB content.\n")}}
	opts := provisioning.ApplyOptions{Provider: configurator.ProviderCline, BaseDir: home}

	// Unlisted: refused, nothing lands in the target.
	safewrite.ResetAllowedLinks()
	provisioning.Apply(m, opts)
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Fatalf("unlisted link was followed: %v", entries)
	}

	safewrite.SetAllowedLinks([]string{link})
	t.Cleanup(safewrite.ResetAllowedLinks)

	dry := opts
	dry.DryRun = true
	res, err := provisioning.Apply(m, dry)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("would write through symlink %q -> %q", link, target)
	if n := strings.Count(strings.Join(res.Warnings, "\n"), want); n != 1 {
		t.Errorf("dry-run note count = %d, want 1 (%v)", n, res.Warnings)
	}
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Fatalf("dry run wrote: %v", entries)
	}

	if _, err := provisioning.Apply(m, opts); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(target); len(entries) == 0 {
		t.Error("nothing written to the target")
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("link not preserved: %v", err)
	}
	lock, err := provisioning.ReadLock(filepath.Join(home, provisioning.LockFileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range lock.Managed {
		if filepath.IsAbs(mf.Path) || strings.Contains(mf.Path, target) {
			t.Errorf("lock entry holds a resolved path: %q", mf.Path)
		}
	}
}

// A listed ~/.claude.json link is reported by a dry run that adds an MCP entry.
func TestApply_DryRunReportsAListedMCPConfigLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	kbRoot := t.TempDir()
	writeMCPFixture(t, kbRoot, "wiki-tools", `{"type":"http","url":"https://tools.example.com/mcp"}`)
	mcp := findMCPArtifact(t, signedMCPManifest(t, kbRoot, "wiki-tools"), "wiki-tools")
	mcp.Signed = true
	home, targetDir := t.TempDir(), t.TempDir()
	target := filepath.Join(targetDir, "claude.json")
	if err := os.WriteFile(target, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".claude.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	safewrite.SetAllowedLinks([]string{link})
	t.Cleanup(safewrite.ResetAllowedLinks)
	res, err := provisioning.Apply(provisioning.Manifest{Revision: "r1", Artifacts: []provisioning.Artifact{mcp}},
		provisioning.ApplyOptions{Provider: configurator.ProviderClaudeCode, BaseDir: home, DryRun: true, KBRoots: map[string]string{"kb": kbRoot}, SkipLockWrite: true,
			ApprovedMCP: map[string]string{"kb:kb\x00wiki-tools": mcp.ContentHash}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "would write through symlink") {
		t.Errorf("no dry-run note for the MCP config link: %v", res.Warnings)
	}
	if b, _ := os.ReadFile(target); string(b) != "{}\n" {
		t.Errorf("dry run wrote the target: %q", b)
	}
}

// The bootstrap and write-findings hooks' dry run reports a listed link on
// their registration file too.
func TestSyntheticHookDryRunNotes_ReportsAListedSettingsLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	home, targetDir := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".claude", "settings.json")
	if err := os.Symlink(filepath.Join(targetDir, "settings.json"), link); err != nil {
		t.Fatal(err)
	}
	if n := provisioning.SyntheticHookDryRunNotes(home, configurator.ProviderClaudeCode); len(n) != 0 {
		t.Errorf("unlisted link produced notes: %v", n)
	}
	safewrite.SetAllowedLinks([]string{link})
	t.Cleanup(safewrite.ResetAllowedLinks)
	notes := provisioning.SyntheticHookDryRunNotes(home, configurator.ProviderClaudeCode)
	if len(notes) != 1 || !strings.Contains(notes[0], "would write through symlink") {
		t.Errorf("notes = %v, want one for the settings link", notes)
	}
}
