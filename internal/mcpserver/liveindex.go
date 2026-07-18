package mcpserver

import (
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/search"
)

// conceptMeta holds the cheap-to-derive per-concept data needed by search
// results (title, snippet) without requiring a ReadConcept call in the search
// handler (D70): the frontmatter title and the body (frontmatter stripped),
// kept in sync with the keyword index.
type conceptMeta struct {
	Title string
	Body  string
}

// liveIndex is a swappable, concurrency-safe handle to the in-memory keyword
// index. Multiple tool calls (concept_write, search, index_rebuild) can run
// concurrently under the HTTP transport; liveIndex synchronizes access to the
// underlying *search.Index instead of relying on an unsynchronized **search.Index.
type liveIndex struct {
	mu   sync.RWMutex
	idx  *search.Index
	meta map[string]conceptMeta
}

// newLiveIndex wraps an already-built index and its parallel metadata map.
func newLiveIndex(idx *search.Index, meta map[string]conceptMeta) *liveIndex {
	if meta == nil {
		meta = make(map[string]conceptMeta)
	}
	return &liveIndex{idx: idx, meta: meta}
}

// get returns the current index for read-only use (e.g. Search).
func (l *liveIndex) get() *search.Index {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.idx
}

// title returns the frontmatter title for id, or "" if id is unknown or has
// no title.
func (l *liveIndex) title(id string) string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.meta[id].Title
}

// snippet returns an excerpt (~maxChars) of id's body around the first
// occurrence of any term in query, falling back to the first maxChars of the
// body if no term matches (or query is empty). Empty if id is unknown.
func (l *liveIndex) snippet(id, query string, maxChars int) string {
	l.mu.RLock()
	body := l.meta[id].Body
	l.mu.RUnlock()
	return extractSnippet(body, query, maxChars)
}

// swap atomically replaces the index and metadata map (used by index_rebuild).
func (l *liveIndex) swap(idx *search.Index, meta map[string]conceptMeta) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.idx = idx
	l.meta = meta
}

// add incrementally indexes a single concept (used by concept_write).
// content is the raw file content (frontmatter + body).
func (l *liveIndex) add(id, content string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.idx.Add(id, content)
	l.meta[id] = parseConceptMeta(content)
}

// remove incrementally deindexes a single concept (used by concept_delete).
func (l *liveIndex) remove(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.idx.Remove(id)
	delete(l.meta, id)
}

// parseConceptMeta extracts the frontmatter title and body from a concept's
// raw content (frontmatter + body).
func parseConceptMeta(content string) conceptMeta {
	fmRaw, body, _ := okf.SplitFrontmatter(content)
	var title string
	if fm, err := okf.ParseFrontmatter(fmRaw); err == nil {
		if v, ok := fm.Get("title"); ok {
			if s, ok := v.(string); ok {
				title = s
			}
		}
	}
	return conceptMeta{Title: title, Body: body}
}

// extractSnippet returns an excerpt (~maxChars runes) of body around the
// first occurrence of any tokenized term of query, or the first maxChars
// runes of body as a fallback (no term found, or empty query/body).
func extractSnippet(body, query string, maxChars int) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	if maxChars <= 0 {
		maxChars = 200
	}

	lower := strings.ToLower(body)
	bytePos := -1
	for _, term := range search.Tokenize(query) {
		if idx := strings.Index(lower, term); idx >= 0 && (bytePos < 0 || idx < bytePos) {
			bytePos = idx
		}
	}

	runes := []rune(body)
	if bytePos < 0 {
		if len(runes) <= maxChars {
			return string(runes)
		}
		return strings.TrimSpace(string(runes[:maxChars])) + "…"
	}

	pos := utf8.RuneCountInString(body[:bytePos])
	start := pos - maxChars/2
	if start < 0 {
		start = 0
	}
	end := start + maxChars
	if end > len(runes) {
		end = len(runes)
		start = end - maxChars
		if start < 0 {
			start = 0
		}
	}

	snippet := string(runes[start:end])
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(runes) {
		snippet = snippet + "…"
	}
	return strings.TrimSpace(snippet)
}
