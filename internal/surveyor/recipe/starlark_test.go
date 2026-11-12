package recipe

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/plugin/starcore/recipes"
)

func starRecipe(t *testing.T, src, shape string, env map[string]string) Recipe {
	t.Helper()
	p, err := recipes.Load([]core.TreeFile{{Path: core.SurveyorScript, Content: []byte(src)}})
	if err != nil || p == nil {
		t.Fatalf("load: %v", err)
	}
	return NewStarlark(StarConfig{
		ExecConfig: ExecConfig{Name: "acme/wiki", Shape: shape, Env: env, Timeout: time.Minute, MaxOutput: 1 << 20, MaxContent: 1 << 10, Instructions: "x"},
		Program:    p, Local: "wiki", Plugin: "acme", Permissions: &core.Permissions{Env: []string{"TOKEN"}},
	})
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var re *Error
	if !errors.As(err, &re) || re.Code != code {
		t.Fatalf("err = %v, want code %s", err, code)
	}
}

const okEntry = `{"key": "k1", "title": "T", "revision": "r", "change": "added"}`

// A Starlark listing goes through the exec parser: the caps, key rules and
// codes are the exec ones.
func TestStarlarkListingIsJudgedByTheExecParser(t *testing.T) {
	good := starRecipe(t, "def l(r):\n    return {\"entries\": ["+okEntry+"], \"deletes\": [\"gone\"], \"next_cursor\": \"c\"}\nrecipe(\"wiki\", l)\n", "feed", nil)
	l, err := good.List(context.Background(), Request{MaxContentBytes: 100})
	if err != nil || len(l.Entries) != 2 || l.NextCursor != "c" || l.Entries[1].Change != Removed {
		t.Fatalf("%+v %v", l, err)
	}
	for name, tc := range map[string]struct{ ret, code string }{
		"too many entries": {`{"entries": [` + okEntry + `, ` + strings.ReplaceAll(okEntry, "k1", "k2") + `], "next_cursor": "c"}`, CodeProtocolError},
		"duplicate key":    {`{"entries": [` + okEntry + `, ` + okEntry + `], "next_cursor": "c"}`, CodeProtocolError},
		"long cursor":      {`{"next_cursor": "x" * 2000}`, CodeProtocolError},
		"bad change":       {`{"entries": [{"key": "k", "title": "T", "revision": "r", "change": "removed"}]}`, CodeProtocolError},
		"shape error":      {`{"bogus": 1}`, CodeListFailed},
	} {
		t.Run(name, func(t *testing.T) {
			r := starRecipe(t, "def l(r):\n    return "+tc.ret+"\nrecipe(\"wiki\", l)\n", "feed", nil)
			_, err := r.List(context.Background(), Request{MaxEntries: 1})
			wantCode(t, err, tc.code)
		})
	}
}

func TestStarlarkFailureIsListFailedAndRedacted(t *testing.T) {
	t.Setenv("OPERATOR_TOKEN", "s3cr3t-value")
	r := starRecipe(t, "def l(r):\n    fail(\"boom s3cr3t-value\")\nrecipe(\"wiki\", l)\n", "feed", map[string]string{"TOKEN": "OPERATOR_TOKEN"})
	_, err := r.List(context.Background(), Request{})
	wantCode(t, err, CodeListFailed)
	if strings.Contains(err.Error(), "s3cr3t-value") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("the detail is redacted of mapped values: %v", err)
	}
}

func TestStarlarkEnvIsTheMapping(t *testing.T) {
	t.Setenv("OPERATOR_TOKEN", "mapped-value")
	t.Setenv("TOKEN", "unmapped-value")
	r := starRecipe(t, "def l(r):\n    return {\"next_cursor\": env.get(\"TOKEN\")}\nrecipe(\"wiki\", l)\n", "feed", map[string]string{"TOKEN": "OPERATOR_TOKEN"})
	l, err := r.List(context.Background(), Request{})
	if err != nil || l.NextCursor != "mapped-value" {
		t.Fatalf("%+v %v", l, err)
	}
}

func TestStarlarkVerbsNotDeclaredAreUnsupported(t *testing.T) {
	r := starRecipe(t, "def l(r):\n    return {}\nrecipe(\"wiki\", l)\n", "feed", nil)
	if _, err := r.(Specer).Spec(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Errorf("spec: %v", err)
	}
	if _, err := r.(Checker).Check(context.Background(), Request{}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("check: %v", err)
	}
	if _, err := r.(Fetcher).Fetch(context.Background(), Request{}, []string{"k"}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("fetch: %v", err)
	}
}

func TestStarlarkSystemCheckProvesReadOnly(t *testing.T) {
	src := func(ro string) string {
		return "def l(r):\n    return {}\ndef c(r):\n    return {\"ok\": True" + ro + ", \"identity\": \"reader\"}\nrecipe(\"wiki\", l, shape = \"system\", check = c)\n"
	}
	r := starRecipe(t, src(`, "read_only": False`), "system", nil)
	_, err := r.(Checker).Check(context.Background(), Request{})
	wantCode(t, err, CodeIdentityNotReadOnly)
	r = starRecipe(t, src(`, "read_only": True`), "system", nil)
	if v, err := r.(Checker).Check(context.Background(), Request{}); err != nil {
		t.Fatalf("%v", err)
	} else if _, err := r.(SystemRecipe).ListSystem(context.Background(), v, Request{}); err != nil {
		t.Fatalf("a clean check lists: %v", err)
	}
}

func TestStarlarkFetch(t *testing.T) {
	r := starRecipe(t, "def l(r):\n    return {}\ndef f(r):\n    return {\"contents\": {k: \"text \" + k for k in r[\"keys\"]}}\nrecipe(\"wiki\", l, fetch = f)\n", "feed", nil)
	got, err := r.(Fetcher).Fetch(context.Background(), Request{}, []string{"a", "b"})
	if err != nil || got["a"] != "text a" || got["b"] != "text b" {
		t.Fatalf("%v %v", got, err)
	}
	// A key left unanswered is the exec protocol_error.
	r = starRecipe(t, "def l(r):\n    return {}\ndef f(r):\n    return {\"contents\": {}}\nrecipe(\"wiki\", l, fetch = f)\n", "feed", nil)
	_, err = r.(Fetcher).Fetch(context.Background(), Request{}, []string{"a"})
	wantCode(t, err, CodeProtocolError)
}

// A mapped variable that is unset or empty fails the verb naming the variable,
// as it does for an exec recipe (connector.EnvValue).
func TestStarlarkMappedVariableMustBeSetAndNonEmpty(t *testing.T) {
	src := "def l(r):\n    return {\"next_cursor\": \"x\"}\nrecipe(\"wiki\", l)\n"
	for name, set := range map[string]func(){
		"empty": func() { t.Setenv("OPERATOR_TOKEN", "") },
		"unset": func() { os.Unsetenv("OPERATOR_TOKEN") },
	} {
		t.Run(name, func(t *testing.T) {
			set()
			r := starRecipe(t, src, "feed", map[string]string{"TOKEN": "OPERATOR_TOKEN"})
			_, err := r.List(context.Background(), Request{})
			wantCode(t, err, CodeListFailed)
			if !strings.Contains(err.Error(), "OPERATOR_TOKEN is unset or empty") {
				t.Errorf("%v", err)
			}
		})
	}
}
