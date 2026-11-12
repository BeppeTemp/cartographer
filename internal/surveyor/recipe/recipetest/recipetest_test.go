package recipetest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/surveyor/recipe"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The test binary re-executed as an exec recipe: no interpreter, so the tests
// run the same on Linux, macOS and Windows. Modes: good (conforms), flaky (a
// listing that differs on every call; args[0] counts them), chatty (answers a
// verb it does not know instead of exiting 64).
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == "--helper" {
		helper(os.Args[2], os.Args[3:])
	}
	if os.Getenv("CONNECTOR_TEST_HELPER") == "1" { // the same binary as a stdio MCP server
		if err := mcpHelper().Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func helper(mode string, args []string) {
	var req struct {
		Protocol int
		Verb     string
	}
	_ = json.NewDecoder(os.Stdin).Decode(&req)
	if mode != "chatty" && (req.Protocol != 2 || !slices.Contains([]string{"spec", "check", "list"}, req.Verb)) {
		os.Exit(64)
	}
	switch req.Verb {
	case "spec":
		fmt.Println(`{"spec":{"title":"T","description":"d","shapes":["feed"],"verbs":["spec","check","list"],"settings":{},"env":[]}}`)
	case "check":
		fmt.Println(`{"check":{"ok":true,"detail":"ok"}}`)
	case "list":
		title := "A"
		if mode == "flaky" {
			b, _ := os.ReadFile(args[0])
			n, _ := strconv.Atoi(string(b))
			_ = os.WriteFile(args[0], []byte(strconv.Itoa(n+1)), 0o600)
			title = "A" + strconv.Itoa(n)
		}
		fmt.Printf(`{"entry":{"key":"a","title":%q,"revision":"1","change":"added"}}`+"\n", title)
		fmt.Println(`{"next_cursor":"c1"}`)
	default:
		fmt.Println(`{"next_cursor":""}`)
	}
	os.Exit(0)
}

func execInput(t *testing.T, mode string, args ...string) Input {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Input{Name: "wiki", Scenario: scenario(), Exec: &recipe.ExecConfig{Name: "acme/wiki", Command: exe,
		Args: append([]string{"--helper", mode}, args...), Timeout: time.Minute, MaxOutput: 1 << 20, MaxContent: 1 << 10, Instructions: "x"}}
}

func status(res []Result, check string) string {
	for _, r := range res {
		if r.Check == check {
			return r.Status
		}
	}
	return "absent"
}

// feedRecipe is a feed recipe that conforms but for what its list returns.
func feedRecipe(result string) string {
	return `
def spec(r):
    return {"title": "Wiki", "description": "d", "shapes": ["feed"], "verbs": ["spec", "check", "list"], "settings": {}, "env": []}
def check(r):
    return {"ok": True}
def lst(r):
    return ` + result + `
recipe("wiki", lst, spec = spec, check = check)
`
}

// systemRecipe is a system recipe, read-only check included, whose list returns result.
func systemRecipe(result string) string {
	return `
def spec(r):
    return {"title": "S", "description": "d", "shapes": ["system"], "verbs": ["spec", "check", "list"], "settings": {}, "env": []}
def check(r):
    return {"ok": True, "read_only": True, "identity": "reader"}
def lst(r):
    return ` + result + `
recipe("wiki", lst, shape = "system", spec = spec, check = check)
`
}

// A recipe that conforms passes every check it can be asked, and what it cannot
// be asked is skipped with a reason.
const conforming = `
def spec(r):
    return {"title": "Wiki", "description": "d", "shapes": ["feed"], "verbs": ["spec", "check", "list"], "settings": {}, "env": ["TOKEN"]}
def check(r):
    return {"ok": True, "detail": "reachable"}
def lst(r):
    if r.get("cursor") == "c1":
        return {"entries": [{"key": "b", "title": "B", "revision": "1", "change": "added"}], "next_cursor": "c2"}
    return {"entries": [{"key": "a", "title": "A", "revision": "1", "change": "added"}], "next_cursor": "c1"}
recipe("wiki", lst, spec = spec, check = check)
`

func TestAConformingRecipePassesEveryCheck(t *testing.T) {
	sc := scenario(
		Step{Verb: "check", Expect: Expect{OK: new(true)}},
		Step{Verb: "list", Expect: Expect{Entries: []any{"a"}, NextCursor: "c1"}},
		Step{Verb: "list", Request: StepRequest{Cursor: "@next"}, Expect: Expect{Entries: 1, NextCursor: "nonempty"}},
	)
	res := Run(context.Background(), starInput(t, conforming, "feed", sc))
	for _, r := range res {
		if r.Status == Fail {
			t.Errorf("%+v", r)
		}
	}
	for _, c := range []string{"spec", "parser", "entries", "repeatable", "incremental", "secrets"} {
		if got := status(res, c); got != Pass {
			t.Errorf("%s: %s", c, got)
		}
	}
	for _, c := range []string{"system", "fetch", "protocol"} {
		if got := status(res, c); got != Skip {
			t.Errorf("%s: %s, want skip", c, got)
		}
	}
	if len(res) != len(Checks)+2 { // the steps and every check but the connector one
		t.Errorf("%d results, want one per check and per step", len(res))
	}
}

func TestEveryCheckFailsOnItsNonConformingRecipe(t *testing.T) {
	two := scenario(Step{Verb: "list"}, Step{Verb: "list", Request: StepRequest{Cursor: "@next"}})
	entry := `{"key": "a", "title": "A", "revision": "1", "change": "added"}`
	for _, tc := range []struct {
		check string
		in    func(t *testing.T) Input
	}{
		{"spec", func(t *testing.T) Input {
			return starInput(t, strings.Replace(conforming, `"verbs": ["spec", "check", "list"]`, `"verbs": ["spec", "list"]`, 1), "feed", scenario())
		}},
		{"spec", func(t *testing.T) Input { // env outside the manifest's
			return starInput(t, strings.Replace(conforming, `"env": ["TOKEN"]`, `"env": ["OTHER"]`, 1), "feed", scenario())
		}},
		{"parser", func(t *testing.T) Input { // a revision that is not a string: the parser refuses the line
			return starInput(t, feedRecipe(`{"entries": [{"key": "a", "title": "A", "revision": 1, "change": "added"}], "next_cursor": "c"}`), "feed", scenario())
		}},
		{"entries", func(t *testing.T) Input { // a key twice
			return starInput(t, feedRecipe(`{"entries": [`+entry+`, `+entry+`], "next_cursor": "c"}`), "feed", scenario())
		}},
		{"entries", func(t *testing.T) Input { // no key
			return starInput(t, feedRecipe(`{"entries": [{"title": "A", "revision": "1", "change": "added"}], "next_cursor": "c"}`), "feed", scenario())
		}},
		{"entries", func(t *testing.T) Input { // no revision
			return starInput(t, feedRecipe(`{"entries": [{"key": "a", "title": "A", "change": "added"}], "next_cursor": "c"}`), "feed", scenario())
		}},
		{"entries", func(t *testing.T) Input { // an entry that says it was removed (a deletion is a delete line)
			return starInput(t, feedRecipe(`{"entries": [{"key": "a", "title": "A", "revision": "1", "change": "removed"}], "next_cursor": "c"}`), "feed", scenario())
		}},
		{"system", func(t *testing.T) Input { // a system listing with a cursor to resume from
			return starInput(t, systemRecipe(`{"entries": [{"key": "k", "content": "x"}], "next_cursor": "more"}`), "system", scenario())
		}},
		{"system", func(t *testing.T) Input { // a system listing with a delete line
			return starInput(t, systemRecipe(`{"entries": [{"key": "k", "content": "x"}], "deletes": ["gone"], "next_cursor": ""}`), "system", scenario())
		}},
		{"incremental", func(t *testing.T) Input {
			src := "def lst(r):\n    return {\"entries\": [" + entry + "], \"next_cursor\": \"c1\"}\nrecipe(\"wiki\", lst)\n"
			return starInput(t, src, "feed", two)
		}},
		{"fetch", func(t *testing.T) Input { // declares fetch and answers no key
			src := `
def spec(r):
    return {"title": "Wiki", "description": "d", "shapes": ["feed"], "verbs": ["spec", "check", "list", "fetch"], "settings": {}, "env": []}
def check(r):
    return {"ok": True}
def lst(r):
    return {"entries": [{"key": "a", "title": "A", "revision": "1", "change": "added"}], "next_cursor": "c1"}
def f(r):
    return {"contents": {}}
recipe("wiki", lst, spec = spec, check = check, fetch = f)
`
			return starInput(t, src, "feed", scenario())
		}},
		{"system", func(t *testing.T) Input { // a check that does not prove the identity read-only
			src := `
def spec(r):
    return {"title": "S", "description": "d", "shapes": ["system"], "verbs": ["spec", "check", "list"], "settings": {}, "env": []}
def check(r):
    return {"ok": True, "read_only": False, "identity": "writer"}
def lst(r):
    return {"entries": [{"key": "k", "content": "x"}], "next_cursor": ""}
recipe("wiki", lst, shape = "system", spec = spec, check = check)
`
			return starInput(t, src, "system", scenario(Step{Verb: "list", Expect: Expect{Error: recipe.CodeIdentityNotReadOnly}}))
		}},
		{"secrets", func(t *testing.T) Input { // the value of a declared variable in an entry title
			src := "def lst(r):\n    return {\"entries\": [{\"key\": \"a\", \"title\": env.get(\"TOKEN\"), \"revision\": \"1\", \"change\": \"added\"}], \"next_cursor\": \"c\"}\nrecipe(\"wiki\", lst)\n"
			sc := scenario()
			sc.Env["TOKEN"] = "tok-secret-value"
			return starInput(t, src, "feed", sc)
		}},
		{"repeatable", func(t *testing.T) Input { return execInput(t, "flaky", filepath.Join(t.TempDir(), "count")) }},
		{"protocol", func(t *testing.T) Input { return execInput(t, "chatty") }},
	} {
		t.Run(tc.check, func(t *testing.T) {
			res := Run(context.Background(), tc.in(t))
			if got := status(res, tc.check); got != Fail {
				t.Errorf("%s: %s, want fail; %+v", tc.check, got, res)
			}
			// The entry and system rules are the parser's: it is read once, by the
			// check that owns the rule, and the parser check is silent on them.
			if tc.check == "entries" || tc.check == "system" || tc.check == "parser" {
				for _, c := range Checks {
					if c.Name != tc.check && status(res, c.Name) == Fail {
						t.Errorf("%s fixture also fails %s: %+v", tc.check, c.Name, res)
					}
				}
			}
		})
	}
	// The exec helper that conforms passes the two checks that need a process.
	res := Run(context.Background(), execInput(t, "good"))
	for _, c := range []string{"spec", "parser", "repeatable", "protocol"} {
		if got := status(res, c); got != Pass {
			t.Errorf("good exec recipe, %s: %s; %+v", c, got, res)
		}
	}
}

// docs/plugins.md lists the checks by name: the list in the kit and the doc
// cannot drift (the harness's checks are named in one place each).
func TestChecksAreDocumented(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "docs", "plugins.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(doc), "\n### Conformance checks\n")
	if !ok {
		t.Fatal("docs/plugins.md has no section ### Conformance checks")
	}
	if i := strings.Index(section, "\n#"); i >= 0 {
		section = section[:i]
	}
	var documented, kit []string
	for _, m := range regexp.MustCompile("(?m)^\\| `([a-z]+)` \\|").FindAllStringSubmatch(section, -1) {
		documented = append(documented, m[1])
	}
	for _, c := range Checks {
		kit = append(kit, c.Name)
	}
	if !slices.Equal(documented, kit) {
		t.Errorf("docs/plugins.md lists %v, the kit runs %v", documented, kit)
	}
}

// A protocol error in a call the automatic checks make (here the fetch) is the
// parser's too: the parser verdict is read after every call.
func TestParserCoversTheAutomaticCalls(t *testing.T) {
	src := `
def spec(r):
    return {"title": "Wiki", "description": "d", "shapes": ["feed"], "verbs": ["spec", "check", "list", "fetch"], "settings": {}, "env": []}
def check(r):
    return {"ok": True}
def lst(r):
    return {"entries": [{"key": "a", "title": "A", "revision": "1", "change": "added"}], "next_cursor": "c1"}
def f(r):
    return {"contents": {}}
recipe("wiki", lst, spec = spec, check = check, fetch = f)
`
	res := Run(context.Background(), starInput(t, src, "feed", scenario()))
	if status(res, "parser") != Fail || status(res, "fetch") != Fail {
		t.Errorf("%+v", res)
	}
	// And the order of the report is the order of Checks, less the connector
	// check, which only RunConnector reports.
	var got []string
	for _, r := range res {
		if !strings.HasPrefix(r.Check, "step") {
			got = append(got, r.Check)
		}
	}
	var want []string
	for _, c := range Checks {
		if c.Name != "connector" {
			want = append(want, c.Name)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("report order %v, want %v", got, want)
	}
}

// The absolute tests/ folder a {tests} expansion gives is a path of the author's
// own folder, not a credential: a recipe that reads fixtures through it passes
// the secrets check, while a real test credential is still watched.
func TestSecretsCheckIgnoresTheTestsPath(t *testing.T) {
	dir := t.TempDir()
	doc := "env:\n  TOKEN: tok-secret-value\n  ROOT: \"{tests}/fixtures\"\nsteps:\n  - verb: list\n"
	if err := os.WriteFile(filepath.Join(dir, "wiki.yaml"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	sc, err := ReadFile(dir, "wiki")
	if err != nil {
		t.Fatal(err)
	}
	src := "def lst(r):\n    return {\"entries\": [{\"key\": \"a\", \"title\": env.get(\"ROOT\"), \"revision\": \"1\", \"change\": \"added\"}], \"next_cursor\": \"c\"}\nrecipe(\"wiki\", lst)\n"
	in := starInput(t, src, "feed", sc)
	in.Star.Env["ROOT"] = "CONFORMANCE_TEST_ROOT"
	in.Permissions.Env = append(in.Permissions.Env, "ROOT")
	if got := status(Run(context.Background(), in), "secrets"); got != Pass {
		t.Errorf("secrets: %s", got)
	}
}
