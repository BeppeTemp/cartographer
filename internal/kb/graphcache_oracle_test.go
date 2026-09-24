package kb

// The equivalence oracle for D241: verbatim copies of the uncached graph
// readers as they were before the stat-validated cache, taken from graph.go
// and graphsnapshot.go. The cached readers must return exactly what these
// return, after every step of graphcache_test.go's mutation script.

import (
	"fmt"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/graphalgo"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

func (kb *KB) oracleWalk(fn func(id okf.ConceptID, physicalPath, content string) error) error {
	files, err := kb.listMDFiles(".")
	if err != nil {
		return err
	}
	svcFiles, err := kb.oracleListServiceFiles()
	if err != nil {
		return err
	}
	files = append(files, svcFiles...)

	for _, rel := range files {
		rel = filepath.ToSlash(rel)
		base := path.Base(rel)

		if base == "index.md" {
			dir := path.Dir(rel)
			if dir == "." || len(strings.Split(dir, "/")) != 2 {
				// Root index.md and map-level index.md ("map/index.md")
				// stay reserved/excluded — only an expanded concept's
				// index.md ("map/concept/index.md") is emitted.
				continue
			}
			content, err := kb.ReadRaw(rel)
			if err != nil {
				continue
			}
			if err := fn(okf.ConceptID(dir), rel, content); err != nil {
				return err
			}
			continue
		}

		if okf.IsReserved(base) {
			continue
		}
		content, err := kb.ReadRaw(rel)
		if err != nil {
			continue
		}
		id := okf.ConceptID(strings.TrimSuffix(rel, ".md"))
		if err := fn(id, rel, content); err != nil {
			return err
		}
	}
	return nil
}

func (kb *KB) oracleListServiceFiles() ([]string, error) {
	servicesDir := filepath.Join(kb.Root, "services")
	if _, err := os.Stat(servicesDir); os.IsNotExist(err) {
		return nil, nil
	}
	var files []string
	err := filepath.WalkDir(servicesDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".md") {
			rel, _ := filepath.Rel(kb.Root, p)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	return files, err
}

func (kb *KB) oracleAdjacency() (linkAdjacency, error) {
	graph := linkAdjacency{
		out: make(map[okf.ConceptID]map[okf.ConceptID]struct{}),
		in:  make(map[okf.ConceptID]map[okf.ConceptID]struct{}),
	}
	err := kb.oracleWalk(func(id okf.ConceptID, physicalPath, content string) error {
		_, body, _ := okf.SplitFrontmatter(content)
		if graph.out[id] == nil {
			graph.out[id] = make(map[okf.ConceptID]struct{})
		}
		for _, target := range ExtractLinks(body, physicalPath, kb.AssetExists) {
			graph.out[id][target] = struct{}{}
			if graph.in[target] == nil {
				graph.in[target] = make(map[okf.ConceptID]struct{})
			}
			graph.in[target][id] = struct{}{}
		}
		return nil
	})
	return graph, err
}

func (kb *KB) oracleNeighbors(id okf.ConceptID, depth int, directions ...string) (map[string]int, error) {
	if depth <= 0 {
		depth = 1
	}
	direction := "out"
	if len(directions) > 0 && directions[0] != "" {
		direction = directions[0]
	}
	if direction != "out" && direction != "in" && direction != "both" {
		return nil, fmt.Errorf("invalid graph direction %q", direction)
	}

	graph, err := kb.oracleAdjacency()
	if err != nil {
		return nil, err
	}

	result := map[string]int{}
	frontier := []okf.ConceptID{id}

	for d := 1; d <= depth; d++ {
		var next []okf.ConceptID
		for _, cur := range frontier {
			neighbors := make(map[okf.ConceptID]struct{})
			if direction == "out" || direction == "both" {
				for neighbor := range graph.out[cur] {
					neighbors[neighbor] = struct{}{}
				}
			}
			if direction == "in" || direction == "both" {
				for neighbor := range graph.in[cur] {
					neighbors[neighbor] = struct{}{}
				}
			}
			for neighbor := range neighbors {
				if neighbor == cur || neighbor == id {
					continue
				}
				if _, seen := result[string(neighbor)]; !seen {
					result[string(neighbor)] = d
					next = append(next, neighbor)
				}
			}
		}
		frontier = next
	}

	return result, nil
}

func (kb *KB) oracleSnapshot(opts GraphSnapshotOptions) (GraphSnapshot, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultGraphNodeLimit
	}
	if limit > MaxGraphNodeLimit {
		limit = MaxGraphNodeLimit
	}
	visible := opts.Include
	if visible == nil {
		visible = func(string) bool { return true }
	}

	type conceptMeta struct {
		collection string
		title      string
		typ        string
		status     string
		expanded   bool
		targets    []okf.ConceptID
	}
	// exists holds every concept id in the KB, visible or not: it is what
	// tells a genuinely broken link from a link to a concept this caller may
	// not see. Only the first is reportable.
	exists := map[okf.ConceptID]struct{}{}
	metas := map[okf.ConceptID]*conceptMeta{}
	ids := []okf.ConceptID{}

	err := kb.oracleWalk(func(id okf.ConceptID, physicalPath, content string) error {
		exists[id] = struct{}{}
		if !visible(string(id)) {
			return nil
		}
		fmRaw, body, _ := okf.SplitFrontmatter(content)
		meta := &conceptMeta{
			collection: conceptCollection(id),
			expanded:   physicalPath == path.Join(string(id), "index.md"),
			targets:    ExtractLinks(body, physicalPath, kb.AssetExists),
		}
		// Malformed frontmatter leaves the facets empty rather than failing the
		// walk: one unparseable file must not blank the whole graph.
		if fm, err := okf.ParseFrontmatter(fmRaw); err == nil {
			meta.typ = fm.Type()
			if v, ok := fm.Get("title"); ok {
				meta.title, _ = v.(string)
			}
			if v, ok := fm.Get("status"); ok {
				meta.status, _ = v.(string)
			}
		}
		ids = append(ids, id)
		metas[id] = meta
		return nil
	})
	if err != nil {
		return GraphSnapshot{}, err
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	// Pass two resolves every link against the visible concept set. Degrees
	// are whole-KB totals: scope and limit shrink what is drawn, never what a
	// node reports about itself.
	type edgeKey struct{ source, target okf.ConceptID }
	edgeSet := map[edgeKey]struct{}{}
	selfLink := map[okf.ConceptID]bool{}
	inDegree := map[okf.ConceptID]int{}
	outDegree := map[okf.ConceptID]int{}
	var broken []BrokenTarget

	for _, id := range ids {
		seen := map[okf.ConceptID]struct{}{}
		for _, target := range metas[id].targets {
			if _, dup := seen[target]; dup {
				continue
			}
			seen[target] = struct{}{}
			switch {
			case target == id:
				selfLink[id] = true
			case metas[target] != nil:
				edgeSet[edgeKey{id, target}] = struct{}{}
				outDegree[id]++
				inDegree[target]++
			default:
				if _, hidden := exists[target]; hidden {
					// Exists but invisible to this caller: not an edge, and
					// not reportable as broken either.
					continue
				}
				broken = append(broken, BrokenTarget{Source: id, Target: target})
			}
		}
	}

	// PageRank and communities on the visible graph, from the walk's own
	// edges rather than the cached view (D244).
	index := make(map[okf.ConceptID]int, len(ids))
	for i, id := range ids {
		index[id] = i
	}
	outs := make([]map[int]bool, len(ids))
	ins := make([]map[int]bool, len(ids))
	for i := range ids {
		outs[i], ins[i] = map[int]bool{}, map[int]bool{}
	}
	for key := range edgeSet {
		outs[index[key.source]][index[key.target]] = true
		ins[index[key.target]][index[key.source]] = true
	}
	sortedKeys := func(m map[int]bool) []int {
		out := make([]int, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		sort.Ints(out)
		return out
	}
	g := &graphalgo.Graph{N: len(ids), Out: make([][]int, len(ids)), In: make([][]int, len(ids))}
	degree := make([]int, len(ids))
	for i, id := range ids {
		g.Out[i], g.In[i] = sortedKeys(outs[i]), sortedKeys(ins[i])
		degree[i] = inDegree[id] + outDegree[id]
	}
	pagerank := graphalgo.PageRank(g, 0.85, 1e-9, 100)
	communityOf, communities := graphalgo.Communities(g, 1, degree)
	prOf := func(id okf.ConceptID) float64 { return pagerank[index[id]] }

	// Scope selects the full candidate node set; the limit then cuts it.
	scoped := ids
	if opts.Scope != "" {
		scoped = scoped[:0:0]
		for _, id := range ids {
			if metas[id].collection == opts.Scope {
				scoped = append(scoped, id)
			}
		}
	}
	inScope := make(map[okf.ConceptID]struct{}, len(scoped))
	for _, id := range scoped {
		inScope[id] = struct{}{}
	}

	snap := GraphSnapshot{TotalNodes: len(scoped), Limit: limit}
	for key := range edgeSet {
		if _, ok := inScope[key.source]; !ok {
			continue
		}
		if _, ok := inScope[key.target]; !ok {
			continue
		}
		snap.TotalEdges++
	}

	snap.Communities = make([]SnapshotCommunity, len(communities))
	for i, c := range communities {
		snap.Communities[i] = SnapshotCommunity{Rank: c.Rank, Size: c.Size, Anchor: ids[c.Anchor], Slot: c.Slot}
	}
	kept := scoped
	if len(kept) > limit {
		kept = append([]okf.ConceptID(nil), scoped...)
		sort.SliceStable(kept, func(i, j int) bool {
			if prOf(kept[i]) != prOf(kept[j]) {
				return prOf(kept[i]) > prOf(kept[j])
			}
			return kept[i] < kept[j]
		})
		kept = kept[:limit]
		sort.Slice(kept, func(i, j int) bool { return kept[i] < kept[j] })
		snap.Truncated = true
	}
	inSnapshot := make(map[okf.ConceptID]struct{}, len(kept))
	snap.Nodes = make([]GraphNode, 0, len(kept))
	for _, id := range kept {
		inSnapshot[id] = struct{}{}
		snap.Nodes = append(snap.Nodes, GraphNode{
			ID:         id,
			Collection: metas[id].collection,
			Title:      metas[id].title,
			Type:       metas[id].typ,
			Status:     metas[id].status,
			Expanded:   metas[id].expanded,
			SelfLink:   selfLink[id],
			InDegree:   inDegree[id],
			OutDegree:  outDegree[id],
			PageRank:   math.Round(prOf(id)*1e6) / 1e6,
			Community:  communityOf[index[id]],
		})
	}

	snap.Edges = make([]GraphEdge, 0, len(edgeSet))
	for key := range edgeSet {
		if _, ok := inSnapshot[key.source]; !ok {
			continue
		}
		if _, ok := inSnapshot[key.target]; !ok {
			continue
		}
		snap.Edges = append(snap.Edges, GraphEdge{Source: key.source, Target: key.target})
	}
	sort.Slice(snap.Edges, func(i, j int) bool {
		if snap.Edges[i].Source != snap.Edges[j].Source {
			return snap.Edges[i].Source < snap.Edges[j].Source
		}
		return snap.Edges[i].Target < snap.Edges[j].Target
	})
	if len(snap.Edges) > MaxGraphEdges {
		weaker := func(e GraphEdge) float64 { return math.Min(prOf(e.Source), prOf(e.Target)) }
		sort.SliceStable(snap.Edges, func(i, j int) bool { return weaker(snap.Edges[i]) > weaker(snap.Edges[j]) })
		snap.Edges = snap.Edges[:MaxGraphEdges]
		sort.Slice(snap.Edges, func(i, j int) bool {
			if snap.Edges[i].Source != snap.Edges[j].Source {
				return snap.Edges[i].Source < snap.Edges[j].Source
			}
			return snap.Edges[i].Target < snap.Edges[j].Target
		})
		snap.Truncated = true
	}

	kept2 := broken[:0:0]
	for _, b := range broken {
		if _, ok := inSnapshot[b.Source]; ok {
			kept2 = append(kept2, b)
		}
	}
	sort.Slice(kept2, func(i, j int) bool {
		if kept2[i].Source != kept2[j].Source {
			return kept2[i].Source < kept2[j].Source
		}
		return kept2[i].Target < kept2[j].Target
	})
	snap.TotalBroken = len(kept2)
	if len(kept2) > MaxGraphEdges {
		kept2 = kept2[:MaxGraphEdges]
		snap.Truncated = true
	}
	snap.Broken = kept2
	return snap, nil
}
