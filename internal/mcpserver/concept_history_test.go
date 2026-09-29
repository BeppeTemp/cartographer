package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"
)

type historyEntry struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	Path    string `json:"path"`
}

type historyResult struct {
	ID        string         `json:"id"`
	Revisions []historyEntry `json:"revisions"`
	Truncated bool           `json:"truncated"`
	Note      string         `json:"note"`
}

func (c *changesLinksKB) history(args string) (historyResult, ToolResult) {
	c.t.Helper()
	res := c.s.callTool(authLocalContext(), "concept_history", json.RawMessage(args))
	var out historyResult
	if !res.IsError && len(res.Content) > 0 {
		if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
			c.t.Fatal(err)
		}
	}
	return out, res
}

func (c *changesLinksKB) read(args string) (map[string]interface{}, ToolResult) {
	c.t.Helper()
	res := c.s.callTool(authLocalContext(), "concept_read", json.RawMessage(args))
	var out map[string]interface{}
	if !res.IsError && len(res.Content) > 0 {
		if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
			c.t.Fatal(err)
		}
	}
	return out, res
}

func TestConceptHistoryAndReadAtRev(t *testing.T) {
	c := newChangesLinksKB(t)
	c.k.AutoCommit = true
	c.write("ops/page", "version one\n")
	c.commit(true)
	c.write("ops/page", "version two\n")
	c.commit(true)
	c.write("ops/page", "version three\n")
	c.commit(true)

	h, res := c.history(`{"id":"ops/page"}`)
	if res.IsError {
		t.Fatalf("concept_history: %+v", res)
	}
	if len(h.Revisions) != 3 || h.Truncated {
		t.Fatalf("history = %+v, want 3 revisions, not truncated", h)
	}
	if h.Revisions[0].Path != "data/ops/page.md" {
		t.Errorf("path = %q", h.Revisions[0].Path)
	}
	// Newest first: revisions[2] is version one, revisions[1] version two.
	got, res := c.read(`{"id":"ops/page","rev":"` + h.Revisions[1].SHA + `"}`)
	if res.IsError {
		t.Fatalf("concept_read at rev: %+v", res)
	}
	if body, _ := got["body"].(string); !strings.Contains(body, "version two") {
		t.Errorf("body at rev = %q, want version two", body)
	}
	if got["rev"] != h.Revisions[1].SHA {
		t.Errorf("rev = %v", got["rev"])
	}
	// An abbreviated SHA works, and a plain read is still the current version.
	short, _ := c.read(`{"id":"ops/page","rev":"` + h.Revisions[2].SHA[:8] + `"}`)
	if body, _ := short["body"].(string); !strings.Contains(body, "version one") {
		t.Errorf("body at short rev = %q, want version one", body)
	}
	cur, _ := c.read(`{"id":"ops/page"}`)
	if body, _ := cur["body"].(string); !strings.Contains(body, "version three") {
		t.Errorf("current body = %q", body)
	}
	if _, ok := cur["rev"]; ok {
		t.Error("a read without rev must not carry rev")
	}

	// limit clamps and truncation is reported.
	h2, _ := c.history(`{"id":"ops/page","limit":2}`)
	if len(h2.Revisions) != 2 || !h2.Truncated {
		t.Errorf("limit 2 = %+v, want 2 revisions and truncated", h2)
	}
}

func TestConceptReadAtRevAcrossRename(t *testing.T) {
	c := newChangesLinksKB(t)
	c.k.AutoCommit = true
	c.write("ops/before", "original text\n")
	c.commit(true)
	c.rename("ops/before", "ops/after")
	c.commit(true)
	c.write("ops/after", "edited text\n")
	c.commit(true)

	h, _ := c.history(`{"id":"ops/after"}`)
	if len(h.Revisions) != 3 {
		t.Fatalf("history across rename = %+v, want 3", h)
	}
	got, res := c.read(`{"id":"ops/after","rev":"` + h.Revisions[2].SHA + `"}`)
	if res.IsError {
		t.Fatalf("read at pre-rename rev: %+v", res)
	}
	if body, _ := got["body"].(string); !strings.Contains(body, "original text") {
		t.Errorf("body = %q, want original text", body)
	}
}

func TestConceptReadAtRevBeforeCreation(t *testing.T) {
	c := newChangesLinksKB(t)
	c.k.AutoCommit = true
	c.write("ops/early", "alpha bravo charlie delta echo foxtrot golf hotel india\n")
	c.commit(true)
	early, _ := c.history(`{"id":"ops/early"}`)
	c.write("ops/late", "completely different words about something else entirely\n")
	c.commit(true)

	_, res := c.read(`{"id":"ops/late","rev":"` + early.Revisions[0].SHA + `"}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "not found at rev") {
		t.Fatalf("read before creation = %+v, want not found at rev", res)
	}
}

func TestConceptReadInvalidRev(t *testing.T) {
	c := newChangesLinksKB(t)
	c.write("ops/page", "x\n")
	c.commit(true)
	for _, rev := range []string{"HEAD", "main", "HEAD~1", "--all", "abc", "zzzzzzz", strings.Repeat("a", 41)} {
		_, res := c.read(`{"id":"ops/page","rev":"` + rev + `"}`)
		if !res.IsError || !strings.Contains(res.Content[0].Text, "invalid rev") {
			t.Errorf("rev %q = %+v, want invalid rev", rev, res)
		}
	}
}

func TestConceptHistoryNotFoundAndNoGit(t *testing.T) {
	c := newChangesLinksKB(t)
	c.k.AutoCommit = true
	c.write("ops/page", "x\n")
	c.commit(true)
	_, res := c.history(`{"id":"ops/nope"}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, `concept_history "ops/nope": not found`) {
		t.Fatalf("missing concept = %+v", res)
	}
	c.k.AutoCommit = false
	h, res := c.history(`{"id":"ops/page"}`)
	if res.IsError || h.Note != "no git history" || len(h.Revisions) != 0 {
		t.Fatalf("auto-commit off = %+v %+v, want empty revisions and a note", h, res)
	}
}
