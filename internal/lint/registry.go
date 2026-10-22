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
// by hand (repairability, the write gate, the artifacts panel…) is a projection
// of this struct, so a new check is one entry here. A check has no severity: a
// finding of any registered check is a problem (D947). A state of a KB that a
// reasonable maintainer may leave as it is is not a check but an observation
// (ObservationSpec), and only kb_review lists it.
type CheckSpec struct {
	Name  string
	Level string
	// FixKinds are the Fix kinds the check's findings may carry; non-empty means
	// kb_repair accepts the check (FixableChecks).
	FixKinds []string
	// AutoRepairSafe: the fix may run with no person (run on write, by the background repair and by `kb repair --apply`, D907).
	AutoRepairSafe bool
	// CrossConcept: the fix touches another file than the finding's own.
	CrossConcept bool
	// OnWrite: evaluated on the write path (CheckConcept for concept-level
	// checks, ScopedCheck for the graph and map ones).
	OnWrite bool
	// Judgement: an unfixed finding becomes a kb_review lint_judgement item.
	Judgement bool

	// AvoidableAtWrite (D907): a write that introduces a finding of this check
	// could have avoided it within the same call, so the write gate refuses it.
	// A check that no write can avoid is false and says why in the
	// classification table of docs/data-plane.md: time (stale_claim), a legacy
	// state (legacy_archive_descriptor), a KB's initial state
	// (missing_instructions), or work another tool does (source_uncited). A
	// check the write path refuses before lint (missing_frontmatter,
	// unparseable_frontmatter, missing_type, concept_too_deep) is false too:
	// refusal is already its gate. Only the OnWrite checks are judged by the
	// gate: it can refuse what the write path evaluates.
	AvoidableAtWrite bool
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
	{Name: "malformed_frontmatter", Level: LevelConcept, OnWrite: true, AvoidableAtWrite: true, Category: CategoryPages},
	{Name: "stringified_list", Level: LevelConcept, OnWrite: true, FixKinds: []string{FixListifyField}, AutoRepairSafe: true, AvoidableAtWrite: true, Category: CategoryPages},
	{Name: "stale_claim", Level: LevelConcept, OnWrite: true, Category: CategoryPages},
	{Name: "status_semantics", Level: LevelConcept, OnWrite: true, AvoidableAtWrite: true, Category: CategoryPages},
	{Name: "machine_path", Level: LevelConcept, OnWrite: true, AvoidableAtWrite: true, Category: CategoryPages},
	{Name: "mangled_placeholder", Level: LevelConcept, OnWrite: true, AvoidableAtWrite: true, Category: CategoryPages},
	{Name: "missing_title", Level: LevelConcept, OnWrite: true, FixKinds: []string{FixSetValue}, AutoRepairSafe: true, AvoidableAtWrite: true, Category: CategoryPages},
	{Name: "title_h1_mismatch", Level: LevelConcept, OnWrite: true, FixKinds: []string{FixSyncH1, FixSetValue}, AutoRepairSafe: true, AvoidableAtWrite: true, Category: CategoryPages},
	{Name: "title_quality", Level: LevelConcept, OnWrite: true, FixKinds: []string{FixSetValue}, AutoRepairSafe: true, AvoidableAtWrite: true, Category: CategoryPages},
	{Name: "missing_required_field", Level: LevelConcept, OnWrite: true, AvoidableAtWrite: true, Category: CategoryPages},
	{Name: "invalid_field_value", Level: LevelConcept, OnWrite: true, FixKinds: []string{FixSetValue}, AutoRepairSafe: true, AvoidableAtWrite: true, Category: CategoryPages},
	{Name: "forbidden_field", Level: LevelConcept, OnWrite: true, AvoidableAtWrite: true, Category: CategoryPages},
	{Name: "nonstandard_field", Level: LevelConcept, OnWrite: true, FixKinds: []string{FixRenameField, FixDropField}, AutoRepairSafe: true, AvoidableAtWrite: true, Category: CategoryPages},
	{Name: "tool_param_field", Level: LevelConcept, OnWrite: true, FixKinds: []string{FixDropField}, AutoRepairSafe: true, AvoidableAtWrite: true, Category: CategoryPages},
	{Name: "prose_value", Level: LevelConcept, OnWrite: true, FixKinds: []string{FixSplitValue}, AutoRepairSafe: true, AvoidableAtWrite: true, Category: CategoryPages},
	{Name: "stale_open", Level: LevelConcept, OnWrite: true, Judgement: true, Category: CategoryPages},
	{Name: "closed_with_open_items", Level: LevelConcept, OnWrite: true, Judgement: true, AvoidableAtWrite: true, Category: CategoryPages},
	{Name: "template_section_missing", Level: LevelConcept, OnWrite: true, Judgement: true, AvoidableAtWrite: true, Category: CategoryTemplates},
	// Templates are closed page schemas a map's pages bind to (D352). Evaluated
	// on a map that declares templates or a default template, which binds every
	// page (D907: no key makes them optional).
	{Name: "template_missing", Level: LevelConcept, OnWrite: true, FixKinds: []string{FixSetValue}, AutoRepairSafe: true, Judgement: true, AvoidableAtWrite: true, Category: CategoryTemplates},
	{Name: "template_unknown", Level: LevelConcept, OnWrite: true, Judgement: true, AvoidableAtWrite: true, Category: CategoryTemplates},
	{Name: "template_not_allowed", Level: LevelConcept, OnWrite: true, Judgement: true, AvoidableAtWrite: true, Category: CategoryTemplates},
	{Name: "template_type_mismatch", Level: LevelConcept, OnWrite: true, FixKinds: []string{FixSetValue}, AutoRepairSafe: true, AvoidableAtWrite: true, Category: CategoryTemplates},
	{Name: "template_field_missing", Level: LevelConcept, OnWrite: true, Judgement: true, AvoidableAtWrite: true, Category: CategoryTemplates},
	{Name: "template_field_value", Level: LevelConcept, OnWrite: true, FixKinds: []string{FixSetValue}, AutoRepairSafe: true, Judgement: true, AvoidableAtWrite: true, Category: CategoryTemplates},
	{Name: "template_extra_section", Level: LevelConcept, OnWrite: true, Judgement: true, AvoidableAtWrite: true, Category: CategoryTemplates},
	{Name: "template_section_alias", Level: LevelConcept, OnWrite: true, FixKinds: []string{FixRenameHeading}, AutoRepairSafe: true, Judgement: true, AvoidableAtWrite: true, Category: CategoryTemplates},
	{Name: "template_section_order", Level: LevelConcept, OnWrite: true, FixKinds: []string{FixReorderSections}, AutoRepairSafe: true, AvoidableAtWrite: true, Category: CategoryTemplates},
	{Name: "open_marker", Level: LevelConcept, OnWrite: true, AvoidableAtWrite: true, Category: CategoryPages},
	{Name: "repeated_link", Level: LevelConcept, OnWrite: true, FixKinds: []string{FixUnlinkRepeat}, AutoRepairSafe: true, AvoidableAtWrite: true, Category: CategoryPages},
	// What the write path refuses about a page, as lint (D356): the repair and
	// the doctor act on it. Not OnWrite: a write that would produce one of them
	// is refused before it lints.
	{Name: "missing_frontmatter", Level: LevelConcept, FixKinds: []string{FixAddFrontmatter}, AutoRepairSafe: true, Judgement: true, Category: CategoryValidity},
	{Name: "unparseable_frontmatter", Level: LevelConcept, FixKinds: []string{FixQuoteValue}, AutoRepairSafe: true, Judgement: true, Category: CategoryValidity},
	{Name: "missing_type", Level: LevelConcept, FixKinds: []string{FixSetValue}, AutoRepairSafe: true, Judgement: true, Category: CategoryValidity},
	{Name: "concept_too_deep", Level: LevelConcept, Judgement: true, Category: CategoryValidity},
	{Name: "nonslug_file_name", Level: LevelConcept, FixKinds: []string{FixMove}, AutoRepairSafe: true, CrossConcept: true, AvoidableAtWrite: true, Category: CategoryValidity},
	{Name: "empty_concept", Level: LevelConcept, Judgement: true, AvoidableAtWrite: true, Category: CategoryValidity},
	// One page, but only a whole-KB run has the inputs (a curation backlog).
	{Name: "imported_draft", Level: LevelConcept, Category: CategoryPages},
	{Name: "secrets_on_non_service", Level: LevelConcept, OnWrite: true, AvoidableAtWrite: true, Category: CategoryPages},
	{Name: "sops_format_mismatch", Level: LevelConcept, OnWrite: true, Panel: true, AvoidableAtWrite: true, Category: CategoryPages},
	{Name: "sops_missing_file", Level: LevelConcept, OnWrite: true, Panel: true, AvoidableAtWrite: true, Category: CategoryPages},
	{Name: "legacy_path", Level: LevelConcept, OnWrite: true, FixKinds: []string{FixReplacePrefix}, AutoRepairSafe: true, AvoidableAtWrite: true, Category: CategoryPages},

	// Links and the graph (ScopedCheck on the write path).
	{Name: "broken_link", Level: LevelGraph, OnWrite: true, FixKinds: []string{FixRebaseLink}, CrossConcept: true, Judgement: true, AvoidableAtWrite: true, Category: CategoryLinks},
	{Name: "duplicate_link", Level: LevelGraph, OnWrite: true, FixKinds: []string{FixDropLinkItem, FixRewriteLinkItem}, AutoRepairSafe: true, AvoidableAtWrite: true, Category: CategoryLinks},
	{Name: "reciprocal_link_item", Level: LevelGraph, OnWrite: true, FixKinds: []string{FixDropLinkItem}, CrossConcept: true, AvoidableAtWrite: true, Category: CategoryLinks},
	{Name: "bare_link_list", Level: LevelGraph, OnWrite: true, Judgement: true, AvoidableAtWrite: true, Category: CategoryLinks},
	{Name: "index_link_form", Level: LevelGraph, OnWrite: true, FixKinds: []string{FixRebaseLink, FixRewriteWikiLink}, AutoRepairSafe: true, CrossConcept: true, AvoidableAtWrite: true, Category: CategoryLinks},
	{Name: "orphan", Level: LevelGraph, OnWrite: true, AvoidableAtWrite: true, Category: CategoryLinks},
	{Name: "broken_relation", Level: LevelGraph, OnWrite: true, AvoidableAtWrite: true, Category: CategoryLinks},
	{Name: "link_to_retired", Level: LevelGraph, OnWrite: true, WholeGraph: true, FixKinds: []string{FixRetargetLinks}, AutoRepairSafe: true, CrossConcept: true, AvoidableAtWrite: true, Category: CategoryLinks},
	{Name: "unknown_placeholder", Level: LevelGraph, OnWrite: true, AvoidableAtWrite: true, Category: CategoryLinks},
	{Name: "forbidden_term", Level: LevelGraph, OnWrite: true, AvoidableAtWrite: true, Category: CategoryLinks},
	{Name: "source_uncited", Level: LevelGraph, Category: CategoryLinks},
	// KB-wide values (D357): the type palette and the spellings in use. Their
	// messages name the KB's types and values, hidden pages' included, so a
	// caller that cannot see the whole KB does not get them (WholeGraph).
	{Name: "unknown_type", Level: LevelGraph, FixKinds: []string{FixSetValue}, Judgement: true, WholeGraph: true, AvoidableAtWrite: true, Category: CategoryValues},
	{Name: "value_case_variant", Level: LevelGraph, FixKinds: []string{FixSetValue}, AutoRepairSafe: true, WholeGraph: true, AvoidableAtWrite: true, Category: CategoryValues},

	// Maps and expanded concepts.
	{Name: "contract_malformed", Level: LevelMap, AvoidableAtWrite: true, Category: CategoryMaps},
	{Name: "legacy_archive_descriptor", Level: LevelMap, Category: CategoryMaps},
	// A removed key of a map's contract (D907): the repair rewrites _map.md.
	{Name: "contract_legacy_key", Level: LevelMap, FixKinds: []string{FixRewriteContract}, AutoRepairSafe: true, Category: CategoryMaps},
	{Name: "index_incomplete", Level: LevelMap, OnWrite: true, AvoidableAtWrite: true, Category: CategoryMaps},
	{Name: "index_lists_retired", Level: LevelMap, WholeGraph: true, FixKinds: []string{FixDropIndexEntry}, AutoRepairSafe: true, AvoidableAtWrite: true, Category: CategoryMaps},
	{Name: "index_stale", Level: LevelMap, Category: CategoryMaps},
	{Name: "expanded_missing_index", Level: LevelMap, AvoidableAtWrite: true, Category: CategoryMaps},
	{Name: "expanded_ambiguous", Level: LevelMap, AvoidableAtWrite: true, Category: CategoryMaps},
	{Name: "expanded_as_category", Level: LevelMap, AvoidableAtWrite: true, Category: CategoryMaps},
	{Name: "unlistable_assets", Level: LevelMap, AvoidableAtWrite: true, Category: CategoryMaps},
	{Name: "oversized_asset", Level: LevelMap, AvoidableAtWrite: true, Category: CategoryMaps},
	{Name: "orphan_asset", Level: LevelMap, Category: CategoryMaps},
	// The data/ tree (D357). unmapped_folder is on a folder, not a page.
	{Name: "unmapped_folder", Level: LevelMap, FixKinds: []string{FixScaffoldMap}, AutoRepairSafe: true, AvoidableAtWrite: true, Category: CategoryMaps},
	{Name: "stray_file", Level: LevelMap, Judgement: true, AvoidableAtWrite: true, Category: CategoryMaps},

	// KB-root artifacts (D316).
	{Name: "skill_invalid", Level: LevelArtifact, Panel: true, AvoidableAtWrite: true, Category: CategoryArtifacts},
	{Name: "skill_warning", Level: LevelArtifact, Panel: true, AvoidableAtWrite: true, Category: CategoryArtifacts},
	// A move, not a file rewrite: kb_repair routes it with the map repairs.
	{Name: "legacy_skill_name", Level: LevelArtifact, Panel: true, FixKinds: []string{FixMoveSkill}, AvoidableAtWrite: true, Category: CategoryArtifacts},
	{Name: "legacy_tool_name", Level: LevelArtifact, Panel: true, FixKinds: []string{FixStripToolPrefix}, AutoRepairSafe: true, AvoidableAtWrite: true, Category: CategoryArtifacts},
	{Name: "skill_broken_ref", Level: LevelArtifact, Panel: true, AvoidableAtWrite: true, Category: CategoryArtifacts},
	{Name: "skill_git_command", Level: LevelArtifact, Panel: true, AvoidableAtWrite: true, Category: CategoryArtifacts},
	{Name: "skill_missing_perimeter", Level: LevelArtifact, Panel: true, AvoidableAtWrite: true, Category: CategoryArtifacts},
	{Name: "cross_kb_path", Level: LevelArtifact, Panel: true, AvoidableAtWrite: true, Category: CategoryArtifacts},
	// A junk file is deleted; missing_instructions has no file to key on.
	{Name: "junk_file", Level: LevelArtifact, Panel: true, AvoidableAtWrite: true, Category: CategoryArtifacts},
	// No write can create a symlink, so the check is not avoidable at write.
	{Name: "symlinked_path", Level: LevelArtifact, Panel: true, Category: CategoryArtifacts},
	{Name: "junk_asset", Level: LevelArtifact, Panel: true, AvoidableAtWrite: true, Category: CategoryArtifacts},
	{Name: "missing_instructions", Level: LevelArtifact, Panel: true, Category: CategoryArtifacts},
	// Unused skills and agents (D1043): the fix is a person's, so no FixKinds,
	// and a write can't avoid it.
	{Name: "artifact_unused", Level: LevelArtifact, Panel: true, Category: CategoryArtifacts},

	// KB-level files.
	{Name: "hook_invalid", Level: LevelKB, Panel: true, AvoidableAtWrite: true, Category: CategoryKB},
	{Name: "missing_registry", Level: LevelKB, Category: CategoryKB},
	{Name: "unused_placeholder", Level: LevelKB, Category: CategoryKB},

	// kb_review kinds (D298): registered so the registry is the one list of
	// names. They are not lint findings, except repeated_fact which a write
	// response also reports (D351), and none is gated (D907): each closes on
	// page state (docs/data-plane.md §Review keys (D298)).
	{Name: ReviewDuplicate, Level: LevelKB},
	{Name: ReviewZombie, Level: LevelKB},
	{Name: ReviewPromotion, Level: LevelKB},
	{Name: ReviewGlossary, Level: LevelKB},
	{Name: ReviewLintJudgement, Level: LevelKB},
	{Name: ReviewRepeatedFact, Level: LevelKB},
	{Name: ReviewReadHotspot, Level: LevelKB},
	{Name: ReviewScatteredWork, Level: LevelKB},
	{Name: ReviewStatusReclassify, Level: LevelKB},
	{Name: ReviewHarvestCandidate, Level: LevelKB},
	{Name: ReviewTemplateProposal, Level: LevelKB},
	{Name: ReviewMapNaming, Level: LevelKB},
	{Name: ReviewObservation, Level: LevelKB},
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
// or a map's index rather than a concept (unmapped_folder, D357: its repair
// writes the folder's descriptor; index_lists_retired, D366: it drops an entry
// from the index; contract_legacy_key, D907: it rewrites the descriptor).
func MapRepairCheck(check string) bool {
	s, ok := Spec(check)
	if !ok {
		return false
	}
	for _, k := range s.FixKinds {
		if k == FixScaffoldMap || k == FixDropIndexEntry || k == FixRewriteContract || k == FixMoveSkill {
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
	return ok && s.Level == LevelArtifact && len(s.FixKinds) > 0 && !MapRepairCheck(check)
}

var unknownCheckOnce sync.Map

// newFinding builds a finding of a registered check: Check is the name. Every
// finding of a check is built here, so the check is declared in one place
// (D354). A name with no spec panics under test, and so does an observation's:
// an observation is never a finding (newObservation builds it for kb_review,
// D947). In production an unknown name is reported as it came and one stderr
// line says the registry is missing it.
func newFinding(check string, f Finding) Finding {
	f.Check = check
	if _, ok := Spec(check); !ok {
		if _, obs := ObservationSpecOf(check); obs && testing.Testing() {
			panic("lint: " + check + " is an observation, not a check: use newObservation")
		}
		if testing.Testing() {
			panic("lint: finding of unregistered check " + check)
		}
		if _, seen := unknownCheckOnce.LoadOrStore(check, true); !seen {
			fmt.Fprintf(os.Stderr, "lint: check %q is not in the registry\n", check)
		}
	}
	return f
}
