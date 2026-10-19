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
