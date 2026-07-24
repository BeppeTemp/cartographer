package mcpserver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// setupGitKB initialises a temp KB and verifies the initial git commit was made.
// If git is not available or not configured, the calling test is skipped.
func setupGitKB(t *testing.T) (*kb.KB, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "wiki-gitwrap-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	k, err := kb.Init(dir)
	if err != nil {
		t.Fatalf("kb.Init: %v", err)
	}
	sha, err := gitx.HeadSHA(dir)
	if err != nil {
		t.Skipf("git not available or initial commit failed: %v", err)
	}
	return k, sha
}

// TestGitWrap_ConceptWrite_CreatesCommit verifies that a successful concept_write
// via the git-wrapped tool creates a new git commit when AutoCommit=true.
func TestGitWrap_ConceptWrite_CreatesCommit(t *testing.T) {
	k, sha1 := setupGitKB(t)
	k.AutoCommit = true

	s := New("0.1.0-test")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"test/autocommit","frontmatter":{"type":"Note","title":"AutoCommit"},"body":"# Test\n"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	if len(resps) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(resps))
	}

	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("concept_write: isError=true: %v", tr.Content)
	}

	sha2, err := gitx.HeadSHA(k.Root)
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	if sha1 == sha2 {
		t.Fatal("expected a new commit after concept_write with AutoCommit=true, but HEAD SHA is unchanged")
	}
}

// TestGitWrap_ConceptWrite_FailedOp_NoCommit verifies that a failed concept_write
// (missing required 'type' field) does NOT produce a git commit.
func TestGitWrap_ConceptWrite_FailedOp_NoCommit(t *testing.T) {
	k, sha1 := setupGitKB(t)
	k.AutoCommit = true

	s := New("0.1.0-test")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		// frontmatter without 'type' → concept_write returns isError=true
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"test/fail","frontmatter":{},"body":"# Fail\n"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	if len(resps) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(resps))
	}

	tr := decodeToolResult(t, resps[1])
	if !tr.IsError {
		t.Fatal("concept_write without type: expected isError=true but got success")
	}

	sha2, err := gitx.HeadSHA(k.Root)
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	if sha1 != sha2 {
		t.Fatal("expected no commit after failed concept_write but HEAD SHA changed")
	}
}

// TestGitWrap_MapDelete_EmptyMap_CreatesCommit verifies that map_delete on an
// empty map (only the map_create scaffold, no concepts) removes the
// directory and creates a git commit (D88 WP2).
func TestGitWrap_MapDelete_EmptyMap_CreatesCommit(t *testing.T) {
	k, sha1 := setupGitKB(t)
	k.AutoCommit = true

	s := New("0.1.0-test")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"map_create","arguments":{"name":"empty-map","title":"Empty Map","kind":"map"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"map_delete","arguments":{"map":"empty-map"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	if len(resps) != 3 {
		t.Fatalf("expected 3 responses, got %d", len(resps))
	}

	if tr := decodeToolResult(t, resps[1]); tr.IsError {
		t.Fatalf("map_create: isError=true: %v", tr.Content)
	}

	trDelete := decodeToolResult(t, resps[2])
	if trDelete.IsError {
		t.Fatalf("map_delete: isError=true: %v", trDelete.Content)
	}

	if _, err := os.Stat(filepath.Join(k.DataRoot(), "empty-map")); !os.IsNotExist(err) {
		t.Errorf("map_delete: directory still present on disk, err=%v", err)
	}

	sha2, err := gitx.HeadSHA(k.Root)
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	if sha1 == sha2 {
		t.Fatal("expected a new commit after map_delete with AutoCommit=true, but HEAD SHA is unchanged")
	}
}

// setupGitKBWithRemote initialises a temp KB with a bare remote attached as
// "origin" (D76/WP4: needed to exercise the async push worker end-to-end
// through gitWrap, not just kb.SchedulePush/FlushPush directly).
func setupGitKBWithRemote(t *testing.T) (k *kb.KB, bare string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "kb")
	k, err := kb.Init(root)
	if err != nil {
		t.Fatalf("kb.Init: %v", err)
	}
	if _, err := gitx.HeadSHA(k.Root); err != nil {
		t.Skipf("git not available or initial commit failed: %v", err)
	}
	bare = filepath.Join(base, "remote.git")
	cmd := exec.Command("git", "init", "--bare", bare)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	if err := gitx.AddRemote(k.Root, "origin", bare); err != nil {
		t.Fatalf("AddRemote: %v", err)
	}
	return k, bare
}

// remoteCommitCount returns `git rev-list --count <branch>` in dir.
func remoteCommitCount(t *testing.T, dir, branch string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "rev-list", "--count", branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-list --count: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestGitWrap_AsyncPush_CommitIsSyncPushIsDeferred verifies the D76/WP4
// critical-path change: with SyncOutDebounce > 0, a successful write commits
// synchronously (visible on HEAD immediately) but the push to origin is
// deferred — not yet on the remote right after the tool call returns — and
// eventually lands once the debounce elapses (verified here via FlushPush,
// which forces it).
func TestGitWrap_AsyncPush_CommitIsSyncPushIsDeferred(t *testing.T) {
	k, bare := setupGitKBWithRemote(t)
	k.AutoCommit = true
	k.GitSync = true

	branch, _ := gitx.Branch(k.Root)

	// Seed the remote (synchronous push) so SyncIn — which gitWrap runs
	// before every write — has a matching ref to fetch/rebase against.
	if err := k.SyncOut(); err != nil {
		t.Fatalf("seed SyncOut: %v", err)
	}
	baseline := remoteCommitCount(t, bare, branch)

	k.SyncOutDebounce = 1 * time.Hour // long enough to never fire on its own in this test

	s := New("0.1.0-test")
	RegisterKBTools(s, k, Deps{})

	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_write","arguments":{"id":"test/async","frontmatter":{"type":"Note","title":"Async"},"body":"# Test\n"}}}`,
	}
	resps := runMCPSequence(t, s, msgs)
	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("concept_write: isError=true: %v", tr.Content)
	}

	// The commit is synchronous: HEAD must already reflect it.
	if _, err := gitx.HeadSHA(k.Root); err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}

	// The push, however, must NOT have reached the remote yet — it is
	// scheduled on the async worker, debounced for 1h.
	if count := remoteCommitCount(t, bare, branch); count != baseline {
		t.Fatalf("remote already has %s commit(s) (baseline %s) — push should have been deferred, not inline", count, baseline)
	}

	// Force it: FlushPush makes the deferred push happen now.
	if err := k.FlushPush(5 * time.Second); err != nil {
		t.Fatalf("FlushPush: %v", err)
	}
	if count := remoteCommitCount(t, bare, branch); count == baseline {
		t.Fatalf("remote did not receive the deferred push after FlushPush (still at baseline %s)", baseline)
	}
}

// TestFormatTiming verifies the greppable timing line format, including the
// case where some phases are skipped (zero duration).
func TestFormatTiming(t *testing.T) {
	cases := []struct {
		name                              string
		op                                string
		syncIn, handler, commit, push, tt time.Duration
		pushAsync                         bool
		want                              string
	}{
		{
			name:    "all phases",
			op:      `concept_write: test/id`,
			syncIn:  12 * time.Millisecond,
			handler: 3 * time.Millisecond,
			commit:  45 * time.Millisecond,
			push:    200 * time.Millisecond,
			tt:      260 * time.Millisecond,
			want:    `cartographer: timing op="concept_write: test/id" sync_in=12ms handler=3ms commit=45ms push=200ms total=260ms`,
		},
		{
			name: "zero phases",
			op:   "concept_write",
			want: `cartographer: timing op="concept_write" sync_in=0ms handler=0ms commit=0ms push=0ms total=0ms`,
		},
		{
			name:    "handler failed: no commit/push",
			op:      "log_append",
			syncIn:  5 * time.Millisecond,
			handler: 1500 * time.Microsecond,
			tt:      7 * time.Millisecond,
			want:    `cartographer: timing op="log_append" sync_in=5ms handler=1ms commit=0ms push=0ms total=7ms`,
		},
		{
			name:      "async push (D76/WP4)",
			op:        "concept_write: test/id",
			syncIn:    2 * time.Millisecond,
			handler:   3 * time.Millisecond,
			commit:    10 * time.Millisecond,
			pushAsync: true,
			tt:        15 * time.Millisecond,
			want:      `cartographer: timing op="concept_write: test/id" sync_in=2ms handler=3ms commit=10ms push=async total=15ms`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatTiming(tc.op, tc.syncIn, tc.handler, tc.commit, tc.push, tc.pushAsync, tc.tt)
			if got != tc.want {
				t.Errorf("formatTiming(...) = %q, want %q", got, tc.want)
			}
		})
	}
}
