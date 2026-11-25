package kb

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// relKB makes a KB whose origin is host/org/<repoName> and whose committed
// settings.yaml declares deps (name, repo pairs).
func relKB(t *testing.T, repoName string, deps ...[2]string) *KB {
	t.Helper()
	if !haveGit() {
		t.Skip("git not in PATH")
	}
	k := newTestKB(t)
	if repoName != "" {
		gitHere(t, k.Root, "remote", "add", "origin", "https://git.example.com/org/"+repoName+".git")
	}
	if len(deps) > 0 {
		var b strings.Builder
		b.WriteString("depends_on:\n")
		for _, d := range deps {
			fmt.Fprintf(&b, "  - {name: %s, remote: git.example.com/org/%s}\n", d[0], d[1])
		}
		commitSettingsHere(t, k, b.String())
	}
	if repoName != "" {
		// What the first sync leaves behind: the remote's default branch is known
		// and carries the committed settings.
		gitHere(t, k.Root, "update-ref", "refs/remotes/origin/main", "HEAD")
		gitHere(t, k.Root, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	}
	return k
}

type relFleet struct {
	byName map[string]*KB
	order  []string
}

func (f *relFleet) add(name string, k *KB) { f.byName[name] = k; f.order = append(f.order, name) }

func (f *relFleet) mount(aliases map[string]string) Mounts {
	m := NewMounts(f.byName, f.order, aliases)
	for _, k := range f.byName {
		k.SetMounts(m)
	}
	return m
}

func newFleet() *relFleet { return &relFleet{byName: map[string]*KB{}} }

func resolve(t *testing.T, m Mounts, k *KB, readable func(string) bool) RelationView {
	t.Helper()
	v, err := ResolveRelations(RelationEnv{Mounts: m, Readable: readable}, k)
	if err != nil {
		t.Fatalf("ResolveRelations: %v", err)
	}
	return v
}

func reasons(v RelationView) map[string]string {
	out := map[string]string{}
	for _, u := range v.Unresolved {
		out[u.Name] = u.Reason
	}
	return out
}

// AC-kbrel-6
func TestResolveClosureAndCycle(t *testing.T) {
	t.Parallel()
	f := newFleet()
	f.add("a", relKB(t, "a", [2]string{"b", "b"}))
	f.add("b", relKB(t, "b", [2]string{"c", "c"}))
	f.add("c", relKB(t, "c"))
	m := f.mount(nil)
	v := resolve(t, m, f.byName["a"], nil)
	if !reflect.DeepEqual(v.Resolved, []string{"b"}) || !reflect.DeepEqual(v.Closure, []string{"b", "c"}) || len(v.Unresolved) != 0 {
		t.Fatalf("chain: %+v", v)
	}
	if !reflect.DeepEqual(v.Visited(), []string{"b", "c"}) {
		t.Fatalf("visited = %v", v.Visited())
	}

	// a -> b -> a: each edge is a cycle in the view of its own KB.
	g := newFleet()
	g.add("a", relKB(t, "a", [2]string{"b", "b"}))
	g.add("b", relKB(t, "b", [2]string{"a", "a"}))
	gm := g.mount(nil)
	for _, n := range []string{"a", "b"} {
		v := resolve(t, gm, g.byName[n], nil)
		if len(v.Closure) != 0 || len(v.Resolved) != 0 || len(v.Unresolved) != 1 || v.Unresolved[0].Reason != RelationCycle {
			t.Fatalf("2-cycle seen from %s: %+v", n, v)
		}
	}

	// a -> b -> c -> b: the cycle does not pass through a, so no edge of a is marked.
	h := newFleet()
	h.add("a", relKB(t, "a", [2]string{"b", "b"}))
	h.add("b", relKB(t, "b", [2]string{"c", "c"}))
	h.add("c", relKB(t, "c", [2]string{"b", "b"}))
	hv := resolve(t, h.mount(nil), h.byName["a"], nil)
	if !reflect.DeepEqual(hv.Closure, []string{"b", "c"}) || len(hv.Unresolved) != 0 {
		t.Fatalf("cycle not through self: %+v", hv)
	}

	// Diamond: a -> b, c; both -> d. d once, in discovery order.
	d := newFleet()
	d.add("a", relKB(t, "a", [2]string{"b", "b"}, [2]string{"c", "c"}))
	d.add("b", relKB(t, "b", [2]string{"d", "d"}))
	d.add("c", relKB(t, "c", [2]string{"d", "d"}))
	d.add("d", relKB(t, "d"))
	dv := resolve(t, d.mount(nil), d.byName["a"], nil)
	if !reflect.DeepEqual(dv.Closure, []string{"b", "d", "c"}) || !reflect.DeepEqual(dv.Resolved, []string{"b", "c"}) {
		t.Fatalf("diamond: %+v", dv)
	}
}

// A cycle of three through the KB, and a cycle closed through an alias.
func TestResolveLongerAndAliasedCycles(t *testing.T) {
	t.Parallel()
	f := newFleet()
	f.add("a", relKB(t, "a", [2]string{"b", "b"}))
	f.add("b", relKB(t, "b", [2]string{"c", "c"}))
	f.add("c", relKB(t, "c", [2]string{"a", "a"}))
	m := f.mount(nil)
	for _, n := range []string{"a", "b", "c"} {
		v := resolve(t, m, f.byName[n], nil)
		if len(v.Resolved) != 0 || len(v.Closure) != 0 || len(v.Unresolved) != 1 || v.Unresolved[0].Reason != RelationCycle {
			t.Fatalf("3-cycle seen from %s: %+v", n, v)
		}
	}

	// a declares "old" (renamed to "new"); new declares a back by its current name.
	g := newFleet()
	g.add("a", relKB(t, "a", [2]string{"old", "core"}))
	g.add("new", relKB(t, "core", [2]string{"a", "a"}))
	v := resolve(t, g.mount(map[string]string{"old": "new"}), g.byName["a"], nil)
	if len(v.Resolved) != 0 || len(v.Closure) != 0 || len(v.Unresolved) != 1 ||
		v.Unresolved[0].Name != "old" || v.Unresolved[0].Reason != RelationCycle {
		t.Fatalf("alias cycle: %+v", v)
	}
	// The same cycle entered through an alias in the middle of the chain.
	h := newFleet()
	h.add("a", relKB(t, "a", [2]string{"b", "b"}))
	h.add("b", relKB(t, "b", [2]string{"old", "core"}))
	h.add("new", relKB(t, "core", [2]string{"a", "a"}))
	hv := resolve(t, h.mount(map[string]string{"old": "new"}), h.byName["a"], nil)
	if len(hv.Resolved) != 0 || len(hv.Unresolved) != 1 || hv.Unresolved[0].Reason != RelationCycle {
		t.Fatalf("alias in the middle of a cycle: %+v", hv)
	}
}

// AC-kbrel-7
func TestUnreadableTargetEqualsAbsent(t *testing.T) {
	t.Parallel()
	mk := func(withTarget bool) []byte {
		f := newFleet()
		f.add("a", relKB(t, "a", [2]string{"x", "x"}))
		if withTarget {
			f.add("x", relKB(t, "x", [2]string{"y", "y"}))
			f.add("y", relKB(t, "y"))
		}
		m := f.mount(nil)
		v := resolve(t, m, f.byName["a"], func(n string) bool { return n == "a" })
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(v.Visited(), []string{"x"}) {
			t.Fatalf("visited = %v", v.Visited())
		}
		return b
	}
	present, absent := mk(true), mk(false)
	if string(present) != string(absent) {
		t.Fatalf("unreadable target differs from absent:\n%s\n%s", present, absent)
	}
	if !strings.Contains(string(present), `"reason":"not_mounted"`) {
		t.Fatalf("view = %s", present)
	}

	// An unreadable middle node: the closure stops there.
	f := newFleet()
	f.add("a", relKB(t, "a", [2]string{"b", "b"}, [2]string{"c", "c"}))
	f.add("b", relKB(t, "b", [2]string{"d", "d"}))
	f.add("c", relKB(t, "c", [2]string{"b", "b"}))
	f.add("d", relKB(t, "d"))
	m := f.mount(nil)
	v := resolve(t, m, f.byName["a"], func(n string) bool { return n != "d" })
	if !reflect.DeepEqual(v.Closure, []string{"b", "c"}) {
		t.Fatalf("closure = %v", v.Closure)
	}
}

// AC-kbrel-8
func TestRemoteMismatch(t *testing.T) {
	t.Parallel()
	f := newFleet()
	f.add("a", relKB(t, "a", [2]string{"b", "b"}, [2]string{"c", "c"}))
	f.add("b", relKB(t, "other"))
	f.add("c", relKB(t, ""))
	v := resolve(t, f.mount(nil), f.byName["a"], nil)
	got := reasons(v)
	if got["b"] != RelationRemoteMismatch || got["c"] != RelationNoRemote || len(v.Closure) != 0 || len(v.Resolved) != 0 {
		t.Fatalf("view = %+v", v)
	}
	if b, _ := json.Marshal(v); strings.Contains(string(b), "other") {
		t.Fatalf("the target's actual remote leaked: %s", b)
	}
}

// AC-kbrel-9
func TestSelfRelation(t *testing.T) {
	t.Parallel()
	f := newFleet()
	f.add("a", relKB(t, "a", [2]string{"a", "elsewhere"}, [2]string{"z", "a"}))
	v := resolve(t, f.mount(nil), f.byName["a"], nil)
	got := reasons(v)
	if got["a"] != RelationSelf || got["z"] != RelationSelf || len(v.Resolved) != 0 || len(v.Closure) != 0 {
		t.Fatalf("view = %+v", v)
	}
}

// AC-kbrel-10
func TestRelationThroughAlias(t *testing.T) {
	t.Parallel()
	f := newFleet()
	f.add("a", relKB(t, "a", [2]string{"old", "core"}, [2]string{"older", "unrelated"}))
	f.add("new", relKB(t, "core"))
	v := resolve(t, f.mount(map[string]string{"old": "new", "older": "new"}), f.byName["a"], nil)
	if !reflect.DeepEqual(v.Resolved, []string{"new"}) || !reflect.DeepEqual(v.Closure, []string{"new"}) || v.Declared[0].Name != "old" {
		t.Fatalf("alias: %+v", v)
	}
	if reasons(v)["older"] != RelationRemoteMismatch {
		t.Fatalf("alias with a different origin: %+v", v)
	}
	// A KB later mounted under the old name wins over the alias.
	f.add("old", relKB(t, "core"))
	v = resolve(t, f.mount(map[string]string{"old": "new"}), f.byName["a"], nil)
	if !reflect.DeepEqual(v.Resolved, []string{"old"}) {
		t.Fatalf("mounted name must win: %+v", v)
	}
}

// AC-kbrel-11
func TestResolveReadErrorIsAnError(t *testing.T) {
	t.Parallel()
	f := newFleet()
	f.add("a", relKB(t, "a", [2]string{"b", "b"}))
	f.add("b", relKB(t, "b", [2]string{"c", "c"}))
	f.add("c", relKB(t, "c"))
	m := f.mount(nil)
	c := f.byName["c"]
	c.NormalizedOrigin() // memoized: the node is still a candidate when its settings cannot be read
	if err := os.RemoveAll(filepath.Join(c.Root, ".git")); err != nil {
		t.Fatal(err)
	}
	v, err := ResolveRelations(RelationEnv{Mounts: m}, f.byName["a"])
	if err == nil {
		t.Fatalf("expected an error, got %+v", v)
	}
	if !reflect.DeepEqual(v, RelationView{}) {
		t.Fatalf("a partial view came back with the error: %+v", v)
	}
}

// A git failure reading a target's origin is an error, never no_remote.
func TestResolveOriginReadErrorIsAnError(t *testing.T) {
	t.Parallel()
	f := newFleet()
	f.add("a", relKB(t, "a", [2]string{"b", "b"}))
	f.add("b", relKB(t, "b"))
	m := f.mount(nil)
	b := f.byName["b"]
	if err := os.Rename(filepath.Join(b.Root, ".git"), filepath.Join(b.Root, ".git-away")); err != nil {
		t.Fatal(err)
	}
	v, err := ResolveRelations(RelationEnv{Mounts: m}, f.byName["a"])
	if err == nil {
		t.Fatalf("a failing git read as a reason: %+v", v)
	}
	if !reflect.DeepEqual(v, RelationView{}) {
		t.Fatalf("a partial view came back with the error: %+v", v)
	}
}

func TestResolveNodeCap(t *testing.T) {
	t.Parallel()
	f := newFleet()
	n := relationNodeCap + 3
	for i := 0; i < n; i++ {
		var deps [][2]string
		if i+1 < n {
			deps = [][2]string{{fmt.Sprintf("k%d", i+1), fmt.Sprintf("k%d", i+1)}}
		}
		f.add(fmt.Sprintf("k%d", i), relKB(t, fmt.Sprintf("k%d", i), deps...))
	}
	v := resolve(t, f.mount(nil), f.byName["k0"], nil)
	if got := reasons(v)["k1"]; got != RelationTooDeep || len(v.Resolved) != 0 {
		t.Fatalf("cap: %v %+v", got, v.Unresolved)
	}
}

func TestResolveNoMounts(t *testing.T) {
	t.Parallel()
	k := relKB(t, "a", [2]string{"b", "b"})
	v, err := ResolveRelations(RelationEnv{}, k)
	if err != nil || reasons(v)["b"] != RelationNotMounted {
		t.Fatalf("%+v, %v", v, err)
	}
}
