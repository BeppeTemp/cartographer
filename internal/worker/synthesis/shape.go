// Package synthesis is the third task kind of the worker (D11): with nothing
// left to ingest or repair it reads what the KB holds and proposes what it
// infers, as Contradiction concepts of a gap kind (a hypothesis is a gap, never
// a claim). This part is the pure shape of a hypothesis, the guard every model
// call goes through and the choice of candidates; the task kind that drives
// them is wired separately.
//
// It imports neither internal/kb nor internal/mcpserver (the worker only speaks
// MCP): the constants it needs are mirrored here and pinned to the server's by
// the tests.
package synthesis

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

// The two gap kinds a hypothesis may carry. Any other contradiction_kind blocks
// commit_gate on the pages it involves, and a machine guess must never block a
// person's write (D273).
const (
	KindMissingContext = "missing_context"
	KindOpenQuestion   = "open_question"
)

// IsGapKind is the constant check: a hypothesis has one of the two gap kinds.
func IsGapKind(kind string) bool { return kind == KindMissingContext || kind == KindOpenQuestion }

// Confidence values.
var confidences = []string{"low", "medium", "high"}

const (
	maxInvolves = 6
	// Origin marks the concepts the worker proposed.
	Origin = "synthesis"
)

// ID is the concept id of a hypothesis: stable across replicas and across the
// order of involves, so two workers proposing the same thing write the same
// id and the creation is a compare-and-swap.
func ID(contradictionMap, kind string, involves []string) string {
	h := sha256.Sum256([]byte(kind + "\x00" + strings.Join(sortedSet(involves), "\n")))
	slug := strings.ReplaceAll(kind, "_", "-")
	return path.Join(contradictionMap, "synthesis-"+slug+"-"+hex.EncodeToString(h[:4]))
}

func sortedSet(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// Evidence is one line of the body's Evidence section: what a cited page says.
type Evidence struct {
	ID   string // a provenance entry
	Says string
}

// Hypothesis is what the model proposes for one candidate.
type Hypothesis struct {
	Map        string
	Kind       string
	Title      string
	Involves   []string
	Provenance []string
	Confidence string
	Evidence   []Evidence
	// Confirm tells a reader what to check and what writing would follow.
	Confirm string
	Date    time.Time
}

// ID is the concept id this hypothesis is written under.
func (h Hypothesis) ID() string { return ID(h.Map, h.Kind, h.Involves) }

// link reports whether line cites id as a wiki-link (D720: root-relative id).
func link(line, id string) bool {
	return strings.Contains(line, "[["+id+"]]") || strings.Contains(line, "[["+id+"|") || strings.Contains(line, "[["+id+"#")
}

// DropUncited removes every evidence line that links no provenance entry and
// every provenance entry whose id is in gone (a page that vanished between
// reading and writing). A sentence without a cited page is not written.
func (h *Hypothesis) DropUncited(gone map[string]bool) {
	var prov []string
	for _, p := range sortedSet(h.Provenance) {
		if !gone[p] {
			prov = append(prov, p)
		}
	}
	h.Provenance = prov
	var ev []Evidence
	for _, e := range h.Evidence {
		if e.Says == "" || !contains(prov, e.ID) {
			continue
		}
		if link("[["+e.ID+"]] "+e.Says, e.ID) { // the line the body renders
			ev = append(ev, e)
		}
	}
	h.Evidence = ev
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// ErrNoEvidence means nothing is left to cite: no write.
var ErrNoEvidence = errors.New("synthesis: no provenance left, nothing to write")

// Check validates the hypothesis against the shape; an error means no write.
func (h Hypothesis) Check() error {
	switch {
	case !IsGapKind(h.Kind):
		return fmt.Errorf("synthesis: contradiction_kind %q is not a gap kind (%s, %s)", h.Kind, KindMissingContext, KindOpenQuestion)
	case h.Map == "":
		return errors.New("synthesis: no contradiction map")
	case strings.TrimSpace(h.Title) == "":
		return errors.New("synthesis: a hypothesis needs a title")
	case len(sortedSet(h.Involves)) == 0 || len(sortedSet(h.Involves)) > maxInvolves:
		return fmt.Errorf("synthesis: involves must list 1..%d concepts", maxInvolves)
	case len(h.Provenance) == 0 || len(h.Evidence) == 0:
		return ErrNoEvidence
	case !contains(confidences, h.Confidence):
		return fmt.Errorf("synthesis: confidence %q is not one of %v", h.Confidence, confidences)
	}
	for _, e := range h.Evidence {
		if !contains(h.Provenance, e.ID) || !link(h.evidenceLine(e), e.ID) {
			return fmt.Errorf("synthesis: evidence line for %q cites no provenance entry", e.ID)
		}
	}
	return nil
}

func (h Hypothesis) evidenceLine(e Evidence) string {
	return fmt.Sprintf("- [[%s]]: %s", e.ID, strings.Join(strings.Fields(e.Says), " "))
}

// Frontmatter is the concept's frontmatter, the keys of D11's convention.
func (h Hypothesis) Frontmatter() map[string]any {
	return map[string]any{
		"type":               "Contradiction",
		"title":              strings.TrimSpace(h.Title),
		"contradiction_kind": h.Kind,
		"resolution_status":  "open",
		"involves":           sortedSet(h.Involves),
		"provenance":         sortedSet(h.Provenance),
		"origin":             Origin,
		"confidence":         h.Confidence,
		"timestamp":          h.Date.Format("2006-01-02"),
	}
}

// Body is the markdown body: the hypothesis, its evidence, how to confirm or
// reject it.
func (h Hypothesis) Body() string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(h.Title) + "\n\n## Evidence\n\n")
	for _, e := range h.Evidence {
		b.WriteString(h.evidenceLine(e) + "\n")
	}
	b.WriteString("\n## How to confirm or reject\n\n" + strings.TrimSpace(h.Confirm) + "\n")
	return b.String()
}
