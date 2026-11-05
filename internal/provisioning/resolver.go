package provisioning

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// ResolveProjections is the one client-side step that decides what each
// projection of a sync receives (D1656): the projection of ProjectManifest
// (destination, strict_tools, agent -> skill degradation) and then the
// evaluation of `requires` on the result, over the greatest fixpoint.
//
// Sync and status both call it with the same inputs, or drift would be
// permanent: a requirement that flips must flip in both.

// ProjectionInput is one projection to resolve: what a sync materializes for a
// provider at one base dir. Manifest is its source set, after the caller's own
// exclusions (derived bindings leave kinds out).
type ProjectionInput struct {
	// Key identifies the projection in the result map.
	Key      string
	Provider configurator.Provider
	Scope    Scope
	// Workspace is the canonical path of the bound workspace, empty for the
	// global projection.
	Workspace string
	Manifest  Manifest
}

// ProjectionResult is what one projection receives.
type ProjectionResult struct {
	Manifest Manifest
	Notes    []ProjectionNote
}

// ancestorVisibleKinds are the kinds a client is proven to load from the
// projection of an ancestor workspace (D1019, docs/harnesses.md): skills on
// OpenCode. Everything else is conservatively NOT visible across workspaces
// until a probe widens this table.
var ancestorVisibleKinds = map[configurator.Provider]map[string]bool{
	configurator.ProviderOpenCode: {"skill": true},
}

// resolveEntry is one artifact of one projection during the fixpoint.
type resolveEntry struct {
	art   Artifact
	reqs  []string
	alive bool
	// Why it was removed, once it is: final is a complete reason (an unreadable
	// declaration); otherwise head is "requires <what>" and the cause is either
	// the text in cause or the removed entry via, rendered lazily and capped so a
	// long chain does not build quadratic strings.
	final string
	head  string
	cause string
	via   *resolveEntry
}

// maxCauseDepth caps the nesting of a rendered cause chain.
const maxCauseDepth = 8

func (e *resolveEntry) reason(depth int) string {
	if e.final != "" {
		return e.final
	}
	switch {
	case e.via == nil:
		return e.head + " (" + e.cause + ")"
	case depth >= maxCauseDepth:
		return e.head + " (skipped: ...)"
	}
	return e.head + " (skipped: " + e.via.reason(depth+1) + ")"
}

type resolveInput struct {
	in      ProjectionInput
	out     Manifest
	notes   []ProjectionNote
	dropped map[string]string
	entries []*resolveEntry
	// byID indexes the entries by "kind/name", under the artifact's own and its
	// origin's name, dead ones included, so a lookup is not a scan.
	byID    map[string][]*resolveEntry
	removed int
}

// originID is the "kind/name" an entry stands for in a report: its origin's
// when it is a derived skill.
func (e *resolveEntry) originID() string {
	if e.art.DerivedFrom != nil {
		return e.art.DerivedFrom.Kind + "/" + e.art.DerivedFrom.Name
	}
	return e.art.Kind + "/" + e.art.Name
}

// ResolveProjections returns, by Key, what each input receives. The visible
// set of a projection is its own candidates, the global projection of the same
// provider and the projections of its ancestor workspaces, the latter only for
// the kinds a client is proven to load from there. Each projection is judged
// against that union but removes artifacts only from itself.
//
// A `requires` that cannot be read counts as unsatisfied: the artifact is
// skipped ("requires unreadable: ..."), in sync and status alike.
func ResolveProjections(inputs []ProjectionInput, opts ProjectOptions) map[string]ProjectionResult {
	rs := make([]*resolveInput, len(inputs))
	order := make([]int, len(inputs))
	for i, in := range inputs {
		out, notes, dropped := projectOne(in.Manifest, in.Provider, in.Scope, opts)
		r := &resolveInput{in: in, out: out, notes: notes, dropped: dropped, byID: map[string][]*resolveEntry{}}
		for _, a := range out.Artifacts {
			e := &resolveEntry{art: a, alive: true}
			if !a.BuiltIn && a.Source != "bundle" {
				src := a
				if o, ok := a.Origin(); ok {
					src = o
				}
				reqs, err := RequiresOf(src, opts.BundleFS, opts.KBRoots)
				if err != nil {
					e.alive = false
					e.final = "requires unreadable: " + err.Error()
					r.removed++
				}
				e.reqs = reqs
			}
			r.entries = append(r.entries, e)
			id := a.Kind + "/" + a.Name
			r.byID[id] = append(r.byID[id], e)
			if oid := e.originID(); oid != id {
				r.byID[oid] = append(r.byID[oid], e)
			}
		}
		rs[i] = r
		order[i] = i
	}
	// A deterministic evaluation order whatever order the caller built inputs in.
	sort.SliceStable(order, func(x, y int) bool { return inputs[order[x]].Key < inputs[order[y]].Key })

	// views[i] lists the inputs whose artifacts projection i can see, itself
	// first; ancestor marks the ones filtered by ancestorVisibleKinds.
	type view struct {
		r        *resolveInput
		ancestor bool
	}
	views := make([][]view, len(rs))
	for i, r := range rs {
		views[i] = []view{{r, false}}
		for j, o := range rs {
			if i == j || o.in.Provider != r.in.Provider {
				continue
			}
			switch {
			case o.in.Workspace == "" && r.in.Workspace != "":
				views[i] = append(views[i], view{o, false})
			case o.in.Workspace != "" && isAncestorDir(o.in.Workspace, r.in.Workspace):
				views[i] = append(views[i], view{o, true})
			}
		}
	}
	visible := func(v view, kind string) bool {
		return !v.ancestor || ancestorVisibleKinds[v.r.in.Provider][kind]
	}

	present := func(i int, kind, name string) bool {
		id := kind + "/" + name
		for _, v := range views[i] {
			if !visible(v, kind) {
				continue
			}
			for _, e := range v.r.byID[id] {
				if e.alive {
					return true
				}
			}
		}
		return false
	}
	// missing says why kind/name is not there: the removed entry, what dropped
	// it from its projection, or that nothing provides it.
	missing := func(i int, kind, name string) (via *resolveEntry, cause string) {
		id := kind + "/" + name
		for _, v := range views[i] {
			if !visible(v, kind) {
				continue
			}
			for _, e := range v.r.byID[id] {
				if !e.alive {
					return e, ""
				}
			}
			if d, ok := v.r.dropped[id]; ok {
				return nil, "skipped: " + d
			}
		}
		return nil, "not provided to this client"
	}

	type removal struct {
		e *resolveEntry
		r *resolveInput
		// head, cause and via as in resolveEntry.
		head, cause string
		via         *resolveEntry
	}
	for {
		var batch []removal
		for _, idx := range order {
			r := rs[idx]
			for _, e := range r.entries {
				if !e.alive {
					continue
				}
				for _, req := range e.reqs {
					if kind, name, isRef := strings.Cut(req, ":"); isRef {
						if !present(idx, kind, name) {
							via, cause := missing(idx, kind, name)
							batch = append(batch, removal{e, r, "requires " + kind + "/" + name, cause, via})
							break
						}
						continue
					}
					if st := CapabilityStatus(r.in.Provider, r.in.Scope, Capability(req)); st != CapVerified {
						batch = append(batch, removal{e, r, "requires capability " + req, fmt.Sprintf("it is %s on this client", st), nil})
						break
					}
				}
			}
		}
		if len(batch) == 0 {
			break
		}
		// Reasons were decided against the state at the start of the round, so
		// they never depend on the order of removal within it.
		for _, b := range batch {
			b.e.alive = false
			b.e.head, b.e.cause, b.e.via = b.head, b.cause, b.via
			b.r.removed++
		}
	}

	res := make(map[string]ProjectionResult, len(rs))
	for _, r := range rs {
		out, notes := r.out, r.notes
		if r.removed > 0 {
			out.Artifacts = nil
			for _, e := range r.entries {
				if e.alive {
					out.Artifacts = append(out.Artifacts, e.art)
					continue
				}
				notes = append(notes, ProjectionNote{NoteSkipped, e.originID(), e.reason(0)})
			}
			out.Revision = computeRevision(out.Artifacts)
		}
		res[r.in.Key] = ProjectionResult{Manifest: out, Notes: notes}
	}
	return res
}

// isAncestorDir reports whether dir is a strict ancestor of sub.
func isAncestorDir(dir, sub string) bool {
	rel, err := filepath.Rel(dir, sub)
	if err != nil || rel == "." {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
