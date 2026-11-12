package star

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"go.starlark.net/starlark"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
)

// Host is what a call may reach outside the interpreter: the adapters of the
// capability modules.
type Host struct {
	// LookupEnv reads the environment for env.get; nil is os.LookupEnv. The
	// manifest's list is checked before it is asked.
	LookupEnv func(string) (string, bool)
	// Home, ConfigDir and CacheDir place files.*: the home directory every
	// path stays under and the user's config and cache directories, whose
	// "cartographer" directory is denied. Empty is the OS's.
	Home, ConfigDir, CacheDir string
	// KB reaches the KB's tools for kb.call; nil means no KB is reachable
	// (kb.call fails, saying so).
	KB KBCaller
}

// Request is one call of a registered function.
type Request struct {
	Kind   string // KindCommand, KindTool or KindEvent
	Name   string // the command or tool name, or the event
	Plugin string // the plugin's name, for the log prefix
	// Version is the plugin's version, sent in http.request's User-Agent.
	Version string

	// Args are a command's arguments; Params a tool's arguments, a JSON
	// object validated against the tool's schema; Event the fields of an
	// event's ctx.
	Args   []string
	Params json.RawMessage
	Event  map[string]any

	// ReadOnly is a read-only session: files.write and files.remove are
	// denied and kb.call is limited to read-only tools, whatever the manifest
	// grants.
	ReadOnly bool
	// KBReadOnly limits kb.call to read-only tools without touching files.*: a
	// tool registered writes=False is advertised read-only to the client, so it
	// must not write through the KB whatever the manifest grants.
	KBReadOnly bool

	// Permissions are the manifest's, nil for none.
	Permissions *core.Permissions
	// Declared are the manifest's settings and Values the user's; Call types
	// them, applies the defaults and hands the script `settings`.
	Declared map[string]core.Setting
	Values   map[string]any

	Host Host
	// Stdout receives a command's print; Log receives log.* and, for a tool
	// or an event, print (a bridge's stdout is protocol). Nil discards.
	Stdout, Log io.Writer
}

// Result is what a call returned.
type Result struct {
	// Text is a tool's result text, or a command's printed string.
	Text string
	// ExitCode is a command's exit code.
	ExitCode int
}

// callOpts are the seams of tests: lower caps and extra predeclared names.
type callOpts struct {
	steps uint64
	net   *netSeams
	extra starlark.StringDict
}

// Call runs one registered function on a fresh thread and returns its result.
// The error, when there is one, is a *CallError. Calls are independent: they
// share only the program's sources.
func (p *Program) Call(ctx context.Context, req Request) (Result, error) {
	return p.call(ctx, req, callOpts{})
}

// call translates the request into the runtime's callSpec: what a command, a
// tool or an event is given, how its result is read and which state the
// client-only modules (files, kb) hold are decided here; the runtime (call.go)
// knows none of them.
func (p *Program) call(ctx context.Context, req Request, o callOpts) (Result, error) {
	steps := o.steps
	if steps == 0 {
		steps = MaxCallStepsTool
		if req.Kind == KindCommand {
			steps = MaxCallStepsCommand
		}
	}
	spec := callSpec{
		Kind: req.Kind, Name: req.Name, Plugin: req.Plugin, Version: req.Version,
		Perms: req.Permissions, Declared: req.Declared, Values: req.Values,
		LookupEnv: req.Host.LookupEnv, Log: req.Log, Stdout: req.Stdout,
		PrintToStdout: req.Kind == KindCommand,
		Timeout:       timeout(req.Kind),
		Steps:         steps,
		Net:           o.net,
		Extra:         o.extra,
		Arg:           func(reg *Registration) (starlark.Tuple, error) { return argument(reg, req) },
		Result: func(ctx context.Context, v starlark.Value) (any, error) {
			return result(ctx, req.Kind, v)
		},
		Ext: func(cc *callCtx) any {
			x := &clientExt{}
			x.files = &filesState{cc: cc, host: req.Host, readOnly: req.ReadOnly}
			x.kb = &kbState{cc: cc, caller: req.Host.KB, readOnly: req.ReadOnly || req.KBReadOnly}
			if !req.ReadOnly && req.KBReadOnly {
				x.kb.why = "this tool is registered writes=False"
			}
			return x
		},
	}
	v, err := p.invoke(ctx, spec)
	if err != nil {
		return Result{}, err
	}
	return v.(Result), nil
}

// clientExt is the state of the client-only modules in a call's callCtx.
type clientExt struct {
	files *filesState
	kb    *kbState
}

func filesOf(c *callCtx) *filesState { return c.ext.(*clientExt).files }
func kbOf(c *callCtx) *kbState       { return c.ext.(*clientExt).kb }

func timeout(kind string) time.Duration {
	switch kind {
	case KindTool:
		return ToolTimeout
	case KindEvent:
		return EventTimeout
	}
	return 0
}

// argument is the single value the impl is called with: a list of strings for a
// command, the validated arguments as a dict for a tool, a ctx struct for an event.
func argument(reg *Registration, req Request) (starlark.Tuple, error) {
	switch req.Kind {
	case KindCommand:
		v, _ := toStarlark(req.Args)
		return starlark.Tuple{v}, nil
	case KindTool:
		args, err := toolArguments(reg, req.Params)
		if err != nil {
			return nil, &CallError{Kind: Failed, Msg: err.Error()}
		}
		v, err := toStarlark(args)
		if err != nil {
			return nil, &CallError{Kind: Failed, Msg: err.Error()}
		}
		return starlark.Tuple{v}, nil
	case KindEvent:
		v, err := ctxStruct(req.Event)
		if err != nil {
			return nil, &CallError{Kind: Internal, Msg: err.Error()}
		}
		return starlark.Tuple{v}, nil
	}
	return nil, &CallError{Kind: Internal, Msg: fmt.Sprintf("unknown call kind %q", req.Kind)}
}

// toolArguments decodes the JSON object and validates it against the tool's
// params schema; the error names the field.
func toolArguments(reg *Registration, raw json.RawMessage) (map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var decoded map[string]any
	if err := dec.Decode(&decoded); err != nil || decoded == nil {
		return nil, errors.New("invalid arguments: expected a JSON object")
	}
	// The schema validates the very values the script receives.
	args := normalize(decoded).(map[string]any)
	var s jsonschema.Schema
	if err := json.Unmarshal(reg.Params, &s); err != nil {
		return nil, fmt.Errorf("invalid schema of tool %s: %w", reg.Name, err)
	}
	rs, err := s.Resolve(&jsonschema.ResolveOptions{})
	if err != nil {
		return nil, fmt.Errorf("invalid schema of tool %s: %w", reg.Name, err)
	}
	if err := rs.Validate(args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	return args, nil
}

// normalize turns every json.Number into an int64 when it is a whole number
// that fits (1e2 included), else a float64: JSON has one number type and the
// schema, like the script, must see the same value. A whole number beyond int64
// (1e30) stays a float64, which the schema still accepts as an integer.
func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = normalize(e)
		}
	case []any:
		for i, e := range x {
			x[i] = normalize(e)
		}
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		if f, err := x.Float64(); err == nil {
			if f == math.Trunc(f) && math.Abs(f) < 1<<62 {
				return int64(f)
			}
			return f
		}
	}
	return v
}

func result(ctx context.Context, kind string, v starlark.Value) (Result, error) {
	switch kind {
	case KindEvent:
		return Result{}, nil
	case KindCommand:
		switch x := v.(type) {
		case starlark.NoneType:
			return Result{}, nil
		case starlark.Int:
			n, ok := x.Int64()
			if !ok || n < 0 || n > 255 {
				return Result{}, &CallError{Kind: Failed, Msg: fmt.Sprintf("a command returns an exit code from 0 to 255, got %s", x)}
			}
			return Result{ExitCode: int(n)}, nil
		case starlark.String:
			return textResult(string(x))
		}
		return Result{}, &CallError{Kind: Failed, Msg: fmt.Sprintf("a command returns None, an int or a string, not %s", v.Type())}
	}
	switch x := v.(type) {
	case starlark.NoneType:
		return Result{Text: "ok"}, nil
	case starlark.String:
		return textResult(string(x))
	}
	var raw bytes.Buffer
	if err := encodeJSON(ctx, &raw, v, 0); err != nil {
		return Result{}, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, raw.Bytes(), "", "  "); err != nil {
		return Result{}, &CallError{Kind: Internal, Msg: err.Error()}
	}
	return textResult(out.String())
}

// textResult wraps a size-capped text as a Result.
func textResult(s string) (Result, error) {
	s, err := capText(s)
	if err != nil {
		return Result{}, err
	}
	return Result{Text: s}, nil
}
