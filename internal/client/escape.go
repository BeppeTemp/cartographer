package client

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Escape, EscapeCap and HasControl make text a remote party chose safe to print
// on a terminal; every client-side print of server-chosen text goes through
// them. Escape shows every rune that is not printable (a newline that forges a line,
// an ESC that rewrites the screen, a bidi override, a zero-width character,
// DEL, a C1 control) as its escape, tab excepted. Printable text, accented
// letters included, is untouched.
func Escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if !unicode.IsPrint(r) && r != '\t' {
			switch {
			case r == '\n':
				b.WriteString(`\n`)
			case r == '\r':
				b.WriteString(`\r`)
			case r < 0x80:
				fmt.Fprintf(&b, `\x%02x`, r)
			default:
				q := strconv.QuoteToASCII(string(r))
				b.WriteString(q[1 : len(q)-1])
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// EscapeCap is Escape on at most max runes of s; a longer text ends in "...".
func EscapeCap(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return Escape(string(r[:max])) + "..."
	}
	return Escape(s)
}

// HasControl reports whether s holds a control character (C0, DEL, C1).
func HasControl(s string) bool { return strings.IndexFunc(s, unicode.IsControl) >= 0 }
