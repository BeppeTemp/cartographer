package client

import (
	"strings"
	"testing"
)

func TestEscapeNeutralisesControlBidiAndZeroWidth(t *testing.T) {
	for _, in := range []string{"a\x1b[2Jb", "a\x1b]0;t\x07", "a\nb", "a\x7fb", "a\u0085b", "a\u202eb", "a\u2066b", "a\u200bb", "a\ufeffb", "a\u200fb"} {
		out := Escape(in)
		if out == in || strings.ContainsAny(out, "\x1b\x07\n\x7f\u0085\u202e\u2066\u200b\ufeff\u200f") {
			t.Errorf("Escape(%q) = %q", in, out)
		}
	}
	if got := Escape("caffè\tx"); got != "caffè\tx" {
		t.Errorf("printable text changed: %q", got)
	}
}

func TestEscapeCapCuts(t *testing.T) {
	if got := EscapeCap(strings.Repeat("a", 10), 4); got != "aaaa..." {
		t.Errorf("got %q", got)
	}
	if got := EscapeCap("a\x1b", 4); got != `a\x1b` {
		t.Errorf("got %q", got)
	}
}
