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
	"github.com/BeppeTemp/cartographer/internal/worker/system"
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

func codeBlock(t *testing.T, text, lang string) string {
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
	for _, want := range []string{"`protocol`: `1`", `"list"`, recipe.Added, recipe.Modified, recipe.Removed, "next_cursor", "preflight", "read_only"} {
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
	if err := os.WriteFile(path, []byte(cfgGolden+conn+codeBlock(t, readRef(t), "yaml")), 0o600); err != nil {
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
	if err := os.WriteFile(script, []byte(codeBlock(t, readRef(t), "python")), 0o600); err != nil {
		t.Fatal(err)
	}
	r := recipe.NewExec(recipe.ExecConfig{Name: "x", Command: py, Args: []string{script}, Timeout: 20 * time.Second, MaxOutput: 1 << 20, MaxContent: 1 << 10})
	l, err := r.List(context.Background(), recipe.Request{ID: "s", MaxEntries: 5})
	if err != nil || len(l.Entries) != 0 || l.NextCursor != "" {
		t.Errorf("listing = %+v, %v", l, err)
	}
}

func sysTest(t *testing.T, s *sysEnv, in SourceTestInput) *SourceTestReport {
	t.Helper()
	cfg := s.cfg()
	rep, err := SourceTest(context.Background(), &cfg, in)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestSourceTestSystemRecipeModePreflightsListsAndHasNoState(t *testing.T) {
	s := newSysEnv(t)
	s.list("namespace/team-a", "deployment api image:v1\n"+snapCanary, "namespace/team-b", "deployment web image:v1")
	rep := sysTest(t, s, SourceTestInput{Recipe: "k8s", Locator: "kubernetes://dev-cluster"})
	if rep.Failure != nil || rep.Shape != recipe.ShapeSystem || rep.Identity != "dev-viewer" || rep.StateStatus != "no stored state" {
		t.Fatalf("%+v", rep)
	}
	if len(rep.Entries) != 2 || rep.Counts["added"] != 2 || rep.Counts["unchanged"] != 0 || rep.NextCursor == "" {
		t.Errorf("%+v", rep)
	}
	// Revisions are the worker's, not the lister's.
	if rep.Entries[0].Key != "namespace/team-a" || rep.Entries[0].Revision != system.Revision("deployment api image:v1\n"+snapCanary) {
		t.Errorf("entry = %+v", rep.Entries[0])
	}
	if s.modes() != "preflight,list" {
		t.Errorf("modes = %s: the preflight must precede the listing", s.modes())
	}
	if strings.Contains(fmt.Sprintf("%+v", rep), snapCanary) {
		t.Error("the snapshot reached the report without --show")
	}
	rep = sysTest(t, s, SourceTestInput{Recipe: "k8s", Locator: "x", Show: "namespace/team-b", Cursor: "ignored", CursorSet: true})
	if rep.Show == nil || !rep.Show.Found || !strings.Contains(rep.Show.Content, "## Observed") || !strings.Contains(rep.Show.Content, "First observation.") {
		t.Errorf("show = %+v", rep.Show)
	}
	if !strings.Contains(strings.Join(rep.Warnings, "|"), "--cursor is ignored") {
		t.Errorf("warnings = %v", rep.Warnings)
	}
}

// The state is read with Load only and the dry run writes nothing: after a real
// poll stored the state, a dry run diffs against it and leaves every page, asset
// and tool-call counter of the write tools as it was.
func TestSourceTestSystemSourceModeDiffsAgainstTheStoredStateAndWritesNothing(t *testing.T) {
	s := newSysEnv(t)
	s.list("namespace/team-a", "v1 a", "namespace/team-b", "v1 b", "namespace/team-c", "v1 c")
	s.mustPoll()
	state, vers, calls := s.stateFile(), map[string]int{}, map[string]int{}
	for id, p := range s.k.pages {
		vers[id] = p.ver
	}
	for k, v := range s.k.calls {
		calls[k] = v
	}
	s.list("namespace/team-a", "v1 a", "namespace/team-b", "v2 b", "namespace/team-d", "v1 d") // c removed, b modified, d added
	rep := sysTest(t, s, SourceTestInput{Source: "sources/feed", Tools: s.k})
	if rep.Failure != nil || rep.StateStatus != "trusted, 3 groups" {
		t.Fatalf("%+v", rep)
	}
	if rep.Counts["added"] != 1 || rep.Counts["modified"] != 1 || rep.Counts["removed"] != 1 || rep.Counts["unchanged"] != 1 {
		t.Errorf("counts = %v", rep.Counts)
	}
	var kinds []string
	for _, e := range rep.Entries {
		kinds = append(kinds, e.Change+":"+e.Key)
	}
	if got := strings.Join(kinds, ","); got != "modified:namespace/team-b,removed:namespace/team-c,added:namespace/team-d" {
		t.Errorf("entries (key order) = %s", got)
	}
	rep = sysTest(t, s, SourceTestInput{Source: "sources/feed", Tools: s.k, Show: "namespace/team-b"})
	if !strings.Contains(rep.Show.Content, "v2 b") {
		t.Errorf("show = %q", rep.Show.Content)
	}
	if s.stateFile() != state || len(s.k.pages) != len(vers) {
		t.Error("the state asset or the page set changed")
	}
	for id, p := range s.k.pages {
		if vers[id] != p.ver {
			t.Errorf("%s was written", id)
		}
	}
	for tool, n := range s.k.calls {
		if n != calls[tool] && !dryRunTools[tool] {
			t.Errorf("the dry run called %s", tool)
		}
	}
}

func TestSourceTestSystemUntrustedStateAndEmptyListing(t *testing.T) {
	s := newSysEnv(t)
	s.list("namespace/team-a", "v1 a")
	s.mustPoll()
	s.feed()["feed_cursor"] = "not-the-digest"
	rep := sysTest(t, s, SourceTestInput{Source: "sources/feed", Tools: s.k})
	if rep.Failure != nil || !strings.HasPrefix(rep.StateStatus, "untrusted (the state file does not match feed_cursor)") || rep.Counts["added"] != 1 {
		t.Errorf("%+v", rep)
	}
	s.feed()["feed_cursor"] = sha(s.stateFile())
	s.list()
	rep = sysTest(t, s, SourceTestInput{Source: "sources/feed", Tools: s.k})
	if rep.Failure == nil || rep.Failure.Code != codeEmptyListing || rep.PreflightRefused {
		t.Errorf("%+v", rep)
	}
}

func TestSourceTestSystemRefusedPreflightListsNothing(t *testing.T) {
	for name, answer := range map[string]string{
		"not read only": `{"read_only":false,"identity":"cluster-admin","detail":"can delete"}`,
		"malformed":     `not json`,
		"no boolean":    `{"identity":"x"}`,
	} {
		t.Run(name, func(t *testing.T) {
			s := newSysEnv(t)
			s.list("namespace/team-a", "v1")
			s.preflight(answer)
			rep := sysTest(t, s, SourceTestInput{Recipe: "k8s", Locator: "x"})
			if !rep.PreflightRefused || rep.Failure == nil || rep.Failure.Code != recipe.CodeIdentityNotReadOnly || len(rep.Entries) != 0 {
				t.Errorf("%+v", rep)
			}
			if s.modes() != "preflight" {
				t.Errorf("modes = %s: nothing may be listed after a refusal", s.modes())
			}
		})
	}
	s := newSysEnv(t)
	if err := os.Remove(filepath.Join(s.dir, "preflight.json")); err != nil {
		t.Fatal(err)
	}
	rep := sysTest(t, s, SourceTestInput{Recipe: "k8s", Locator: "x"})
	if !rep.PreflightRefused || s.modes() != "preflight" {
		t.Errorf("a preflight that crashes is a refusal: %+v (%s)", rep, s.modes())
	}
}

// A recipe whose shape differs from the Source's is refused in both directions,
// with the poll's own text (the poll decides by the recipe's shape).
func TestSourceTestShapeMismatchReportsWhatThePollWrites(t *testing.T) {
	for name, tc := range map[string]struct{ shape, recipe string }{
		"system source, feed recipe": {"system", "my-feed"},
		"feed source, system recipe": {"feed", "k8s"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSysEnv(t)
			s.list("namespace/team-a", "v1")
			s.feed()["source_shape"], s.feed()["recipe"] = tc.shape, tc.recipe
			rep := sysTest(t, s, SourceTestInput{Source: "sources/feed", Tools: s.k})
			if rep.Failure == nil || rep.Failure.Code != codeRecipeShape || rep.PreflightRefused {
				t.Fatalf("%+v", rep)
			}
			s.mustPoll()
			got, _ := s.feed()["feed_error"].(string)
			if got != codeRecipeShape+": "+rep.Failure.Detail {
				t.Errorf("poll wrote %q, dry run said %+v", got, rep.Failure)
			}
			if s.modes() != "" {
				t.Errorf("the script ran (%s) despite the mismatch", s.modes())
			}
		})
	}
}
