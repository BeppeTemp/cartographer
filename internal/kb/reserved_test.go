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
	t.Parallel()
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
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k := newTestKB(t)
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
	t.Parallel()
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
	t.Parallel()
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
	assertServerAuthoredHead(t, k)
	if err := k.SyncOut(); err != nil {
		t.Fatalf("SyncOut after auto-resolution: %v", err)
	}
}

// The auto-resolution merges the remote SHA, and a merge needs a committer
// identity even with --no-commit: on a host with no git identity it must use
// the server one (ServerGitEnv). Not parallel: it sets the process environment.
func TestSyncIn_LogConflictAutoResolvedOnHostWithoutGitIdentity(t *testing.T) {
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, other, branch := reservedSyncKB(t, false, nil)
	colleaguePush(t, other, branch, map[string]string{"data/log.md": "# Log\n\n- remote entry\n"})
	localCommit(t, k, map[string]string{"data/log.md": "# Log\n\n- local entry\n"})
	noHostGitIdentity(t)

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
}

func TestSyncIn_StructuralReservedConflictTakesTheirs(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, other, branch := reservedSyncKB(t, true, map[string]string{"data/ops/_map.md": "# Ops\n"})
	colleaguePush(t, other, branch, map[string]string{"data/ops/_map.md": "# Ops\n\nremote\n"})
	localCommit(t, k, map[string]string{"data/ops/_map.md": "# Ops\n\nlocal\n"})

	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn: %v", err)
	}
	if got := readRel(t, k.Root, "data/ops/_map.md"); got != "# Ops\n\nremote\n" {
		t.Fatalf("_map.md = %q, want the remote version", got)
	}
	if cs, _ := k.ListConflicts(); len(cs) != 0 {
		t.Fatalf("registry = %+v, want empty", cs)
	}
}

func TestSyncIn_ReservedWithConceptConflictNotAutoResolved(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	assertServerAuthoredHead(t, k)
}

// TestSyncIn_RebaseKeepsTheWriterAsAuthorAndCommitter: a sync that replays a
// local write on top of the remote keeps the writer as its author and as the
// committer of the replayed commit (D950): a forge with a membership push rule
// checks the committer too.
func TestSyncIn_RebaseKeepsTheWriterAsAuthorAndCommitter(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, other, branch := reservedSyncKB(t, true, nil)
	colleaguePush(t, other, branch, map[string]string{"data/remote.md": "remote\n"})
	writeRel(t, k.Root, "data/local.md", "local\n")
	tgit(t, k.Root, "add", "-A")
	tgit(t, k.Root, "commit", "-q", "--author", "Writer <writer@example.com>", "-m", "local write")

	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn: %v", err)
	}
	if parent := tgit(t, k.Root, "log", "-1", "--format=%s", "HEAD~1"); parent != "colleague" {
		t.Fatalf("HEAD~1 = %q, want the colleague's commit under the replayed write", parent)
	}
	want := "Writer <writer@example.com> | Writer <writer@example.com>"
	if got := tgit(t, k.Root, "log", "-1", "--format=%an <%ae> | %cn <%ce>"); got != want {
		t.Errorf("replayed write author | committer = %q, want %q", got, want)
	}
}

// assertServerAuthoredHead: a commit that is not a write (a conflict
// resolution) is authored and committed by the server identity (D904).
func assertServerAuthoredHead(t *testing.T, k *KB) {
	t.Helper()
	name, email := ServerIdentity()
	want := name + " <" + email + "> | " + name + " <" + email + ">"
	if got := tgit(t, k.Root, "log", "-1", "--format=%an <%ae> | %cn <%ce>"); got != want {
		t.Errorf("HEAD author | committer = %q, want %q", got, want)
	}
}

func TestSyncIn_FetchFailureWithCurrentBaseProceedsDegraded(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t)
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
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t)
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
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := setupKBWithRemote(t) // never pushed: no origin/<branch>
	if err := os.RemoveAll(bare); err != nil {
		t.Fatal(err)
	}
	if _, err := k.SyncIn(); err == nil || !strings.Contains(err.Error(), "SyncIn fetch") {
		t.Fatalf("SyncIn err = %v, want the fetch error", err)
	}
}

// TestSyncIn_RebaseReCommitsAServerCommitAsTheConfiguredIdentity: a commit made
// while no identity was configured (authored and committed by the server, as
// 0.25.0 did) is re-authored and re-committed as the identity configured since,
// so the forge accepts it (D950).
func TestSyncIn_RebaseReCommitsAServerCommitAsTheConfiguredIdentity(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, other, branch := reservedSyncKB(t, true, nil)
	colleaguePush(t, other, branch, map[string]string{"data/remote.md": "remote\n"})
	writeRel(t, k.Root, "data/local.md", "local\n")
	if _, err := k.CommitOp("local by the server"); err != nil {
		t.Fatal(err)
	}
	k.SetDefaultIdentity("Ada Example", "ada@example.test")
	if _, err := k.SyncIn(); err != nil {
		t.Fatalf("SyncIn: %v", err)
	}
	want := "Ada Example <ada@example.test> | Ada Example <ada@example.test>"
	if got := tgit(t, k.Root, "log", "-1", "--format=%an <%ae> | %cn <%ce>"); got != want {
		t.Errorf("replayed server commit author | committer = %q, want %q", got, want)
	}
	if got := tgit(t, k.Root, "log", "-1", "--format=%s"); got != "local by the server" {
		t.Errorf("subject = %q, the message must survive the rewrite", got)
	}
	if parent := tgit(t, k.Root, "log", "-1", "--format=%s", "HEAD~1"); parent != "colleague" {
		t.Errorf("HEAD~1 = %q, want the colleague's commit under the rewritten one", parent)
	}
}

// The pull before a write is a local rebase onto what Fetch just brought: a
// remote that has gone away between the two must not be asked again (the second
// round-trip ran outside the fetch deadline, with the KB git lock held).
func TestPullRebase_NeedsNoNetworkAfterTheFetch(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k, bare := remoteWithDefault(t)
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, filepath.Dir(other), "clone", "-q", bare, other)
	if err := os.WriteFile(filepath.Join(other, "remote.md"), []byte("r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, other, "add", "remote.md")
	gitIn(t, other, "-c", "user.name=user", "-c", "user.email=user@example.com", "commit", "-q", "-m", "remote")
	gitIn(t, other, "push", "-q", "origin", "main")
	if err := os.WriteFile(filepath.Join(k.Root, "local.md"), []byte("l\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, k.Root, "add", "local.md")
	gitIn(t, k.Root, "-c", "user.name=user", "-c", "user.email=user@example.com", "commit", "-q", "-m", "local")
	if err := gitx.Fetch(k.Root, "origin"); err != nil {
		t.Fatal(err)
	}
	gitIn(t, k.Root, "remote", "set-url", "origin", "http://127.0.0.1:1/gone.git")
	if err := k.pullRebase("origin", "main"); err != nil {
		t.Fatalf("pullRebase after the fetch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(k.Root, "remote.md")); err != nil {
		t.Fatalf("the fetched commit was not rebased onto: %v", err)
	}
}

func TestIsReservedPath_ExpandedConceptBodyIsAConcept(t *testing.T) {
	t.Parallel()
	for p, want := range map[string]bool{
		"data/log.md":               true,
		"data/index.md":             true,
		"data/ops/index.md":         true,
		"data/ops/_map.md":          true,
		"data/ops/runbook/index.md": false,
		"data/ops/note.md":          false,
	} {
		if got := IsReservedPath(p); got != want {
			t.Errorf("IsReservedPath(%q) = %v, want %v", p, got, want)
		}
	}
	if id, ok := GitPathToExpandedConceptID("data/ops/runbook/index.md"); !ok || id != "ops/runbook" {
		t.Errorf("expanded id = %q, %v", id, ok)
	}
	if _, ok := GitPathToExpandedConceptID("data/ops/index.md"); ok {
		t.Error("a map index is not an expanded concept")
	}
}

// resolveIndexConflict makes two clones edit data/m/index.md from the same base
// and returns the index after k replays its local line onto the remote's.
func resolveIndexConflict(t *testing.T, mapContract string) string {
	t.Helper()
	k, bare := remoteWithDefault(t)
	commit := func(dir, msg string) {
		gitIn(t, dir, "add", "-A")
		gitIn(t, dir, "-c", "user.name=user", "-c", "user.email=user@example.com", "commit", "-q", "-m", msg)
	}
	write := func(dir, rel, content string) {
		abs := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, filepath.Dir(other), "clone", "-q", bare, other)
	write(other, "data/m/_map.md", "---\ntype: Map\nkind: map\ntitle: M\n"+mapContract+"---\n# M\n")
	write(other, "data/m/index.md", "---\ntype: Index\ntitle: M\n---\n# M\n\n")
	commit(other, "base")
	gitIn(t, other, "push", "-q", "origin", "main")
	if err := gitx.Fetch(k.Root, "origin"); err != nil {
		t.Fatal(err)
	}
	if err := k.pullRebase("origin", "main"); err != nil {
		t.Fatal(err)
	}
	write(other, "data/m/index.md", "---\ntype: Index\ntitle: M\n---\n# M\n\n- [a](a.md)\n")
	commit(other, "remote entry")
	gitIn(t, other, "push", "-q", "origin", "main")
	write(k.Root, "data/m/index.md", "---\ntype: Index\ntitle: M\n---\n# M\n\n- [b](b.md)\n")
	commit(k.Root, "local entry")
	if err := gitx.Fetch(k.Root, "origin"); err != nil {
		t.Fatal(err)
	}
	if err := k.pullRebase("origin", "main"); err != nil {
		t.Fatalf("pullRebase: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(k.Root, "data/m/index.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Two clones adding a concept to a curated index: both lines survive. The
// remote side alone lost the local line silently (D1566).
func TestPullRebase_CuratedIndexKeepsBothSides(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	got := resolveIndexConflict(t, "")
	if !strings.Contains(got, "a.md") || !strings.Contains(got, "b.md") {
		t.Fatalf("index lost a side:\n%s", got)
	}
}

// A generated index is rewritten by every write, so the remote side is enough.
func TestPullRebase_GeneratedIndexTakesTheRemoteSide(t *testing.T) {
	t.Parallel()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	got := resolveIndexConflict(t, "index: generated\n")
	if !strings.Contains(got, "a.md") || strings.Contains(got, "b.md") {
		t.Fatalf("generated index = %q, want the remote's version", got)
	}
}
