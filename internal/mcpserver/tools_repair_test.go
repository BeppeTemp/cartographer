package mcpserver

import (
	"encoding/json"
	"fmt"
	"github.com/BeppeTemp/cartographer/internal/execbit"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// repairKB builds a git KB with a map "ops" whose contract requires the synonym
// "updated", holding n concepts that use it.
func repairKB(t *testing.T, n int) (*kb.KB, *Server) {
	t.Helper()
	k, _ := setupGitKB(t)
	if err := k.CreateMapWithContract("ops", "Ops", "map", nil, "", kb.MapContract{RequiredFields: []string{"updated"}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		fm := newFM()
		fm.Set("type", "Note")
		fm.Set("title", fmt.Sprintf("N%d", i))
		fm.Set("updated", "2026-01-02")
		fm.Set("tags", []string{"a"})
		if _, err := k.WriteConcept(okf.ConceptID(fmt.Sprintf("ops/n%d", i)), fm, "# T\n", ""); err != nil {
			t.Fatal(err)
		}
	}
	k.AutoCommit = true
	if _, err := k.CommitOp("test: seed"); err != nil {
		t.Fatal(err)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	return k, s
}

func commitCount(t *testing.T, k *kb.KB) int {
	t.Helper()
	out, err := exec.Command("git", "-C", k.Root, "rev-list", "--count", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	var n int
	fmt.Sscan(string(out), &n)
	return n
}

func repairCall(t *testing.T, s *Server, args string) map[string]any {
	t.Helper()
	res := callTool(t, s, "kb_repair", args)
	if res.IsError {
		t.Fatalf("kb_repair: %s", res.Content[0].Text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestKBRepairDryRunWritesNothing(t *testing.T) {
	k, s := repairKB(t, 3)
	before := commitCount(t, k)
	out := repairCall(t, s, `{"check":"nonstandard_field"}`)
	if out["dry_run"] != true || out["planned_total"].(float64) != 3 || out["applied"].(float64) != 0 {
		t.Fatalf("dry run response = %v", out)
	}
	if st, _ := gitx.Status(k.Root); strings.TrimSpace(st) != "" {
		t.Fatalf("dry run left a dirty tree: %q", st)
	}
	if commitCount(t, k) != before {
		o, _ := exec.Command("git", "-C", k.Root, "show", "--stat", "HEAD").Output()
		t.Fatalf("dry run committed: %s", o)
	}
}

func TestKBRepairRenamesInOneCommitAndRewritesContract(t *testing.T) {
	k, s := repairKB(t, 3)
	before := commitCount(t, k)
	out := repairCall(t, s, `{"check":"nonstandard_field","dry_run":false}`)
	if out["applied"].(float64) != 3 {
		t.Fatalf("applied = %v (%v)", out["applied"], out)
	}
	if commitCount(t, k) != before+1 {
		t.Fatalf("want exactly one commit, got %d", commitCount(t, k)-before)
	}
	subject, _ := exec.Command("git", "-C", k.Root, "log", "-1", "--format=%s").Output()
	if got := strings.TrimSpace(string(subject)); got != "kb_repair: nonstandard_field (3 concepts)" {
		t.Fatalf("subject = %q", got)
	}
	cd, err := k.ReadConcept("ops/n0")
	if err != nil {
		t.Fatal(err)
	}
	fm, _ := okf.ParseFrontmatter(cd.FrontmatterRaw)
	if _, ok := fm.Get("updated"); ok {
		t.Fatal("old key survived")
	}
	if v, _ := fm.Get("timestamp"); v != "2026-01-02" {
		t.Fatalf("timestamp = %v", v)
	}
	if keys := fm.Keys(); keys[2] != "timestamp" || keys[3] != "tags" {
		t.Fatalf("key position not kept: %v", keys)
	}
	c, err := k.ReadMapContract("ops")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.RequiredFields) != 1 || c.RequiredFields[0] != "timestamp" {
		t.Fatalf("contract required_fields = %v", c.RequiredFields)
	}
	if got := lastDoctorDate(k); got != "" {
		t.Fatalf("kb_repair must not stamp the doctor marker, got %q", got)
	}

	// Idempotent: a second run applies nothing and commits nothing.
	n := commitCount(t, k)
	out = repairCall(t, s, `{"check":"nonstandard_field","dry_run":false}`)
	if out["applied"].(float64) != 0 || commitCount(t, k) != n {
		t.Fatalf("second run applied %v, commits +%d", out["applied"], commitCount(t, k)-n)
	}
}

func TestKBRepairSkipsStaleConcept(t *testing.T) {
	k, _ := repairKB(t, 2)
	targets, _, err := planRepair(k, "nonstandard_field", "")
	if err != nil || len(targets) != 2 {
		t.Fatalf("plan = %v, %v", targets, err)
	}
	// Someone edits ops/n0 between listing and applying.
	cd, _ := k.ReadConcept("ops/n0")
	fm, _ := okf.ParseFrontmatter(cd.FrontmatterRaw)
	fm.Set("title", "Edited")
	if _, err := k.WriteConcept("ops/n0", fm, cd.Body, ""); err != nil {
		t.Fatal(err)
	}
	applied, skipped := applyRepair(k, targets, false)
	if len(applied) != 1 || applied[0].Path != "ops/n1.md" {
		t.Fatalf("applied = %v", applied)
	}
	if len(skipped) != 1 || skipped[0].Path != "ops/n0.md" || !strings.Contains(skipped[0].Reason, "stale_write") {
		t.Fatalf("skipped = %v", skipped)
	}
	cd, _ = k.ReadConcept("ops/n0")
	if !strings.Contains(cd.FrontmatterRaw, "title: Edited") || !strings.Contains(cd.FrontmatterRaw, "updated:") {
		t.Fatalf("stale concept was overwritten:\n%s", cd.FrontmatterRaw)
	}
}

func TestKBRepairDropFieldAndLimit(t *testing.T) {
	k, s := repairKB(t, 0)
	for i := 0; i < 3; i++ {
		fm := newFM()
		fm.Set("type", "Note")
		fm.Set("title", "T")
		fm.Set("op", "x") // a write-tool parameter stored as a field
		if _, err := k.WriteConcept(okf.ConceptID(fmt.Sprintf("ops/d%d", i)), fm, "# T\n", ""); err != nil {
			t.Fatal(err)
		}
	}
	out := repairCall(t, s, `{"check":"tool_param_field","dry_run":false,"limit":2}`)
	if out["applied"].(float64) != 2 {
		t.Fatalf("limit: applied = %v", out["applied"])
	}
	cd, _ := k.ReadConcept("ops/d0")
	if strings.Contains(cd.FrontmatterRaw, "op:") {
		t.Fatalf("field not dropped:\n%s", cd.FrontmatterRaw)
	}
	cd, _ = k.ReadConcept("ops/d2")
	if !strings.Contains(cd.FrontmatterRaw, "op:") {
		t.Fatal("limit exceeded")
	}
}

func TestKBRepairRejectsChecksWithoutFix(t *testing.T) {
	_, s := repairKB(t, 1)
	for _, check := range []string{"orphan", "no_such_check", ""} {
		res := callTool(t, s, "kb_repair", fmt.Sprintf(`{"check":%q}`, check))
		if !res.IsError || !strings.Contains(res.Content[0].Text, "nonstandard_field") || !strings.Contains(res.Content[0].Text, "lint") {
			t.Fatalf("check %q: %+v", check, res)
		}
	}
}

// FixableChecks is the tool's contract: every check that emits a fix must be in
// it, or kb_repair would refuse a repair lint advertises.
func TestFixableChecksCoverEveryEmittedFix(t *testing.T) {
	k, _ := repairKB(t, 1)
	fm := newFM()
	fm.Set("type", "Note")
	fm.Set("title", "P")
	fm.Set("body", "x")
	if _, err := k.WriteConcept("ops/p", fm, "# P\n", ""); err != nil {
		t.Fatal(err)
	}
	seedBodyFixes(t, k)
	seedLegacyFixes(t, k)
	sf := newFM()
	sf.Set("type", "Note")
	sf.Set("title", "S")
	sf.Set("provenance", "[a, b]")
	if _, err := k.WriteConcept("ops/stringified", sf, "# S\n", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := k.UpdateMapContract("ops", kb.MapContractUpdate{FieldValues: map[string][]string{"status": {"done"}}}); err != nil {
		t.Fatal(err)
	}
	for id, st := range map[string]string{"ops/v1": "Completato", "ops/v2": "done — with prose"} {
		vf := newFM()
		vf.Set("type", "Note")
		vf.Set("title", "V")
		vf.Set("updated", "2026-01-02")
		vf.Set("status", st)
		if _, err := k.WriteConcept(okf.ConceptID(id), vf, "# V\n", ""); err != nil {
			t.Fatal(err)
		}
	}
	findings, err := lint.Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, c := range lint.FixableChecks {
		listed[c] = true
	}
	seen := map[string]bool{}
	for _, f := range findings {
		if f.Fix != nil {
			seen[f.Check] = true
			if !listed[f.Check] {
				t.Errorf("check %q emits a fix but is not in lint.FixableChecks", f.Check)
			}
		}
	}
	if len(seen) != len(listed) {
		t.Errorf("fixture did not exercise every fixable check: saw %v", seen)
	}
}

func TestFrontmatterRenameKeepsPosition(t *testing.T) {
	fm := newFM()
	fm.Set("a", "1")
	fm.Set("b", "2")
	fm.Set("c", "3")
	if !fm.Rename("b", "z") || fm.Rename("b", "y") || fm.Rename("a", "c") {
		t.Fatal("Rename result wrong")
	}
	if got := strings.Join(fm.Keys(), ","); got != "a,z,c" {
		t.Fatalf("keys = %s", got)
	}
	if v, _ := fm.Get("z"); v != "2" {
		t.Fatalf("z = %v", v)
	}
	fm.Delete("a")
	if v, _ := fm.Get("z"); v != "2" {
		t.Fatal("index broken after Rename+Delete")
	}
}

// --- kb_status.conformance (D290) ---

func TestSummarizeConformanceTruthTable(t *testing.T) {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	f := func(check, sev string, fix bool) lint.Finding {
		x := lint.Finding{Check: check, Severity: sev}
		if fix {
			x.Fix = &lint.Fix{Kind: lint.FixDropField}
		}
		return x
	}
	warn := []lint.Finding{f("broken_link", lint.SevWarning, false)}
	info := []lint.Finding{f("map_misfit", lint.SevInfo, false)}
	cases := []struct {
		name     string
		findings []lint.Finding
		review   int
		last     string
		interval int
		want     bool
	}{
		{"clean, never run", nil, 0, "", 14, false},
		{"unrelated finding ignored", []lint.Finding{f("orphan", lint.SevWarning, false)}, 0, "", 14, false},
		{"warning, never run", warn, 0, "", 14, true},
		{"warning, run recently", warn, 0, "2026-09-30", 14, false},
		{"fixable, run long ago", []lint.Finding{f("tool_param_field", lint.SevInfo, true)}, 0, "2026-09-01", 14, true},
		{"info, interval just passed", info, 0, "2026-09-17", 14, true},
		{"info, a day short", info, 0, "2026-09-18", 14, false},
		{"review only, due", nil, 3, "2026-08-01", 14, true},
		{"debt, interval off", warn, 5, "", 0, false},
	}
	for _, c := range cases {
		got := summarizeConformance(c.findings, c.review, c.last, c.interval, now)
		if got["doctor_suggested"] != c.want {
			t.Errorf("%s: doctor_suggested = %v, want %v", c.name, got["doctor_suggested"], c.want)
		}
	}
	if got := summarizeConformance(warn, 0, "2026-09-20", 14, now); got["next_doctor"] != "2026-10-04" {
		t.Errorf("next_doctor = %v, want 2026-10-04", got["next_doctor"])
	}
	got := summarizeConformance([]lint.Finding{
		f("nonstandard_field", lint.SevWarning, true), f("nonstandard_field", lint.SevWarning, false),
		f("link_to_retired", lint.SevInfo, false), f("orphan", lint.SevWarning, false),
	}, 0, "2026-09-20", 14, now)
	// D313: the accept level of every counted check, none for an error-only or
	// unlisted one.
	acc := got["acceptability"].(map[string]string)
	if acc["nonstandard_field"] != "concept" || acc["link_to_retired"] != "concept" || len(acc) != 2 {
		t.Fatalf("acceptability = %v", acc)
	}
	if m := summarizeConformance([]lint.Finding{f("missing_value_contract", lint.SevInfo, false)}, 0, "", 14, now)["acceptability"].(map[string]string); m["missing_value_contract"] != "map" {
		t.Fatalf("map-only check: %v", m)
	}
	sev := got["findings"].(map[string]int)
	if sev[lint.SevWarning] != 2 || sev[lint.SevInfo] != 1 || got["fixable"] != 1 || got["last_doctor"] != "2026-09-20" {
		t.Fatalf("summary = %v", got)
	}
}

func TestKBStatusConformance(t *testing.T) {
	k, s := repairKB(t, 2)
	k.DoctorIntervalDays = 14
	status := func() map[string]any {
		res := callTool(t, s, "kb_status", `{}`)
		var out map[string]any
		if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
			t.Fatal(err)
		}
		return out["conformance"].(map[string]any)
	}
	c := status()
	if c["fixable"].(float64) != 2 || c["doctor_suggested"] != true {
		t.Fatalf("conformance = %v", c)
	}
	if _, ok := c["last_doctor"]; ok {
		t.Fatal("last_doctor without a marker")
	}
	if err := k.AppendLog("kb-doctor: warnings 2 -> 0", time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err := k.AppendLog("unrelated entry", time.Date(2026, 5, 6, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if got := lastDoctorDate(k); got != "2026-03-04" {
		t.Fatalf("last_doctor = %q", got)
	}
	repairCall(t, s, `{"check":"nonstandard_field","dry_run":false}`)
	c = status()
	if c["fixable"].(float64) != 0 || c["last_doctor"] != "2026-03-04" {
		t.Fatalf("after repair: %v", c)
	}
}

// kb_status runs a whole-KB lint on every call (D290): the benchmark is the
// measurement the decision records.
func BenchmarkKBStatusConformance1000(b *testing.B) {
	dir := b.TempDir()
	k, err := kb.Init(dir)
	if err != nil {
		b.Fatal(err)
	}
	for m := 0; m < 10; m++ {
		name := fmt.Sprintf("m%d", m)
		if err := k.CreateMapWithContract(name, name, "map", nil, "", kb.MapContract{}); err != nil {
			b.Fatal(err)
		}
		for i := 0; i < 100; i++ {
			fm := newFM()
			fm.Set("type", "Note")
			fm.Set("title", fmt.Sprintf("C%d", i))
			fm.Set("updated", "2026-01-02")
			body := fmt.Sprintf("# C\n\nSee [next](c%d.md).\n", (i+1)%100)
			if _, err := k.WriteConcept(okf.ConceptID(fmt.Sprintf("%s/c%d", name, i)), fm, body, ""); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f, err := lint.Run(k, "", false)
		if err != nil {
			b.Fatal(err)
		}
		_ = summarizeConformance(f, 0, lastDoctorDate(k), 14, time.Now())
	}
}

func newFM() *okf.Frontmatter {
	fm, _ := okf.ParseFrontmatter("")
	return fm
}

// seedBodyFixes writes an expanded concept whose index still carries links
// written for the pre-expansion file (rebase_link), plus a links section that
// repeats a link of the text as a bare item (drop_link_item).
func seedBodyFixes(t *testing.T, k *kb.KB) {
	t.Helper()
	dir := filepath.Join(k.DataRoot(), "ops", "e")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\ntype: Note\ntitle: E\nupdated: 2026-01-02\n---\n# E\n\nSee [n0](n0.md) and [[ops/n0]].\n\n## Links\n\n- [[ops/n0]]\n- [[ops/n0]] — the reason it matters\n"
	if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// D310: a link spelled <concept>/index, in both syntaxes.
	lk := "---\ntype: Note\ntitle: LK\nupdated: 2026-01-02\n---\nSee [e](e/index.md) and [[ops/e/index|the e]].\n"
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "ops", "lk.md"), []byte(lk), 0o644); err != nil {
		t.Fatal(err)
	}
	// D301: ops/r1 lists ops/r2, which links back (reciprocal_link_item);
	// it also lists ops/r3, which does not.
	for id, b := range map[string]string{
		"r1": "# R1\n\n## Links\n\n- [[ops/r2]]\n- [[ops/r3]]\n",
		"r2": "# R2\n\nDepends on [[ops/r1]].\n",
		"r3": "# R3\n",
	} {
		c := "---\ntype: Note\ntitle: " + id + "\nupdated: 2026-01-02\n---\n" + b
		if err := os.WriteFile(filepath.Join(k.DataRoot(), "ops", id+".md"), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestKBRepairReciprocalLinkItem: the item goes only when the target links
// back, through the usual dry-run plan, and the check is no conformance debt.
func TestKBRepairReciprocalLinkItem(t *testing.T) {
	k, s := repairKB(t, 0)
	seedBodyFixes(t, k)
	if conformanceChecks["reciprocal_link_item"] {
		t.Fatal("reciprocal_link_item counted as conformance debt")
	}
	plan := repairCall(t, s, `{"check":"reciprocal_link_item"}`)
	if plan["planned_total"].(float64) != 1 {
		t.Fatalf("plan = %v", plan)
	}
	if cd, _ := k.ReadConcept("ops/r1"); !strings.Contains(cd.Body, "- [[ops/r2]]") {
		t.Fatal("dry run wrote")
	}
	repairCall(t, s, `{"check":"reciprocal_link_item","dry_run":false}`)
	cd, err := k.ReadConcept("ops/r1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cd.Body, "[[ops/r2]]") || !strings.Contains(cd.Body, "- [[ops/r3]]") {
		t.Fatalf("after apply:\n%s", cd.Body)
	}
}

// TestKBRepair_ReciprocalLinkItem_MutualPairSkipsSecondSide (D309): should
// the lint ever flag both sides of a mutual pair again, the repair drops one
// item and keeps the other, so the edge survives.
func TestKBRepair_ReciprocalLinkItem_MutualPairSkipsSecondSide(t *testing.T) {
	k, _ := repairKB(t, 0)
	var targets []repairTarget
	for id, other := range map[string]string{"pa": "pb", "pb": "pa"} {
		fm := newFM()
		fm.Set("type", "Note")
		fm.Set("title", id)
		item := "- [[ops/" + other + "]]"
		hash, err := k.WriteConcept(okf.ConceptID("ops/"+id), fm, "# "+id+"\n\n## Links\n\n"+item+"\n", "")
		if err != nil {
			t.Fatal(err)
		}
		targets = append(targets, repairTarget{ID: okf.ConceptID("ops/" + id), Path: "ops/" + id + ".md", Hash: hash,
			Fixes: []*lint.Fix{{Kind: lint.FixDropLinkItem, Field: item}}})
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Path < targets[j].Path })
	applied, skipped := applyRepair(k, targets, true)
	if len(applied) != 1 || applied[0].Path != "ops/pa.md" {
		t.Fatalf("applied = %v", applied)
	}
	if len(skipped) != 1 || skipped[0].Path != "ops/pb.md" || !strings.Contains(skipped[0].Reason, "mutual pair: other side already removed") {
		t.Fatalf("skipped = %v", skipped)
	}
	if cd, _ := k.ReadConcept("ops/pb"); !strings.Contains(cd.Body, "- [[ops/pa]]") {
		t.Fatalf("second side dropped:\n%s", cd.Body)
	}
}

func TestKBRepairBodyFixes(t *testing.T) {
	k, s := repairKB(t, 1)
	seedBodyFixes(t, k)
	plan := repairCall(t, s, `{"check":"broken_link"}`)
	if plan["planned_total"].(float64) != 1 {
		t.Fatalf("broken_link plan = %v", plan)
	}
	repairCall(t, s, `{"check":"broken_link","dry_run":false}`)
	repairCall(t, s, `{"check":"duplicate_link","dry_run":false}`)
	cd, err := k.ReadConcept("ops/e")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cd.Body, "[n0](../n0.md)") {
		t.Errorf("link not rebased:\n%s", cd.Body)
	}
	if strings.Contains(cd.Body, "\n- [[ops/n0]]\n") {
		t.Errorf("bare duplicate item not dropped:\n%s", cd.Body)
	}
	if !strings.Contains(cd.Body, "the reason it matters") || !strings.Contains(cd.Body, "## Links") {
		t.Errorf("item with a reason or heading lost:\n%s", cd.Body)
	}
	findings, _ := lint.Run(k, "ops", false)
	for _, f := range findings {
		if f.Check == "broken_link" && f.Path == "ops/e.md" {
			t.Errorf("still broken after repair: %+v", f)
		}
	}
}

func TestKBRepairValueFixes(t *testing.T) {
	k, s := repairKB(t, 0)
	if _, err := k.UpdateMapContract("ops", kb.MapContractUpdate{FieldValues: map[string][]string{"status": {"done", "in-progress"}}}); err != nil {
		t.Fatal(err)
	}
	for id, st := range map[string]string{"ops/a": "Completato", "ops/b": "done — after the second pass"} {
		fm := newFM()
		fm.Set("type", "Note")
		fm.Set("title", "T")
		fm.Set("updated", "2026-01-02")
		fm.Set("status", st)
		if _, err := k.WriteConcept(okf.ConceptID(id), fm, "# T\n\nbody\n", ""); err != nil {
			t.Fatal(err)
		}
	}
	repairCall(t, s, `{"check":"invalid_field_value","dry_run":false}`)
	repairCall(t, s, `{"check":"prose_value","dry_run":false}`)
	a, _ := k.ReadConcept("ops/a")
	if !strings.Contains(a.FrontmatterRaw, "status: done") {
		t.Errorf("set_value not applied:\n%s", a.FrontmatterRaw)
	}
	b, _ := k.ReadConcept("ops/b")
	if !strings.Contains(b.FrontmatterRaw, "status: done\n") && !strings.HasSuffix(strings.TrimSpace(b.FrontmatterRaw), "status: done") {
		t.Errorf("split_value field:\n%s", b.FrontmatterRaw)
	}
	if !strings.Contains(b.Body, "# T\n\n> status: after the second pass\n") {
		t.Errorf("prose not kept in body:\n%s", b.Body)
	}
	if out := repairCall(t, s, `{"check":"prose_value"}`); out["planned_total"].(float64) != 0 {
		t.Errorf("second run must plan nothing: %v", out)
	}
}

// TestKBRepairSynonymGroups pins two traps found on a real KB. Two synonyms of
// one standard field are one decision: equal values collapse into it,
// different values are left for a person, and in both cases the concept's
// other fixes still apply. And a limited dry run still reports the whole job.
func TestKBRepairSynonymGroups(t *testing.T) {
	k, s := repairKB(t, 0)
	write := func(id string, kv ...string) {
		fm := newFM()
		fm.Set("type", "Note")
		fm.Set("title", "T")
		for i := 0; i < len(kv); i += 2 {
			fm.Set(kv[i], kv[i+1])
		}
		if _, err := k.WriteConcept(okf.ConceptID(id), fm, "# T\n", ""); err != nil {
			t.Fatal(err)
		}
	}
	write("ops/same", "date", "2026-01-02", "modified", "2026-01-02", "sources", "a")
	write("ops/differ", "date", "2026-01-02", "modified", "2026-02-03", "sources", "b")

	dry := repairCall(t, s, `{"check":"nonstandard_field","limit":1}`)
	if dry["found_concepts"].(float64) != 2 || dry["found_total"].(float64) != 6 || dry["planned_total"].(float64) != 3 {
		t.Fatalf("limited dry run hides the job: %v", dry)
	}

	out := repairCall(t, s, `{"check":"nonstandard_field","dry_run":false}`)
	if out["applied"].(float64) != 2 {
		t.Fatalf("applied = %v, skipped = %v", out["applied"], out["skipped"])
	}
	cd, _ := k.ReadConcept("ops/same")
	if fm := cd.FrontmatterRaw; !strings.Contains(fm, "timestamp: 2026-01-02") || strings.Contains(fm, "date:") || strings.Contains(fm, "modified:") || !strings.Contains(fm, "provenance:") {
		t.Fatalf("equal synonyms not collapsed:\n%s", fm)
	}
	cd, _ = k.ReadConcept("ops/differ")
	if fm := cd.FrontmatterRaw; !strings.Contains(fm, "date:") || !strings.Contains(fm, "modified:") || strings.Contains(fm, "timestamp:") || !strings.Contains(fm, "provenance:") {
		t.Fatalf("conflicting synonyms touched, or the other fix held back:\n%s", fm)
	}
	skipped, _ := json.Marshal(out["skipped"])
	if !strings.Contains(string(skipped), `all mean \"timestamp\" and hold different values`) {
		t.Fatalf("skip reason: %s", skipped)
	}
}

// TestMapUpdateLintIgnore (D306): map_update writes the map-wide lint_ignore,
// map_list shows it, and an empty list removes it.
func TestMapUpdateLintIgnore(t *testing.T) {
	_, s := repairKB(t, 0)
	res := callTool(t, s, "map_update", `{"map":"ops","lint_ignore":["duplicate_link","map_misfit"]}`)
	if res.IsError || !strings.Contains(res.Content[0].Text, `"duplicate_link"`) {
		t.Fatalf("map_update lint_ignore: %+v", res)
	}
	if list := callTool(t, s, "map_list", `{}`); !strings.Contains(list.Content[0].Text, `"lint_ignore"`) {
		t.Fatalf("map_list does not show it: %s", list.Content[0].Text)
	}
	res = callTool(t, s, "map_update", `{"map":"ops","lint_ignore":[]}`)
	if res.IsError || !strings.Contains(res.Content[0].Text, `"lint_ignore": []`) {
		t.Fatalf("removing lint_ignore: %+v", res)
	}
}

// seedLegacyFixes gives a repair KB one legacy_path concept (D316 WP13) and one
// skill with a pre-D288 prefixed tool name (WP2).
func seedLegacyFixes(t *testing.T, k *kb.KB) {
	t.Helper()
	files := map[string]string{
		"instructions.md":          "---\nlegacy_paths:\n  \"wiki/\": \"old/\"\n  \"wiki/ops/\": \"ops/\"\n---\nKB.\n",
		"data/ops/legacy.md":       "---\ntype: Note\ntitle: L\nupdated: 2026-01-02\n---\n# L\n\nSee wiki/ops/runbook-x and wiki/notes/y.\n",
		"skills/old-way/SKILL.md":  "---\nname: old-way\ndescription: Old way\n---\nCall `kb_a__search`, then kb_a__concept_read.\n",
		"skills/old-way/helper.sh": "#!/bin/sh\necho kb_a__search\n",
	}
	for rel, content := range files {
		full := filepath.Join(k.Root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(k.Root, "skills", "old-way", "helper.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestRepair_LegacyPath: the declared prefixes are rewritten longest first, in
// one pass, so "wiki/ops/" wins over the shorter "wiki/" it contains.
func TestRepair_LegacyPath(t *testing.T) {
	k, s := repairKB(t, 0)
	seedLegacyFixes(t, k)
	if _, err := k.CommitOp("test: seed legacy"); err != nil {
		t.Fatal(err)
	}
	plan := repairCall(t, s, `{"check":"legacy_path"}`)
	if plan["planned_total"].(float64) != 2 || plan["found_concepts"].(float64) != 1 {
		t.Fatalf("plan = %v", plan)
	}
	out := repairCall(t, s, `{"check":"legacy_path","dry_run":false}`)
	if out["applied"].(float64) != 1 {
		t.Fatalf("apply = %v", out)
	}
	cd, err := k.ReadConcept("ops/legacy")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cd.Body, "See ops/runbook-x and old/notes/y.") {
		t.Errorf("body = %q", cd.Body)
	}
}

// TestRepair_LegacyToolName: the prefix is stripped from every artifact file
// that carries it, the file mode kept, and only with allow_artifact_write.
func TestRepair_LegacyToolName(t *testing.T) {
	k, s := repairKB(t, 0)
	seedLegacyFixes(t, k)
	if _, err := k.CommitOp("test: seed legacy"); err != nil {
		t.Fatal(err)
	}
	plan := repairCall(t, s, `{"check":"legacy_tool_name"}`)
	if plan["planned_total"].(float64) != 3 || plan["found_files"].(float64) != 2 {
		t.Fatalf("plan = %v", plan)
	}
	if res := callTool(t, s, "kb_repair", `{"check":"legacy_tool_name","dry_run":false}`); !res.IsError || !strings.Contains(res.Content[0].Text, "allow_artifact_write") {
		t.Fatalf("apply without allow_artifact_write must be refused: %+v", res)
	}
	k.AllowArtifactWrite = true
	before := commitCount(t, k)
	out := repairCall(t, s, `{"check":"legacy_tool_name","dry_run":false}`)
	if out["applied"].(float64) != 2 {
		t.Fatalf("apply = %v", out)
	}
	data, _ := os.ReadFile(filepath.Join(k.Root, "skills", "old-way", "SKILL.md"))
	if strings.Contains(string(data), "kb_a__") || !strings.Contains(string(data), "Call `search`, then concept_read.") {
		t.Errorf("SKILL.md = %q", data)
	}
	st, _ := os.Stat(filepath.Join(k.Root, "skills", "old-way", "helper.sh"))
	if execbit.Supported && st.Mode().Perm() != 0o755 {
		t.Errorf("helper.sh mode = %v, want 0755 kept", st.Mode().Perm())
	}
	if commitCount(t, k) != before+1 {
		t.Errorf("want one commit for the repair")
	}
}

// TestKBRepairApplyOmitsPlan (D318): the plan is a dry run's product; an apply
// answers with applied/skipped and the found_/planned_ totals only.
func TestKBRepairApplyOmitsPlan(t *testing.T) {
	_, s := repairKB(t, 2)
	dry := repairCall(t, s, `{"check":"nonstandard_field"}`)
	if _, ok := dry["planned"]; !ok {
		t.Fatalf("dry run must carry planned: %v", dry)
	}
	out := repairCall(t, s, `{"check":"nonstandard_field","dry_run":false}`)
	if _, ok := out["planned"]; ok {
		t.Fatalf("apply must not carry planned: %v", out)
	}
	if out["applied"].(float64) != 2 || out["planned_total"].(float64) != 2 {
		t.Fatalf("apply response = %v", out)
	}
}

// TestRepair_StringifiedList: the string becomes a real list, and a second run
// finds nothing left (D314).
func TestRepair_StringifiedList(t *testing.T) {
	k, s := repairKB(t, 0)
	for id, v := range map[string]string{"ops/s1": "[a, b]", "ops/s2": "[a]; [b]", "ops/s3": "- a"} {
		fm := newFM()
		fm.Set("type", "Note")
		fm.Set("title", "S")
		fm.Set("updated", "2026-01-02")
		fm.Set("provenance", v)
		if _, err := k.WriteConcept(okf.ConceptID(id), fm, "# S\n", ""); err != nil {
			t.Fatal(err)
		}
	}
	dry := repairCall(t, s, `{"check":"stringified_list","dry_run":true}`)
	if dry["error"] != nil {
		t.Fatalf("dry run: %v", dry)
	}
	repairCall(t, s, `{"check":"stringified_list","dry_run":false}`)
	want := map[string]int{"ops/s1": 2, "ops/s2": 2, "ops/s3": 1}
	for id, n := range want {
		cd, err := k.ReadConcept(okf.ConceptID(id))
		if err != nil {
			t.Fatal(err)
		}
		fm, _ := okf.ParseFrontmatter(cd.FrontmatterRaw)
		v, _ := fm.Get("provenance")
		list, ok := v.([]string)
		if !ok || len(list) != n {
			t.Errorf("%s provenance = %#v, want a list of %d", id, v, n)
		}
	}
	findings, err := lint.Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Check == "stringified_list" {
			t.Errorf("still flagged after repair: %+v", f)
		}
	}
}

// TestRepair_SyncH1 (D315): the first heading becomes the plain title even
// when it carried formatting, a later heading and a "# " line in a code fence
// stay, and a second run finds nothing left.
func TestRepair_SyncH1(t *testing.T) {
	k, s := repairKB(t, 0)
	fm := newFM()
	fm.Set("type", "Note")
	fm.Set("title", "Alpha")
	fm.Set("updated", "2026-01-02")
	body := "# **Beta** and [link](x.md) `code`\n\ntext\n\n# Later\n\n```\n# in fence\n```\n"
	if _, err := k.WriteConcept("ops/h1", fm, body, ""); err != nil {
		t.Fatal(err)
	}
	if out := repairCall(t, s, `{"check":"title_h1_mismatch","dry_run":false}`); out["error"] != nil || out["applied"].(float64) != 1 {
		t.Fatalf("repair: %v", out)
	}
	cd, err := k.ReadConcept("ops/h1")
	if err != nil {
		t.Fatal(err)
	}
	if want := "# Alpha\n\ntext\n\n# Later\n\n```\n# in fence\n```"; !strings.Contains(cd.Body, want) || strings.Contains(cd.Body, "Beta") {
		t.Fatalf("body = %q", cd.Body)
	}
	findings, err := lint.Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Check == "title_h1_mismatch" {
			t.Errorf("still flagged after repair: %+v", f)
		}
	}
}

func TestRebaseHrefInBody_EmptyTargetKeepsTheLabel(t *testing.T) {
	got := rebaseHrefInBody("a [Proj](proj/index.md) b [P2](proj/index.md#x) c [o](other.md)", "proj/index.md", "")
	if want := "a Proj b P2 c [o](other.md)"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRewriteWikiLinkInBody(t *testing.T) {
	got := rewriteWikiLinkInBody("[[a/b/index]] [[a/b/index#s|t]] `[[a/b/index]]` [[a/b/index2]]", "a/b/index", "a/b")
	if want := "[[a/b]] [[a/b#s|t]] `[[a/b/index]]` [[a/b/index2]]"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// D310: kb_repair converges every <concept>/index link to the canonical form
// in one pass, and lint then reports none.
func TestKBRepairIndexLinkForm(t *testing.T) {
	k, s := repairKB(t, 0)
	seedBodyFixes(t, k)
	if conformanceChecks["index_link_form"] {
		t.Fatal("index_link_form counted as conformance debt")
	}
	repairCall(t, s, `{"check":"index_link_form","dry_run":false}`)
	cd, err := k.ReadConcept("ops/lk")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cd.Body, "[e](e.md)") || !strings.Contains(cd.Body, "[[ops/e|the e]]") || strings.Contains(cd.Body, "index") {
		t.Fatalf("after repair:\n%s", cd.Body)
	}
	findings, err := lint.Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Check == "index_link_form" {
			t.Errorf("still reported: %+v", f)
		}
	}
}

// D310: applying broken_link to a self-link removes the link syntax.
func TestKBRepairSelfLinkIsDropped(t *testing.T) {
	k, s := repairKB(t, 0)
	dir := filepath.Join(k.DataRoot(), "ops", "sl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\ntype: Note\ntitle: SL\nupdated: 2026-01-02\n---\nSee [Me](sl/index.md) here.\n"
	if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	repairCall(t, s, `{"check":"broken_link","dry_run":false}`)
	cd, err := k.ReadConcept("ops/sl")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cd.Body, "See Me here.") {
		t.Fatalf("after repair:\n%s", cd.Body)
	}
}
