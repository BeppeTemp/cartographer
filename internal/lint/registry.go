package lint

import (
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
)

// Check levels (D354): where a check is computed, which decides how it is
// evaluated.
const (
	// LevelConcept: computed from one page, its frontmatter, body and map
	// contract alone. conceptFindings evaluates these for Run and CheckConcept.
	LevelConcept = "concept"
	// LevelMap: a map or an expanded concept's directory (its descriptor, index
	// and asset set).
	LevelMap = "map"
	// LevelGraph: needs the link graph, or KB-wide data computed once per run
	// (placeholder registry, glossary, source citations).
	LevelGraph = "graph"
	// LevelArtifact: a KB-root artifact file (skills, agents, instructions.md,
	// junk files), reported only by an unscoped lint.
	LevelArtifact = "artifact"
	// LevelKB: a KB-level file or a kb_review kind, not a page.
	LevelKB = "kb"
)

// CheckSpec describes one check (D354). Every table that used to list checks
// by hand (acceptability, repairability, suppression, the artifacts panel…) is
// a projection of this struct, so a new check is one entry here.
type CheckSpec struct {
	Name string
	// Severity is the default severity every finding of the check carries. An
	// emission site may pass an explicit override through newFinding.
	Severity string
	Level    string
	// Accept is who can accept a finding with lint_ignore (D313): AcceptConcept,
	// AcceptMap, AcceptArtifact or AcceptNone.
	Accept string
	// FixKinds are the Fix kinds the check's findings may carry; non-empty means
	// kb_repair accepts the check (FixableChecks).
	FixKinds []string
	// AutoRepairSafe: the fix may run with no person (eligible for auto_repair).
	AutoRepairSafe bool
	// CrossConcept: the fix touches another file than the finding's own.
	CrossConcept bool
	// OnWrite: evaluated on the write path (CheckConcept for concept-level
	// checks, ScopedCheck for the graph and map ones).
	OnWrite bool
	// Judgement: an unfixed finding becomes a kb_review lint_judgement item.
	Judgement bool

	// AlsoArtifact: lint_accept in instructions.md may name the check although
	// Accept is concept (it fires on concept bodies and on artifact files).
	AlsoArtifact bool
	// NeverSuppress: lint_ignore never silences it even though it is a warning.
	NeverSuppress bool
	// WholeGraph: its presence depends on concepts other than the one it is
	// reported on (see WholeGraphChecks).
	WholeGraph bool
	// Panel: the Artifacts panel lists the findings (what the KB ships).
	Panel bool
	// Category groups the check for a reader (D365): the Atlas Health panel
	// lists every check by category with its count. Empty for kb_review kinds,
	// which are not lint checks.
	Category string
}

// Check categories (D365), in reading order.
const (
	CategoryPages     = "pages"
	CategoryTemplates = "templates"
	CategoryValidity  = "validity"
	CategoryLinks     = "links"
	CategoryValues    = "values"
	CategoryMaps      = "maps"
	CategoryArtifacts = "artifacts"
	CategoryKB        = "kb"
)

// Categories is the reading order of the check categories.
var Categories = []string{CategoryPages, CategoryTemplates, CategoryValidity, CategoryLinks, CategoryValues, CategoryMaps, CategoryArtifacts, CategoryKB}

// registry is the single list of checks (D354). Order is for reading: the
// catalogue in docs/data-plane.md follows it. Adding a check is this entry, a
// newFinding call and a regenerated catalogue (CONTRIBUTING.md §Adding a lint
// check).
var registry = []CheckSpec{
	// Frontmatter and body of one page (conceptFindings, on the write path).
	{Name: "malformed_frontmatter", Severity: SevWarning, Level: LevelConcept, Accept: AcceptConcept, OnWrite: true, NeverSuppress: true, Category: CategoryPages},
	{Name: "stringified_list", Severity: SevWarning, Level: LevelConcept, Accept: AcceptConcept, OnWrite: true, NeverSuppress: true, FixKinds: []string{FixListifyField}, AutoRepairSafe: true, Category: CategoryPages},
	{Name: "stale_claim", Severity: SevWarning, Level: LevelConcept, Accept: AcceptConcept, OnWrite: true, Category: CategoryPages},
	{Name: "status_semantics", Severity: SevWarning, Level: LevelConcept, Accept: AcceptConcept, OnWrite: true, Category: CategoryPages},
	{Name: "machine_path", Severity: SevWarning, Level: LevelConcept, Accept: AcceptConcept, OnWrite: true, Category: CategoryPages},
	{Name: "mangled_placeholder", Severity: SevWarning, Level: LevelConcept, Accept: AcceptConcept, OnWrite: true, Category: CategoryPages},
	{Name: "missing_title", Severity: SevWarning, Level: LevelConcept, Accept: AcceptConcept, OnWrite: true, FixKinds: []string{FixSetValue}, AutoRepairSafe: true, Category: CategoryPages},
	{Name: "title_h1_mismatch", Severity: SevWarning, Level: LevelConcept, Accept: AcceptConcept, OnWrite: true, FixKinds: []string{FixSyncH1, FixSetValue}, AutoRepairSafe: true, Category: CategoryPages},
	{Name: "title_quality", Severity: SevInfo, Level: LevelConcept, Accept: AcceptConcept, OnWrite: true, Category: CategoryPages},
	{Name: "missing_required_field", Severity: SevError, Level: LevelConcept, Accept: AcceptNone, OnWrite: true, Category: CategoryPages},
	{Name: "invalid_field_value", Severity: SevError, Level: LevelConcept, Accept: AcceptNone, OnWrite: true, FixKinds: []string{FixSetValue}, AutoRepairSafe: true, Category: CategoryPages},
	{Name: "forbidden_field", Severity: SevError, Level: LevelConcept, Accept: AcceptNone, OnWrite: true, Category: CategoryPages},
	{Name: "nonstandard_field", Severity: SevWarning, Level: LevelConcept, Accept: AcceptConcept, OnWrite: true, FixKinds: []string{FixRenameField}, AutoRepairSafe: true, Category: CategoryPages},
	{Name: "tool_param_field", Severity: SevWarning, Level: LevelConcept, Accept: AcceptNone, OnWrite: true, NeverSuppress: true, FixKinds: []string{FixDropField}, AutoRepairSafe: true, Category: CategoryPages},
	{Name: "prose_value", Severity: SevWarning, Level: LevelConcept, Accept: AcceptConcept, OnWrite: true, FixKinds: []string{FixSplitValue}, AutoRepairSafe: true, Category: CategoryPages},
	{Name: "stale_open", Severity: SevInfo, Level: LevelConcept, Accept: AcceptConcept, OnWrite: true, Judgement: true, Category: CategoryPages},
	{Name: "closed_with_open_items", Severity: SevInfo, Level: LevelConcept, Accept: AcceptConcept, OnWrite: true, Judgement: true, Category: CategoryPages},
	{Name: "template_section_missing", Severity: SevInfo, Level: LevelConcept, Accept: AcceptConcept, OnWrite: true, Judgement: true, Category: CategoryTemplates},
	// Templates are closed page schemas a map's pages bind to (D352). Evaluated
	// only on a map that sets require_template, except template_section_missing,
	// which template_sections: true also turns on.
	{Name: "template_missing", Severity: SevWarning, Level: LevelConcept, Accept: AcceptConcept, OnWrite: true, FixKinds: []string{FixSetValue}, AutoRepairSafe: true, Judgement: true, Category: CategoryTemplates},
	{Name: "template_unknown", Severity: SevWarning, Level: LevelConcept, Accept: AcceptConcept, OnWrite: true, Judgement: true, Category: CategoryTemplates},
	{Name: "template_not_allowed", Severity: SevWarning, Level: LevelConcept, Accept: AcceptConcept, OnWrite: true, Judgement: true, Category: CategoryTemplates},
	{Name: "template_type_mismatch", Severity: SevWarning, Level: LevelConcept, Accept: AcceptConcept, OnWrite: true, FixKinds: []string{FixSetValue}, AutoRepairSafe: true, Category: CategoryTemplates},
	{Name: "template_field_missing", Severity: SevWarning, Level: LevelConcept, Accept: AcceptConcept, OnWrite: true, Judgement: true, Category: CategoryTemplates},
	{Name: "template_field_value", Severity: SevWarning, Level: LevelConcept, Accept: AcceptConcept, OnWrite: true, FixKinds: []string{FixSetValue}, AutoRepairSafe: true, Judgement: true, Category: CategoryTemplates},
	{Name: "template_extra_section", Severity: SevWarning, Level: LevelConcept, Accept: AcceptConcept, OnWrite: true, Judgement: true, Category: CategoryTemplates},
	{Name: "template_section_alias", Severity: SevWarning, Level: LevelConcept, Accept: AcceptConcept, OnWrite: true, FixKinds: []string{FixRenameHeading}, AutoRepairSafe: true, Judgement: true, Category: CategoryTemplates},
	{Name: "template_section_order", Severity: SevInfo, Level: LevelConcept, Accept: AcceptConcept, OnWrite: true, FixKinds: []string{FixReorderSections}, AutoRepairSafe: true, Category: CategoryTemplates},
	{Name: "open_marker", Severity: SevInfo, Level: LevelConcept, Accept: AcceptConcept, OnWrite: true, Category: CategoryPages},
	{Name: "repeated_link", Severity: SevInfo, Level: LevelConcept, Accept: AcceptConcept, OnWrite: true, FixKinds: []string{FixUnlinkRepeat}, AutoRepairSafe: true, Category: CategoryPages},
	// What the write path refuses about a page, as lint (D356): the repair and
	// the doctor act on it. Not OnWrite: a write that would produce one of them
	// is refused before it lints.
	{Name: "missing_frontmatter", Severity: SevError, Level: LevelConcept, Accept: AcceptNone, FixKinds: []string{FixAddFrontmatter}, AutoRepairSafe: true, Judgement: true, Category: CategoryValidity},
	{Name: "unparseable_frontmatter", Severity: SevError, Level: LevelConcept, Accept: AcceptNone, FixKinds: []string{FixQuoteValue}, AutoRepairSafe: true, Judgement: true, Category: CategoryValidity},
	{Name: "missing_type", Severity: SevError, Level: LevelConcept, Accept: AcceptNone, FixKinds: []string{FixSetValue}, AutoRepairSafe: true, Judgement: true, Category: CategoryValidity},
	{Name: "concept_too_deep", Severity: SevError, Level: LevelConcept, Accept: AcceptNone, Judgement: true, Category: CategoryValidity},
	{Name: "nonslug_file_name", Severity: SevWarning, Level: LevelConcept, Accept: AcceptConcept, FixKinds: []string{FixMove}, AutoRepairSafe: true, CrossConcept: true, Category: CategoryValidity},
	{Name: "empty_concept", Severity: SevWarning, Level: LevelConcept, Accept: AcceptConcept, Judgement: true, Category: CategoryValidity},
	// One page, but only a whole-KB run has the inputs.
	{Name: "concept_oversize", Severity: SevInfo, Level: LevelConcept, Accept: AcceptConcept, Judgement: true, Category: CategoryPages},
	{Name: "imported_draft", Severity: SevWarning, Level: LevelConcept, Accept: AcceptConcept, Category: CategoryPages},
	{Name: "secrets_on_non_service", Severity: SevInfo, Level: LevelConcept, Accept: AcceptConcept, Category: CategoryPages},
	{Name: "sops_format_mismatch", Severity: SevWarning, Level: LevelConcept, Accept: AcceptConcept, AlsoArtifact: true, Panel: true, Category: CategoryPages},
	{Name: "sops_missing_file", Severity: SevWarning, Level: LevelConcept, Accept: AcceptConcept, AlsoArtifact: true, Panel: true, Category: CategoryPages},
	{Name: "legacy_path", Severity: SevWarning, Level: LevelConcept, Accept: AcceptConcept, AlsoArtifact: true, FixKinds: []string{FixReplacePrefix}, AutoRepairSafe: true, Category: CategoryPages},

	// Links and the graph (ScopedCheck on the write path).
	{Name: "broken_link", Severity: SevWarning, Level: LevelGraph, Accept: AcceptConcept, OnWrite: true, FixKinds: []string{FixRebaseLink}, CrossConcept: true, Judgement: true, Category: CategoryLinks},
	{Name: "duplicate_link", Severity: SevInfo, Level: LevelGraph, Accept: AcceptConcept, OnWrite: true, FixKinds: []string{FixDropLinkItem, FixRewriteLinkItem}, AutoRepairSafe: true, Category: CategoryLinks},
	{Name: "reciprocal_link_item", Severity: SevInfo, Level: LevelGraph, Accept: AcceptConcept, OnWrite: true, FixKinds: []string{FixDropLinkItem}, CrossConcept: true, Category: CategoryLinks},
	{Name: "bare_link_list", Severity: SevInfo, Level: LevelGraph, Accept: AcceptConcept, OnWrite: true, Judgement: true, Category: CategoryLinks},
	{Name: "index_link_form", Severity: SevInfo, Level: LevelGraph, Accept: AcceptNone, OnWrite: true, FixKinds: []string{FixRebaseLink, FixRewriteWikiLink}, AutoRepairSafe: true, CrossConcept: true, Category: CategoryLinks},
	{Name: "orphan", Severity: SevWarning, Level: LevelGraph, Accept: AcceptConcept, OnWrite: true, Category: CategoryLinks},
	{Name: "broken_relation", Severity: SevWarning, Level: LevelGraph, Accept: AcceptConcept, OnWrite: true, Category: CategoryLinks},
	{Name: "link_to_retired", Severity: SevInfo, Level: LevelGraph, Accept: AcceptConcept, OnWrite: true, WholeGraph: true, Category: CategoryLinks},
	{Name: "unknown_placeholder", Severity: SevWarning, Level: LevelGraph, Accept: AcceptConcept, OnWrite: true, Category: CategoryLinks},
	{Name: "forbidden_term", Severity: SevWarning, Level: LevelGraph, Accept: AcceptConcept, OnWrite: true, Category: CategoryLinks},
	{Name: "source_uncited", Severity: SevWarning, Level: LevelGraph, Accept: AcceptConcept, Category: CategoryLinks},
	{Name: "cut_concept", Severity: SevInfo, Level: LevelGraph, Accept: AcceptConcept, WholeGraph: true, Category: CategoryLinks},
	{Name: "island", Severity: SevInfo, Level: LevelGraph, Accept: AcceptConcept, WholeGraph: true, Category: CategoryLinks},
	{Name: "map_misfit", Severity: SevInfo, Level: LevelGraph, Accept: AcceptConcept, WholeGraph: true, Judgement: true, Category: CategoryLinks},
	// KB-wide values (D357): the type palette and the spellings in use.
	{Name: "unknown_type", Severity: SevWarning, Level: LevelGraph, Accept: AcceptConcept, FixKinds: []string{FixSetValue}, Judgement: true, Category: CategoryValues},
	{Name: "value_case_variant", Severity: SevWarning, Level: LevelGraph, Accept: AcceptConcept, FixKinds: []string{FixSetValue}, AutoRepairSafe: true, Category: CategoryValues},

	// Maps and expanded concepts.
	{Name: "contract_malformed", Severity: SevInfo, Level: LevelMap, Accept: AcceptNone, Category: CategoryMaps},
	{Name: "facet_sprawl", Severity: SevInfo, Level: LevelMap, Accept: AcceptMap, Category: CategoryMaps},
	{Name: "missing_value_contract", Severity: SevInfo, Level: LevelMap, Accept: AcceptMap, Category: CategoryMaps},
	{Name: "map_without_templates", Severity: SevInfo, Level: LevelMap, Accept: AcceptMap, Category: CategoryMaps},
	{Name: "map_oversize", Severity: SevInfo, Level: LevelMap, Accept: AcceptMap, Category: CategoryMaps},
	{Name: "legacy_archive_descriptor", Severity: SevWarning, Level: LevelMap, Accept: AcceptNone, Category: CategoryMaps},
	{Name: "index_incomplete", Severity: SevWarning, Level: LevelMap, Accept: AcceptNone, OnWrite: true, Category: CategoryMaps},
	{Name: "index_lists_retired", Severity: SevInfo, Level: LevelMap, Accept: AcceptMap, WholeGraph: true, Category: CategoryMaps},
	{Name: "index_stale", Severity: SevInfo, Level: LevelMap, Accept: AcceptNone, Category: CategoryMaps},
	{Name: "expanded_missing_index", Severity: SevWarning, Level: LevelMap, Accept: AcceptNone, Category: CategoryMaps},
	{Name: "expanded_ambiguous", Severity: SevError, Level: LevelMap, Accept: AcceptNone, Category: CategoryMaps},
	{Name: "expanded_as_category", Severity: SevWarning, Level: LevelMap, Accept: AcceptNone, Category: CategoryMaps},
	{Name: "unlistable_assets", Severity: SevWarning, Level: LevelMap, Accept: AcceptNone, Category: CategoryMaps},
	{Name: "oversized_asset", Severity: SevWarning, Level: LevelMap, Accept: AcceptNone, Category: CategoryMaps},
	{Name: "orphan_asset", Severity: SevInfo, Level: LevelMap, Accept: AcceptNone, Category: CategoryMaps},
	// The data/ tree (D357). unmapped_folder is on a folder, not a page.
	{Name: "unmapped_folder", Severity: SevWarning, Level: LevelMap, Accept: AcceptNone, FixKinds: []string{FixScaffoldMap}, AutoRepairSafe: true, Category: CategoryMaps},
	{Name: "stray_file", Severity: SevWarning, Level: LevelMap, Accept: AcceptConcept, Judgement: true, Category: CategoryMaps},

	// KB-root artifacts (lint_accept in instructions.md, D332).
	{Name: "skill_invalid", Severity: SevWarning, Level: LevelArtifact, Accept: AcceptArtifact, Panel: true, Category: CategoryArtifacts},
	{Name: "skill_warning", Severity: SevInfo, Level: LevelArtifact, Accept: AcceptArtifact, Panel: true, Category: CategoryArtifacts},
	{Name: "legacy_tool_name", Severity: SevWarning, Level: LevelArtifact, Accept: AcceptArtifact, Panel: true, FixKinds: []string{FixStripToolPrefix}, AutoRepairSafe: true, Category: CategoryArtifacts},
	{Name: "skill_broken_ref", Severity: SevWarning, Level: LevelArtifact, Accept: AcceptArtifact, Panel: true, Category: CategoryArtifacts},
	{Name: "skill_git_command", Severity: SevInfo, Level: LevelArtifact, Accept: AcceptArtifact, Panel: true, Category: CategoryArtifacts},
	{Name: "skill_missing_perimeter", Severity: SevInfo, Level: LevelArtifact, Accept: AcceptArtifact, Panel: true, Category: CategoryArtifacts},
	{Name: "cross_kb_path", Severity: SevWarning, Level: LevelArtifact, Accept: AcceptArtifact, Panel: true, Category: CategoryArtifacts},
	{Name: "artifact_unused", Severity: SevInfo, Level: LevelArtifact, Accept: AcceptArtifact, Panel: true, Category: CategoryArtifacts},
	// A junk file is deleted, never accepted; missing_instructions has no file
	// to key on.
	{Name: "junk_file", Severity: SevWarning, Level: LevelArtifact, Accept: AcceptNone, Panel: true, Category: CategoryArtifacts},
	{Name: "junk_asset", Severity: SevWarning, Level: LevelArtifact, Accept: AcceptNone, Panel: true, Category: CategoryArtifacts},
	{Name: "missing_instructions", Severity: SevWarning, Level: LevelArtifact, Accept: AcceptNone, Panel: true, Category: CategoryArtifacts},

	// KB-level files.
	{Name: "hook_invalid", Severity: SevWarning, Level: LevelKB, Accept: AcceptNone, Panel: true, Category: CategoryKB},
	{Name: "missing_registry", Severity: SevInfo, Level: LevelKB, Accept: AcceptNone, Category: CategoryKB},
	{Name: "unused_placeholder", Severity: SevInfo, Level: LevelKB, Accept: AcceptNone, Category: CategoryKB},
	{Name: "lint_ignore_invalid", Severity: SevWarning, Level: LevelKB, Accept: AcceptNone, Category: CategoryKB},

	// kb_review kinds (D298): registered for acceptability only. They are not
	// lint findings, except repeated_fact which a write response also reports
	// (D351). Dismissing one is lint_ignore on a concept it names.
	{Name: ReviewDuplicate, Severity: SevInfo, Level: LevelKB, Accept: AcceptConcept},
	{Name: ReviewZombie, Severity: SevInfo, Level: LevelKB, Accept: AcceptConcept},
	{Name: ReviewPromotion, Severity: SevInfo, Level: LevelKB, Accept: AcceptConcept},
	{Name: ReviewGlossary, Severity: SevInfo, Level: LevelKB, Accept: AcceptConcept},
	{Name: ReviewLintJudgement, Severity: SevInfo, Level: LevelKB, Accept: AcceptConcept},
	{Name: ReviewRepeatedFact, Severity: SevInfo, Level: LevelKB, Accept: AcceptConcept},
	{Name: ReviewReadHotspot, Severity: SevInfo, Level: LevelKB, Accept: AcceptConcept},
	{Name: ReviewScatteredWork, Severity: SevInfo, Level: LevelKB, Accept: AcceptConcept},
	{Name: ReviewStatusReclassify, Severity: SevInfo, Level: LevelKB, Accept: AcceptConcept},
	{Name: ReviewHarvestCandidate, Severity: SevInfo, Level: LevelKB, Accept: AcceptConcept},
	{Name: ReviewTemplateProposal, Severity: SevInfo, Level: LevelKB, Accept: AcceptConcept},
	// Dismissed in a map's _map.md, not on a concept (D304).
	{Name: ReviewMapNaming, Severity: SevInfo, Level: LevelKB, Accept: AcceptConcept},
}

var registryIndex = func() map[string]CheckSpec {
	m := make(map[string]CheckSpec, len(registry))
	for _, s := range registry {
		if _, dup := m[s.Name]; dup {
			panic("lint: check registered twice: " + s.Name)
		}
		m[s.Name] = s
	}
	return m
}()

// Spec returns the descriptor of a check.
func Spec(name string) (CheckSpec, bool) {
	s, ok := registryIndex[name]
	return s, ok
}

// Checks returns every registered check in registry order.
func Checks() []CheckSpec { return append([]CheckSpec(nil), registry...) }

// checkNames lists, sorted, the registered checks for which pick is true.
func checkNames(pick func(CheckSpec) bool) []string {
	var out []string
	for _, s := range registry {
		if pick(s) {
			out = append(out, s.Name)
		}
	}
	sort.Strings(out)
	return out
}

// checkSet is checkNames as a lookup table.
func checkSet(pick func(CheckSpec) bool) map[string]bool {
	out := map[string]bool{}
	for _, n := range checkNames(pick) {
		out[n] = true
	}
	return out
}

// AutoRepairSafe reports whether a check's fix may run with no person (D354).
func AutoRepairSafe(check string) bool {
	s, ok := Spec(check)
	return ok && s.AutoRepairSafe
}

// ArtifactPanelCheck reports whether the Artifacts panel lists a check's
// findings (D316).
func ArtifactPanelCheck(check string) bool {
	s, ok := Spec(check)
	return ok && s.Panel
}

// MapRepairCheck reports whether a fixable check's findings name a data/ folder
// rather than a concept (unmapped_folder, D357): its repair writes the folder's
// descriptor.
func MapRepairCheck(check string) bool {
	s, ok := Spec(check)
	if !ok {
		return false
	}
	for _, k := range s.FixKinds {
		if k == FixScaffoldMap {
			return true
		}
	}
	return false
}

// ArtifactRepairCheck reports whether a fixable check's findings name a
// KB-root artifact file rather than a concept (D316): its repair rewrites that
// file the way artifact_write would.
func ArtifactRepairCheck(check string) bool {
	s, ok := Spec(check)
	return ok && s.Level == LevelArtifact && len(s.FixKinds) > 0
}

var unknownCheckOnce sync.Map

// newFinding builds a finding of a registered check: Check is the name and
// Severity the spec's default unless f already sets one (an explicit override
// for a check whose severity legitimately varies). Every finding of a check is
// built here, so severity is declared in one place (D354). A name with no spec
// panics under test; in production the finding is info and one stderr line says
// the registry is missing it.
func newFinding(check string, f Finding) Finding {
	f.Check = check
	s, ok := Spec(check)
	if !ok {
		if testing.Testing() {
			panic("lint: finding of unregistered check " + check)
		}
		if _, seen := unknownCheckOnce.LoadOrStore(check, true); !seen {
			fmt.Fprintf(os.Stderr, "lint: check %q is not in the registry; reported as info\n", check)
		}
		if f.Severity == "" {
			f.Severity = SevInfo
		}
		return f
	}
	if f.Severity == "" {
		f.Severity = s.Severity
	}
	return f
}
