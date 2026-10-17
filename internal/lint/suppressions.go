package lint

import (
	"fmt"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// Suppression sources (D834).
const (
	SourceConcept = "concept" // lint_ignore in a concept's frontmatter (D159)
	SourceMap     = "map"     // lint_ignore in a map's _map.md (D306)
	SourceAccept  = "accept"  // lint_accept in instructions.md (D332)
)

// Suppression is one lint_ignore / lint_accept entry and what it did in a lint
// run (D834): the inventory behind kb_status's suppressions section. The date
// and reason of the entry are not here: they are derived from git by the
// caller, never stored next to the entry.
type Suppression struct {
	Source string // SourceConcept, SourceMap or SourceAccept
	// Path is the concept file (concept), "<map>/_map.md" (map), or the
	// lint_accept key, an entry on instructions.md (accept).
	Path   string
	Check  string // the name the entry lists
	Hidden int    // findings the entry dropped in this run
	// Stale: a whole-KB run found nothing the entry hides, and the lint run
	// would have seen it (see neverStale). Always false on a scoped run.
	Stale bool

	id okf.ConceptID // concept entries: lets an island be counted on its member
}

type conceptCheck struct {
	id    okf.ConceptID
	check string
}

// neverStale are the lint_ignore names a whole-KB lint run cannot judge: they
// hide nothing in the run because the run does not produce what they name.
// Trap: reporting them stale would tell the operator to delete an entry that
// still works. Today that is every kb_review kind (a candidate list built by
// kb_review, repeated_fact included, which a write response also reports):
// Level kb with Accept concept in the registry. A check that only the write
// path produces would join this set.
var neverStale = checkSet(func(s CheckSpec) bool { return s.Level == LevelKB && s.Accept == AcceptConcept })

// reportStale marks the concept and map entries that hid nothing and adds a
// lint_ignore_invalid on the file that holds each (WP2). Only a whole-KB run
// calls it: a scoped run does not evaluate every check, so it would report
// entries that are fine. A name already reported invalid (unknown,
// unsuppressible) is not reported twice, and lint_accept entries were already
// judged by applyLintAccept.
func reportStale(findings []Finding, sups []Suppression) ([]Finding, []Suppression) {
	for i := range sups {
		sp := &sups[i]
		if sp.Source == SourceAccept {
			continue // Stale is set by applyLintAccept, which reports it itself
		}
		if spec, _ := Spec(sp.Check); spec.NeverSuppress {
			continue // already reported invalid by the concept or the map pass
		}
		valid := perConceptChecks[sp.Check] || (sp.Source == SourceMap && mapOnlyIgnorable[sp.Check])
		if !valid || neverStale[sp.Check] || sp.Hidden > 0 {
			continue
		}
		sp.Stale = true
		path := sp.Path
		if sp.Source == SourceConcept {
			path = okf.IDToPath(sp.id)
		}
		findings = append(findings, newFinding("lint_ignore_invalid", Finding{
			Path:    path,
			Message: fmt.Sprintf("lint_ignore names %q, which hides no finding here, so the entry is stale", sp.Check),
		}))
	}
	return findings, sups
}

// sortSuppressions orders the inventory by source, path, check: a run's
// parallel workers finish in any order.
func sortSuppressions(sups []Suppression) {
	order := map[string]int{SourceConcept: 0, SourceMap: 1, SourceAccept: 2}
	sort.SliceStable(sups, func(i, j int) bool {
		a, b := sups[i], sups[j]
		if a.Source != b.Source {
			return order[a.Source] < order[b.Source]
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Check < b.Check
	})
}

// DefaultOptions is the injected, non-KB input every caller of Run passes, for
// callers that need RunWithSuppressions.
func DefaultOptions(k *kb.KB) Options { return runOptions(k) }

// DeclaredEntries reads the entries a file declares, from the file's whole
// text (a revision read from git, not the working tree): the lint_ignore names
// of a concept or a _map.md (SourceConcept, SourceMap), or the checks
// lint_accept lists under key in instructions.md (SourceAccept). It is how the
// inventory finds the commit that added an entry (D834).
func DeclaredEntries(source, content, key string) map[string]bool {
	fmRaw, _, ok := okf.SplitFrontmatter(content)
	if !ok {
		return nil
	}
	fm, err := okf.ParseFrontmatter(fmRaw)
	if err != nil || fm == nil {
		return nil
	}
	if source != SourceAccept {
		return lintIgnoreSet(fm)
	}
	v, ok := fm.Get("lint_accept")
	if !ok {
		return nil
	}
	b, ok := v.(okf.Block)
	if !ok {
		return nil
	}
	return parseLintAccept(strings.TrimSpace(string(b)))[key]
}

// AcceptKeys lists the keys of the lint_accept mapping of an instructions.md
// text, sorted.
func AcceptKeys(content string) []string {
	fmRaw, _, ok := okf.SplitFrontmatter(content)
	if !ok {
		return nil
	}
	fm, err := okf.ParseFrontmatter(fmRaw)
	if err != nil || fm == nil {
		return nil
	}
	v, _ := fm.Get("lint_accept")
	b, ok := v.(okf.Block)
	if !ok {
		return nil
	}
	var keys []string
	for key := range parseLintAccept(string(b)) {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
