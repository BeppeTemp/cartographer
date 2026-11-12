package recipetest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/plugin/starcore/recipes"
	"github.com/BeppeTemp/cartographer/internal/surveyor/recipe"
)

func scenario(steps ...Step) *Scenario {
	if len(steps) == 0 {
		steps = []Step{{Verb: "list"}}
	}
	return &Scenario{Env: map[string]string{}, Steps: steps}
}

func starInput(t *testing.T, src, shape string, sc *Scenario) Input {
	t.Helper()
	p, err := recipes.Load([]core.TreeFile{{Path: core.SurveyorScript, Content: []byte(src)}})
	if err != nil || p == nil {
		t.Fatalf("load: %v", err)
	}
	perms := &core.Permissions{Env: []string{"TOKEN"}, Net: []string{"wiki.example.com"}}
	return Input{Name: "wiki", Scenario: sc, Permissions: perms,
		Star: &recipe.StarConfig{
			ExecConfig: recipe.ExecConfig{Name: "acme/wiki", Shape: shape, Env: map[string]string{"TOKEN": "CONFORMANCE_TEST_TOKEN"}, Timeout: time.Minute, MaxOutput: 1 << 20, MaxContent: 1 << 10, Instructions: "x"},
			Program:    p, Local: "wiki", Plugin: "acme", Permissions: perms}}
}

// A Starlark recipe's http.request is answered from the recordings of the
// stage the step names, and a request nobody recorded fails the step naming it.
func TestRecordingsAreStaged(t *testing.T) {
	src := `
def lst(r):
    resp = http.request("GET", "https://wiki.example.com/pages")
    return {"entries": [{"key": k, "title": k, "revision": "1", "change": "added"} for k in json.decode(resp["body"])], "next_cursor": "c"}
recipe("wiki", lst)
`
	rec := func(stage, body string) Recording {
		r := Recording{Stage: stage, Method: "GET", URL: "https://wiki.example.com/pages"}
		r.Response.Body = body
		return r
	}
	sc := scenario(Step{Verb: "list", Stage: "one", Expect: Expect{Entries: []any{"a"}}}, Step{Verb: "list", Stage: "two", Expect: Expect{Entries: []any{"a", "b"}}})
	sc.HTTP = []Recording{rec("one", `["a"]`), rec("two", `["a", "b"]`)}
	for _, r := range Run(context.Background(), starInput(t, src, "feed", sc)) {
		if r.Status == Fail && strings.HasPrefix(r.Check, "step") {
			t.Errorf("%+v", r)
		}
	}
	sc.HTTP = nil
	res := Run(context.Background(), starInput(t, src, "feed", sc))
	if got := res[0]; got.Status != Fail || !strings.Contains(got.Detail, "no recorded response for GET https://wiki.example.com/pages") {
		t.Errorf("%+v", got)
	}
}

func TestExpectJudgesWhatAStepAsserts(t *testing.T) {
	o := outcome{entries: []recipe.Entry{{Key: "a", Content: "hello world"}, {Key: "b"}}, deletes: []string{"c"}, next: "n1"}
	for name, tc := range map[string]struct {
		e  Expect
		ok bool
	}{
		"count":     {Expect{Entries: 2}, true},
		"wrong":     {Expect{Entries: 3}, false},
		"keys":      {Expect{Entries: []any{"b", "a"}}, true},
		"deletes":   {Expect{Deletes: []any{"c"}}, true},
		"literal":   {Expect{NextCursor: "n1"}, true},
		"empty":     {Expect{NextCursor: "empty"}, false},
		"content":   {Expect{ContentContains: "world"}, true},
		"no match":  {Expect{ContentContains: "mars"}, false},
		"an error":  {Expect{Error: "list_failed"}, false},
		"ok: false": {Expect{OK: new(false)}, false},
	} {
		if err := tc.e.judge(o); (err == nil) != tc.ok {
			t.Errorf("%s: %v", name, err)
		}
	}
	failed := outcome{err: &recipe.Error{Code: "check_failed", Detail: "no"}}
	if err := (Expect{OK: new(false)}).judge(failed); err != nil {
		t.Errorf("ok: false expects check_failed: %v", err)
	}
}

// A failed step prints the cursor or the keys it got; a recipe that echoed the
// token there must not show it in the report.
func TestReportDetailRedactsTheMappedVariables(t *testing.T) {
	src := `
def lst(r):
    return {"entries": [{"key": "k-" + env.get("TOKEN"), "title": "t", "revision": "1", "change": "added"}], "next_cursor": "c-" + env.get("TOKEN")}
recipe("wiki", lst)
`
	sc := scenario(Step{Verb: "list", Expect: Expect{NextCursor: "other", Entries: []any{"x"}}})
	sc.Env["TOKEN"] = "s3cr3t-token-value"
	res := Run(context.Background(), starInput(t, src, "feed", sc))
	if res[0].Status != Fail || strings.Contains(res[0].Detail, "s3cr3t-token-value") {
		t.Errorf("%+v", res[0])
	}
	sc = scenario(Step{Verb: "list", Expect: Expect{Entries: []any{"x"}}})
	sc.Env["TOKEN"] = "s3cr3t-token-value"
	res = Run(context.Background(), starInput(t, src, "feed", sc))
	if res[0].Status != Fail || strings.Contains(res[0].Detail, "s3cr3t-token-value") || !strings.Contains(res[0].Detail, "***") {
		t.Errorf("%+v", res[0])
	}
}
