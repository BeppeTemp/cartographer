package lint

import (
	"fmt"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// contradictionStatusProblem is the contradiction_status message for one page,
// or "" (#2187). A Contradiction (a gap included) is closed by its
// resolution_status, one of kb.ResolutionStatuses, not by `status`: the gate,
// kb_status and work_list read only resolution_status, and any value but the
// exact "open" counts as closed, so a made-up one ("done") closes the item
// silently and a `status: done` beside an open resolution_status leaves it open.
func contradictionStatusProblem(fm *okf.Frontmatter, contract *kb.MapContract) string {
	if fm == nil || !kb.IsContradictionType(fm.Type()) {
		return ""
	}
	vocab := strings.Join(kb.ResolutionStatuses, ", ")
	rs, hasRS := fm.Get("resolution_status")
	if hasRS {
		if s, ok := rs.(string); !ok || !kb.IsResolutionStatus(s) {
			return fmt.Sprintf("resolution_status %v is not one of %s — a Contradiction or gap is closed with concept_patch setting resolution_status: resolved (or wontfix) and a resolution", rs, vocab)
		}
	}
	if s, _ := rs.(string); s == "" || s == "open" {
		if status, _ := frontmatterValue(fm, "status").(string); status != "" {
			if fam, ok := familiesFor(contract).member(status); ok && (fam == "done" || fam == "resolved") {
				return fmt.Sprintf("status %q does not close a Contradiction, it stays open — set resolution_status: resolved (or wontfix) and a resolution with concept_patch (values: %s)", status, vocab)
			}
		}
	}
	return ""
}
