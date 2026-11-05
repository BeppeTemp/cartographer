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
	// Warnings are `requires` declarations that could not be read: the artifact
	// is then treated as having none, a sync never fails because of them.
	Warnings []string
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
	art    Artifact
	reqs   []string
	alive  bool
	reason string // why it was removed, once it is
}

type resolveInput struct {
	in      ProjectionInput
	out     Manifest
	notes   []ProjectionNote
	dropped map[string]string
	entries []*resolveEntry
	removed int
}

// ResolveProjections returns, by Key, what each input receives. The visible
// set of a projection is its own candidates, the global projection of the same
// provider and the projections of its ancestor workspaces, the latter only for
// the kinds a client is proven to load from there. Each projection is judged
// against that union but removes artifacts only from itself.
func ResolveProjections(inputs []ProjectionInput, opts ProjectOptions) map[string]ProjectionResult {
	rs := make([]*resolveInput, len(inputs))
	order := make([]int, len(inputs))
	var warnings = make([][]string, len(inputs))
	for i, in := range inputs {
		out, notes, dropped := projectOne(in.Manifest, in.Provider, in.Scope, opts)
		r := &resolveInput{in: in, out: out, notes: notes, dropped: dropped}
		for _, a := range out.Artifacts {
			e := &resolveEntry{art: a, alive: true}
			if !a.BuiltIn && a.Source != "bundle" {
				src := a
				if o, ok := a.Origin(); ok {
					src = o
				}
				reqs, err := RequiresOf(src, opts.BundleFS, opts.KBRoots)
				if err != nil {
					warnings[i] = append(warnings[i], fmt.Sprintf("requires of %s/%s ignored: %v", src.Kind, src.Name, err))
				}
				e.reqs = reqs
			}
			r.entries = append(r.entries, e)
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

	present := func(i int, kind, name string) bool {
		for _, v := range views[i] {
			if v.ancestor && !ancestorVisibleKinds[v.r.in.Provider][kind] {
				continue
			}
			for _, e := range v.r.entries {
				if !e.alive {
					continue
				}
				a := e.art
				if (a.Kind == kind && a.Name == name) || (a.DerivedFrom != nil && a.DerivedFrom.Kind == kind && a.DerivedFrom.Name == name) {
					return true
				}
			}
		}
		return false
	}
	// causeOf says why kind/name is not there: what dropped it from its
	// projection, or the requirement it failed.
	causeOf := func(i int, kind, name string) string {
		id := kind + "/" + name
		for _, v := range views[i] {
			if v.ancestor && !ancestorVisibleKinds[v.r.in.Provider][kind] {
				continue
			}
			for _, e := range v.r.entries {
				if e.alive {
					continue
				}
				a := e.art
				oid := a.Kind + "/" + a.Name
				if a.DerivedFrom != nil {
					oid = a.DerivedFrom.Kind + "/" + a.DerivedFrom.Name
				}
				if oid == id || a.Kind+"/"+a.Name == id {
					return "skipped: " + e.reason
				}
			}
			if d, ok := v.r.dropped[id]; ok {
				return "skipped: " + d
			}
		}
		return "not provided to this client"
	}

	type removal struct {
		r *resolveInput
		e *resolveEntry
		w string
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
							batch = append(batch, removal{r, e, fmt.Sprintf("requires %s/%s (%s)", kind, name, causeOf(idx, kind, name))})
							break
						}
						continue
					}
					if st := CapabilityStatus(r.in.Provider, r.in.Scope, Capability(req)); st != CapVerified {
						batch = append(batch, removal{r, e, fmt.Sprintf("requires capability %s (it is %s on this client)", req, st)})
						break
					}
				}
			}
		}
		if len(batch) == 0 {
			break
		}
		// Reasons were computed against the state at the start of the round, so
		// they never depend on the order of removal within it.
		for _, b := range batch {
			b.e.alive = false
			b.e.reason = b.w
			b.r.removed++
		}
	}

	res := make(map[string]ProjectionResult, len(rs))
	for i, r := range rs {
		out, notes := r.out, r.notes
		if r.removed > 0 {
			out.Artifacts = nil
			for _, e := range r.entries {
				if e.alive {
					out.Artifacts = append(out.Artifacts, e.art)
					continue
				}
				id := e.art.Kind + "/" + e.art.Name
				if e.art.DerivedFrom != nil {
					id = e.art.DerivedFrom.Kind + "/" + e.art.DerivedFrom.Name
				}
				notes = append(notes, ProjectionNote{NoteSkipped, id, e.reason})
			}
			out.Revision = computeRevision(out.Artifacts)
		}
		res[r.in.Key] = ProjectionResult{Manifest: out, Notes: notes, Warnings: warnings[i]}
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
