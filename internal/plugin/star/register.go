package star

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
