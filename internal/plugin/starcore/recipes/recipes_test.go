package recipes_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/plugin/starcore"
	"github.com/BeppeTemp/cartographer/internal/plugin/starcore/recipes"
	"github.com/BeppeTemp/cartographer/internal/plugin/starcore/startest"
)

func tree(src string) []core.TreeFile {
	return []core.TreeFile{{Path: core.SurveyorScript, Content: []byte(src)}}
}

func load(t *testing.T, src string) *recipes.Program {
	t.Helper()
	p, err := recipes.Load(tree(src))
	if err != nil || p == nil {
		t.Fatalf("load: %v", err)
	}
	return p
}

func call(p *recipes.Program, verb, body string) ([]byte, error) {
	return p.Call(context.Background(), recipes.Request{Recipe: "wiki", Verb: verb, Body: []byte(body), Plugin: "acme", Version: "1.0.0"})
}

const full = `
def spec(req):
    return {"title": "Wiki", "verbs": ["spec", "check", "list", "fetch"]}

def check(req):
    return {"ok": True, "identity": "reader"}

def lst(req):
    return {
        "entries": [{"key": "p1", "title": "One", "revision": "r1", "change": "added"}],
        "deletes": ["gone"],
        "next_cursor": "c2",
    }

def fetch(req):
    return {"contents": {"b": "text b", "a": "text a"}}

recipe("wiki", lst, spec = spec, check = check, fetch = fetch)
`

func TestVerbsAnswerTheExecWireLines(t *testing.T) {
	p := load(t, full)
	for verb, want := range map[string]string{
		"spec":  `{"spec":{"title":"Wiki","verbs":["spec","check","list","fetch"]}}` + "\n",
		"check": `{"check":{"identity":"reader","ok":true}}` + "\n",
		"list": `{"entry":{"change":"added","key":"p1","revision":"r1","title":"One"}}` + "\n" +
			`{"delete":{"key":"gone"}}` + "\n" + `{"next_cursor":"c2"}` + "\n",
		"fetch": `{"content":{"content":"text a","key":"a"}}` + "\n" + `{"content":{"content":"text b","key":"b"}}` + "\n" + `{"done":true}` + "\n",
	} {
		got, err := call(p, verb, `{"protocol":2,"verb":"`+verb+`","config":{}}`)
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", verb, got, err, want)
		}
	}
	rs := p.Recipes()
	if len(rs) != 1 || rs[0].Name != "wiki" || rs[0].Shape != "feed" || strings.Join(rs[0].Verbs, ",") != "spec,check,list,fetch" || !strings.HasPrefix(rs[0].Pos, "surveyor.star:") {
		t.Errorf("%+v", rs)
	}
}

func TestAVerbNotGivenIsNotDeclared(t *testing.T) {
	p := load(t, "def lst(req):\n    return {}\nrecipe(\"wiki\", lst)\n")
	for _, verb := range []string{"spec", "check", "fetch"} {
		if _, err := call(p, verb, `{}`); !errors.Is(err, recipes.ErrNotDeclared) {
			t.Errorf("%s: %v", verb, err)
		}
	}
	// A list that returns nothing is an empty listing with an empty cursor.
	if got, err := call(p, "list", `{}`); err != nil || string(got) != `{"next_cursor":""}`+"\n" {
		t.Errorf("%q %v", got, err)
	}
}

func TestTheScriptIsHandedTheRequestWithoutProtocolAndVerb(t *testing.T) {
	p := load(t, `
def lst(req):
    if "protocol" in req or "verb" in req:
        fail("protocol and verb must be dropped")
    return {"next_cursor": req["cursor"] + ":" + str(req["max_entries"]) + ":" + req["source"]["locator"]}
recipe("wiki", lst)
`)
	got, err := call(p, "list", `{"protocol":2,"verb":"list","cursor":"c1","max_entries":200,"source":{"locator":"wiki:ops"}}`)
	if err != nil || string(got) != `{"next_cursor":"c1:200:wiki:ops"}`+"\n" {
		t.Errorf("%q %v", got, err)
	}
}

func TestRegistrarRefusals(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"bad name":        {`recipe("Wiki", lst)`, "must match"},
		"slash":           {`recipe("a/b", lst)`, "must match"},
		"bad shape":       {`recipe("wiki", lst, shape = "lake")`, "feed or system"},
		"system no check": {`recipe("wiki", lst, shape = "system")`, "needs check"},
		"system fetch":    {`recipe("wiki", lst, shape = "system", check = lst, fetch = lst)`, "no fetch"},
		"not a function":  {`recipe("wiki", "lst")`, "must be a function"},
		"twice":           {"recipe(\"wiki\", lst)\nrecipe(\"wiki\", lst)", "registered twice"},
		"files absent":    {`files.read("x")`, "undefined"},
		"kb absent":       {`kb.call("x")`, "undefined"},
		"command absent":  {`command("x", lst, "h")`, "undefined"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := recipes.Load(tree("def lst(r):\n    return {}\n" + tc.src + "\n"))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestResultShapeIsChecked(t *testing.T) {
	for name, tc := range map[string]struct{ ret, want string }{
		"not a dict":    {`["x"]`, "returns a dict"},
		"unknown key":   {`{"entries": [], "extra": 1}`, `unknown key "extra"`},
		"entries type":  {`{"entries": "x"}`, "entries has the wrong type"},
		"deletes type":  {`{"deletes": [1]}`, "deletes has the wrong type"},
		"cursor type":   {`{"next_cursor": 5}`, "next_cursor has the wrong type"},
		"not JSON-able": {`{"entries": [lambda: 1]}`, "not JSON-able"},
	} {
		t.Run(name, func(t *testing.T) {
			p := load(t, "def lst(r):\n    return "+tc.ret+"\nrecipe(\"wiki\", lst)\n")
			_, err := call(p, "list", `{}`)
			var ce *starcore.CallError
			if !errors.As(err, &ce) || ce.Kind != starcore.Failed || !strings.Contains(ce.Msg, tc.want) {
				t.Fatalf("err = %v, want Failed %q", err, tc.want)
			}
		})
	}
}

func TestEnvReadsOnlyWhatTheHostMapsAndTheManifestDeclares(t *testing.T) {
	p := load(t, `
def lst(r):
    return {"next_cursor": env.get("TOKEN")}
recipe("wiki", lst)
`)
	req := recipes.Request{Recipe: "wiki", Verb: "list", Body: []byte(`{}`), Plugin: "acme",
		Permissions: &core.Permissions{Env: []string{"TOKEN"}},
		LookupEnv:   func(n string) (string, bool) { return "secret-for-" + n, n == "TOKEN" }}
	if got, err := p.Call(context.Background(), req); err != nil || !strings.Contains(string(got), "secret-for-TOKEN") {
		t.Errorf("%q %v", got, err)
	}
	req.Permissions = &core.Permissions{}
	_, err := p.Call(context.Background(), req)
	var ce *starcore.CallError
	if !errors.As(err, &ce) || ce.Kind != starcore.Denied || !strings.Contains(ce.Msg, "permissions.env") {
		t.Errorf("an undeclared name is denied: %v", err)
	}
}

func TestHTTPThroughARecordedTransport(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer t0k" {
			w.WriteHeader(401)
			return
		}
		fmt.Fprint(w, `{"pages": ["a", "b"]}`)
	}))
	defer srv.Close()
	p := load(t, fmt.Sprintf(`
def lst(r):
    resp = http.request("GET", %q, headers = {"Authorization": "Bearer " + env.get("TOKEN")})
    pages = json.decode(resp["body"])["pages"]
    return {"entries": [{"key": k, "title": k, "revision": "1", "change": "added", "content": "body of " + k} for k in pages], "next_cursor": "done"}
recipe("wiki", lst)
`, srv.URL))
	req := recipes.Request{Recipe: "wiki", Verb: "list", Body: []byte(`{}`), Plugin: "acme",
		Permissions: &core.Permissions{Net: []string{"127.0.0.1"}, Env: []string{"TOKEN"}},
		LookupEnv:   func(string) (string, bool) { return "t0k", true },
		Seams:       &startest.Seams{Net: &startest.NetSeams{TLS: srv.Client().Transport.(*http.Transport).TLSClientConfig}}}
	got, err := p.Call(context.Background(), req)
	if err != nil || strings.Count(string(got), `{"entry":`) != 2 || !strings.Contains(string(got), `"content":"body of b"`) {
		t.Fatalf("%q %v", got, err)
	}
	// Without a net grant the same call is denied, not attempted.
	req.Permissions = &core.Permissions{Env: []string{"TOKEN"}}
	if _, err := p.Call(context.Background(), req); err == nil || !strings.Contains(err.Error(), "permissions.net") {
		t.Errorf("no net grant: %v", err)
	}
}

func TestCheckJudgesTheRegistrationsAgainstTheFolders(t *testing.T) {
	script := core.TreeFile{Path: core.SurveyorScript, Content: []byte("def lst(r):\n    return {}\nrecipe(\"wiki\", lst)\n")}
	instr := core.TreeFile{Path: "recipes/wiki/instructions.md", Content: []byte("x")}
	yml := core.TreeFile{Path: "recipes/wiki/recipe.yaml", Content: []byte("command: run.sh\n")}
	orphan := core.TreeFile{Path: "recipes/other/instructions.md", Content: []byte("x")}
	for name, tc := range map[string]struct {
		files []core.TreeFile
		want  string // "" = no problem
	}{
		"fine":             {[]core.TreeFile{script, instr}, ""},
		"no instructions":  {[]core.TreeFile{script}, "needs recipes/wiki/instructions.md"},
		"both kinds":       {[]core.TreeFile{script, instr, yml}, "exec or Starlark, not both"},
		"orphan folder":    {[]core.TreeFile{script, instr, orphan}, "registers no recipe other"},
		"orphan no script": {[]core.TreeFile{orphan}, "registers no recipe other"},
		"exec only":        {[]core.TreeFile{{Path: "recipes/other/recipe.yaml", Content: []byte("x")}, orphan}, ""},
		"script broken":    {[]core.TreeFile{{Path: core.SurveyorScript, Content: []byte("recipe(")}}, "surveyor.star"},
	} {
		t.Run(name, func(t *testing.T) {
			probs := strings.Join(recipes.Check(core.SideSurveyor, tc.files), "\n")
			if (tc.want == "") != (probs == "") || !strings.Contains(probs, tc.want) {
				t.Fatalf("problems %q, want %q", probs, tc.want)
			}
		})
	}
	if recipes.Check(core.SideClient, []core.TreeFile{script}) != nil {
		t.Error("another side is not judged here")
	}
}
