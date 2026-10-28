package lint

import (
	"fmt"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// conceptCtx is what conceptFindings needs about one page (D354). In carries
// the frontmatter-driven inputs both callers build; the rest only a whole-KB
// run has.
type conceptCtx struct {
	ID okf.ConceptID
	In conceptInput
	// Write: the write path (CheckConcept). Only checks whose spec is OnWrite
	// are evaluated, and the run-only inputs below are never read.
	Write bool

	// KBRoot, HasSecretsDir and LegacyPaths drive the artifact-flavoured
	// body checks (D316).
	KBRoot        string
	HasSecretsDir bool
	LegacyPaths   []legacyPath
}

// wants reports whether the group of checks must be evaluated: always in a
// whole-KB run, only for OnWrite specs on the write path.
func (c conceptCtx) wants(checks ...string) bool {
	if !c.Write {
		return true
	}
	for _, name := range checks {
		if s, ok := Spec(name); ok && s.OnWrite {
			return true
		}
	}
	return false
}

// conceptFindings is the one evaluator of the LevelConcept checks (D354): the
// ones one page's own frontmatter, body and map contract decide. Run calls it
// for every page and CheckConcept for the written one, so the two cannot
// disagree about a page (TestCheckConceptAgreesWithRun). A check added here must be registered; one the
// write path should report is registered OnWrite.
func conceptFindings(c conceptCtx) []Finding {
	var out []Finding
	in := c.In

	// --- sops_format_mismatch / sops_missing_file / legacy_path (D316) ---
	if c.wants("sops_format_mismatch", "sops_missing_file") {
		out = append(out, sopsFindings(in.Body, in.RelPath, c.KBRoot, c.HasSecretsDir)...)
	}
	if c.wants("secret_ref_broken") && in.Parsed != nil {
		out = append(out, secretRefFindings(in.Parsed, in.RelPath, c.KBRoot)...)
	}
	if c.wants("potential_secret") {
		out = append(out, secretFindings(in)...)
	}
	if c.wants("legacy_path") {
		out = append(out, legacyPathFindings(in.Body, in.RelPath, c.LegacyPaths)...)
	}

	if parsed := in.Parsed; parsed != nil {
		// D74 WP1: a concept imported via `cartographer import` (or the
		// agent-side fallback) is marked status: imported until curated.
		// The finding keeps the curation backlog visible and resumable
		// across sessions instead of a big-bang rewrite.
		if statusVal, ok := parsed.Get("status"); ok && c.wants("imported_draft") {
			if statusStr, ok := statusVal.(string); ok && statusStr == "imported" {
				out = append(out, newFinding("imported_draft", Finding{
					Path:    in.RelPath,
					Message: "imported concept awaiting curation",
				}))
			}
		}

		// --- secrets_on_non_service (D158, D313) ---
		// service_list/service_get match type Service (case-insensitively
		// since D158): a concept declaring secrets under any other type is
		// not listed there. A dossier is resolved by concept ID with
		// secret_resolve; the write gate refuses the declaration all the same
		// (D907), and the fix is the type Service or a dedicated Service.
		if c.wants("secrets_on_non_service") && !kb.IsServiceType(parsed.Type()) {
			for _, field := range []string{"secrets_source", "secret_refs"} {
				if v, ok := parsed.Get(field); ok && !emptyFrontmatterValue(v) {
					out = append(out, newFinding("secrets_on_non_service", Finding{
						Path:    in.RelPath,
						Message: fmt.Sprintf("declares %s but type is %q — service_get only resolves type Service; move the %s to a Service concept", field, parsed.Type(), field),
					}))
				}
			}
		}
	}

	// --- stale_claim, machine_path, missing_title, map contracts,
	// nonstandard_field, tool_param_field…: frontmatterFindings ---
	for _, f := range frontmatterFindings(in) {
		if c.wants(f.Check) {
			out = append(out, f)
		}
	}
	return out
}
