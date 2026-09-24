// Package search implements an in-memory inverted keyword index for KB concepts.
// The index is derived and regenerable from .md files (vault = truth, index = disposable).
package search

import (
	"sort"
	"strings"
	"unicode"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// Index is an in-memory inverted keyword index.
type Index struct {
	inverted map[string]map[string]int // term → conceptID → weighted term frequency
	docLen   map[string]int            // conceptID → weighted token count
	// docTerms records each document's distinct terms, so removing one
	// touches only its own postings instead of scanning the vocabulary.
	docTerms map[string][]string
}

// Field weights (D246), matching the SQLite backend's bm25 weights: a title
// hit beats several body hits, a frontmatter hit (type, tags, status) is
// worth more than prose but less than the name.
const (
	titleWeight = 5
	metaWeight  = 2
	bodyWeight  = 1
)

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
		docTerms: make(map[string][]string),
	}
}

// Add indexes the content of a concept — its raw file, frontmatter and body.
// Title tokens count titleWeight times, frontmatter tokens metaWeight times,
// body tokens once (D246). Calling Add again with the same id replaces the
// previous entry.
func (idx *Index) Add(id string, content string) {
	idx.remove(id)
	fields := SplitFields(content)
	counts := make(map[string]int)
	total := 0
	for _, f := range []struct {
		text   string
		weight int
	}{{fields.Title, titleWeight}, {fields.Meta, metaWeight}, {fields.Body, bodyWeight}} {
		for _, tok := range Tokenize(f.text) {
			counts[tok] += f.weight
			total += f.weight
		}
	}
	idx.docLen[id] = total
	terms := make([]string, 0, len(counts))
	for tok, n := range counts {
		if _, ok := idx.inverted[tok]; !ok {
			idx.inverted[tok] = make(map[string]int)
		}
		idx.inverted[tok][id] = n
		terms = append(terms, tok)
	}
	idx.docTerms[id] = terms
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
	for _, term := range idx.docTerms[id] {
		postings := idx.inverted[term]
		delete(postings, id)
		if len(postings) == 0 {
			delete(idx.inverted, term)
		}
	}
	delete(idx.docLen, id)
	delete(idx.docTerms, id)
}

// Search returns hits matching the query, scored by term-frequency relevance.
// It first requires all query terms, then falls back to any term if needed.
// If scope is non-empty, only concepts whose ID starts with scope are returned.
func (idx *Index) Search(query string, scope string, limit int) []Hit {
	return idx.SearchFiltered(query, scope, limit, nil)
}

// SearchFiltered returns the highest-ranked hits accepted by allow. Filtering
// happens after ranking and before limit so inaccessible hits never consume a
// caller's page. A nil predicate permits every hit.
func (idx *Index) SearchFiltered(query string, scope string, limit int, allow func(id string) bool) []Hit {
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
	if allow == nil {
		if len(hits) > limit {
			return hits[:limit]
		}
		return hits
	}
	visible := make([]Hit, 0, min(limit, len(hits)))
	for _, hit := range hits {
		if !allow(hit.ID) {
			continue
		}
		visible = append(visible, hit)
		if len(visible) == limit {
			break
		}
	}
	return visible
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

// Tokenize splits text into lowercase, diacritic-folded word tokens suitable
// for indexing ("Attività" → "attivita", D246).
func Tokenize(text string) []string {
	var tokens []string
	var buf strings.Builder
	for _, r := range Fold(text) {
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

// Fields is a concept's content split the way both search backends weight it.
type Fields struct {
	Title string // the frontmatter title; empty when absent or unparseable
	Meta  string // the raw frontmatter
	Body  string // the body, frontmatter stripped
}

// SplitFields splits a concept's raw content into its weighted fields. An
// unparseable frontmatter keeps its raw text in Meta with an empty Title.
func SplitFields(content string) Fields {
	fmRaw, body, _ := okf.SplitFrontmatter(content)
	f := Fields{Meta: fmRaw, Body: body}
	if fm, err := okf.ParseFrontmatter(fmRaw); err == nil {
		if v, ok := fm.Get("title"); ok {
			f.Title, _ = v.(string)
		}
	}
	return f
}
