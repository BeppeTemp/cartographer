package lint

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// Thresholds of the D301 cost kinds, overridable per map in the contract
// (repeated_fact_min, hotspot_in_degree, hotspot_bytes).
const (
	// repeatedFactMinBytes is the shortest normalised line that counts as a
	// fact: below it a repeated line is a label, not something to keep in sync.
	repeatedFactMinBytes = 40
	// repeatedFactMinConcepts is how many concepts must carry the same line.
	repeatedFactMinConcepts = 3
	// hotspotMinInDegree and hotspotMinBytes: a concept reached by many paths
	// and expensive to read. Either alone is not a cost.
	hotspotMinInDegree = 50
	hotspotMinBytes    = 16 * 1024
	// repeatedFactEvidenceBytes caps the quoted line in the evidence.
	repeatedFactEvidenceBytes = 120
)

var tableSeparatorCell = regexp.MustCompile(`^:?-+:?$`)

// factLines returns the distinct normalised fact lines of one body: prose
// lines and table rows, trimmed with whitespace runs collapsed. Headings,
// code (fenced and inline), table header and separator rows, link-only list
// items and blockquote lines found in a template are not facts.
func factLines(body string, templateQuotes map[string]bool) map[string]bool {
	out := map[string]bool{}
	lines := strings.Split(kb.MaskCodeSpans(body), "\n")
	for i, line := range lines {
		t := strings.Join(strings.Fields(line), " ")
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if strings.HasPrefix(t, "|") {
			cells, sep := tableCells(t)
			if sep || (i+1 < len(lines) && isSeparatorRow(lines[i+1])) {
				continue
			}
			t = "| " + strings.Join(cells, " | ") + " |"
		} else if listMarkRe.MatchString(line) {
			rest := listMarkRe.ReplaceAllString(line, "")
			if strings.TrimSpace(mdLinkRe.ReplaceAllString(wikiLinkRe.ReplaceAllString(rest, ""), "")) == "" {
				continue // a link-only item is a link, not a fact
			}
		}
		if len(t) < repeatedFactMinBytes {
			continue
		}
		if strings.HasPrefix(t, ">") && templateQuotes[t] {
			continue
		}
		out[t] = true
	}
	return out
}

// tableCells splits a table row into trimmed cells and says whether it is a
// separator row (every cell dashes, optionally colon-aligned).
func tableCells(row string) ([]string, bool) {
	row = strings.TrimSpace(row)
	row = strings.TrimSuffix(strings.TrimPrefix(row, "|"), "|")
	parts := strings.Split(row, "|")
	sep := true
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
		if !tableSeparatorCell.MatchString(parts[i]) {
			sep = false
		}
	}
	return parts, sep
}

func isSeparatorRow(line string) bool {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "|") {
		return false
	}
	_, sep := tableCells(t)
	return sep
}

// templateQuoteLines are the normalised blockquote lines of the templates:
// guidance every page made from a template carries by design.
func templateQuoteLines(templates []string) map[string]bool {
	out := map[string]bool{}
	for _, body := range templates {
		for _, line := range strings.Split(body, "\n") {
			if t := strings.Join(strings.Fields(line), " "); strings.HasPrefix(t, ">") {
				out[t] = true
			}
		}
	}
	return out
}

// repeatedFactItems emits one repeated_fact per fact line found in at least
// the threshold number of concepts. The threshold is the lowest one among
// the owners' maps: a map that asked for a stricter check sees its own pages.
func repeatedFactItems(concepts []*reviewConcept, contracts map[string]kb.MapContract, templates []string) []ReviewItem {
	quotes := templateQuoteLines(templates)
	owners := map[string][]string{}
	for _, c := range concepts {
		for line := range factLines(c.body, quotes) {
			owners[line] = append(owners[line], string(c.id))
		}
	}
	threshold := func(mapName string) int {
		if n := contracts[mapName].RepeatedFactMin; n > 0 {
			return n
		}
		return repeatedFactMinConcepts
	}
	var out []ReviewItem
	for line, ids := range owners {
		min := repeatedFactMinConcepts
		for _, id := range ids {
			mapName, _, _ := strings.Cut(id, "/")
			if n := threshold(mapName); n < min {
				min = n
			}
		}
		if len(ids) < min || len(ids) < 2 {
			continue
		}
		sort.Strings(ids)
		out = append(out, ReviewItem{
			Kind:            ReviewRepeatedFact,
			Concepts:        ids,
			Evidence:        fmt.Sprintf("the same line in %d concepts: %q", len(ids), cutBytes(line, repeatedFactEvidenceBytes)),
			SuggestedAction: "keep the fact in one concept (the one whose subject it is) and link to it from the others",
			Weight:          len(ids),
		})
	}
	return out
}

// cutBytes cuts s to at most n bytes at a rune boundary.
func cutBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// readHotspotItems emits one read_hotspot per concept with many inbound links
// and a large body. In-degree is a whole-graph number, so the item is hidden
// from a caller who cannot see the whole KB.
func readHotspotItems(concepts []*reviewConcept, contracts map[string]kb.MapContract, links kb.Links) []ReviewItem {
	var out []ReviewItem
	for _, c := range concepts {
		minIn, minBytes := hotspotMinInDegree, hotspotMinBytes
		if ct := contracts[c.mapName]; ct.HotspotInDegree > 0 || ct.HotspotBytes > 0 {
			if ct.HotspotInDegree > 0 {
				minIn = ct.HotspotInDegree
			}
			if ct.HotspotBytes > 0 {
				minBytes = ct.HotspotBytes
			}
		}
		in := 0
		for src := range links.In[c.id] {
			if src != c.id {
				in++
			}
		}
		if in < minIn || len(c.body) < minBytes {
			continue
		}
		kib := len(c.body) / 1024
		out = append(out, ReviewItem{
			Kind:            ReviewReadHotspot,
			Concepts:        []string{string(c.id)},
			Evidence:        fmt.Sprintf("%d inbound links and a %d KB body: every path through it reads it whole", in, kib),
			SuggestedAction: "split detail into satellites (concept_expand) and keep this page a short summary; or turn it into an index page",
			Weight:          in * kib,
			wholeGraph:      true,
		})
	}
	return out
}
