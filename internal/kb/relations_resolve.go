package kb

import (
	"slices"
	"sort"
)

// The reasons a declared relation is not resolved. The strings are stable:
// clients print them.
const (
	RelationNotMounted     = "not_mounted"
	RelationRemoteMismatch = "remote_mismatch"
	RelationNoRemote       = "no_remote"
	RelationSelf           = "self"
	RelationCycle          = "cycle"
	RelationTooDeep        = "too_deep"
)

// relationNodeCap bounds the KBs one resolution looks at (a defence, not a
// feature): the rest is unresolved with reason too_deep.
const relationNodeCap = 64

// RelationUnresolved is a declared relation that does not resolve, as written.
type RelationUnresolved struct {
	Name   string `json:"name"`
	Remote string `json:"remote"`
	Reason string `json:"reason"`
}

// RelationView is what a caller learns of one KB's dependencies. Arrays are
// never nil. Resolved and Closure carry the current mount name of each target
// (an alias is followed); Declared and Unresolved carry the name as written.
type RelationView struct {
	Declared   []Relation           `json:"declared"`
	Resolved   []string             `json:"resolved"`
	Closure    []string             `json:"closure"`
	Unresolved []RelationUnresolved `json:"unresolved"`
	visited    []string
}

// Visited lists, sorted, every current name the resolution looked at.
func (v RelationView) Visited() []string { return append([]string(nil), v.visited...) }

// RelationEnv is what a resolution runs against. Mounts is the KB's own set
// (nil: nothing is mounted); Readable takes a current mount name and says
// whether the caller may know of that KB (nil: every KB).
type RelationEnv struct {
	Mounts   Mounts
	Readable func(name string) bool
}

type relationEdge struct {
	target *KB
	name   string // current mount name
}

// edge resolves one declared relation of node. reason is empty for a
// candidate. An unreadable target is indistinguishable from an unmounted one:
// no field of the result differs, and the target is not inspected. A git
// failure reading an origin is an error, never a reason.
func (env RelationEnv) edge(node *KB, rel Relation) (relationEdge, string, error) {
	selfName := node.MountName()
	if env.Mounts == nil {
		return relationEdge{}, RelationNotMounted, nil
	}
	cur := env.Mounts.Canonical(rel.Name)
	own, ownOK, err := node.NormalizedOriginErr()
	if err != nil {
		return relationEdge{}, "", err
	}
	if (selfName != "" && cur == selfName) || (ownOK && own == rel.Remote) {
		return relationEdge{}, RelationSelf, nil
	}
	t, ok := env.Mounts.Lookup(rel.Name)
	if !ok || (env.Readable != nil && !env.Readable(cur)) {
		return relationEdge{}, RelationNotMounted, nil
	}
	origin, ok, err := t.NormalizedOriginErr()
	if err != nil {
		return relationEdge{}, "", err
	}
	if !ok {
		return relationEdge{}, RelationNoRemote, nil
	}
	if origin != rel.Remote {
		return relationEdge{}, RelationRemoteMismatch, nil
	}
	return relationEdge{target: t, name: cur}, "", nil
}

// walkResult is what one depth-first walk from a direct edge found.
type walkResult struct {
	order   []string // current names, discovery order, start included
	cycle   bool     // self is reachable
	tooDeep bool     // relationNodeCap would be passed
}

// walk follows the candidate edges from start in depth-first discovery order.
// entered is the resolution's budget: every KB any walk entered.
func (env RelationEnv) walk(self *KB, start relationEdge, entered map[string]bool) (walkResult, error) {
	var res walkResult
	seen := map[string]bool{}
	var visit func(e relationEdge) error
	visit = func(e relationEdge) error {
		if !entered[e.name] && len(entered) >= relationNodeCap {
			res.tooDeep = true
			return nil
		}
		seen[e.name] = true
		entered[e.name] = true
		res.order = append(res.order, e.name)
		rels, err := e.target.Relations()
		if err != nil {
			return err
		}
		for _, r := range rels {
			next, reason, err := env.edge(e.target, r)
			if err != nil {
				return err
			}
			if reason != "" || seen[next.name] {
				continue
			}
			if next.target == self {
				res.cycle = true
				continue
			}
			if err := visit(next); err != nil {
				return err
			}
			if res.tooDeep {
				return nil
			}
		}
		return nil
	}
	err := visit(start)
	return res, err
}

// ResolveRelations resolves self's declared dependencies against env. The error
// is the first one of Relations() on any KB entered: the view is then not
// partial. An unreadable KB is never entered, so the closure is a subset of
// what the caller reads, and its own relations never show.
//
// A direct edge self to T is "cycle" when self is reachable from T through
// candidate edges; it is not followed. Cycles that do not pass through self
// mark no edge of self.
func ResolveRelations(env RelationEnv, self *KB) (RelationView, error) {
	v := RelationView{Declared: []Relation{}, Resolved: []string{}, Closure: []string{}, Unresolved: []RelationUnresolved{}}
	declared, err := self.Relations()
	if err != nil {
		return RelationView{}, err
	}
	v.Declared = append(v.Declared, declared...)
	entered := map[string]bool{}
	looked := map[string]bool{}
	followed := map[string]bool{}
	for _, r := range declared {
		if env.Mounts != nil {
			looked[env.Mounts.Canonical(r.Name)] = true
		} else {
			looked[r.Name] = true
		}
		e, reason, err := env.edge(self, r)
		if err != nil {
			return RelationView{}, err
		}
		if reason == "" && followed[e.name] {
			continue // two declared names reaching one target count once
		}
		if reason == "" {
			res, werr := env.walk(self, e, entered)
			if werr != nil {
				return RelationView{}, werr
			}
			switch {
			case res.tooDeep:
				reason = RelationTooDeep
			case res.cycle:
				reason = RelationCycle
			default:
				followed[e.name] = true
				v.Resolved = append(v.Resolved, e.name)
				for _, n := range res.order {
					if !slices.Contains(v.Closure, n) {
						v.Closure = append(v.Closure, n)
					}
				}
			}
		}
		if reason != "" {
			v.Unresolved = append(v.Unresolved, RelationUnresolved{Name: r.Name, Remote: r.Remote, Reason: reason})
		}
	}
	for n := range entered {
		looked[n] = true
	}
	for n := range looked {
		v.visited = append(v.visited, n)
	}
	sort.Strings(v.visited)
	return v, nil
}
