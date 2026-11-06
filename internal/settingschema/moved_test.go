package settingschema

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var movedSchema = Schema{Keys: []Key{
	{Path: "name", Scope: ScopeServer, Type: "string", Default: `""`, Description: "The name."},
	{Path: "net.addr", Scope: ScopeServer, Type: "string", Default: `""`, Description: "The address."},
}}

var movedRegistry = Registry{Moved: []Moved{
	{From: "addr", To: "net.addr", Description: "The old address."},
	{From: "tags", To: "net.meta.tags"},
	{From: "old.mode", To: "net.opts.mode"},
}}

func movedRealign(t *testing.T, in string) (string, Report, error) {
	t.Helper()
	out, rep, err := movedSchema.Realign([]byte(in), movedRegistry)
	return string(out), rep, err
}

func decodeMap(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := yaml.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("output is not YAML: %v\n%s", err, s)
	}
	return m
}

// A moved key keeps its value (scalar, list, nested) and a person's comment,
// lands in mappings created on the way, and leaves no empty mapping behind.
func TestMovedCarriesValuesAndComments(t *testing.T) {
	in := "name: x\n# keep me\naddr: example.com:1\ntags: [a, b]\nold:\n  mode: fast\n"
	out, rep, err := movedRealign(t, in)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeMap(t, out)
	net, _ := m["net"].(map[string]any)
	if net["addr"] != "example.com:1" {
		t.Errorf("net.addr = %v\n%s", net["addr"], out)
	}
	if tags := net["meta"].(map[string]any)["tags"].([]any); len(tags) != 2 || tags[1] != "b" {
		t.Errorf("net.meta.tags = %v", tags)
	}
	if net["opts"].(map[string]any)["mode"] != "fast" {
		t.Errorf("net.opts.mode lost\n%s", out)
	}
	for _, gone := range []string{"addr", "tags", "old"} {
		if _, ok := m[gone]; ok {
			t.Errorf("%s still at the root\n%s", gone, out)
		}
	}
	if !strings.Contains(out, "# keep me") {
		t.Errorf("a person's comment was lost\n%s", out)
	}
	if len(rep.Changes) < 3 || rep.Changes[0].Kind != ChangeMoved || !rep.Substantive() {
		t.Errorf("report = %v", rep.Lines())
	}
}

// Realigning the output again changes nothing.
func TestMovedIsIdempotent(t *testing.T) {
	out, _, err := movedRealign(t, "addr: a:1\ntags: [x]\n")
	if err != nil {
		t.Fatal(err)
	}
	again, rep, err := movedRealign(t, out)
	if err != nil || again != out || !rep.Empty() {
		t.Errorf("second pass changed the file (err %v, report %v):\n%s\n---\n%s", err, rep.Lines(), out, again)
	}
}

// Both paths set: equal values drop the old one, different ones are refused and
// nothing is returned to write.
func TestMovedBothPaths(t *testing.T) {
	out, rep, err := movedRealign(t, "addr: a:1\nnet:\n  addr: a:1\n")
	if err != nil {
		t.Fatal(err)
	}
	if m := decodeMap(t, out); m["addr"] != nil || m["net"].(map[string]any)["addr"] != "a:1" {
		t.Errorf("equal values: old path not dropped\n%s", out)
	}
	if rep.Empty() {
		t.Error("dropping the old path is a change")
	}

	in := "addr: a:1\nnet:\n  addr: b:2\n"
	got, _, err := movedRealign(t, in)
	if err == nil || !strings.Contains(err.Error(), "addr") || !strings.Contains(err.Error(), "net.addr") {
		t.Fatalf("different values: err = %v, want a refusal naming both", err)
	}
	if got != in {
		t.Errorf("a refusal must return the input unchanged:\n%s", got)
	}
	if c := movedRegistry.MovedConflicts([]byte(in)); len(c) != 1 {
		t.Errorf("MovedConflicts = %v", c)
	}
	if c := movedRegistry.MovedConflicts([]byte("addr: a:1\nnet:\n  addr: a:1\n")); len(c) != 0 {
		t.Errorf("equal values are no conflict: %v", c)
	}
}

// An empty old value is dropped, not planted at the new path; a mapping that
// cannot hold the destination is an error.
func TestMovedEmptyAndBlockedDestination(t *testing.T) {
	out, _, err := movedRealign(t, "name: x\naddr: \"\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if m := decodeMap(t, out); m["addr"] != nil || m["net"] != nil {
		t.Errorf("an empty value must not move\n%s", out)
	}
	if _, _, err := movedRealign(t, "addr: a:1\nnet: text\n"); err == nil {
		t.Error("a destination under a scalar must be an error")
	}
}

// A value at the old path next to an empty entry at the new one is a conflict:
// an empty entry means "none", and dropping the old value would delete it.
func TestMovedEmptyDestinationIsAConflict(t *testing.T) {
	for _, in := range []string{"addr: a:1\nnet:\n  addr: \"\"\n", "addr: a:1\nnet:\n  addr:\n"} {
		got, _, err := movedRealign(t, in)
		if err == nil || got != in {
			t.Errorf("%q: err = %v, output changed = %v, want a refusal", in, err, got != in)
		}
		if c := movedRegistry.MovedConflicts([]byte(in)); len(c) != 1 {
			t.Errorf("%q: MovedConflicts = %v", in, c)
		}
	}
}

// A destination mapping that is an alias of an anchor is shared with other
// places: the old key stays, the anchor is not edited.
func TestMovedLeavesAnAliasedDestinationAlone(t *testing.T) {
	in := "base: &b {x: 1}\nnet: *b\naddr: a:1\n"
	out, _, err := movedRealign(t, in)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeMap(t, out)
	if m["addr"] != "a:1" {
		t.Errorf("the old key must stay\n%s", out)
	}
	if base := m["base"].(map[string]any); len(base) != 1 || base["addr"] != nil {
		t.Errorf("the shared anchor was edited: %v", base)
	}
}

// A bare key on the way (net:) is an empty mapping: the value lands in it.
func TestMovedFillsABareDestinationKey(t *testing.T) {
	out, _, err := movedRealign(t, "net:\naddr: a:1\ntags: [x]\n")
	if err != nil {
		t.Fatal(err)
	}
	m := decodeMap(t, out)
	net, _ := m["net"].(map[string]any)
	if net["addr"] != "a:1" || strings.Count(out, "net:") != 1 {
		t.Errorf("output:\n%s", out)
	}
}

// A "*" segment moves every key the document holds there, to the same key in the
// destination; an Except key is left to another move, and a key with a dot or a
// "*" in it moves like any other, addressed literally.
func TestMovedWildcard(t *testing.T) {
	reg := Registry{Moved: []Moved{
		{From: "mode.fallback", To: "defaults.mode"},
		{From: "mode.*", To: "items.*.mode", Except: []string{"fallback"}},
		{From: "tags.*", To: "items.*.tags"},
	}}
	in := "mode:\n  fallback: slow\n  a: fast\n  b: slow\n  \"c.d\": x\n  \"*\": y\ntags:\n  a: [t1]\n"
	out, rep, err := movedSchema.Realign([]byte(in), reg)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeMap(t, string(out))
	items := m["items"].(map[string]any)
	if items["a"].(map[string]any)["mode"] != "fast" || items["b"].(map[string]any)["mode"] != "slow" {
		t.Errorf("items = %v\n%s", items, out)
	}
	if tags := items["a"].(map[string]any)["tags"].([]any); len(tags) != 1 || tags[0] != "t1" {
		t.Errorf("tags = %v", tags)
	}
	if m["defaults"].(map[string]any)["mode"] != "slow" {
		t.Errorf("fallback not moved:\n%s", out)
	}
	if items["c.d"].(map[string]any)["mode"] != "x" || items["*"].(map[string]any)["mode"] != "y" {
		t.Errorf("a dotted or star key did not move: %v\n%s", items, out)
	}
	if _, ok := m["mode"]; ok {
		t.Errorf("mode is still at the root\n%s", out)
	}
	if again, _, err := movedSchema.Realign(out, reg); err != nil || string(again) != string(out) {
		t.Errorf("a second realignment changed the file (err %v):\n%s", err, again)
	}
	if _, ok := m["tags"]; ok {
		t.Errorf("tags is still at the root\n%s", out)
	}
	if len(rep.Changes) != 6 {
		t.Errorf("changes = %v", rep.Lines())
	}
	// Set at both places with different values: refused, as for a plain move.
	if c := reg.MovedConflicts([]byte("mode:\n  a: fast\nitems:\n  a:\n    mode: slow\n")); len(c) != 1 || !strings.Contains(c[0], "mode.a and items.a.mode") {
		t.Errorf("conflicts = %v", c)
	}
}
