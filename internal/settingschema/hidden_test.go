package settingschema

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A Hidden key stays in the table (Lookup, the struct guards) but is written
// nowhere a person reads: not as a comment on a set key, not in the
// available-settings block, not in the generated example document.
func TestHiddenKeyIsNeverRendered(t *testing.T) {
	s := Schema{Keys: []Key{
		{Path: "shown", Scope: ScopeClient, Type: "string", Default: `""`, Description: "A visible key.", Example: "on"},
		{Path: "secretive", Scope: ScopeClient, Type: "string", Default: `""`, Description: "A hidden key.", Example: "on", Hidden: true},
		{Path: "secretmap", Scope: ScopeClient, Type: "map", Default: "{}", Description: "A hidden map.", Example: "a: b", Hidden: true},
	}}
	if k, ok := s.Lookup("secretive"); !ok || !k.Hidden {
		t.Fatal("a hidden key must stay in the table")
	}
	for _, src := range []string{"shown: x\n", "shown: x\nsecretive: y\nsecretmap:\n  a: b\n"} {
		var doc yaml.Node
		if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
			t.Fatal(err)
		}
		s.Render(&doc)
		out, err := Encode(&doc)
		if err != nil {
			t.Fatal(err)
		}
		text := string(out)
		if !strings.Contains(text, "A visible key.") && !strings.Contains(text, "shown") {
			t.Errorf("the visible key vanished:\n%s", text)
		}
		for _, l := range strings.Split(text, "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "#") && (strings.Contains(l, "hidden") || strings.Contains(l, "secret")) {
				t.Errorf("a comment mentions a hidden key: %q\n%s", l, text)
			}
		}
	}
	d, err := s.Document()
	if err != nil {
		t.Fatal(err)
	}
	out, _ := Encode(d)
	if strings.Contains(string(out), "secret") {
		t.Errorf("the example document carries a hidden key:\n%s", out)
	}
}
