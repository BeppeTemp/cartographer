package settingschema

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var testSchema = Schema{Keys: []Key{
	{Path: "name", Scope: ScopeServer, Type: "string", Default: `""`, Description: "The name.", Example: "demo"},
	{Path: "mode", Scope: ScopeServer, Type: "string", Values: "on | off", Default: "off", Env: "X_MODE", Restart: true, Description: "The mode."},
	{Path: "git.sync", Scope: ScopeServer, Type: "bool", Default: "true", Description: "Sync."},
	{Path: "git.profile", Scope: ScopeServer, Type: "string", Default: "local", Description: "Profile."},
	{Path: "items", Scope: ScopeServer, Type: "list", Default: "[]", Description: "The items."},
	{Path: "items[].id", Scope: ScopeServer, Type: "string", Default: `""`, Description: "Item id."},
	{Path: "items[].tag", Scope: ScopeServer, Type: "string", Default: `""`, Description: "Item tag."},
}}

func mustFormat(t *testing.T, in string) string {
	t.Helper()
	out, err := testSchema.Format([]byte(in))
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	return string(out)
}

func TestSetKeysGetHeadCommentsAndUnsetKeysAreListed(t *testing.T) {
	out := mustFormat(t, "name: a\ngit:\n  sync: false\n")
	for _, want := range []string{
		"# The name.\n# Default: \"\".\nname: a",
		"  # Sync.\n  # Default: true.\n  sync: false",
		Marker + "\n  # Profile.\n  # Default: local.\n  # profile: local",
		"# mode: off",
		"# items: []",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "# name:") || strings.Contains(out, "# sync:") {
		t.Errorf("a set key is also listed as unset:\n%s", out)
	}
}

func TestRenderIsIdempotent(t *testing.T) {
	for _, in := range []string{
		"",
		"name: a\n",
		"# top\nname: a\nitems:\n  - id: x\n  - id: y\n    tag: t\n",
		"git:\n  sync: true\n  profile: local\n",
	} {
		once := mustFormat(t, in)
		if twice := mustFormat(t, once); twice != once {
			t.Errorf("not idempotent for %q:\n%s\n--- second pass\n%s", in, once, twice)
		}
	}
}

func TestUserCommentsAndUnknownKeysSurvive(t *testing.T) {
	in := "# my header\nname: a # why\n# keep this\nextra: 1\nmode: on\n# tail note\n"
	out := mustFormat(t, in)
	for _, want := range []string{"# my header", "name: a # why", "# keep this\nextra: 1", "# tail note"} {
		if !strings.Contains(out, want) {
			t.Errorf("lost %q:\n%s", want, out)
		}
	}
	// A key the user already commented is not documented over.
	if strings.Contains(out, "# The name.") {
		t.Errorf("documented a key that already carries a user comment:\n%s", out)
	}
	if !strings.Contains(out, "# The mode.") {
		t.Errorf("a key without a comment was not documented:\n%s", out)
	}
}

// A key the user had commented out, and that is now set, leaves the block.
func TestSettingACommentedOutKeyRemovesItFromTheBlock(t *testing.T) {
	base := mustFormat(t, "name: a\n")
	if !strings.Contains(base, "# mode: off") {
		t.Fatalf("setup: %s", base)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(base), &doc); err != nil {
		t.Fatal(err)
	}
	m := doc.Content[0]
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "mode"},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "on"})
	testSchema.Render(&doc)
	b, err := Encode(&doc)
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	if strings.Contains(out, "# mode:") {
		t.Errorf("mode is set and still listed as unset:\n%s", out)
	}
	if !strings.Contains(out, "mode: \"on\"") && !strings.Contains(out, "mode: on") {
		t.Errorf("mode lost:\n%s", out)
	}
	if strings.Count(out, Marker) != 1 {
		t.Errorf("want exactly one block:\n%s", out)
	}
	if !strings.Contains(out, "# items: []") {
		t.Errorf("an unset key left the block:\n%s", out)
	}
}

// A line of the block the user edited stays as written.
func TestEditedBlockLineIsKept(t *testing.T) {
	base := mustFormat(t, "name: a\n")
	edited := strings.Replace(base, "# mode: off", "# mode: on # my note", 1)
	if out := mustFormat(t, edited); !strings.Contains(out, "# mode: on # my note") {
		t.Errorf("lost the edited line:\n%s", out)
	}
}

func TestListEntriesGetKeyCommentsButNoBlock(t *testing.T) {
	out := mustFormat(t, "items:\n  - id: x\n")
	if !strings.Contains(out, "# Item id.") {
		t.Errorf("list entry key not documented:\n%s", out)
	}
	if strings.Contains(out, "# tag:") {
		t.Errorf("a list entry got its own block:\n%s", out)
	}
	if strings.Count(out, Marker) != 1 {
		t.Errorf("want one block (the root):\n%s", out)
	}
}

func TestExampleHoldsExampleValues(t *testing.T) {
	b, err := testSchema.Example("Header line.")
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	if !strings.HasPrefix(out, "# Header line.\n\n") || !strings.Contains(out, "name: demo") {
		t.Errorf("example:\n%s", out)
	}
}
