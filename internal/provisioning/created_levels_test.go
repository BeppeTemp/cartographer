package provisioning

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// connectKiro writes the kiro MCP config and removes it again, then runs the
// climb a disconnect makes; it returns the roots the climb removed.
func connectDisconnectKiro(t *testing.T, home string) []string {
	t.Helper()
	cfg := &configurator.ServerConfig{Name: "kb-a", URL: "https://example.com/mcp"}
	r, err := configurator.Emit(cfg, configurator.ProviderKiro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configurator.Apply([]*configurator.EmitResult{r}, home, false); err != nil {
		t.Fatal(err)
	}
	if !MCPConfigFileExists(home, configurator.ProviderKiro) {
		t.Fatal("connect wrote no kiro config")
	}
	if _, err := configurator.Remove(cfg, configurator.ProviderKiro, home, false); err != nil {
		t.Fatal(err)
	}
	return PruneConfigFileParents(home, configurator.ProviderKiro)
}

// #2403: a level that existed before the connect (the user's own empty
// .kiro/settings) survives the disconnect; the root above it, which the connect
// made, goes only because the ledger lists it, and is announced.
func TestDisconnectKeepsAPreexistingLevelBelowARoot(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".kiro", "settings"), 0o755); err != nil {
		t.Fatal(err)
	}
	if roots := connectDisconnectKiro(t, home); len(roots) != 0 {
		t.Fatalf("removed roots %v, want none: .kiro existed", roots)
	}
	if !present(home, ".kiro/settings") {
		t.Fatal("the user's empty .kiro/settings was removed")
	}
	if ledgerPresent(home) {
		t.Fatal("the ledger outlived its last entry")
	}
}

// The converse: every level the connect made is removed, the root announced.
func TestDisconnectRemovesTheLevelsTheClientMade(t *testing.T) {
	home := t.TempDir()
	roots := connectDisconnectKiro(t, home)
	if len(roots) != 1 || roots[0] != ".kiro" {
		t.Fatalf("removed roots %v, want [.kiro]", roots)
	}
	if present(home, ".kiro") || ledgerPresent(home) {
		t.Fatal("a level the client made, or its ledger, is left behind")
	}
}

// A root that existed is kept with the level the client made below it.
func TestDisconnectRemovesOnlyTheLevelBelowAPreexistingRoot(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".kiro"), 0o755); err != nil {
		t.Fatal(err)
	}
	if roots := connectDisconnectKiro(t, home); len(roots) != 0 {
		t.Fatalf("removed roots %v, want none", roots)
	}
	if !present(home, ".kiro") || present(home, ".kiro/settings") {
		t.Fatal("want .kiro kept and the settings level the client made removed")
	}
}

// The dry run and the real run agree where the ledger skips a level between
// two listed ones: .kiro/skills is not listed, so .kiro is kept by both.
func TestWouldPruneRootsAgreesWithTheLedgerOnAnUnlistedLevel(t *testing.T) {
	base := t.TempDir()
	mustMkdir(t, base, ".kiro/skills/x")
	for _, f := range []string{".kiro/skills/x/S.md", ".kiro/m.json"} {
		if err := os.WriteFile(filepath.Join(base, filepath.FromSlash(f)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := configurator.NoteCreatedDirs(base, []string{".kiro", ".kiro/skills/x"}); err != nil {
		t.Fatal(err)
	}
	managed := []ManagedFile{
		{Kind: "skill", Name: "x", Path: filepath.FromSlash(".kiro/skills/x/S.md")},
		{Kind: "skill", Name: "m", Path: filepath.FromSlash(".kiro/m.json")},
	}
	dry, _ := PruneManagedReport(managed, base, true)
	real, err := PruneManagedReport(managed, base, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(dry.RemovedRoots) != 0 || len(real.RemovedRoots) != 0 || !present(base, ".kiro/skills") || !present(base, ".kiro") {
		t.Fatalf("dry %v, real %v, want both to keep .kiro (.kiro/skills unlisted)", dry.RemovedRoots, real.RemovedRoots)
	}
}

// A hand-edited ledger entry lets a prune remove only an empty directory a
// managed path climbs through, and never a link.
func TestHandEditedLedgerEntryRemovesOnlyAnEmptyRealDirectory(t *testing.T) {
	base := t.TempDir()
	mustMkdir(t, base, ".ssh/x")
	if err := os.WriteFile(filepath.Join(base, ".ssh", "keep"), []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, ".cartographer-created.json"), []byte(`{"files":{},"dirs":[".ssh",".ssh/x"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	pruneEmptyDirs(base, ".ssh/x/gone.md")
	if present(base, ".ssh/x") || !present(base, ".ssh/keep") {
		t.Fatal("want the empty .ssh/x removed and the non-empty .ssh kept")
	}

	link, target := t.TempDir(), t.TempDir()
	mustMkdir(t, target, "x")
	if err := os.Symlink(target, filepath.Join(link, ".ssh")); err != nil {
		t.Skip(err)
	}
	if err := os.WriteFile(filepath.Join(link, ".cartographer-created.json"), []byte(`{"files":{},"dirs":[".ssh",".ssh/x"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	pruneEmptyDirs(link, ".ssh/x/gone.md")
	if fi, err := os.Lstat(filepath.Join(link, ".ssh")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was unlinked: %v", err)
	}
	if !present(target, "x") {
		t.Fatal("the target was emptied through the link")
	}
}
