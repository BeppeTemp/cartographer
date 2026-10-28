package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
	"github.com/BeppeTemp/cartographer/internal/sqlindex"
)

func writeBody(lines int) string {
	var sb strings.Builder
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&sb, "Item %d of the runbook says that host-%d restarts service-%d after the window closes.\n", i, i*7, i*13)
	}
	return sb.String()
}

func BenchmarkConceptWriteLargeBody(b *testing.B) {
	k := kbtest.New(b)
	ix, err := sqlindex.Open(filepath.Join(k.Root, "idx.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { ix.Close() })
	rec := newSearchReconciler(k, ix)
	ff := &factFinder{k: k, rec: rec, deps: Deps{SQLIndex: ix}}
	tool := toolConceptWrite(k, nil, ff)
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		raw, _ := json.Marshal(map[string]interface{}{"id": "notes/big", "frontmatter": map[string]interface{}{"type": "Note", "title": "Big"}, "body": writeBody(1000 + n)})
		res, err := tool.Handler(authLocalContext(), raw)
		if err != nil || res.IsError {
			b.Fatalf("%v %+v", err, res)
		}
	}
}

// TestConceptWriteLargeBodyIsFast pins #1276: a rewrite that grows a ~200 KB
// page cost tens of seconds, because the repeated_fact lookup asked the trigram
// index for an excerpt of the page's own near-identical lines (snippet() is
// quadratic in the matches inside one body). The bound is generous for CI and
// far under the quadratic cost.
func TestConceptWriteLargeBodyIsFast(t *testing.T) {
	k := kbtest.New(t)
	ix, err := sqlindex.Open(filepath.Join(k.Root, "idx.db"))
	if err != nil {
		t.Skipf("sqlindex: %v", err)
	}
	t.Cleanup(func() { ix.Close() })
	ff := &factFinder{k: k, rec: newSearchReconciler(k, ix), deps: Deps{SQLIndex: ix}}
	tool := toolConceptWrite(k, nil, ff)
	write := func(lines int) ToolResult {
		raw, _ := json.Marshal(map[string]interface{}{"id": "notes/big", "frontmatter": map[string]interface{}{"type": "Note", "title": "Big"}, "body": writeBody(lines)})
		res, err := tool.Handler(authLocalContext(), raw)
		if err != nil || res.IsError {
			t.Fatalf("write %d lines: %v %+v", lines, err, res)
		}
		return res
	}
	write(500)
	start := time.Now()
	write(2300) // ~200 KB, growing the stored page
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("writing a ~200 KB body took %v", d)
	}
}

func TestConceptWriteRefusesAnOversizeBody(t *testing.T) {
	k := kbtest.New(t)
	tool := toolConceptWrite(k, nil, nil)
	raw, _ := json.Marshal(map[string]interface{}{"id": "notes/huge", "frontmatter": map[string]interface{}{"type": "Note", "title": "Huge"}, "body": strings.Repeat("x", kb.MaxConceptBodyBytes+1)})
	res, err := tool.Handler(authLocalContext(), raw)
	if err != nil || !res.IsError || !strings.Contains(res.Content[0].Text, "body_too_large") || !strings.Contains(res.Content[0].Text, "1 MiB") {
		t.Fatalf("res = %+v, %v", res, err)
	}
}

// An existing page already over the limit is not editable through the tools:
// concept_patch is refused, a frontmatter-only change included (D1276).
func TestConceptPatchRefusesAnExistingOversizePage(t *testing.T) {
	k := kbtest.New(t)
	path := filepath.Join(k.DataRoot(), "notes", "huge.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := "---\ntype: Note\ntitle: Huge\n---\nUNIQUE-MARK\n" + strings.Repeat("x", kb.MaxConceptBodyBytes+10) + "\n"
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	cd, err := k.ReadConcept("notes/huge")
	if err != nil {
		t.Fatal(err)
	}
	tool := toolConceptPatch(k, nil)
	for name, args := range map[string]map[string]interface{}{
		"body edit":        {"old_string": "UNIQUE-MARK", "new_string": "CHANGED"},
		"frontmatter only": {"frontmatter": map[string]interface{}{"title": "Huge 2"}},
	} {
		args["id"], args["if_match"] = "notes/huge", cd.ContentHash
		in, _ := json.Marshal(args)
		res, err := tool.Handler(authLocalContext(), in)
		if err != nil || !res.IsError || !strings.Contains(res.Content[0].Text, "body_too_large") {
			t.Fatalf("%s: res = %+v, %v", name, res, err)
		}
	}
}
