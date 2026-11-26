package provisioning

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
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

func mustMkdir(t *testing.T, base, rel string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(base, filepath.FromSlash(rel)), 0o755); err != nil {
		t.Fatal(err)
	}
}

func present(base, rel string) bool {
	_, err := os.Lstat(filepath.Join(base, filepath.FromSlash(rel)))
	return err == nil
}

func TestPruneRemovesRecordedEmptyRoot(t *testing.T) {
	base := t.TempDir()
	mustMkdir(t, base, ".config/opencode/skills/x")
	mustMkdir(t, base, ".claude/skills/y")
	if err := configurator.NoteCreatedDirs(base, []string{".config", ".config/opencode", ".claude"}); err != nil {
		t.Fatal(err)
	}
	got := pruneEmptyDirs(base, ".config/opencode/skills/x/SKILL.md")
	if !reflect.DeepEqual(got, []string{".config/opencode", ".config"}) {
		t.Fatalf("removed roots = %v", got)
	}
	if present(base, ".config") || !present(base, ".claude") {
		t.Fatal("wrong directories removed")
	}
	if configurator.WasDirCreated(base, ".config") || configurator.WasDirCreated(base, ".config/opencode") {
		t.Fatal("removed roots stay in the ledger")
	}
	if got := pruneEmptyDirs(base, ".claude/skills/y/SKILL.md"); !reflect.DeepEqual(got, []string{".claude"}) {
		t.Fatalf("removed roots = %v", got)
	}
	if ledgerPresent(base) {
		t.Fatal("the ledger must go with its last entry")
	}
}

// AC-sdc-11
func TestPruneKeepsNonEmptyOrLinkedRoot(t *testing.T) {
	base := t.TempDir()
	mustMkdir(t, base, ".claude/skills/x")
	if err := os.WriteFile(filepath.Join(base, ".claude", "mine.md"), []byte("user"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := configurator.NoteCreatedDirs(base, []string{".claude"}); err != nil {
		t.Fatal(err)
	}
	if got := pruneEmptyDirs(base, ".claude/skills/x/SKILL.md"); len(got) != 0 {
		t.Fatalf("removed roots = %v", got)
	}
	if !present(base, ".claude/mine.md") || !configurator.WasDirCreated(base, ".claude") {
		t.Fatal("a root with a user file must stay and stay recorded")
	}

	if runtime.GOOS == "windows" {
		return
	}
	linked := t.TempDir()
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(linked, ".claude")); err != nil {
		t.Skip(err)
	}
	if err := configurator.NoteCreatedDirs(linked, []string{".claude"}); err != nil {
		t.Fatal(err)
	}
	if got := pruneEmptyDirs(linked, ".claude/x/SKILL.md"); len(got) != 0 {
		t.Fatalf("removed roots through a link = %v", got)
	}
	if fi, err := os.Lstat(filepath.Join(linked, ".claude")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was touched: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("the target was touched: %v", err)
	}
	if !configurator.WasDirCreated(linked, ".claude") {
		t.Fatal("the entry of a refused root must be kept")
	}
}

// AC-sdc-12
func TestPruneUnknownRootNeverRemoved(t *testing.T) {
	base := t.TempDir()
	mustMkdir(t, base, ".claude/skills/x")
	mustMkdir(t, base, ".github/y")
	if got := pruneEmptyDirs(base, ".claude/skills/x/SKILL.md"); len(got) != 0 || !present(base, ".claude") {
		t.Fatalf("an unrecorded root was removed: %v", got)
	}
	if got := pruneEmptyDirs(base, ".github/y/f"); len(got) != 0 || !present(base, ".github") {
		t.Fatalf("an unrecorded root was removed: %v", got)
	}
}

// AC-sdc-12: every caller of pruneEmptyDirs surfaces what it removed; the one
// discard is removeEmptyDestDir (a directory made and undone in one delivery).
func TestEveryPruneCallerSurfacesRoots(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	bare := regexp.MustCompile(`(?m)^\s*pruneEmptyDirs\(`)
	discards := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if bare.Match(data) {
			t.Errorf("%s calls pruneEmptyDirs as a statement: its removed roots are dropped", f)
		}
		discards += strings.Count(string(data), "_ = pruneEmptyDirs(")
	}
	if discards != 1 {
		t.Errorf("%d discards of pruneEmptyDirs, want only removeEmptyDestDir's", discards)
	}
}

// AC-sdc-14
func TestHostileLedgerRemovesNothing(t *testing.T) {
	home := t.TempDir()
	mustMkdir(t, home, ".ssh")
	mustMkdir(t, home, ".claude/skills/x")
	body := `{"files":{},"dirs":["` + filepath.ToSlash(home) + `",".ssh","..","x","."]}`
	if err := os.WriteFile(filepath.Join(home, configurator.CreatedLedgerName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// .claude is not recorded (the hostile entries are all dropped on read), so it
	// stays; the entries naming $HOME and .ssh are never acted on.
	if got := pruneEmptyDirs(home, ".claude/skills/x/f"); len(got) != 0 {
		t.Fatalf("removed roots = %v", got)
	}
	if !present(home, ".ssh") || !present(home, ".claude") {
		t.Fatal("a hostile ledger removed a directory")
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatal("a hostile ledger removed the home")
	}
}
