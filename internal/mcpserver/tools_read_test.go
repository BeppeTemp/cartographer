package mcpserver

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// commitRun commits the working tree inside the window with a Surveyor-Run
// trailer (none when run is empty).
func (c *changesLinksKB) commitRun(run string) {
	c.t.Helper()
	c.n++
	at := c.base.Add(time.Duration(c.n) * time.Minute).Format(time.RFC3339)
	msg := fmt.Sprintf("test: step %d", c.n)
	if run != "" {
		msg += "\n\nSurveyor-Run: " + run
	}
	if err := gitx.Commit(c.k.Root, msg, "Tester", "tester@example.test", "GIT_AUTHOR_DATE="+at, "GIT_COMMITTER_DATE="+at); err != nil {
		c.t.Fatal(err)
	}
}

// AC-surveyor-run-commit-6: changes_since returns surveyor_runs per concept,
// distinct, newest first, capped, and nothing for a concept (or for a commit
// that also touched a page) outside the caller's perimeter.
func TestChangesSinceSurveyorRuns(t *testing.T) {
	t.Parallel()
	c := newChangesLinksKB(t)
	c.commit(false)
	since := fmt.Sprintf(`{"since":%q}`, c.base.Format(time.RFC3339))
	runsOf := func(r changesSinceResult, id string) []string {
		for _, ch := range r.Concepts {
			if ch.ID == id {
				return ch.SurveyorRuns
			}
		}
		return nil
	}

	c.write("ops/a", "A1.\n")
	c.commitRun("rep-1-1")
	c.write("ops/a", "A2.\n")
	c.commitRun("rep-1-2")
	c.write("ops/a", "A3.\n")
	c.commitRun("rep-1-2") // distinct
	c.write("ops/b", "B.\n")
	c.commit(true) // no trailer: omitted
	c.write("ops/c", "C.\n")
	c.write("hidden/h", "H.\n")
	c.commitRun("rep-2-7") // one commit across the perimeter

	got := c.call(adminCtx, since)
	if r := runsOf(got, "ops/a"); !reflect.DeepEqual(r, []string{"rep-1-2", "rep-1-1"}) {
		t.Errorf("ops/a runs = %v, want newest first and distinct", r)
	}
	if r := runsOf(got, "ops/b"); r != nil {
		t.Errorf("ops/b runs = %v, want none", r)
	}
	if r := runsOf(got, "hidden/h"); !reflect.DeepEqual(r, []string{"rep-2-7"}) {
		t.Errorf("admin: hidden/h runs = %v", r)
	}

	narrow := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"ops"}}}})
	nr := c.call(narrow, since)
	if r := runsOf(nr, "hidden/h"); r != nil {
		t.Errorf("narrowed: hidden/h runs = %v, want none", r)
	}
	// ops/c shares the commit with hidden/h: the caller sees the id on the page
	// it can see, and the hidden page is absent.
	if r := runsOf(nr, "ops/c"); !reflect.DeepEqual(r, []string{"rep-2-7"}) {
		t.Errorf("narrowed: ops/c runs = %v", r)
	}
	for _, ch := range nr.Concepts {
		if ch.ID == "hidden/h" {
			t.Errorf("narrowed answer lists hidden/h")
		}
	}

	// The cap.
	for i := 0; i < maxSurveyorRuns+3; i++ {
		c.write("ops/many", fmt.Sprintf("M%d.\n", i))
		c.commitRun(fmt.Sprintf("rep-3-%d", i))
	}
	r := runsOf(c.call(adminCtx, since), "ops/many")
	if len(r) != maxSurveyorRuns || r[0] != fmt.Sprintf("rep-3-%d", maxSurveyorRuns+2) {
		t.Errorf("ops/many runs = %v, want %d newest first", r, maxSurveyorRuns)
	}

	// A forged trailer that is not a valid id (pushed by git, not the server) is not returned.
	c.write("ops/bad", "X.\n")
	c.commitRun("has space")
	if r := runsOf(c.call(adminCtx, since), "ops/bad"); r != nil {
		t.Errorf("ops/bad runs = %v, want none", r)
	}
}
