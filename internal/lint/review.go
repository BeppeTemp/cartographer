package lint

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

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
	// D304: map titles that do not read as one set.
	ReviewMapNaming = "map_naming"
	// D321: an `active` page in a journal that does not declare it open.
	ReviewStatusReclassify = "status_reclassify"
	// ReviewHarvestCandidate (D322) is declared in harvest.go.
)

// ReviewKinds lists the kinds in ranking priority: an item of an earlier kind
// always comes before one of a later kind.
// Repeated facts and hotspots rank before promotion (D301): a duplicated fact
// is cheaper to fix than to keep updating in every copy.
var ReviewKinds = []string{ReviewDuplicate, ReviewTemplateProposal, ReviewZombie, ReviewHarvestCandidate, ReviewStatusReclassify, ReviewRepeatedFact, ReviewReadHotspot, ReviewPromotion, ReviewScatteredWork, ReviewMapNaming, ReviewGlossary, ReviewLintJudgement}

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
// doctor works from the same list (lint_judgement). A finding counts only
// without a mechanical fix: one with a fix is kb_repair's.
var lintJudgementChecks = checkSet(func(s CheckSpec) bool { return s.Judgement })

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

var quarterHalfRe = regexp.MustCompile(`^[qh][1-4]$`)

// isPeriodToken reports a folded token that names a period or a number in a
// series: it holds a digit (2026, 08, v2, q2) or is a quarter/half (D345).
// Month names are not included: they are language-dependent.
func isPeriodToken(t string) bool {
	return quarterHalfRe.MatchString(t) || strings.ContainsFunc(t, unicode.IsDigit)
}

// SeriesSiblings reports two concepts that are consecutive pages of one
// series ("Archive 2026 Q2" / "Archive 2026 Q3"): their titles differ only in
// period tokens and their id basenames differ only in the same positions
// (D345). Shared by duplicate_candidate and the `similar` advice on creation,
// so the two never disagree. Anything unclear is false: it stays a candidate.
func SeriesSiblings(titleA, idA, titleB, idB string) bool {
	split := func(title string) (period, rest map[string]bool) {
		period, rest = map[string]bool{}, map[string]bool{}
		for t := range TitleTokens(title) {
			if isPeriodToken(t) {
				period[t] = true
			} else {
				rest[t] = true
			}
		}
		return
	}
	pa, ra := split(titleA)
	pb, rb := split(titleB)
	if len(ra) == 0 || len(ra) != len(rb) {
		return false
	}
	for t := range ra {
		if !rb[t] {
			return false
		}
	}
	if len(pa) == len(pb) {
		same := true
		for t := range pa {
			if !pb[t] {
				same = false
			}
		}
		if same {
			return false
		}
	}
	base := func(id string) []string {
		if i := strings.LastIndex(id, "/"); i >= 0 {
			id = id[i+1:]
		}
		return strings.FieldsFunc(strings.ToLower(id), func(r rune) bool { return r == '-' || r == '_' || r == ' ' })
	}
	ba, bb := base(idA), base(idB)
	if len(ba) == 0 || len(ba) != len(bb) {
		return false
	}
	differ := false
	for i := range ba {
		if ba[i] == bb[i] {
			continue
		}
		if !isPeriodToken(ba[i]) || !isPeriodToken(bb[i]) {
			return false
		}
		differ = true
	}
	return differ
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
	// TemplateSlug, Template and MapUpdate are the payload of a
	// template_proposal item (D352): the slug and full text of the template to
	// write ("" when one of that slug already exists) and the map_update
	// arguments that bind the map to it.
	TemplateSlug string                 `json:"template_slug,omitempty"`
	Template     string                 `json:"template,omitempty"`
	MapUpdate    map[string]interface{} `json:"map_update,omitempty"`
	// wholeGraph marks an item derived from a check that reads the whole
	// graph (WholeGraphChecks): a caller who cannot see the whole KB must not
	// receive it.
	wholeGraph bool
	// dismissOnFirst: only the first concept's lint_ignore dismisses the
	// item. A group item (a shared retired origin) must not vanish because
	// one member dismissed its own, separate item.
	dismissOnFirst bool
}

type reviewConcept struct {
	id      okf.ConceptID
	mapName string
	typ     string
	title   string
	status  string
	// fm is the parsed frontmatter, for the open/closed readers (D347).
	fm *okf.Frontmatter
	// path is the KB-relative file holding the concept: the base its relative
	// links resolve against ("<id>/index.md" for an expanded concept).
	path string
	// timestamp is the frontmatter timestamp, verbatim (harvest_candidate).
	timestamp string
	resource  string
	body      string
	ignores   map[string]bool
}

// Review builds the ranked work list from the KB, its contracts and the
// whole-KB lint findings (D298). Read-only and deterministic: the same KB
// gives the same list in the same order. Items a concept dismissed with
// lint_ignore are already dropped; visibility is FilterReview's.
func Review(k *kb.KB, findings []Finding) ([]ReviewItem, error) {
	var concepts []*reviewConcept
	byID := map[okf.ConceptID]*reviewConcept{}
	if err := k.WalkConceptPaths(func(id okf.ConceptID, physicalPath string, content string) error {
		if _, dup := byID[id]; dup {
			return nil // direct form wins, as in Run
		}
		fmRaw, body, _ := okf.SplitFrontmatter(content)
		c := &reviewConcept{id: id, body: body, path: physicalPath}
		if parts := strings.Split(string(id), "/"); len(parts) > 1 {
			c.mapName = parts[0]
		}
		if parsed, _ := okf.ParseFrontmatter(fmRaw); parsed != nil {
			c.fm = parsed
			c.typ = parsed.Type()
			c.title, _ = frontmatterValue(parsed, "title").(string)
			c.status, _ = frontmatterValue(parsed, "status").(string)
			c.timestamp, _ = frontmatterValue(parsed, "timestamp").(string)
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
	var titles []mapTitle
	for _, a := range archives {
		if c, cerr := k.ReadMapContract(a); cerr == nil {
			contracts[a] = c
		}
		// A map is dismissed from map_naming by lint_ignore in its _map.md,
		// under the pseudo-ID the item names it by.
		if meta, merr := k.ReadArchiveMeta(a); merr == nil {
			t, _ := frontmatterValue(meta, "title").(string)
			titles = append(titles, mapTitle{name: a, title: strings.TrimSpace(t)})
			byID[okf.ConceptID(a+mapDescriptorSuffix)] = &reviewConcept{id: okf.ConceptID(a + mapDescriptorSuffix), ignores: lintIgnoreSet(meta)}
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
		if f.Artifact {
			continue // a file, not a concept (D316)
		}
		if id := findingConceptID(f.Path); id != "" {
			byConcept[id] = append(byConcept[id], f)
		}
	}

	var items []ReviewItem
	items = append(items, duplicateItems(concepts)...)
	items = append(items, templateProposalItems(concepts, contracts, k.TemplateCatalog())...)
	zombies, zombieItems := zombieWorkItems(k, byID, concepts, contracts, byConcept, links)
	items = append(items, zombieItems...)
	items = append(items, harvestCandidateItems(concepts, contracts)...)
	items = append(items, statusReclassifyItems(concepts, contracts)...)
	items = append(items, repeatedFactItems(concepts, contracts, k.TemplateTexts())...)
	items = append(items, readHotspotItems(concepts, contracts, links)...)
	items = append(items, promotionItems(concepts, contracts, links)...)
	items = append(items, scatteredWorkItems(concepts, contracts, links)...)
	items = append(items, mapNamingItems(titles)...)
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
	names := it.Concepts
	if it.dismissOnFirst && len(names) > 0 {
		names = names[:1]
	}
	for _, id := range names {
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

// resourceTitleJaccardMin is the title similarity two concepts sharing a
// resource need before they are duplicate candidates (D313).
const resourceTitleJaccardMin = 0.3

func duplicateItems(concepts []*reviewConcept) []ReviewItem {
	var out []ReviewItem
	paired := map[[2]okf.ConceptID]bool{}

	// (a) one type, one resource.
	type key struct{ typ, resource string }
	groups := map[key][]okf.ConceptID{}
	groupTokens := map[okf.ConceptID]map[string]bool{}
	for _, c := range concepts {
		if c.resource != "" && c.typ != "" {
			groupTokens[c.id] = TitleTokens(c.title)
			k := key{strings.ToLower(c.typ), c.resource}
			groups[k] = append(groups[k], c.id)
		}
	}
	for k, ids := range groups {
		if len(ids) < 2 {
			continue
		}
		// D313: a shared resource alone is not duplication (a dozen tasks on
		// one cluster); at least one pair must also share
		// resourceTitleJaccardMin of its title words.
		similar := false
		for i := range ids {
			for j := i + 1; j < len(ids); j++ {
				if Jaccard(groupTokens[ids[i]], groupTokens[ids[j]]) >= resourceTitleJaccardMin {
					similar = true
				}
			}
		}
		if !similar {
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
				if paired[pair] || namedAfterParent(c.id, other, titles) || SeriesSiblings(titles[c.id], string(c.id), titles[other], string(other)) {
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

// namedAfterParent reports a satellite whose title extends its parent's
// ("Plan" and "Plan — Phases 0–4"): concept_expand names parts that way, so
// the shared words are the split, not a duplicate. A satellite titled like
// its parent without extending it is still compared.
func namedAfterParent(a, b okf.ConceptID, titles map[okf.ConceptID]string) bool {
	parent, child := a, b
	if strings.HasPrefix(string(a), string(b)+"/") {
		parent, child = b, a
	} else if !strings.HasPrefix(string(b), string(a)+"/") {
		return false
	}
	pt, ct := strings.TrimSpace(titles[parent]), strings.TrimSpace(titles[child])
	return pt != "" && len(ct) > len(pt) && strings.HasPrefix(ct, pt)
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

// originHeadings are the headings (any level) whose section carries what an
// open concept came from or waits on (D345). English and Italian, matched as
// folded prefixes like the procedure headings; "prerequisit" covers the
// Italian plural. Another language, or a page that differs on purpose, uses
// lint_ignore: [zombie_work].
var originHeadings = []string{"origin", "origine", "depends on", "dipende da", "blocked by", "bloccato da", "derived from", "derivato da", "prerequisite", "prerequisit"}

var atxHeading = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*#*\s*$`)

// originSections joins the sections of masked whose heading is in
// originHeadings, each running to the next heading of the same or higher
// level.
func originSections(masked string) string {
	var out []string
	depth := 0 // level of the open origin section, 0 when none
	for _, line := range strings.Split(masked, "\n") {
		if m := atxHeading.FindStringSubmatch(line); m != nil {
			level := len(m[1])
			if depth != 0 && level <= depth {
				depth = 0
			}
			if depth == 0 && isProcedureHeading(m[2], originHeadings) {
				depth = level
			}
			continue
		}
		if depth != 0 {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// zombieSharedMin is how many open concepts linking one retired concept make
// it their common origin rather than each one's subject.
const zombieSharedMin = 3

func zombieWorkItems(k *kb.KB, byID map[okf.ConceptID]*reviewConcept, concepts []*reviewConcept, contracts map[string]kb.MapContract, byConcept map[okf.ConceptID][]Finding, links kb.Links) (map[okf.ConceptID]bool, []ReviewItem) {
	type opener struct {
		id      okf.ConceptID
		status  string
		retired []string // finding messages
		targets []string // retired concept IDs, parallel to retired
	}
	var open []opener
	linkers := map[string][]okf.ConceptID{}
	for _, c := range concepts {
		var o opener
		staleOpen := false
		// Read from the link graph, not from link_to_retired findings: those sit
		// on the retired concept (D313) and a lint_ignore there must not hide
		// open work that still points at it.
		if kind := contracts[c.mapName].Kind; c.mapName != "" && (kind == "" || kind == "map") && !retired(c.status) {
			var targets []okf.ConceptID
			// Only a link in an origin/dependency section is a dependency (D345):
			// a precedent cited in prose is history, not unfinished work.
			inOrigin := map[okf.ConceptID]bool{}
			if sec := originSections(kb.MaskCodeSpans(c.body)); sec != "" {
				base := c.path
				if base == "" {
					base = okf.IDToPath(c.id)
				}
				for _, id := range kb.ExtractLinks(sec, base, k.AssetExists) {
					inOrigin[id] = true
				}
			}
			for t := range links.Out[c.id] {
				if tc := byID[t]; tc != nil && retired(tc.status) && t != c.id && inOrigin[t] {
					targets = append(targets, t)
				}
			}
			sort.Slice(targets, func(i, j int) bool { return targets[i] < targets[j] })
			for _, t := range targets {
				o.retired = append(o.retired, fmt.Sprintf("links to retired concept %s (status: %s)", t, byID[t].status))
				o.targets = append(o.targets, string(t))
			}
		}
		for _, f := range byConcept[c.id] {
			switch f.Check {
			case "stale_open":
				staleOpen = true
			}
		}
		if len(o.retired) == 0 {
			continue
		}
		var contract *kb.MapContract
		if ct, ok := contracts[c.mapName]; ok {
			contract = &ct
		}
		if !staleOpen && !openPhase(c.fm, contract) {
			continue
		}
		o.id, o.status = c.id, c.status
		open = append(open, o)
		for _, t := range o.targets {
			if t != "" {
				linkers[t] = append(linkers[t], c.id)
			}
		}
	}

	zombies := map[okf.ConceptID]bool{}
	var out []ReviewItem
	// A retired concept many open ones link is where they came from (an old
	// backlog, a migrated page), not what each is about: one item for the
	// group, dismissed once on the retired concept, instead of one per opener
	// burying the zombies that are real.
	shared := map[string]bool{}
	var sharedTargets []string
	for t, ids := range linkers {
		if len(ids) >= zombieSharedMin {
			shared[t] = true
			sharedTargets = append(sharedTargets, t)
		}
	}
	sort.Strings(sharedTargets)
	for _, t := range sharedTargets {
		ids := idStrings(linkers[t])
		for _, id := range linkers[t] {
			zombies[id] = true
		}
		out = append(out, ReviewItem{
			Kind:            ReviewZombie,
			Concepts:        append([]string{t}, ids...),
			Evidence:        fmt.Sprintf("%d open concepts link the retired %s: more likely the place they came from than what each is about", len(ids), t),
			SuggestedAction: fmt.Sprintf("if %s is their origin, dismiss once with lint_ignore: [zombie_work] on it; otherwise close or retarget each", t),
			Weight:          len(ids),
			wholeGraph:      true,
			dismissOnFirst:  true,
		})
	}
	for _, o := range open {
		var own []string
		for i, t := range o.targets {
			if !shared[t] {
				own = append(own, o.retired[i])
			}
		}
		if len(own) == 0 {
			continue
		}
		zombies[o.id] = true
		out = append(out, ReviewItem{
			Kind:            ReviewZombie,
			Concepts:        []string{string(o.id)},
			Evidence:        fmt.Sprintf("status %q, still open, and it links to retired concepts in an origin/dependency section: %s", o.status, strings.Join(own, "; ")),
			SuggestedAction: "close it as obsolete, or retarget it at what replaced the retired concept",
			Weight:          len(own),
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
		headings := procedureHeadingsOf(&contract)
		var why []string
		weight := 0
		if n := longestNumberedRun(kb.MaskCodeSpans(c.body)); n >= promotionMinSteps {
			why = append(why, fmt.Sprintf("%d numbered steps", n))
			weight = n
		}
		for _, h := range kb.H2Headings(c.body) {
			if isProcedureHeading(h, headings) {
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

// --- status_reclassify (D321) ---

// activeNotOpen reports whether status is in the active family in a journal
// whose contract does not list it in open_statuses: the page says "valid" where
// the journal's reader reads "work". status_semantics and status_reclassify
// share it.
func activeNotOpen(fm *okf.Frontmatter, contract *kb.MapContract) bool {
	// D347: with an open_field, status is the lifecycle, not the work state.
	if contract == nil || contract.Kind != "journal" || contract.OpenField != "" {
		return false
	}
	status := statusOf(fm)
	if fam, ok := familiesFor(contract).member(status); !ok || fam != "active" {
		return false
	}
	return !openPhase(fm, contract)
}

var (
	reclassifyReferenceTypes = map[string]bool{"assessment": true, "solution": true, "analysis": true, "reference": true}
	reclassifyClosedHeads    = []string{"esito", "outcome", "chiuso", "closed"}
	reclassifyNextHeads      = []string{"prossimi passi", "next steps"}
	reclassifyWaiting        = []string{"in attesa di", "waiting for", "blocked by"}
)

// statusReclassifyItems proposes a status for every `active` page of a journal
// that does not declare active open (D321), from deterministic signals only;
// with none, the proposal is "unknown" and the operator decides.
func statusReclassifyItems(concepts []*reviewConcept, contracts map[string]kb.MapContract) []ReviewItem {
	var out []ReviewItem
	for _, c := range concepts {
		contract, ok := contracts[c.mapName]
		if !ok || !activeNotOpen(c.fm, &contract) {
			continue
		}
		proposed, signal := reclassifySignal(c)
		it := ReviewItem{
			Kind:     ReviewStatusReclassify,
			Concepts: []string{string(c.id)},
			Evidence: fmt.Sprintf("status active in journal %s; signals: %s", c.mapName, signal),
		}
		if proposed == "unknown" {
			it.SuggestedAction = "no deterministic signal — the operator decides: reference, done, in-progress, or blocked"
		} else {
			it.SuggestedAction = "set status to " + proposed
		}
		out = append(out, it)
	}
	return out
}

// reclassifySignal is the first matching signal's proposal and its name.
func reclassifySignal(c *reviewConcept) (proposed, signal string) {
	if reclassifyReferenceTypes[strings.ToLower(c.typ)] {
		return "reference", "type " + strings.ToLower(c.typ)
	}
	heads := kb.H2Headings(c.body)
	hasHead := func(want []string) bool {
		for _, h := range heads {
			if isProcedureHeading(h, want) {
				return true
			}
		}
		return false
	}
	masked := kb.MaskCodeSpans(c.body)
	switch {
	case hasHead(reclassifyClosedHeads):
		return "done", "outcome section"
	case openCheckbox.MatchString(masked+"\n") || hasHead(reclassifyNextHeads):
		return "in-progress", "unchecked items or next steps"
	}
	folded := search.Fold(strings.ToLower(masked))
	for _, w := range reclassifyWaiting {
		if strings.Contains(folded, w) {
			return "blocked", "waiting phrase"
		}
	}
	return "unknown", "none"
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
		open := openPhase(c.fm, &contract)
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
			state, name, _ := effectiveState(c.fm, &contract)
			evidence = fmt.Sprintf("%s %q", name, state)
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

// --- map_naming (D304) ---

// mapDescriptorSuffix makes a map's pseudo-ID in a map_naming item: the map
// is not a concept, and its _map.md is where lint_ignore dismisses it.
const mapDescriptorSuffix = "/_map"

// mapNamingMin is how many titled maps make a set worth comparing.
const mapNamingMin = 3

type mapTitle struct{ name, title string }

// subtitleSep is what splits a title into a name and a subtitle.
var subtitleSep = regexp.MustCompile(` [—–-] |: `)

// Thresholds of the individual-title arm of map_naming (D315): a map title
// is a short label, and a subtitle past the second is a description.
const (
	mapTitleMaxRunes    = 30
	mapSubtitleMaxRunes = 20
)

// mapNamingItems flags a KB whose map titles do not share one shape (some
// with a subtitle and some without, or Title Case beside sentence case) and,
// separately, a map whose title is poor on its own: too long, a subtitle that
// is a description, or no resemblance to its folder (D315). A consistent set
// of poor titles is still flagged. One item for the set, naming every map:
// renaming is a scheme, not a page.
func mapNamingItems(titles []mapTitle) []ReviewItem {
	var named []mapTitle
	for _, m := range titles {
		if m.title != "" {
			named = append(named, m)
		}
	}
	if len(named) < mapNamingMin {
		return nil
	}
	sub, plain, upper, lower := 0, 0, 0, 0
	for _, m := range named {
		head := m.title
		if loc := subtitleSep.FindStringIndex(head); loc != nil {
			sub++
			head = head[:loc[0]]
		} else {
			plain++
		}
		switch titleCaseStyle(head) {
		case "title":
			upper++
		case "sentence":
			lower++
		}
	}
	var why []string
	if sub > 0 && plain > 0 {
		why = append(why, fmt.Sprintf("%d with a subtitle and %d without", sub, plain))
	}
	if upper > 0 && lower > 0 {
		why = append(why, fmt.Sprintf("%d in Title Case and %d in sentence case", upper, lower))
	}
	poor := poorMapTitles(named)
	if len(why) == 0 && len(poor) == 0 {
		return nil
	}
	var evidence []string
	var ids []string
	weight := len(poor)
	if len(why) > 0 {
		list := make([]string, 0, len(named))
		for _, m := range named {
			ids = append(ids, m.name+mapDescriptorSuffix)
			list = append(list, fmt.Sprintf("%s %q", m.name, m.title))
		}
		evidence = append(evidence, fmt.Sprintf("map titles mix %s: %s", strings.Join(why, ", "), strings.Join(list, "; ")))
		weight = len(named)
	}
	if len(poor) > 0 {
		list := make([]string, 0, len(poor))
		for _, p := range poor {
			if len(why) == 0 {
				ids = append(ids, p.name+mapDescriptorSuffix)
			}
			list = append(list, fmt.Sprintf("%s %q: %s", p.name, p.title, p.reason))
		}
		evidence = append(evidence, "poor map titles: "+strings.Join(list, "; "))
	}
	return []ReviewItem{{
		Kind:            ReviewMapNaming,
		Concepts:        ids,
		Evidence:        strings.Join(evidence, " — "),
		SuggestedAction: "agree a scheme with the operator: one language, one shape (a short name; a description belongs in the index body), one capitalisation, each title recognisable from its folder; then rename with map_update title",
		Weight:          weight,
		// Titles of maps a restricted caller cannot see stay out of its list.
		wholeGraph: true,
	}}
}

type poorTitle struct{ name, title, reason string }

// poorMapTitles lists the map titles that are individually off, each with
// every reason (D315). The folder mismatch is a signal, not a rule: a title
// in another language than its folder is the agent's to judge.
func poorMapTitles(named []mapTitle) []poorTitle {
	var out []poorTitle
	for _, m := range named {
		var reasons []string
		if n := len([]rune(m.title)); n > mapTitleMaxRunes {
			reasons = append(reasons, fmt.Sprintf("%d characters; map titles are 1-3 words", n))
		}
		head := m.title
		if loc := subtitleSep.FindStringIndex(m.title); loc != nil {
			head = m.title[:loc[0]]
			if rest := strings.TrimSpace(m.title[loc[1]:]); len([]rune(rest)) > mapSubtitleMaxRunes {
				reasons = append(reasons, fmt.Sprintf("a %d-character subtitle is a description, not a qualifier", len([]rune(rest))))
			}
		}
		if i := strings.Index(head, " ("); i >= 0 {
			head = head[:i]
		}
		if slug, folder := slugOf(head), slugOf(m.name); slug != "" && !strings.HasPrefix(slug, folder) && !strings.HasPrefix(folder, slug) {
			reasons = append(reasons, fmt.Sprintf("does not match folder %q", m.name))
		}
		if len(reasons) > 0 {
			out = append(out, poorTitle{m.name, m.title, strings.Join(reasons, ", ")})
		}
	}
	return out
}

// slugOf lowercases s, turns spaces into hyphens and drops what is neither a
// letter, a digit nor a hyphen.
func slugOf(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('-')
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// titleCaseStyle is "title" when every later word of four letters or more
// starts upper-case, "sentence" when every one starts lower-case, else "".
// Acronyms and short words (articles, prepositions) say nothing either way.
func titleCaseStyle(s string) string {
	words := strings.Fields(s)
	upper, lower := 0, 0
	for _, w := range words[min(1, len(words)):] {
		r := []rune(w)
		if len(r) < 4 || strings.ToUpper(w) == w {
			continue
		}
		switch {
		case unicode.IsUpper(r[0]):
			upper++
		case unicode.IsLower(r[0]):
			lower++
		}
	}
	switch {
	case upper > 0 && lower == 0:
		return "title"
	case lower > 0 && upper == 0:
		return "sentence"
	}
	return ""
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

// commonTechTerms are acronyms every technical reader knows: a glossary entry
// for them defines nothing (D307). A KB's own terms are what glossary_gap is
// for. Compared folded.
var commonTechTerms = map[string]bool{}

func init() {
	for _, t := range strings.Fields(`API HTTP HTTPS IP URL URI JSON YAML XML HTML CSS CLI UI UX
		SSH DNS LAN WAN VPN TCP UDP TLS SSL CPU GPU RAM SSD HDD NVMe USB GB MB KB TB GiB MiB KiB TiB
		DB SQL ID OK PR CI CD SDK REST PC VM AI LLM UTC TTL OS PDF CSV IoT WiFi FAQ TODO README`) {
		commonTechTerms[search.Fold(t)] = true
	}
}

// shoutMinLen: an all-caps word of at least this length whose lowercase form
// is an ordinary word in as many concepts is emphasis ("NON"), not a term.
// Shorter ones stay terms: two letters ("HA") are acronyms in any language.
const shoutMinLen = 3

func glossaryItems(concepts []*reviewConcept, contracts map[string]kb.MapContract, glossary kb.Glossary) []ReviewItem {
	known := map[string]bool{}
	for t := range commonTechTerms {
		known[t] = true
	}
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
	lowerUse := map[string]int{} // concepts using a word in lower case
	for _, c := range concepts {
		text := urlRe.ReplaceAllString(kb.MaskCodeSpans(c.body), " ")
		terms := map[string]bool{}
		lower := map[string]bool{}
		for _, w := range wordRe.FindAllString(text, -1) {
			if acronymRe.MatchString(w) || mixedCaseRe.MatchString(w) {
				terms[w] = true
			} else if w == strings.ToLower(w) {
				lower[w] = true
			}
		}
		for w := range lower {
			lowerUse[w]++
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
		if len(t) >= shoutMinLen && acronymRe.MatchString(t) && lowerUse[strings.ToLower(t)] >= len(ids) {
			continue // emphasis of an ordinary word, not a term
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

// suggestedLintAction is what the doctor is told to do with a judgement
// finding: an error cannot be accepted (D306), so it is fixed.
func suggestedLintAction(check string) string {
	if CheckAcceptability(check) == AcceptNone {
		return "fix it by hand: an error cannot be accepted with lint_ignore"
	}
	return "judge the finding: act on it, or record why with lint_ignore and a reason"
}

func lintJudgementItems(findings []Finding, zombies map[okf.ConceptID]bool) []ReviewItem {
	var out []ReviewItem
	for _, f := range findings {
		if !lintJudgementChecks[f.Check] || f.Fix != nil {
			continue
		}
		id := findingConceptID(f.Path)
		if f.Check == "stray_file" {
			id = okf.ConceptID(f.Path) // a file, named as it is: the doctor decides what to do with it
		}
		if id == "" || f.Artifact {
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
			SuggestedAction: suggestedLintAction(f.Check),
			Weight:          severityRank(f.Severity),
			wholeGraph:      WholeGraphChecks[f.Check],
		})
	}
	return out
}
