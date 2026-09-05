package mcpserver

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/search"
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

// TestLiveIndexConcurrentSearchAndWrite guards the lock discipline that keeps
// the in-memory index safe under the HTTP transport, where search and
// concept_write run on different goroutines. search.Index is a pair of plain
// maps with no synchronization of its own, so a search that ranks outside
// liveIndex's read lock races every concurrent add/remove. Run with -race:
// before searchFiltered held the lock for the whole query, this failed with a
// write/read race on search.Index's inverted map.
func TestLiveIndexConcurrentSearchAndWrite(t *testing.T) {
	live := newLiveIndex(search.New(), nil)
	for i := 0; i < 50; i++ {
		live.add(fmt.Sprintf("maps/seed%d", i), "---\ntitle: t\n---\nalpha beta gamma")
	}

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				live.add(fmt.Sprintf("maps/w%d-%d", w, i), "---\ntitle: t\n---\nalpha delta")
			}
		}(w)
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				live.searchFiltered("alpha", "", 10, nil)
			}
		}()
	}
	wg.Wait()
}
