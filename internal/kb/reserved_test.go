package kb

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// reservedSyncKB is a synced KB with a pushed seed commit and a second clone
// ("the colleague") of the same remote. withAttrs=false removes the
// .gitattributes first, as on a KB created before D311.
func reservedSyncKB(t *testing.T, withAttrs bool, seed map[string]string) (k *KB, other, branch string) {
	t.Helper()
	k, bare := setupKBWithRemote(t)
	k.GitSync = true
	k.AutoCommit = true
	if !withAttrs {
		tgit(t, k.Root, "rm", "-q", gitAttributesFile)
		tgit(t, k.Root, "commit", "-q", "-m", "drop attributes")
		k.gitattrsChecked = true // keep SyncIn from restoring it
	}
	for rel, content := range seed {
		writeRel(t, k.Root, rel, content)
	}
	if len(seed) > 0 {
		tgit(t, k.Root, "add", "-A")
		tgit(t, k.Root, "commit", "-q", "-m", "seed")
	}
	if err := k.SyncOut(); err != nil {
		t.Fatalf("seed SyncOut: %v", err)
	}
	branch, _ = gitx.Branch(k.Root)
	parent := t.TempDir()
	other = filepath.Join(parent, "other")
	gitIn(t, parent, "clone", "-q", bare, other)
	gitIn(t, other, "checkout", "-q", branch)
	return k, other, branch
}

func writeRel(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readRel(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// colleaguePush commits files in the other clone and pushes them.
func colleaguePush(t *testing.T, other, branch string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		writeRel(t, other, rel, content)
	}
	tgit(t, other, "add", "-A")
	tgit(t, other, "commit", "-q", "-m", "colleague")
	tgit(t, other, "push", "-q", "origin", branch)
}

func localCommit(t *testing.T, k *KB, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		writeRel(t, k.Root, rel, content)
	}
	tgit(t, k.Root, "add", "-A")
	tgit(t, k.Root, "commit", "-q", "-m", "local")
}

func TestInitCommitsGitAttributes(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := tgit(t, k.Root, "show", "HEAD:"+gitAttributesFile); got != logUnionAttr {
		t.Fatalf("committed .gitattributes = %q, want %q", got, logUnionAttr)
	}
}

func TestEnsureGitAttributesIdempotent(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeRel(t, k.Root, gitAttributesFile, "*.png binary") // no trailing newline
	tgit(t, k.Root, "commit", "-q", "-am", "other attributes")
	before := tgit(t, k.Root, "rev-list", "--count", "HEAD")

	k.ensureGitAttributes()
	if got := readRel(t, k.Root, gitAttributesFile); got != "*.png binary\n"+logUnionAttr+"\n" {
		t.Fatalf(".gitattributes = %q", got)
	}
	if after := tgit(t, k.Root, "rev-list", "--count", "HEAD"); after == before {
		t.Fatal("ensureGitAttributes made no commit")
	}
	if subject := tgit(t, k.Root, "log", "-1", "--format=%s"); subject != "chore: set merge=union for data/log.md" {
		t.Fatalf("commit subject = %q", subject)
	}
	if st := tgit(t, k.Root, "status", "--porcelain"); st != "" {
		t.Fatalf("tree dirty after ensureGitAttributes: %q", st)
	}

	count := tgit(t, k.Root, "rev-list", "--count", "HEAD")
	k.gitattrsChecked = false
	k.ensureGitAttributes()
	if again := tgit(t, k.Root, "rev-list", "--count", "HEAD"); again != count {
		t.Fatal("a second ensureGitAttributes committed again")
	}
}

func TestSyncIn_ConcurrentLogAppendMergesByUnion(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, other, branch := reservedSyncKB(t, true, nil)
	base := readRel(t, k.Root, "data/log.md")
	colleaguePush(t, other, branch, map[string]string{"data/log.md": "# Log\n\n- remote entry\n" + strings.TrimPrefix(base, "# Log\n\n")})
	localCommit(t, k, map[string]string{"data/log.md": "# Log\n\n- local entry\n" + strings.TrimPrefix(base, "# Log\n\n")})

	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn: %v", err)
	}
	got := readRel(t, k.Root, "data/log.md")
	if !strings.Contains(got, "- local entry") || !strings.Contains(got, "- remote entry") {
		t.Fatalf("log.md lost an entry:\n%s", got)
	}
}

func TestSyncIn_LogConflictWithoutAttributesAutoResolvedByUnion(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, other, branch := reservedSyncKB(t, false, nil)
	colleaguePush(t, other, branch, map[string]string{"data/log.md": "# Log\n\n- remote entry\n"})
	localCommit(t, k, map[string]string{"data/log.md": "# Log\n\n- local entry\n"})

	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn: %v", err)
	}
	got := readRel(t, k.Root, "data/log.md")
	if !strings.Contains(got, "- local entry") || !strings.Contains(got, "- remote entry") {
		t.Fatalf("log.md lost an entry:\n%s", got)
	}
	if subject := tgit(t, k.Root, "log", "-1", "--format=%s"); !strings.HasPrefix(subject, "auto-resolve reserved file conflict") {
		t.Fatalf("HEAD subject = %q", subject)
	}
	if err := k.SyncOut(); err != nil {
		t.Fatalf("SyncOut after auto-resolution: %v", err)
	}
}

func TestSyncIn_StructuralReservedConflictTakesTheirs(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, other, branch := reservedSyncKB(t, true, map[string]string{"data/ops/index.md": "# Ops\n"})
	colleaguePush(t, other, branch, map[string]string{"data/ops/index.md": "# Ops\n\nremote\n"})
	localCommit(t, k, map[string]string{"data/ops/index.md": "# Ops\n\nlocal\n"})

	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn: %v", err)
	}
	if got := readRel(t, k.Root, "data/ops/index.md"); got != "# Ops\n\nremote\n" {
		t.Fatalf("index.md = %q, want the remote version", got)
	}
	if cs, _ := k.ListConflicts(); len(cs) != 0 {
		t.Fatalf("registry = %+v, want empty", cs)
	}
}

func TestSyncIn_ReservedWithConceptConflictNotAutoResolved(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, other, branch := reservedSyncKB(t, false, map[string]string{"data/ops/runbook.md": "base\n"})
	colleaguePush(t, other, branch, map[string]string{"data/log.md": "# Log\n\n- remote\n", "data/ops/runbook.md": "remote\n"})
	localCommit(t, k, map[string]string{"data/log.md": "# Log\n\n- local\n", "data/ops/runbook.md": "local\n"})

	_, err := k.SyncIn()
	var rce *gitx.RebaseConflictError
	if !errors.As(err, &rce) {
		t.Fatalf("SyncIn err = %v, want a RebaseConflictError", err)
	}
	if strings.Join(rce.Files, ",") != "data/log.md,data/ops/runbook.md" {
		t.Fatalf("conflict files = %v", rce.Files)
	}
	if st := tgit(t, k.Root, "status", "--porcelain"); st != "" {
		t.Fatalf("tree dirty after the aborted rebase: %q", st)
	}
}

// TestFinalizeConflictsResolvesUnregisteredReserved: a reserved file that
// conflicted alongside a concept stays out of the registry and takes its
// automatic resolution when the concept is finalized.
func TestFinalizeConflictsResolvesUnregisteredReserved(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, other, branch := reservedSyncKB(t, false, map[string]string{"data/ops/runbook.md": "base\n"})
	colleaguePush(t, other, branch, map[string]string{"data/log.md": "# Log\n\n- remote\n", "data/ops/runbook.md": "remote\n"})
	localCommit(t, k, map[string]string{"data/log.md": "# Log\n\n- local\n", "data/ops/runbook.md": "local\n"})
	_, err := k.SyncIn()
	var rce *gitx.RebaseConflictError
	if !errors.As(err, &rce) {
		t.Fatalf("SyncIn err = %v", err)
	}
	if err := k.RegisterConflict(Conflict{ConceptID: "ops/runbook", Path: "data/ops/runbook.md", LocalSHA: rce.LocalSHA, RemoteSHA: rce.RemoteSHA, Branch: branch, Files: rce.Files}); err != nil {
		t.Fatal(err)
	}
	if err := k.RecordResolution("ops/runbook", "theirs", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := k.FinalizeConflicts(); err != nil {
		t.Fatalf("FinalizeConflicts: %v", err)
	}
	got := readRel(t, k.Root, "data/log.md")
	if !strings.Contains(got, "- local") || !strings.Contains(got, "- remote") {
		t.Fatalf("log.md = %q, want the union", got)
	}
}

// TestFinalizeConflictsReservedUnion: a reserved log.md registered with kind
// reserved is settled by strategy union.
func TestFinalizeConflictsReservedUnion(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, other, branch := reservedSyncKB(t, false, nil)
	colleaguePush(t, other, branch, map[string]string{"data/log.md": "# Log\n\n- remote\n"})
	localCommit(t, k, map[string]string{"data/log.md": "# Log\n\n- local\n"})
	if err := gitx.Fetch(k.Root, "origin"); err != nil {
		t.Fatal(err)
	}
	local := tgit(t, k.Root, "rev-parse", "HEAD")
	remote := tgit(t, k.Root, "rev-parse", "origin/"+branch)
	if err := k.RegisterConflict(Conflict{ConceptID: "data/log.md", Path: "data/log.md", Kind: ConflictKindReserved, LocalSHA: local, RemoteSHA: remote, Branch: branch, Files: []string{"data/log.md"}}); err != nil {
		t.Fatal(err)
	}
	if err := k.RecordResolution("data/log.md", "union", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := k.FinalizeConflicts(); err != nil {
		t.Fatalf("FinalizeConflicts: %v", err)
	}
	got := readRel(t, k.Root, "data/log.md")
	if !strings.Contains(got, "- local") || !strings.Contains(got, "- remote") {
		t.Fatalf("log.md = %q, want the union", got)
	}
}

func TestSyncIn_FetchFailureWithCurrentBaseProceedsDegraded(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t)
	k.GitSync = true
	if err := k.SyncOut(); err != nil {
		t.Fatalf("seed SyncOut: %v", err)
	}
	if err := os.RemoveAll(bare); err != nil {
		t.Fatal(err)
	}
	fetched, err := k.SyncIn()
	if err != nil {
		t.Fatalf("SyncIn on a current base: %v", err)
	}
	if !fetched {
		t.Fatal("SyncIn reported no fetch attempt")
	}
	s := k.GitStatusSnapshot()
	if s.State != "degraded" || !strings.HasPrefix(s.LastError, FetchFailedLocalBase) {
		t.Fatalf("status = %+v", s)
	}
	// A write's MarkPushPending keeps the degraded status visible.
	k.MarkPushPending()
	if s := k.GitStatusSnapshot(); s.State != "degraded" {
		t.Fatalf("status after MarkPushPending = %+v", s)
	}
}

func TestSyncIn_FetchFailureWithUnpushedCommitFails(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t)
	k.GitSync = true
	if err := k.SyncOut(); err != nil {
		t.Fatalf("seed SyncOut: %v", err)
	}
	localCommit(t, k, map[string]string{"data/x.md": "x\n"})
	if err := os.RemoveAll(bare); err != nil {
		t.Fatal(err)
	}
	if _, err := k.SyncIn(); err == nil || !strings.Contains(err.Error(), "SyncIn fetch") {
		t.Fatalf("SyncIn err = %v, want the fetch error", err)
	}
}

func TestSyncIn_FetchFailureWithoutTrackingRefFails(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t) // never pushed: no origin/<branch>
	k.GitSync = true
	if err := os.RemoveAll(bare); err != nil {
		t.Fatal(err)
	}
	if _, err := k.SyncIn(); err == nil || !strings.Contains(err.Error(), "SyncIn fetch") {
		t.Fatalf("SyncIn err = %v, want the fetch error", err)
	}
}
