package lint

import (
	"fmt"
	"strings"
	"testing"
)

func findingFor(findings []Finding, path, check string) *Finding {
	for i := range findings {
		if findings[i].Path == path && findings[i].Check == check {
			return &findings[i]
		}
	}
	return nil
}

func TestNonstandardField(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "kb-a/alone.md", "---\ntype: Note\ntitle: A\naggiornato: 2026-01-01\n---\nx\n")
	writeFile(t, k.DataRoot(), "kb-a/both.md", "---\ntype: Note\ntitle: B\nupdated: 2026-01-01\ntimestamp: 2026-02-01\n---\nx\n")
	writeFile(t, k.DataRoot(), "kb-a/clean.md", "---\ntype: Note\ntitle: C\ntimestamp: 2026-02-01\n---\nx\n")
	writeFile(t, k.DataRoot(), "kb-a/quiet.md", "---\ntype: Note\ntitle: Q\nstato: open\nlint_ignore: [nonstandard_field]\n---\nx\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	f := findingFor(findings, "kb-a/alone.md", "nonstandard_field")
	if f == nil || f.Severity != SevWarning || f.Fix == nil || *f.Fix != (Fix{Kind: FixRenameField, Field: "aggiornato", To: "timestamp"}) {
		t.Fatalf("alone: %+v", f)
	}
	f = findingFor(findings, "kb-a/both.md", "nonstandard_field")
	if f == nil || f.Fix != nil || !strings.Contains(f.Message, "has both") {
		t.Fatalf("both: %+v", f)
	}
	if hasCheck(findings, "kb-a/clean.md", "nonstandard_field") || hasCheck(findings, "kb-a/quiet.md", "nonstandard_field") {
		t.Fatalf("clean or ignored concept reported: %+v", findings)
	}
}

func TestNonstandardField_RequiredByMap(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "kb-a/_map.md", "---\ntype: Map\nkind: map\ntitle: A\nrequired_fields: [aggiornato]\n---\n# A\n")
	writeFile(t, k.DataRoot(), "kb-a/x.md", "---\ntype: Note\ntitle: X\naggiornato: 2026-01-01\n---\nx\n")
	findings, _ := Run(k, "", false)
	f := findingFor(findings, "kb-a/x.md", "nonstandard_field")
	if f == nil || !strings.Contains(f.Message, "requires") || f.Fix == nil {
		t.Fatalf("%+v", f)
	}
}

func TestStandardFieldSynonymsDisjoint(t *testing.T) {
	seen := map[string]string{}
	for std, syns := range StandardFieldSynonyms {
		for _, s := range syns {
			if s != strings.ToLower(s) {
				t.Errorf("synonym %q must be lower-case", s)
			}
			if prev, dup := seen[s]; dup {
				t.Errorf("synonym %q listed under %q and %q", s, prev, std)
			}
			if _, isStd := StandardFieldSynonyms[s]; isStd {
				t.Errorf("synonym %q is itself a standard field", s)
			}
			seen[s] = std
		}
	}
}

func TestToolParamField(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "kb-a/bad.md", "---\ntype: Note\ntitle: B\nif_match: abc\nlint_ignore: [tool_param_field]\n---\nx\n")
	findings, _ := Run(k, "", false)
	f := findingFor(findings, "kb-a/bad.md", "tool_param_field")
	if f == nil || f.Severity != SevWarning || f.Fix == nil || *f.Fix != (Fix{Kind: FixDropField, Field: "if_match"}) {
		t.Fatalf("%+v", f)
	}
	// Not suppressible: naming it in lint_ignore is itself reported.
	if !hasCheck(findings, "kb-a/bad.md", "lint_ignore_invalid") {
		t.Errorf("lint_ignore of tool_param_field should be flagged invalid")
	}
}

func valueContractKB(t *testing.T, mapFM string, statuses []string) string {
	return valueContractKBField(t, mapFM, statuses, "status")
}

func valueContractKBField(t *testing.T, mapFM string, values []string, field string) string {
	t.Helper()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "kb-a/_map.md", "---\ntype: Map\nkind: map\ntitle: A\n"+mapFM+"---\n# A\n")
	for i, v := range values {
		writeFile(t, k.DataRoot(), fmt.Sprintf("kb-a/t%d.md", i), fmt.Sprintf("---\ntype: Task\ntitle: T%d\n%s: %s\n---\nx\n", i, field, v))
	}
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if f := findingFor(findings, "kb-a/_map.md", "missing_value_contract"); f != nil {
		if f.Severity != SevInfo || f.Fix != nil {
			t.Errorf("bad severity/fix: %+v", f)
		}
		return f.Message
	}
	return ""
}

func TestMissingValueContract(t *testing.T) {
	five := []string{"open", "open", "done", "done", "done"}
	msg := valueContractKB(t, "", five)
	if !strings.Contains(msg, "field_values.Task.status: [done, open]") || !strings.Contains(msg, "done ×3, open ×2") {
		t.Fatalf("typed suggestion: %q", msg)
	}
	if msg := valueContractKB(t, "", five[:4]); msg != "" {
		t.Errorf("below threshold fired: %q", msg)
	}
	if msg := valueContractKB(t, "field_values.Task.status: [open, done]\n", five); msg != "" {
		t.Errorf("typed contract declared, still fired: %q", msg)
	}
	if msg := valueContractKB(t, "field_values.status: [open, done]\n", five); msg != "" {
		t.Errorf("map-wide contract declared, still fired: %q", msg)
	}
	// A non-status field with more than 8 distinct values is skipped.
	var many []string
	for i := 0; i < 9; i++ {
		many = append(many, fmt.Sprintf("v%d", i), fmt.Sprintf("v%d", i))
	}
	if msg := valueContractKBField(t, "", many, "phase"); msg != "" {
		t.Errorf("more than 8 distinct values fired for non-status field: %q", msg)
	}
	// But status bypasses the cap (D295): 10 distinct values still fires.
	if msg := valueContractKB(t, "", many); msg == "" {
		t.Error("status with 9 distinct values should still fire (no cap)")
	}
}

func TestMissingValueContract_MapWideWhenTypesMixed(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "kb-a/_map.md", "---\ntype: Map\nkind: map\ntitle: A\n---\n# A\n")
	for i := 0; i < 6; i++ {
		typ := "Task"
		if i%2 == 0 {
			typ = "Incident"
		}
		writeFile(t, k.DataRoot(), fmt.Sprintf("kb-a/c%d.md", i), fmt.Sprintf("---\ntype: %s\ntitle: C%d\nphase: p%d\n---\nx\n", typ, i, i%2))
	}
	findings, _ := Run(k, "", false)
	f := findingFor(findings, "kb-a/_map.md", "missing_value_contract")
	if f == nil || !strings.Contains(f.Message, "field_values.phase: [p0, p1]") {
		t.Fatalf("%+v", f)
	}
}

func TestMissingValueContract_SkipsStandardAndSynonyms(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "kb-a/_map.md", "---\ntype: Map\nkind: map\ntitle: A\n---\n# A\n")
	for i := 0; i < 6; i++ {
		writeFile(t, k.DataRoot(), fmt.Sprintf("kb-a/c%d.md", i), "---\ntype: Note\ntitle: same\nstato: open\ndescription: same\n---\nx\n")
	}
	findings, _ := Run(k, "", false)
	if hasCheck(findings, "kb-a/_map.md", "missing_value_contract") {
		t.Fatalf("title/description/synonym must not be suggested: %+v", findings)
	}
}

func TestCheckConcept(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "infra/_map.md", "---\ntype: Map\nkind: map\ntitle: I\nfield_values.status: [active, draft]\nrequired_fields: [owner]\n---\n# I\n")
	found := CheckConcept(k, "infra/x", "---\ntype: Note\nstatus: nope\nstato: x\n---\n# Heading\n")
	checks := map[string]bool{}
	for _, f := range found {
		checks[f.Check] = true
	}
	for _, want := range []string{"invalid_field_value", "missing_required_field", "missing_title", "nonstandard_field"} {
		if !checks[want] {
			t.Errorf("missing %s in %+v", want, found)
		}
	}
	// lint_ignore applies to warnings, never to errors.
	found = CheckConcept(k, "infra/x", "---\ntype: Note\nstatus: nope\nlint_ignore: [missing_title]\n---\nb\n")
	for _, f := range found {
		if f.Check == "missing_title" {
			t.Errorf("ignored check reported")
		}
	}
	if len(CheckConcept(k, "infra/ok", "---\ntype: Note\ntitle: T\nowner: me\nstatus: draft\n---\nb\n")) != 0 {
		t.Errorf("clean concept has findings")
	}
}

func TestNonstandardField_TimestampSynonymNeedsDateValue(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "kb-a/payload.md", "---\ntype: Note\ntitle: P\ndata: some payload\n---\nx\n")
	writeFile(t, k.DataRoot(), "kb-a/list.md", "---\ntype: Note\ntitle: L\ndate: [a, b]\n---\nx\n")
	writeFile(t, k.DataRoot(), "kb-a/day.md", "---\ntype: Note\ntitle: D\ndata: 2026-01-02\n---\nx\n")
	writeFile(t, k.DataRoot(), "kb-a/rfc.md", "---\ntype: Note\ntitle: R\nupdated: 2026-01-02T10:00:00Z\n---\nx\n")
	writeFile(t, k.DataRoot(), "kb-a/state.md", "---\ntype: Note\ntitle: S\nstato: anything\n---\nx\n")
	findings, err := Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"kb-a/payload.md", "kb-a/list.md"} {
		if hasCheck(findings, p, "nonstandard_field") {
			t.Errorf("%s: non-date value flagged", p)
		}
	}
	for p, field := range map[string]string{"kb-a/day.md": "data", "kb-a/rfc.md": "updated"} {
		f := findingFor(findings, p, "nonstandard_field")
		if f == nil || f.Fix == nil || *f.Fix != (Fix{Kind: FixRenameField, Field: field, To: "timestamp"}) {
			t.Errorf("%s: %+v", p, f)
		}
	}
	if f := findingFor(findings, "kb-a/state.md", "nonstandard_field"); f == nil || f.Fix == nil {
		t.Errorf("non-timestamp synonyms keep their behaviour: %+v", f)
	}
}

func TestMalformedFrontmatter_Detected(t *testing.T) {
	k := tempKB(t)
	// Scalar followed by indented list lines — the parser truncates the value.
	writeFile(t, k.DataRoot(), "kb-a/bad.md", "---\ntype: Note\ntitle: Bad\nprovenance: \"- a\"\n  - b\n  - c\n---\nx\n")
	writeFile(t, k.DataRoot(), "kb-a/good.md", "---\ntype: Note\ntitle: Good\nprovenance:\n  - a\n  - b\n---\nx\n")
	findings, _ := Run(k, "", false)
	if f := findingFor(findings, "kb-a/bad.md", "malformed_frontmatter"); f == nil {
		t.Fatal("malformed_frontmatter not detected")
	} else if f.Severity != SevWarning {
		t.Errorf("wrong severity: %s", f.Severity)
	}
	if hasCheck(findings, "kb-a/good.md", "malformed_frontmatter") {
		t.Error("false positive on well-formed block list")
	}
}

func TestMalformedFrontmatter_NotSuppressible(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "kb-a/ign.md", "---\ntype: Note\ntitle: I\nlint_ignore: [malformed_frontmatter]\nprovenance: \"- a\"\n  - b\n---\nx\n")
	findings, _ := Run(k, "", false)
	if !hasCheck(findings, "kb-a/ign.md", "malformed_frontmatter") {
		t.Error("malformed_frontmatter should not be suppressible")
	}
}

func TestMissingValueContract_ProvenanceExcluded(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "kb-a/_map.md", "---\ntype: Map\nkind: map\ntitle: A\n---\n# A\n")
	for i := 0; i < 6; i++ {
		writeFile(t, k.DataRoot(), fmt.Sprintf("kb-a/c%d.md", i), "---\ntype: Note\ntitle: same\nprovenance: same\ntags: same\nresource: same\nsecrets_source: same\n---\nx\n")
	}
	findings, _ := Run(k, "", false)
	if hasCheck(findings, "kb-a/_map.md", "missing_value_contract") {
		t.Errorf("provenance/tags/resource/secrets_source should be excluded: %+v", findings)
	}
}

func TestMissingValueContract_StatusNoCap(t *testing.T) {
	// Status with 10 distinct values should still fire (D295 WP3).
	var many []string
	for i := 0; i < 10; i++ {
		many = append(many, fmt.Sprintf("s%d", i), fmt.Sprintf("s%d", i))
	}
	msg := valueContractKB(t, "", many)
	if msg == "" {
		t.Error("status with 10 distinct values should fire (no cap)")
	}
}

// A link may name an expanded concept by its index file or a map by its own
// index.md: neither is a walked concept ID, and neither is broken. Pins the
// fallback the D294 enumeration lost (one finding per such link on a real KB).
func TestBrokenLink_IndexFormTargetsResolve(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "kb-a/index.md", "---\ntype: Index\ntitle: A\n---\n# A\n")
	writeFile(t, k.DataRoot(), "kb-a/exp/index.md", "---\ntype: Note\ntitle: E\n---\n# E\n")
	writeFile(t, k.DataRoot(), "kb-a/exp/sat.md", "---\ntype: Note\ntitle: S\n---\nUp: [e](index.md), map: [a](../index.md), [[kb-a/exp/index]].\n")
	findings, _ := Run(k, "", false)
	if hasCheck(findings, "kb-a/exp/sat.md", "broken_link") {
		t.Fatalf("index-form links reported broken: %+v", findings)
	}
}

// An indented continuation line is never taken for a key of its own.
func TestMalformedFrontmatter_OneFindingPerKey(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "kb-a/bad.md", "---\ntype: Note\ntitle: Bad\nprovenance: \"- a\"\n  - see https://example.com\n  - b\n---\nx\n")
	findings, _ := Run(k, "", false)
	n := 0
	for _, f := range findings {
		if f.Check == "malformed_frontmatter" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want 1 malformed_frontmatter, got %d", n)
	}
}

// D313: a purely date-valued field holds instants, not a vocabulary.
func TestMissingValueContract_DateFieldSkipped(t *testing.T) {
	dates := []string{"2026-01-01", "2026-01-01", "2026-01-02", "2026-01-02", "2026-01-03", "2026-01-03"}
	if msg := valueContractKBField(t, "", dates, "claimed_at"); msg != "" {
		t.Errorf("date-shaped field proposed a vocabulary: %q", msg)
	}
	mixed := append(append([]string{}, dates...), "whenever", "whenever")
	if msg := valueContractKBField(t, "", mixed, "claimed_at"); msg == "" {
		t.Error("a field with a non-date value must still fire")
	}
}
