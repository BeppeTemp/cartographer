package kb

import (
	"reflect"
	"testing"
)

func mountsFixture(t *testing.T) (a, b *KB) {
	t.Helper()
	return &KB{Root: t.TempDir()}, &KB{Root: t.TempDir()}
}

// AC-cross-kb-links-1
func TestMountsLookupAliases(t *testing.T) {
	a, b := mountsFixture(t)
	m := NewMounts(map[string]*KB{"kb-a": a, "new": b}, []string{"kb-a", "new"},
		map[string]string{"old": "new", "gone": "unmounted", "kb-a": "new"})

	if k, ok := m.Lookup("old"); !ok || k != b {
		t.Errorf("Lookup(old) = %v, %v; want the KB renamed to new", k, ok)
	}
	if k, ok := m.Lookup("kb-a"); !ok || k != a {
		t.Errorf("a mounted name must beat an alias: got %v, %v", k, ok)
	}
	if _, ok := m.Lookup("gone"); ok {
		t.Error("an alias to an unmounted KB must not resolve")
	}
	if _, ok := m.Lookup("nope"); ok {
		t.Error("an unknown name must not resolve")
	}
	// single hop: an alias to an alias does not chain.
	m2 := NewMounts(map[string]*KB{"new": b}, []string{"new"}, map[string]string{"older": "old", "old": "new"})
	if _, ok := m2.Lookup("older"); ok {
		t.Error("aliases are single hop")
	}
	for in, want := range map[string]string{"new": "new", "old": "new", "gone": "gone", "kb-a": "kb-a", "nope": "nope"} {
		if got := m.Canonical(in); got != want {
			t.Errorf("Canonical(%q) = %q, want %q", in, got, want)
		}
	}
	if got, want := m.NamesFor("new"), []string{"new", "old"}; !reflect.DeepEqual(got, want) {
		t.Errorf("NamesFor(new) = %v, want %v", got, want)
	}
	if got := m.Names(); !reflect.DeepEqual(got, []string{"kb-a", "new"}) {
		t.Errorf("Names() = %v", got)
	}
}

// AC-cross-kb-links-2
func TestMountName(t *testing.T) {
	a, b := mountsFixture(t)
	outside := &KB{Root: t.TempDir()}
	if got := a.MountName(); got != "" || a.Mounts() != nil {
		t.Errorf("nil mounts: MountName = %q, Mounts = %v", got, a.Mounts())
	}
	m := NewMounts(map[string]*KB{"kb-a": a, "kb-b": b}, []string{"kb-a", "kb-b"}, map[string]string{"old": "kb-b"})
	a.SetMounts(m)
	b.SetMounts(m)
	outside.SetMounts(m)
	if got := a.MountName(); got != "kb-a" {
		t.Errorf("a.MountName() = %q", got)
	}
	if got := b.MountName(); got != "kb-b" {
		t.Errorf("b.MountName() = %q (an alias is never a name)", got)
	}
	if got := outside.MountName(); got != "" {
		t.Errorf("a KB outside the set: MountName = %q, want empty", got)
	}
}

func TestNewMountsOrderMatchesSet(t *testing.T) {
	a, b := mountsFixture(t)
	// "ghost" is in order but not mounted; "kb-b" is mounted but not in order; "kb-a" is listed twice.
	m := NewMounts(map[string]*KB{"kb-a": a, "kb-b": b}, []string{"ghost", "kb-a", "kb-a"}, nil)
	if got, want := m.Names(), []string{"kb-a", "kb-b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	for _, n := range m.Names() {
		if _, ok := m.Lookup(n); !ok {
			t.Errorf("Names() lists %q but Lookup rejects it", n)
		}
	}
	b.SetMounts(m)
	if got := b.MountName(); got != "kb-b" {
		t.Errorf("a KB missing from order is invisible: MountName = %q", got)
	}
}
