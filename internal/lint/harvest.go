package lint

import (
	"fmt"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// ReviewHarvestCandidate (D322) is a closed journal entry that has rested long
// enough to be harvested: its durable facts carried into the live pages it
// links, then the entry archived. The server only finds them (D14); what is
// durable is the agent's judgement (D305).
const ReviewHarvestCandidate = "harvest_candidate"

// ReviewLessonUnpromoted (D1078) is an archived journal entry whose lessons
// were never carried: no live page of a non-journal map links it and it does
// not say it has none. harvest_candidate covers the closed entries not yet
// archived, so the two kinds are disjoint by status.
const ReviewLessonUnpromoted = "lesson_unpromoted"

// harvestHeadings are the H2 prefixes (folded, English and Italian) that
// usually hold a fact worth carrying into a live page. A hint for the doctor,
// not a verdict: an entry with none is still a candidate.
var harvestHeadings = []string{
	"cause", "causa", "root cause", "lessons", "lezioni", "diagnostics", "diagnostica",
	"useful commands", "comandi utili", "notes", "note", "resolution", "risoluzione",
	"fix", "workaround", "soluzione",
}

// harvestCandidateItems lists, per journal, the entries whose status is
// closed (and not already archived) and whose timestamp is at least the
// contract's harvest_after days old (default 45). An entry with no usable
// timestamp is not a candidate: its age is unknown.
func harvestCandidateItems(concepts []*reviewConcept, contracts map[string]kb.MapContract) []ReviewItem {
	var out []ReviewItem
	today := Now().Truncate(24 * time.Hour)
	for _, c := range concepts {
		contract, ok := contracts[c.mapName]
		if !ok || contract.Kind != "journal" {
			continue
		}
		if NormValue(c.status) == kb.StatusArchived || !closedPhase(c.fm, &contract) {
			continue
		}
		if len(c.timestamp) < 10 {
			continue
		}
		t, err := time.Parse("2006-01-02", c.timestamp[:10])
		if err != nil {
			continue
		}
		after := contract.HarvestAfterDays
		if after <= 0 {
			after = kb.DefaultHarvestAfterDays
		}
		age := int(today.Sub(t).Hours() / 24)
		if age < after {
			continue
		}
		var durable []string
		for _, h := range kb.H2Headings(c.body) {
			if isProcedureHeading(h, harvestHeadings) {
				durable = append(durable, h)
			}
		}
		state, stateName, _ := effectiveState(c.fm, &contract)
		evidence := fmt.Sprintf("%s %s, %d days old (harvest after %d); ", stateName, state, age, after)
		if len(durable) > 0 {
			evidence += fmt.Sprintf("%d durable-looking section(s): %s", len(durable), strings.Join(durable, ", "))
		} else {
			evidence += "no durable-looking section heading"
		}
		out = append(out, ReviewItem{
			Kind:            ReviewHarvestCandidate,
			Concepts:        []string{string(c.id)},
			Evidence:        evidence,
			SuggestedAction: "harvest the durable facts into the live pages this entry links to, each with a dated link back to the entry; then set status to archived and put a line at the top of the entry saying where the lessons went (or that there are none) (the cartographer-kb-doctor skill's harvest step, where the client has it)",
			Weight:          age,
		})
	}
	return out
}

// noLessonsHeader reports whether the body carries the header line the doctor
// writes for an entry with nothing to carry: a blockquote line starting
// "Archived " that contains "no durable lessons" (case-insensitive, code
// masked). The wording is the cartographer-kb-doctor skill's step 5b
// ("> Archived YYYY-MM-DD: no durable lessons."); the kind and the skill are
// changed together, never apart.
func noLessonsHeader(body string) bool {
	for _, line := range strings.Split(kb.MaskCodeSpans(body), "\n") {
		l := strings.TrimSpace(line)
		if !strings.HasPrefix(l, ">") {
			continue
		}
		l = strings.TrimSpace(strings.TrimLeft(l, "> "))
		if strings.HasPrefix(l, "Archived ") && strings.Contains(strings.ToLower(l), "no durable lessons") {
			return true
		}
	}
	return false
}

// lessonUnpromotedItems lists the archived entries of a journal that no live
// page of a non-journal map links (D1078). A link from another journal entry,
// from a retired page or from a digest (which links what it archived, D322)
// is not a carried lesson. A linker with no contract entry (top level) counts
// as a live non-journal page. Weight is the entry's age in days, 0 when its
// timestamp does not parse, as for harvest_candidate.
func lessonUnpromotedItems(concepts []*reviewConcept, contracts map[string]kb.MapContract, links kb.Links) []ReviewItem {
	byID := make(map[okf.ConceptID]*reviewConcept, len(concepts))
	for _, c := range concepts {
		byID[c.id] = c
	}
	today := Now().Truncate(24 * time.Hour)
	var out []ReviewItem
	for _, c := range concepts {
		if contract, ok := contracts[c.mapName]; !ok || contract.Kind != "journal" {
			continue
		}
		if NormValue(c.status) != kb.StatusArchived || noLessonsHeader(c.body) {
			continue
		}
		carried := false
		for from := range links.In[c.id] {
			l := byID[from]
			if l == nil || kb.IsRetired(NormValue(l.status)) || kb.IsDigestType(l.typ) {
				continue
			}
			if lc, ok := contracts[l.mapName]; ok && lc.Kind == "journal" {
				continue
			}
			carried = true
			break
		}
		if carried {
			continue
		}
		age := 0
		if len(c.timestamp) >= 10 {
			if t, err := time.Parse("2006-01-02", c.timestamp[:10]); err == nil {
				age = int(today.Sub(t).Hours() / 24)
			}
		}
		out = append(out, ReviewItem{
			Kind:            ReviewLessonUnpromoted,
			Concepts:        []string{string(c.id)},
			Evidence:        fmt.Sprintf("archived entry of journal %s; no live page of another map links it and it carries no \"no durable lessons\" header", c.mapName),
			SuggestedAction: "read the entry and carry its durable facts into the live page they belong to, with a dated link back to the entry; if there are none, add the line `> Archived YYYY-MM-DD: no durable lessons.` at the top of the entry (never delete the entry; the cartographer-kb-doctor skill's harvest step, where the client has it)",
			Weight:          age,
		})
	}
	return out
}
