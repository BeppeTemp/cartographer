package search

import (
	"testing"
	"unicode"
)

// FoldRune lowercases before it looks the rune up, so an uppercase key in
// foldTable is never reached: every key must already be lowercase.
func TestFoldTableKeysAreLowercase(t *testing.T) {
	for k := range foldTable {
		if unicode.ToLower(k) != k {
			t.Errorf("foldTable key %q is not lowercase: FoldRune never reaches it", k)
		}
	}
	if got := Fold("ÀÉÎÕÜ àéîõü"); got != "aeiou aeiou" {
		t.Fatalf("Fold = %q", got)
	}
}
