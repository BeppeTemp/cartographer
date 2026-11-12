package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/surveyor/recipe"
	"github.com/BeppeTemp/cartographer/internal/surveyor/recipe/recipetest"
)

const testRecipeStar = `
def lst(req):
    resp = http.request("GET", "https://wiki.example.com/pages", headers = {"Authorization": "Bearer " + env.get("WIKI_TOKEN")})
    return {"entries": [{"key": k, "title": k, "revision": "1", "change": "added", "content": "text of " + k} for k in json.decode(resp["body"])], "next_cursor": "c1"}
recipe("pages", lst)
`

const testRecipeScenario = `env:
  WIKI_TOKEN: test-token-value
http:
  - method: GET
    url: https://wiki.example.com/pages
    response: {body: '["a", "b"]'}
steps:
  - verb: list
    expect: {entries: [a, b], next_cursor: c1, content_contains: "text of b"}
`

// recipePlugin writes a surveyor-only plugin with one Starlark recipe, "pages".
func recipePlugin(t *testing.T, scenario string) (dir, scenarioPath string) {
	t.Helper()
	dir = t.TempDir()
	files := map[string]string{
		"cartographer-plugin.yaml":      "schema: 2\nname: example-wiki\nversion: 0.1.0\ndisplay_name: Wiki\ndescription: Pages of a wiki.\ncartographer: \">=0.29.0\"\ncomponents:\n  surveyor:\n    sdk: {recipes: 1}\n    permissions:\n      net: [wiki.example.com]\n      env: [WIKI_TOKEN]\n",
		"surveyor.star":                 testRecipeStar,
		"recipes/pages/instructions.md": "Each unit is one page.",
		"tests/pages.yaml":              scenario,
	}
	for p, c := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir, filepath.Join(dir, "tests", "pages.yaml")
}

// A Starlark recipe is tested with no network: its http.request is answered
// from tests/, and the mapped variable holds the scenario's value.
func TestPluginTestRunsARecipeAgainstItsScenario(t *testing.T) {
	dir, _ := recipePlugin(t, testRecipeScenario)
	code, out := runPlugin(t, "test", dir, "--output", "json")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	var doc struct {
		Passed  bool                `json:"passed"`
		Results []recipetest.Result `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || !doc.Passed || len(doc.Results) != 1 || doc.Results[0].Check != "step 1 list" {
		t.Fatalf("%v\n%s", err, out)
	}
	if code, out = runPlugin(t, "test", dir); code != 0 || !strings.Contains(out, "pages") || !strings.Contains(out, "pass") {
		t.Errorf("table: exit %d\n%s", code, out)
	}
	if _, set := os.LookupEnv(pluginTestEnvPrefix + "WIKI_TOKEN"); set {
		t.Error("the test value of a variable outlived the run")
	}
}

func TestPluginTestExitCodes(t *testing.T) {
	dir, scenario := recipePlugin(t, testRecipeScenario)
	set := func(s string) {
		if err := os.WriteFile(scenario, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// A step that expects what the recipe does not do is a failure: exit 1.
	set(strings.Replace(testRecipeScenario, "entries: [a, b]", "entries: [a, nope]", 1))
	if code, out := runPlugin(t, "test", dir); code != 1 || !strings.Contains(out, "fail") || !strings.Contains(out, "nope") {
		t.Errorf("a wrong expectation: exit %d\n%s", code, out)
	}
	// A request nobody recorded fails the step, naming it.
	set(strings.Replace(testRecipeScenario, "url: https://wiki.example.com/pages", "url: https://wiki.example.com/other", 1))
	if code, out := runPlugin(t, "test", dir); code != 1 || !strings.Contains(out, "no recorded response for GET https://wiki.example.com/pages") {
		t.Errorf("an unrecorded request: exit %d\n%s", code, out)
	}
	// Could not run: an unknown scenario key, an unknown recipe, no scenario, not a folder.
	set("stepz: []\n")
	if code, _ := runPlugin(t, "test", dir); code != 2 {
		t.Errorf("a malformed scenario: exit %d", code)
	}
	set(testRecipeScenario)
	if code, _ := runPlugin(t, "test", dir, "--recipe", "nope"); code != 2 {
		t.Errorf("an unknown recipe: exit %d", code)
	}
	if code, _ := runPlugin(t, "test", dir, "--recipe", "pages"); code != 0 {
		t.Errorf("--recipe: exit %d", code)
	}
	if err := os.Remove(scenario); err != nil {
		t.Fatal(err)
	}
	if code, _ := runPlugin(t, "test", dir); code != 2 {
		t.Errorf("no scenario: exit %d", code)
	}
	if code, _ := runPlugin(t, "test", filepath.Join(dir, "nope")); code != 2 {
		t.Errorf("not a folder: exit %d", code)
	}
}

// An exec recipe is a program of the folder started as the user: its command is
// printed, escaped, before it runs.
func TestExecAnnouncementNamesTheCommandEscaped(t *testing.T) {
	line := execAnnouncement("lister", &recipe.ExecConfig{Command: "/store/p/run.sh", Args: []string{"--all", "a\x1b[2Jb"}})
	if !strings.Contains(line, "/store/p/run.sh --all") || strings.ContainsRune(line, 0x1b) || !strings.Contains(line, "unapproved") {
		t.Errorf("%q", line)
	}
}
