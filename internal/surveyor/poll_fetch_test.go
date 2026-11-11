package surveyor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/surveyor/recipe"
)

// fetchEnv is a poll env whose feed is an exec recipe listing a.md without
// content, b.md with inline content and one delete; log gets one line per fetch
// call, the keys asked.
func fetchEnv(t *testing.T, mode string) (*pollEnv, string) {
	log := filepath.Join(t.TempDir(), "fetch.log")
	e := newPollEnv(t, Config{})
	cfg := Config{GitAllowHosts: []string{"git.example.com"}, ExecRecipes: []recipe.ExecConfig{{
		Name: "my-feed", Command: os.Args[0], Args: []string{execHelperFlag, mode, log}, Timeout: 20 * time.Second,
		MaxOutput: 1 << 20, MaxContent: 1 << 10,
	}}}
	e.in = e.instance(cfg)
	e.feed()["recipe"], e.feed()["locator"] = "my-feed", "wiki:space/ops"
	delete(e.feed(), "feed_include")
	return e, log
}

func fetchCalls(t *testing.T, log string) []string {
	b, _ := os.ReadFile(log)
	return strings.Fields(string(b))
}

func assetOf(e *pollEnv, id string) string { return e.k.assets[id+"/content.txt"] }

func TestPollFetchStagesContentForNewUnitsOnly(t *testing.T) {
	e, log := fetchEnv(t, "fetch")
	res := e.mustPoll()
	ids := e.k.units()
	if len(ids) != 2 || e.feed()["feed_cursor"] != "c1" || e.feed()["feed_error"] != nil {
		t.Fatalf("units %v, feed %v, %s", ids, e.feed(), res.Summary)
	}
	// Only the entry listed without content was asked for; the delete is only counted.
	if c := fetchCalls(t, log); len(c) != 1 || c[0] != "a.md" {
		t.Fatalf("fetch calls %v", c)
	}
	got := map[string]bool{}
	for _, id := range ids {
		fm := e.k.pages[id].fm
		if fm["status"] != "open" || fm["content_asset"] != "content.txt" {
			t.Fatalf("unit %s = %v", id, fm)
		}
		got[assetOf(e, id)] = true
	}
	if !got["Fetched a.md."] || !got["Inline b."] || len(e.k.assets) != 2 {
		t.Fatalf("assets %v", e.k.assets)
	}
	// The unit executor offers no source_fetch for them (the content is staged); the
	// model was called once per unit and the second poll fetches and writes nothing.
	e.mustPoll()
	if c := fetchCalls(t, log); len(c) != 1 || e.k.calls["asset_write"] != 2 || len(e.model.reqs) != 2 {
		t.Fatalf("second poll: fetch calls %v, writes %d, model %d", c, e.k.calls["asset_write"], len(e.model.reqs))
	}
}

// A crash between the draft unit and its asset leaves a staged unit with no
// content to redo it from: the next poll fetches again for it, and only for it.
func TestPollFetchResumesAStagedUnitAfterACrash(t *testing.T) {
	e, log := fetchEnv(t, "fetch")
	armed := true
	e.k.fault = func(tool string, n int) error {
		if armed && tool == "asset_write" && n == 1 { // after the draft unit of a.md
			armed = false
			return os.ErrDeadlineExceeded
		}
		return nil
	}
	e.poll()
	if armed {
		t.Fatal("the fault never fired")
	}
	e.mustPoll()
	e.mustPoll()
	ids := e.k.units()
	if len(ids) != 2 || e.feed()["feed_cursor"] != "c1" || len(e.model.reqs) != 2 {
		t.Fatalf("units %v, feed %v, model %d", ids, e.feed(), len(e.model.reqs))
	}
	for _, id := range ids {
		if e.k.pages[id].fm["status"] != "open" || assetOf(e, id) == "" {
			t.Fatalf("unit %s = %v, assets %v", id, e.k.pages[id].fm, e.k.assets)
		}
	}
	if c := fetchCalls(t, log); len(c) != 2 {
		t.Fatalf("fetch calls %v: want the first poll and the resume, no third", c)
	}
}

func TestPollFetchFailureEndsThePollWithTheCursorUntouched(t *testing.T) {
	for mode, code := range map[string]string{"fetchfail": "fetch_failed", "fetchbad": "protocol_error"} {
		t.Run(mode, func(t *testing.T) {
			e, _ := fetchEnv(t, mode)
			e.mustPoll()
			fe, _ := e.feed()["feed_error"].(string)
			if !strings.HasPrefix(fe, code+":") || e.feed()["feed_cursor"] != nil || len(e.k.units()) != 0 || len(e.model.reqs) != 0 {
				t.Fatalf("feed_error %q, feed %v, units %v", fe, e.feed(), e.k.units())
			}
		})
	}
}

// A script without the verb (exit 64) is a recipe that lists as before: the
// entry keeps its empty content and the unit executor fetches it itself.
func TestPollRecipeWithoutFetchBehavesAsBefore(t *testing.T) {
	e := newPollEnv(t, Config{})
	e.in = e.instance(Config{GitAllowHosts: []string{"git.example.com"}, ExecRecipes: execCfg("", nil).ExecRecipes})
	e.feed()["recipe"], e.feed()["locator"] = "my-feed", "wiki:space/ops"
	delete(e.feed(), "feed_include")
	e.mustPoll()
	ids := e.k.units()
	if len(ids) != 1 || e.feed()["feed_error"] != nil || len(e.k.assets) != 0 || e.k.pages[ids[0]].fm["content_asset"] != nil {
		t.Fatalf("units %v, feed %v, assets %v", ids, e.feed(), e.k.assets)
	}
}

func TestSourceTestFetchesWhatTheDryRunPrintsAndShowsIt(t *testing.T) {
	log := filepath.Join(t.TempDir(), "fetch.log")
	cfg := Config{ExecRecipes: []recipe.ExecConfig{{Name: "my-feed", Command: os.Args[0], Args: []string{execHelperFlag, "fetch", log},
		Timeout: 20 * time.Second, MaxOutput: 1 << 20, MaxContent: 1 << 10}}}
	rep, err := SourceTest(context.Background(), &cfg, SourceTestInput{Recipe: "my-feed", Locator: "x", Show: "a.md"})
	if err != nil || rep.Failure != nil {
		t.Fatalf("%v %+v", err, rep)
	}
	if rep.Show == nil || rep.Show.Content != "Fetched a.md." || rep.ContentBytes != len("Fetched a.md.")+len("Inline b.") {
		t.Errorf("show %+v, bytes %d", rep.Show, rep.ContentBytes)
	}
	// A failing fetch is the poll-class failure with the poll's own code.
	cfg.ExecRecipes[0].Args = []string{execHelperFlag, "fetchfail", log}
	rep, err = SourceTest(context.Background(), &cfg, SourceTestInput{Recipe: "my-feed", Locator: "x"})
	if err != nil || rep.Failure == nil || rep.Failure.Code != "fetch_failed" {
		t.Errorf("a failing fetch: %v %+v", err, rep)
	}
	// A spec that does not declare fetch is believed: the verb is not called (the
	// helper exits 3 on it).
	cfg = execCfg("v2-nofetch", nil)
	cfg.ExecRecipes[0].Settings = map[string]any{"space": "ENG"}
	rep, err = SourceTest(context.Background(), &cfg, SourceTestInput{Recipe: "my-feed", Locator: "x"})
	if err != nil || rep.Failure != nil || len(rep.Entries) != 1 || rep.ContentBytes != 0 {
		t.Errorf("an undeclared fetch: %v %+v", err, rep)
	}
}

// The poll fetches at most pollFetchBudget bytes in total, checked before each
// batch: the entries after the batch that spent it keep the content they were
// listed with (none), so their units are created as before and the cursor moves.
func TestPollFetchStopsAtTheAggregateBudget(t *testing.T) {
	old := pollFetchBudget
	pollFetchBudget = 1
	t.Cleanup(func() { pollFetchBudget = old })
	e, log := fetchEnv(t, "fetchmany")
	e.model.answers = repeatAnswer(40) // one model call per unit, 30 units
	e.mustPoll()
	if c := fetchCalls(t, log); len(c) != 1 || strings.Count(c[0], ",") != recipe.MaxFetchKeys-1 {
		t.Fatalf("fetch calls %v: want one batch of %d", c, recipe.MaxFetchKeys)
	}
	if len(e.k.units()) != 30 || len(e.k.assets) != recipe.MaxFetchKeys || e.feed()["feed_cursor"] != "c1" || e.feed()["feed_error"] != nil {
		t.Fatalf("units %d, assets %d, feed %v", len(e.k.units()), len(e.k.assets), e.feed())
	}
}
