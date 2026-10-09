package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/sqlindex"
)

const repeatedLine = "The nightly job runs at 02:00 and copies the data to the backup host."

type findingsOutT struct {
	Findings []struct {
		Path     string `json:"path"`
		Check    string `json:"check"`
		Severity string `json:"severity"`
		Message  string `json:"message"`
	} `json:"findings"`
}

func repeatedFindings(t *testing.T, res ToolResult) []string {
	t.Helper()
	if res.IsError {
		t.Fatalf("write error: %+v", res.Content)
	}
	var out findingsOutT
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	var msgs []string
	for _, f := range out.Findings {
		if f.Check == "repeated_fact" {
			if f.Severity != "info" {
				t.Errorf("severity = %q, want info", f.Severity)
			}
			msgs = append(msgs, f.Message)
		}
	}
	return msgs
}

func factServer(t *testing.T) (*Server, string) {
	t.Helper()
	k := setupTestKB(t)
	writeKBFile(t, k, "ops/a.md", "---\ntype: Note\ntitle: A\n---\n# A\n\n"+repeatedLine+"\n")
	writeKBFile(t, k, "ops/b.md", "---\ntype: Note\ntitle: B\n---\n# B\n\n"+repeatedLine+"\n")
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	return s, k.Root
}

func TestConceptWriteReportsRepeatedFact(t *testing.T) {
	s, _ := factServer(t)
	args, _ := json.Marshal(map[string]interface{}{"id": "ops/c", "frontmatter": map[string]interface{}{"type": "Note", "title": "C"}, "body": "# C\n\n" + repeatedLine + "\n"})
	got := repeatedFindings(t, callTool(t, s, "concept_write", string(args)))
	if len(got) != 1 || !strings.Contains(got[0], "ops/a, ops/b") {
		t.Fatalf("want one finding naming both owners: %v", got)
	}
	// A concept with no duplicate has no repeated_fact.
	args, _ = json.Marshal(map[string]interface{}{"id": "ops/d", "frontmatter": map[string]interface{}{"type": "Note", "title": "D"}, "body": "# D\n\nNothing that any other page of this fixture says at all.\n"})
	if got := repeatedFindings(t, callTool(t, s, "concept_write", string(args))); len(got) != 0 {
		t.Fatalf("unexpected: %v", got)
	}
}

func TestConceptPatchReportsOnlyAddedRepeatedFact(t *testing.T) {
	s, _ := factServer(t)
	args, _ := json.Marshal(map[string]interface{}{"id": "ops/c", "frontmatter": map[string]interface{}{"type": "Note", "title": "C"}, "body": "# C\n\nStart.\n"})
	res := callTool(t, s, "concept_write", string(args))
	hash := func(res ToolResult) string {
		var out struct {
			ContentHash string `json:"content_hash"`
		}
		_ = json.Unmarshal([]byte(res.Content[0].Text), &out)
		return out.ContentHash
	}
	res = callTool(t, s, "concept_patch", `{"id":"ops/c","if_match":"`+hash(res)+`","old_string":"Start.","new_string":"Start.\n\n`+repeatedLine+`"}`)
	if got := repeatedFindings(t, res); len(got) != 1 {
		t.Fatalf("patch adding the line: %v", got)
	}
	// Touching another line leaves the repeated one alone: not added.
	res = callTool(t, s, "concept_patch", `{"id":"ops/c","if_match":"`+hash(res)+`","old_string":"Start.","new_string":"Begin."}`)
	if got := repeatedFindings(t, res); len(got) != 0 {
		t.Fatalf("unchanged repeated line reported again: %v", got)
	}
}

func TestConceptNewTemplateLinesAreNotRepeatedFacts(t *testing.T) {
	k := setupTestKB(t)
	boiler := "> Fill in the details of this note before saving it anywhere."
	tpl := filepath.Join(k.Root, "templates", "note.md")
	if err := os.MkdirAll(filepath.Dir(tpl), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tpl, []byte("---\ntype: Note\ntitle: \"{{title}}\"\n---\n# {{title}}\n\n"+boiler+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeKBFile(t, k, "ops/a.md", "---\ntype: Note\ntitle: A\n---\n"+boiler+"\n")
	writeKBFile(t, k, "ops/b.md", "---\ntype: Note\ntitle: B\n---\n"+boiler+"\n")
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	res := callTool(t, s, "concept_new", `{"template":"note","id":"ops/c","vars":{"title":"C"}}`)
	if got := repeatedFindings(t, res); len(got) != 0 {
		t.Fatalf("template line reported: %v", got)
	}
}

func TestConceptBatchReportsRepeatedFactPerEntry(t *testing.T) {
	s, _ := factServer(t)
	for _, id := range []string{"ops/c", "ops/d"} {
		args, _ := json.Marshal(map[string]interface{}{"id": id, "frontmatter": map[string]interface{}{"type": "Note", "title": id}, "body": "# T\n\nStart.\n"})
		if res := callTool(t, s, "concept_write", string(args)); res.IsError {
			t.Fatal(res.Content)
		}
	}
	hash := func(id string) string {
		res := callTool(t, s, "concept_read", `{"id":"`+id+`"}`)
		var out struct {
			ContentHash string `json:"content_hash"`
		}
		_ = json.Unmarshal([]byte(res.Content[0].Text), &out)
		return out.ContentHash
	}
	args := fmt.Sprintf(`{"operations":[{"op":"patch","id":"ops/c","if_match":%q,"old_string":"Start.","new_string":"Start.\n\n%s"},{"op":"patch","id":"ops/d","if_match":%q,"old_string":"Start.","new_string":"Begin."}]}`, hash("ops/c"), repeatedLine, hash("ops/d"))
	res := callTool(t, s, "concept_batch", args)
	if res.IsError {
		t.Fatal(res.Content)
	}
	var out struct {
		Results []struct {
			ID       string `json:"id"`
			Findings []struct {
				Check string `json:"check"`
			} `json:"findings"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	count := map[string]int{}
	for _, r := range out.Results {
		for _, f := range r.Findings {
			if f.Check == "repeated_fact" {
				count[r.ID]++
			}
		}
	}
	if count["ops/c"] != 1 || count["ops/d"] != 0 {
		t.Fatalf("per-entry findings: %v", count)
	}
}

// TestRepeatedFactHidesInvisibleOwners: an owner the caller cannot see is
// neither named nor counted (D351).
func TestRepeatedFactHidesInvisibleOwners(t *testing.T) {
	k := setupTestKB(t)
	k.AuthName = "docs"
	for _, m := range []string{"visible", "hidden"} {
		writeKBFile(t, k, m+"/_map.md", "---\ntype: Map\ntitle: "+m+"\n---\n")
	}
	writeKBFile(t, k, "visible/_map.md", "---\ntype: Map\ntitle: visible\nrepeated_fact_min: 2\n---\n")
	writeKBFile(t, k, "visible/a.md", "---\ntype: Note\ntitle: A\n---\n"+repeatedLine+"\n")
	writeKBFile(t, k, "hidden/b.md", "---\ntype: Note\ntitle: B\n---\n"+repeatedLine+"\n")
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true, Maps: []string{"visible"}}}})
	args, _ := json.Marshal(map[string]interface{}{"id": "visible/c", "frontmatter": map[string]interface{}{"type": "Note", "title": "C"}, "body": repeatedLine + "\n"})
	res := s.callTool(ctx, "concept_write", args)
	got := repeatedFindings(t, res)
	// visible/a + visible/c reach the map's threshold of 2; hidden/b is not named.
	if len(got) != 1 || !strings.Contains(got[0], "visible/a") || strings.Contains(res.Content[0].Text, "hidden/") {
		t.Fatalf("hidden owner counted or named: %v %s", got, res.Content[0].Text)
	}
}

// BenchmarkWriteRepeatedFact: the cost the lookup adds to a concept_batch of
// 50 patches, each adding 3 fact lines, on a 1,000-concept KB with the FTS5
// index (D351). Acceptance: "with" under 50 ms over "without" (no finder).
func BenchmarkWriteRepeatedFact(b *testing.B) {
	dir := b.TempDir()
	k, err := kb.Init(dir)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		p := filepath.Join(k.DataRoot(), fmt.Sprintf("m%d", i%10), fmt.Sprintf("c%d.md", i))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		body := fmt.Sprintf("---\ntype: Note\ntitle: C%d\n---\n# C\n\nAn ordinary sentence number %d about nothing in particular here.\n", i, i)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	ix, err := sqlindex.Open(filepath.Join(dir, "idx.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { ix.Close() })
	deps := Deps{SQLIndex: ix}
	rec := newSearchReconciler(k, ix)
	if _, err := rec.reconcile(); err != nil {
		b.Fatal(err)
	}
	time.Sleep(2200 * time.Millisecond) // age the files past the graph cache's racy window
	for _, withFinder := range []bool{false, true} {
		name := "without"
		var ff *factFinder
		if withFinder {
			name = "with"
			ff = &factFinder{k: k, rec: rec, deps: deps}
		}
		tool := toolConceptBatch(k, ff)
		b.Run(name, func(b *testing.B) {
			for n := 0; n < b.N; n++ {
				b.StopTimer()
				var ops []string
				for i := 0; i < 50; i++ {
					id := fmt.Sprintf("m%d/c%d", i%10, i)
					cd, err := k.ReadConcept(okf.ConceptID(id))
					if err != nil {
						b.Fatal(err)
					}
					old := "# C"
					add := fmt.Sprintf("%s\n\nAdded fact %s-%d-%d one that is long enough to be a fact line.\n\nAdded fact %s-%d-%d two that is long enough to be a fact line.\n\nAdded fact %s-%d-%d three that is long enough to be a fact line.", old, name, n, i, name, n, i, name, n, i)
					raw, _ := json.Marshal(map[string]interface{}{"op": "patch", "id": id, "if_match": cd.ContentHash, "old_string": old, "new_string": add})
					ops = append(ops, string(raw))
				}
				args := json.RawMessage(`{"operations":[` + strings.Join(ops, ",") + `]}`)
				b.StartTimer()
				res, err := tool.Handler(authLocalContext(), args)
				if err != nil || res.IsError {
					b.Fatalf("batch: %v %+v", err, res)
				}
			}
		})
	}
}
