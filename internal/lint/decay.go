package lint

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/search"
)

// Lifecycle defaults (D297). A journal is a log of work, so an unfinished
// entry ages; a map holds reference pages, which are not stale by age unless
// the contract says so.
const (
	journalStaleAfterDays = 60
	facetSprawlMinValues  = 30
)

var defaultOpenMarkers = []string{"TODO", "TBD", "FIXME"}

// openPhase reports whether status means "not finished" for this map. A
// contract's open_statuses decide; otherwise the in-progress, blocked,
// proposed and draft families, open and decision-needed, and — in a journal
// only, where it means "not closed" — the active family.
func openPhase(status string, contract *kb.MapContract) bool {
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
	case "active":
		return contract != nil && contract.Kind == "journal"
	}
	return false
}

// closedPhase reports whether status means "finished".
func closedPhase(status string, contract *kb.MapContract) bool {
	fam, ok := familiesFor(contract).member(status)
	return ok && (fam == "done" || fam == "resolved")
}

var openCheckbox = regexp.MustCompile(`(?m)^\s*[-*+]\s+\[ \]\s`)

// decayFindings are the per-concept lifecycle checks (D297), all info.
func decayFindings(in conceptInput, sections []string) []Finding {
	if in.Parsed == nil {
		return nil
	}
	var out []Finding
	status, _ := frontmatterValue(in.Parsed, "status").(string)
	masked := kb.MaskCodeSpans(in.Body)

	// --- stale_open ---
	if in.Contract != nil && openPhase(status, in.Contract) {
		days := in.Contract.StaleAfterDays
		if days == 0 && in.Contract.Kind == "journal" {
			days = journalStaleAfterDays
		}
		if ts, ok := frontmatterValue(in.Parsed, "timestamp").(string); ok && days > 0 && len(ts) >= 10 {
			if t, err := time.Parse("2006-01-02", ts[:10]); err == nil {
				if age := int(Now().Sub(t).Hours() / 24); age > days {
					out = append(out, Finding{Path: in.RelPath, Check: "stale_open", Severity: SevInfo,
						Message: fmt.Sprintf("status %q and untouched for %d days (stale after %d): close it, update it, or say why it is still open", status, age, days)})
				}
			}
		}
	}

	// --- closed_with_open_items ---
	if closedPhase(status, in.Contract) {
		if n := countOpenItems(masked, in.Contract); n > 0 {
			out = append(out, Finding{Path: in.RelPath, Check: "closed_with_open_items", Severity: SevInfo,
				Message: fmt.Sprintf("status %q but %d unchecked item(s) in the body: tick them, move them, or reopen", status, n)})
		}
	}

	// --- template_section_missing ---
	if in.Contract != nil && in.Contract.TemplateSections && len(sections) > 0 {
		have := map[string]bool{}
		for _, h := range kb.H2Headings(in.Body) {
			have[foldHeading(h)] = true
		}
		var missing []string
		for _, s := range sections {
			if !have[foldHeading(s)] {
				missing = append(missing, s)
			}
		}
		if len(missing) > 0 {
			out = append(out, Finding{Path: in.RelPath, Check: "template_section_missing", Severity: SevInfo,
				Message: fmt.Sprintf("missing %d section(s) of its %q template: %s", len(missing), in.Parsed.Type(), strings.Join(missing, ", "))})
		}
	}

	// --- open_marker ---
	markers := defaultOpenMarkers
	if in.Contract != nil && len(in.Contract.OpenMarkers) > 0 {
		markers = in.Contract.OpenMarkers
	}
	if re := markerRegexp(markers); re != nil {
		// Struck-through text is closed or cancelled: a marker there is
		// history, not an open question (D307).
		unstruck := struckRe.ReplaceAllStringFunc(masked, func(m string) string { return strings.Repeat(" ", len(m)) })
		lines := strings.Split(unstruck, "\n")
		// D313: a heading is a section name ("## Todo list"), not an open
		// question; a table row of an open concept is tracking its status.
		tableRowsTracked := openPhase(status, in.Contract)
		for i, l := range lines {
			if headingLine.MatchString(l) || (tableRowsTracked && tableRowLine.MatchString(l)) {
				lines[i] = ""
			}
		}
		orig := strings.Split(in.Body, "\n")
		count, first := 0, ""
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
			out = append(out, Finding{Path: in.RelPath, Check: "open_marker", Severity: SevInfo, Count: count,
				Message: fmt.Sprintf("%d open marker(s), first: %q — answer them, or record an open_question", count, first)})
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

func foldHeading(h string) string {
	return strings.Join(strings.Fields(search.Fold(strings.ToLower(h))), " ")
}

var markerCache = map[string]*regexp.Regexp{}

// markerRegexp matches any marker as a whole word, case- and accent-folded.
func markerRegexp(markers []string) *regexp.Regexp {
	key := strings.Join(markers, "\x00")
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
// being one (D297, info, directory-level): at least 30 distinct values and at
// least half of them used once.
func facetSprawlFindings(mapName string, concepts map[okf.ConceptID]string) []Finding {
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
	if len(tags) > 10 {
		tags = tags[:10]
	}
	return []Finding{{Path: mapName + "/_map.md", Check: "facet_sprawl", Severity: SevInfo,
		Message: fmt.Sprintf("tags: %d distinct values, %d used once — a facet nobody can filter on; the most used, a likely vocabulary: %s", len(counts), single, strings.Join(tags, ", "))}}
}
