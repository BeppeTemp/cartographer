package star

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	starjson "go.starlark.net/lib/json"
	"go.starlark.net/starlark"
)

var (
	commandRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	toolRe    = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,47}$`)
	// events are the moments a plugin can hook; sync and connect run them in a
	// later change.
	events = []string{"sync.after", "connect.after"}
)

// registry collects what plugin.star registers while it loads.
type registry struct {
	regs []Registration
	seen map[string]bool
}

func (r *registry) add(reg Registration, key string) error {
	if r.seen[key] {
		return fmt.Errorf("%s is registered twice", key)
	}
	if r.seen == nil {
		r.seen = map[string]bool{}
	}
	r.seen[key] = true
	r.regs = append(r.regs, reg)
	return nil
}

// registrars are the builtins only plugin.star has (a lib file is not given
// them, so plugin.star alone says what the plugin contributes) and that only
// work while it loads: an impl function that calls one later is refused.
func registrars() starlark.StringDict {
	return starlark.StringDict{
		"command": starlark.NewBuiltin("command", registerCommand),
		"tool":    starlark.NewBuiltin("tool", registerTool),
		"on":      starlark.NewBuiltin("on", registerEvent),
	}
}

func registering(thread *starlark.Thread, fn string) (*callCtx, string, error) {
	c := loading(thread)
	if c == nil {
		return nil, "", fmt.Errorf("%s() can only run while plugin.star loads", fn)
	}
	f := thread.CallFrame(1)
	return c, fmt.Sprintf("%s:%d", f.Pos.Filename(), f.Pos.Line), nil
}

func implFunc(fn string, v starlark.Value) (*starlark.Function, error) {
	f, ok := v.(*starlark.Function)
	if !ok {
		return nil, fmt.Errorf("%s: impl must be a function defined in the plugin, not %s", fn, v.Type())
	}
	return f, nil
}

func text(fn, what, s string) error {
	switch {
	case s == "" && what != "usage":
		return fmt.Errorf("%s: %s is empty", fn, what)
	case len(s) > MaxRegistrationText || !utf8.ValidString(s):
		return fmt.Errorf("%s: %s is over %d bytes or not UTF-8", fn, what, MaxRegistrationText)
	}
	return nil
}

func registerCommand(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	c, pos, err := registering(thread, "command")
	if err != nil {
		return nil, err
	}
	var name, help, usage string
	var impl starlark.Value
	if err := starlark.UnpackArgs("command", args, kwargs, "name", &name, "impl", &impl, "help", &help, "usage?", &usage); err != nil {
		return nil, err
	}
	if !commandRe.MatchString(name) || name == "help" {
		return nil, fmt.Errorf("command: name %q must match %s and not be help", name, commandRe)
	}
	fn, err := implFunc("command", impl)
	if err != nil {
		return nil, err
	}
	if err := text("command", "help", help); err != nil {
		return nil, err
	}
	if err := text("command", "usage", usage); err != nil {
		return nil, err
	}
	return starlark.None, c.reg.add(Registration{Kind: KindCommand, Name: name, Help: help, Usage: usage, Pos: pos, impl: fn}, "command "+name)
}

func registerTool(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	c, pos, err := registering(thread, "tool")
	if err != nil {
		return nil, err
	}
	var name, desc string
	var impl, params starlark.Value = nil, starlark.None
	var writes bool
	if err := starlark.UnpackArgs("tool", args, kwargs, "name", &name, "impl", &impl, "description", &desc, "params?", &params, "writes?", &writes); err != nil {
		return nil, err
	}
	if !toolRe.MatchString(name) {
		return nil, fmt.Errorf("tool: name %q must match %s", name, toolRe)
	}
	fn, err := implFunc("tool", impl)
	if err != nil {
		return nil, err
	}
	if err := text("tool", "description", desc); err != nil {
		return nil, err
	}
	schema, err := paramsSchema(thread, params)
	if err != nil {
		return nil, fmt.Errorf("tool %s: params: %w", name, err)
	}
	return starlark.None, c.reg.add(Registration{Kind: KindTool, Name: name, Help: desc, Params: schema, Writes: writes, Pos: pos, impl: fn}, "tool "+name)
}

func registerEvent(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	c, pos, err := registering(thread, "on")
	if err != nil {
		return nil, err
	}
	var event string
	var impl starlark.Value
	if err := starlark.UnpackArgs("on", args, kwargs, "event", &event, "impl", &impl); err != nil {
		return nil, err
	}
	if !slices.Contains(events, event) {
		return nil, fmt.Errorf("on: unknown event %q, expected one of %s", event, strings.Join(events, ", "))
	}
	fn, err := implFunc("on", impl)
	if err != nil {
		return nil, err
	}
	return starlark.None, c.reg.add(Registration{Kind: KindEvent, Name: event, Pos: pos, impl: fn}, "event "+event)
}

// paramsSchema turns the params dict of a tool into the JSON of an object
// schema and checks it resolves, once, at load.
func paramsSchema(thread *starlark.Thread, v starlark.Value) ([]byte, error) {
	if v == starlark.None {
		return []byte(`{"type":"object"}`), nil
	}
	if _, ok := v.(*starlark.Dict); !ok {
		return nil, fmt.Errorf("expected a dict (a JSON schema), got %s", v.Type())
	}
	enc, err := starlark.Call(thread, starjson.Module.Members["encode"], starlark.Tuple{v}, nil)
	if err != nil {
		return nil, err
	}
	raw := []byte(string(enc.(starlark.String)))
	var head struct {
		Type any `json:"type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, err
	}
	if head.Type != "object" {
		return nil, errors.New(`the schema must have "type": "object"`)
	}
	var s jsonschema.Schema
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&s); err != nil {
		return nil, err
	}
	if _, err := s.Resolve(&jsonschema.ResolveOptions{}); err != nil {
		return nil, err
	}
	return raw, nil
}
