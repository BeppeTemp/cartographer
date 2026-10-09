package lint

import (
	"fmt"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// ReviewHarvestCandidate (D322) is a closed journal entry that has rested long
// enough to be harvested: its durable facts carried into the live pages it
// links, then the entry archived. The server only finds them (D14); what is
// durable is the agent's judgement (D305).
const ReviewHarvestCandidate = "harvest_candidate"

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
		if contract.HarvestAfterDays < 0 {
			continue // harvest_after: 0, off (D347)
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
			SuggestedAction: "harvest the durable facts into the live pages this entry links to, then set status to archived with a header line (kb-doctor, harvest and archive)",
			Weight:          age,
		})
	}
	return out
}
