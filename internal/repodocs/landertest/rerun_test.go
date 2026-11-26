package landertest

import (
	"strings"
	"testing"
)

// The re-run of a red hosted train gate hands the signoff the red run's id (SIGNOFF_RERUN), which
// reruns that run's failed jobs; the first gate gets none.
func TestLanderRerunsTheRedHostedRun(t *testing.T) {
	e := newLandEnv(t)
	e.queue("x", "fix(kb): x\n", "2026-01-01T10:00:00Z", map[string]string{"x.txt": "x\n"})
	red := hostedRun("completed", "completed success", map[string]string{"test-macos": "completed failure"})
	out, _ := e.landArgs(nil, "FAKE_GH_JOBS="+red)
	log := e.log()
	if n := strings.Count(log, "run view 7 -R up/proj --json attempt,headSha"); n != 1 {
		t.Errorf("the re-run asks once after run 7, the first gate's (got %d):\n%s\n%s", n, log, out)
	}
	if i, j := strings.Index(log, "workflow run"), strings.Index(log, "--json attempt,headSha"); i < 0 || j < i {
		t.Errorf("the first gate dispatches a run, the re-run follows it:\n%s", log)
	}
}
