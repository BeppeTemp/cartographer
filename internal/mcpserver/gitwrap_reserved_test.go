package mcpserver

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// D311: the conflict message names reserved files and never says "0
// concept(s)" alone, nor suggests a git command.
func TestConflictMessage(t *testing.T) {
	only := conflictMessage(0, []string{"data/log.md"})
	if strings.Contains(only, "0 concept(s)") || !strings.Contains(only, "data/log.md") || !strings.Contains(only, "git_conflict_resolve") {
		t.Fatalf("reserved-only message = %q", only)
	}
	mixed := conflictMessage(1, []string{"data/log.md"})
	if !strings.Contains(mixed, "1 concept(s)") || !strings.Contains(mixed, "1 reserved file(s)") || !strings.Contains(mixed, "data/log.md") {
		t.Fatalf("mixed message = %q", mixed)
	}
	if plain := conflictMessage(2, nil); !strings.Contains(plain, "2 concept(s)") || strings.Contains(plain, "reserved") {
		t.Fatalf("concept-only message = %q", plain)
	}
	for _, m := range []string{only, mixed} {
		if strings.Contains(m, "git -C") || strings.Contains(m, "rebase --") {
			t.Fatalf("message suggests a git command: %q", m)
		}
	}
}

func TestHandleConflictError_ReservedOnlyIsRegisteredWithKind(t *testing.T) {
	k, sha := setupGitKB(t)
	n, reserved := handleConflictError(k, &gitx.RebaseConflictError{Files: []string{"data/log.md"}, LocalSHA: sha, RemoteSHA: sha, Branch: "main"})
	if n != 0 || len(reserved) != 1 {
		t.Fatalf("n=%d reserved=%v", n, reserved)
	}
	cs, err := k.ListConflicts()
	if err != nil || len(cs) != 1 || cs[0].ConceptID != "data/log.md" || cs[0].Kind != kb.ConflictKindReserved {
		t.Fatalf("registry = %+v, err %v", cs, err)
	}

	res, _ := toolConflictsList(k).Handler(authLocalContext(), nil)
	if !strings.Contains(res.Content[0].Text, `"kind": "reserved"`) || !strings.Contains(res.Content[0].Text, "union") {
		t.Fatalf("conflicts_list = %s", res.Content[0].Text)
	}

	resolve := toolGitConflictResolve(k).Handler
	res, _ = resolve(authLocalContext(), json.RawMessage(`{"concept_id":"data/log.md","strategy":"edit","body":"x"}`))
	if !res.IsError {
		t.Fatalf("edit on a reserved file accepted: %+v", res)
	}
}

func TestHandleConflictError_ReservedWithConceptStaysOutOfRegistry(t *testing.T) {
	k, sha := setupGitKB(t)
	if err := os.MkdirAll(k.Root+"/data/ops", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(k.Root+"/data/ops/runbook.md", []byte("---\ntype: Topic\ntitle: R\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, reserved := handleConflictError(k, &gitx.RebaseConflictError{Files: []string{"data/log.md", "data/ops/runbook.md"}, LocalSHA: sha, RemoteSHA: sha, Branch: "main"})
	if n != 1 || len(reserved) != 1 || reserved[0] != "data/log.md" {
		t.Fatalf("n=%d reserved=%v", n, reserved)
	}
	cs, _ := k.ListConflicts()
	if len(cs) != 1 || cs[0].ConceptID != "ops/runbook" {
		t.Fatalf("registry = %+v", cs)
	}
	res, _ := toolGitConflictResolve(k).Handler(authLocalContext(), json.RawMessage(`{"concept_id":"ops/runbook","strategy":"union"}`))
	if !res.IsError {
		t.Fatalf("union on a concept accepted: %+v", res)
	}
}

// D311 WP2: a write whose fetch fails, on a base with nothing unpushed,
// commits locally and reports degraded; once the remote is back the next
// write syncs normally.
func TestGitWrap_UnreachableRemoteWriteSucceedsDegraded(t *testing.T) {
	k, bare := setupGitKBWithRemote(t)
	k.AutoCommit, k.GitSync, k.SyncOutDebounce = true, true, time.Hour
	if err := k.SyncOut(); err != nil {
		t.Fatalf("seed SyncOut: %v", err)
	}
	moved := bare + ".away"
	if err := os.Rename(bare, moved); err != nil {
		t.Fatal(err)
	}
	head, _ := gitx.HeadSHA(k.Root)
	res := writeWrappedTool(t, k, "test_write", nil)
	if res.IsError {
		t.Fatalf("write on an unreachable remote failed: %+v", res)
	}
	if after, _ := gitx.HeadSHA(k.Root); after == head {
		t.Fatal("the write made no local commit")
	}
	warning := syncWarning(t, res)
	if warning["sync_state"] != "degraded" || !strings.HasPrefix(warning["last_error"].(string), kb.FetchFailedLocalBase) {
		t.Fatalf("warning = %+v", warning)
	}

	if err := os.Rename(moved, bare); err != nil {
		t.Fatal(err)
	}
	k.SyncOutDebounce = 0
	tool := gitWrap(k, Tool{Name: "test_write_again", Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
		if err := k.WriteFileAtomic("data/again.md", []byte("again\n")); err != nil {
			return ToolResult{}, err
		}
		return textResult(`{"ok":true}`), nil
	}})
	res, err := tool.Handler(authLocalContext(), json.RawMessage(`{}`))
	if err != nil || res.IsError {
		t.Fatalf("write after the remote came back: %+v, %v", res, err)
	}
	if s := k.GitStatusSnapshot(); s.State != "clean" {
		t.Fatalf("status = %+v, want clean", s)
	}
	branch, _ := gitx.Branch(k.Root)
	local, _ := gitx.HeadSHA(k.Root)
	out, gerr := exec.Command("git", "-C", bare, "rev-parse", branch).CombinedOutput()
	if remote := strings.TrimSpace(string(out)); gerr != nil || remote != local {
		t.Fatalf("remote %s != local %s: the deferred commit was not pushed", remote, local)
	}
}
