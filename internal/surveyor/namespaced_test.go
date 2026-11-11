package surveyor

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/surveyor/recipe"
)

// A Source naming "<plugin>/<recipe>" reaches the recipe registered under that
// whole string; the unprefixed name of the same recipe, and a plugin that is not
// pinned, are recipe_unknown (a KB never selects code).
func TestPollResolvesAQualifiedRecipeName(t *testing.T) {
	cfg := Config{GitAllowHosts: []string{"git.example.com"}, ExecRecipes: []recipe.ExecConfig{{
		Name: "acme-wiki/feed", Command: os.Args[0], Args: []string{execHelperFlag, "fetch", os.DevNull},
		Timeout: 20 * time.Second, MaxOutput: 1 << 20, MaxContent: 1 << 10,
	}}}
	for name, tc := range map[string]struct{ recipe, code string }{
		"qualified":         {"acme-wiki/feed", ""},
		"local part alone":  {"feed", "recipe_unknown"},
		"unknown plugin":    {"other-plugin/feed", "recipe_unknown"},
		"unknown in plugin": {"acme-wiki/nope", "recipe_unknown"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newPollEnv(t, Config{})
			e.in = e.instance(cfg)
			e.feed()["recipe"], e.feed()["locator"] = tc.recipe, "wiki:space/ops"
			delete(e.feed(), "feed_include")
			_, res, err := e.poll()
			if err != nil {
				t.Fatal(err)
			}
			fe, _ := e.feed()["feed_error"].(string)
			if tc.code != "" {
				if !strings.HasPrefix(fe, tc.code+": ") || len(e.k.units()) != 0 {
					t.Fatalf("feed_error %q, units %v", fe, e.k.units())
				}
				return
			}
			if fe != "" || len(e.k.units()) == 0 {
				t.Fatalf("feed_error %q, units %v (%s)", fe, e.k.units(), res.Summary)
			}
		})
	}
}

// The connector of a Source may be qualified: the unit starts it by that name and
// the model sees and calls its tools by the alias.
func TestIngestRoutesAQualifiedConnector(t *testing.T) {
	var reads, writes atomic.Int32
	srv := wikiServer(&reads, &writes)
	defer srv.Close()
	cfg := wikiConfig(srv.URL)
	cfg.Name = "acme-wiki/wiki"
	k := newIKB()
	k.src("s/wiki", map[string]any{"connector": "acme-wiki/wiki", "locator": "wiki:page/7"})
	k.unit("u/1", "s/wiki", "open", "any", nil)
	k.src("s/other", map[string]any{"connector": "other-plugin/wiki", "locator": "wiki:page/8"})
	k.unit("u/2", "s/other", "open", "any", nil)
	in := connectorInst(t, cfg)
	it := next(t, in, k)
	if it == nil || it.Key != "u/1" {
		t.Fatalf("offered %+v: the unit of the declared qualified connector first", it)
	}
	args := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
	m := &recModel{turns: []Response{
		{Stop: StopToolUse, Usage: Usage{InputTokens: 100, OutputTokens: 10}, ToolCalls: []ToolCall{
			{ID: "1", Name: "acme-wiki--wiki__read", Arguments: args(map[string]any{"id": "7"})},
			{ID: "2", Name: "acme-wiki--wiki__write", Arguments: args(map[string]any{})},
			{ID: "3", Name: "concept_write", Arguments: args(map[string]any{"id": "kb/p", "body": "text", "reason": "unit"})},
		}},
		{Stop: StopEnd, Text: "done", Usage: Usage{InputTokens: 100, OutputTokens: 10}},
	}}
	if res, err := in.Run(context.Background(), it, rtFor(k, m, "r1")); err != nil || res.Units != 1 {
		t.Fatalf("run: %+v %v", res, err)
	}
	if reads.Load() != 1 || writes.Load() != 0 {
		t.Fatalf("server saw %d reads and %d writes", reads.Load(), writes.Load())
	}
	// The unit whose Source names a plugin connector that is not declared is
	// skipped, not failed and not offered.
	if again := next(t, in, k); again != nil {
		t.Fatalf("the undeclared plugin's unit was offered: %+v", again)
	}
	if k.status("u/2") != "open" {
		t.Fatalf("u/2 = %s", k.status("u/2"))
	}
}
