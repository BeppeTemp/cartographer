package lint

import (
	"fmt"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/graphalgo"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// Structural checks (D243): what the shape of the link graph and the
// supersede relation say about a KB's hygiene, beyond any single file.

const (
	// islandMinSize: a lone concept is already an orphan; an island is two or
	// more concepts linked to each other and to nothing else.
	islandMinSize = 2
	// cutConceptMinSeparated: a page with a child or two is the normal shape
	// of a wiki; three concepts hanging on one page is a single point of
	// failure worth knowing about.
	cutConceptMinSeparated = 3
	// mapMisfitMinNeighbours and the two-thirds majority keep map_misfit to
	// concepts whose links clearly point elsewhere: with fewer neighbours, or
	// a weaker majority, the "right" map is a matter of taste.
	mapMisfitMinNeighbours = 4
	mapMisfitMajorityNum   = 2
	mapMisfitMajorityDen   = 3
)

// retired is the status vocabulary's "no longer current" (docs/data-plane.md):
// deprecated, or superseded — what supersede writes. disputed is not retired.
func retired(status string) bool { return status == "deprecated" || status == "superseded" }

// structure is the whole-KB graph analysis one lint run shares. It is
// computed on the whole graph whatever the scope — a scoped lint must give a
// concept the same verdict a whole-KB lint does — and findings are emitted
// only for concepts in scope.
type structure struct {
	lg *kb.LinkGraph
	// cut is the message of each cut_concept finding, by concept.
	cut map[okf.ConceptID]string
	// islands are the non-main components worth reporting, with their anchor.
	islands []island
	// kinds is each collection's declared kind ("map" by default).
	kinds    map[string]string
	resolves func(id string) bool
}

type island struct {
	anchor  okf.ConceptID
	members []okf.ConceptID
}

func analyseStructure(k *kb.KB, lg *kb.LinkGraph, archives []string) (*structure, error) {
	s := &structure{lg: lg, cut: map[okf.ConceptID]string{}, kinds: map[string]string{}}
	for _, a := range archives {
		kind := "map"
		if meta, err := k.ReadArchiveMeta(a); err == nil {
			if v, ok := meta.Get("kind"); ok {
				if str, ok := v.(string); ok && str != "" {
					kind = str
				}
			}
		}
		s.kinds[a] = kind
	}
	// The resolver concept_read uses (D149), so an expanded successor counts.
	s.resolves = func(id string) bool {
		cid, err := okf.PathToID(id + ".md")
		if err != nil {
			return false
		}
		_, err = k.ReadConcept(cid)
		return err == nil
	}

	g := lg.Graph
	comps := graphalgo.WeakComponents(g)
	if len(comps) == 0 {
		return s, nil
	}
	// The main component is the largest; among equals, the one holding the
	// smallest id — WeakComponents' own order.
	main := map[int]bool{}
	for _, u := range comps[0] {
		main[u] = true
	}
	undirected := g.Undirected()
	for _, comp := range comps[1:] {
		if len(comp) < islandMinSize {
			continue
		}
		anchor := comp[0]
		for _, u := range comp {
			if len(undirected[u]) > len(undirected[anchor]) {
				anchor = u
			}
		}
		members := make([]okf.ConceptID, len(comp))
		for i, u := range comp {
			members[i] = lg.IDs[u]
		}
		s.islands = append(s.islands, island{anchor: lg.IDs[anchor], members: members})
	}
	for _, a := range graphalgo.ArticulationPoints(g) {
		if !main[a.Node] || a.Separated < cutConceptMinSeparated {
			continue
		}
		cutOff := separatedNodes(g, a.Node)
		examples := make([]string, 0, 3)
		for _, u := range cutOff {
			if len(examples) == 3 {
				break
			}
			examples = append(examples, string(lg.IDs[u]))
		}
		s.cut[lg.IDs[a.Node]] = fmt.Sprintf("removing or unlinking this concept disconnects %d concepts (e.g. %s)",
			a.Separated, strings.Join(examples, ", "))
	}
	return s, nil
}

// separatedNodes lists, ascending, the nodes of a's component that removing a
// cuts off from the largest remaining part.
func separatedNodes(g *graphalgo.Graph, a int) []int {
	adj := g.Undirected()
	seen := map[int]bool{a: true}
	var parts [][]int
	for _, s := range adj[a] {
		if seen[s] {
			continue
		}
		seen[s] = true
		part := []int{s}
		for i := 0; i < len(part); i++ {
			for _, v := range adj[part[i]] {
				if !seen[v] {
					seen[v] = true
					part = append(part, v)
				}
			}
		}
		sort.Ints(part)
		parts = append(parts, part)
	}
	// Largest part stays; ties keep the one with the smallest member, as
	// parts come in ascending order of their first neighbour of a.
	largest := 0
	for i, p := range parts {
		if len(p) > len(parts[largest]) {
			largest = i
		}
	}
	var out []int
	for i, p := range parts {
		if i != largest {
			out = append(out, p...)
		}
	}
	sort.Ints(out)
	return out
}

// kindOf is the declared kind of an id's collection, "" for a concept at the
// root or outside any declared collection (services/).
func (s *structure) kindOf(id okf.ConceptID) string {
	parts := strings.Split(string(id), "/")
	if len(parts) < 2 {
		return ""
	}
	return s.kinds[parts[0]]
}

// conceptChecks returns the per-concept structural findings for id, to be
// passed through the caller's emit (so lint_ignore applies).
func (s *structure) conceptChecks(id okf.ConceptID, relPath string) []Finding {
	i, ok := s.lg.Index[id]
	if !ok {
		return nil
	}
	var out []Finding
	facets := s.lg.Facets[i]

	// --- cut_concept (info) ---
	if msg, ok := s.cut[id]; ok {
		out = append(out, Finding{Path: relPath, Check: "cut_concept", Severity: SevInfo, Message: msg})
	}

	// --- broken_relation (warning) ---
	if sb := facets.SupersededBy; sb != "" {
		switch {
		case sb == string(id):
			out = append(out, Finding{Path: relPath, Check: "broken_relation", Severity: SevWarning,
				Message: fmt.Sprintf("superseded_by %q names this concept itself", sb)})
		case !s.resolves(sb):
			out = append(out, Finding{Path: relPath, Check: "broken_relation", Severity: SevWarning,
				Message: fmt.Sprintf("superseded_by %q is not a concept", sb)})
		}
	}

	// --- link_to_retired (info) ---
	// From a live concept in a map only: an incident in a journal legitimately
	// cites a component that is gone today.
	if !retired(facets.Status) && s.kindOf(id) == "map" {
		for _, t := range s.lg.Graph.Out[i] { // ascending, so sorted by target
			tf := s.lg.Facets[t]
			if !retired(tf.Status) {
				continue
			}
			msg := fmt.Sprintf("links to retired concept %s (status: %s)", s.lg.IDs[t], tf.Status)
			if tf.SupersededBy != "" && tf.SupersededBy != string(s.lg.IDs[t]) && s.resolves(tf.SupersededBy) {
				msg += "; successor: " + tf.SupersededBy
			}
			out = append(out, Finding{Path: relPath, Check: "link_to_retired", Severity: SevInfo, Message: msg})
		}
	}

	// --- map_misfit (info) ---
	// A direct neighbour-majority rule, not community detection: communities
	// shift with unrelated edits, and the finding would flicker.
	if home := strings.Split(string(id), "/")[0]; s.kindOf(id) == "map" {
		counts := map[string]int{}
		n := 0
		for _, v := range s.lg.Graph.Undirected()[i] {
			vid := s.lg.IDs[v]
			if s.kindOf(vid) != "map" {
				continue // journals, root and services concepts do not vote
			}
			n++
			counts[strings.Split(string(vid), "/")[0]]++
		}
		if n >= mapMisfitMinNeighbours {
			maps := make([]string, 0, len(counts))
			for m := range counts {
				maps = append(maps, m)
			}
			sort.Strings(maps)
			for _, m := range maps {
				if m != home && counts[m]*mapMisfitMajorityDen >= n*mapMisfitMajorityNum {
					out = append(out, Finding{Path: relPath, Check: "map_misfit", Severity: SevInfo,
						Message: fmt.Sprintf("%d of %d linked concepts are in map %s: consider concept_move", counts[m], n, m)})
					break
				}
			}
		}
	}
	return out
}

// islandFindings are emitted once per island, on its anchor, when the anchor
// is in scope. Not suppressible: an island belongs to a component, not to any
// one concept's frontmatter.
func (s *structure) islandFindings(inScope func(okf.ConceptID) bool) []Finding {
	var out []Finding
	for _, isl := range s.islands {
		if !inScope(isl.anchor) {
			continue
		}
		names := make([]string, 0, 5)
		for _, m := range isl.members {
			if len(names) == 5 {
				names = append(names, "…")
				break
			}
			names = append(names, string(m))
		}
		out = append(out, Finding{
			Path:     okf.IDToPath(isl.anchor),
			Check:    "island",
			Severity: SevInfo,
			Message:  fmt.Sprintf("%d concepts form an island disconnected from the main graph: %s", len(isl.members), strings.Join(names, ", ")),
		})
	}
	return out
}
