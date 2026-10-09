package lint

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// Work is a view, not a type (D302): a work item is a concept in an open
// phase for its map (any type), or an unchecked list item in any concept
// (whatever its status). Nothing has to be retyped for the server to see it,
// and the server never interprets a KB's priority, owner or due fields: it
// returns and orders by the ones the caller names.

// workItemTextMax caps an item's text, cut at a rune boundary.
const workItemTextMax = 200

// WorkItem is one unchecked list item.
type WorkItem struct {
	Text    string `json:"text"`
	Section string `json:"section,omitempty"`
	Line    int    `json:"line"`
}

// WorkEntry is one concept that carries work.
type WorkEntry struct {
	ID        string     `json:"id"`
	Title     string     `json:"title,omitempty"`
	Type      string     `json:"type,omitempty"`
	Map       string     `json:"map,omitempty"`
	Status    string     `json:"status,omitempty"`
	OpenPhase bool       `json:"open_phase"`
	Timestamp string     `json:"timestamp,omitempty"`
	AgeDays   *int       `json:"age_days,omitempty"`
	Stale     bool       `json:"stale"`
	Items     []WorkItem `json:"items"`
	// Frontmatter is the parsed frontmatter, for the caller's where, fields
	// and order_by. Shared with the cache: read-only. Nil when unparseable.
	Frontmatter *okf.Frontmatter `json:"-"`
}

// Work collects every concept that carries work, sorted by ID. Visibility is
// the caller's: the result covers the whole KB.
func Work(k *kb.KB) ([]WorkEntry, error) {
	contracts := map[string]*kb.MapContract{}
	if archives, err := k.ListArchives(); err == nil {
		for _, a := range archives {
			if c, cerr := k.ReadMapContract(a); cerr == nil {
				c := c
				contracts[a] = &c
			}
		}
	}
	seen := map[okf.ConceptID]bool{}
	var out []WorkEntry
	err := k.WalkConceptPaths(func(id okf.ConceptID, _ string, content string) error {
		if seen[id] {
			return nil // direct form wins, as in Run
		}
		seen[id] = true
		mapName, _, _ := strings.Cut(string(id), "/")
		if !strings.Contains(string(id), "/") {
			mapName = ""
		}
		contract := contracts[mapName]
		fmRaw, body, _ := okf.SplitFrontmatter(content)
		e := WorkEntry{ID: string(id), Map: mapName, Items: workItems(body)}
		if fm, perr := okf.ParseFrontmatter(fmRaw); perr == nil && fm != nil {
			e.Frontmatter = fm
			e.Type = fm.Type()
			e.Title, _ = frontmatterValue(fm, "title").(string)
			e.Status, _ = frontmatterValue(fm, "status").(string)
			e.Timestamp, _ = frontmatterValue(fm, "timestamp").(string)
		}
		e.OpenPhase = workPhase(e.Status, contract)
		if !e.OpenPhase && len(e.Items) == 0 {
			return nil
		}
		if len(e.Timestamp) >= 10 {
			if t, terr := time.Parse("2006-01-02", e.Timestamp[:10]); terr == nil {
				age := int(Now().Sub(t).Hours() / 24)
				e.AgeDays = &age
				// The stale_open threshold (D297), extended to a concept
				// whose unchecked items are as old.
				if days, _ := EffectiveStaleAfter(contract); days > 0 && age > days && !reviewSuspended(e.Frontmatter) {
					e.Stale = true
				}
			}
		}
		out = append(out, e)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("lint.Work: walk: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// workPhase reports whether status makes a concept work: openPhase, except
// the draft family, which says a page is still being written rather than that
// something in its subject is pending. A draft rots like any open phase
// (stale_open keeps it) but is work only where a map's open_statuses lists it.
// Its unchecked items stay work whatever the status.
func workPhase(status string, contract *kb.MapContract) bool {
	if !openPhase(status, contract) {
		return false
	}
	if contract != nil && len(contract.OpenStatuses) > 0 {
		return true
	}
	fam, ok := familiesFor(contract).member(status)
	return !ok || fam != "draft"
}

// EffectiveStaleAfter is the one place that decides stale_open's threshold
// (D346): an explicit stale_after wins, 0 is off, else 60 in a journal, 30 in
// a map that holds work, none in a reference map. defaulted is true when the
// value comes from the default, never written to _map.md.
func EffectiveStaleAfter(contract *kb.MapContract) (days int, defaulted bool) {
	if contract == nil {
		return 0, false
	}
	if contract.StaleAfterDays > 0 {
		return contract.StaleAfterDays, false
	}
	if contract.StaleAfterOff {
		return 0, false
	}
	if contract.Kind == "journal" {
		return journalStaleAfterDays, true
	}
	if holdsWork(contract) {
		return workMapStaleAfterDays, true
	}
	return 0, false
}

// holdsWork reports whether a map's contract declares an open phase: the only
// contract-level signal that its pages are meant to be open or closed.
func holdsWork(contract *kb.MapContract) bool {
	if len(contract.OpenStatuses) > 0 {
		return true
	}
	for _, v := range contract.FieldValues["status"] {
		if openPhase(v, contract) {
			return true
		}
	}
	for _, fv := range contract.FieldValuesByType {
		for _, v := range fv["status"] {
			if openPhase(v, contract) {
				return true
			}
		}
	}
	return false
}

// workItems returns the unchecked items of a body outside code, each with the
// nearest preceding heading and its 1-based line in the body.
func workItems(body string) []WorkItem {
	masked := strings.Split(kb.MaskCodeSpans(body), "\n")
	orig := strings.Split(body, "\n")
	items := []WorkItem{}
	section := ""
	for i, line := range masked {
		if m := workHeading.FindStringSubmatch(line); m != nil {
			section = m[1]
			continue
		}
		if !openCheckbox.MatchString(line) || i >= len(orig) {
			continue
		}
		_, after, _ := strings.Cut(orig[i], "[ ]")
		items = append(items, WorkItem{Text: cutAtRune(strings.TrimSpace(after), workItemTextMax), Section: section, Line: i + 1})
	}
	return items
}

var workHeading = regexp.MustCompile(`^ {0,3}#{1,6}\s+(.*?)\s*#*\s*$`)

// cutAtRune cuts s to at most n bytes at a rune boundary, without marker.
func cutAtRune(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
