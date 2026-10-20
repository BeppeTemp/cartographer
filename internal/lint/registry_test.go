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
// as the regression: the derived tables must hold exactly these names. The
// suppression tables went with lint_ignore and lint_accept (D907);
// contract_legacy_key is the one fix added since.
var legacyTables = map[string]string{
	"wholeGraph":    `index_lists_retired link_to_retired unknown_type value_case_variant`,
	"lintJudgement": `bare_link_list broken_link closed_with_open_items concept_too_deep empty_concept missing_frontmatter missing_type stale_open stray_file template_extra_section template_field_missing template_field_value template_missing template_not_allowed template_section_alias template_section_missing template_unknown unknown_type unparseable_frontmatter`,
	"fixable":       `broken_link contract_legacy_key duplicate_link index_link_form index_lists_retired invalid_field_value legacy_path legacy_tool_name link_to_retired missing_frontmatter missing_title missing_type nonslug_file_name nonstandard_field prose_value reciprocal_link_item repeated_link stringified_list template_field_value template_missing template_section_alias template_section_order template_type_mismatch title_h1_mismatch title_quality tool_param_field unknown_type unmapped_folder unparseable_frontmatter value_case_variant`,
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
	t.Parallel()
	got := map[string][]string{
		"wholeGraph":    sortedKeys(WholeGraphChecks),
		"lintJudgement": sortedKeys(lintJudgementChecks),
		"fixable":       append([]string(nil), FixableChecks...),
	}
	for table, want := range legacyTables {
		if !reflect.DeepEqual(got[table], strings.Fields(want)) {
			t.Errorf("%s drifted from the legacy table:\n got  %v\n want %v", table, got[table], strings.Fields(want))
		}
	}
}

func TestRegistry_Invariants(t *testing.T) {
	t.Parallel()
	levels := map[string]bool{LevelConcept: true, LevelMap: true, LevelGraph: true, LevelArtifact: true, LevelKB: true}
	seen := map[string]bool{}
	for _, s := range Checks() {
		if seen[s.Name] {
			t.Errorf("%s registered twice", s.Name)
		}
		seen[s.Name] = true
		if !levels[s.Level] {
			t.Errorf("%s: level %q", s.Name, s.Level)
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

func TestNewFinding_RegisteredChecksOnly(t *testing.T) {
	t.Parallel()
	if f := newFinding("broken_link", Finding{Path: "a.md"}); f.Check != "broken_link" {
		t.Errorf("check name: %+v", f)
	}
	for _, name := range []string{"no_such_check", "cut_concept"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("a finding of %q must panic under test: it is not a registered check", name)
				}
			}()
			newFinding(name, Finding{})
		}()
	}
	defer func() {
		if recover() == nil {
			t.Error("an observation of a registered check must panic under test")
		}
	}()
	newObservation("broken_link", Finding{})
}

// TestNoSeverityAnywhere pins D947: a lint finding is a problem, so nothing a
// reader receives carries a severity, however it is named.
func TestNoSeverityAnywhere(t *testing.T) {
	t.Parallel()
	for _, v := range []any{Finding{}, CheckSpec{}, ObservationSpec{}, ReviewItem{}} {
		rt := reflect.TypeOf(v)
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if strings.Contains(strings.ToLower(f.Name), "severity") || strings.Contains(strings.ToLower(f.Tag.Get("json")), "severity") {
				t.Errorf("%s has a severity field: %s", rt.Name(), f.Name)
			}
		}
	}
}

// TestEveryCheckIsLintOrObservation pins the D947 classification: a name is a
// lint check (a definite fix: a state of the KB that makes the finding go and is
// always better), an observation (no single right answer: only kb_review lists
// it, and it names the page state that closes its item), or a kb_review kind. It
// is never two of them.
func TestEveryCheckIsLintOrObservation(t *testing.T) {
	t.Parallel()
	review := map[string]bool{}
	for _, k := range ReviewKinds {
		review[k] = true
	}
	names := map[string]string{}
	for _, c := range Checks() {
		kind := "lint check"
		if review[c.Name] {
			kind = "review kind"
		}
		names[c.Name] = kind
	}
	for _, o := range Observations() {
		if prev, dup := names[o.Name]; dup {
			t.Errorf("%s is an observation and a %s", o.Name, prev)
		}
		names[o.Name] = "observation"
		if o.Why == "" || o.Closes == "" || o.Advice == "" {
			t.Errorf("observation %s must say why it is not a problem, what closes its item and what to do: %+v", o.Name, o)
		}
		if categoryOrder(o.Category) == len(Categories) {
			t.Errorf("observation %s has category %q, not one of %v", o.Name, o.Category, Categories)
		}
		if _, ok := Spec(o.Name); ok {
			t.Errorf("observation %s is also a registered check", o.Name)
		}
	}
	// What D947 moved out of the lint, by reason: an observation, or covered
	// by a kb_review kind that already existed. None is a check any more.
	for _, gone := range []string{"cut_concept", "island", "map_misfit", "concept_oversize", "map_oversize", "facet_sprawl", "missing_value_contract", "map_without_templates", "artifact_unused"} {
		if _, ok := Spec(gone); ok {
			t.Errorf("%s left the lint in D947 but is registered as a check", gone)
		}
	}
}

// TestNoFindingLiteralOutsideRegistry pins decision 3 of D354: a finding of a
// check is built by newFinding, so the registry knows its check. A literal
// that sets Check bypasses the registry.
func TestNoFindingLiteralOutsideRegistry(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	}
	for id, content := range pages {
		writeFile(t, root, id+".md", content)
	}
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	key := func(f Finding) string { return f.Check + "|" + f.Message }
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

// Every lint check has a reader's category (D365); kb_review kinds have none.
func TestRegistryCategories(t *testing.T) {
	t.Parallel()
	known := map[string]bool{}
	for _, c := range Categories {
		known[c] = true
	}
	review := map[string]bool{}
	for _, k := range ReviewKinds {
		review[k] = true
	}
	for _, s := range Checks() {
		switch {
		case review[s.Name] || s.Name == ReviewRepeatedFact || s.Name == ReviewTemplateProposal:
			if s.Category != "" {
				t.Errorf("%s is a review kind but has category %q", s.Name, s.Category)
			}
		case !known[s.Category]:
			t.Errorf("%s has category %q, not one of %v", s.Name, s.Category, Categories)
		}
	}
}
