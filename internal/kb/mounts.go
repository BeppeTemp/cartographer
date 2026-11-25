package kb

import (
	"sort"
	"sync/atomic"
)

// Mounts is how a KB reaches the other KBs mounted on the same server: by
// current name, or by an old name that kb_aliases still maps (D772). It is the
// one mechanism cross-KB features use; a KB opened without serve has none.
//
// Resolving through it reads the other KB's own caches only; it takes no git
// lock (internal/mcpserver/AGENTS.md).
type Mounts interface {
	// Lookup finds a KB by its mount name or by an alias. A mounted name beats
	// an alias (D772), so an old name never reaches a KB later mounted under it.
	Lookup(name string) (*KB, bool)
	// Names lists the current mount names, in mount order.
	Names() []string
	// Canonical returns the current mount name of name: itself when mounted,
	// else the target of an alias whose target is mounted, else name unchanged
	// (it resolves nowhere).
	Canonical(name string) string
	// NamesFor lists the current name and every alias pointing at it, sorted.
	NamesFor(current string) []string
}

type mounts struct {
	byName  map[string]*KB
	order   []string
	aliases map[string]string // from -> to, flat, single hop
}

// NewMounts builds the set the server hands to every KB. byName and aliases
// are copied; aliases maps an old name to the current one, single hop (D772).
//
// order and byName name the same set: an entry of order missing from byName is
// dropped, and a name of byName missing from order is appended in sorted
// order, so Names() lists exactly the names Lookup accepts (and MountName
// sees every KB).
func NewMounts(byName map[string]*KB, order []string, aliases map[string]string) Mounts {
	m := &mounts{
		byName:  make(map[string]*KB, len(byName)),
		order:   make([]string, 0, len(byName)),
		aliases: make(map[string]string, len(aliases)),
	}
	for n, k := range byName {
		m.byName[n] = k
	}
	seen := make(map[string]bool, len(byName))
	for _, n := range order {
		if _, ok := byName[n]; ok && !seen[n] {
			seen[n] = true
			m.order = append(m.order, n)
		}
	}
	var rest []string
	for n := range byName {
		if !seen[n] {
			rest = append(rest, n)
		}
	}
	sort.Strings(rest)
	m.order = append(m.order, rest...)
	for from, to := range aliases {
		m.aliases[from] = to
	}
	return m
}

func (m *mounts) Lookup(name string) (*KB, bool) {
	if k, ok := m.byName[name]; ok {
		return k, true
	}
	if to, ok := m.aliases[name]; ok {
		k, ok := m.byName[to]
		return k, ok
	}
	return nil, false
}

func (m *mounts) Names() []string { return append([]string(nil), m.order...) }

func (m *mounts) Canonical(name string) string {
	if _, ok := m.byName[name]; ok {
		return name
	}
	if to, ok := m.aliases[name]; ok {
		if _, ok := m.byName[to]; ok {
			return to
		}
	}
	return name
}

func (m *mounts) NamesFor(current string) []string {
	out := []string{current}
	for from, to := range m.aliases {
		// An alias shadowed by a mounted name does not reach current (D772).
		if _, mounted := m.byName[from]; to == current && from != current && !mounted {
			out = append(out, from)
		}
	}
	sort.Strings(out)
	return out
}

// mountsHolder keeps the set a KB belongs to; it is set once at serve start.
type mountsHolder struct {
	p atomic.Pointer[Mounts]
}

// SetMounts records the set of KBs this KB is mounted with. Pass the same set
// to every KB of a server.
func (k *KB) SetMounts(m Mounts) {
	k.mounts.p.Store(&m)
}

// Mounts returns the set of KBs mounted with this one, or nil when none was
// set (a bare kb.Open, single-KB stdio): every cross-KB lookup is then
// unresolved.
func (k *KB) Mounts() Mounts {
	if p := k.mounts.p.Load(); p != nil {
		return *p
	}
	return nil
}

// MountName is the name this KB is mounted under: the entry of Names() whose
// Lookup is this very *KB (an alias never counts). Empty when Mounts() is nil
// or the KB is not in the set.
func (k *KB) MountName() string {
	m := k.Mounts()
	if m == nil {
		return ""
	}
	for _, n := range m.Names() {
		if got, ok := m.Lookup(n); ok && got == k {
			return n
		}
	}
	return ""
}
