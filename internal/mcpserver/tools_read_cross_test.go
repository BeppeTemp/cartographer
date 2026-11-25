package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// edgesRig is kb-a with page p (links kb-b/ops/x, kb-b/ops/gone) and q, and
// kb-b with ops/x and two pages linking kb-a/p: ops/back and priv/hid.
func edgesRig(t *testing.T) (*Server, crossRig) {
	t.Helper()
	r := newCrossRig(t)
	putThroughServer(t, r.a, "p", "[[kb:kb-b/ops/x]] [[kb:kb-b/ops/gone]] [[kb:nope/z]] [[kb:kb-a/q]] [[kb:bad name/z]]\n")
	putThroughServer(t, r.a, "q", "q\n")
	putThroughServer(t, r.b, "ops/back", "[[kb:kb-a/p]]\n")
	putThroughServer(t, r.b, "priv/hid", "[[kb:kb-a/p]]\n")
	s := New("test")
	RegisterKBTools(s, r.a, Deps{})
	return s, r
}

func neighbors(t *testing.T, s *Server, ctx context.Context, args string) map[string]any {
	t.Helper()
	text, isErr := callJSON(t, s, ctx, "graph_neighbors", args)
	if isErr {
		t.Fatalf("graph_neighbors %s: %s", args, text)
	}
	return decodeJSON(t, text)
}

func crossRows(out map[string]any) []map[string]any {
	var rows []map[string]any
	list, _ := out["cross"].([]any)
	for _, r := range list {
		rows = append(rows, r.(map[string]any))
	}
	return rows
}

// AC-cross-kb-links-17
func TestGraphNeighborsCrossIn(t *testing.T) {
	t.Parallel()
	s, r := edgesRig(t)
	callers := crossCallers()
	in := `{"id":"p","direction":"in"}`

	got := crossRows(neighbors(t, s, callers["reads-a-b"], in))
	if len(got) != 2 || got[0]["id"] != "ops/back" || got[1]["id"] != "priv/hid" || got[0]["direction"] != "in" || got[0]["kb"] != "kb-b" {
		t.Errorf("reader of kb-b: %v", got)
	}
	// A reader of one map of kb-b sees the rows of that map only.
	if got := crossRows(neighbors(t, s, callers["b-scoped"], in)); len(got) != 1 || got[0]["id"] != "ops/back" {
		t.Errorf("b-scoped: %v", got)
	}

	// A caller who reads no linking page gets the answer of a link that does not
	// exist: every field, cross_count included.
	hidden := neighbors(t, s, callers["a-only"], in)
	putThroughServer(t, r.b, "ops/back", "no link\n")
	putThroughServer(t, r.b, "priv/hid", "no link\n")
	absent := neighbors(t, s, callers["a-only"], in)
	if !jsonEqual(t, hidden, absent) {
		t.Errorf("a caller who cannot read kb-b sees a difference:\n%v\n%v", hidden, absent)
	}
	if _, ok := hidden["cross"]; ok {
		t.Error("cross present for a caller who reads no linking page")
	}
	if _, ok := hidden["cross_count"]; ok {
		t.Error("cross_count present for a caller who reads no linking page")
	}
	// A cross-only in-edge does not make a missing id exist.
	if _, isErr := callJSON(t, s, callers["admin"], "graph_neighbors", `{"id":"nope-id","direction":"in"}`); !isErr {
		t.Error("a missing id answered")
	}
}

func jsonEqual(t *testing.T, a, b any) bool {
	t.Helper()
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// AC-cross-kb-links-18
func TestGraphNeighborsCrossOut(t *testing.T) {
	t.Parallel()
	s, _ := edgesRig(t)
	callers := crossCallers()
	out := `{"id":"p"}`
	state := func(rows []map[string]any, kbn, id string) any {
		for _, r := range rows {
			if r["kb"] == kbn && r["id"] == id {
				return r["state"]
			}
		}
		return "<no row>"
	}

	rows := crossRows(neighbors(t, s, callers["reads-a-b"], out))
	for id, want := range map[string]string{"ops/x": "resolved", "ops/gone": "missing"} {
		if got := state(rows, "kb-b", id); got != want {
			t.Errorf("kb-b/%s = %v, want %s", id, got, want)
		}
	}
	if got := state(rows, "nope", "z"); got != "unresolved" {
		t.Errorf("absent kb = %v", got)
	}
	// Malformed and self-named links name nothing.
	for _, r := range rows {
		if r["kb"] == "kb-a" || r["kb"] == "bad name" {
			t.Errorf("row for a malformed or self-named link: %v", r)
		}
		for k := range r {
			if k != "direction" && k != "kb" && k != "id" && k != "state" && k != "bind" {
				t.Errorf("row carries %q: %v", k, r)
			}
		}
	}
	// A caller who cannot read kb-b: the same neutral value for a present page,
	// a missing one and an absent KB, and no count.
	rows = crossRows(neighbors(t, s, callers["a-only"], out))
	for _, id := range []string{"ops/x", "ops/gone"} {
		if got := state(rows, "kb-b", id); got != "unresolved" {
			t.Errorf("a-only kb-b/%s = %v", id, got)
		}
	}
	// out_of_scope: only for a reader of the target, with the bind text by connection kind.
	routed := withRoutedSet(callers["reads-a-b"], []string{"kb-a"})
	rows = crossRows(neighbors(t, s, routed, out))
	if got := state(rows, "kb-b", "ops/x"); got != "out_of_scope" {
		t.Fatalf("routed reader: %v", got)
	}
	bind := func(rows []map[string]any) string {
		for _, r := range rows {
			if r["kb"] == "kb-b" && r["id"] == "ops/x" {
				s, _ := r["bind"].(string)
				return s
			}
		}
		return ""
	}
	if generic := bind(rows); !strings.Contains(generic, "kb-b") || strings.Contains(generic, "cartographer ") {
		t.Errorf("generic bind = %q", generic)
	}
	if op := bind(crossRows(neighbors(t, s, withOperator(routed), out))); !strings.Contains(op, "cartographer client bind <provider> kb-b") {
		t.Errorf("operator bind = %q", op)
	}
	noGrant := crossRows(neighbors(t, s, withRoutedSet(callers["a-only"], []string{"kb-a"}), out))
	for _, r := range noGrant {
		if r["state"] == "out_of_scope" || r["bind"] != nil {
			t.Errorf("a caller with no grant got %v", r)
		}
	}
	// A KB with no cross link keeps its answer: no cross, no cross_count.
	plain := neighbors(t, s, callers["admin"], `{"id":"q"}`)
	if _, ok := plain["cross"]; ok {
		t.Error("cross present on a page with no cross edge")
	}
}
