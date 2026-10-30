package worker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/worker/recipe"
)

func execCfg(mode string, env map[string]string) Config {
	args := []string{execHelperFlag}
	if mode != "" {
		args = append(args, mode)
	}
	return Config{ExecRecipes: []recipe.ExecConfig{{
		Name: "my-feed", Command: os.Args[0], Args: args, Env: env, Timeout: 20 * time.Second,
		MaxOutput: 1 << 20, MaxContent: 1 << 10, Instructions: "An entry is a wiki page.",
	}}}
}

func TestSourceTestRecipeModeListsAndPassesTheCursorOn(t *testing.T) {
	cfg := execCfg("", nil)
	rep, err := SourceTest(context.Background(), &cfg, SourceTestInput{Recipe: "my-feed", Locator: "wiki:space/ops", Cursor: "c0", CursorSet: true})
	if err != nil || rep.Failure != nil {
		t.Fatalf("%v %+v", err, rep)
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Key != "a.md" || rep.NextCursor != "c1" || rep.CursorIn != "c0" || rep.Shape != recipe.ShapeFeed || rep.SourceID != "" {
		t.Errorf("report = %+v", rep)
	}
	if !rep.PollDefaults || rep.MaxEntries != 200 {
		t.Errorf("poll defaults not reported: %+v", rep)
	}
}

func TestSourceTestShowIsCappedAndMarked(t *testing.T) {
	cfg := execCfg("content", nil)
	rep, err := SourceTest(context.Background(), &cfg, SourceTestInput{Recipe: "my-feed", Locator: "x", Show: "a.md"})
	if err != nil || rep.Show == nil || !rep.Show.Found || rep.Show.Content != "Inline page text." || rep.Show.Truncated {
		t.Fatalf("%v %+v", err, rep)
	}
	if rep.ContentBytes != len("Inline page text.") || rep.Tokens != 5 {
		t.Errorf("sizes: %+v", rep)
	}
	rep, _ = SourceTest(context.Background(), &cfg, SourceTestInput{Recipe: "my-feed", Locator: "x", Show: "nope"})
	if rep.Show == nil || rep.Show.Found {
		t.Errorf("a key not in the listing must report not found: %+v", rep.Show)
	}
}

func TestSourceTestListFailureIsClass1WithThePollsCode(t *testing.T) {
	cfg := execCfg("fail", nil)
	rep, err := SourceTest(context.Background(), &cfg, SourceTestInput{Recipe: "my-feed", Locator: "x"})
	if err != nil || rep.Failure == nil || rep.Failure.Code != codeListFailed || !strings.Contains(rep.Failure.Detail, "lister broke") {
		t.Fatalf("%v %+v", err, rep)
	}
}

func TestSourceTestStartErrors(t *testing.T) {
	cfg := execCfg("", map[string]string{"WIKI_TOKEN": "TEST_UNSET_WIKI_VAR"})
	_, err := SourceTest(context.Background(), &cfg, SourceTestInput{Recipe: "nope", Locator: "x"})
	if !IsSourceTestError(err) || !strings.Contains(err.Error(), "my-feed") || !strings.Contains(err.Error(), "git-docs") {
		t.Errorf("unknown recipe lists the known names: %v", err)
	}
	_, err = SourceTest(context.Background(), &cfg, SourceTestInput{Recipe: "my-feed", Locator: "x"})
	if !IsSourceTestError(err) || !strings.Contains(err.Error(), "TEST_UNSET_WIKI_VAR") {
		t.Errorf("an unset variable is named: %v", err)
	}
	t.Setenv("TEST_UNSET_WIKI_VAR", "marker-secret-value")
	rep, err := SourceTest(context.Background(), &cfg, SourceTestInput{Recipe: "my-feed", Locator: "x"})
	if err != nil || fmt.Sprintf("%+v", rep) == "" || strings.Contains(fmt.Sprintf("%+v", rep), "marker-secret-value") {
		t.Errorf("the value must not reach the report: %v %+v", err, rep)
	}
}

// The dry run and the poll share feedPreconditions and listFeed: for each
// failure the poll would write, the dry run reports the same code and text.
func TestSourceTestReportsWhatThePollWouldWrite(t *testing.T) {
	for name, set := range map[string]map[string]any{
		"recipe unknown": {"recipe": "ghost"},
		"bad field":      {"feed_interval": "10m"},
		"host":           {"locator": "https://evil.example.org/x.git"},
		"clone fails":    {},
	} {
		t.Run(name, func(t *testing.T) {
			e := newPollEnv(t, Config{})
			for k, v := range set {
				e.feed()[k] = v
			}
			if name == "clone fails" {
				e.cloneErr = fmt.Errorf("clone broke")
			}
			rep, err := SourceTest(context.Background(), &Config{GitAllowHosts: []string{"git.example.com"}},
				SourceTestInput{Source: "sources/feed", Tools: e.k, Clone: e.in.clone})
			if err != nil || rep.Failure == nil {
				t.Fatalf("%v %+v", err, rep)
			}
			e.mustPoll()
			got, _ := e.feed()["feed_error"].(string)
			if !strings.HasPrefix(got, rep.Failure.Code+": ") || !strings.Contains(got, rep.Failure.Detail) {
				t.Errorf("poll wrote %q, dry run said %+v", got, rep.Failure)
			}
		})
	}
}

func TestSourceTestSourceModeListsLikeThePollAndWritesNothing(t *testing.T) {
	e := newPollEnv(t, Config{})
	vers := map[string]int{}
	for id, p := range e.k.pages {
		vers[id] = p.ver
	}
	rep, err := SourceTest(context.Background(), &Config{GitAllowHosts: []string{"git.example.com"}},
		SourceTestInput{Source: "sources/feed", Tools: e.k, Clone: e.in.clone})
	if err != nil || rep.Failure != nil {
		t.Fatalf("%v %+v", err, rep)
	}
	if len(rep.Entries) != 2 || rep.Entries[0].Key != "pg/a.md" || rep.SourceID != "sources/feed" {
		t.Errorf("entries = %+v", rep.Entries)
	}
	for tool := range e.k.calls {
		if !dryRunTools[tool] {
			t.Errorf("the dry run called %s", tool)
		}
	}
	for id, p := range e.k.pages {
		if vers[id] != p.ver || len(e.k.pages) != len(vers) {
			t.Errorf("%s changed", id)
		}
	}
	if len(rep.Warnings) != 1 || strings.Contains(strings.Join(rep.Warnings, ""), codeNoPlan) {
		t.Errorf("warnings = %v (the plan exists; only the poll-defaults one is expected)", rep.Warnings)
	}
	delete(e.k.pages, "ingest/plan")
	rep, _ = SourceTest(context.Background(), &Config{GitAllowHosts: []string{"git.example.com"}},
		SourceTestInput{Source: "sources/feed", Tools: e.k, Clone: e.in.clone})
	if !strings.Contains(strings.Join(rep.Warnings, "|"), codeNoPlan) {
		t.Errorf("no plan must warn: %v", rep.Warnings)
	}
}

func TestReadOnlyRefusesWrites(t *testing.T) {
	e := newPollEnv(t, Config{})
	ro := readOnly{e.k}
	for _, tool := range []string{"concept_patch", "source_register", "concept_batch", "asset_write", "log_append"} {
		if _, err := ro.CallTool(tool, map[string]any{}); err == nil {
			t.Errorf("%s reached the server", tool)
		}
	}
	if e.k.calls["concept_patch"] != 0 {
		t.Error("a refused call reached the KB")
	}
}

func TestSourceTestNeedsNoModelKeyAndNoTokenInRecipeMode(t *testing.T) {
	body := cfgGolden
	path := t.TempDir() + "/worker.yaml"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKER_TOKEN", "")
	t.Setenv("CLOUD_API_KEY", "")
	if _, err := LoadConfig(path, cfgKinds()); err == nil {
		t.Fatal("the full load must still require the variables")
	}
	if _, err := LoadConfigScoped(path, cfgKinds(), EnvScope{}); err != nil {
		t.Errorf("no variable is required: %v", err)
	}
	if _, err := LoadConfigScoped(path, cfgKinds(), EnvScope{Token: true}); err == nil || !strings.Contains(err.Error(), "WORKER_TOKEN") {
		t.Errorf("token scope names the token variable: %v", err)
	}
	bad := strings.Replace(body, "name: docs-night", "bogus_key: 1\nname: docs-night", 1)
	_ = os.WriteFile(path, []byte(bad), 0o600)
	if _, err := LoadConfigScoped(path, cfgKinds(), EnvScope{}); err == nil {
		t.Error("the structure is validated in full whatever the scope")
	}
}

// The lister reference must not rot against the code: its protocol facts, its
// config snippet and its skeleton are checked here.
func readRef(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../skillbundle/bundled/cartographer-kb-source-author/references/lister-protocol.md")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func fence(t *testing.T, text, lang string) string {
	t.Helper()
	_, rest, ok := strings.Cut(text, "```"+lang+"\n")
	if !ok {
		t.Fatalf("no %s block", lang)
	}
	body, _, _ := strings.Cut(rest, "```")
	return body
}

func TestListerReferenceNamesTheProtocol(t *testing.T) {
	ref := readRef(t)
	for _, want := range []string{"`protocol`: `1`", `"list"`, recipe.Added, recipe.Modified, recipe.Removed, "next_cursor"} {
		if !strings.Contains(ref, want) {
			t.Errorf("lister-protocol.md lacks %q", want)
		}
	}
}

func TestListerReferenceSnippetLoadsWithTheStructuralLoader(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "recipes"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "recipes", "wiki-changes.md"), []byte("An entry is a page."), 0o600); err != nil {
		t.Fatal(err)
	}
	conn := "connectors:\n  docs-wiki:\n    transport: http\n    url: https://wiki.example.com/mcp\n    tools: [get_page]\n"
	path := filepath.Join(dir, "worker.yaml")
	if err := os.WriteFile(path, []byte(cfgGolden+conn+fence(t, readRef(t), "yaml")), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfigScoped(path, cfgKinds(), EnvScope{})
	if err != nil {
		t.Fatalf("the reference's snippet does not load: %v", err)
	}
	if len(cfg.ExecRecipes) != 1 || cfg.ExecRecipes[0].Name != "wiki-changes" {
		t.Errorf("recipes = %+v", cfg.ExecRecipes)
	}
}

func TestListerReferenceSkeletonProducesWhatTheRunnerAccepts(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not on PATH")
	}
	script := filepath.Join(t.TempDir(), "lister.py")
	if err := os.WriteFile(script, []byte(fence(t, readRef(t), "python")), 0o600); err != nil {
		t.Fatal(err)
	}
	r := recipe.NewExec(recipe.ExecConfig{Name: "x", Command: py, Args: []string{script}, Timeout: 20 * time.Second, MaxOutput: 1 << 20, MaxContent: 1 << 10})
	l, err := r.List(context.Background(), recipe.Request{ID: "s", MaxEntries: 5})
	if err != nil || len(l.Entries) != 0 || l.NextCursor != "" {
		t.Errorf("listing = %+v, %v", l, err)
	}
}
