package configurator

import (
	"os"
	"path/filepath"
	"testing"
)

func writeLedgerRaw(t *testing.T, base, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(base, CreatedLedgerName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ledgerExists(base string) bool {
	_, err := os.Lstat(filepath.Join(base, CreatedLedgerName))
	return err == nil
}

// AC-sdc-1
func TestNoteCreatedDirs(t *testing.T) {
	base := t.TempDir()
	if err := NoteCreatedDirs(base, []string{".claude", "somewhere", ".config/opencode", ".claude"}); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]bool{".claude": true, ".config/opencode": true, "somewhere": false, ".config": false} {
		if got := WasDirCreated(base, rel); got != want {
			t.Errorf("WasDirCreated(%q) = %v, want %v", rel, got, want)
		}
	}
	if got := readCreated(base).Dirs; len(got) != 2 {
		t.Errorf("Dirs = %v, want two unique entries", got)
	}
	if err := ForgetCreatedDir(base, ".claude"); err != nil {
		t.Fatal(err)
	}
	if WasDirCreated(base, ".claude") || !WasDirCreated(base, ".config/opencode") {
		t.Errorf("forget touched the wrong entry: %v", readCreated(base).Dirs)
	}
}

// AC-sdc-2
func TestHostileLedgerDirsAreDropped(t *testing.T) {
	base := t.TempDir()
	writeLedgerRaw(t, base, `{"files":{},"dirs":["../x","/etc",".ssh","",".claude/../.ssh",".claude/skills",".config/../..",".claude"]}`)
	got := readCreated(base).Dirs
	if len(got) != 1 || got[0] != ".claude" {
		t.Fatalf("Dirs = %v, want only .claude", got)
	}
	for _, rel := range []string{"../x", "/etc", ".ssh", "", ".claude/skills"} {
		if WasDirCreated(base, rel) {
			t.Errorf("WasDirCreated(%q) = true", rel)
		}
	}
}

// AC-sdc-3
func TestLedgerFileGoesWithLastEntry(t *testing.T) {
	base := t.TempDir()
	if err := NoteOwner(base, ".claude.json", "srv", true); err != nil {
		t.Fatal(err)
	}
	if err := NoteCreatedDirs(base, []string{".claude"}); err != nil {
		t.Fatal(err)
	}
	if err := ForgetCreated(base, ".claude.json"); err != nil {
		t.Fatal(err)
	}
	if !ledgerExists(base) || !WasDirCreated(base, ".claude") {
		t.Fatal("ledger with only dirs must be kept")
	}
	if err := ForgetCreatedDir(base, ".claude"); err != nil {
		t.Fatal(err)
	}
	if ledgerExists(base) {
		t.Fatal("ledger must go with its last entry")
	}
	// the other way round: files left, dirs gone
	_ = NoteCreatedDirs(base, []string{".claude"})
	_ = NoteOwner(base, ".claude.json", "srv", true)
	_ = ForgetCreatedDir(base, ".claude")
	if !ledgerExists(base) {
		t.Fatal("ledger with only files must be kept")
	}
	_ = ForgetCreated(base, ".claude.json")
	if ledgerExists(base) {
		t.Fatal("ledger must go with its last entry")
	}
	// a ledger without dirs reads and rewrites unchanged
	writeLedgerRaw(t, base, "{\"files\":{\"a.json\":[\"x\"]}}\n")
	if err := DropOwner(base, "a.json", "y"); err != nil {
		t.Fatal(err)
	}
	if got := readCreated(base); len(got.Dirs) != 0 || len(got.Files["a.json"]) != 1 || got.Files["a.json"][0] != "x" {
		t.Fatalf("ledger changed: %+v", got)
	}
}

// AC-sdc-4
func TestReconcileCreatedDirs(t *testing.T) {
	base := t.TempDir()
	for _, d := range []string{".claude", ".codex", ".kiro", ".pi"} {
		_ = os.Mkdir(filepath.Join(base, d), 0o755)
	}
	_ = os.Remove(filepath.Join(base, ".codex"))
	_ = os.WriteFile(filepath.Join(base, ".codex"), []byte("x"), 0o644) // replaced by a file
	_ = os.Remove(filepath.Join(base, ".kiro"))
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(base, ".kiro")); err != nil { // replaced by a link
		t.Skip("symlinks unavailable:", err)
	}
	_ = os.Remove(filepath.Join(base, ".pi")) // removed
	if err := NoteCreatedDirs(base, []string{".claude", ".codex", ".kiro", ".pi"}); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileCreatedDirs(base); err != nil {
		t.Fatal(err)
	}
	got := readCreated(base).Dirs
	if len(got) != 1 || got[0] != ".claude" {
		t.Fatalf("Dirs = %v, want only .claude", got)
	}
}
