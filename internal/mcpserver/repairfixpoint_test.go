package mcpserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// chainKB is a git KB whose map "ops" declares the status vocabulary, with n
// pages carrying the D355 reproduction: `stato: attivo — …` needs a rename
// before the prose split can see the field (prose_value canonicalises the
// token itself, so invalid_field_value has nothing left), plus an independent
// title_h1_mismatch so the page needs three checks in all.
func chainKB(t *testing.T, n int) (*kb.KB, *Server) {
	t.Helper()
	k, _ := setupGitKB(t)
	contract := kb.MapContract{FieldValues: map[string][]string{"status": {"active", "draft", "deprecated"}}}
	if err := k.CreateMapWithContract("ops", "Ops", "map", nil, "", contract); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		writeChainPage(t, k, fmt.Sprintf("n%d", i))
	}
	k.AutoCommit = true
	if _, err := k.CommitOp("test: seed"); err != nil {
		t.Fatal(err)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	return k, s
}

func writeChainPage(t *testing.T, k *kb.KB, name string) {
	t.Helper()
	text := "---\ntype: Note\ntitle: Page " + name + "\nstato: attivo — gira bene da marzo\n---\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "ops", name+".md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Every unattended check of the product default, in its order.
var chainChecks = []string{"nonstandard_field", "prose_value", "title_h1_mismatch"}

func TestFixpointConvergesAChainInOneCall(t *testing.T) {
	k, _ := chainKB(t, 1)
	out, skip := repairConceptFixpoint(k, "ops/n0", chainChecks, nil)
	if skip != nil {
		t.Fatalf("skip = %+v", skip)
	}
	for _, c := range chainChecks {
		if out.Changed[c] == 0 {
			t.Errorf("%s did not run: %v", c, out.Changed)
		}
	}
	cd, _ := k.ReadConcept("ops/n0")
	if !strings.Contains(cd.FrontmatterRaw, "status: active") || strings.Contains(cd.FrontmatterRaw, "stato") {
		t.Fatalf("frontmatter:\n%s", cd.FrontmatterRaw)
	}
	if !strings.Contains(cd.Body, "> status: ") {
		t.Fatalf("the prose note is gone:\n%s", cd.Body)
	}
	if left := lint.CheckConcept(k, "ops/n0", cd.Content); len(fixableOf(left, chainChecks)) != 0 {
		t.Fatalf("findings left: %+v", left)
	}
	// Idempotent: the second call changes nothing and writes nothing.
	again, skip := repairConceptFixpoint(k, "ops/n0", chainChecks, nil)
	if skip != nil || again.Hash != "" || len(again.Changed) != 0 {
		t.Fatalf("second call = %+v, %+v", again, skip)
	}
}

func fixableOf(fs []lint.Finding, checks []string) []lint.Finding {
	var out []lint.Finding
	for _, f := range fs {
		for _, c := range checks {
			if f.Check == c && f.Fix != nil {
				out = append(out, f)
			}
		}
	}
	return out
}

// fakeFixpointChecker swaps the evaluator for synthetic findings and restores it.
func fakeFixpointChecker(t *testing.T, fn func(content string) []lint.Finding) {
	t.Helper()
	prev := fixpointCheck
	fixpointCheck = func(_ *kb.KB, _ okf.ConceptID, content string) []lint.Finding { return fn(content) }
	t.Cleanup(func() { fixpointCheck = prev })
}

func setValue(field, to string) []lint.Finding {
	return []lint.Finding{{Check: "invalid_field_value", Fix: &lint.Fix{Kind: lint.FixSetValue, Field: field, To: to}}}
}

func TestFixpointOscillationWritesNothing(t *testing.T) {
	k, _ := chainKB(t, 1)
	before, _ := k.ReadConcept("ops/n0")
	// Two fixes undoing each other: the content flips between two values.
	fakeFixpointChecker(t, func(content string) []lint.Finding {
		if strings.Contains(content, "flip: a") {
			return setValue("flip", "b")
		}
		return setValue("flip", "a")
	})
	_, skip := repairConceptFixpoint(k, "ops/n0", []string{"invalid_field_value"}, nil)
	if skip == nil || skip.Reason != "did not converge: invalid_field_value" {
		t.Fatalf("skip = %+v", skip)
	}
	after, _ := k.ReadConcept("ops/n0")
	if after.ContentHash != before.ContentHash {
		t.Fatal("an oscillating concept was written")
	}
}

func TestFixpointStopsAtTheCap(t *testing.T) {
	k, _ := chainKB(t, 1)
	before, _ := k.ReadConcept("ops/n0")
	passes := 0
	// Never repeats a content, never ends: only the cap stops it.
	fakeFixpointChecker(t, func(string) []lint.Finding {
		passes++
		return setValue("count", fmt.Sprint(passes))
	})
	_, skip := repairConceptFixpoint(k, "ops/n0", []string{"invalid_field_value"}, nil)
	if skip == nil || !strings.HasPrefix(skip.Reason, "did not converge") {
		t.Fatalf("skip = %+v", skip)
	}
	if passes != repairFixpointMax+1 {
		t.Fatalf("evaluated %d times, want %d (the cap, then one look)", passes, repairFixpointMax+1)
	}
	if after, _ := k.ReadConcept("ops/n0"); after.ContentHash != before.ContentHash {
		t.Fatal("a concept that did not converge was written")
	}
}

func TestFixpointLeavesCrossConceptFindingsAlone(t *testing.T) {
	k, _ := chainKB(t, 1)
	before, _ := k.ReadConcept("ops/n0")
	seed := []lint.Finding{{Check: "broken_link", Path: "ops/n0.md", Fix: &lint.Fix{Kind: lint.FixRebaseLink, Field: "x.md", To: "y.md"}}}
	out, skip := repairConceptFixpoint(k, "ops/n0", []string{"broken_link"}, seed)
	if skip != nil || out.Hash != "" {
		t.Fatalf("out = %+v, skip = %+v", out, skip)
	}
	if after, _ := k.ReadConcept("ops/n0"); after.ContentHash != before.ContentHash {
		t.Fatal("a cross-concept finding was applied by the stage-1 applier")
	}
}

func TestAutoRepairRunConvergesAChainInOneCommit(t *testing.T) {
	k, s := chainKB(t, 3)
	k.AutoRepair = chainChecks
	k.DoctorAutoIntervalDays = 1
	before := commitCount(t, k)
	run := s.runAutoRepairQuota(context.Background())
	if commitCount(t, k) != before+1 {
		t.Fatalf("commits +%d, want one per run", commitCount(t, k)-before)
	}
	for _, e := range run.Checks {
		if e.Applied != 3 || e.Error != "" {
			t.Errorf("entry = %+v, want 3 concepts and no error", e)
		}
	}
	body := gitOut(t, k, "log", "-1", "--format=%b")
	for _, c := range chainChecks {
		if !strings.Contains(body, c+": 3") {
			t.Errorf("commit body lacks %q:\n%s", c+": 3", body)
		}
	}
	if left := fixableOf(mustLint(t, k), chainChecks); len(left) != 0 {
		t.Fatalf("findings after one run: %+v", left)
	}
}

func mustLint(t *testing.T, k *kb.KB) []lint.Finding {
	t.Helper()
	fs, err := lint.Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

func TestStagedRepairQuotaCountsConceptsNotChecks(t *testing.T) {
	k, _ := chainKB(t, 12)
	r := stagedAutoRepair(context.Background(), k, chainChecks, 5)
	// Each concept needs all three checks, but the quota counts concepts.
	for _, c := range chainChecks {
		if r.applied[c] != 5 {
			t.Errorf("%s applied to %d concepts, want the quota 5", c, r.applied[c])
		}
	}
}

func TestAutoRepairRunLeavesNothingOnTheProcessedConcepts(t *testing.T) {
	const drifted = autoRepairLimit + 100
	k, s := chainKB(t, drifted)
	k.AutoRepair = chainChecks
	k.DoctorAutoIntervalDays = 1
	s.runAutoRepairQuota(context.Background())
	left := map[string]bool{}
	for _, f := range fixableOf(mustLint(t, k), chainChecks) {
		left[f.Path] = true
	}
	if len(left) != drifted-autoRepairLimit {
		t.Fatalf("%d concepts still carry findings, want the %d beyond the quota", len(left), drifted-autoRepairLimit)
	}
}

func TestAutoRepairIgnoresChecksThatAreNotSafe(t *testing.T) {
	k, s := chainKB(t, 1)
	k.AutoRepair = []string{"broken_link", "nonstandard_field", "reciprocal_link_item"}
	k.DoctorAutoIntervalDays = 1
	safe, ignored := autoRepairSplit(k)
	if len(safe) != 1 || safe[0] != "nonstandard_field" || len(ignored) != 2 {
		t.Fatalf("safe = %v, ignored = %v", safe, ignored)
	}
	if got := kbCapabilities(k)["auto_repair"].Ignored; len(got) != 2 || got[0] != "broken_link" {
		t.Fatalf("capabilities.auto_repair.ignored = %v", got)
	}
	run := s.runAutoRepairQuota(context.Background())
	if len(run.Checks) != 1 || run.Checks[0].Check != "nonstandard_field" {
		t.Fatalf("run = %+v", run)
	}
	// Repair-on-write follows the same rule.
	k.RepairOnWrite = true
	if got := repairOnWriteChecks(k); len(got) != 1 || got[0] != "nonstandard_field" {
		t.Fatalf("repair-on-write checks = %v", got)
	}
}

func TestDefaultAutoRepairRunsEveryUnattendedCheck(t *testing.T) {
	k, _ := chainKB(t, 1)
	k.AutoRepair = config.DefaultAutoRepair
	if _, ignored := autoRepairSplit(k); len(ignored) != 0 {
		t.Fatalf("the default list has checks the heartbeat ignores: %v", ignored)
	}
}

func TestRepairOnWriteConvergesTheChainInOneWrite(t *testing.T) {
	k, s := rowKB(t, chainChecks...)
	contract := kb.MapContract{FieldValues: map[string][]string{"status": {"active", "draft", "deprecated"}}}
	if _, err := k.UpdateMapContract("ops", kb.MapContractUpdate{FieldValues: contract.FieldValues}); err != nil {
		t.Fatal(err)
	}
	out := decodeJSON(t, mustText(t, s, "concept_write",
		`{"id":"ops/chain","frontmatter":{"type":"Note","title":"Chain page","stato":"attivo — gira bene da marzo"},"body":"# Chain\n"}`))
	got := repairedMap(out)
	for _, c := range chainChecks {
		if got[c] == 0 {
			t.Errorf("repaired = %v, missing %s", out["repaired"], c)
		}
	}
	if fs, _ := out["findings"].([]interface{}); len(fixableOfOut(fs, chainChecks)) != 0 {
		t.Fatalf("findings left: %v", out["findings"])
	}
}

func fixableOfOut(fs []interface{}, checks []string) []interface{} {
	var out []interface{}
	for _, f := range fs {
		m, _ := f.(map[string]interface{})
		for _, c := range checks {
			if m["check"] == c {
				out = append(out, f)
			}
		}
	}
	return out
}

func countRuns(t *testing.T, k *kb.KB) int {
	t.Helper()
	b, _ := os.ReadFile(autoRepairLogPath(k))
	return strings.Count(string(b), "\n")
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestAutoRepairPullTriggersOneDebouncedRun(t *testing.T) {
	k, s := chainKB(t, 1)
	k.AutoRepair = chainChecks
	k.DoctorAutoIntervalDays = 1
	s.autoRepairTrigger = make(chan struct{}, 1)
	s.autoRepairPullGap = 400 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { s.autoRepairLoop(ctx, time.Hour, time.Hour); close(done) }()

	s.triggerAutoRepair()
	waitFor(t, "the pull-triggered run", func() bool { return countRuns(t, k) == 1 })
	// A second and third pull inside the gap coalesce into one run.
	writeChainPage(t, k, "late")
	s.triggerAutoRepair()
	time.Sleep(50 * time.Millisecond)
	s.triggerAutoRepair()
	if n := countRuns(t, k); n != 1 {
		t.Fatalf("a pull inside the gap ran at once: %d runs", n)
	}
	waitFor(t, "the debounced run", func() bool { return countRuns(t, k) == 2 })
	time.Sleep(600 * time.Millisecond)
	if n := countRuns(t, k); n != 2 {
		t.Fatalf("two pulls inside the gap made %d runs, want 1 more", n-1)
	}
	cancel()
	<-done
}

func TestStartAutoRepairChainsTheSyncInCallback(t *testing.T) {
	k, s := chainKB(t, 1)
	k.AutoRepair = chainChecks
	k.DoctorAutoIntervalDays = 1
	calls := 0
	k.OnSyncIn = func() { calls++ }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.StartAutoRepair(ctx)
	k.OnSyncIn() // what SyncIn calls after a pull that moved HEAD
	if calls != 1 {
		t.Fatalf("the previous OnSyncIn was dropped: %d calls", calls)
	}
	waitFor(t, "the run after the pull", func() bool { return countRuns(t, k) == 1 })
}
