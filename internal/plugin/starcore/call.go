package starcore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/plugin/starcore/internal/seam"
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

// CallSpec is one call, as the runtime sees it. The command, tool and event
// kinds live in the client half (request.go): it decides what the impl is
// given (Arg), how its result is read (Result), which state the client-only
// modules keep in the call's context (Ext), the wall clock and the step cap.
type CallSpec struct {
	Kind, Name string // the registration called
	Plugin     string // for the log prefix and the User-Agent
	Version    string // sent in http.request's User-Agent

	Perms    *core.Permissions
	Declared map[string]core.Setting
	Values   map[string]any

	// LookupEnv reads the environment for env.get; nil is os.LookupEnv.
	LookupEnv func(string) (string, bool)

	// Log receives log.* and print; with PrintToStdout print goes to Stdout
	// instead, without prefix. Nil discards.
	Log, Stdout   io.Writer
	PrintToStdout bool

	// Timeout is the wall clock (0 none); Steps the step cap of the whole call,
	// which the host always sets (the default per kind is the host's).
	Timeout time.Duration
	Steps   uint64

	// Arg builds the arguments the impl is called with; its errors are *CallError.
	Arg func(reg *Registration) (starlark.Tuple, error)
	// Result reads the value the impl returned; ctx carries the call's deadline.
	Result func(ctx context.Context, v starlark.Value) (any, error)
	// Ext builds the state of the modules the host adds (CallCtx.ext); nil for none.
	Ext func(cc *CallCtx) any
	// Same compares what two runs of the script registered; nil is sameRegistrations.
	Same func(a, b []Registration) bool

	// Seams are the test seams of a call; only startest can build one.
	Seams *seam.Seams
}

// Invoke runs one registered function on a fresh thread and returns what
// spec.Result made of its value. The error, when there is one, is a *CallError.
func (p *Script) Invoke(ctx context.Context, spec CallSpec) (res any, err error) {
	defer func() {
		if r := recover(); r != nil {
			res, err = nil, &CallError{Kind: Internal, Msg: fmt.Sprintf("plugin %s: internal error: %v", spec.Plugin, r)}
		}
	}()
	reg := p.find(spec.Kind, spec.Name)
	if reg == nil {
		return nil, &CallError{Kind: Internal, Msg: fmt.Sprintf("%s %q is not registered by the plugin", spec.Kind, spec.Name)}
	}
	if err := ctx.Err(); err != nil {
		return nil, ctxError(err)
	}
	settings, serr := newSettings(spec.Declared, spec.Values)
	if serr != nil {
		return nil, &CallError{Kind: Failed, Msg: serr.Error()}
	}
	arg, err := spec.Arg(reg)
	if err != nil {
		return nil, err
	}

	// The deadline, the watcher and the step budget cover the whole call,
	// including the private run of plugin.star below.
	if d := spec.Timeout; d > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}
	steps := spec.Steps
	if steps == 0 {
		return nil, &CallError{Kind: Internal, Msg: "the host did not set a step cap for the call"}
	}

	// The same sources run again for this call, so the functions bind this
	// call's settings (see settingsValue). It is charged to the call's budget.
	lim := &runLimits{ctx: ctx, budget: steps, base: liveHeap()}
	opts := p.opts
	var net *seam.NetSeams
	if spec.Seams != nil {
		opts.Extra = Merge(opts.Extra, spec.Seams.Extra)
		net = spec.Seams.Net
	}
	inst, lerr := run(p.sources(), settings, opts, lim)
	if lerr != nil {
		if ce := lim.failure(); ce != nil {
			return nil, ce
		}
		return nil, &CallError{Kind: Internal, Msg: "the script no longer loads: " + client.Escape(lerr.Error())}
	}
	same := spec.Same
	if same == nil {
		same = sameRegistrations
	}
	if !same(p.Registrations, inst.Registrations) {
		return nil, &CallError{Kind: Internal, Msg: "the script registers something else when it runs again: what it registers must not depend on anything but its sources"}
	}
	impl := inst.implOf(spec.Kind, spec.Name)
	if impl == nil {
		return nil, &CallError{Kind: Internal, Msg: fmt.Sprintf("%s %q is not registered by the plugin", spec.Kind, spec.Name)}
	}
	settings.open = true

	logw := newCapped(spec.Log)
	cc := &CallCtx{phase: phaseCall, Plugin: spec.Plugin, Log: logw, Ctx: ctx, Perms: spec.Perms, LookupEnv: spec.LookupEnv}
	if cc.LookupEnv == nil {
		cc.LookupEnv = os.LookupEnv
	}
	cc.net = newNetState(cc, spec.Version, net)
	if spec.Ext != nil {
		cc.Ext = spec.Ext(cc)
	}
	var printw io.Writer = logw
	prefix := "plugin " + spec.Plugin + ": "
	if spec.PrintToStdout {
		printw, prefix = newCapped(spec.Stdout), ""
	}
	var stepsHit atomic.Bool
	thread := &starlark.Thread{
		Name:  spec.Kind + " " + spec.Name,
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
			return nil, &CallError{Kind: LimitExceeded, Msg: memoryMsg()}
		}
		return nil, classify(cerr, ctx, stepsHit.Load(), steps)
	}
	return spec.Result(ctx, v)
}

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

// EscapeLines escapes each line of a multi-line text.
func EscapeLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = client.Escape(l)
	}
	return strings.Join(lines, "\n")
}

func (p *Script) find(kind, name string) *Registration {
	for i := range p.Registrations {
		if r := &p.Registrations[i]; r.Kind == kind && r.Name == name {
			return r
		}
	}
	return nil
}

// find on a run's program returns the impl itself.
func (p *Script) implOf(kind, name string) *starlark.Function {
	if r := p.find(kind, name); r != nil {
		return r.Impl
	}
	return nil
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
		return &CallError{Kind: Denied, Msg: EscapeLines(denied.msg)}
	case errors.As(err, &limit):
		return &CallError{Kind: LimitExceeded, Msg: EscapeLines(limit.msg)}
	}
	var ev *starlark.EvalError
	if !errors.As(err, &ev) {
		return &CallError{Kind: Internal, Msg: client.Escape(err.Error())}
	}
	if n := len(ev.CallStack); n > 0 && ev.CallStack[n-1].Name == "fail" && strings.HasPrefix(ev.Msg, "fail: ") {
		return &CallError{Kind: Failed, Msg: client.Escape(strings.TrimPrefix(ev.Msg, "fail: "))}
	}
	le := loadError(ev)
	return &CallError{Kind: Failed, Msg: client.Escape(le.Msg), Trace: EscapeLines(le.Trace)}
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
