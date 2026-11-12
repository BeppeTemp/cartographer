package recipes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strings"

	"go.starlark.net/starlark"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/plugin/starcore"
	"github.com/BeppeTemp/cartographer/internal/plugin/starcore/internal/seam"
)

// The verbs of a recipe, as registration kinds. list is the one every recipe
// has; its registration carries the shape in Help.
const (
	KindList  = "recipe.list"
	KindSpec  = "recipe.spec"
	KindCheck = "recipe.check"
	KindFetch = "recipe.fetch"
)

const (
	shapeFeed   = "feed"
	shapeSystem = "system"
)

// nameRe is the local name of a recipe, the grammar of an operator-local name
// (internal/surveyor/recipe.ValidLocal).
var nameRe = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

// ErrNotDeclared is what Call answers for a verb the script did not register:
// the exec "exit 64" of the Starlark side.
var ErrNotDeclared = errors.New("recipes: the recipe does not declare this verb")

// Program is a loaded surveyor.star.
type Program struct{ *starcore.Script }

// Recipe is what a script registered under one name.
type Recipe struct {
	Name  string
	Shape string
	Verbs []string // list, then the others it gave, in a fixed order
	Pos   string   // where recipe() was called, "surveyor.star:line"
}

func options() starcore.Options {
	return starcore.Options{
		Entry:      core.SurveyorScript,
		Registrars: starlark.StringDict{"recipe": starlark.NewBuiltin("recipe", register)},
	}
}

// Load runs surveyor.star once and reports what it registered; (nil, nil) when
// the tree has none. It reads the files it is given and nothing else.
func Load(files []core.TreeFile) (*Program, error) {
	var own []core.TreeFile
	for _, f := range files {
		if f.Path == core.SurveyorScript {
			own = append(own, f)
		}
	}
	s, err := starcore.LoadScript(own, options())
	if s == nil || err != nil {
		return nil, err
	}
	return &Program{s}, nil
}

// Check is core.Options.CheckScripts for the surveyor side: the script must
// load, every recipe it registers needs recipes/<name>/instructions.md and no
// recipe.yaml (a recipe is exec or Starlark, never both), and a folder without
// recipe.yaml needs a registration.
func Check(side core.Side, files []core.TreeFile) []string {
	if side != core.SideSurveyor {
		return nil
	}
	var problems []string
	prog, err := Load(files)
	if err != nil {
		return []string{err.Error()}
	}
	registered := map[string]Recipe{}
	if prog != nil {
		for _, r := range prog.Recipes() {
			registered[r.Name] = r
		}
	}
	yml, instr := map[string]bool{}, map[string]bool{}
	for _, f := range files {
		name, rest, ok := strings.Cut(strings.TrimPrefix(f.Path, "recipes/"), "/")
		if !ok || !strings.HasPrefix(f.Path, "recipes/") {
			continue
		}
		switch rest {
		case "recipe.yaml":
			yml[name] = true
		case "instructions.md":
			instr[name] = true
		}
	}
	for _, name := range slices.Sorted(mapKeys(registered)) {
		r := registered[name]
		switch {
		case yml[name]:
			problems = append(problems, fmt.Sprintf("%s: recipe %s is registered here and has recipes/%s/recipe.yaml: a recipe is exec or Starlark, not both", r.Pos, name, name))
		case !instr[name]:
			problems = append(problems, fmt.Sprintf("%s: recipe %s needs recipes/%s/instructions.md", r.Pos, name, name))
		}
	}
	for _, name := range slices.Sorted(mapKeys(instr)) {
		if _, ok := registered[name]; !ok && !yml[name] {
			problems = append(problems, fmt.Sprintf("recipes/%s/: no recipe.yaml, and surveyor.star registers no recipe %s", name, name))
		}
	}
	return problems
}

func mapKeys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// Recipes lists what the script registered, by name.
func (p *Program) Recipes() []Recipe {
	by := map[string]*Recipe{}
	for _, r := range p.Registrations {
		e := by[r.Name]
		if e == nil {
			e = &Recipe{Name: r.Name}
			by[r.Name] = e
		}
		if r.Kind == KindList {
			e.Shape, e.Pos = r.Help, r.Pos
		}
		e.Verbs = append(e.Verbs, strings.TrimPrefix(r.Kind, "recipe."))
	}
	out := make([]Recipe, 0, len(by))
	for _, e := range by {
		order := []string{"spec", "check", "list", "fetch"}
		slices.SortFunc(e.Verbs, func(a, b string) int { return slices.Index(order, a) - slices.Index(order, b) })
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// register is recipe(name, list, shape="feed", spec=None, check=None, fetch=None).
func register(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	reg, pos, err := starcore.Registering(thread, "recipe")
	if err != nil {
		return nil, err
	}
	var name, shape string = "", shapeFeed
	var list starlark.Value
	var spec, check, fetch starlark.Value = starlark.None, starlark.None, starlark.None
	if err := starlark.UnpackArgs("recipe", args, kwargs, "name", &name, "list", &list, "shape?", &shape, "spec?", &spec, "check?", &check, "fetch?", &fetch); err != nil {
		return nil, err
	}
	if !nameRe.MatchString(name) {
		return nil, fmt.Errorf("recipe: name %q must match %s (the local name: the plugin's name is added)", name, nameRe)
	}
	if shape != shapeFeed && shape != shapeSystem {
		return nil, fmt.Errorf("recipe %s: shape is feed or system, not %q", name, shape)
	}
	if shape == shapeSystem && check == starlark.None {
		return nil, fmt.Errorf("recipe %s: a system recipe needs check, which proves its identity read-only", name)
	}
	if shape == shapeSystem && fetch != starlark.None {
		return nil, fmt.Errorf("recipe %s: a system recipe has no fetch", name)
	}
	add := func(kind string, v starlark.Value, required bool) error {
		if v == starlark.None && !required {
			return nil
		}
		fn, err := starcore.ImplFunc("recipe "+name+" "+strings.TrimPrefix(kind, "recipe."), v)
		if err != nil {
			return err
		}
		r := starcore.Registration{Kind: kind, Name: name, Pos: pos, Impl: fn}
		if kind == KindList {
			r.Help = shape
		}
		return reg.Add(r, kind+" "+name)
	}
	for _, v := range []struct {
		kind string
		val  starlark.Value
		req  bool
	}{{KindList, list, true}, {KindSpec, spec, false}, {KindCheck, check, false}, {KindFetch, fetch, false}} {
		if err := add(v.kind, v.val, v.req); err != nil {
			return nil, err
		}
	}
	return starlark.None, nil
}

// Request is one verb call. Body is what an exec recipe gets on stdin; the
// script is handed it as a dict without protocol and verb.
type Request struct {
	Recipe, Verb string
	Body         json.RawMessage

	Plugin, Version string
	// Permissions are the surveyor side's of the manifest; Declared its settings
	// and Values the Surveyor config's.
	Permissions *core.Permissions
	Declared    map[string]core.Setting
	Values      map[string]any
	// LookupEnv answers env.get from the mapping of the Surveyor config.
	LookupEnv func(string) (string, bool)
	Log       io.Writer

	// Seams are the test seams of the runtime; only startest can build one.
	Seams *seam.Seams
}

// Call runs a verb and answers the JSON lines the same verb of an exec recipe
// prints. The error is a *starcore.CallError, or ErrNotDeclared.
func (p *Program) Call(ctx context.Context, req Request) ([]byte, error) {
	kind := "recipe." + req.Verb
	if !slices.ContainsFunc(p.Registrations, func(r starcore.Registration) bool { return r.Kind == kind && r.Name == req.Recipe }) {
		return nil, ErrNotDeclared
	}
	v, err := p.Invoke(ctx, starcore.CallSpec{
		Kind: kind, Name: req.Recipe, Plugin: req.Plugin, Version: req.Version,
		Perms: req.Permissions, Declared: req.Declared, Values: req.Values,
		LookupEnv: req.LookupEnv, Log: req.Log,
		Timeout: starcore.RecipeTimeout, Steps: starcore.MaxCallStepsRecipe,
		Arg: func(*starcore.Registration) (starlark.Tuple, error) {
			dec := json.NewDecoder(bytes.NewReader(req.Body))
			dec.UseNumber()
			var m map[string]any
			if err := dec.Decode(&m); err != nil {
				return nil, &starcore.CallError{Kind: starcore.Internal, Msg: "the request is not a JSON object"}
			}
			delete(m, "protocol")
			delete(m, "verb")
			a, err := starcore.ToStarlark(m)
			if err != nil {
				return nil, &starcore.CallError{Kind: starcore.Internal, Msg: err.Error()}
			}
			return starlark.Tuple{a}, nil
		},
		Result: func(ctx context.Context, v starlark.Value) (any, error) { return wire(ctx, req.Verb, v) },
		Seams:  req.Seams,
	})
	if err != nil {
		return nil, err
	}
	return v.([]byte), nil
}
