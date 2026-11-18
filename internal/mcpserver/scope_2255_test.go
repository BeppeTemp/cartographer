package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #2255: lint, validate and gate_check take the same scopes (a map, a mapped
// folder, a concept id, a trailing slash) and refuse the rest with one message,
// none of them naming a server path.
func TestScopeFormsAreTheSameForLintValidateGate(t *testing.T) {
	t.Parallel()
	s, k := graphToolKBWithKB(t, map[string]string{"ops/n1.md": "---\ntype: Note\ntitle: N1\n---\nx\n"})
	if err := os.MkdirAll(filepath.Join(k.Root, "services"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc := "---\ntype: Service\ntitle: S1\n---\nx\n"
	if err := os.WriteFile(filepath.Join(k.Root, "services", "s1.md"), []byte(svc), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		scope  string
		refuse bool
	}{
		{"ops", false}, {"ops/n1", false}, {"ops/", false}, {"services", false}, {"nosuchmap", true}, {"ops/nope", true},
	}
	for _, tool := range []string{"lint", "validate", "gate_check"} {
		for _, c := range cases {
			text, isErr := callJSON(t, s, adminCtx, tool, `{"scope":"`+c.scope+`"}`)
			if isErr != c.refuse {
				t.Errorf("%s %q: refused=%v, want %v: %q", tool, c.scope, isErr, c.refuse, text)
			}
			if c.refuse && !strings.Contains(text, "unknown scope") {
				t.Errorf("%s %q: message %q", tool, c.scope, text)
			}
			for _, bad := range []string{k.Root, "lstat", "../", "non-canonical", "no such file"} {
				if strings.Contains(text, bad) {
					t.Errorf("%s %q: %q leaks %q", tool, c.scope, text, bad)
				}
			}
		}
	}
}
