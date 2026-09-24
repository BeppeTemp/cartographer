package okf

import (
	"reflect"
	"testing"
)

func TestPlaceholders(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{"none", "no placeholder here", nil},
		{"sorted and unique", "{{repo:b}} then {{path:a}} and again {{repo:b}}", []string{"path:a", "repo:b"}},
		{"escaped excluded", "write {{\\repo:x}} to cite a repo", nil},
		{"metasyntax excluded", "{{repo:<name>}} {{path:...}} {{repo:…}}", nil},
		{"mixed", "{{\\path:doc}} {{path:real}} {{repo:<k>}}", []string{"path:real"}},
		{"other kinds ignored", "{{env:HOME}} {{repo:}}", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Placeholders(c.text); !reflect.DeepEqual(got, c.want) {
				t.Errorf("Placeholders(%q) = %v, want %v", c.text, got, c.want)
			}
		})
	}
}

func TestIsPlaceholderMetasyntax(t *testing.T) {
	for _, k := range []string{"...", "…", "<name>", "<nome>"} {
		if !IsPlaceholderMetasyntax(k) {
			t.Errorf("%q should be metasyntax", k)
		}
	}
	for _, k := range []string{"cartographer", "github.com/o/r", "a..b"} {
		if IsPlaceholderMetasyntax(k) {
			t.Errorf("%q should not be metasyntax", k)
		}
	}
}

func TestParseAndSplitPlaceholder(t *testing.T) {
	p, ok := ParsePlaceholder([]byte(`{{\repo:x}}`))
	if !ok || !p.Escaped || p.Kind != "repo" || p.Key != "x" || p.ID() != "repo:x" {
		t.Errorf("ParsePlaceholder = %+v, %v", p, ok)
	}
	if _, ok := ParsePlaceholder([]byte("plain")); ok {
		t.Error("ParsePlaceholder accepted a non-placeholder")
	}
	if kind, key, ok := SplitPlaceholderID("path:a:b"); !ok || kind != "path" || key != "a:b" {
		t.Errorf("SplitPlaceholderID = %q %q %v", kind, key, ok)
	}
	for _, bad := range []string{"env:x", "repo:", "nokind"} {
		if _, _, ok := SplitPlaceholderID(bad); ok {
			t.Errorf("SplitPlaceholderID(%q) accepted", bad)
		}
	}
}
