package mcpserver

import (
	"strings"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

const (
	// factQueryBytes caps the search query built from a fact line.
	factQueryBytes = 120
	// factLookupWindow is how many candidates one lookup asks the index for.
	factLookupWindow = 20
)

// factFinder reports, on a write response, the fact lines the write added that
// other concepts already carry (D351). Like similarFinder, nil finds nothing,
// so a tool built without one (a unit test) stays valid.
type factFinder struct {
	k    *kb.KB
	rec  *searchReconciler
	deps Deps
}

// priorBody is the body id has now, "" when it does not exist yet. A handler
// must read it BEFORE the write: afterwards the previous version is gone and
// every line would look added.
func priorBody(k *kb.KB, id string) string {
	data, err := k.ReadConcept(okf.ConceptID(id))
	if err != nil {
		return ""
	}
	return data.Body
}

// find returns the repeated_fact findings for the concept id just written,
// whose body was prev before the write. budget is shared across a
// concept_batch and decremented. It never
// fails: any error means no finding.
func (ff *factFinder) find(ctx requestContext, id, prev string, budget *factBudget) []findingOut {
	if ff == nil || budget.left <= 0 {
		return nil
	}
	data, err := ff.k.ReadConcept(okf.ConceptID(id))
	if err != nil {
		return nil
	}
	// Reconcile once per call, before the first lookup: the written concepts
	// (and the earlier operations of a batch) must be in the index, and each
	// lookup would otherwise walk every file again.
	if !budget.reconciled {
		if _, err := ff.rec.reconcile(); err != nil {
			return nil
		}
		budget.reconciled = true
	}
	lookup := func(line string) []okf.ConceptID {
		hits, _ := keywordHitsReconciled(ctx, ff.k, ff.rec, ff.deps, factQuery(line), "", factLookupWindow, false)
		ids := make([]okf.ConceptID, 0, len(hits))
		for _, h := range hits {
			ids = append(ids, okf.ConceptID(h.ID))
		}
		return ids
	}
	found, used := lint.AddedRepeatedFacts(ff.k, okf.ConceptID(id), prev, data.Body, budget.left, lookup)
	budget.left -= used
	return findingsOut(found)
}

// factQuery cuts a normalised fact line to factQueryBytes at a rune boundary
// and at the last whole word, without an ellipsis: the search index needs
// terms, and a half word of 1-2 characters matches nothing.
func factQuery(line string) string {
	if len(line) <= factQueryBytes {
		return line
	}
	n := factQueryBytes
	for n > 0 && !utf8.RuneStart(line[n]) {
		n--
	}
	cut := line[:n]
	if line[n] != ' ' {
		if i := strings.LastIndex(cut, " "); i > 0 {
			cut = cut[:i]
		}
	}
	return strings.TrimSpace(cut)
}

// withFacts appends the repeated_fact findings to a write response's findings.
// The structural ones keep their order; these come last.
func withFacts(findings []findingOut, ff *factFinder, ctx requestContext, id, prev string, budget *factBudget) []findingOut {
	return append(findings, ff.find(ctx, id, prev, budget)...)
}

// factBudget is the state one write call shares across its concepts (D351):
// the lookups still allowed, and whether the index was reconciled already.
type factBudget struct {
	left       int
	reconciled bool
}

func newFactBudget() *factBudget {
	return &factBudget{left: lint.RepeatedFactLookupCap}
}
