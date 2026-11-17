package search

import (
	"strings"
	"testing"
	"time"
)

func TestExcerptAroundFirstMatch(t *testing.T) {
	text := strings.Repeat("lorem ", 100) + "the Needle is here " + strings.Repeat("ipsum ", 100)
	got, found := Excerpt(text, []string{"needle"}, 40)
	if !found || !strings.Contains(got, "Needle") || !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") {
		t.Fatalf("excerpt = %q found=%v", got, found)
	}
	if got, found := Excerpt("Città vecchia", []string{"citta"}, 40); !found || got != "Città vecchia" {
		t.Fatalf("folded match: %q %v", got, found)
	}
	if got, found := Excerpt("abc def", []string{"zzz"}, 40); found || got != "abc def" {
		t.Fatalf("no match: %q %v", got, found)
	}
}

// The text read is capped: a megabyte costs what the cap costs (#2189).
func TestExcerptIsBounded(t *testing.T) {
	text := strings.Repeat("x", 1<<20)
	start := time.Now()
	got, found := Excerpt(text, []string{"xxxx"}, 200)
	if !found || len([]rune(got)) > 202 || time.Since(start) > time.Second {
		t.Fatalf("len=%d found=%v in %v", len(got), found, time.Since(start))
	}
	late := strings.Repeat("a", ExcerptScanBytes+10) + "needle"
	if got, found := Excerpt(late, []string{"needle"}, 20); found || got == "" {
		t.Fatalf("a match past the cap: %q %v", got, found)
	}
}
