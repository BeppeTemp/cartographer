package lint

import (
	"strings"
	"testing"
)

// D314: stringified_list and mangled_placeholder.

func shapeFindings(t *testing.T, fm, body string) []Finding {
	t.Helper()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/a.md", "---\ntype: Note\ntitle: A\n"+fm+"---\n"+body+"\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	return findings
}

func TestStringifiedList_Detected(t *testing.T) {
	for _, v := range []string{`"[a, b]"`, `"[a]; [b]"`, `"- a"`} {
		got := findingsOf(shapeFindings(t, "provenance: "+v+"\n", "x"), "stringified_list")
		if len(got) != 1 || got[0].Severity != SevWarning || got[0].Fix == nil || got[0].Fix.Field != "provenance" {
			t.Errorf("provenance: %s -> %+v", v, got)
		}
	}
}

func TestStringifiedList_GoodListNotFlagged(t *testing.T) {
	for _, fm := range []string{"provenance: [a, b]\n", "provenance:\n  - a\n  - b\n"} {
		if got := findingsOf(shapeFindings(t, fm, "x"), "stringified_list"); len(got) != 0 {
			t.Errorf("%q flagged: %+v", fm, got)
		}
	}
}

func TestStringifiedList_NonListFieldIgnored(t *testing.T) {
	if got := findingsOf(shapeFindings(t, "title2: \"[notes]\"\n", "x"), "stringified_list"); len(got) != 0 {
		t.Errorf("flagged: %+v", got)
	}
}

func TestStringifiedList_NotSuppressible(t *testing.T) {
	got := shapeFindings(t, "provenance: \"[a, b]\"\nlint_ignore: [stringified_list]\n", "x")
	if len(findingsOf(got, "stringified_list")) != 1 {
		t.Errorf("suppressed: %+v", got)
	}
}

func TestListItems(t *testing.T) {
	for _, c := range []struct{ field, in, want string }{
		{"tags", "[a, b]", "a|b"},
		{"tags", "[a]; [b]", "a|b"},
		{"tags", "- a", "a"},
		{"tags", `["x y", 'z']`, "x y|z"},
		// D357: a bare string. Identifier fields split on commas…
		{"tags", "x, y", "x|y"},
		{"related", "a/b, c/d", "a/b|c/d"},
		{"tags", "backup", "backup"},
		// …citation fields keep it whole, a written list still splits.
		{"provenance", "Talk, 2024", "Talk, 2024"},
		{"secrets_source", "vault, prod", "vault, prod"},
		{"provenance", "[Talk, 2024]", "Talk|2024"},
	} {
		if got := strings.Join(ListItems(c.field, c.in), "|"); got != c.want {
			t.Errorf("ListItems(%s, %q) = %q, want %q", c.field, c.in, got, c.want)
		}
	}
}

// D357: a string in a list field is always the wrong type, bracketed or not.
func TestStringifiedList_BareScalarDetected(t *testing.T) {
	for _, fm := range []string{"tags: \"x, y\"\n", "provenance: \"a, b, c\"\n", "tags: backup\n", "related: other/page\n"} {
		got := findingsOf(shapeFindings(t, fm, "x"), "stringified_list")
		if len(got) != 1 || got[0].Fix == nil || got[0].Fix.Kind != FixListifyField {
			t.Errorf("%q -> %+v", fm, got)
		}
	}
}

func TestMangledPlaceholder_Detected(t *testing.T) {
	for _, body := range []string{
		"cd `repo:my-tools` fra doppie graffe",
		"open `path:kubeconfig` between double braces",
	} {
		got := findingsOf(shapeFindings(t, "", body), "mangled_placeholder")
		if len(got) != 1 || got[0].Severity != SevWarning {
			t.Errorf("%q -> %+v", body, got)
		}
	}
}

func TestMangledPlaceholder_NotFlagged(t *testing.T) {
	for name, body := range map[string]string{
		"real placeholder": "{{repo:my-tools}}",
		"documentation":    "use {{repo:key}} (that is, `repo:key` between double braces)",
		"code fence":       "```\n`repo:my-tools` between double braces\n```",
	} {
		if got := findingsOf(shapeFindings(t, "", body), "mangled_placeholder"); len(got) != 0 {
			t.Errorf("%s flagged: %+v", name, got)
		}
	}
}

func TestMangledPlaceholder_Suppressible(t *testing.T) {
	got := shapeFindings(t, "lint_ignore: [mangled_placeholder]\n", "cd `repo:my-tools` fra doppie graffe")
	if len(findingsOf(got, "mangled_placeholder")) != 0 {
		t.Errorf("not suppressed: %+v", got)
	}
}

func TestRun_MissingRegistry_WithPlaceholders(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/a.md", "---\ntype: Note\ntitle: A\n---\n{{path:x}}\n")
	writeFile(t, k.DataRoot(), "arch/b.md", "---\ntype: Note\ntitle: B\n---\n{{path:y}} see [[arch/a]]\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	got := findingsOf(findings, "missing_registry")
	if len(got) != 1 || got[0].Severity != SevInfo || got[0].Path != "paths.yaml" {
		t.Fatalf("missing_registry = %+v", got)
	}
}

func TestRun_MissingRegistry_NoPlaceholders(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "arch/a.md", "---\ntype: Note\ntitle: A\n---\nplain\n")
	findings, _ := Run(k, "", false)
	if got := findingsOf(findings, "missing_registry"); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestRun_MissingRegistry_MalformedFile(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "paths.yaml", "- not\n- a mapping\n")
	writeFile(t, k.DataRoot(), "arch/a.md", "---\ntype: Note\ntitle: A\n---\n{{path:x}}\n")
	findings, _ := Run(k, "", false)
	if got := findingsOf(findings, "missing_registry"); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	if got := findingsOf(findings, "contract_malformed"); len(got) == 0 {
		t.Fatal("want contract_malformed")
	}
}
