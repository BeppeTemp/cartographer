package mcpserver

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// failingWrite is a write tool that touches the disk (a tracked file and a new
// one) and then fails, the shape of concept_move after applied moves.
func failingWrite(k *kb.KB, goErr bool) Tool {
	return gitWrap(k, Tool{Name: "test_failing_write", Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
		_ = k.WriteFileAtomic("data/half-done.md", []byte("half\n"))
		_ = os.WriteFile(filepath.Join(k.Root, "README.md"), []byte("changed\n"), 0o644)
		if goErr {
			return ToolResult{}, errors.New("boom")
		}
		return errorResult("test_failing_write: late failure; " + recoveryHint), nil
	}})
}

func TestGitWrap_FailedHandlerLeavesTheTreeAtHEAD(t *testing.T) {
	t.Parallel()
	for _, goErr := range []bool{false, true} {
		k, _ := setupGitKB(t)
		head := gitOut(t, k, "rev-parse", "HEAD")
		res, err := failingWrite(k, goErr).Handler(authLocalContext(), json.RawMessage(`{}`))
		if goErr {
			if err == nil || !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "discarded") {
				t.Fatalf("err = %v, want the handler error plus the discarded note", err)
			}
		} else {
			if !res.IsError || strings.Contains(res.Content[0].Text, "concept_history") || !strings.Contains(res.Content[0].Text, "discarded") {
				t.Fatalf("result = %+v, want the error to say the changes were discarded", res)
			}
		}
		if st := gitOut(t, k, "status", "--porcelain"); st != "" {
			t.Fatalf("goErr=%v: tree not clean after a failed write:\n%s", goErr, st)
		}
		if got := gitOut(t, k, "rev-parse", "HEAD"); got != head {
			t.Fatalf("goErr=%v: HEAD moved to %s", goErr, got)
		}
		// The next write, a plain one, commits no "external changes".
		writeWrappedTool(t, k, "log_append", nil)
		if strings.Contains(gitOut(t, k, "log", "--format=%s"), externalChangesSubject) {
			t.Fatalf("goErr=%v: the residue of the failed write was committed as external changes", goErr)
		}
	}
}

func TestGitWrap_FailedHandlerDiscardsNothingWhenTheTreeWasNotClean(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	// An open conflict makes commitExternalChanges leave the dirty tree alone,
	// so the tree is not provably clean: nothing may be discarded.
	editOutOfBand(t, k, "notes/mine.md", "---\ntype: Note\ntitle: Mine\n---\n# Mine\n")
	if err := k.RegisterConflict(kb.Conflict{ConceptID: "foo", Path: "data/foo.md"}); err != nil {
		t.Fatal(err)
	}
	ran := false
	tool := gitWrap(k, Tool{Name: "test_failing_write", Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
		ran = true
		return ToolResult{}, errors.New("boom")
	}})
	tool.Handler(authLocalContext(), json.RawMessage(`{}`)) //nolint:errcheck
	if !ran {
		t.Skip("the open conflict refuses the write before the handler: nothing to assert")
	}
	if _, err := os.Stat(filepath.Join(k.DataRoot(), "notes/mine.md")); err != nil {
		t.Fatalf("unsaved work was discarded: %v", err)
	}
}
