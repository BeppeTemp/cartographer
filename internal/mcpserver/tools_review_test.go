package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

type reviewOut struct {
	Total      int            `json:"total"`
	ByKind     map[string]int `json:"by_kind"`
	Offset     int            `json:"offset"`
	NextOffset *int           `json:"next_offset"`
	Items      []struct {
		Kind     string   `json:"kind"`
		Concepts []string `json:"concepts"`
		Check    string   `json:"check"`
	} `json:"items"`
}

func writeKBFile(t *testing.T, k *kb.KB, rel, content string) {
	t.Helper()
	abs := filepath.Join(k.DataRoot(), rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// reviewServer is a KB with one resource duplicate in "visible", a
// cross-map one reaching into "hidden", and two broken links.
func reviewServer(t *testing.T) (*Server, *kb.KB) {
	t.Helper()
	k := setupTestKB(t)
	k.AuthName = "docs"
	for _, m := range []string{"visible", "hidden"} {
		writeKBFile(t, k, m+"/_map.md", "---\ntype: Map\ntitle: "+m+"\n---\n")
	}
	writeKBFile(t, k, "visible/a.md", "---\ntype: Service\ntitle: Alpha\nresource: https://example.com/a\n---\n# A\n")
	writeKBFile(t, k, "visible/b.md", "---\ntype: Service\ntitle: Beta\nresource: https://example.com/a\n---\n# B\n\n[x](gone.md)\n")
	writeKBFile(t, k, "visible/c.md", "---\ntype: Service\ntitle: Gamma\nresource: https://example.com/c\n---\n# C\n\n[y](gone.md)\n")
	writeKBFile(t, k, "hidden/c.md", "---\ntype: Service\ntitle: Hidden gamma\nresource: https://example.com/c\n---\n# C\n")
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	return s, k
}

func decodeReview(t *testing.T, res ToolResult) reviewOut {
	t.Helper()
	if res.IsError {
		t.Fatalf("kb_review error: %+v", res.Content)
	}
	var out reviewOut
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatalf("decode: %v: %s", err, res.Content[0].Text)
	}
	return out
}

func TestKBReviewListsRankedItems(t *testing.T) {
	s, _ := reviewServer(t)
	all := decodeReview(t, callTool(t, s, "kb_review", `{}`))
	if all.Total != 4 || all.ByKind["duplicate_candidate"] != 2 || all.ByKind["lint_judgement"] != 2 {
		t.Fatalf("kb_review = %+v", all)
	}
	if all.Items[0].Kind != "duplicate_candidate" || all.Items[3].Kind != "lint_judgement" {
		t.Fatalf("kind priority not applied: %+v", all.Items)
	}

	// Pagination is stable: two pages of two equal the whole list.
	p1 := decodeReview(t, callTool(t, s, "kb_review", `{"limit":2}`))
	p2 := decodeReview(t, callTool(t, s, "kb_review", `{"limit":2,"offset":2}`))
	if p1.NextOffset == nil || *p1.NextOffset != 2 || p2.NextOffset != nil {
		t.Fatalf("next_offset: %v %v", p1.NextOffset, p2.NextOffset)
	}
	pages := append(p1.Items, p2.Items...)
	for i := range pages {
		if strings.Join(pages[i].Concepts, "|") != strings.Join(all.Items[i].Concepts, "|") {
			t.Fatalf("page item %d = %v, want %v", i, pages[i].Concepts, all.Items[i].Concepts)
		}
	}

	only := decodeReview(t, callTool(t, s, "kb_review", `{"kind":"lint_judgement"}`))
	if only.Total != 2 || only.Items[0].Check != "broken_link" {
		t.Fatalf("kind filter: %+v", only)
	}
	scoped := decodeReview(t, callTool(t, s, "kb_review", `{"scope":"hidden"}`))
	if scoped.Total != 1 {
		t.Fatalf("scope filter: %+v", scoped)
	}
	if res := callTool(t, s, "kb_review", `{"kind":"bogus"}`); !res.IsError || !strings.Contains(res.Content[0].Text, "glossary_gap") {
		t.Fatalf("invalid kind not rejected with the list: %+v", res)
	}
}

// TestKBReviewRestrictedCaller: dispatch refuses a map-scoped token (a
// whole-KB resource), and the handler's own filter drops every item naming a
// concept the caller cannot see.
func TestKBReviewRestrictedCaller(t *testing.T) {
	s, _ := reviewServer(t)
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"visible"}}}})
	if res := s.callTool(ctx, "kb_review", json.RawMessage(`{}`)); !res.IsError {
		t.Fatalf("map-scoped token reached kb_review: %+v", res.Content)
	}
	res, err := s.Tools()["kb_review"].Handler(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	out := decodeReview(t, res)
	if strings.Contains(res.Content[0].Text, "hidden/") {
		t.Fatalf("hidden concept leaked: %s", res.Content[0].Text)
	}
	if out.ByKind["duplicate_candidate"] != 1 {
		t.Fatalf("visible duplicate lost: %+v", out)
	}
}

func TestKBStatusReportsReview(t *testing.T) {
	s, _ := reviewServer(t)
	got := kbStatusResult(t, s)
	var review struct {
		Total  int            `json:"total"`
		ByKind map[string]int `json:"by_kind"`
	}
	if err := json.Unmarshal(got["review"], &review); err != nil {
		t.Fatalf("kb_status.review: %v (%s)", err, got["review"])
	}
	if review.Total != 4 || review.ByKind["duplicate_candidate"] != 2 {
		t.Fatalf("kb_status.review = %+v", review)
	}
}

type similarOut struct {
	Similar []struct {
		ID string `json:"id"`
	} `json:"similar"`
}

func writeResult(t *testing.T, res ToolResult) similarOut {
	t.Helper()
	if res.IsError {
		t.Fatalf("write error: %+v", res.Content)
	}
	var out similarOut
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestConceptWriteSimilarOnCreation(t *testing.T) {
	k := setupTestKB(t)
	writeKBFile(t, k, "manutenzione/restore-backup.md", "---\ntype: Runbook\ntitle: Restore backup procedure\n---\n# R\n\nSteps to restore a backup.\n")
	writeKBFile(t, k, "manutenzione/backup-schedule.md", "---\ntype: Runbook\ntitle: Backup schedule\n---\n# S\n\nWhen backups run.\n")
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	write := func(id, title string) similarOut {
		args, _ := json.Marshal(map[string]interface{}{"id": id, "frontmatter": map[string]interface{}{"type": "Runbook", "title": title}, "body": "# T\n"})
		return writeResult(t, callTool(t, s, "concept_write", string(args)))
	}
	near := write("manutenzione/backup-restore", "Backup restore procedure")
	if len(near.Similar) != 1 || near.Similar[0].ID != "manutenzione/restore-backup" {
		t.Fatalf("near-duplicate not advised: %+v", near)
	}
	// One shared word ("backup") is not similar.
	if far := write("manutenzione/backup-encryption-keys", "Backup encryption keys"); len(far.Similar) != 0 {
		t.Fatalf("unrelated title advised: %+v", far)
	}
	// An update of an existing ID never returns similar.
	if upd := write("manutenzione/backup-restore", "Backup restore procedure"); len(upd.Similar) != 0 {
		t.Fatalf("update advised similar: %+v", upd)
	}
}

func TestConceptNewSimilarOnCreation(t *testing.T) {
	k := setupTestKB(t)
	writeKBFile(t, k, "manutenzione/restore-backup.md", "---\ntype: Runbook\ntitle: Restore backup procedure\n---\n# R\n")
	tpl := filepath.Join(k.Root, "templates", "runbook.md")
	if err := os.MkdirAll(filepath.Dir(tpl), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tpl, []byte("---\ntype: Runbook\ntitle: \"{{title}}\"\n---\n# {{title}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	out := writeResult(t, callTool(t, s, "concept_new", `{"template":"runbook","id":"manutenzione/backup-restore","vars":{"title":"Backup restore procedure"}}`))
	if len(out.Similar) != 1 || out.Similar[0].ID != "manutenzione/restore-backup" {
		t.Fatalf("concept_new similar: %+v", out)
	}
}
