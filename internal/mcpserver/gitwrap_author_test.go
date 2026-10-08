package mcpserver

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// headIdent returns "<author> | <committer>" of HEAD as name <email>.
func headIdent(t *testing.T, k *kb.KB) string {
	t.Helper()
	out, err := exec.Command("git", "-C", k.Root, "log", "-1", "--format=%an <%ae> | %cn <%ce>").Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func wrappedWriteAs(t *testing.T, k *kb.KB, p auth.Principal, content string) {
	t.Helper()
	tool := gitWrap(k, Tool{Name: "concept_write", Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
		if err := k.WriteFileAtomic("data/wrapped.md", []byte(content)); err != nil {
			return ToolResult{}, err
		}
		return textResult(`{"ok":true}`), nil
	}})
	res, err := tool.Handler(auth.ContextWithPrincipal(context.Background(), p), json.RawMessage(`{}`))
	if err != nil || res.IsError {
		t.Fatalf("wrapped handler: err=%v res=%+v", err, res)
	}
}

// A token that declares an author identity is the author of the commits its
// writes produce; the committer stays the KB's identity. A token without one
// commits as the KB.
func TestGitWrap_CommitAuthorFollowsTokenIdentity(t *testing.T) {
	k, _ := setupGitKB(t)
	k.AutoCommit = true
	k.GitAuthorName, k.GitAuthorEmail, k.GitAuthorExplicit = "Owner", "owner@example.com", true
	k.GitEnv = append(k.GitEnv, "GIT_COMMITTER_NAME=Owner", "GIT_COMMITTER_EMAIL=owner@example.com")

	agent := auth.Principal{ID: "agent", Policy: auth.Policy{Admin: true}, AuthorName: "agent-bot", AuthorEmail: "agent@example.com"}
	wrappedWriteAs(t, k, agent, "one\n")
	if got, want := headIdent(t, k), "agent-bot <agent@example.com> | Owner <owner@example.com>"; got != want {
		t.Fatalf("token with identity: HEAD = %q, want %q", got, want)
	}

	wrappedWriteAs(t, k, auth.Principal{ID: "plain", Policy: auth.Policy{Admin: true}}, "two\n")
	if got, want := headIdent(t, k), "Owner <owner@example.com> | Owner <owner@example.com>"; got != want {
		t.Fatalf("token without identity: HEAD = %q, want %q", got, want)
	}
}
