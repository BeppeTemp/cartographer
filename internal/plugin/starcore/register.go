package starcore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	starjson "go.starlark.net/lib/json"
	"go.starlark.net/starlark"
)

// Registry collects what plugin.star registers while it loads.
type Registry struct {
	regs []Registration
	seen map[string]bool
}

// Add records a registration under a key that must be unique.
func (r *Registry) Add(reg Registration, key string) error {
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

// Registering is the first step of a registrar: the registry of the run that
// is loading plugin.star (an error anywhere else) and the position of the
// registering call, "file:line".
func Registering(thread *starlark.Thread, fn string) (*Registry, string, error) {
	c := loading(thread)
	if c == nil {
		return nil, "", fmt.Errorf("%s() can only run while plugin.star loads", fn)
	}
	f := thread.CallFrame(1)
	return c.reg, fmt.Sprintf("%s:%d", f.Pos.Filename(), f.Pos.Line), nil
}

// ImplFunc checks that v is a function defined in the plugin.
func ImplFunc(fn string, v starlark.Value) (*starlark.Function, error) {
	f, ok := v.(*starlark.Function)
	if !ok {
		return nil, fmt.Errorf("%s: impl must be a function defined in the plugin, not %s", fn, v.Type())
	}
	return f, nil
}

// CheckText checks a registration text (help, usage, description) against the cap.
func CheckText(fn, what, s string) error {
	switch {
	case s == "" && what != "usage":
		return fmt.Errorf("%s: %s is empty", fn, what)
	case len(s) > MaxRegistrationText || !utf8.ValidString(s):
		return fmt.Errorf("%s: %s is over %d bytes or not UTF-8", fn, what, MaxRegistrationText)
	}
	return nil
}

// ParamsSchema turns the params dict of a tool into the JSON of an object
// schema and checks it resolves, once, at load.
func ParamsSchema(thread *starlark.Thread, v starlark.Value) ([]byte, error) {
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
