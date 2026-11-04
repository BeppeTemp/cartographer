package search

import "unicode"

// foldTable maps each lowercase accented letter of Latin-1 Supplement and Latin
// Extended-A to its base letter (FoldRune lowercases first, so an uppercase key
// would never be reached), one rune to one rune (D246). Letters with no
// single-letter base — ß, æ, œ, ĳ, þ, ŋ, ĸ, ŉ — are absent and left alone.
// One rune for one rune is what lets a caller search a folded copy of a text
// and cut the match out of the original at the same rune offsets.
var foldTable = func() map[rune]rune {
	groups := map[rune]string{
		'a': "àáâãäåāăą",
		'c': "çćĉċč",
		'd': "ðďđ",
		'e': "èéêëēĕėęě",
		'g': "ĝğġģ",
		'h': "ĥħ",
		'i': "ìíîïĩīĭįı",
		'j': "ĵ",
		'k': "ķ",
		'l': "ĺļľŀł",
		'n': "ñńņň",
		'o': "òóôõöøōŏő",
		'r': "ŕŗř",
		's': "śŝşšſ",
		't': "ţťŧ",
		'u': "ùúûüũūŭůűų",
		'w': "ŵ",
		'y': "ýÿŷ",
		'z': "źżž",
	}
	t := make(map[rune]rune)
	for base, letters := range groups {
		for _, r := range letters {
			t[r] = base
		}
	}
	return t
}()

// FoldRune lowercases r and strips its diacritic, always returning exactly
// one rune. unicode.ToLower is used rather than strings.ToLower because the
// latter may change the rune count ("İ" lowercases to two runes).
func FoldRune(r rune) rune {
	r = unicode.ToLower(r)
	if b, ok := foldTable[r]; ok {
		return b
	}
	return r
}

// Fold returns s lowercased and diacritic-folded, rune for rune: the result
// has exactly as many runes as s, so a rune offset found in it is valid in s.
func Fold(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		out = append(out, FoldRune(r))
	}
	return string(out)
}
