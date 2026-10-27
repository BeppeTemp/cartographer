package search

import (
	"math"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// Section weights, matching the sqlindex sections_fts bm25 weights: a heading
// token counts three times a body token.
const (
	sectionHeadingWeight = 3
	sectionBodyWeight    = 1
)

// BestSection returns the level-2 section of body that best matches query
// (okf.Level2Sections: the heading as concept_read accepts it, the title and the
// text), and false when no section holds any query term. It is the in-memory
// twin of sqlindex.BestSection (the page index has no FTS): terms are the
// folded tokens of query, heading tokens weigh three, body tokens one, and a
// bm25-like saturation and length normalisation keeps a long section from
// winning on repetition. Ties go to the earlier section.
func BestSection(body, query string) (okf.Section, bool) {
	secs := okf.Level2Sections(body)
	terms := Tokenize(query)
	if len(secs) == 0 || len(terms) == 0 {
		return okf.Section{}, false
	}
	type counted struct {
		head, body map[string]int
		length     int
	}
	cs := make([]counted, len(secs))
	total := 0
	for i, s := range secs {
		c := counted{head: map[string]int{}, body: map[string]int{}}
		for _, t := range Tokenize(s.Title) {
			c.head[t]++
			c.length++
		}
		for _, t := range Tokenize(s.Text) {
			c.body[t]++
			c.length++
		}
		cs[i] = c
		total += c.length
	}
	avg := math.Max(float64(total)/float64(len(secs)), 1)
	const k1, b = 1.2, 0.75
	best, bestScore := -1, 0.0
	for i, c := range cs {
		norm := 1 - b + b*float64(c.length)/avg
		score := 0.0
		for _, t := range terms {
			tf := float64(sectionHeadingWeight*c.head[t] + sectionBodyWeight*c.body[t])
			if tf > 0 {
				score += tf * (k1 + 1) / (tf + k1*norm)
			}
		}
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	if best < 0 {
		return okf.Section{}, false
	}
	return secs[best], true
}
