package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/sqlindex"
)

func writeWrappedTool(t *testing.T, k *kb.KB, name string, beforeCommit func()) ToolResult {
	t.Helper()
	tool := gitWrap(k, Tool{Name: name, Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
		if beforeCommit != nil {
			beforeCommit()
		}
		if err := k.WriteFileAtomic("data/wrapped.md", []byte("wrapped\n")); err != nil {
			return ToolResult{}, err
		}
		return textResult(`{"ok":true}`), nil
	}})
	res, err := tool.Handler(authLocalContext(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("wrapped handler: %v", err)
	}
	return res
}

func syncWarning(t *testing.T, res ToolResult) map[string]any {
	t.Helper()
	if len(res.Content) != 2 {
		t.Fatalf("content blocks = %+v, want response plus warning", res.Content)
	}
	var warning map[string]any
	if err := json.Unmarshal([]byte(res.Content[1].Text), &warning); err != nil {
		t.Fatalf("warning JSON %q: %v", res.Content[1].Text, err)
	}
	return warning
}

func syncStatus(t *testing.T, k *kb.KB) map[string]any {
	t.Helper()
	res, err := toolSyncStatus(k).Handler(authLocalContext(), nil)
	if err != nil {
		t.Fatalf("sync_status: %v", err)
	}
	var status map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &status); err != nil {
		t.Fatalf("sync_status JSON %q: %v", res.Content[0].Text, err)
	}
	return status
}

func TestGitWrap_SyncPushFailureAppendsFailedWarning(t *testing.T) {
	k, bare := setupGitKBWithRemote(t)
	k.AutoCommit, k.GitSync = true, true
	if err := k.SyncOut(); err != nil {
		t.Fatalf("seed SyncOut: %v", err)
	}
	res := writeWrappedTool(t, k, "test_write", func() {
		if err := os.RemoveAll(bare); err != nil {
			t.Fatal(err)
		}
	})
	warning := syncWarning(t, res)
	if warning["sync_state"] != "failed" || warning["last_error"] == "" {
		t.Fatalf("warning = %+v", warning)
	}
	if status := k.GitStatusSnapshot(); status.State != "failed" || status.Attempts != 5 {
		t.Fatalf("status = %+v", status)
	}
	status := syncStatus(t, k)
	if status["state"] != "failed" || status["attempts"] != float64(5) || status["last_error"] == "" {
		t.Fatalf("sync_status = %+v", status)
	}
}

func TestGitWrap_DebouncedWriteAppendsPendingWarningAndPreservesFailure(t *testing.T) {
	k, _ := setupGitKBWithRemote(t)
	k.AutoCommit, k.GitSync, k.SyncOutDebounce = true, true, time.Hour
	if err := k.SyncOut(); err != nil {
		t.Fatalf("seed SyncOut: %v", err)
	}
	k.SetGitStatus("failed", fmt.Errorf("push rejected"))
	res := writeWrappedTool(t, k, "test_write", nil)
	warning := syncWarning(t, res)
	if warning["sync_state"] != "failed" || warning["last_error"] != "push rejected" {
		t.Fatalf("warning = %+v", warning)
	}
	if status := k.GitStatusSnapshot(); status.State != "failed" {
		t.Fatalf("later write cleared failure: %+v", status)
	}
	k.SetGitStatus("clean", nil)
	res = writeWrappedTool(t, k, "test_write_again", nil)
	if warning = syncWarning(t, res); warning["sync_state"] != "pending" {
		t.Fatalf("warning after successful push = %+v", warning)
	}
}

func TestGitWrap_CommitFailureIsRecordedAndDoesNotSchedulePush(t *testing.T) {
	k, bare := setupGitKBWithRemote(t)
	k.AutoCommit, k.GitSync, k.SyncOutDebounce = true, true, time.Hour
	if err := k.SyncOut(); err != nil {
		t.Fatalf("seed SyncOut: %v", err)
	}
	hook := filepath.Join(k.Root, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := writeWrappedTool(t, k, "test_write", nil)
	warning := syncWarning(t, res)
	if warning["sync_state"] != "failed" || !strings.Contains(warning["last_error"].(string), "commit:") {
		t.Fatalf("warning = %+v", warning)
	}
	branch, _ := gitx.Branch(k.Root)
	if count := remoteCommitCount(t, bare, branch); count != "1" {
		t.Fatalf("remote count = %s, want seed only", count)
	}
}

func TestSyncStatus_MissingRemoteTrackingRefUsesJSONNull(t *testing.T) {
	k, _ := setupGitKBWithRemote(t)
	k.GitSync = true
	status := syncStatus(t, k)
	if status["unpushed_commits"] != nil {
		t.Fatalf("sync_status unpushed_commits = %#v, want null", status["unpushed_commits"])
	}
}

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

func TestGitWrap_ConceptNew_CreatesOneCommit(t *testing.T) {
	k, _ := setupGitKB(t)
	if err := os.MkdirAll(filepath.Join(k.Root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(k.Root, "templates", "note.md"), []byte("---\ntype: Note\ntitle: {{title}}\n---\n# Details\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOp("test: add template"); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		out, err := exec.Command("git", "-C", k.Root, "rev-list", "--count", "HEAD").Output()
		if err != nil {
			t.Fatal(err)
		}
		var n int
		if _, err := fmt.Sscan(string(out), &n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := count()
	k.AutoCommit = true
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	tr := decodeToolResult(t, runMCPSequence(t, s, []string{initMsg, artifactCallMsg(t, 2, "concept_new", map[string]any{"template": "note", "id": "notes/from-template", "vars": map[string]string{"title": "From template"}})})[1])
	if tr.IsError {
		t.Fatalf("concept_new: %+v", tr.Content)
	}
	if got := count(); got != before+1 {
		t.Fatalf("commit count = %d, want %d", got, before+1)
	}
}

func TestGitWrap_AssetWriteAndDeleteCreateOneCommitEach(t *testing.T) {
	k, _ := setupGitKB(t)
	fm, _ := okf.ParseFrontmatter("type: Note\ntitle: Asset owner")
	if _, err := k.WriteConcept("assets/owner", fm, "# Owner\n", ""); err != nil {
		t.Fatal(err)
	}
	if err := k.ExpandConcept("assets/owner"); err != nil {
		t.Fatal(err)
	}
	k.AutoCommit = true
	if _, err := k.CommitOp("test: set up asset owner"); err != nil {
		t.Fatal(err)
	}
	commitCount := func() int {
		out, err := exec.Command("git", "-C", k.Root, "rev-list", "--count", "HEAD").Output()
		if err != nil {
			t.Fatal(err)
		}
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := commitCount()
	s := New("0.1.0-test")
	RegisterKBTools(s, k, Deps{})
	write := s.Tools()["asset_write"]
	result, err := write.Handler(authLocalContext(), json.RawMessage(`{"concept_id":"assets/owner","path":"evidence/check.txt","content":"check"}`))
	if err != nil || result.IsError {
		t.Fatalf("asset_write: result=%+v err=%v", result, err)
	}
	if got := commitCount(); got != before+1 {
		t.Fatalf("asset_write commits = %d, want %d", got, before+1)
	}
	_, entry, err := k.ReadAsset("assets/owner", "evidence/check.txt")
	if err != nil {
		t.Fatal(err)
	}
	deleteTool := s.Tools()["asset_delete"]
	result, err = deleteTool.Handler(authLocalContext(), json.RawMessage(`{"concept_id":"assets/owner","path":"evidence/check.txt","if_match":"`+entry.SHA256+`"}`))
	if err != nil || result.IsError {
		t.Fatalf("asset_delete: result=%+v err=%v", result, err)
	}
	if got := commitCount(); got != before+2 {
		t.Fatalf("asset_delete commits = %d, want %d", got, before+2)
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

// TestGitWrap_IndexPatch_CreatesOneCommit verifies a batch index_patch
// (D122 WP2) produces exactly one git commit, same as concept_patch's batch
// 'edits' form.
func TestGitWrap_IndexPatch_CreatesOneCommit(t *testing.T) {
	k, sha1 := setupGitKB(t)
	k.AutoCommit = true

	s := New("0.1.0-test")
	RegisterKBTools(s, k, Deps{})

	getResp := decodeToolResult(t, runMCPSequence(t, s, []string{initMsg,
		artifactCallMsg(t, 2, "index_get", map[string]any{"with_hash": true}),
	})[1])
	if getResp.IsError {
		t.Fatalf("index_get with_hash: isError=true: %v", getResp.Content)
	}
	var getResult struct {
		Content     string `json:"content"`
		ContentHash string `json:"content_hash"`
	}
	if err := json.Unmarshal([]byte(getResp.Content[0].Text), &getResult); err != nil {
		t.Fatalf("decode index_get result: %v", err)
	}

	patchResp := decodeToolResult(t, runMCPSequence(t, s, []string{initMsg,
		artifactCallMsg(t, 2, "index_patch", map[string]any{
			"if_match": getResult.ContentHash,
			"edits": []map[string]any{
				{"old_string": "KB initialized.", "new_string": "KB initialized (patched)."},
			},
		}),
	})[1])
	if patchResp.IsError {
		t.Fatalf("index_patch: isError=true: %v", patchResp.Content)
	}

	sha2, err := gitx.HeadSHA(k.Root)
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	if sha1 == sha2 {
		t.Fatal("expected a new commit after index_patch with AutoCommit=true, but HEAD SHA is unchanged")
	}
}

// TestGitWrap_IndexPatch_FailedOp_NoCommit verifies a stale_write index_patch
// does not produce a git commit.
func TestGitWrap_IndexPatch_FailedOp_NoCommit(t *testing.T) {
	k, sha1 := setupGitKB(t)
	k.AutoCommit = true

	s := New("0.1.0-test")
	RegisterKBTools(s, k, Deps{})

	resp := decodeToolResult(t, runMCPSequence(t, s, []string{initMsg,
		artifactCallMsg(t, 2, "index_patch", map[string]any{
			"if_match":   "wrong-hash",
			"old_string": "x",
			"new_string": "y",
		}),
	})[1])
	if !resp.IsError {
		t.Fatal("index_patch with wrong if_match: expected isError=true but got success")
	}

	sha2, err := gitx.HeadSHA(k.Root)
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	if sha1 != sha2 {
		t.Fatal("expected no commit after failed index_patch but HEAD SHA changed")
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

// pushRemoteFile simulates a second server by committing a KB file through a
// fresh clone of the shared bare remote.
func pushRemoteFile(t *testing.T, bare, branch, relPath, content, message string) {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "clone")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run(filepath.Dir(clone), "clone", bare, clone)
	run(clone, "checkout", branch)
	path := filepath.Join(clone, relPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	run(clone, "add", relPath)
	run(clone, "-c", "user.email=test@test", "-c", "user.name=test", "commit", "-m", message)
	run(clone, "push", "origin", branch)
}

func syncInForTest(t *testing.T, k *kb.KB) {
	t.Helper()
	if err := k.WithGitLock(func() error {
		_, err := k.SyncIn()
		return err
	}); err != nil {
		t.Fatalf("SyncIn: %v", err)
	}
}

func TestReadSyncWrap_RefreshesRemoteConceptAndSearch(t *testing.T) {
	k, bare := setupGitKBWithRemote(t)
	k.GitSync = true
	if err := k.SyncOut(); err != nil {
		t.Fatalf("seed SyncOut: %v", err)
	}
	k.SyncInWindow = time.Hour
	branch, _ := gitx.Branch(k.Root)

	sqlIdx, err := sqlindex.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlIdx.Close()
	s := New("0.1.0-test")
	RegisterKBTools(s, k, Deps{SQLIndex: sqlIdx})

	pushRemoteFile(t, bare, branch, "data/remote/fresh.md", "---\ntype: Note\ntitle: Fresh\n---\nremote-sync-fresh\n", "remote fresh")
	resps := runMCPSequence(t, s, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_read","arguments":{"id":"remote/fresh"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search","arguments":{"query":"remote-sync-fresh"}}}`,
	})
	if got := decodeToolResult(t, resps[1]); got.IsError || !strings.Contains(got.Content[0].Text, "remote-sync-fresh") {
		t.Fatalf("concept_read after remote change = %+v", got)
	}
	if got := decodeToolResult(t, resps[2]); got.IsError || !strings.Contains(got.Content[0].Text, "remote/fresh") {
		t.Fatalf("search after read-side SyncIn = %+v", got)
	}

	// A second remote commit remains invisible during the freshness window: the
	// immediate read must not fetch again.
	pushRemoteFile(t, bare, branch, "data/remote/second.md", "---\ntype: Note\ntitle: Second\n---\nremote-sync-second\n", "remote second")
	resps = runMCPSequence(t, s, []string{
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"concept_read","arguments":{"id":"remote/second"}}}`,
	})
	if got := decodeToolResult(t, resps[0]); !got.IsError {
		t.Fatalf("concept_read within SyncInWindow fetched remote change: %+v", got)
	}

	// Disabling the window makes the next read fetch the pending change.
	k.SyncInWindow = 0
	resps = runMCPSequence(t, s, []string{
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"concept_read","arguments":{"id":"remote/second"}}}`,
	})
	if got := decodeToolResult(t, resps[0]); got.IsError || !strings.Contains(got.Content[0].Text, "remote-sync-second") {
		t.Fatalf("concept_read after SyncInWindow disabled = %+v", got)
	}
}

func TestReadSyncWrap_ConflictDegradesAndServesLocalRead(t *testing.T) {
	k, bare := setupGitKBWithRemote(t)
	k.GitSync = true
	if err := k.SyncOut(); err != nil {
		t.Fatalf("seed SyncOut: %v", err)
	}
	branch, _ := gitx.Branch(k.Root)
	path := "data/remote/conflict.md"
	base := "---\ntype: Note\ntitle: Conflict\n---\nbase\n"
	pushRemoteFile(t, bare, branch, path, base, "remote base")
	syncInForTest(t, k)

	local := "---\ntype: Note\ntitle: Conflict\n---\nlocal version\n"
	if err := os.WriteFile(filepath.Join(k.Root, path), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := gitx.Commit(k.Root, "local conflict", "test", "test@test"); err != nil {
		t.Fatal(err)
	}
	remote := "---\ntype: Note\ntitle: Conflict\n---\nremote version\n"
	pushRemoteFile(t, bare, branch, path, remote, "remote conflict")

	s := New("0.1.0-test")
	RegisterKBTools(s, k, Deps{})
	resps := runMCPSequence(t, s, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_read","arguments":{"id":"remote/conflict"}}}`,
	})
	if got := decodeToolResult(t, resps[1]); got.IsError || !strings.Contains(got.Content[0].Text, "local version") {
		t.Fatalf("read after rebase conflict = %+v", got)
	}
	conflicts, err := k.ListConflicts()
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 || conflicts[0].ConceptID != "remote/conflict" {
		t.Fatalf("registered conflicts = %+v", conflicts)
	}
}

func TestReadSyncWrap_DisabledOrNoRemoteKeepsReadPathWorking(t *testing.T) {
	for _, tc := range []struct {
		name    string
		gitSync bool
	}{
		{name: "sync disabled", gitSync: false},
		{name: "no remote", gitSync: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := setupTestKB(t)
			k.GitSync = tc.gitSync
			s := New("0.1.0-test")
			RegisterKBTools(s, k, Deps{})
			resps := runMCPSequence(t, s, []string{
				`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
				`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"concept_read","arguments":{"id":"manutenzione/test-runbook"}}}`,
			})
			if got := decodeToolResult(t, resps[1]); got.IsError || !strings.Contains(got.Content[0].Text, "Test Runbook") {
				t.Fatalf("concept_read = %+v", got)
			}
		})
	}
}

func TestGitWrap_ReauthorizesAfterSyncInChangesConceptType(t *testing.T) {
	k, bare := setupGitKBWithRemote(t)
	k.AuthName = "docs"
	k.AutoCommit, k.GitSync = true, true
	if err := k.CreateMap("manutenzione", "Maintenance", "map", nil, ""); err != nil {
		t.Fatal(err)
	}
	fm, err := okf.ParseFrontmatter("")
	if err != nil {
		t.Fatal(err)
	}
	fm.Set("type", "Runbook")
	fm.Set("title", "before sync")
	if _, err := k.WriteConcept("manutenzione/reauth", fm, "body", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOp("seed RBAC fixture"); err != nil {
		t.Fatal(err)
	}
	if err := k.SyncOut(); err != nil {
		t.Fatal(err)
	}
	branch, _ := gitx.Branch(k.Root)

	s := New("test")
	RegisterKBTools(s, k, Deps{})
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true, Maps: []string{"manutenzione"}, Types: []string{"Runbook"}}}})
	// The first dispatch sees Runbook. SyncIn below replaces it with Secret;
	// the lock-time decision must therefore deny before handler/log/commit.
	pushRemoteFile(t, bare, branch, "data/manutenzione/reauth.md", "---\ntype: Secret\ntitle: after sync\n---\nbody\n", "change type remotely")
	request := &Request{ID: json.RawMessage(`1`), Method: "tools/call", Params: json.RawMessage(`{"name":"concept_patch","arguments":{"id":"manutenzione/reauth","if_match":"any","old_string":"body","new_string":"changed"}}`)}
	result := decodeToolResult(t, s.dispatch(ctx, request))
	if !result.IsError || result.Content[0].Text != genericNotFound {
		t.Fatalf("reauthorized write = %+v, want non-disclosing denial", result)
	}
	data, err := k.ReadConcept("manutenzione/reauth")
	if err != nil || !strings.Contains(data.Content, "type: Secret") || strings.Contains(data.Content, "changed") {
		t.Fatalf("denied write mutated synced concept: data=%+v err=%v", data, err)
	}
	if count := remoteCommitCount(t, bare, branch); count != "3" {
		t.Fatalf("denied write committed or pushed: remote commits=%s, want 3", count)
	}
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

// headSubject returns the subject line of HEAD in dir. The commit subject is
// the audit trail of a git-backed KB, so the tests below assert on what landed
// in the history rather than only on commitMessage's return value.
func headSubject(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "log", "-1", "--format=%s").Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func TestCommitMessage_IdentifiesTheResourceForEveryWriteTool(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args string
		want string
	}{
		// Per-tool fields: these are the ones that used to commit as the bare
		// tool name, leaving consecutive writes indistinguishable in the log.
		{"artifact_write by path", "artifact_write", `{"path":"skills/kb-import/SKILL.md","content":"x"}`, "artifact_write: skills/kb-import/SKILL.md"},
		{"artifact_delete by path", "artifact_delete", `{"path":"agents/dev.md","if_match":"abc"}`, "artifact_delete: agents/dev.md"},
		{"asset_write by concept and path", "asset_write", `{"concept_id":"assets/owner","path":"evidence/check.txt","content":"x"}`, "asset_write: assets/owner/evidence/check.txt"},
		{"asset_delete by concept and path", "asset_delete", `{"concept_id":"assets/owner","path":"evidence/check.txt"}`, "asset_delete: assets/owner/evidence/check.txt"},
		{"asset_write with only concept_id", "asset_write", `{"concept_id":"assets/owner"}`, "asset_write: assets/owner"},
		// Fallback vocabulary: unchanged behaviour.
		{"concept_patch by id", "concept_patch", `{"id":"manutenzione/runbook","if_match":"abc"}`, "concept_patch: manutenzione/runbook"},
		{"map_create by name", "map_create", `{"name":"notes","title":"Notes"}`, "map_create: notes"},
		{"supersede by source_id", "supersede", `{"source_id":"a/b","target_id":"c/d"}`, "supersede: a/b"},
		{"conflict_resolve by contradiction_id", "conflict_resolve", `{"contradiction_id":"x1"}`, "conflict_resolve: x1"},
		// No identifying argument, and unparseable arguments.
		{"no identifier", "snapshot", `{"message":"m"}`, "snapshot"},
		{"empty identifier", "concept_patch", `{"id":""}`, "concept_patch"},
		{"invalid json", "concept_patch", `not json`, "concept_patch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := commitMessage(tc.tool, json.RawMessage(tc.args)); got != tc.want {
				t.Errorf("commitMessage(%q, %s) = %q, want %q", tc.tool, tc.args, got, tc.want)
			}
		})
	}
}

func TestGitWrap_ArtifactWriteCommitSubjectNamesThePath(t *testing.T) {
	k, _ := setupGitKB(t)
	k.AutoCommit = true
	k.AllowArtifactWrite = true
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	write := s.Tools()["artifact_write"]
	for _, slug := range []string{"one", "two"} {
		path := "skills/" + slug + "/SKILL.md"
		body := fmt.Sprintf("---\nname: %s\ndescription: Test skill %s.\n---\n# %s\n", slug, slug, slug)
		args, err := json.Marshal(map[string]string{"path": path, "content": body})
		if err != nil {
			t.Fatal(err)
		}
		result, err := write.Handler(authLocalContext(), json.RawMessage(args))
		if err != nil || result.IsError {
			t.Fatalf("artifact_write %s: result=%+v err=%v", path, result, err)
		}
		if got, want := headSubject(t, k.Root), "artifact_write: "+path; got != want {
			t.Fatalf("commit subject = %q, want %q", got, want)
		}
	}
	// The regression this guards: two consecutive artifact writes used to
	// produce two identical subjects.
	out, err := exec.Command("git", "-C", k.Root, "log", "-2", "--format=%s").Output()
	if err != nil {
		t.Fatal(err)
	}
	subjects := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(subjects) != 2 || subjects[0] == subjects[1] {
		t.Fatalf("last two subjects are not distinguishable: %q", subjects)
	}
}

func TestGitWrap_AssetWriteCommitSubjectNamesConceptAndPath(t *testing.T) {
	k, _ := setupGitKB(t)
	fm, _ := okf.ParseFrontmatter("type: Note\ntitle: Asset owner")
	if _, err := k.WriteConcept("assets/owner", fm, "# Owner\n", ""); err != nil {
		t.Fatal(err)
	}
	if err := k.ExpandConcept("assets/owner"); err != nil {
		t.Fatal(err)
	}
	k.AutoCommit = true
	if _, err := k.CommitOp("test: set up asset owner"); err != nil {
		t.Fatal(err)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	write := s.Tools()["asset_write"]
	result, err := write.Handler(authLocalContext(), json.RawMessage(`{"concept_id":"assets/owner","path":"evidence/check.txt","content":"check"}`))
	if err != nil || result.IsError {
		t.Fatalf("asset_write: result=%+v err=%v", result, err)
	}
	if got, want := headSubject(t, k.Root), "asset_write: assets/owner/evidence/check.txt"; got != want {
		t.Fatalf("commit subject = %q, want %q", got, want)
	}
}
