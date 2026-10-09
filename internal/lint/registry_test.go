package lint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// The legacy tables, as they were hard-coded before the registry (D354), kept
// as the regression: the derived tables must hold exactly these names.
var legacyTables = map[string]string{
	"perConcept":         `bare_link_list broken_link broken_relation closed_with_open_items concept_oversize cut_concept duplicate_candidate duplicate_link empty_concept forbidden_term glossary_gap harvest_candidate imported_draft island legacy_path link_to_retired lint_judgement machine_path malformed_frontmatter mangled_placeholder map_misfit map_naming missing_title nonslug_file_name nonstandard_field open_marker orphan promotion_candidate prose_value read_hotspot reciprocal_link_item repeated_fact repeated_link scattered_work secrets_on_non_service sops_format_mismatch sops_missing_file source_uncited stale_claim stale_open status_reclassify status_semantics stray_file stringified_list template_section_missing title_h1_mismatch title_quality unknown_placeholder unknown_type value_case_variant zombie_work`,
	"mapOnly":            `facet_sprawl index_lists_retired map_oversize missing_value_contract`,
	"wholeGraph":         `cut_concept index_lists_retired island link_to_retired map_misfit`,
	"lintJudgement":      `bare_link_list broken_link closed_with_open_items concept_oversize concept_too_deep empty_concept map_misfit missing_frontmatter missing_type stale_open stray_file template_section_missing unknown_type unparseable_frontmatter`,
	"artifactChecks":     `artifact_unused cross_kb_path junk_asset junk_file legacy_tool_name missing_instructions skill_broken_ref skill_git_command skill_invalid skill_missing_perimeter skill_warning`,
	"artifactAcceptable": `artifact_unused cross_kb_path legacy_path legacy_tool_name skill_broken_ref skill_git_command skill_invalid skill_missing_perimeter skill_warning sops_format_mismatch sops_missing_file`,
	"fixable":            `broken_link duplicate_link index_link_form invalid_field_value legacy_path legacy_tool_name missing_frontmatter missing_title missing_type nonslug_file_name nonstandard_field prose_value reciprocal_link_item repeated_link stringified_list title_h1_mismatch tool_param_field unknown_type unmapped_folder unparseable_frontmatter value_case_variant`,
}

// legacyAcceptability is CheckAcceptability for every check name known before
// the registry (no_such_check is unknown, hence none).
var legacyAcceptability = map[string]string{
	"concept":  `bare_link_list broken_link broken_relation closed_with_open_items concept_oversize cut_concept duplicate_candidate duplicate_link empty_concept forbidden_term glossary_gap harvest_candidate imported_draft island legacy_path link_to_retired lint_judgement machine_path malformed_frontmatter mangled_placeholder map_misfit map_naming missing_title nonslug_file_name nonstandard_field open_marker orphan promotion_candidate prose_value read_hotspot reciprocal_link_item repeated_fact repeated_link scattered_work secrets_on_non_service sops_format_mismatch sops_missing_file source_uncited stale_claim stale_open status_reclassify status_semantics stray_file stringified_list template_section_missing title_h1_mismatch title_quality unknown_placeholder unknown_type value_case_variant zombie_work`,
	"map":      `facet_sprawl index_lists_retired map_oversize missing_value_contract`,
	"artifact": `artifact_unused cross_kb_path legacy_tool_name skill_broken_ref skill_git_command skill_invalid skill_missing_perimeter skill_warning`,
	"none":     `concept_too_deep contract_malformed expanded_ambiguous expanded_as_category expanded_missing_index forbidden_field hook_invalid index_incomplete index_link_form index_stale invalid_field_value junk_asset junk_file legacy_archive_descriptor lint_ignore_invalid missing_frontmatter missing_instructions missing_registry missing_required_field missing_type no_such orphan_asset oversized_asset tool_param_field unlistable_assets unmapped_folder unparseable_frontmatter unused_placeholder`,
}

func sortedKeys(m map[string]bool) []string {
	var out []string
	for k, v := range m {
		if v {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func TestRegistry_DerivedTablesMatchLegacy(t *testing.T) {
	artifactAcceptableNow := map[string]bool{}
	for _, s := range Checks() {
		artifactAcceptableNow[s.Name] = artifactAcceptable(s.Name)
	}
	got := map[string][]string{
		"perConcept":         sortedKeys(perConceptChecks),
		"mapOnly":            sortedKeys(mapOnlyIgnorable),
		"wholeGraph":         sortedKeys(WholeGraphChecks),
		"lintJudgement":      sortedKeys(lintJudgementChecks),
		"artifactChecks":     sortedKeys(artifactChecks),
		"artifactAcceptable": sortedKeys(artifactAcceptableNow),
		"fixable":            append([]string(nil), FixableChecks...),
	}
	for table, want := range legacyTables {
		if !reflect.DeepEqual(got[table], strings.Fields(want)) {
			t.Errorf("%s drifted from the legacy table:\n got  %v\n want %v", table, got[table], strings.Fields(want))
		}
	}
	for want, names := range legacyAcceptability {
		for _, name := range strings.Fields(names) {
			if got := CheckAcceptability(name); got != want {
				t.Errorf("CheckAcceptability(%q) = %q, want %q", name, got, want)
			}
		}
	}
	if CheckAcceptability("no_such_check") != AcceptNone {
		t.Error("an unregistered check must not be acceptable")
	}
}

func TestRegistry_Invariants(t *testing.T) {
	levels := map[string]bool{LevelConcept: true, LevelMap: true, LevelGraph: true, LevelArtifact: true, LevelKB: true}
	accepts := map[string]bool{AcceptConcept: true, AcceptMap: true, AcceptArtifact: true, AcceptNone: true}
	seen := map[string]bool{}
	for _, s := range Checks() {
		if seen[s.Name] {
			t.Errorf("%s registered twice", s.Name)
		}
		seen[s.Name] = true
		if !ValidSeverity(s.Severity) {
			t.Errorf("%s: severity %q", s.Name, s.Severity)
		}
		if !levels[s.Level] || !accepts[s.Accept] {
			t.Errorf("%s: level %q / accept %q", s.Name, s.Level, s.Accept)
		}
		// D306: an error is never suppressible, and so never acceptable.
		if s.Severity == SevError {
			if s.Accept != AcceptNone {
				t.Errorf("%s: an error cannot be accepted (D306)", s.Name)
			}
			if suppressed(Finding{Check: s.Name, Severity: SevError}, map[string]bool{s.Name: true}) {
				t.Errorf("%s: an error was suppressed", s.Name)
			}
		}
		if s.NeverSuppress && suppressed(Finding{Check: s.Name, Severity: s.Severity}, map[string]bool{s.Name: true}) {
			t.Errorf("%s: NeverSuppress was suppressed", s.Name)
		}
		if (s.AutoRepairSafe || s.CrossConcept) && len(s.FixKinds) == 0 {
			t.Errorf("%s: auto-repair / cross-concept without a fix", s.Name)
		}
		if s.Judgement && s.Level == LevelKB {
			t.Errorf("%s: a kb_review kind is not a lint judgement", s.Name)
		}
		if s.OnWrite && s.Level != LevelConcept && s.Level != LevelGraph && s.Level != LevelMap {
			t.Errorf("%s: OnWrite at level %q has no write-path evaluator", s.Name, s.Level)
		}
		for _, k := range s.FixKinds {
			if k == "" {
				t.Errorf("%s: empty fix kind", s.Name)
			}
		}
	}
	for _, name := range FixableChecks {
		if s, _ := Spec(name); len(s.FixKinds) == 0 {
			t.Errorf("%s is fixable without FixKinds", name)
		}
	}
	// A Review* kind is registered, whatever the kb_review generators add.
	for _, kind := range ReviewKinds {
		if _, ok := Spec(kind); !ok {
			t.Errorf("review kind %q is not registered", kind)
		}
	}
}

func TestNewFinding_SeverityFromSpec(t *testing.T) {
	if f := newFinding("broken_link", Finding{Path: "a.md"}); f.Check != "broken_link" || f.Severity != SevWarning {
		t.Errorf("default severity: %+v", f)
	}
	if f := newFinding("broken_link", Finding{Path: "a.md", Severity: SevInfo}); f.Severity != SevInfo {
		t.Errorf("explicit override must win: %+v", f)
	}
	defer func() {
		if recover() == nil {
			t.Error("a finding of an unregistered check must panic under test")
		}
	}()
	newFinding("no_such_check", Finding{})
}

// TestNoFindingLiteralOutsideRegistry pins decision 3 of D354: a finding of a
// check is built by newFinding, so its severity comes from the spec. A literal
// that sets Check bypasses the registry.
func TestNoFindingLiteralOutsideRegistry(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || name == "registry.go" {
			continue
		}
		af, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			cl, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			// Finding{…}, or an element of []Finding{{…}} whose type is elided.
			isFinding := false
			if id, ok := cl.Type.(*ast.Ident); ok && id.Name == "Finding" {
				isFinding = true
			}
			if at, ok := cl.Type.(*ast.ArrayType); ok {
				if id, ok := at.Elt.(*ast.Ident); ok && id.Name == "Finding" {
					for _, e := range cl.Elts {
						if c, ok := e.(*ast.CompositeLit); ok && c.Type == nil && setsCheck(c) {
							t.Errorf("%s: Finding literal sets Check; use newFinding", fset.Position(c.Pos()))
						}
					}
				}
			}
			if isFinding && setsCheck(cl) {
				t.Errorf("%s: Finding literal sets Check; use newFinding", fset.Position(cl.Pos()))
			}
			return true
		})
	}
}

func setsCheck(cl *ast.CompositeLit) bool {
	for _, e := range cl.Elts {
		if kv, ok := e.(*ast.KeyValueExpr); ok {
			if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "Check" {
				return true
			}
		}
	}
	return false
}

// TestCheckConceptAgreesWithRun pins the D354 invariant: the write path and a
// whole-KB run give the same page-level findings for the same page. For every
// concept of a KB that trips most of them, CheckConcept equals the OnWrite
// LevelConcept subset of Run's findings for that path.
func TestCheckConceptAgreesWithRun(t *testing.T) {
	k := tempKB(t)
	root := k.DataRoot()
	writeFile(t, root, "ops/_map.md", "---\ntype: Map\nkind: map\ntitle: Ops\nrequired_fields: [owner]\nforbidden_fields: [secret_note]\nfield_values.status: [open, done]\n---\n# Ops\n")
	writeFile(t, root, "plain/_map.md", "---\ntype: Map\nkind: map\ntitle: Plain\n---\n# Plain\n")
	pages := map[string]string{
		"ops/clean":     "---\ntype: Note\ntitle: Clean\nowner: me\nstatus: open\ntimestamp: 2026-01-01\n---\n# Clean\n\nNothing wrong.\n",
		"ops/untitled":  "---\ntype: Note\nowner: me\n---\n# Heading\n\nbody\n",
		"ops/mismatch":  "---\ntype: Note\ntitle: One\nowner: me\n---\n# Another\n",
		"ops/badvalue":  "---\ntype: Note\ntitle: B\nowner: me\nstatus: weird\nsecret_note: x\n---\n# B\n",
		"ops/noowner":   "---\ntype: Note\ntitle: N\n---\n# N\n",
		"ops/synonym":   "---\ntype: Note\ntitle: S\nowner: me\nupdated: 2026-01-01\nbody: oops\n---\n# S\n",
		"ops/stale":     "---\ntype: Note\ntitle: St\nowner: me\nreview_after: 2001-01-01\n---\n# St\n\nSee /Users/someone/work/notes for details.\n",
		"ops/stringy":   "---\ntype: Note\ntitle: L\nowner: me\ntags: \"[a, b]\"\n---\n# L\n",
		"ops/malformed": "---\ntype: Note\ntitle: M\nowner: me\nrelated: x\n  - y\n---\n# M\n",
		"ops/mangled":   "---\ntype: Note\ntitle: P\nowner: me\n---\n# P\n\nPath {{repo:}} and {{path:x\n",
		"ops/openmark":  "---\ntype: Note\ntitle: O\nowner: me\n---\n# O\n\n- [ ] one\n- [ ] two\n",
		"ops/big":       "---\ntype: Note\ntitle: Big\nowner: me\n---\n# Big\n\n" + strings.Repeat("padding line\n", 6000),
		"plain/imp":     "---\ntype: Note\ntitle: Imp\nstatus: imported\nsecrets_source: [x]\n---\n# Imp\n",
		"plain/noparse": "no frontmatter at all\n",
		"plain/ignored": "---\ntype: Note\ntitle: Ig\nupdated: 2026-01-01\nlint_ignore: [nonstandard_field]\n---\n# Ig\n",
	}
	for id, content := range pages {
		writeFile(t, root, id+".md", content)
	}
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	key := func(f Finding) string { return f.Check + "|" + f.Severity + "|" + f.Message }
	seen := map[string]bool{}
	for id, content := range pages {
		path := okf.IDToPath(okf.ConceptID(id))
		var want []string
		for _, f := range findings {
			if s, ok := Spec(f.Check); f.Path == path && ok && s.Level == LevelConcept && s.OnWrite {
				want = append(want, key(f))
			}
		}
		var got []string
		for _, f := range CheckConcept(k, okf.ConceptID(id), content) {
			got = append(got, key(f))
			seen[f.Check] = true
		}
		sort.Strings(want)
		sort.Strings(got)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: CheckConcept disagrees with Run\n write: %q\n run:   %q", id, got, want)
		}
	}
	// The fixture must exercise the shared evaluator, not agree on nothing.
	for _, check := range []string{"missing_title", "title_h1_mismatch", "invalid_field_value", "missing_required_field", "forbidden_field", "nonstandard_field", "tool_param_field", "stale_claim", "machine_path", "stringified_list", "malformed_frontmatter"} {
		if !seen[check] {
			t.Errorf("fixture never produced %s on the write path", check)
		}
	}
	// What the write path leaves out stays out (no wire change).
	for id, content := range pages {
		for _, f := range CheckConcept(k, okf.ConceptID(id), content) {
			if s, _ := Spec(f.Check); !s.OnWrite {
				t.Errorf("%s: CheckConcept returned %s, which is not OnWrite", id, f.Check)
			}
		}
	}
}
