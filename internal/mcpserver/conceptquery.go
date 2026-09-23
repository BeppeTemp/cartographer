package mcpserver

import (
	"path"
	"sort"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// ConceptEntry is the stable metadata every read surface needs about one
// concept. It is deliberately flat and free of prose: concept_list renders a
// subset of it as JSON text for an agent, and the UI API serves all of it to a
// browser, from the same walk.
type ConceptEntry struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	Type  string `json:"type,omitempty"`
	// Status is the frontmatter `status`, when it is a string. A missing or
	// non-string value is the empty string, never an error: a KB is allowed to
	// have concepts that do not model a lifecycle.
	Status string `json:"status,omitempty"`
	// Collection is the top-level Map or Journal — the first path segment.
	Collection string `json:"collection,omitempty"`
	// Expanded is true when the body lives at "<id>/index.md".
	Expanded bool `json:"expanded,omitempty"`
	// Timestamp is the raw frontmatter value, unparsed: the query filters on
	// it, a client displays it, and neither should have to agree on a layout.
	Timestamp string `json:"timestamp,omitempty"`
}

// ConceptQuery selects concepts from a KB. It holds no authorization logic:
// Include is the caller's own visibility predicate, so the MCP handler and the
// HTTP adapter run the same walk under their own principal.
type ConceptQuery struct {
	// Scope is a path prefix relative to the KB root, without a trailing
	// slash. Empty means the whole KB.
	Scope   string
	Filters []conceptListFilter
	Before  *time.Time
	After   *time.Time
	Include func(id string) bool
}

// ConceptQueryResult is the sorted inventory plus the accounting concept_list
// reports: Examined counts the concepts the scope and the visibility predicate
// let through, before any frontmatter filter, and SkippedTimestamp counts
// those dropped for lacking a usable timestamp while a timestamp bound was in
// force.
type ConceptQueryResult struct {
	Entries          []ConceptEntry
	Examined         int
	SkippedTimestamp int
}

// queryConcepts walks the KB once and returns the matching concepts sorted by
// id. It never truncates: the node/result limit belongs to the caller, which
// also has to report what it cut.
func queryConcepts(k *kb.KB, q ConceptQuery) (ConceptQueryResult, error) {
	visible := q.Include
	if visible == nil {
		visible = func(string) bool { return true }
	}
	filtersApplied := len(q.Filters) > 0 || q.Before != nil || q.After != nil
	timestampFilter := q.Before != nil || q.After != nil

	var res ConceptQueryResult
	res.Entries = []ConceptEntry{}
	err := k.WalkConceptPaths(func(id okf.ConceptID, physicalPath, content string) error {
		idStr := string(id)
		if q.Scope != "" && idStr != q.Scope && !hasPathPrefix(idStr, q.Scope) {
			return nil
		}
		if !visible(idStr) {
			return nil
		}
		res.Examined++
		fmRaw, _, _ := okf.SplitFrontmatter(content)
		entry := ConceptEntry{
			ID:         idStr,
			Collection: conceptCollection(idStr),
			Expanded:   physicalPath == path.Join(idStr, "index.md"),
		}
		fm, parseErr := okf.ParseFrontmatter(fmRaw)
		if parseErr == nil {
			entry.Title = frontmatterString(fm, "title")
			entry.Type = fm.Type()
			entry.Status = frontmatterString(fm, "status")
			entry.Timestamp = frontmatterString(fm, "timestamp")
		}
		if filtersApplied {
			// Malformed frontmatter is examined but cannot match a filter.
			if parseErr != nil || !matchesConceptListFilters(fm, q.Filters) {
				return nil
			}
			if timestampFilter {
				value, exists := fm.Get("timestamp")
				timestamp, timestampOK := value.(string)
				parsed, timestampErr := parseConceptListTimestamp(timestamp)
				if !exists || !timestampOK || timestampErr != nil {
					res.SkippedTimestamp++
					return nil
				}
				if (q.Before != nil && !parsed.Before(*q.Before)) || (q.After != nil && !parsed.After(*q.After)) {
					return nil
				}
			}
		}
		res.Entries = append(res.Entries, entry)
		return nil
	})
	if err != nil {
		return ConceptQueryResult{}, err
	}
	sort.Slice(res.Entries, func(i, j int) bool { return res.Entries[i].ID < res.Entries[j].ID })
	return res, nil
}

// hasPathPrefix reports whether id lives under the scope prefix. It compares
// whole segments, so scope "infra" does not swallow "infrastructure/x".
func hasPathPrefix(id, scope string) bool {
	return len(id) > len(scope) && id[:len(scope)] == scope && id[len(scope)] == '/'
}

// conceptCollection is the top-level Map or Journal of a concept id. A concept
// sitting at the KB root has none.
func conceptCollection(id string) string {
	for i := 0; i < len(id); i++ {
		if id[i] == '/' {
			return id[:i]
		}
	}
	return ""
}

// frontmatterString reads a frontmatter key that is only meaningful as a
// string. A missing key, or one holding a list or a number, yields "".
func frontmatterString(fm *okf.Frontmatter, key string) string {
	v, ok := fm.Get(key)
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}
