package recipetest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/plugin/starcore"
	"github.com/BeppeTemp/cartographer/internal/plugin/starcore/recipes"
	"github.com/BeppeTemp/cartographer/internal/surveyor/connector"
	"github.com/BeppeTemp/cartographer/internal/surveyor/recipe"
)

// Result statuses.
const (
	Pass = "pass"
	Fail = "fail"
	Skip = "skip"
)

// Result is one line of the report: one check of one target.
type Result struct {
	Target string `json:"target"`
	Check  string `json:"check"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Input is one recipe to test: exactly one of Exec and Star, built by the
// Surveyor's own plugin loader (surveyor.PluginPoints), so the code run here is
// the code a Surveyor would run.
type Input struct {
	Name        string // the local name, the one tests/<name>.yaml is named after
	Exec        *recipe.ExecConfig
	Star        *recipe.StarConfig
	Permissions *core.Permissions
	Declared    map[string]core.Setting
	Scenario    *Scenario
}

type run struct {
	in     Input
	env    map[string]string // the mapped variables, which a report line redacts
	r      recipe.Recipe
	replay *starcore.Replay
	stage  string
	out    []Result
	spec   *recipe.Spec
	lists  []listed
	next   string
	proto  []string
	seen   [][2]string // where, text
}

type outcome struct {
	err     error
	entries []recipe.Entry
	deletes []string
	next    string
	content map[string]string
}

type listed struct {
	cursor, stage string
	fromNext      bool
	o             outcome
}

// Run runs the scenario's steps against one recipe and judges each against its
// expect. The environment variables the recipe maps are set to the scenario's
// test values for the duration and restored after.
func Run(ctx context.Context, in Input) []Result {
	x := &run{in: in, replay: &starcore.Replay{}}
	env := map[string]string{}
	if in.Exec != nil {
		env = in.Exec.Env
		x.r = recipe.NewExec(*in.Exec)
	} else {
		sc := *in.Star
		sc.Tune = func(r *recipes.Request) { r.Replay = x.replay }
		env = sc.Env
		x.r = recipe.NewStarlark(sc)
	}
	x.env = env
	defer x.setEnv(env)()
	x.steps(ctx)
	return x.out
}

// setEnv gives each mapped variable the scenario's value for the name the
// recipe sees it as (a placeholder when the scenario leaves it out).
func (x *run) setEnv(mapped map[string]string) func() {
	var undo []func()
	for name, v := range mapped {
		val, ok := x.in.Scenario.Env[name]
		if !ok {
			val = "unset-test-value-" + name
			x.in.Scenario.Env[name] = val // the secrets check watches it too
		}
		old, had := os.LookupEnv(v)
		_ = os.Setenv(v, val)
		undo = append(undo, func() {
			if had {
				_ = os.Setenv(v, old)
			} else {
				_ = os.Unsetenv(v)
			}
		})
	}
	return func() {
		for _, u := range undo {
			u()
		}
	}
}

func (x *run) add(check string, err error, skip string) {
	r := Result{Target: x.in.Name, Check: check, Status: Pass}
	switch {
	case skip != "":
		r.Status, r.Detail = Skip, skip
	case err != nil:
		// Whatever a failure prints (a cursor, the keys a step got) may hold a
		// mapped variable's value the recipe echoed: redact it like a recipe error.
		r.Status, r.Detail = Fail, connector.RedactEnv(err.Error(), x.env)
	}
	x.out = append(x.out, r)
}

// point sets which recordings the next call sees.
func (x *run) point(stage string) {
	x.stage = stage
	x.replay.Exchanges = nil
	for _, rec := range x.in.Scenario.HTTP {
		if rec.Stage != "" && rec.Stage != stage {
			continue
		}
		x.replay.Exchanges = append(x.replay.Exchanges, starcore.Exchange{
			Method: rec.Method, URL: rec.URL, Query: rec.Query,
			Status: rec.Response.Status, Headers: rec.Response.Headers, Body: rec.Response.Body})
	}
}

func (x *run) note(where string, texts ...string) {
	for _, t := range texts {
		x.seen = append(x.seen, [2]string{where, t})
	}
}

// call runs one verb through the production recipe and keeps what it printed.
func (x *run) call(ctx context.Context, stage, verb, cursor string, keys []string) (o outcome) {
	x.point(stage)
	sc := x.in.Scenario
	req := recipe.Request{ID: sc.Source.ID, Title: sc.Source.Title, Locator: sc.Source.Locator,
		SourceKind: sc.Source.SourceKind, Include: sc.Source.Include, Cursor: cursor}
	var l recipe.Listing
	switch verb {
	case "spec":
		if s, ok := x.r.(recipe.Specer); !ok {
			o.err = recipe.ErrUnsupported
		} else if sp, err := s.Spec(ctx); err != nil {
			o.err = err
		} else {
			x.spec = &sp
			x.note("spec", sp.Title, sp.Description)
		}
	case "check":
		if c, ok := x.r.(recipe.Checker); !ok {
			o.err = recipe.ErrUnsupported
		} else if v, err := c.Check(ctx, req); err != nil {
			o.err = err
		} else {
			x.note("check", v.Identity(), v.Detail())
		}
	case "list":
		if sys, ok := x.r.(recipe.SystemRecipe); ok && x.r.Shape() == recipe.ShapeSystem {
			var v recipe.Verified
			if v, o.err = sys.Check(ctx, req); o.err == nil {
				l, o.err = sys.ListSystem(ctx, v, req)
			}
		} else {
			l, o.err = x.r.List(ctx, req)
		}
		o.next = l.NextCursor
		for _, e := range l.Entries {
			x.note("entry "+e.Key, e.Key, e.Title, e.Revision, e.Locator, e.Content)
			if e.Change == recipe.Removed {
				o.deletes = append(o.deletes, e.Key)
			} else {
				o.entries = append(o.entries, e)
			}
		}
	case "fetch":
		if f, ok := x.r.(recipe.Fetcher); !ok {
			o.err = recipe.ErrUnsupported
		} else if o.content, o.err = f.Fetch(ctx, req, keys); o.err == nil {
			for k, c := range o.content {
				x.note("fetched "+k, c)
			}
		}
	}
	if o.err != nil {
		x.note(verb+" error", o.err.Error())
	}
	return o
}

// code is the failure code of an error: a recipe.Error's, "unsupported" for a
// verb the script does not implement.
func code(err error) string {
	var re *recipe.Error
	switch {
	case errors.As(err, &re):
		return re.Code
	case errors.Is(err, recipe.ErrUnsupported):
		return "unsupported"
	}
	return "error"
}

// steps runs the scenario's own steps and judges each against its expect.
func (x *run) steps(ctx context.Context) {
	for i, s := range x.in.Scenario.Steps {
		cursor := s.Request.Cursor
		if cursor == "@next" {
			cursor = x.next
		}
		o := x.call(ctx, s.Stage, s.Verb, cursor, s.Request.Keys)
		if o.err != nil && code(o.err) == recipe.CodeProtocolError && s.Expect.Error != recipe.CodeProtocolError {
			x.proto = append(x.proto, fmt.Sprintf("step %d %s: %v", i+1, s.Verb, o.err))
		}
		if s.Verb == "list" && o.err == nil {
			x.lists = append(x.lists, listed{cursor, s.Stage, s.Request.Cursor == "@next", o})
			x.next = o.next
		}
		x.add(fmt.Sprintf("step %d %s", i+1, s.Verb), s.Expect.judge(o), "")
	}
}

// judge compares an outcome with what a step expects.
func (e Expect) judge(o outcome) error {
	want := e.Error
	if e.OK != nil && !*e.OK && want == "" {
		want = recipe.CodeCheckFailed
	}
	if want != "" {
		if got := code(o.err); o.err == nil || got != want {
			return fmt.Errorf("expected the error %s, got %s", want, describe(o.err))
		}
		return nil
	}
	if o.err != nil {
		return fmt.Errorf("failed: %v", o.err)
	}
	var keys []string
	var text []string
	for _, en := range o.entries {
		keys, text = append(keys, en.Key), append(text, en.Content)
	}
	for _, c := range o.content {
		text = append(text, c)
	}
	if err := sameKeys("entries", e.Entries, keys); err != nil {
		return err
	}
	if err := sameKeys("deletes", e.Deletes, o.deletes); err != nil {
		return err
	}
	switch e.NextCursor {
	case "":
	case "empty", "nonempty":
		if (o.next == "") != (e.NextCursor == "empty") {
			return fmt.Errorf("next_cursor is %q, expected it %s", o.next, e.NextCursor)
		}
	default:
		if o.next != e.NextCursor {
			return fmt.Errorf("next_cursor is %q, expected %q", o.next, e.NextCursor)
		}
	}
	if e.ContentContains != "" && !slices.ContainsFunc(text, func(t string) bool { return strings.Contains(t, e.ContentContains) }) {
		return fmt.Errorf("no content contains %q", e.ContentContains)
	}
	return nil
}

func describe(err error) string {
	if err == nil {
		return "success"
	}
	return err.Error()
}

// sameKeys holds got to a count or to the exact set of keys; nil means no assertion.
func sameKeys(what string, want any, got []string) error {
	switch w := want.(type) {
	case nil:
		return nil
	case int:
		if len(got) != w {
			return fmt.Errorf("%s: %d, expected %d", what, len(got), w)
		}
		return nil
	case []any:
		var ws []string
		for _, k := range w {
			ws = append(ws, fmt.Sprint(k))
		}
		a, b := slices.Clone(ws), slices.Clone(got)
		sort.Strings(a)
		sort.Strings(b)
		if !slices.Equal(a, b) {
			return fmt.Errorf("%s: %v, expected %v", what, got, ws)
		}
		return nil
	}
	return fmt.Errorf("%s is a count or a list of keys", what)
}
