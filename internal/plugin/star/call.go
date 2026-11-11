package star

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/google/jsonschema-go/jsonschema"
	"go.starlark.net/starlark"
)

// ErrKind is how a call failed, for the caller to map to an exit code or an
// MCP error.
type ErrKind int

const (
	// Failed is the script's own failure: fail(), a runtime error, a bad
	// argument, a bad setting.
	Failed ErrKind = iota + 1
	// Denied is a capability the manifest does not grant.
	Denied
	// LimitExceeded is a cap hit: steps, time, size or count.
	LimitExceeded
	// Cancelled is the caller's context cancelled (Ctrl-C).
	Cancelled
	// Internal is a bug of the host: a panic, an impl that is not there.
	Internal
)

func (k ErrKind) String() string {
	return [...]string{"", "failed", "denied", "limit exceeded", "cancelled", "internal error"}[k]
}

// CallError is what Call returns when the call did not succeed.
type CallError struct {
	Kind ErrKind
	// Msg is the message for the user: for fail() the script's own text, for
	// any other error the message with its position.
	Msg string
	// Trace is the Starlark backtrace, when the error has one.
	Trace string
}

func (e *CallError) Error() string { return e.Msg }

// deniedError and limitError are what a capability builtin returns; classify
// finds them through the interpreter's error wrapping.
type deniedError struct{ msg string }

func (e *deniedError) Error() string { return e.msg }

type limitError struct{ msg string }

func (e *limitError) Error() string { return e.msg }

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

func (p *Program) call(ctx context.Context, req Request, o callOpts) (res Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			res, err = Result{}, &CallError{Kind: Internal, Msg: fmt.Sprintf("plugin %s: internal error: %v", req.Plugin, r)}
		}
	}()
	reg := p.find(req.Kind, req.Name)
	if reg == nil {
		return Result{}, &CallError{Kind: Internal, Msg: fmt.Sprintf("%s %q is not registered by the plugin", req.Kind, req.Name)}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, ctxError(err)
	}
	settings, serr := newSettings(req.Declared, req.Values)
	if serr != nil {
		return Result{}, &CallError{Kind: Failed, Msg: serr.Error()}
	}
	arg, err := argument(reg, req)
	if err != nil {
		return Result{}, err
	}

	// The deadline, the watcher and the step budget cover the whole call,
	// including the private run of plugin.star below.
	if d := timeout(req.Kind); d > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}
	steps := o.steps
	if steps == 0 {
		steps = MaxCallStepsTool
		if req.Kind == KindCommand {
			steps = MaxCallStepsCommand
		}
	}

	// The same sources run again for this call, so the functions bind this
	// call's settings (see settingsValue). It is charged to the call's budget.
	lim := &runLimits{ctx: ctx, budget: steps, base: liveHeap()}
	inst, lerr := run(p.srcs, settings, o.extra, lim)
	if lerr != nil {
		if ce := lim.failure(); ce != nil {
			return Result{}, ce
		}
		return Result{}, &CallError{Kind: Internal, Msg: "the script no longer loads: " + client.Escape(lerr.Error())}
	}
	if !sameRegistrations(p.Registrations, inst.Registrations) {
		return Result{}, &CallError{Kind: Internal, Msg: "the script registers something else when it runs again: what it registers must not depend on anything but its sources"}
	}
	impl := inst.implOf(req.Kind, req.Name)
	if impl == nil {
		return Result{}, &CallError{Kind: Internal, Msg: fmt.Sprintf("%s %q is not registered by the plugin", req.Kind, req.Name)}
	}
	settings.open = true

	logw := newCapped(req.Log)
	cc := &callCtx{phase: phaseCall, plugin: req.Plugin, log: logw, ctx: ctx, perms: req.Permissions, lookupEnv: req.Host.LookupEnv}
	if cc.lookupEnv == nil {
		cc.lookupEnv = os.LookupEnv
	}
	cc.net = newNetState(cc, req.Version, o.net)
	cc.files = &filesState{cc: cc, host: req.Host, readOnly: req.ReadOnly}
	cc.kb = &kbState{cc: cc, caller: req.Host.KB, readOnly: req.ReadOnly || req.KBReadOnly}
	if !req.ReadOnly && req.KBReadOnly {
		cc.kb.why = "this tool is registered writes=False"
	}
	var printw io.Writer = logw
	prefix := "plugin " + req.Plugin + ": "
	if req.Kind == KindCommand {
		printw, prefix = newCapped(req.Stdout), ""
	}
	var stepsHit atomic.Bool
	thread := &starlark.Thread{
		Name:  req.Kind + " " + req.Name,
		Print: func(_ *starlark.Thread, msg string) { emit(printw, prefix, msg) },
		OnMaxSteps: func(t *starlark.Thread) {
			stepsHit.Store(true)
			t.Cancel(stepsCancel)
		},
	}
	thread.SetLocal(ctxKey, cc)
	left := steps - min(steps, lim.used)
	thread.SetMaxExecutionSteps(max(left, 1))

	// The watcher ends with the call, however it ends (a recovered panic included).
	done := make(chan struct{})
	var watcher sync.WaitGroup
	watcher.Add(1)
	go func() {
		defer watcher.Done()
		select {
		case <-ctx.Done():
			thread.Cancel("context done")
		case <-done:
		}
	}()
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(func() { close(done); watcher.Wait() }) }
	defer stop()
	mem := watchHeap(lim.base, thread.Cancel)
	defer mem.stop() // also on a panic
	v, cerr := starlark.Call(thread, impl, arg, nil)
	mem.stop()
	stop()
	// Flush a truncation mark even when the output was capped after the last write.
	logw.finish()
	if c, ok := printw.(*capped); ok {
		c.finish()
	}

	if cerr != nil {
		if mem.hit.Load() {
			return Result{}, &CallError{Kind: LimitExceeded, Msg: memoryMsg()}
		}
		return Result{}, classify(cerr, ctx, stepsHit.Load(), steps)
	}
	return result(ctx, req.Kind, v)
}

// sameRegistrations compares what two runs of the script registered.
func sameRegistrations(a, b []Registration) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.Kind != y.Kind || x.Name != y.Name || x.Help != y.Help || x.Usage != y.Usage ||
			!bytes.Equal(x.Params, y.Params) || x.Writes != y.Writes || x.Pos != y.Pos {
			return false
		}
	}
	return true
}

// emit writes msg as lines, each with the prefix and its control characters
// escaped, so a script cannot forge another plugin's prefix or rewrite the terminal.
func emit(w io.Writer, prefix, msg string) {
	msg = strings.ReplaceAll(strings.TrimSuffix(msg, "\n"), "\r\n", "\n")
	for _, line := range strings.Split(msg, "\n") {
		fmt.Fprintln(w, prefix+client.Escape(line))
	}
}

// escapeLines escapes each line of a multi-line text.
func escapeLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = client.Escape(l)
	}
	return strings.Join(lines, "\n")
}

func timeout(kind string) time.Duration {
	switch kind {
	case KindTool:
		return ToolTimeout
	case KindEvent:
		return EventTimeout
	}
	return 0
}

func (p *Program) find(kind, name string) *Registration {
	for i := range p.Registrations {
		if r := &p.Registrations[i]; r.Kind == kind && r.Name == name {
			return r
		}
	}
	return nil
}

// find on a run's program returns the impl itself.
func (p *Program) implOf(kind, name string) *starlark.Function {
	if r := p.find(kind, name); r != nil {
		return r.impl
	}
	return nil
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
			return capText(string(x))
		}
		return Result{}, &CallError{Kind: Failed, Msg: fmt.Sprintf("a command returns None, an int or a string, not %s", v.Type())}
	}
	switch x := v.(type) {
	case starlark.NoneType:
		return Result{Text: "ok"}, nil
	case starlark.String:
		return capText(string(x))
	}
	var raw bytes.Buffer
	if err := encodeJSON(ctx, &raw, v, 0); err != nil {
		return Result{}, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, raw.Bytes(), "", "  "); err != nil {
		return Result{}, &CallError{Kind: Internal, Msg: err.Error()}
	}
	return capText(out.String())
}

const maxJSONDepth = 100

// encodeJSON writes v as compact JSON and stops with LimitExceeded as soon as
// the output passes MaxResultBytes, so shared substructure (x = [x, x] again
// and again) costs a bounded amount of work; it also honours the call's context.
func encodeJSON(ctx context.Context, w *bytes.Buffer, v starlark.Value, depth int) error {
	if w.Len() > MaxResultBytes {
		return &CallError{Kind: LimitExceeded, Msg: fmt.Sprintf("the result is over the %d byte cap", MaxResultBytes)}
	}
	if err := ctx.Err(); err != nil {
		return ctxError(err)
	}
	if depth > maxJSONDepth {
		return &CallError{Kind: Failed, Msg: "the result is nested too deeply or contains itself"}
	}
	bad := func() error {
		return &CallError{Kind: Failed, Msg: "the result is not JSON-able: " + v.Type()}
	}
	switch x := v.(type) {
	case starlark.NoneType:
		w.WriteString("null")
	case starlark.Bool:
		fmt.Fprint(w, bool(x))
	case starlark.Int:
		w.WriteString(x.String())
	case starlark.Float:
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return &CallError{Kind: Failed, Msg: "the result is not JSON-able: a float that is not finite"}
		}
		b, _ := json.Marshal(f)
		w.Write(b)
	case starlark.String:
		writeJSONString(w, string(x))
	case *starlark.List:
		w.WriteByte('[')
		for i := 0; i < x.Len(); i++ {
			if i > 0 {
				w.WriteByte(',')
			}
			if err := encodeJSON(ctx, w, x.Index(i), depth+1); err != nil {
				return err
			}
		}
		w.WriteByte(']')
	case starlark.Tuple:
		w.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				w.WriteByte(',')
			}
			if err := encodeJSON(ctx, w, e, depth+1); err != nil {
				return err
			}
		}
		w.WriteByte(']')
	case *starlark.Dict:
		keys := make([]string, 0, x.Len())
		vals := map[string]starlark.Value{}
		for _, kv := range x.Items() {
			k, ok := starlark.AsString(kv[0])
			if !ok {
				return &CallError{Kind: Failed, Msg: "the result is not JSON-able: a dict key of type " + kv[0].Type()}
			}
			keys = append(keys, k)
			vals[k] = kv[1]
		}
		sort.Strings(keys)
		w.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				w.WriteByte(',')
			}
			writeJSONString(w, k)
			w.WriteByte(':')
			if err := encodeJSON(ctx, w, vals[k], depth+1); err != nil {
				return err
			}
		}
		w.WriteByte('}')
	default:
		return bad()
	}
	return nil
}

func writeJSONString(w *bytes.Buffer, s string) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	w.Truncate(w.Len() - 1) // Encode ends with a newline
}

func capText(s string) (Result, error) {
	if len(s) > MaxResultBytes {
		return Result{}, &CallError{Kind: LimitExceeded, Msg: fmt.Sprintf("the result is %d bytes, over the %d cap", len(s), MaxResultBytes)}
	}
	return Result{Text: s}, nil
}

func ctxError(err error) *CallError {
	if errors.Is(err, context.DeadlineExceeded) {
		return &CallError{Kind: LimitExceeded, Msg: "the time limit was exceeded"}
	}
	return &CallError{Kind: Cancelled, Msg: "cancelled"}
}

// classify maps what the interpreter returned to a typed error.
func classify(err error, ctx context.Context, stepsHit bool, steps uint64) *CallError {
	if stepsHit {
		return &CallError{Kind: LimitExceeded, Msg: fmt.Sprintf("the script ran more than %d steps", steps)}
	}
	if cerr := ctx.Err(); cerr != nil {
		return ctxError(cerr)
	}
	var denied *deniedError
	var limit *limitError
	switch {
	case errors.As(err, &denied):
		return &CallError{Kind: Denied, Msg: escapeLines(denied.msg)}
	case errors.As(err, &limit):
		return &CallError{Kind: LimitExceeded, Msg: escapeLines(limit.msg)}
	}
	var ev *starlark.EvalError
	if !errors.As(err, &ev) {
		return &CallError{Kind: Internal, Msg: client.Escape(err.Error())}
	}
	if n := len(ev.CallStack); n > 0 && ev.CallStack[n-1].Name == "fail" && strings.HasPrefix(ev.Msg, "fail: ") {
		return &CallError{Kind: Failed, Msg: client.Escape(strings.TrimPrefix(ev.Msg, "fail: "))}
	}
	le := loadError(ev)
	return &CallError{Kind: Failed, Msg: client.Escape(le.Msg), Trace: escapeLines(le.Trace)}
}

// capped writes at most MaxPrintBytes, then drops the rest and, once, says so.
type capped struct {
	mu      sync.Mutex
	w       io.Writer
	n       int
	dropped bool
	marked  bool
}

func newCapped(w io.Writer) *capped {
	if w == nil {
		w = io.Discard
	}
	return &capped{w: w}
}

func (c *capped) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	room := MaxPrintBytes - c.n
	if room < len(b) {
		c.dropped = true
		b2 := b[:max(room, 0)]
		c.n += len(b2)
		_, _ = c.w.Write(b2)
		return len(b), nil
	}
	c.n += len(b)
	_, _ = c.w.Write(b)
	return len(b), nil
}

func (c *capped) finish() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dropped && !c.marked {
		c.marked = true
		_, _ = io.WriteString(c.w, "\n[output truncated]\n")
	}
}
