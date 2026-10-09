package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// writeRaw puts a file under the map directory exactly as given: the invalid
// pages of D356 cannot be built through the validated write path.
func writeRaw(t *testing.T, k *kb.KB, rel, content string) {
	t.Helper()
	abs := filepath.Join(k.DataRoot(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedInvalidPages is the D356 reproduction, in map "ops" (whose typed pages
// all say Note): no frontmatter, no type, broken YAML, a file name with spaces
// and one with an accent, an empty file, a page four segments deep, plus a page
// linking the space-named one.
func seedInvalidPages(t *testing.T, k *kb.KB) {
	t.Helper()
	writeRaw(t, k, "ops/bare.md", "# Bare Page\n\nBody text.\n")
	writeRaw(t, k, "ops/notype.md", "---\ntitle: No Type\nupdated: 2026-01-02\n---\n# No Type\n\nBody.\n")
	writeRaw(t, k, "ops/broken.md", "---\ntype: Note\ntitle: [unclosed\nupdated: 2026-01-02\n---\n# Broken\n\nBody.\n")
	writeRaw(t, k, "ops/My Page.md", "---\ntype: Note\ntitle: My Page\nupdated: 2026-01-02\n---\n# My Page\n\nBody.\n")
	writeRaw(t, k, "ops/Città.md", "---\ntype: Note\ntitle: Citta\nupdated: 2026-01-02\n---\n# Citta\n\nBody.\n")
	writeRaw(t, k, "ops/empty.md", "")
	writeRaw(t, k, "ops/a/b/deep.md", "---\ntype: Note\ntitle: Deep\nupdated: 2026-01-02\n---\n# Deep\n\nBody.\n")
	writeRaw(t, k, "ops/linker.md", "---\ntype: Note\ntitle: Linker\nupdated: 2026-01-02\n---\n# Linker\n\nSee [[ops/My Page]].\n")
}

func findingsFor(t *testing.T, k *kb.KB, check string) map[string]lint.Finding {
	t.Helper()
	out := map[string]lint.Finding{}
	for _, f := range mustLint(t, k) {
		if f.Check == check {
			out[f.Path] = f
		}
	}
	return out
}

func TestInvalidPagesAreLintChecks(t *testing.T) {
	k, _ := repairKB(t, 2)
	seedInvalidPages(t, k)
	want := map[string]string{
		"ops/bare.md":     "missing_frontmatter",
		"ops/empty.md":    "missing_frontmatter",
		"ops/notype.md":   "missing_type",
		"ops/broken.md":   "unparseable_frontmatter",
		"ops/a/b/deep.md": "concept_too_deep",
		"ops/My Page.md":  "nonslug_file_name",
		"ops/Città.md":    "nonslug_file_name",
		"ops/empty.md ":   "empty_concept",
	}
	for path, check := range want {
		path = strings.TrimSpace(path)
		if _, ok := findingsFor(t, k, check)[path]; !ok {
			t.Errorf("%s: no %s finding", path, check)
		}
	}
	// Fixes where the answer is unique.
	if fx := findingsFor(t, k, "missing_frontmatter")["ops/bare.md"].Fix; fx == nil || fx.Kind != lint.FixAddFrontmatter || fx.To != "Note" {
		t.Errorf("bare fix = %+v", fx)
	}
	if fx := findingsFor(t, k, "missing_type")["ops/notype.md"].Fix; fx == nil || fx.Kind != lint.FixSetValue || fx.Field != "type" || fx.To != "Note" {
		t.Errorf("notype fix = %+v", fx)
	}
	if fx := findingsFor(t, k, "unparseable_frontmatter")["ops/broken.md"].Fix; fx == nil || fx.Kind != lint.FixQuoteValue || fx.Field != "title" {
		t.Errorf("broken fix = %+v", fx)
	}
	if fx := findingsFor(t, k, "concept_too_deep")["ops/a/b/deep.md"].Fix; fx != nil {
		t.Errorf("a too-deep page must carry no fix: %+v", fx)
	}
	if fx := findingsFor(t, k, "nonslug_file_name")["ops/Città.md"].Fix; fx == nil || fx.Kind != lint.FixMove || fx.To != "ops/citta" {
		t.Errorf("accent move = %+v", fx)
	}
	if fx := findingsFor(t, k, "empty_concept")["ops/empty.md"].Fix; fx != nil {
		t.Errorf("an empty page must carry no fix: %+v", fx)
	}
	// Errors, not acceptable.
	for _, c := range []string{"missing_frontmatter", "unparseable_frontmatter", "missing_type", "concept_too_deep"} {
		if s, _ := lint.Spec(c); s.Severity != lint.SevError || s.Accept != lint.AcceptNone {
			t.Errorf("%s: %+v", c, s)
		}
	}
}

// A frontmatter block with delimiters but no key is missing_type, not
// missing_frontmatter.
func TestEmptyFrontmatterBlockIsMissingType(t *testing.T) {
	k, _ := repairKB(t, 2)
	writeRaw(t, k, "ops/hollow.md", "---\n\n---\n# Hollow\n\nBody.\n")
	if _, ok := findingsFor(t, k, "missing_type")["ops/hollow.md"]; !ok {
		t.Error("no missing_type")
	}
	if _, ok := findingsFor(t, k, "missing_frontmatter")["ops/hollow.md"]; ok {
		t.Error("a block with delimiters is not missing_frontmatter")
	}
}

func TestGateCheckListsEachInvalidPageOnce(t *testing.T) {
	k, s := repairKB(t, 2)
	seedInvalidPages(t, k)
	_ = k
	out := decodeJSON(t, mustText(t, s, "gate_check", `{"changed_ids":[]}`))
	if out["pass"] != false {
		t.Fatalf("pass = %v", out["pass"])
	}
	validated := map[string]bool{}
	for _, e := range out["validation_errors"].([]interface{}) {
		validated[e.(map[string]interface{})["path"].(string)] = true
	}
	for _, p := range []string{"ops/bare.md", "ops/notype.md", "ops/broken.md"} {
		if !validated[p] {
			t.Errorf("validate did not report %s", p)
		}
	}
	for _, f := range out["lint_findings"].([]interface{}) {
		m := f.(map[string]interface{})
		switch m["check"] {
		case "missing_frontmatter", "unparseable_frontmatter", "missing_type":
			if validated[m["path"].(string)] {
				t.Errorf("%v is reported by validate and by lint", m)
			}
		}
	}
	// concept_too_deep is not a validate error: lint is its only voice.
	found := false
	for _, f := range out["lint_findings"].([]interface{}) {
		found = found || f.(map[string]interface{})["check"] == "concept_too_deep"
	}
	if !found {
		t.Error("concept_too_deep missing from gate_check")
	}
}

func TestKBRepairFixesInvalidPages(t *testing.T) {
	k, s := repairKB(t, 2)
	seedInvalidPages(t, k)
	for _, check := range []string{"missing_frontmatter", "missing_type", "unparseable_frontmatter"} {
		out := repairCall(t, s, `{"check":"`+check+`","dry_run":false}`)
		if out["applied"].(float64) < 1 {
			t.Fatalf("%s: %v", check, out)
		}
	}
	cd, _ := k.ReadConcept("ops/bare")
	if !strings.Contains(cd.FrontmatterRaw, "type: Note") || !strings.Contains(cd.FrontmatterRaw, "title: Bare Page") || !strings.Contains(cd.Body, "Body text.") {
		t.Errorf("bare = %q / %q", cd.FrontmatterRaw, cd.Body)
	}
	cd, _ = k.ReadConcept("ops/empty")
	if !strings.Contains(cd.FrontmatterRaw, "title: empty") {
		t.Errorf("empty page: title not derived from the stem: %q", cd.FrontmatterRaw)
	}
	cd, _ = k.ReadConcept("ops/notype")
	if !strings.Contains(cd.FrontmatterRaw, "type: Note") || !strings.Contains(cd.FrontmatterRaw, "title: No Type") {
		t.Errorf("notype = %q", cd.FrontmatterRaw)
	}
	cd, _ = k.ReadConcept("ops/broken")
	fm, err := okf.ParseFrontmatter(cd.FrontmatterRaw)
	if err != nil {
		t.Fatalf("broken still unparseable: %v\n%s", err, cd.FrontmatterRaw)
	}
	if v, _ := fm.Get("title"); v != "[unclosed" || strings.Join(fm.Keys(), ",") != "type,title,updated" {
		t.Errorf("broken keys = %v title = %v", fm.Keys(), v)
	}
	for _, check := range []string{"missing_frontmatter", "unparseable_frontmatter", "missing_type"} {
		if got := findingsFor(t, k, check); len(got) != 0 {
			t.Errorf("%s still reported: %v", check, got)
		}
	}
}

func TestKBRepairMovesNonSlugFilesAndRewritesBacklinks(t *testing.T) {
	k, s := repairKB(t, 2)
	seedInvalidPages(t, k)
	// A page whose slug is already taken: no fix, the message names it.
	writeRaw(t, k, "ops/Taken Name.md", "---\ntype: Note\ntitle: Mine\nupdated: 2026-01-02\n---\n# Mine\n\nBody.\n")
	writeRaw(t, k, "ops/taken-name.md", "---\ntype: Note\ntitle: Taken\nupdated: 2026-01-02\n---\n# Taken\n\nBody.\n")
	f := findingsFor(t, k, "nonslug_file_name")["ops/Taken Name.md"]
	if f.Fix != nil || !strings.Contains(f.Message, "ops/taken-name") {
		t.Fatalf("collision finding = %+v", f)
	}
	out := repairCall(t, s, `{"check":"nonslug_file_name","dry_run":false}`)
	if out["applied"].(float64) != 2 {
		t.Fatalf("applied = %v (%v)", out["applied"], out)
	}
	for _, id := range []okf.ConceptID{"ops/citta", "ops/my-page", "ops/taken-name"} {
		if _, err := k.ReadConcept(id); err != nil {
			t.Errorf("%s: %v", id, err)
		}
	}
	if _, err := k.ReadConcept("ops/Città"); !errors.Is(err, okf.ErrNotFound) {
		t.Errorf("source survived: %v", err)
	}
	if cd, err := k.ReadConcept("ops/Taken Name"); err != nil || !strings.Contains(cd.Body, "Mine") {
		t.Errorf("the colliding page was moved or lost: %v", err)
	}
	if cd, _ := k.ReadConcept("ops/taken-name"); !strings.Contains(cd.Body, "Taken") {
		t.Errorf("the existing page was overwritten: %q", cd.Body)
	}
	cd, _ := k.ReadConcept("ops/linker")
	if strings.Contains(cd.Body, "My Page") || !strings.Contains(cd.Body, "ops/my-page") {
		t.Errorf("backlink not rewritten: %q", cd.Body)
	}
}

// An empty page is a finding for the doctor, never deleted.
func TestEmptyConceptIsReviewedNeverDeleted(t *testing.T) {
	k, s := repairKB(t, 2)
	seedInvalidPages(t, k)
	k.AutoRepair = config.DefaultAutoRepair
	stagedAutoRepair(context.Background(), k, config.DefaultAutoRepair, 50)
	if _, err := os.Stat(filepath.Join(k.DataRoot(), "ops", "empty.md")); err != nil {
		t.Fatalf("empty page deleted: %v", err)
	}
	rev := decodeReview(t, callTool(t, s, "kb_review", `{"kind":"lint_judgement"}`))
	checks := map[string]string{}
	for _, it := range rev.Items {
		checks[it.Check] = strings.Join(it.Concepts, ",")
	}
	if checks["empty_concept"] != "ops/empty" {
		t.Errorf("review items = %v", checks)
	}
	if checks["concept_too_deep"] != "ops/a/b/deep" {
		t.Errorf("review items = %v", checks)
	}
}

// The heartbeat repairs the reproduction in one run, and a page whose type
// cannot be resolved is left alone and reviewed.
func TestHeartbeatRepairsInvalidPagesAndLeavesUnresolvedTypes(t *testing.T) {
	k, s := repairKB(t, 2)
	seedInvalidPages(t, k)
	// A map whose pages disagree on the type: nothing resolves there.
	if err := k.CreateMapWithContract("mix", "Mix", "map", nil, "", kb.MapContract{}); err != nil {
		t.Fatal(err)
	}
	writeRaw(t, k, "mix/a.md", "---\ntype: Note\ntitle: A\n---\n# A\n\nBody.\n")
	writeRaw(t, k, "mix/b.md", "---\ntype: Task\ntitle: B\n---\n# B\n\nBody.\n")
	writeRaw(t, k, "mix/c.md", "---\ntitle: C\n---\n# C\n\nBody.\n")
	// Baseline rule: an unrelated fix lands on a page that has no type.
	writeRaw(t, k, "mix/d.md", "---\ntitle: D\nupdated: 2026-01-02\n---\n# D\n\nBody.\n")
	r := stagedAutoRepair(context.Background(), k, config.DefaultAutoRepair, 50)
	for _, c := range []string{"missing_frontmatter", "missing_type", "unparseable_frontmatter", "nonslug_file_name"} {
		if r.applied[c] == 0 {
			t.Errorf("heartbeat applied nothing for %s: %v", c, r.applied)
		}
	}
	if got := findingsFor(t, k, "missing_type"); len(got) != 2 || got["mix/c.md"].Fix != nil {
		t.Errorf("missing_type left = %v", got)
	}
	cd, _ := k.ReadConcept("mix/c")
	if strings.Contains(cd.FrontmatterRaw, "type:") {
		t.Errorf("an unresolved type was guessed: %q", cd.FrontmatterRaw)
	}
	cd, _ = k.ReadConcept("mix/d")
	if strings.Contains(cd.FrontmatterRaw, "updated:") || !strings.Contains(cd.FrontmatterRaw, "timestamp:") {
		t.Errorf("the nonstandard_field fix did not land on a page without type: %q", cd.FrontmatterRaw)
	}
	rev := decodeReview(t, callTool(t, s, "kb_review", `{"kind":"lint_judgement"}`))
	seen := false
	for _, it := range rev.Items {
		seen = seen || (it.Check == "missing_type" && strings.Join(it.Concepts, ",") == "mix/c")
	}
	if !seen {
		t.Errorf("no lint_judgement item for the unresolved type: %+v", rev.Items)
	}
}

// Repair never widens the error set, and an agent write keeps full validation.
func TestRepairConceptNeverWidensErrorsAndAgentWritesStayStrict(t *testing.T) {
	k, s := repairKB(t, 2)
	seedInvalidPages(t, k)
	typeless := newFM()
	typeless.Set("title", "No Type")
	// Already without a type: staying that way is allowed for a repair…
	if _, err := k.RepairConcept("ops/notype", typeless, "# No Type\n", ""); err != nil {
		t.Errorf("repair of an already-typeless page: %v", err)
	}
	// …but not for a page that has one: that would be a new error.
	cd, _ := k.ReadConcept("ops/n0")
	if _, err := k.RepairConcept("ops/n0", typeless, cd.Body, cd.ContentHash); err == nil {
		t.Error("RepairConcept dropped the type of a valid page")
	}
	// Never creates a path.
	typed := newFM()
	typed.Set("type", "Note")
	if _, err := k.RepairConcept("ops/brand-new", typed, "# N\n", ""); !errors.Is(err, okf.ErrNotFound) {
		t.Errorf("RepairConcept created a path: %v", err)
	}
	// The agent-facing write refuses the same typeless content.
	res := callTool(t, s, "concept_write", `{"id":"ops/notype","frontmatter":{"title":"No Type"},"body":"# No Type\n"}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "type") {
		t.Errorf("concept_write accepted an invalid concept: %+v", res)
	}
	if _, err := k.WriteConcept("ops/notype", typeless, "# No Type\n", ""); err == nil {
		t.Error("WriteConcept accepted a typeless page")
	}
	// A page too deep keeps its path under repair and is refused by a write.
	if _, err := k.WriteConcept("ops/a/b/deep", typed, "# D\n", ""); err == nil {
		t.Error("WriteConcept accepted a four-segment path")
	}
	if _, err := k.RepairConcept("ops/a/b/deep", typed, "# D\n", ""); err != nil {
		t.Errorf("repair of a too-deep page: %v", err)
	}
}

func TestQuoteBrokenValueOnlyWhenUnique(t *testing.T) {
	for name, raw := range map[string]string{
		"multi-line list": "type: Note\ntags: [a,\n  b\ntitle: T",
		"two broken":      "type: Note\ntitle: [x\ntags: [y",
	} {
		if fixed, _, ok := lint.QuoteBrokenValue(raw); ok {
			t.Errorf("%s: quoted anyway: %q", name, fixed)
		}
	}
	fixed, key, ok := lint.QuoteBrokenValue("type: Note\ntitle: [say \"hi\" \\ x\nupdated: 1")
	if !ok || key != "title" {
		t.Fatalf("not fixed: %q %q", fixed, key)
	}
	fm, err := okf.ParseFrontmatter(fixed)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := fm.Get("title"); v != `[say "hi" \ x` {
		t.Errorf("title = %q", v)
	}
	b, _ := json.Marshal(fm.Keys())
	if string(b) != `["type","title","updated"]` {
		t.Errorf("keys = %s", b)
	}
}
