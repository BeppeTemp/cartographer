package lint

import (
	"fmt"
	"sort"
	"strings"
	"time"

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

// WholeGraphChecks are the checks computed on the whole-KB graph whose very
// presence, let alone their message, depends on concepts other than the one
// they are reported on. A caller that cannot see the whole KB must not receive
// them: a link_to_retired on a visible page counts, and names, the hidden
// pages that link it. broken_relation is not among them — it reads only the
// concept's own frontmatter. The graph-shape observations are not findings
// (D947); their own flag is ObservationSpec.WholeGraph.
var WholeGraphChecks = checkSet(func(s CheckSpec) bool { return s.WholeGraph })

// retired is the status vocabulary's "no longer current" (docs/data-plane.md):
// deprecated, superseded — what supersede writes — or archived (D322).
// disputed is not retired.
func retired(status string) bool { return kb.IsRetired(status) }

// structure is the whole-KB graph analysis one lint run shares. It is
// computed on the whole graph whatever the scope — a scoped lint must give a
// concept the same verdict a whole-KB lint does — and findings are emitted
// only for concepts in scope.
type structure struct {
	lg *kb.LinkGraph
	// cut is the message of each cut_concept observation, by concept.
	cut map[okf.ConceptID]string
	// islands are the non-main components worth reporting, with their anchor.
	islands []island
	// kinds is each collection's declared kind ("map" by default).
	kinds map[string]string
	// contracts are the map lint contracts, keyed by map name.
	contracts map[string]kb.MapContract
	resolves  func(id string) bool
}

type island struct {
	anchor  okf.ConceptID
	members []okf.ConceptID
}

// newStructure is the cheap part of the analysis: the collection kinds and the
// successor resolver, with no component or articulation pass. ScopedCheck
// (D312) uses it alone for the per-concept checks that need only the graph's
// edges and facets.
func newStructure(k *kb.KB, lg *kb.LinkGraph, archives []string, contracts map[string]kb.MapContract) *structure {
	s := &structure{lg: lg, cut: map[okf.ConceptID]string{}, kinds: map[string]string{}, contracts: contracts}
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
	return s
}

func analyseStructure(k *kb.KB, lg *kb.LinkGraph, archives []string, contracts map[string]kb.MapContract) (*structure, error) {
	s := newStructure(k, lg, archives, contracts)
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
		// Structural leaves are not fragility (D313): an expanded concept's
		// satellites hang off its index by construction, and a journal's
		// entries are normally linked only to their subject page. A vertex is
		// reported only for what remains.
		vertex := lg.IDs[a.Node]
		var cutOff []int
		for _, u := range separatedNodes(g, a.Node) {
			id := lg.IDs[u]
			if strings.HasPrefix(string(id), string(vertex)+"/") || s.kindOf(id) == "journal" {
				continue
			}
			cutOff = append(cutOff, u)
		}
		if len(cutOff) < cutConceptMinSeparated {
			continue
		}
		examples := make([]string, 0, 3)
		for _, u := range cutOff {
			if len(examples) == 3 {
				break
			}
			examples = append(examples, string(lg.IDs[u]))
		}
		s.cut[vertex] = fmt.Sprintf("removing or unlinking this concept disconnects %d concepts (e.g. %s)",
			len(cutOff), strings.Join(examples, ", "))
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

// admitsType reports whether the map's contract allows conceptType. A flexible
// map admits everything; a strict map admits only its declared concept_types.
func (s *structure) admitsType(mapName, conceptType string) bool {
	c, ok := s.contracts[mapName]
	if !ok {
		return true // no contract: accepts anything
	}
	if c.OntologyMode != "strict" {
		return true
	}
	for _, t := range c.ConceptTypes {
		if t == conceptType {
			return true
		}
	}
	return false
}

// conceptChecks returns the per-concept structural problems for id, to be
// passed through the caller's emit: broken_relation and link_to_retired. The
// graph-shape observations (cut_concept, island, map_misfit) are not problems
// and are not here (D947): observations() builds them for kb_review.
func (s *structure) conceptChecks(id okf.ConceptID, relPath string) []Finding {
	i, ok := s.lg.Index[id]
	if !ok {
		return nil
	}
	facets := s.lg.Facets[i]
	var out []Finding
	out = append(out, s.brokenRelation(id, relPath, facets)...)
	out = append(out, s.linkToRetired(i, relPath, facets)...)
	return out
}

// mapMisfit is the map_misfit observation for the concept at node i, if any.
//
// A direct neighbour-majority rule, not community detection: communities shift
// with unrelated edits, and the item would flicker. A retired concept is not
// misfiled: an archive holds retired things of every domain, so its neighbours
// are always elsewhere (D307).
func (s *structure) mapMisfit(i int, relPath string) []Finding {
	id := s.lg.IDs[i]
	facets := s.lg.Facets[i]
	home := strings.Split(string(id), "/")[0]
	if s.kindOf(id) != "map" || retired(facets.Status) {
		return nil
	}
	conceptType := facets.Type
	counts := map[string]int{}
	n := 0
	for _, v := range s.lg.Graph.Undirected()[i] {
		vid := s.lg.IDs[v]
		if s.kindOf(vid) != "map" || retired(s.lg.Facets[v].Status) {
			continue // journals, root, services and retired concepts do not vote
		}
		n++
		counts[strings.Split(string(vid), "/")[0]]++
	}
	if n < mapMisfitMinNeighbours {
		return nil
	}
	maps := make([]string, 0, len(counts))
	for m := range counts {
		maps = append(maps, m)
	}
	sort.Strings(maps)
	for _, m := range maps {
		if m != home && counts[m]*mapMisfitMajorityDen >= n*mapMisfitMajorityNum {
			// A candidate map must admit the concept's type (D295 WP3): a
			// strict map that refuses the type is not advice.
			if !s.admitsType(m, conceptType) {
				continue
			}
			return []Finding{newObservation("map_misfit", Finding{Path: relPath,
				Message: fmt.Sprintf("%d of %d linked concepts are in map %s: consider concept_move", counts[m], n, m)})}
		}
	}
	return nil
}

// observations are the graph-shape observations of the whole KB (D947):
// cut_concept, map_misfit per concept in graph order, then one island per
// component. They are shapes a reasonable maintainer may leave as they are, so
// they are kb_review items and never lint findings.
func (s *structure) observations() []Finding {
	var out []Finding
	for i, id := range s.lg.IDs {
		relPath := okf.IDToPath(id)
		if msg, ok := s.cut[id]; ok {
			out = append(out, newObservation("cut_concept", Finding{Path: relPath, Message: msg}))
		}
		out = append(out, s.mapMisfit(i, relPath)...)
	}
	out = append(out, s.islandFindings()...)
	return out
}

// brokenRelation reports a superseded_by that names the concept itself or
// something that is not a concept.
func (s *structure) brokenRelation(id okf.ConceptID, relPath string, facets kb.NodeFacets) []Finding {
	var out []Finding
	if sb := facets.SupersededBy; sb != "" {
		switch {
		case sb == string(id):
			out = append(out, newFinding("broken_relation", Finding{Path: relPath,
				Message: fmt.Sprintf("superseded_by %q names this concept itself", sb)}))
		case !s.resolves(sb):
			out = append(out, newFinding("broken_relation", Finding{Path: relPath,
				Message: fmt.Sprintf("superseded_by %q is not a concept", sb)}))
		default:
			if end, ok := s.supersessionEnd(sb); ok {
				out = append(out, newFinding("superseded_target_retired", Finding{Path: relPath,
					Message: fmt.Sprintf("superseded_by %q is itself superseded: the chain ends at %q, the current replacement", sb, end),
					Fix:     &Fix{Kind: FixSetValue, Field: "superseded_by", To: end}}))
			}
		}
	}
	return out
}

// supersessionEnd follows the explicit superseded_by chain from start, which
// is a superseded concept with a successor, to the first concept whose status
// is not superseded. ok is false (no finding, no better state) when start is
// not superseded, when the chain dead-ends in a superseded page with no
// successor, leaves the graph, or cycles: a deprecated or archived target is
// history kept readable on purpose (D322), so it is never walked past.
//
// This reads only the written concept's own pointer and its chain: a
// neighbour of a written concept is never examined, so retiring B never
// refuses a write for a finding it introduces on an untouched A (the whole-KB
// lint reports that later).
func (s *structure) supersessionEnd(start string) (string, bool) {
	cur := start
	visited := map[string]bool{}
	for hops := 0; hops <= len(s.lg.IDs); hops++ {
		i, ok := s.lg.Index[okf.ConceptID(cur)]
		if !ok || visited[cur] {
			return "", false
		}
		visited[cur] = true
		f := s.lg.Facets[i]
		if NormValue(f.Status) != "superseded" {
			return cur, cur != start
		}
		if f.SupersededBy == "" {
			return "", false
		}
		cur = f.SupersededBy
	}
	return "", false
}

// digestMissing is the digest_missing check (D322): an archived entry of a
// journal is covered when a concept of type digest in the same journal links
// it. One finding per (journal, quarter of the entry's timestamp), on the
// group's first entry in ascending id order; entries without a parseable
// timestamp form an "undated" group.
func (s *structure) digestMissing(k *kb.KB) []Finding {
	type group struct {
		journal, quarter string
		first            okf.ConceptID
		count            int
	}
	var groups []*group
	byKey := map[string]*group{}
	for i, id := range s.lg.IDs { // ascending
		if s.lg.Facets[i].Status != kb.StatusArchived || s.kindOf(id) != "journal" {
			continue
		}
		journal := strings.Split(string(id), "/")[0]
		if kb.IsDigestType(s.lg.Facets[i].Type) {
			continue // a digest is the covering page, not a covered entry
		}
		covered := false
		for _, u := range s.lg.Graph.In[i] {
			uid := s.lg.IDs[u]
			if kb.IsDigestType(s.lg.Facets[u].Type) && strings.Split(string(uid), "/")[0] == journal {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		quarter := "undated"
		if c, err := k.ReadConcept(id); err == nil {
			if fm, perr := okf.ParseFrontmatter(c.FrontmatterRaw); perr == nil {
				if ts, ok := frontmatterValue(fm, "timestamp").(string); ok && len(ts) >= 10 {
					if t, err := time.Parse("2006-01-02", ts[:10]); err == nil {
						quarter = fmt.Sprintf("%d-q%d", t.Year(), (int(t.Month())-1)/3+1)
					}
				}
			}
		}
		key := journal + "\x00" + quarter
		g := byKey[key]
		if g == nil {
			g = &group{journal: journal, quarter: quarter, first: id}
			byKey[key] = g
			groups = append(groups, g)
		}
		g.count++
	}
	var out []Finding
	for _, g := range groups {
		digest := g.journal + "/archive-" + g.quarter
		if g.quarter == "undated" {
			digest = g.journal + "/archive-<year>-q<n>"
		}
		out = append(out, newFinding("digest_missing", Finding{Path: okf.IDToPath(g.first),
			Message: fmt.Sprintf("%d archived entries of journal %q (%s) are linked by no digest: create or update a digest page %q that links each entry with its outcome", g.count, g.journal, g.quarter, digest)}))
	}
	return out
}

// retiredLinkers lists, ascending, the live map concepts that still link the
// concept at node i: the ones link_to_retired counts. The declared successor
// is never one.
func (s *structure) retiredLinkers(i int, facets kb.NodeFacets) []string {
	var linkers []string
	self := string(s.lg.IDs[i]) + "/"
	for _, u := range s.lg.Graph.In[i] { // ascending, so sorted by id
		uid := s.lg.IDs[u]
		// A satellite of an expanded concept is part of it: it links its own
		// parent, and retiring the concept retires them together.
		if strings.HasPrefix(string(uid), self) {
			continue
		}
		if u == i || retired(s.lg.Facets[u].Status) || s.kindOf(uid) != "map" || string(uid) == facets.SupersededBy {
			continue
		}
		// A digest (D322) is where archiving records what it archived: it
		// links the archived entries by design. A journal's own index is a
		// journal page, already out above.
		if kb.IsDigestType(s.lg.Facets[u].Type) {
			continue
		}
		linkers = append(linkers, string(uid))
	}
	return linkers
}

// linkToRetired is the link_to_retired check for the concept at node i, on the
// concept itself (D313).
//
// One finding per retired concept, on the retired concept: retiring a
// component is one decision, so it yields one finding, and it closes when the
// remaining mentions are plain text, not links (D907). Linkers are live concepts in a map only (an incident in a
// journal legitimately cites a component that is gone today), and the
// declared successor is never counted.
func (s *structure) linkToRetired(i int, relPath string, facets kb.NodeFacets) []Finding {
	if !retired(facets.Status) {
		return nil
	}
	linkers := s.retiredLinkers(i, facets)
	if len(linkers) == 0 {
		return nil
	}
	shown := linkers
	more := ""
	if len(shown) > 5 {
		shown, more = shown[:5], ", …"
	}
	f := newFinding("link_to_retired", Finding{Path: relPath,
		Message: fmt.Sprintf("retired (status: %s) but still linked by %d live concepts: %s%s — update the ones that rely on it; write a mention that is only history as plain text, not a link",
			facets.Status, len(linkers), strings.Join(shown, ", "), more)})
	// D366: pointing the links at the declared successor is mechanical when
	// that is a live concept, one hop away. Without one (none, unresolvable,
	// itself retired) whether a mention is historical is judgement.
	if sb := facets.SupersededBy; sb != "" && sb != string(s.lg.IDs[i]) {
		if j, ok := s.lg.Index[okf.ConceptID(sb)]; ok && !retired(s.lg.Facets[j].Status) {
			f.Fix = &Fix{Kind: FixRetargetLinks, Field: string(s.lg.IDs[i]), To: sb, Targets: linkers}
		}
	}
	return []Finding{f}
}

// islandFindings are emitted once per island, on its anchor.
func (s *structure) islandFindings() []Finding {
	var out []Finding
	for _, isl := range s.islands {
		names := make([]string, 0, 5)
		for _, m := range isl.members {
			if len(names) == 5 {
				names = append(names, "…")
				break
			}
			names = append(names, string(m))
		}
		out = append(out, newObservation("island", Finding{
			Path:    okf.IDToPath(isl.anchor),
			Message: fmt.Sprintf("%d concepts form an island disconnected from the main graph: %s", len(isl.members), strings.Join(names, ", ")),
			members: isl.members,
		}))
	}
	return out
}
