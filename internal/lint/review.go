package lint

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/search"
)

// Review kinds (D298): the candidate classes kb_review ranks for the doctor.
// The server never decides any of them (D14): it builds the list, the agent
// judges, the operator confirms. Each kind is also a lint_ignore name, so a
// concept dismisses an item that names it without new state (D159).
const (
	ReviewDuplicate     = "duplicate_candidate"
	ReviewZombie        = "zombie_work"
	ReviewPromotion     = "promotion_candidate"
	ReviewGlossary      = "glossary_gap"
	ReviewLintJudgement = "lint_judgement"
	// D301: the structural causes of read and write fan-out.
	ReviewRepeatedFact = "repeated_fact"
	ReviewReadHotspot  = "read_hotspot"
	// D302: work kept outside the map the KB says work belongs in.
	ReviewScatteredWork = "scattered_work"
)

// ReviewKinds lists the kinds in ranking priority: an item of an earlier kind
// always comes before one of a later kind.
// Repeated facts and hotspots rank before promotion (D301): a duplicated fact
// is cheaper to fix than to keep updating in every copy.
var ReviewKinds = []string{ReviewDuplicate, ReviewZombie, ReviewRepeatedFact, ReviewReadHotspot, ReviewPromotion, ReviewScatteredWork, ReviewGlossary, ReviewLintJudgement}

// Thresholds of the review generators.
const (
	// TitleJaccardMin is the folded title-token Jaccard at or above which two
	// titles are near-duplicates: duplicate_candidate in one map, and the
	// `similar` advice on creation. Two titles that share a single word never
	// reach it unless both are that one word (1/2 = 0.5 < 0.6).
	TitleJaccardMin = 0.6
	// promotionMinSteps is the run of numbered list items that makes a journal
	// entry hold a procedure.
	promotionMinSteps = 5
	// glossaryMinConcepts is how many concepts must use an undefined term.
	glossaryMinConcepts = 10
)

// defaultProcedureHeadings are the H2 prefixes that mark a procedure when the
// contract declares none. English only: a KB in another language declares
// its own with procedure_headings.
var defaultProcedureHeadings = []string{"procedure", "steps", "how to"}

// lintJudgementChecks are the lint findings that need judgement and that the
// doctor works from the same list (lint_judgement). broken_link counts only
// without a mechanical fix: one with a fix is kb_repair's.
var lintJudgementChecks = map[string]bool{
	"stale_open":               true,
	"closed_with_open_items":   true,
	"template_section_missing": true,
	"bare_link_list":           true,
	"map_misfit":               true,
	"concept_oversize":         true,
	"broken_link":              true,
}

// titleStopWords are dropped before comparing titles: words that carry no
// subject. A short English list; a KB's own language only lowers recall.
var titleStopWords = map[string]bool{
	"a": true, "an": true, "the": true, "of": true, "and": true, "or": true, "for": true,
	"to": true, "in": true, "on": true, "with": true, "by": true, "at": true, "from": true,
	"is": true, "as": true, "how": true, "vs": true,
}

// TitleTokens is a title's folded token set with stop-words dropped (D298).
func TitleTokens(title string) map[string]bool {
	out := map[string]bool{}
	for _, t := range search.Tokenize(title) {
		if !titleStopWords[t] {
			out[t] = true
		}
	}
	return out
}

// Jaccard is |a∩b| / |a∪b|, 0 when both are empty.
func Jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	inter := 0
	for t := range a {
		if b[t] {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}

// ReviewItem is one entry of the doctor's work list.
type ReviewItem struct {
	Kind            string   `json:"kind"`
	Concepts        []string `json:"concepts"`
	Evidence        string   `json:"evidence"`
	SuggestedAction string   `json:"suggested_action"`
	// Check is the source lint check of a lint_judgement item.
	Check string `json:"check,omitempty"`
	// Term is the undefined term of a glossary_gap item.
	Term string `json:"term,omitempty"`
	// Weight orders items of one kind, heavier first.
	Weight int `json:"weight"`
	// wholeGraph marks an item derived from a check that reads the whole
	// graph (WholeGraphChecks): a caller who cannot see the whole KB must not
	// receive it.
	wholeGraph bool
}

type reviewConcept struct {
	id       okf.ConceptID
	mapName  string
	typ      string
	title    string
	status   string
	resource string
	body     string
	ignores  map[string]bool
}

// Review builds the ranked work list from the KB, its contracts and the
// whole-KB lint findings (D298). Read-only and deterministic: the same KB
// gives the same list in the same order. Items a concept dismissed with
// lint_ignore are already dropped; visibility is FilterReview's.
func Review(k *kb.KB, findings []Finding) ([]ReviewItem, error) {
	var concepts []*reviewConcept
	byID := map[okf.ConceptID]*reviewConcept{}
	if err := k.WalkConceptPaths(func(id okf.ConceptID, _ string, content string) error {
		if _, dup := byID[id]; dup {
			return nil // direct form wins, as in Run
		}
		fmRaw, body, _ := okf.SplitFrontmatter(content)
		c := &reviewConcept{id: id, body: body}
		if parts := strings.Split(string(id), "/"); len(parts) > 1 {
			c.mapName = parts[0]
		}
		if parsed, _ := okf.ParseFrontmatter(fmRaw); parsed != nil {
			c.typ = parsed.Type()
			c.title, _ = frontmatterValue(parsed, "title").(string)
			c.status, _ = frontmatterValue(parsed, "status").(string)
			c.resource, _ = frontmatterValue(parsed, "resource").(string)
			c.resource = strings.TrimSpace(c.resource)
			c.ignores = lintIgnoreSet(parsed)
		}
		concepts = append(concepts, c)
		byID[id] = c
		return nil
	}); err != nil {
		return nil, fmt.Errorf("lint.Review: walk: %w", err)
	}
	sort.Slice(concepts, func(i, j int) bool { return concepts[i].id < concepts[j].id })

	contracts := map[string]kb.MapContract{}
	archives, err := k.ListArchives()
	if err != nil {
		return nil, fmt.Errorf("lint.Review: list archives: %w", err)
	}
	for _, a := range archives {
		if c, cerr := k.ReadMapContract(a); cerr == nil {
			contracts[a] = c
		}
	}
	links, err := k.Links()
	if err != nil {
		return nil, fmt.Errorf("lint.Review: links: %w", err)
	}
	var glossary kb.Glossary
	if st, gerr := k.ReadGlossary(); gerr == nil {
		glossary = st.Glossary
	}

	// Findings by concept ID.
	byConcept := map[okf.ConceptID][]Finding{}
	for _, f := range findings {
		if id := findingConceptID(f.Path); id != "" {
			byConcept[id] = append(byConcept[id], f)
		}
	}

	var items []ReviewItem
	items = append(items, duplicateItems(concepts)...)
	zombies, zombieItems := zombieWorkItems(concepts, contracts, byConcept)
	items = append(items, zombieItems...)
	items = append(items, repeatedFactItems(concepts, contracts, k.TemplateTexts())...)
	items = append(items, readHotspotItems(concepts, contracts, links)...)
	items = append(items, promotionItems(concepts, contracts, links)...)
	items = append(items, scatteredWorkItems(concepts, contracts, links)...)
	items = append(items, glossaryItems(concepts, contracts, glossary)...)
	items = append(items, lintJudgementItems(findings, zombies)...)

	// Dismissal (D159): lint_ignore: [kind] on a concept the item names.
	kept := items[:0]
	for _, it := range items {
		if !dismissed(it, byID) {
			kept = append(kept, it)
		}
	}
	SortReview(kept)
	return kept, nil
}

func dismissed(it ReviewItem, byID map[okf.ConceptID]*reviewConcept) bool {
	for _, id := range it.Concepts {
		if c := byID[okf.ConceptID(id)]; c != nil && c.ignores[it.Kind] {
			return true
		}
	}
	return false
}

// SortReview orders items by kind priority, then weight (heavier first), then
// the first concept ID and the evidence, so pagination is stable.
func SortReview(items []ReviewItem) {
	rank := map[string]int{}
	for i, k := range ReviewKinds {
		rank[k] = i
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if rank[a.Kind] != rank[b.Kind] {
			return rank[a.Kind] < rank[b.Kind]
		}
		if a.Weight != b.Weight {
			return a.Weight > b.Weight
		}
		af, bf := strings.Join(a.Concepts, "\x00"), strings.Join(b.Concepts, "\x00")
		if af != bf {
			return af < bf
		}
		return a.Evidence < b.Evidence
	})
}

// FilterReview keeps the items a caller may see: every concept an item names
// must be visible, and a whole-graph item needs the whole KB. A glossary_gap
// counts only visible concepts, and drops below its threshold.
func FilterReview(items []ReviewItem, visible func(id string) bool, whole bool) []ReviewItem {
	out := make([]ReviewItem, 0, len(items))
	for _, it := range items {
		if it.wholeGraph && !whole {
			continue
		}
		if it.Kind == ReviewGlossary {
			var vis []string
			for _, id := range it.Concepts {
				if visible(id) {
					vis = append(vis, id)
				}
			}
			if len(vis) < glossaryMinConcepts {
				continue
			}
			if len(vis) != len(it.Concepts) {
				it.Concepts = vis
				it.Weight = len(vis)
				it.Evidence = glossaryEvidence(it.Term, len(vis))
			}
			out = append(out, it)
			continue
		}
		ok := true
		for _, id := range it.Concepts {
			if !visible(id) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, it)
		}
	}
	return out
}

// findingConceptID maps a finding's path to the concept it is on, or "" for
// a directory- or KB-level finding.
func findingConceptID(path string) okf.ConceptID {
	p := strings.ReplaceAll(path, "\\", "/")
	base := p
	if i := strings.LastIndex(p, "/"); i >= 0 {
		base = p[i+1:]
	}
	if base == "_map.md" || base == "_archive.md" || base == "index.md" || base == "log.md" || !strings.Contains(p, "/") {
		return ""
	}
	return okf.ConceptID(strings.TrimSuffix(p, ".md"))
}

// --- duplicate_candidate ---

func duplicateItems(concepts []*reviewConcept) []ReviewItem {
	var out []ReviewItem
	paired := map[[2]okf.ConceptID]bool{}

	// (a) one type, one resource.
	type key struct{ typ, resource string }
	groups := map[key][]okf.ConceptID{}
	for _, c := range concepts {
		if c.resource != "" && c.typ != "" {
			k := key{strings.ToLower(c.typ), c.resource}
			groups[k] = append(groups[k], c.id)
		}
	}
	for k, ids := range groups {
		if len(ids) < 2 {
			continue
		}
		for i := range ids {
			for j := i + 1; j < len(ids); j++ {
				paired[[2]okf.ConceptID{ids[i], ids[j]}] = true
			}
		}
		names := idStrings(ids)
		out = append(out, ReviewItem{
			Kind:            ReviewDuplicate,
			Concepts:        names,
			Evidence:        fmt.Sprintf("%d concepts of type %s document the same resource %q", len(ids), k.typ, k.resource),
			SuggestedAction: duplicateAction(ids),
			Weight:          100 + len(ids),
		})
	}

	// (b) near-identical titles in one map, through an inverted token index
	// so a large map is not compared pairwise.
	tokens := map[okf.ConceptID]map[string]bool{}
	byMapToken := map[string]map[string][]okf.ConceptID{}
	for _, c := range concepts {
		if c.mapName == "" || c.title == "" {
			continue
		}
		ts := TitleTokens(c.title)
		if len(ts) == 0 {
			continue
		}
		tokens[c.id] = ts
		if byMapToken[c.mapName] == nil {
			byMapToken[c.mapName] = map[string][]okf.ConceptID{}
		}
		for t := range ts {
			byMapToken[c.mapName][t] = append(byMapToken[c.mapName][t], c.id)
		}
	}
	titles := map[okf.ConceptID]string{}
	for _, c := range concepts {
		titles[c.id] = c.title
	}
	for _, c := range concepts {
		ts := tokens[c.id]
		if ts == nil {
			continue
		}
		seen := map[okf.ConceptID]bool{}
		for t := range ts {
			for _, other := range byMapToken[c.mapName][t] {
				if other <= c.id || seen[other] {
					continue
				}
				seen[other] = true
				pair := [2]okf.ConceptID{c.id, other}
				if paired[pair] {
					continue
				}
				j := Jaccard(ts, tokens[other])
				if j < TitleJaccardMin {
					continue
				}
				paired[pair] = true
				out = append(out, ReviewItem{
					Kind:            ReviewDuplicate,
					Concepts:        []string{string(c.id), string(other)},
					Evidence:        fmt.Sprintf("titles %q and %q share %.0f%% of their words in map %s", titles[c.id], titles[other], j*100, c.mapName),
					SuggestedAction: duplicateAction(pair[:]),
					Weight:          int(j * 100),
				})
			}
		}
	}
	return out
}

func duplicateAction(ids []okf.ConceptID) string {
	if len(ids) == 2 {
		a, b := string(ids[0]), string(ids[1])
		if strings.HasPrefix(a, b+"/") && strings.Count(a, "/") == 2 {
			return fmt.Sprintf("concept_merge %s into its parent %s", a, b)
		}
		if strings.HasPrefix(b, a+"/") && strings.Count(b, "/") == 2 {
			return fmt.Sprintf("concept_merge %s into its parent %s", b, a)
		}
	}
	return "merge them into one concept, or link them saying how they differ"
}

func idStrings(ids []okf.ConceptID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = string(id)
	}
	sort.Strings(out)
	return out
}

// --- zombie_work ---

func zombieWorkItems(concepts []*reviewConcept, contracts map[string]kb.MapContract, byConcept map[okf.ConceptID][]Finding) (map[okf.ConceptID]bool, []ReviewItem) {
	zombies := map[okf.ConceptID]bool{}
	var out []ReviewItem
	for _, c := range concepts {
		var retired []string
		staleOpen := false
		for _, f := range byConcept[c.id] {
			switch f.Check {
			case "link_to_retired":
				retired = append(retired, f.Message)
			case "stale_open":
				staleOpen = true
			}
		}
		if len(retired) == 0 {
			continue
		}
		var contract *kb.MapContract
		if ct, ok := contracts[c.mapName]; ok {
			contract = &ct
		}
		if !staleOpen && !openPhase(c.status, contract) {
			continue
		}
		zombies[c.id] = true
		out = append(out, ReviewItem{
			Kind:            ReviewZombie,
			Concepts:        []string{string(c.id)},
			Evidence:        fmt.Sprintf("status %q, still open, and it links to retired concepts: %s", c.status, strings.Join(retired, "; ")),
			SuggestedAction: "close it as obsolete, or retarget it at what replaced the retired concept",
			Weight:          len(retired),
			wholeGraph:      true,
		})
	}
	return zombies, out
}

// --- promotion_candidate ---

var numberedItem = regexp.MustCompile(`^\s{0,3}\d{1,3}[.)]\s+\S`)

// longestNumberedRun is the longest run of numbered list items; blank,
// masked and indented continuation lines do not break it.
func longestNumberedRun(masked string) int {
	best, run := 0, 0
	for _, l := range strings.Split(masked, "\n") {
		switch {
		case numberedItem.MatchString(l):
			run++
			if run > best {
				best = run
			}
		case strings.TrimSpace(l) == "", strings.HasPrefix(l, " "), strings.HasPrefix(l, "\t"):
		default:
			run = 0
		}
	}
	return best
}

func promotionItems(concepts []*reviewConcept, contracts map[string]kb.MapContract, links kb.Links) []ReviewItem {
	var out []ReviewItem
	for _, c := range concepts {
		contract, ok := contracts[c.mapName]
		if !ok || contract.PromoteTo == "" || contract.PromoteTo == c.mapName {
			continue
		}
		headings := defaultProcedureHeadings
		if len(contract.ProcedureHeadings) > 0 {
			headings = contract.ProcedureHeadings
		}
		var why []string
		weight := 0
		if n := longestNumberedRun(kb.MaskCodeSpans(c.body)); n >= promotionMinSteps {
			why = append(why, fmt.Sprintf("%d numbered steps", n))
			weight = n
		}
		for _, h := range kb.H2Headings(c.body) {
			fh := foldHeading(h)
			matched := false
			for _, p := range headings {
				if fp := foldHeading(p); fp != "" && strings.HasPrefix(fh, fp) {
					matched = true
					break
				}
			}
			if matched {
				why = append(why, fmt.Sprintf("heading %q", h))
				weight += promotionMinSteps
				break
			}
		}
		if len(why) == 0 {
			continue
		}
		linked := false
		for target := range links.Out[c.id] {
			if strings.HasPrefix(string(target), contract.PromoteTo+"/") {
				linked = true
				break
			}
		}
		if linked {
			continue
		}
		out = append(out, ReviewItem{
			Kind:            ReviewPromotion,
			Concepts:        []string{string(c.id)},
			Evidence:        fmt.Sprintf("a procedure (%s) and no link into %s, where the contract promotes procedures", strings.Join(why, ", "), contract.PromoteTo),
			SuggestedAction: fmt.Sprintf("create the procedure in %s from that map's template (template_list, concept_new), move the steps there, and link both ways", contract.PromoteTo),
			Weight:          weight,
		})
	}
	return out
}

// --- scattered_work (D302) ---

// scatteredWorkItems flags work in a map whose contract names a work_map:
// unchecked items or an open-phase status, and no link into that map. A KB
// that keeps its work in journals sets no work_map and gets no item.
func scatteredWorkItems(concepts []*reviewConcept, contracts map[string]kb.MapContract, links kb.Links) []ReviewItem {
	var out []ReviewItem
	for _, c := range concepts {
		contract, ok := contracts[c.mapName]
		if !ok || contract.WorkMap == "" || contract.WorkMap == c.mapName {
			continue
		}
		items := workItems(c.body)
		open := openPhase(c.status, &contract)
		if len(items) == 0 && !open {
			continue
		}
		linked := false
		for target := range links.Out[c.id] {
			if strings.HasPrefix(string(target), contract.WorkMap+"/") {
				linked = true
				break
			}
		}
		if linked {
			continue
		}
		var evidence string
		if len(items) > 0 {
			evidence = fmt.Sprintf("%d unchecked item(s)", len(items))
			if items[0].Section != "" {
				evidence += fmt.Sprintf(", first under %q", items[0].Section)
			}
		} else {
			evidence = fmt.Sprintf("status %q", c.status)
		}
		out = append(out, ReviewItem{
			Kind:            ReviewScatteredWork,
			Concepts:        []string{string(c.id)},
			Evidence:        fmt.Sprintf("%s and no link into %s, where the contract keeps this map's work", evidence, contract.WorkMap),
			SuggestedAction: fmt.Sprintf("create a concept in %s from its template for this work (one per independent item or one for the checklist), link both ways, and replace the items with the link", contract.WorkMap),
			Weight:          len(items),
		})
	}
	return out
}

// --- glossary_gap ---

var (
	wordRe      = regexp.MustCompile(`[A-Za-z][A-Za-z0-9]*`)
	acronymRe   = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,7}$`)
	mixedCaseRe = regexp.MustCompile(`^[A-Z][a-z]+[A-Z]\w*$`)
)

func glossaryEvidence(term string, n int) string {
	return fmt.Sprintf("%q is used in %d concepts and defined nowhere: not in glossary.yaml, not in a glossary map", term, n)
}

func glossaryItems(concepts []*reviewConcept, contracts map[string]kb.MapContract, glossary kb.Glossary) []ReviewItem {
	known := map[string]bool{}
	for _, t := range glossary.Terms {
		for _, s := range append(append([]string{t.Canonical}, t.Aliases...), t.Forbidden...) {
			known[search.Fold(strings.TrimSpace(s))] = true
		}
	}
	for _, m := range defaultOpenMarkers {
		known[search.Fold(m)] = true
	}
	defined := map[string]bool{} // terms some glossary-map concept uses
	users := map[string][]string{}
	for _, c := range concepts {
		text := urlRe.ReplaceAllString(kb.MaskCodeSpans(c.body), " ")
		terms := map[string]bool{}
		for _, w := range wordRe.FindAllString(text, -1) {
			if acronymRe.MatchString(w) || mixedCaseRe.MatchString(w) {
				terms[w] = true
			}
		}
		inGlossaryMap := contracts[c.mapName].Glossary && c.mapName != ""
		for t := range terms {
			if inGlossaryMap {
				defined[t] = true
				continue
			}
			if c.ignores[ReviewGlossary] {
				continue // the concept opts out of the count (D298)
			}
			users[t] = append(users[t], string(c.id))
		}
	}
	var out []ReviewItem
	for t, ids := range users {
		if len(ids) < glossaryMinConcepts || defined[t] || known[search.Fold(t)] {
			continue
		}
		sort.Strings(ids)
		out = append(out, ReviewItem{
			Kind:            ReviewGlossary,
			Concepts:        ids,
			Term:            t,
			Evidence:        glossaryEvidence(t, len(ids)),
			SuggestedAction: "define it: a glossary.yaml term (with its aliases) or a page in the KB's glossary map",
			Weight:          len(ids),
		})
	}
	return out
}

// --- lint_judgement ---

func lintJudgementItems(findings []Finding, zombies map[okf.ConceptID]bool) []ReviewItem {
	var out []ReviewItem
	for _, f := range findings {
		if !lintJudgementChecks[f.Check] || (f.Check == "broken_link" && f.Fix != nil) {
			continue
		}
		id := findingConceptID(f.Path)
		if id == "" {
			continue // a map index or descriptor: not a concept the doctor opens
		}
		if f.Check == "stale_open" && zombies[id] {
			continue // already a zombie_work item
		}
		out = append(out, ReviewItem{
			Kind:            ReviewLintJudgement,
			Concepts:        []string{string(id)},
			Check:           f.Check,
			Evidence:        f.Check + ": " + f.Message,
			SuggestedAction: "judge the finding: act on it, or record why with lint_ignore and a reason",
			Weight:          severityRank(f.Severity),
			wholeGraph:      WholeGraphChecks[f.Check],
		})
	}
	return out
}
