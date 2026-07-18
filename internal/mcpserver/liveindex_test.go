package mcpserver

import (
	"strings"
	"testing"
)

// TestExtractSnippet_AroundMatch verifies the in-memory snippet extraction
// (D70) returns an excerpt centered around the first occurrence of a query
// term, bounded by maxChars.
func TestExtractSnippet_AroundMatch(t *testing.T) {
	filler := strings.Repeat("parola ", 40) // ~280 chars of filler
	body := filler + "termineraro" + filler

	snip := extractSnippet(body, "termineraro", 100)
	if !strings.Contains(snip, "termineraro") {
		t.Fatalf("expected snippet to contain the match, got %q", snip)
	}
	if len(snip) > 130 { // 100 + ellipses/margin
		t.Errorf("expected snippet bounded to ~100 chars, got %d: %q", len(snip), snip)
	}
}

// TestExtractSnippet_FallbackNoMatch verifies the fallback to the first
// maxChars of the body when the query does not match (D70).
func TestExtractSnippet_FallbackNoMatch(t *testing.T) {
	body := "Prime righe del corpo del concetto, usate come fallback quando il termine cercato non compare nel testo."

	snip := extractSnippet(body, "assente", 20)
	if snip == "" {
		t.Fatal("expected non-empty fallback snippet")
	}
	if !strings.HasPrefix(body, strings.TrimSuffix(snip, "…")) {
		t.Errorf("fallback snippet should be a prefix of body, got %q", snip)
	}
}

// TestExtractSnippet_EmptyBody verifies an empty body yields an empty
// snippet.
func TestExtractSnippet_EmptyBody(t *testing.T) {
	if snip := extractSnippet("   ", "query", 100); snip != "" {
		t.Errorf("expected empty snippet for empty body, got %q", snip)
	}
}

// TestParseConceptMeta_Title verifies the frontmatter title is extracted and
// the body has the frontmatter stripped (D70).
func TestParseConceptMeta_Title(t *testing.T) {
	content := "---\ntype: Note\ntitle: Titolo Di Prova\n---\n# Corpo\n\nTesto.\n"
	meta := parseConceptMeta(content)
	if meta.Title != "Titolo Di Prova" {
		t.Errorf("expected title 'Titolo Di Prova', got %q", meta.Title)
	}
	if strings.Contains(meta.Body, "type: Note") {
		t.Errorf("expected frontmatter stripped from body, got %q", meta.Body)
	}
	if !strings.Contains(meta.Body, "Testo.") {
		t.Errorf("expected body content preserved, got %q", meta.Body)
	}
}
