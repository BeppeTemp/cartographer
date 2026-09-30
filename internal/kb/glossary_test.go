package kb

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const testGlossary = `terms:
  - canonical: Home Assistant
    aliases: [HA, hass]
    forbidden: [HomeAssistant]
  - canonical: Kubernetes
    aliases: [k8s]
`

func TestValidateGlossary_Accepts(t *testing.T) {
	for _, c := range []string{testGlossary, "", "terms:\n", "terms: []\n", "terms:\n  - canonical: Solo\n"} {
		if err := ValidateGlossary([]byte(c)); err != nil {
			t.Errorf("ValidateGlossary(%q): %v", c, err)
		}
	}
}

func TestValidateGlossary_Rejects(t *testing.T) {
	long := strings.Repeat("x", glossaryTermMaxBytes+1)
	cases := []struct{ name, content, want string }{
		{"duplicate across groups", "terms:\n  - canonical: A\n    aliases: [shared]\n  - canonical: B\n    aliases: [Shared]\n", `"Shared" already belongs to the term "A"`},
		{"same canonical twice", "terms:\n  - canonical: A\n  - canonical: a\n", `terms[1]`},
		{"alias also forbidden", "terms:\n  - canonical: A\n    aliases: [old]\n    forbidden: [OLD]\n", "both an alias and forbidden"},
		{"forbidden elsewhere an alias", "terms:\n  - canonical: A\n    aliases: [x]\n  - canonical: B\n    forbidden: [x]\n", "already belongs"},
		{"empty term", "terms:\n  - canonical: A\n    aliases: [\"  \"]\n", "must not be empty"},
		{"empty canonical", "terms:\n  - canonical: \"\"\n", "canonical must not be empty"},
		{"missing canonical", "terms:\n  - aliases: [x]\n", "canonical is required"},
		{"too long", "terms:\n  - canonical: " + long + "\n", "longer than"},
		{"unknown key", "terms:\n  - canonical: A\n    alias: [x]\n", `unknown field "alias"`},
		{"unknown top-level key", "words:\n  - canonical: A\n", "unknown top-level field"},
		{"not a list", "terms: {canonical: A}\n", "must be a list"},
		{"aliases not a list", "terms:\n  - canonical: A\n    aliases: x\n", "must be a list of strings"},
		{"not yaml", "terms: [\n", "not valid YAML"},
		{"not a mapping", "- a\n", "top level must be a mapping"},
	}
	for _, c := range cases {
		err := ValidateGlossary([]byte(c.content))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want an error naming %q, got %v", c.name, c.want, err)
		}
	}
}

// The tolerant read keeps every good term and names the bad ones.
func TestReadGlossary_Tolerant(t *testing.T) {
	k := &KB{Root: t.TempDir()}
	st, err := k.ReadGlossary()
	if err != nil || st.Present {
		t.Fatalf("no file: %+v %v", st, err)
	}
	content := testGlossary + "  - canonical: Other\n    aliases: [HA]\n"
	if err := os.WriteFile(filepath.Join(k.Root, GlossaryFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err = k.ReadGlossary()
	if err != nil || !st.Present || st.Unparseable {
		t.Fatalf("read: %+v %v", st, err)
	}
	if len(st.Glossary.Terms) != 2 || len(st.Malformed) != 1 || st.Malformed[0].Entry != "terms[2]" {
		t.Fatalf("tolerant read = %+v", st)
	}
	if err := os.WriteFile(filepath.Join(k.Root, GlossaryFile), []byte("- a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st, err = k.ReadGlossary(); err != nil || !st.Unparseable || len(st.Glossary.Terms) != 0 {
		t.Fatalf("unparseable: %+v %v", st, err)
	}
}

func TestReadGlossary_SymlinkRejected(t *testing.T) {
	k := &KB{Root: t.TempDir()}
	target := filepath.Join(t.TempDir(), "g.yaml")
	if err := os.WriteFile(target, []byte(testGlossary), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(k.Root, GlossaryFile)); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if _, err := k.ReadGlossary(); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("want a symlink error, got %v", err)
	}
}

func parsedTestGlossary(t *testing.T) Glossary {
	t.Helper()
	g, bad, err := ParseGlossary([]byte(testGlossary))
	if err != nil || len(bad) != 0 {
		t.Fatalf("parse: %v %v", bad, err)
	}
	return g
}

func TestGlossaryVariants(t *testing.T) {
	g := parsedTestGlossary(t)
	cases := []struct {
		name, query string
		max         int
		want        []string
	}{
		{"single word alias", "ha restart", 8, []string{"ha restart", "home assistant restart", "hass restart"}},
		{"multi-word canonical", "home assistant backup", 8, []string{"home assistant backup", "ha backup", "hass backup"}},
		{"two groups", "k8s ha", 8, []string{"k8s ha", "k8s home assistant", "k8s hass", "kubernetes ha"}},
		{"no match", "grafana dashboard", 8, []string{"grafana dashboard"}},
		{"not a whole word", "sha256 hashing", 8, []string{"sha256 hashing"}},
		{"forbidden does not expand", "homeassistant", 8, []string{"homeassistant"}},
		{"cap", "ha restart", 2, []string{"ha restart", "home assistant restart"}},
		{"cap of one is the query", "ha", 1, []string{"ha"}},
	}
	for _, c := range cases {
		if got := g.Variants(c.query, c.max); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Variants(%q, %d) = %q, want %q", c.name, c.query, c.max, got, c.want)
		}
	}
}

func TestGlossaryForbiddenUses(t *testing.T) {
	g := parsedTestGlossary(t)
	got := g.ForbiddenUses("Restart HomeAssistant, then homeassistant again; MyHomeAssistantX is not it.")
	want := []ForbiddenUse{{Term: "HomeAssistant", Canonical: "Home Assistant"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ForbiddenUses = %+v, want %+v", got, want)
	}
	if got := g.ForbiddenUses("Home Assistant only"); len(got) != 0 {
		t.Fatalf("canonical flagged: %+v", got)
	}
}
