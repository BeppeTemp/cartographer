package search

import (
	"strings"
	"unicode/utf8"
)

// ExcerptScanBytes bounds the text Excerpt reads: the excerpt of a concept
// costs the same whatever its size. FTS5's snippet() is quadratic in the
// matches inside one body under the trigram tokenizer, so a body of one
// megabyte of repeated characters never answered a search (#2189); a match
// past the cap is not marked, the excerpt then opens the text.
const ExcerptScanBytes = 64 << 10

// Excerpt cuts about maxChars runes of text around the first occurrence of any
// of terms (folded the way the indexes fold, D246) and reports whether one was
// found. Only the first ExcerptScanBytes of text are read; without a match the
// excerpt is the start of the text. The cut is marked with "…" on each side
// that continues.
func Excerpt(text string, terms []string, maxChars int) (string, bool) {
	if maxChars <= 0 {
		maxChars = 200
	}
	cut := len(text) > ExcerptScanBytes
	text = CapExcerptText(text)
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	folded := Fold(text)
	at := -1
	for _, term := range terms {
		term = Fold(term)
		if term == "" {
			continue
		}
		if i := strings.Index(folded, term); i >= 0 && (at < 0 || i < at) {
			at = i
		}
	}
	runes := []rune(text)
	start := 0
	if at >= 0 {
		start = utf8.RuneCountInString(folded[:at]) - maxChars/2
	}
	end := start + maxChars
	if end > len(runes) {
		end = len(runes)
		start = end - maxChars
	}
	start = max(start, 0)
	out := string(runes[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(runes) || cut {
		out += "…"
	}
	return strings.TrimSpace(out), at >= 0
}

// CapExcerptText is text cut to its first ExcerptScanBytes, at a rune start.
func CapExcerptText(text string) string {
	if len(text) <= ExcerptScanBytes {
		return text
	}
	n := ExcerptScanBytes
	for n > 0 && !utf8.RuneStart(text[n]) {
		n--
	}
	return text[:n]
}
