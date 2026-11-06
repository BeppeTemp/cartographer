package settingschema

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var reorderSchema = Schema{Keys: []Key{
	{Path: "name", Scope: ScopeServer, Type: "string", Default: `""`, Description: "The name."},
	{Path: "net.addr", Scope: ScopeServer, Type: "string", Default: `""`, Description: "The address."},
	{Path: "net.port", Scope: ScopeServer, Type: "int", Default: "1", Description: "The port."},
	{Path: "entries", Scope: ScopeServer, Type: "map", Default: "{}", Description: "Entries."},
	{Path: "last", Scope: ScopeServer, Type: "bool", Default: "true", Description: "Last."},
}}

func rootKeys(t *testing.T, doc string) []string {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &n); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for i := 0; i < len(n.Content[0].Content); i += 2 {
		keys = append(keys, n.Content[0].Content[i].Value)
	}
	return keys
}

// Reorder writes the table's order at the root, in a described group and in the
// entries of a map the table leaves to the file; a comment and an unknown key
// travel with the key before them, an unknown leading key stays first, and the
// order of the entries themselves is the file's.
func TestRealignReordersInTableOrder(t *testing.T) {
	in := "last: false\nstray: 1\n# note on entries\nentries:\n  zeta:\n    b: 2\n    a: 1\n  alpha:\n    a: 1\nnet:\n  port: 8\n  # the address\n  addr: x\nname: n\n"
	reg := Registry{Reorder: true, Order: map[string][]string{"entries": {"a", "b"}}}
	out, rep, err := reorderSchema.Realign([]byte(in), reg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(rep.Lines(), "\n"), "order") {
		t.Errorf("report does not say the keys were reordered: %v", rep.Lines())
	}
	got := string(out)
	if keys := rootKeys(t, got); strings.Join(keys, ",") != "name,net,entries,last,stray" {
		t.Errorf("root order = %v\n%s", keys, got)
	}
	if strings.Index(got, "addr: x") > strings.Index(got, "port: 8") {
		t.Errorf("net keys out of order\n%s", got)
	}
	for _, want := range []string{"last: false\nstray: 1", "# note on entries\nentries:", "  zeta:\n    a: 1\n    b: 2\n  alpha:", "# the address\n  addr: x", "port: 8"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	again, rep, err := reorderSchema.Realign(out, reg)
	if err != nil || !rep.Empty() || string(again) != got {
		t.Errorf("not idempotent: %v %v\n%s", rep.Lines(), err, again)
	}
}

// Without Reorder the file's order is its owner's; ReorderOnMove writes the
// table's order only when a key moved.
func TestRealignKeepsTheOrderUnlessAsked(t *testing.T) {
	in := "last: true\nname: n\n"
	out, _, err := reorderSchema.Realign([]byte(in), Registry{})
	if err != nil || strings.Join(rootKeys(t, string(out)), ",") != "last,name" {
		t.Fatalf("order changed without being asked: %s %v", out, err)
	}
	reg := Registry{ReorderOnMove: true, Moved: []Moved{{From: "addr", To: "net.addr"}}}
	if out, _, err = reorderSchema.Realign([]byte(in), reg); err != nil || strings.Join(rootKeys(t, string(out)), ",") != "last,name" {
		t.Fatalf("order changed with no key moved: %s %v", out, err)
	}
	out, _, err = reorderSchema.Realign([]byte("last: true\naddr: x\nname: n\n"), reg)
	if err != nil || strings.Join(rootKeys(t, string(out)), ",") != "name,net,last" {
		t.Fatalf("order after a move = %s %v", out, err)
	}
}

// A mapping with an anchor, an alias or a merge key keeps its order: the table's
// order would put the alias before its anchor and write a file that does not parse.
func TestRealignKeepsTheOrderOfAMappingWithAnchors(t *testing.T) {
	reg := Registry{Reorder: true}
	for name, in := range map[string]string{
		"alias":    "last: &x true\nname: *x\n",
		"anchored": "last: 1\nname: &n\n  k: 1\nnet: *n\n",
		"merge":    "base: &b\n  k: 1\nlast: 1\nname:\n  <<: *b\n",
	} {
		out, rep, err := reorderSchema.Realign([]byte(in), reg)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var check yaml.Node
		if err := yaml.Unmarshal(out, &check); err != nil {
			t.Errorf("%s: the output does not parse: %v\n%s", name, err, out)
		}
		for _, c := range rep.Changes {
			if c.Kind == ChangeReordered {
				t.Errorf("%s: reported a reorder\n%s", name, out)
			}
		}
		if i, j := strings.Index(string(out), "last"), strings.Index(string(out), "name"); i > j {
			t.Errorf("%s: keys moved\n%s", name, out)
		}
	}
}

// The comment above the first key and the one below the last stay at the top and
// at the end of the file when the keys move.
func TestRealignKeepsTheHeaderAndFootComments(t *testing.T) {
	in := "# header\nlast: false\nentries:\n  e:\n    a: 1\nnet:\n  addr: x\n  port: 8\nname: n\n# foot\n"
	reg := Registry{Reorder: true, Order: map[string][]string{"entries": {"a"}}}
	out, _, err := reorderSchema.Realign([]byte(in), reg)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if keys := rootKeys(t, got); strings.Join(keys, ",") != "name,net,entries,last" {
		t.Fatalf("root order = %v\n%s", keys, got)
	}
	if !strings.HasPrefix(got, "# header\n") {
		t.Errorf("the header comment left the top:\n%s", got)
	}
	if !strings.HasSuffix(got, "last: false\n# foot\n") || strings.Index(got, "# foot") < strings.Index(got, "last: false") {
		t.Errorf("the foot comment left the end:\n%s", got)
	}
	if strings.Count(got, "# header") != 1 || strings.Count(got, "# foot") != 1 {
		t.Errorf("a comment is duplicated or lost:\n%s", got)
	}
}

// The file's header and foot join the own comments of the key that ends up
// first and last; they do not replace them.
func TestRealignKeepsTheOwnCommentsOfTheNewFirstAndLastKey(t *testing.T) {
	reg := Registry{Reorder: true}
	// No file header: the key moving to the top keeps its own comment.
	out, _, err := reorderSchema.Realign([]byte("last: false\n# about name\nname: n\n"), reg)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); !strings.HasPrefix(got, "# about name\nname: n\n") {
		t.Errorf("the new first key lost its own comment:\n%s", got)
	}
	// A header and an own comment: both, the header on top.
	out, _, err = reorderSchema.Realign([]byte("# header\nlast: false\n# about name\nname: n\n"), reg)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); !strings.HasPrefix(got, "# header\n# about name\nname: n\n") || strings.Count(got, "# header") != 1 {
		t.Errorf("header and own comment:\n%s", got)
	}
}

// The own foot comment of the key that ends up last stays, above the file's.
func TestRealignKeepsTheOwnFootOfTheNewLastKey(t *testing.T) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte("last: false\nname: n\n"), &doc); err != nil {
		t.Fatal(err)
	}
	root := doc.Content[0]
	root.Content[0].FootComment = "own foot of last"
	root.Content[2].FootComment = "file foot"
	if !reorderSchema.Reorder(&doc, Registry{}) {
		t.Fatal("no move")
	}
	if root.Content[0].Value != "name" || root.Content[2].Value != "last" {
		t.Fatalf("order %s %s", root.Content[0].Value, root.Content[2].Value)
	}
	if got := root.Content[2].FootComment; got != "own foot of last\nfile foot" {
		t.Errorf("foot of the new last key = %q", got)
	}
	if got := root.Content[0].FootComment; got != "" {
		t.Errorf("foot of the new first key = %q", got)
	}
}

// An anchor and its alias inside one pair do not stop the root from being
// reordered; an alias to the anchor of another pair does (above).
func TestRealignReordersAroundAnchorsInsideOnePair(t *testing.T) {
	in := "last: 1\nname: n\nnet:\n  addr: &a x\n  port: *a\n"
	out, rep, err := reorderSchema.Realign([]byte(in), Registry{Reorder: true})
	if err != nil {
		t.Fatal(err)
	}
	var check yaml.Node
	if err := yaml.Unmarshal(out, &check); err != nil {
		t.Fatalf("does not parse: %v\n%s", err, out)
	}
	if keys := rootKeys(t, string(out)); strings.Join(keys, ",") != "name,net,last" {
		t.Errorf("root order = %v\n%s", keys, out)
	}
	moved := false
	for _, c := range rep.Changes {
		moved = moved || c.Kind == ChangeReordered
	}
	if !moved {
		t.Error("no reorder reported")
	}
}
