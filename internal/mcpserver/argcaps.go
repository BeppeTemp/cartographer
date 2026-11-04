package mcpserver

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/search"
)

// Argument caps of the read tools: the "Argument caps" entry of docs/control-plane.md.
//
// A read tool does work proportional to the size of some of its arguments: a
// search expands and runs its terms, a filter list is matched against every
// concept, a sort key is compared on every comparison. Each such argument has
// a cap, and a call above it is refused with an error that names the cap and
// the size received. It is never truncated: a query or a filter that is
// silently shortened answers a different question than the one asked, and an
// agent cannot tell.
//
// A cap is a contract: raising one is a change to the tool's interface.
const (
	// maxSearchQueryBytes and maxSearchQueryTerms bound the query of search
	// and of graph_context.
	maxSearchQueryBytes = 1024
	maxSearchQueryTerms = 64

	// maxWhereClauses bounds where of concept_list and work_list.
	maxWhereClauses = 16

	// maxOrderByKeys bounds order_by of work_list.
	maxOrderByKeys = 8

	// maxWorkListFields bounds fields of work_list, as concept_list's fields
	// (maxConceptListFields, D343).
	maxWorkListFields = maxConceptListFields

	// minGraphDepth and maxGraphDepth clamp the depth of graph_neighbors, as
	// graph_path clamps max_hops to 1-12: a depth is clamped and reported, not
	// refused.
	minGraphDepth = 1
	maxGraphDepth = 12
)

// queryTermCount counts the terms of a keyword query both ways the two search
// backends split it, as whitespace-separated words (SQLite FTS5) and as runs of
// letters and digits (the in-memory index), and the larger count decides.
func queryTermCount(query string) int {
	return max(len(strings.Fields(query)), len(search.Tokenize(query)))
}

// checkQuerySize refuses a keyword query above maxSearchQueryBytes bytes or
// maxSearchQueryTerms terms. The size is checked first, so counting the terms
// never reads more than the cap.
func checkQuerySize(tool, query string) error {
	if len(query) > maxSearchQueryBytes {
		return fmt.Errorf("%s: query is %d bytes: at most %d bytes and %d terms", tool, len(query), maxSearchQueryBytes, maxSearchQueryTerms)
	}
	if n := queryTermCount(query); n > maxSearchQueryTerms {
		return fmt.Errorf("%s: query has %d terms: at most %d terms and %d bytes", tool, n, maxSearchQueryTerms, maxSearchQueryBytes)
	}
	return nil
}

// boundedQuery is query within the query caps: unchanged when it is, else its
// first terms, cut at a word boundary, that fit in maxSearchQueryBytes bytes
// and maxSearchQueryTerms terms ("" when not even the first word does).
//
// It is for a query the server builds from stored text (a concept's title),
// which only advises: a tool's own query argument is refused above the caps by
// checkQuerySize, never shortened. expandedKeywordHits applies it, so every
// search the server runs, whoever built the query, stays within the caps.
func boundedQuery(query string) string {
	if checkQuerySize("", query) == nil {
		return query
	}
	head := query
	if len(head) > maxSearchQueryBytes {
		n := maxSearchQueryBytes
		for n > 0 && !utf8.RuneStart(head[n]) {
			n--
		}
		// A cut inside a word leaves a fragment that matches nothing the
		// author wrote: drop it, unless it is the only word.
		if r, _ := utf8.DecodeRuneInString(head[n:]); !unicode.IsSpace(r) {
			if i := strings.LastIndexFunc(head[:n], unicode.IsSpace); i > 0 {
				n = i
			}
		}
		head = head[:n]
	}
	var out strings.Builder
	tokens := 0
	for i, word := range strings.Fields(head) {
		tokens += len(search.Tokenize(word))
		if i >= maxSearchQueryTerms || tokens > maxSearchQueryTerms {
			break
		}
		if i > 0 {
			out.WriteByte(' ')
		}
		out.WriteString(word)
	}
	return out.String()
}

// checkWhereClauses refuses a where list above maxWhereClauses entries, before
// any entry is parsed.
func checkWhereClauses(tool string, where []string) error {
	if len(where) > maxWhereClauses {
		return fmt.Errorf("%s: where: %d clauses: at most %d", tool, len(where), maxWhereClauses)
	}
	return nil
}

// checkWorkListArgs refuses a work_list call whose where, order_by or fields
// list is above its cap.
func checkWorkListArgs(where, orderBy, fields []string) error {
	if err := checkWhereClauses("work_list", where); err != nil {
		return err
	}
	if len(orderBy) > maxOrderByKeys {
		return fmt.Errorf("work_list: order_by: %d keys: at most %d", len(orderBy), maxOrderByKeys)
	}
	if len(fields) > maxWorkListFields {
		return fmt.Errorf("work_list: fields: %d keys: at most %d", len(fields), maxWorkListFields)
	}
	return nil
}
