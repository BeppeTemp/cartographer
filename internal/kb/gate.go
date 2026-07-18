package kb

import (
	"fmt"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// GateResult holds the result of a commit gate check.
type GateResult struct {
	Pass     bool
	Blockers []GateBlocker
}

// GateBlocker describes a single blocking contradiction.
type GateBlocker struct {
	ConceptPath string   // path of the Contradiction concept
	Involves    []string // concept IDs it involves
	Kind        string   // contradiction_kind value
	Reason      string   // reason field
}

// CommitGate checks for open contradictions involving any of the given concept IDs.
// It walks all .md files, finds those with type=Contradiction and resolution_status=open,
// and checks if their "involves" list intersects with changedIDs.
// Returns Pass=true if no blocking contradictions found.
func (kb *KB) CommitGate(changedIDs []okf.ConceptID) (*GateResult, error) {
	changedSet := make(map[string]bool, len(changedIDs))
	for _, id := range changedIDs {
		changedSet[string(id)] = true
	}

	files, err := kb.listMDFiles(".")
	if err != nil {
		return nil, fmt.Errorf("CommitGate: list files: %w", err)
	}

	result := &GateResult{Pass: true}

	for _, rel := range files {
		content, err := kb.ReadRaw(rel)
		if err != nil {
			continue
		}
		fmRaw, _, hasFM := okf.SplitFrontmatter(content)
		if !hasFM {
			continue
		}
		parsed, err := okf.ParseFrontmatter(fmRaw)
		if err != nil {
			continue
		}

		if parsed.Type() != "Contradiction" {
			continue
		}

		statusVal, ok := parsed.Get("resolution_status")
		if !ok {
			continue
		}
		statusStr, ok := statusVal.(string)
		if !ok || statusStr != "open" {
			continue
		}

		// Collect the involves list.
		invVal, ok := parsed.Get("involves")
		if !ok {
			continue
		}
		var involvesIDs []string
		switch v := invVal.(type) {
		case []string:
			involvesIDs = v
		case string:
			if v != "" {
				involvesIDs = []string{v}
			}
		default:
			continue
		}

		// Check intersection with changedIDs.
		intersects := false
		for _, inv := range involvesIDs {
			if changedSet[inv] {
				intersects = true
				break
			}
		}
		if !intersects {
			continue
		}

		kind := ""
		if kVal, ok := parsed.Get("contradiction_kind"); ok {
			if s, ok := kVal.(string); ok {
				kind = s
			}
		}
		reason := ""
		if rVal, ok := parsed.Get("reason"); ok {
			if s, ok := rVal.(string); ok {
				reason = s
			}
		}

		result.Pass = false
		result.Blockers = append(result.Blockers, GateBlocker{
			ConceptPath: rel,
			Involves:    involvesIDs,
			Kind:        kind,
			Reason:      reason,
		})
	}

	return result, nil
}
