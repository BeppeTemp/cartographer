package mcpserver

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// treeWithoutLog is every tracked-looking file of a KB by path, minus log.md
// (its entry is led by the caller's label) and the local state.
func treeWithoutLog(t *testing.T, k *kb.KB) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(k.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(k.Root, p)
		if d.IsDir() && (rel == ".git" || rel == ".cartographer") {
			return filepath.SkipDir
		}
		if d.IsDir() || filepath.Base(rel) == "log.md" {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestKBRepairAutoRunsTheStagedRepair(t *testing.T) {
	t.Parallel()
	k, s := autoRepairKB(t)
	before := commitCount(t, k)
	res := callTool(t, s, "kb_repair", `{"check":"auto","dry_run":false,"reason":"auto-repair (surveyor)"}`)
	if res.IsError {
		t.Fatalf("kb_repair auto: %s", res.Content[0].Text)
	}
	if !strings.Contains(res.Content[0].Text, `"applied_total":3`) {
		t.Fatalf("result = %s", res.Content[0].Text)
	}
	if commitCount(t, k) != before+1 {
		t.Fatal("want exactly one commit")
	}
	if got := gitOut(t, k, "log", "-1", "--format=%s"); got != autoRepairMCPReason {
		t.Fatalf("subject = %q", got)
	}
	if got := gitOut(t, k, "log", "-1", "--format=%(trailers:key=Reason,valueonly)"); got != autoRepairMCPReason {
		t.Fatalf("reason trailer = %q", got)
	}
	// Recognised as a repair by the revert tool and the Atlas.
	sha := gitOut(t, k, "rev-parse", "HEAD")
	if res := callTool(t, s, "repair_revert", `{"sha":"`+sha+`"}`); res.IsError {
		t.Fatalf("repair_revert refused an auto commit: %s", res.Content[0].Text)
	}
	// A caller cannot claim the Surveyor label by sending its reason, and the
	// on-demand run is in the log the Atlas reads.
	if run, ok := lastAutoRepairRun(k); !ok || len(run.Checks) != 1 || run.Checks[0].Applied != 3 || run.Checks[0].Commit == "" {
		t.Fatalf("log = %+v, %v", run, ok)
	}
	callTool(t, s, "kb_repair", `{"check":"auto","dry_run":false,"reason":"because"}`)
	if got := gitOut(t, k, "log", "-1", "--format=%s"); got != autoRepairMCPReason {
		t.Fatalf("subject = %q", got)
	}
	if got := gitOut(t, k, "log", "-1", "--format=%(trailers:key=Reason,valueonly)"); got != autoRepairMCPReason {
		t.Fatalf("reason trailer = %q", got)
	}
}

func TestKBRepairAutoHasNoDryRun(t *testing.T) {
	t.Parallel()
	k, s := autoRepairKB(t)
	before := commitCount(t, k)
	for _, args := range []string{`{"check":"auto"}`, `{"check":"auto","dry_run":true}`} {
		res := callTool(t, s, "kb_repair", args)
		if !res.IsError || !strings.Contains(res.Content[0].Text, "auto has no dry run") {
			t.Fatalf("%s: %+v", args, res)
		}
	}
	if res := callTool(t, s, "kb_repair", `{"check":["auto"],"dry_run":false}`); !res.IsError {
		t.Fatal("auto inside a list must be refused")
	}
	if commitCount(t, k) != before {
		t.Fatal("a refused call committed")
	}
}

func TestKBRepairAutoTakesNoScopeOrLimit(t *testing.T) {
	t.Parallel()
	k, s := autoRepairKB(t)
	before := commitCount(t, k)
	for _, args := range []string{`{"check":"auto","dry_run":false,"scope":"ops"}`, `{"check":"auto","dry_run":false,"limit":1}`} {
		res := callTool(t, s, "kb_repair", args)
		if !res.IsError || !strings.Contains(res.Content[0].Text, "no scope or limit") {
			t.Fatalf("%s: %+v", args, res)
		}
	}
	if commitCount(t, k) != before {
		t.Fatal("a refused call committed")
	}
}

func TestKBRepairAutoRefusesAnOpenConflict(t *testing.T) {
	t.Parallel()
	k, s := autoRepairKB(t)
	if err := os.MkdirAll(k.Root+"/.cartographer", 0o755); err != nil {
		t.Fatal(err)
	}
	conflicts := `[{"concept_id":"ops/n0","path":"ops/n0.md","detected_at":"2026-01-01T00:00:00Z"}]`
	if err := os.WriteFile(k.Root+"/.cartographer/conflicts.json", []byte(conflicts), 0o644); err != nil {
		t.Fatal(err)
	}
	before := commitCount(t, k)
	res := callTool(t, s, "kb_repair", `{"check":"auto","dry_run":false}`)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "1 open git conflict(s)") {
		t.Fatalf("res = %+v", res)
	}
	if commitCount(t, k) != before {
		t.Fatal("a refused call committed")
	}
}

// The timer and the tool are one routine: the same KB comes out the same.
func TestKBRepairAutoMatchesTheTimer(t *testing.T) {
	t.Parallel()
	kTimer, sTimer := autoRepairKB(t)
	kTool, sTool := autoRepairKB(t)
	sTimer.runAutoRepairQuota(context.Background())
	if res := callTool(t, sTool, "kb_repair", `{"check":"auto","dry_run":false}`); res.IsError {
		t.Fatal(res.Content[0].Text)
	}
	a, b := treeWithoutLog(t, kTimer), treeWithoutLog(t, kTool)
	if len(a) != len(b) {
		t.Fatalf("files: timer %d, tool %d", len(a), len(b))
	}
	for p, c := range a {
		if b[p] != c {
			t.Errorf("%s differs between the timer and the tool", p)
		}
	}
}

func TestAutoRepairSwitch(t *testing.T) {
	t.Parallel()
	s := New("test")
	if !s.AutoRepairEnabled() {
		t.Fatal("on by default")
	}
	s.SetAutoRepairEnabled(false)
	if s.AutoRepairEnabled() {
		t.Fatal("off after SetAutoRepairEnabled(false)")
	}
	// Off means StartAutoRepair is never called: nothing is installed.
	if s.autoRepairTrigger != nil {
		t.Fatal("the switch must not start the timer")
	}
}
