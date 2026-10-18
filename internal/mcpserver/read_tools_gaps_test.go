package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// readConceptCall runs concept_read with the given arguments and returns the
// parsed JSON object.
func readConceptCall(t *testing.T, k *kb.KB, args map[string]any) map[string]any {
	t.Helper()
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	resps := runMCPSequence(t, s, []string{
		initMsg,
		artifactCallMsg(t, 2, "concept_read", args),
	})
	tr := decodeToolResult(t, resps[1])
	if tr.IsError {
		t.Fatalf("concept_read %v: isError=true: %v", args, tr.Content)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(tr.Content[0].Text), &out); err != nil {
		t.Fatalf("decode concept_read %v: %v", args, err)
	}
	return out
}

// TestConceptList_ContentHashPseudoField checks that content_hash in fields
// returns each concept's hash, equal to the if_match concept_read reports, and
// that it does not count against the 8 frontmatter-key cap (#835).
func TestConceptList_ContentHashPseudoField(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)

	s := New("test")
	RegisterKBTools(s, k, Deps{})

	var listed struct {
		Results []struct {
			ID          string `json:"id"`
			ContentHash string `json:"content_hash"`
		} `json:"results"`
	}
	raw := callOK(t, s, "concept_list", `{"scope":"manutenzione","fields":["content_hash"]}`)
	if err := json.Unmarshal([]byte(raw), &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(listed.Results) == 0 {
		t.Fatal("no results")
	}
	for _, r := range listed.Results {
		if r.ContentHash == "" {
			t.Errorf("%s: content_hash empty", r.ID)
		}
		// It must equal the hash a concept_read returns as if_match.
		read := readConceptCall(t, k, map[string]any{"id": r.ID})
		if read["content_hash"] != r.ContentHash {
			t.Errorf("%s: list hash %q != read hash %v", r.ID, r.ContentHash, read["content_hash"])
		}
	}

	// content_hash does not consume a frontmatter-field slot: 8 real keys plus
	// content_hash is still accepted.
	tr := s.callTool(authLocalContext(), "concept_list",
		json.RawMessage(`{"fields":["a","b","c","d","e","f","g","h","content_hash"]}`))
	if tr.IsError {
		t.Errorf("content_hash must not count toward the 8-key cap: %+v", tr)
	}
}

// TestConceptRead_MatchWithinSection greps a single section of a page (#835).
func TestConceptRead_MatchWithinSection(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	body := "---\ntype: Note\ntitle: Sectioned\n---\n" +
		"# Alpha\nneedle in alpha\nother line\n" +
		"# Beta\nneedle in beta\n"
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "manutenzione", "sectioned.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	// Grep restricted to Alpha finds only the alpha needle, not beta's.
	out := readConceptCall(t, k, map[string]any{
		"id":      "manutenzione/sectioned",
		"section": "# Alpha",
		"match":   "needle",
	})
	if out["section"] != "# Alpha" {
		t.Errorf("response must echo the section, got %v", out["section"])
	}
	matches, _ := json.Marshal(out["matches"])
	if !strings.Contains(string(matches), "alpha") {
		t.Errorf("expected the alpha match: %s", matches)
	}
	if strings.Contains(string(matches), "beta") {
		t.Errorf("match leaked outside the section: %s", matches)
	}
}

// TestConceptRead_MatchSectionNotFound errors like a plain section read when
// the named section is missing (#835).
func TestConceptRead_MatchSectionNotFound(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)

	s := New("test")
	RegisterKBTools(s, k, Deps{})
	resps := runMCPSequence(t, s, []string{
		initMsg,
		artifactCallMsg(t, 2, "concept_read", map[string]any{
			"id":      "manutenzione/test-runbook",
			"section": "# Nope",
			"match":   "x",
		}),
	})
	tr := decodeToolResult(t, resps[1])
	if !tr.IsError || !strings.Contains(tr.Content[0].Text, "not found") {
		t.Errorf("a missing section with match must error: %+v", tr)
	}
}

// TestConceptRead_FrontmatterInPartialReads checks that outline, section and
// the size-guard responses now carry the parsed frontmatter (#835).
func TestConceptRead_FrontmatterInPartialReads(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)

	// Outline carries frontmatter.
	out := readConceptCall(t, k, map[string]any{"id": "manutenzione/test-runbook", "outline": true})
	fm, ok := out["frontmatter"].(map[string]any)
	if !ok {
		t.Fatalf("outline must carry frontmatter: %v", out)
	}
	if fm["type"] != "Runbook" {
		t.Errorf("outline frontmatter type = %v, want Runbook", fm["type"])
	}

	// Section carries frontmatter.
	out = readConceptCall(t, k, map[string]any{"id": "manutenzione/test-runbook", "section": "# Schema"})
	if _, ok := out["frontmatter"].(map[string]any); !ok {
		t.Errorf("section must carry frontmatter: %v", out)
	}

	// Size-guard response carries frontmatter.
	big := "---\ntype: Note\ntitle: Big\n---\n# Grande\n\n" + strings.Repeat("a", int(okf.ConceptReadSizeGuard)+10) + "\n"
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "manutenzione", "big.md"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	out = readConceptCall(t, k, map[string]any{"id": "manutenzione/big"})
	if _, ok := out["note"]; !ok {
		t.Fatalf("expected the size-guard note: %v", out)
	}
	if _, ok := out["frontmatter"].(map[string]any); !ok {
		t.Errorf("size-guard response must carry frontmatter: %v", out)
	}
}
