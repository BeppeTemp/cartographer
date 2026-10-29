package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// multiKB seeds one fixture that needs a concept check (nonstandard_field), an
// artifact check (legacy_tool_name) and a map check (unmapped_folder).
func multiKB(t *testing.T) (*kb.KB, *Server) {
	t.Helper()
	k, s := repairKB(t, 3)
	seedLegacyFixes(t, k)
	driftWrite(t, k, "loose-notes/a.md", "---\ntype: Note\ntitle: A\n---\n# A\n")
	if _, err := k.CommitOp("test: seed multi"); err != nil {
		t.Fatal(err)
	}
	return k, s
}

func multiChecks(out map[string]any) map[string]map[string]any {
	got := map[string]map[string]any{}
	for _, e := range out["checks"].([]any) {
		m := e.(map[string]any)
		got[m["check"].(string)] = m
	}
	return got
}

// The default stays a dry run for a list and for "*": nothing is written, the
// per-check counts are there and each planned list is capped at 5.
func TestKBRepairMultiDefaultsToDryRun(t *testing.T) {
	t.Parallel()
	k, s := repairKB(t, 8)
	before := commitCount(t, k)
	for _, args := range []string{`{"check":"*"}`, `{"check":["nonstandard_field","legacy_tool_name"]}`} {
		out := repairCall(t, s, args)
		if out["dry_run"] != true {
			t.Fatalf("%s: dry_run = %v", args, out["dry_run"])
		}
		e := multiChecks(out)["nonstandard_field"]
		if e == nil || e["planned_total"].(float64) != 8 {
			t.Fatalf("%s: entry = %v", args, e)
		}
		if n := len(e["planned"].([]any)); n != repairMultiPlannedCap {
			t.Errorf("%s: planned has %d items, want the cap %d", args, n, repairMultiPlannedCap)
		}
		if out["planned_total_all"].(float64) < 8 || out["checks_run"].(float64) < 2 {
			t.Errorf("%s: totals = %v", args, out)
		}
	}
	if commitCount(t, k) != before {
		t.Fatal("a dry run committed")
	}
}

// "*" applies concept, artifact and map checks in one commit and one log
// entry; one repair_revert undoes it; a second call is a no-op.
func TestKBRepairStarAppliesInOneCommit(t *testing.T) {
	t.Parallel()
	k, s := multiKB(t)
	treeBefore := gitOut(t, k, "rev-parse", "HEAD^{tree}")
	before := commitCount(t, k)
	logBefore := strings.Count(readLog(t, k), "kb_repair")

	out := repairCall(t, s, `{"check":"*","dry_run":false}`)
	got := multiChecks(out)
	for _, c := range []string{"nonstandard_field", "legacy_tool_name", "unmapped_folder"} {
		if got[c] == nil || got[c]["applied"].(float64) == 0 {
			t.Fatalf("%s not applied: %v", c, out)
		}
		if _, ok := got[c]["planned"]; ok {
			t.Errorf("%s: an apply omits planned", c)
		}
	}
	if out["applied_total"].(float64) < 3 {
		t.Fatalf("applied_total = %v", out["applied_total"])
	}
	if commitCount(t, k) != before+1 {
		t.Fatalf("commits = %d, want %d", commitCount(t, k), before+1)
	}
	subject := gitOut(t, k, "log", "-1", "--format=%s")
	if !strings.HasPrefix(subject, "kb_repair") {
		t.Fatalf("subject = %q", subject)
	}
	if n := strings.Count(readLog(t, k), "kb_repair") - logBefore; n < 1 || strings.Count(readLog(t, k), "kb_repair: ") != logBefore+1 {
		t.Errorf("want exactly one new log entry, got %d", n)
	}
	sha := gitOut(t, k, "rev-parse", "HEAD")

	if again := repairCall(t, s, `{"check":"*","dry_run":false}`); again["applied_total"].(float64) != 0 || commitCount(t, k) != before+1 {
		t.Fatalf("a second run must be a no-op: %v", again)
	}
	if res := revertCall(t, s, sha); res.IsError {
		t.Fatalf("revert: %s", res.Content[0].Text)
	}
	if got := gitOut(t, k, "rev-parse", "HEAD^{tree}"); got != treeBefore {
		t.Fatalf("revert did not restore the tree: %s != %s", got, treeBefore)
	}
}

func readLog(t *testing.T, k *kb.KB) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(k.DataRoot(), "log.md"))
	if err != nil {
		return ""
	}
	return string(b)
}

// The server's order, not the caller's: the same tree whichever way the list
// is written (rename -> prose split -> value chain, D352).
func TestKBRepairMultiOrderIsTheServers(t *testing.T) {
	t.Parallel()
	run := func(args string) string {
		k, s := repairKB(t, 0)
		if _, err := k.UpdateMapContract("ops", kb.MapContractUpdate{FieldValues: map[string]map[string][]string{kb.AllTypes: {"status": {"done", "in-progress"}}}}); err != nil {
			t.Fatal(err)
		}
		for id, st := range map[string]string{"ops/a": "Completato", "ops/b": "done — after the second pass"} {
			fm := newFM()
			fm.Set("type", "Note")
			fm.Set("title", "T")
			fm.Set("updated", "2026-01-02")
			fm.Set("state", st)
			if _, err := k.WriteConcept(okf.ConceptID(id), fm, "# T\n\nbody\n", ""); err != nil {
				t.Fatal(err)
			}
		}
		repairCall(t, s, args)
		return readRaw(t, k, "ops/a") + "\n=====\n" + readRaw(t, k, "ops/b")
	}
	fwd := run(`{"check":["prose_value","nonstandard_field","invalid_field_value"],"dry_run":false}`)
	rev := run(`{"check":["invalid_field_value","nonstandard_field","prose_value"],"dry_run":false}`)
	star := run(`{"check":"*","dry_run":false}`)
	if fwd != rev || fwd != star {
		t.Fatalf("order changed the result:\n%s\n---\n%s\n---\n%s", fwd, rev, star)
	}

	got, multi, err := parseRepairChecks(json.RawMessage(`["zzz_never","*","nonstandard_field"]`))
	if err == nil || !strings.Contains(err.Error(), "zzz_never") {
		t.Fatalf("unknown name must refuse and name it: %v %v", got, err)
	}
	got, multi, err = parseRepairChecks(json.RawMessage(`["unmapped_folder","nonstandard_field","nonstandard_field"]`))
	if err != nil || !multi {
		t.Fatal(err, multi)
	}
	var want []string
	for _, c := range lint.AutoRepairOrder {
		if c == "unmapped_folder" || c == "nonstandard_field" {
			want = append(want, c)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	star2, _, _ := parseRepairChecks(json.RawMessage(`"*"`))
	if len(star2) != len(lint.FixableChecks) {
		t.Fatalf("\"*\" expands to %d checks, want %d", len(star2), len(lint.FixableChecks))
	}
}

// Validation is all-or-nothing and before any write; an empty list is an error;
// a single string keeps its old shape.
func TestKBRepairMultiRefusals(t *testing.T) {
	t.Parallel()
	k, s := multiKB(t)
	before := commitCount(t, k)
	res := callTool(t, s, "kb_repair", `{"check":["nonstandard_field","nope_check"],"dry_run":false}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "nope_check") {
		t.Fatalf("unknown name: %+v", res)
	}
	if res := callTool(t, s, "kb_repair", `{"check":[],"dry_run":false}`); !res.IsError {
		t.Fatal("an empty list must be an error")
	}
	if commitCount(t, k) != before {
		t.Fatal("a refused call wrote")
	}
	single := repairCall(t, s, `{"check":"nonstandard_field"}`)
	if _, ok := single["checks"]; ok || single["check"] != "nonstandard_field" {
		t.Fatalf("single string must answer as before: %v", single)
	}
}

// limit applies per check, scope to all, and removals are named.
func TestKBRepairMultiLimitScopeRemovals(t *testing.T) {
	t.Parallel()
	k, s := tagsKB(t)
	for i := 0; i < 3; i++ {
		writeTagged(t, k, fmt.Sprintf("ops/a%d", i), "Foo_Bar", "junk")
	}
	if _, err := k.CommitOp("test: seed"); err != nil {
		t.Fatal(err)
	}
	out := repairCall(t, s, `{"check":["invalid_field_value","nonstandard_field"],"limit":2}`)
	got := multiChecks(out)
	for _, c := range []string{"invalid_field_value", "nonstandard_field"} {
		if got[c]["planned_total"] == nil || got[c]["found_concepts"].(float64) != 3 {
			t.Fatalf("%s: %v", c, got[c])
		}
	}
	paths := map[string]bool{}
	for _, it := range multiChecks(repairCall(t, s, `{"check":["invalid_field_value"],"limit":2}`))["invalid_field_value"]["planned"].([]any) {
		paths[it.(map[string]any)["path"].(string)] = true
	}
	if len(paths) != 2 {
		t.Errorf("limit 2 planned %d concepts", len(paths))
	}
	if got["invalid_field_value"]["removals"].(float64) < 1 {
		t.Errorf("removals not reported: %v", got["invalid_field_value"])
	}
	if _, ok := got["nonstandard_field"]["removals"]; ok {
		t.Errorf("removals only above zero: %v", got["nonstandard_field"])
	}
	if scoped := repairCall(t, s, `{"check":"*","scope":"nowhere"}`); len(scoped["checks"].([]any)) != 0 {
		t.Errorf("a scope with no concept plans nothing: %v", scoped)
	}
	// An explicit apply removes (D1197).
	repairCall(t, s, `{"check":"*","dry_run":false}`)
	if raw := tagsOf(t, k, "ops/a0"); strings.Contains(raw, "junk") {
		t.Errorf("the explicit multi apply must remove:\n%s", raw)
	}
}

// kb_status names the call only when something is repairable.
func TestSummarizeConformanceNamesTheRepairCall(t *testing.T) {
	t.Parallel()
	now := lint.Now()
	with := summarizeConformance([]lint.Finding{{Check: "nonstandard_field", Fix: &lint.Fix{}}}, 0, "", 14, now)
	if with["repair"] != repairCallHint || !strings.Contains(repairCallHint, `kb_repair(check: "*")`) || strings.Contains(repairCallHint, "cartographer ") {
		t.Errorf("repair = %v", with["repair"])
	}
	if without := summarizeConformance([]lint.Finding{{Check: "orphan"}}, 0, "", 14, now); without["repair"] != nil {
		t.Errorf("nothing repairable, yet repair = %v", without["repair"])
	}
	if _, ok := summarizeConformance(nil, 0, "", 14, now)["repair"]; ok {
		t.Error("a clean KB must not carry repair")
	}
}

// A concept changed after the plan was made is skipped, never overwritten
// (if_match with the hash read at planning time). The seam edits the page
// between plan and apply. Not parallel: it sets a package-level seam.
func TestKBRepairMultiSkipsAConceptChangedSinceThePlan(t *testing.T) {
	k, s := repairKB(t, 2)
	repairAfterPlan = func(check string) {
		if check != "nonstandard_field" {
			return
		}
		fm := newFM()
		fm.Set("type", "Note")
		fm.Set("title", "N0")
		fm.Set("updated", "2026-01-02")
		if _, err := k.WriteConcept("ops/n0", fm, "# N0\n\nedited meanwhile\n", ""); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { repairAfterPlan = nil })
	out := repairCall(t, s, `{"check":["nonstandard_field","tool_param_field"],"dry_run":false}`)
	e := multiChecks(out)["nonstandard_field"]
	if e["applied"].(float64) != 1 || len(e["skipped"].([]any)) != 1 {
		t.Fatalf("want one applied and one skipped: %v", e)
	}
	if cd, _ := k.ReadConcept("ops/n0"); !strings.Contains(cd.Body, "edited meanwhile") || !strings.Contains(cd.FrontmatterRaw, "updated") {
		t.Errorf("the changed concept was overwritten:\n%s\n%s", cd.FrontmatterRaw, cd.Body)
	}
}

// A hard failure inside a later check stops the loop and names what the
// earlier checks already wrote (uncommitted). Not parallel: package seam.
func TestKBRepairMultiHardFailureNamesWhatWasWritten(t *testing.T) {
	_, s := repairKB(t, 2)
	repairContractRewrite = func(*kb.KB, []repairTarget) ([]string, error) { return nil, fmt.Errorf("disk full") }
	t.Cleanup(func() { repairContractRewrite = renameInContracts })
	// Single check: the old wording.
	res := callTool(t, s, "kb_repair", `{"check":"nonstandard_field","dry_run":false}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "contract rewrite failed after 2 concept(s) were written (uncommitted") {
		t.Fatalf("single: %+v", res)
	}
}

// A failing check after an earlier one wrote: the error lists the written one.
// Not parallel: package seam.
func TestKBRepairMultiErrorListsEarlierWrites(t *testing.T) {
	k, s := repairKB(t, 2)
	fm := newFM()
	fm.Set("type", "Note")
	fm.Set("title", "T")
	fm.Set("op", "x") // a write-tool parameter stored as a field
	if _, err := k.WriteConcept("ops/d0", fm, "# T\n", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOp("test: seed"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	repairContractRewrite = func(k *kb.KB, a []repairTarget) ([]string, error) {
		if calls++; calls == 2 {
			return nil, fmt.Errorf("disk full")
		}
		return renameInContracts(k, a)
	}
	t.Cleanup(func() { repairContractRewrite = renameInContracts })
	res := callTool(t, s, "kb_repair", `{"check":["tool_param_field","nonstandard_field"],"dry_run":false}`)
	msg := res.Content[0].Text
	if !res.IsError || !strings.Contains(msg, "already written and uncommitted: nonstandard_field: 2") || !strings.Contains(msg, "disk full") {
		t.Fatalf("%+v", res)
	}
}

// A read-only session is refused a multi-check call, whatever the form.
func TestKBRepairMultiRefusedToAReadOnlySession(t *testing.T) {
	t.Parallel()
	handler := newRoutedTestHandler(t, "kb-a")
	for _, check := range []any{"*", []string{"nonstandard_field", "duplicate_link"}} {
		res := routedCallAt(t, handler, readOnlyPath, "kb_repair", map[string]any{"kb": "kb-a", "check": check, "dry_run": false})
		if !res.IsError || !strings.Contains(res.Content[0].Text, "forbidden") {
			t.Errorf("check %v on a read-only session: %+v", check, res)
		}
	}
}

// A missing check keeps the old refusal that lists the fixable checks.
func TestKBRepairMissingCheckListsTheFixableOnes(t *testing.T) {
	t.Parallel()
	_, s := repairKB(t, 0)
	res := callTool(t, s, "kb_repair", `{}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "checks that emit fixes") {
		t.Fatalf("%+v", res)
	}
}
