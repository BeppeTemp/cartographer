// Package search implements an in-memory inverted keyword index for KB concepts.
// The index is derived and regenerable from .md files (vault = truth, index = disposable).
package search

import (
	"sort"
	"strings"
	"unicode"
)

// Index is an in-memory inverted keyword index.
type Index struct {
	inverted map[string]map[string]int // term → conceptID → term frequency
	docLen   map[string]int            // conceptID → total token count
}

// Hit represents a single search result.
type Hit struct {
	ID    string  // concept ID
	Score float64 // relevance score (higher = better)
}

// New creates an empty Index.
func New() *Index {
	return &Index{
		inverted: make(map[string]map[string]int),
		docLen:   make(map[string]int),
	}
}

// Add indexes the content of a concept. Calling Add again with the same id
// replaces the previous entry.
func (idx *Index) Add(id string, content string) {
	idx.remove(id)
	tokens := Tokenize(content)
	idx.docLen[id] = len(tokens)
	for _, tok := range tokens {
		if _, ok := idx.inverted[tok]; !ok {
			idx.inverted[tok] = make(map[string]int)
		}
		idx.inverted[tok][id]++
	}
}

// Remove deletes a concept from the index (used by concept_delete).
func (idx *Index) Remove(id string) {
	idx.remove(id)
}

// remove deletes a concept from the index.
func (idx *Index) remove(id string) {
	if _, ok := idx.docLen[id]; !ok {
		return
	}
	for term, postings := range idx.inverted {
		delete(postings, id)
		if len(postings) == 0 {
			delete(idx.inverted, term)
		}
	}
	delete(idx.docLen, id)
}

// Search returns hits matching the query, scored by term-frequency relevance.
// It first requires all query terms, then falls back to any term if needed.
// If scope is non-empty, only concepts whose ID starts with scope are returned.
func (idx *Index) Search(query string, scope string, limit int) []Hit {
	if limit <= 0 {
		limit = 20
	}
	terms := Tokenize(query)
	if len(terms) == 0 {
		return nil
	}

	candidates := idx.andCandidates(terms)
	if len(candidates) == 0 && len(terms) >= 2 {
		candidates = idx.orCandidates(terms)
	}

	var hits []Hit
	for id := range candidates {
		if scope != "" && !strings.HasPrefix(id, scope) {
			continue
		}
		dl := idx.docLen[id]
		if dl == 0 {
			dl = 1
		}
		score := 0.0
		for _, term := range terms {
			tf := 0
			if p, ok := idx.inverted[term]; ok {
				tf = p[id]
			}
			score += float64(tf) / float64(dl)
		}
		hits = append(hits, Hit{ID: id, Score: score})
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].ID < hits[j].ID
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

func (idx *Index) andCandidates(terms []string) map[string]int {
	candidates := idx.posting(terms[0])
	if candidates == nil {
		return nil
	}
	for _, term := range terms[1:] {
		next := idx.posting(term)
		if next == nil {
			return nil
		}
		for id := range candidates {
			if _, ok := next[id]; !ok {
				delete(candidates, id)
			}
		}
		if len(candidates) == 0 {
			return nil
		}
	}
	return candidates
}

func (idx *Index) orCandidates(terms []string) map[string]int {
	candidates := make(map[string]int)
	for _, term := range terms {
		for id := range idx.posting(term) {
			candidates[id] = 1
		}
	}
	return candidates
}

// posting returns a copy of the posting list for a term.
func (idx *Index) posting(term string) map[string]int {
	p, ok := idx.inverted[term]
	if !ok {
		return nil
	}
	cp := make(map[string]int, len(p))
	for k, v := range p {
		cp[k] = v
	}
	return cp
}

// Count returns the number of indexed concepts.
func (idx *Index) Count() int {
	return len(idx.docLen)
}

// Tokenize splits text into lowercase word tokens suitable for indexing.
func Tokenize(text string) []string {
	var tokens []string
	var buf strings.Builder
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			buf.WriteRune(r)
		} else {
			if buf.Len() > 0 {
				tokens = append(tokens, buf.String())
				buf.Reset()
			}
		}
	}
	if buf.Len() > 0 {
		tokens = append(tokens, buf.String())
	}
	return tokens
}
