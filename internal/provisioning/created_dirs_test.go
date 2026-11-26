package provisioning

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
	"github.com/BeppeTemp/cartographer/internal/safewrite"
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
	got := configurator.CreatedDirs(base)
	want := []string{".claude", ".claude/skills", ".claude/skills/demo"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ledger dirs = %v, want every level made: %v", got, want)
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

// AC-sdc-7: the writers outside the client base dir call safewrite directly
// (clinehooks.go, repohygiene.go) and record nothing; mkdirAllNoFollow records
// every level it makes.
func TestUnrecordedCreators(t *testing.T) {
	docs := t.TempDir()
	if err := safewrite.MkdirAllNoFollow(docs, filepath.Join(docs, "Cline", "Hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if ledgerPresent(docs) {
		t.Fatal("a Cline hook directory in Documents was recorded")
	}
	repo := t.TempDir()
	if err := safewrite.MkdirAllNoFollow(repo, filepath.Join(repo, ".git", "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	if ledgerPresent(repo) {
		t.Fatal(".git/info was recorded")
	}
	// Every level is recorded by its own path, a nested root and the ones below.
	home := t.TempDir()
	if err := mkdirAllNoFollow(home, filepath.Join(home, ".config", "opencode", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := configurator.CreatedDirs(home)
	if want := []string{".config", ".config/opencode", ".config/opencode/skills"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ledger dirs = %v, want %v", got, want)
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
	if err := configurator.NoteCreatedDirs(base, []string{".config", ".config/opencode", ".config/opencode/skills", ".config/opencode/skills/x", ".claude", ".claude/skills", ".claude/skills/y"}); err != nil {
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

// AC-sdc-12: every caller of pruneEmptyDirs surfaces what it removed. The one
// statement call left is removeEmptyDestDir's, which stops the climb at every
// root (removeRoots false), so it has nothing to announce. PruneManaged, which
// drops RemovedRoots, is called by no non-test file: they use PruneManagedReport.
func TestEveryPruneCallerSurfacesRoots(t *testing.T) {
	var files []string
	for _, glob := range []string{"*.go", filepath.Join("..", "..", "cmd", "cartographer", "*.go")} {
		m, err := filepath.Glob(glob)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	bare := regexp.MustCompile(`(?m)^\s*pruneEmptyDirs\(`)
	bareFrom := regexp.MustCompile(`(?m)^\s*pruneEmptyDirsFrom\(.*\)$`)
	dropsRoots := regexp.MustCompile(`(?m)^[^/\n]*\bPruneManaged\(`)
	fromStatements := 0
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
		for _, l := range bareFrom.FindAll(data, -1) {
			if !strings.HasSuffix(string(l), ", false)") {
				t.Errorf("%s: %s drops the roots it may remove", f, strings.TrimSpace(string(l)))
			}
			fromStatements++
		}
		for _, l := range dropsRoots.FindAll(data, -1) {
			if !strings.Contains(string(l), "func PruneManaged(") {
				t.Errorf("%s calls PruneManaged, which drops RemovedRoots: use PruneManagedReport (%s)", f, strings.TrimSpace(string(l)))
			}
		}
	}
	if fromStatements != 1 {
		t.Errorf("%d statement calls of pruneEmptyDirsFrom, want only removeEmptyDestDir's", fromStatements)
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

// AC-sdc-16: for each tree, the roots a dry run predicts are the roots the real
// prune of the same tree removes.
func TestWouldPruneRootsMatchesRealRun(t *testing.T) {
	type tree struct {
		name     string
		files    []string // managed files, created and pruned
		user     []string // files of the user, kept
		recorded []string
	}
	cases := []tree{
		{name: "recorded and emptied", files: []string{".claude/skills/x/SKILL.md"}, recorded: []string{".claude"}},
		{name: "recorded with a user file", files: []string{".claude/skills/x/SKILL.md"}, user: []string{".claude/mine.md"}, recorded: []string{".claude"}},
		{name: "unrecorded", files: []string{".claude/skills/x/SKILL.md"}},
		{name: "nested roots", files: []string{".config/opencode/skills/x/SKILL.md"}, recorded: []string{".config", ".config/opencode"}},
		{name: "outer root holds a user file", files: []string{".config/opencode/skills/x/SKILL.md"}, user: []string{".config/mine"}, recorded: []string{".config", ".config/opencode"}},
		{name: "two roots", files: []string{".claude/skills/x/SKILL.md", ".kiro/skills/y/SKILL.md"}, user: []string{".kiro/keep"}, recorded: []string{".claude", ".kiro"}},
		{name: "sibling level with a user file", files: []string{".github/a/f"}, user: []string{".github/workflows/ci.yml"}, recorded: []string{".github"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			var managed []ManagedFile
			for _, f := range tc.files {
				mustMkdir(t, base, filepath.ToSlash(filepath.Dir(f)))
				if err := os.WriteFile(filepath.Join(base, filepath.FromSlash(f)), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
				managed = append(managed, ManagedFile{Kind: "skill", Name: "n", Path: filepath.FromSlash(f)})
			}
			for _, f := range tc.user {
				mustMkdir(t, base, filepath.ToSlash(filepath.Dir(f)))
				if err := os.WriteFile(filepath.Join(base, filepath.FromSlash(f)), []byte("u"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := configurator.NoteCreatedDirs(base, tc.recorded); err != nil {
				t.Fatal(err)
			}
			want, _ := wouldPruneRoots(base, managed)
			rep, err := PruneManagedReport(managed, base, true)
			if err != nil || !reflect.DeepEqual(rep.RemovedRoots, want) {
				t.Fatalf("dry report = %v, %v; want %v", rep.RemovedRoots, err, want)
			}
			real, err := PruneManagedReport(managed, base, false)
			if err != nil {
				t.Fatal(err)
			}
			// The dry report (the walk by name) lists the roots the real prune removes, in the same order.
			if !reflect.DeepEqual(rep.RemovedRoots, real.RemovedRoots) && !(len(rep.RemovedRoots) == 0 && len(real.RemovedRoots) == 0) {
				t.Fatalf("dry report %v, real report %v", rep.RemovedRoots, real.RemovedRoots)
			}
			gotSet, wantSet := append([]string(nil), real.RemovedRoots...), append([]string(nil), want...)
			sortStrings(gotSet)
			sortStrings(wantSet)
			if !reflect.DeepEqual(gotSet, wantSet) {
				t.Fatalf("dry run predicted %v, the real run removed %v", want, real.RemovedRoots)
			}
		})
	}

	if runtime.GOOS != "windows" {
		base, target := t.TempDir(), t.TempDir()
		mustMkdir(t, target, "skills/x")
		if err := os.WriteFile(filepath.Join(target, "skills", "x", "SKILL.md"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(base, ".claude")); err != nil {
			t.Skip(err)
		}
		if err := configurator.NoteCreatedDirs(base, []string{".claude"}); err != nil {
			t.Fatal(err)
		}
		managed := []ManagedFile{{Kind: "skill", Name: "n", Path: filepath.FromSlash(".claude/skills/x/SKILL.md")}}
		if got, _ := wouldPruneRoots(base, managed); len(got) != 0 {
			t.Fatalf("a root replaced by a link was predicted: %v", got)
		}
		if real, _ := PruneManagedReport(managed, base, false); len(real.RemovedRoots) != 0 {
			t.Fatalf("the real run removed %v through a link", real.RemovedRoots)
		}
	}
}

func sortStrings(s []string) { sort.Strings(s) }

// A write-findings removal hands the roots it took out to its caller, and its
// dry run predicts them.
func TestWriteFindingsRemovalSurfacesRoots(t *testing.T) {
	base := t.TempDir()
	lock, err := EnsureWriteFindingsHook(base, configurator.ProviderClaudeCode, Lock{}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !configurator.WasDirCreated(base, ".claude") {
		t.Skip("the hook did not create .claude here")
	}
	_, _, dry, err := EnsureWriteFindingsHookNote(base, configurator.ProviderClaudeCode, lock, false, true)
	if err != nil {
		t.Fatal(err)
	}
	_, _, real, err := EnsureWriteFindingsHookNote(base, configurator.ProviderClaudeCode, lock, false, false)
	if err != nil {
		t.Fatal(err)
	}
	// The dry run may list fewer roots (the settings file holding the hook's
	// registration is not predicted gone), never one the real run keeps.
	if !reflect.DeepEqual(real, []string{".claude"}) || len(dry) > len(real) {
		t.Fatalf("dry run %v, real run %v, want the real run to remove [.claude]", dry, real)
	}
	if present(base, ".claude") {
		t.Error(".claude survived")
	}
}

// A refused delivery undoes the levels it made, never a root a user emptied
// after an earlier run recorded it.
func TestRemoveEmptyDestDirStopsAtRecordedRoot(t *testing.T) {
	base := t.TempDir()
	mustMkdir(t, base, ".claude/skills/x")
	if err := configurator.NoteCreatedDirs(base, []string{".claude", ".claude/skills", ".claude/skills/x"}); err != nil {
		t.Fatal(err)
	}
	removeEmptyDestDir(base, filepath.Join(base, ".claude", "skills", "x"))
	if !present(base, ".claude") || !configurator.WasDirCreated(base, ".claude") {
		t.Fatal("a recorded root was removed by an undo")
	}
	if present(base, ".claude/skills") {
		t.Error("the levels below the root should go")
	}
}

// A root under a symlinked parent (.config is a link) is refused by the real
// climb; the prediction refuses it the same way, so both remove nothing.
func TestWouldPruneRootsSymlinkedParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	base, target := t.TempDir(), t.TempDir()
	mustMkdir(t, target, "opencode/skills/x")
	if err := os.WriteFile(filepath.Join(target, "opencode", "skills", "x", "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(base, ".config")); err != nil {
		t.Skip(err)
	}
	if err := configurator.NoteCreatedDirs(base, []string{".config", ".config/opencode"}); err != nil {
		t.Fatal(err)
	}
	managed := []ManagedFile{{Kind: "skill", Name: "n", Path: filepath.FromSlash(".config/opencode/skills/x/SKILL.md")}}
	// The dry run reports the refusal the real run meets as well.
	dry, _ := PruneManagedReport(managed, base, true)
	if len(dry.RemovedRoots) != 0 {
		t.Fatalf("dry run predicted %v through a linked parent", dry.RemovedRoots)
	}
	real, _ := PruneManagedReport(managed, base, false)
	if len(real.RemovedRoots) != 0 {
		t.Fatalf("the real run removed %v through a linked parent", real.RemovedRoots)
	}
	if _, err := os.Stat(filepath.Join(target, "opencode")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// A recorded root that cannot be read is reported as unknown, never dropped.
func TestWouldPruneRootsUnreadableIsUnknown(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a mode 0 directory is readable here")
	}
	base := t.TempDir()
	mustMkdir(t, base, ".claude/skills/x")
	if err := os.WriteFile(filepath.Join(base, ".claude", "skills", "x", "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := configurator.NoteCreatedDirs(base, []string{".claude", ".claude/skills", ".claude/skills/x"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(base, ".claude", "skills"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(base, ".claude", "skills"), 0o755) })
	managed := []ManagedFile{{Kind: "skill", Name: "n", Path: filepath.FromSlash(".claude/skills/x/SKILL.md")}}
	rep, _ := PruneManagedReport(managed, base, true)
	if len(rep.RemovedRoots) != 0 || len(rep.UnknownRoots) != 1 || rep.UnknownRoots[0] != ".claude" {
		t.Fatalf("roots %v unknown %v, want .claude unknown", rep.RemovedRoots, rep.UnknownRoots)
	}
}

// The prediction reads each directory once per call and stops at the first
// surviving entry: a large tree under a surviving file is never walked.
func TestRootPredictorBoundedReads(t *testing.T) {
	base := t.TempDir()
	const skills = 300
	var files []string
	for i := 0; i < skills; i++ {
		rel := fmt.Sprintf(".claude/skills/s%03d/SKILL.md", i)
		mustMkdir(t, base, filepath.ToSlash(filepath.Dir(rel)))
		if err := os.WriteFile(filepath.Join(base, filepath.FromSlash(rel)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, rel)
	}
	// A surviving file sorting first, and a deep tree after it that must not be walked.
	if err := os.WriteFile(filepath.Join(base, ".claude", "0keep.md"), []byte("u"), 0o644); err != nil {
		t.Fatal(err)
	}
	deep := ".claude/zz"
	for i := 0; i < 100; i++ {
		deep += "/d"
	}
	mustMkdir(t, base, deep)
	if err := configurator.NoteCreatedDirs(base, []string{".claude"}); err != nil {
		t.Fatal(err)
	}
	reads := 0
	readDirFn = func(name string) ([]os.DirEntry, error) { reads++; return os.ReadDir(name) }
	t.Cleanup(func() { readDirFn = os.ReadDir })
	roots, unknown := NewRootPredictor(base).Predict(files)
	if len(roots) != 0 || len(unknown) != 0 {
		t.Fatalf("roots %v unknown %v, want none (the root holds a surviving file)", roots, unknown)
	}
	if reads > 3*skills {
		t.Fatalf("%d directory reads for %d files: the walk is not bounded", reads, skills)
	}
}

// A disconnect that has no config file to delete leaves an empty directory that
// held none: the dry run predicts nothing, and the real prune removes nothing.
func TestWouldPruneRootsConfigFileMustExist(t *testing.T) {
	base := t.TempDir()
	mustMkdir(t, base, ".kiro/settings")
	if err := configurator.NoteCreatedDirs(base, []string{".kiro"}); err != nil {
		t.Fatal(err)
	}
	// A hook file of kiro puts the provider in play; the config file is absent.
	managed := []ManagedFile{{Kind: "hook", Name: "h", Path: filepath.FromSlash(".kiro/hooks/h/hook.json")}}
	dry, err := PruneManagedReportWith(managed, base, true, &PredictOptions{Provider: "kiro", Whole: true, ConfigFiles: true})
	if err != nil || len(dry.RemovedRoots) != 0 {
		t.Fatalf("dry run predicted %v, %v with no config file on disk", dry.RemovedRoots, err)
	}
	real, _ := PruneManagedReport(managed, base, false)
	if len(real.RemovedRoots) != 0 || !present(base, ".kiro/settings") {
		t.Fatalf("the real run removed %v or the user's settings dir", real.RemovedRoots)
	}
}
