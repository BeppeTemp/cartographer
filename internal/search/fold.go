package search

import "unicode"

// foldTable maps each accented letter of Latin-1 Supplement and Latin
// Extended-A to its base letter, one rune to one rune (D246). Letters with no
// single-letter base — ß, æ, œ, ĳ, þ, ŋ, ĸ, ŉ — are absent and left alone.
// One rune for one rune is what lets a caller search a folded copy of a text
// and cut the match out of the original at the same rune offsets.
var foldTable = func() map[rune]rune {
	groups := map[rune]string{
		'A': "ÀÁÂÃÄÅĀĂĄ", 'a': "àáâãäåāăą",
		'C': "ÇĆĈĊČ", 'c': "çćĉċč",
		'D': "ÐĎĐ", 'd': "ðďđ",
		'E': "ÈÉÊËĒĔĖĘĚ", 'e': "èéêëēĕėęě",
		'G': "ĜĞĠĢ", 'g': "ĝğġģ",
		'H': "ĤĦ", 'h': "ĥħ",
		'I': "ÌÍÎÏĨĪĬĮİ", 'i': "ìíîïĩīĭįı",
		'J': "Ĵ", 'j': "ĵ",
		'K': "Ķ", 'k': "ķ",
		'L': "ĹĻĽĿŁ", 'l': "ĺļľŀł",
		'N': "ÑŃŅŇ", 'n': "ñńņň",
		'O': "ÒÓÔÕÖØŌŎŐ", 'o': "òóôõöøōŏő",
		'R': "ŔŖŘ", 'r': "ŕŗř",
		'S': "ŚŜŞŠ", 's': "śŝşšſ",
		'T': "ŢŤŦ", 't': "ţťŧ",
		'U': "ÙÚÛÜŨŪŬŮŰŲ", 'u': "ùúûüũūŭůűų",
		'W': "Ŵ", 'w': "ŵ",
		'Y': "ÝŶŸ", 'y': "ýÿŷ",
		'Z': "ŹŻŽ", 'z': "źżž",
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
