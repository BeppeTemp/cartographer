package lint

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/search"
)

// Lifecycle defaults (D297, D346). A journal is a log of work, so an
// unfinished entry ages; a map that holds work (an open phase in its
// contract) ages faster; a reference map is not stale by age unless the
// contract says so.
const (
	journalStaleAfterDays = 60
	workMapStaleAfterDays = 30
	facetSprawlMinValues  = 30
	// facetSprawlVocabMinUses is the total uses a cluster of tag spellings
	// needs to become a vocabulary value (D1197); below it the tag is dropped.
	facetSprawlVocabMinUses = 2
)

var defaultOpenMarkers = []string{"TODO", "TBD", "FIXME"}

// statusOf is the concept's real status value.
func statusOf(fm *okf.Frontmatter) string {
	s, _ := frontmatterValue(fm, "status").(string)
	return s
}

// effectiveState is the value that says whether a concept is open or closed
// (D347): the map's open_field when the contract names one and the concept
// carries a non-empty string there, else status. name is the key it came from,
// for messages; fromField is true when open_field supplied it.
func effectiveState(fm *okf.Frontmatter, contract *kb.MapContract) (value, name string, fromField bool) {
	if fm != nil && contract != nil && contract.OpenField != "" {
		if v, ok := frontmatterValue(fm, contract.OpenField).(string); ok && strings.TrimSpace(v) != "" {
			return v, contract.OpenField, true
		}
	}
	return statusOf(fm), "status", false
}

// openPhase reports whether the concept's state means "not finished" for this map. A
// contract's open_statuses decide; otherwise the in-progress, blocked,
// proposed and draft families, open and decision-needed. The active family is
// never open by default, in a journal or a map: active means the page is valid
// (D321); a KB that reads it as work lists it in open_statuses.
func openPhase(fm *okf.Frontmatter, contract *kb.MapContract) bool {
	status, _, fromField := effectiveState(fm, contract)
	if fromField && NormValue(statusOf(fm)) == kb.StatusArchived {
		return false // archived ends the lifecycle whatever the outcome says (D347)
	}
	return openValue(status, contract)
}

// openValue is openPhase on an already-chosen state value.
func openValue(status string, contract *kb.MapContract) bool {
	n := NormValue(status)
	if n == "" {
		return false
	}
	if contract != nil && len(contract.OpenStatuses) > 0 {
		for _, s := range contract.OpenStatuses {
			if NormValue(s) == n {
				return true
			}
		}
		return false
	}
	if n == "open" || n == "decision-needed" {
		return true
	}
	fam, ok := familiesFor(contract).member(status)
	if !ok {
		return false
	}
	switch fam {
	case "in-progress", "blocked", "proposed", "draft":
		return true
	}
	return false
}

// reviewSuspended reports whether a concept declares review_after today or
// later: its timer is suspended, it is still open but not stale (D321). A
// date before today does not suspend: the review is overdue.
func reviewSuspended(fm *okf.Frontmatter) bool {
	t, ok := reviewAfterDate(fm)
	return ok && !pastDay(t)
}

// reviewAfterDate parses review_after as the YYYY-MM-DD date reviewSuspended
// and wait_undated both read, so the two cannot drift.
func reviewAfterDate(fm *okf.Frontmatter) (time.Time, bool) {
	if fm == nil {
		return time.Time{}, false
	}
	ra, _ := frontmatterValue(fm, "review_after").(string)
	if len(ra) < 10 {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02", ra[:10])
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// closedPhase reports whether status means "finished". archived is finished
// too (D322), by the reserved word rather than a family: it is a lifecycle
// stage after done, not a synonym of it.
func closedPhase(fm *okf.Frontmatter, contract *kb.MapContract) bool {
	if NormValue(statusOf(fm)) == kb.StatusArchived {
		return true
	}
	status, _, fromField := effectiveState(fm, contract)
	if fromField {
		// D347: any non-open outcome is a finished state; requiring it in
		// the done/resolved families would hide a KB's own vocabulary.
		return !openPhase(fm, contract)
	}
	fam, ok := familiesFor(contract).member(status)
	return ok && (fam == "done" || fam == "resolved")
}

var openCheckbox = regexp.MustCompile(`(?m)^\s*[-*+]\s+\[ \]\s`)

// decayFindings are the per-concept lifecycle checks (D297).
func decayFindings(in conceptInput) []Finding {
	if in.Parsed == nil {
		return nil
	}
	var out []Finding
	status, stateName, _ := effectiveState(in.Parsed, in.Contract)
	masked := kb.MaskCodeSpans(in.Body)

	// --- stale_open ---
	if in.Contract != nil && openPhase(in.Parsed, in.Contract) && !reviewSuspended(in.Parsed) {
		days, _ := EffectiveStaleAfter(in.Contract)
		if ts, ok := frontmatterValue(in.Parsed, "timestamp").(string); ok && days > 0 && len(ts) >= 10 {
			if t, err := time.Parse("2006-01-02", ts[:10]); err == nil {
				if age := int(Now().Sub(t).Hours() / 24); age > days {
					out = append(out, newFinding("stale_open", Finding{Path: in.RelPath,
						Message: fmt.Sprintf("%s %q and untouched for %d days (stale after %d): close it, update it, or say why it is still open", stateName, status, age, days)}))
				}
			}
		}
	}

	// --- wait_undated ---
	// A waiting_on with no parseable review_after suspends nothing and never
	// comes back. A past date is stale_claim's, not this check's. A closed or
	// retired page has nothing left to wait for.
	if !emptyFrontmatterValue(frontmatterValue(in.Parsed, "waiting_on")) &&
		!closedPhase(in.Parsed, in.Contract) && !kb.IsRetired(NormValue(statusOf(in.Parsed))) {
		if _, ok := reviewAfterDate(in.Parsed); !ok {
			out = append(out, newFinding("wait_undated", Finding{Path: in.RelPath,
				Message: "waiting_on names a blocker but review_after is not a YYYY-MM-DD date, so the wait never comes back: set review_after to the day it should be looked at again, or drop waiting_on"}))
		}
	}

	// --- closed_with_open_items ---
	if closedPhase(in.Parsed, in.Contract) {
		if n := countOpenItems(masked, in.Contract); n > 0 {
			out = append(out, newFinding("closed_with_open_items", Finding{Path: in.RelPath,
				Message: fmt.Sprintf("%s %q but %d unchecked item(s) in the body: tick them, move them, or reopen", stateName, status, n)}))
		}
	}

	// --- open_marker ---
	markers := defaultOpenMarkers
	if in.Contract != nil && len(in.Contract.OpenMarkers) > 0 {
		markers = in.Contract.OpenMarkers
	}
	// A verbatim external document is not the KB's writing (#1195, D1195).
	if re := markerRegexp(markers); re != nil && !externalOrigin(in.Parsed) {
		// Struck-through text is closed or cancelled: a marker there is
		// history, not an open question (D307).
		unstruck := struckRe.ReplaceAllStringFunc(masked, func(m string) string { return strings.Repeat(" ", len(m)) })
		lines := strings.Split(unstruck, "\n")
		// D313: a heading is a section name ("## Todo list"), not an open
		// question; a table row of an open concept is tracking its status.
		tableRowsTracked := openPhase(in.Parsed, in.Contract)
		for i, l := range lines {
			if headingLine.MatchString(l) || (tableRowsTracked && tableRowLine.MatchString(l)) {
				lines[i] = ""
			}
		}
		orig := strings.Split(in.Body, "\n")
		count, first := 0, ""
		// Most pages carry no marker at all: one substring scan of the folded
		// page spares them the per-line regexp, the costliest part of a lint.
		if !containsAnyMarker(search.Fold(unstruck), markers) {
			lines = nil
		}
		for i, l := range lines {
			if n := len(re.FindAllStringIndex(search.Fold(l), -1)); n > 0 {
				count += n
				if first == "" && i < len(orig) {
					first = strings.TrimSpace(orig[i])
				}
			}
		}
		if count > 0 {
			if len([]rune(first)) > 100 {
				first = string([]rune(first)[:100]) + "…"
			}
			out = append(out, newFinding("open_marker", Finding{Path: in.RelPath, Count: count,
				Message: fmt.Sprintf("%d open marker(s), first: %q — answer them, or record an open_question; markers counted: %s (not in code, headings or struck-through text; table rows are exempt only while the page is open)", count, first, strings.Join(markers, ", "))}))
		}
	}
	return out
}

var (
	headingLine  = regexp.MustCompile(`^\s{0,3}#{1,6}\s`)
	tableRowLine = regexp.MustCompile(`^\s*\|`)
)

// procedureHeadingsOf is the map's procedure_headings contract, else the
// built-in defaults (D298).
func procedureHeadingsOf(contract *kb.MapContract) []string {
	if contract != nil && len(contract.ProcedureHeadings) > 0 {
		return contract.ProcedureHeadings
	}
	return defaultProcedureHeadings
}

// isProcedureHeading reports whether an H2 heading starts with one of headings
// (folded, prefix match), the rule promotion_candidate uses.
func isProcedureHeading(h string, headings []string) bool {
	fh := foldHeading(h)
	for _, p := range headings {
		if fp := foldHeading(p); fp != "" && strings.HasPrefix(fh, fp) {
			return true
		}
	}
	return false
}

// countOpenItems counts unchecked checkboxes outside the sections whose H2
// matches the map's procedure_headings: a procedure's checklist is a reusable
// template, not unclosed work (D313).
func countOpenItems(masked string, contract *kb.MapContract) int {
	headings := procedureHeadingsOf(contract)
	n, inProcedure := 0, false
	for _, line := range strings.Split(masked, "\n") {
		switch {
		case strings.HasPrefix(line, "## "):
			inProcedure = isProcedureHeading(strings.TrimSpace(strings.TrimRight(strings.TrimPrefix(line, "## "), "#")), headings)
		case strings.HasPrefix(line, "# "):
			inProcedure = false
		case !inProcedure && openCheckbox.MatchString(line+"\n"):
			n++
		}
	}
	return n
}

var struckRe = regexp.MustCompile(`~~[^~\n]+~~`)

// FoldHeading folds a heading for matching: case, accents and spacing.
func FoldHeading(h string) string { return foldHeading(h) }

func foldHeading(h string) string {
	return strings.Join(strings.Fields(search.Fold(strings.ToLower(h))), " ")
}

// markerCache is shared by every lint run, and runs are concurrent (one per
// request, one per KB): the mutex is what keeps it a cache and not a crash.
var (
	markerMu    sync.Mutex
	markerCache = map[string]*regexp.Regexp{}
)

// containsAnyMarker reports whether folded holds any marker as a substring:
// a necessary condition for markerRegexp to match it.
func containsAnyMarker(folded string, markers []string) bool {
	for _, m := range markers {
		if m = strings.TrimSpace(m); m != "" && strings.Contains(folded, search.Fold(strings.ToLower(m))) {
			return true
		}
	}
	return false
}

// markerRegexp matches any marker as a whole word, case- and accent-folded.
func markerRegexp(markers []string) *regexp.Regexp {
	key := strings.Join(markers, "\x00")
	markerMu.Lock()
	defer markerMu.Unlock()
	if re, ok := markerCache[key]; ok {
		return re
	}
	var alts []string
	for _, m := range markers {
		if m = strings.TrimSpace(m); m != "" {
			alts = append(alts, regexp.QuoteMeta(search.Fold(strings.ToLower(m))))
		}
	}
	if len(alts) == 0 {
		return nil
	}
	re := regexp.MustCompile(`(?i)(^|[^\pL\pN])(` + strings.Join(alts, "|") + `)($|[^\pL\pN])`)
	markerCache[key] = re
	return re
}

// facetSprawlFindings reports, on a map's _map.md, a tags facet that stopped
// being one (D297, an observation since D947, directory-level): at least 30 distinct values and at
// least half of them used once. Unless the contract already declares the tags'
// values, it carries the closed vocabulary that converges them (D1197).
func facetSprawlFindings(mapName string, contract kb.MapContract, concepts map[okf.ConceptID]string) []Finding {
	counts := map[string]int{}
	prefix := mapName + "/"
	for id, content := range concepts {
		if !strings.HasPrefix(string(id), prefix) {
			continue
		}
		fmRaw, _, ok := okf.SplitFrontmatter(content)
		if !ok {
			continue
		}
		parsed, _ := okf.ParseFrontmatter(fmRaw)
		if parsed == nil {
			continue
		}
		if tags, ok := frontmatterValue(parsed, "tags").([]string); ok {
			for _, t := range tags {
				if t = strings.TrimSpace(t); t != "" {
					counts[t]++
				}
			}
		}
	}
	if len(counts) < facetSprawlMinValues {
		return nil
	}
	single := 0
	tags := make([]string, 0, len(counts))
	for t, n := range counts {
		if n == 1 {
			single++
		}
		tags = append(tags, t)
	}
	if single*2 < len(counts) {
		return nil
	}
	sort.Slice(tags, func(i, j int) bool {
		if counts[tags[i]] != counts[tags[j]] {
			return counts[tags[i]] > counts[tags[j]]
		}
		return tags[i] < tags[j]
	})
	head := fmt.Sprintf("tags: %d distinct values, %d used once — a facet nobody can filter on", len(counts), single)
	f := Finding{Path: mapName + "/_map.md"}
	if tagsDeclared(contract) {
		f.Message = head + "; the map declares the tags' values: kb_repair invalid_field_value converges the pages"
		return []Finding{newObservation("facet_sprawl", f)}
	}
	p := facetSprawlProposal(counts)
	if p == nil {
		if len(tags) > 10 {
			tags = tags[:10]
		}
		f.Message = fmt.Sprintf("%s; the most used, a likely vocabulary: %s", head, strings.Join(tags, ", "))
		return []Finding{newObservation("facet_sprawl", f)}
	}
	// The evidence counts what the proposal does: the variants that fold onto
	// another value (a case or plural spelling), not just the distinct values.
	f.Message = fmt.Sprintf("%s; %d of them are variants that fold onto another value, %d values kept, %d dropped — declare them with map_update (field_values, value_synonyms), then kb_repair invalid_field_value; a new tag needs a map_update first",
		head, len(p.Mapping), len(p.Values), len(p.Drop))
	f.Proposal = p
	return []Finding{newObservation("facet_sprawl", f)}
}

// tagsDeclared reports whether the contract already declares values for tags,
// map-wide or for any type: the vocabulary is then the operator's.
func tagsDeclared(c kb.MapContract) bool {
	if _, ok := c.FieldValues["tags"]; ok {
		return true
	}
	for _, byField := range c.FieldValuesByType {
		if _, ok := byField["tags"]; ok {
			return true
		}
	}
	return false
}

// tagClusterKey is the normalisation key of a tag (D1197): case and accents
// folded, '-', '_' and spaces removed, then one trailing 's' dropped when the
// stem keeps at least three letters and the word does not end in "ss".
func tagClusterKey(t string) string {
	var b strings.Builder
	for _, r := range search.Fold(strings.ToLower(t)) {
		if r == '-' || r == '_' || unicode.IsSpace(r) {
			continue
		}
		b.WriteRune(r)
	}
	k := b.String()
	if strings.HasSuffix(k, "s") && !strings.HasSuffix(k, "ss") && utf8.RuneCountInString(k) > 3 {
		k = strings.TrimSuffix(k, "s")
	}
	return k
}

// facetSprawlProposal builds the closed tags vocabulary of D1197, with no edit
// distance and no co-occurrence: tags sharing a normalisation key form a
// cluster, a cluster used at least facetSprawlVocabMinUses times becomes one
// value named by its most used spelling (ties: shorter, then lexical), the
// other spellings map onto it, and a lone singleton is dropped. Nil when no
// cluster qualifies. The output is sorted: the same tags give the same bytes.
func facetSprawlProposal(counts map[string]int) *Proposal {
	clusters := map[string][]string{}
	for t := range counts {
		k := tagClusterKey(t)
		clusters[k] = append(clusters[k], t)
	}
	var values, drop []string
	mapping := map[string]string{}
	for _, members := range clusters {
		sort.Slice(members, func(i, j int) bool {
			a, b := members[i], members[j]
			if counts[a] != counts[b] {
				return counts[a] > counts[b]
			}
			if len(a) != len(b) {
				return len(a) < len(b)
			}
			return a < b
		})
		total := 0
		for _, m := range members {
			total += counts[m]
		}
		if total < facetSprawlVocabMinUses {
			drop = append(drop, members...)
			continue
		}
		values = append(values, members[0])
		for _, m := range members[1:] {
			mapping[m] = members[0]
		}
	}
	if len(values) == 0 {
		return nil
	}
	sort.Strings(values)
	sort.Strings(drop)
	if len(mapping) == 0 {
		mapping = nil
	}
	return &Proposal{Key: "field_values.tags", Field: "tags", Values: values, Mapping: mapping, Drop: drop}
}
