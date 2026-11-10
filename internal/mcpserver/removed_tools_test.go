package mcpserver

import (
	"strings"
	"testing"
)

// A released client still calls sync_apply (present v0.1.0 to v0.28.0). It gets
// the not-found answer (the routed endpoint words it as the protocol's unknown-tool error) plus the pointer to what replaced it, on the
// per-KB and on the routed endpoint; the `cartographer sync` clause only for a
// connection addressed to an operator (D1101).
func TestRemovedTool_SyncApplyAnswersWithAPointer(t *testing.T) {
	t.Parallel()
	handler := newRoutedTestHandler(t, "kb-a", "kb-b")
	for _, tc := range []struct {
		name, path string
		operator   bool
	}{
		{"routed", RoutedMountPath, false},
		{"routed provisioned", provisionedPath(RoutedMountPath), true},
		{"per-KB", "/mcp/kb-a", false},
		{"per-KB provisioned", "/mcp/kb-a?provisioned=1", false}, // a per-KB endpoint is always generic (D1101)
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := aliasCall(t, handler, tc.path, "sync_apply", map[string]any{"base_dir": t.TempDir()})
			if !res.IsError {
				t.Fatalf("sync_apply answered without an error: %+v", res)
			}
			text := res.Content[0].Text
			if !strings.Contains(text, "sync_apply") || !strings.Contains(text, "removed in 0.29") {
				t.Errorf("message = %q", text)
			}
			if got := strings.Contains(text, "cartographer sync"); got != tc.operator {
				t.Errorf("cartographer sync mentioned = %v, want %v: %q", got, tc.operator, text)
			}
			if !strings.Contains(text, "sync_pull") {
				t.Errorf("message does not name sync_pull: %q", text)
			}
		})
	}
}

func TestRemovedTool_OtherUnknownToolsStayPlain(t *testing.T) {
	t.Parallel()
	if got := removedToolMessage(authLocalContext(), "nonsense_tool"); got != "" {
		t.Errorf("removedToolMessage(nonsense_tool) = %q, want empty", got)
	}
}
