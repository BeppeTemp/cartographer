package lint

import (
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// scopedNeighbourCap bounds how many graph neighbours of the written concepts
// ScopedCheck examines (D312). A hub linked from 500 pages must not turn one
// write into a whole-KB lint: past the cap the remaining neighbours are left to
// the next lint or gate_check. The cap is per direction (linkers, link
// targets) and applies after sorting, so the same write always examines the
// same neighbours.
const scopedNeighbourCap = 200

// ScopedCheck is the write-time structural lint (D312): the body, link and
// lightweight graph findings that concern the given concept IDs, without
// walking the KB. CheckConcept already covers the frontmatter-driven checks, so
// a write response is CheckConcept plus ScopedCheck.
//
// On each existing ID it runs broken_link, duplicate_link, bare_link_list,
// reciprocal_link_item, unknown_placeholder, forbidden_term, orphan,
// broken_relation, link_to_retired (when the concept is retired) and
// index_incomplete (its map or expanded concept opted into a curated index).
// On the neighbours it reports only what the write introduced: a broken_link
// on a page that links an ID that does not exist (a moved or deleted
// concept), and link_to_retired on a retired concept the given ID links. A
// neighbour's own pre-existing findings are never returned.
//
// The whole-KB structural analysis stays out: cut_concept, island,
// map_misfit, map_oversize, facet_sprawl and missing_value_contract need the
// whole graph and are left to lint and gate_check. lint_ignore applies, on the
// concept and on its map. The cost is one cached graph read, the written
// concepts' own files and at most scopedNeighbourCap neighbours per direction.
// Nil when there is nothing to report.
func ScopedCheck(k *kb.KB, ids []okf.ConceptID) []Finding {
	if len(ids) == 0 {
		return nil
	}
	lg, err := k.LinkGraph(nil)
	if err != nil {
		return nil
	}
	links := lg.Links
	archives, err := k.ListArchives()
	if err != nil {
		return nil
	}
	archiveSet := make(map[string]bool, len(archives))
	for _, a := range archives {
		archiveSet[a] = true
	}
	st := newStructure(k, lg, archives, nil)

	inputs := append([]okf.ConceptID(nil), ids...)
	sort.Slice(inputs, func(i, j int) bool { return inputs[i] < inputs[j] })
	isInput := make(map[okf.ConceptID]bool, len(inputs))
	for _, id := range inputs {
		isInput[id] = true
	}

	// exists mirrors runChecks: the graph's answer first, the resolver on a
	// miss (a link may name "<id>/index" or a map's own index).
	exists := func(target okf.ConceptID) bool {
		if _, ok := links.Exists[target]; ok {
			return true
		}
		_, err := k.ReadConcept(target)
		return err == nil
	}

	ignoreCache := map[okf.ConceptID]map[string]bool{}
	ignoresOf := func(id okf.ConceptID) map[string]bool {
		if set, ok := ignoreCache[id]; ok {
			return set
		}
		var set map[string]bool
		if data, err := k.ReadConcept(id); err == nil {
			if fm, _ := okf.ParseFrontmatter(data.FrontmatterRaw); fm != nil {
				set = lintIgnoreSet(fm)
			}
		}
		ignoreCache[id] = set
		return set
	}

	var findings []Finding
	emitFor := func(id okf.ConceptID) func(Finding) {
		return func(f Finding) {
			if !suppressed(f, ignoresOf(id)) {
				findings = append(findings, f)
			}
		}
	}

	registry, _ := loadRegistryLint(k)
	glossary, _ := loadGlossaryLint(k)
	bodies := map[okf.ConceptID]string{}
	readTargetBody := func(target okf.ConceptID) (string, string) {
		b, ok := bodies[target]
		if !ok {
			if data, err := k.ReadConcept(target); err == nil {
				b = data.Body
			}
			bodies[target] = b
		}
		rel, _ := k.ConceptRelPath(target)
		return b, rel
	}
	contracts := map[string]kb.MapContract{}
	contractOf := func(mapName string) (kb.MapContract, bool) {
		if c, ok := contracts[mapName]; ok {
			return c, true
		}
		if !archiveSet[mapName] {
			return kb.MapContract{}, false
		}
		c, err := k.ReadMapContract(mapName)
		if err != nil {
			return kb.MapContract{}, false
		}
		contracts[mapName] = c
		return c, true
	}

	// curated collects the written concepts per curated index to check: the
	// map's index for a top-level concept, the expanded concept's own index
	// for a satellite.
	curated := map[string][]okf.ConceptID{}
	present := func(t okf.ConceptID) bool { _, ok := links.Exists[t]; return ok }

	for _, id := range inputs {
		node, inGraph := lg.Index[id]
		if _, ok := links.Exists[id]; !ok || !inGraph {
			// Gone (moved away or deleted): what remains are the pages that
			// still link it.
			if exists(id) {
				continue
			}
			linkers := sortedIDs(links.In[id])
			if len(linkers) > scopedNeighbourCap {
				linkers = linkers[:scopedNeighbourCap]
			}
			for _, u := range linkers {
				if u == id || isInput[u] || !present(u) {
					continue
				}
				emitFor(u)(Finding{
					Path:     okf.IDToPath(u),
					Check:    "broken_link",
					Severity: SevWarning,
					Message:  "broken link to " + okf.IDToPath(id),
				})
			}
			continue
		}
		data, err := k.ReadConcept(id)
		if err != nil {
			continue
		}
		relPath := okf.IDToPath(id)
		linkBase, _ := k.ConceptRelPath(id)
		emit := emitFor(id)

		linkFindings(k, id, relPath, linkBase, data.Body, exists, links.Out, readTargetBody, emit)
		if undeclared := registry.undeclared(lg.Facets[node].Placeholders); len(undeclared) > 0 {
			emit(unknownPlaceholderFinding(relPath, undeclared))
		}
		for _, f := range forbiddenTermFindings(relPath, data.Body, glossary) {
			emit(f)
		}
		if f, ok := orphanFinding(id, relPath, links.In, links.Out, archiveSet, present); ok {
			emit(f)
		}
		for _, f := range st.brokenRelation(id, relPath, lg.Facets[node]) {
			emit(f)
		}
		for _, f := range st.linkToRetired(node, relPath, lg.Facets[node]) {
			emit(f)
		}

		// The retired concepts this one links: it is one of the linkers they
		// are reported for, so the write introduced (or kept) the finding.
		targets := lg.Graph.Out[node]
		if len(targets) > scopedNeighbourCap {
			targets = targets[:scopedNeighbourCap]
		}
		for _, ti := range targets {
			tid := lg.IDs[ti]
			if isInput[tid] || !retired(lg.Facets[ti].Status) {
				continue
			}
			linked := false
			for _, l := range st.retiredLinkers(ti, lg.Facets[ti]) {
				if l == string(id) {
					linked = true
					break
				}
			}
			if linked {
				for _, f := range st.linkToRetired(ti, okf.IDToPath(tid), lg.Facets[ti]) {
					emitFor(tid)(f)
				}
			}
		}

		parts := strings.Split(string(id), "/")
		switch {
		case len(parts) == 2:
			curated[parts[0]] = append(curated[parts[0]], id)
		case len(parts) > 2:
			curated[parts[0]+"/"+parts[1]] = append(curated[parts[0]+"/"+parts[1]], id)
		}
	}

	folders := make([]string, 0, len(curated))
	for folder := range curated {
		folders = append(folders, folder)
	}
	sort.Strings(folders)
	for _, folder := range folders {
		mapName, _, _ := strings.Cut(folder, "/")
		contract, ok := contractOf(mapName)
		if !ok || contract.Index == kb.IndexGenerated || !contract.RequireIndexEntry {
			continue
		}
		checkCuratedIndex(k, folder, folder+"/index.md", curated[folder], false, &findings, exists)
	}

	findings = applyMapIgnores(k, findings)
	out := findings[:0]
	for _, f := range findings {
		// A map naming a check it cannot accept is the map's own finding, not
		// something this write did.
		if f.Check == "lint_ignore_invalid" && strings.HasSuffix(f.Path, "/_map.md") {
			continue
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		if out[i].Check != out[j].Check {
			return out[i].Check < out[j].Check
		}
		return out[i].Message < out[j].Message
	})
	return out
}

func sortedIDs(set map[okf.ConceptID]struct{}) []okf.ConceptID {
	out := make([]okf.ConceptID, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
