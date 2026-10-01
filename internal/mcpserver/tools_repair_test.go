package mcpserver

import (
	"encoding/json"
	"fmt"
	"os/exec"
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
	applied, skipped := applyRepair(k, targets)
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
	cases := []struct {
		name     string
		findings []lint.Finding
		last     string
		want     bool
	}{
		{"clean", nil, "", false},
		{"unrelated finding ignored", []lint.Finding{f("orphan", lint.SevWarning, false)}, "", false},
		{"warning", []lint.Finding{f("broken_link", lint.SevWarning, false)}, "2026-09-30", true},
		{"fixable", []lint.Finding{f("tool_param_field", lint.SevInfo, true)}, "2026-09-30", true},
		{"info, never run", []lint.Finding{f("map_misfit", lint.SevInfo, false)}, "", true},
		{"info, run long ago", []lint.Finding{f("map_misfit", lint.SevInfo, false)}, "2026-08-01", true},
		{"info, run recently", []lint.Finding{f("map_misfit", lint.SevInfo, false)}, "2026-09-20", false},
		{"no finding, never run", nil, "", false},
	}
	for _, c := range cases {
		got := summarizeConformance(c.findings, c.last, now)
		if got["doctor_suggested"] != c.want {
			t.Errorf("%s: doctor_suggested = %v, want %v", c.name, got["doctor_suggested"], c.want)
		}
	}
	got := summarizeConformance([]lint.Finding{
		f("nonstandard_field", lint.SevWarning, true), f("nonstandard_field", lint.SevWarning, false),
		f("link_to_retired", lint.SevInfo, false), f("orphan", lint.SevWarning, false),
	}, "2026-09-20", now)
	sev := got["findings"].(map[string]int)
	if sev[lint.SevWarning] != 2 || sev[lint.SevInfo] != 1 || got["fixable"] != 1 || got["last_doctor"] != "2026-09-20" {
		t.Fatalf("summary = %v", got)
	}
}

func TestKBStatusConformance(t *testing.T) {
	k, s := repairKB(t, 2)
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
		_ = summarizeConformance(f, lastDoctorDate(k), time.Now())
	}
}

func newFM() *okf.Frontmatter {
	fm, _ := okf.ParseFrontmatter("")
	return fm
}
