package star

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// fileOptions is the language subset: sets on; while, recursion, top-level
// control flow and global reassignment off (the go.starlark.net defaults), so
// every loop runs over a finite value and the step cap is only the backstop.
var fileOptions = &syntax.FileOptions{Set: true}

// LoadError is a script that does not load, with the position of the failure.
type LoadError struct {
	Msg string // "file:line:col: reason", or the reason alone
	// Trace is the Starlark backtrace, when the failure has one.
	Trace string
}

func (e *LoadError) Error() string { return e.Msg }

// stepsCancel is the reason a thread is cancelled with when it runs out of steps.
const stepsCancel = "too many steps"

func loadError(err error) *LoadError {
	var ev *starlark.EvalError
	if errors.As(err, &ev) {
		msg := ev.Msg
		// The cap of a first Load; a load inside a call is reported by
		// runLimits.failure, with the cap that thread had.
		if strings.HasSuffix(msg, "cancelled: "+stepsCancel) {
			// Said as a call says it; the position below stays.
			msg = fmt.Sprintf("the script ran more than %d steps", MaxLoadSteps)
		}
		for i := len(ev.CallStack) - 1; i >= 0; i-- {
			// The innermost frame of a builtin error is the builtin itself, which has no source.
			if p := ev.CallStack[i].Pos; p.IsValid() && p.Filename() != "<builtin>" {
				msg = fmt.Sprintf("%s:%d:%d: %s", p.Filename(), p.Line, p.Col, msg)
				break
			}
		}
		return &LoadError{Msg: msg, Trace: ev.Backtrace()}
	}
	return &LoadError{Msg: err.Error()}
}

// loadScript runs plugin.star once, with lib/*.star loadable from it, and
// reports what it registered. It reads the files it is given and nothing else.
// A tree without plugin.star and without lib/ is not a script plugin: (nil, nil).
func loadScript(files []core.TreeFile, opts Options) (*script, error) {
	srcs := map[string][]byte{}
	libs := 0
	for _, f := range files {
		if !core.IsScriptPath(f.Path) {
			continue
		}
		switch {
		case f.Path != core.ScriptEntry && !core.IsLibPath(f.Path):
			return nil, &LoadError{Msg: fmt.Sprintf("%s: only plugin.star and lib/<name>.star are scripts", f.Path)}
		case len(f.Content) > core.MaxScriptBytes:
			return nil, &LoadError{Msg: fmt.Sprintf("%s: %d bytes, over the %d cap of a script", f.Path, len(f.Content), core.MaxScriptBytes)}
		}
		if f.Path != core.ScriptEntry {
			libs++
		}
		srcs[f.Path] = f.Content
	}
	if len(srcs) == 0 {
		return nil, nil
	}
	_, ok := srcs[core.ScriptEntry]
	if !ok {
		return nil, &LoadError{Msg: "lib/ needs a plugin.star at the plugin root"}
	}
	if libs > core.MaxLibFiles {
		return nil, &LoadError{Msg: fmt.Sprintf("lib/: %d files, over the %d cap", libs, core.MaxLibFiles)}
	}

	return run(srcs, &settingsValue{}, opts, nil)
}

// run executes plugin.star over srcs. settings is the `settings` every function
// of this run binds; opts.Extra is added to the predeclared names (the host
// adapters, and a test seam) and opts.Registrars are the builtins plugin.star
// alone has. A CALL runs it again for its own thread, with its own settings:
// see settingsValue.
func run(srcs map[string][]byte, settings *settingsValue, opts Options, lim *runLimits) (*script, error) {
	entry := srcs[core.ScriptEntry]
	reg := &registry{}
	ctx := &callCtx{phase: phaseLoad, reg: reg}
	pure, caps := pureModules(), merge(capabilityModules(settings), opts.Extra)
	if lim != nil {
		defer lim.watch()()
	}
	l := &loader{srcs: srcs, ctx: ctx, lim: lim, cache: map[string]*loaded{}, predeclared: merge(pure, caps)}
	base := uint64(0)
	if lim != nil {
		base = lim.base // a call has one baseline, taken before its re-run
	} else {
		base = liveHeap()
	}
	mem := watchHeap(base, l.cancelAll)
	defer mem.stop()
	thread := l.newThread(core.ScriptEntry)
	entryEnv := merge(pure, caps, opts.Registrars)
	globals, err := l.exec(thread, core.ScriptEntry, entry, entryEnv)
	if mem.hit.Load() {
		if lim != nil {
			lim.memHit.Store(true)
		}
		return nil, &LoadError{Msg: memoryMsg()}
	}
	if err != nil {
		return nil, loadError(err)
	}
	globals.Freeze()

	p := &script{Registrations: reg.regs, globals: globals, srcs: srcs, opts: Options{Registrars: opts.Registrars, Extra: opts.Extra}}
	for _, path := range slices.Sorted(maps.Keys(srcs)) {
		p.Files = append(p.Files, fileInfo(path, srcs[path]))
	}
	return p, nil
}

func merge(ds ...starlark.StringDict) starlark.StringDict {
	out := starlark.StringDict{}
	for _, d := range ds {
		for k, v := range d {
			out[k] = v
		}
	}
	return out
}

// loader resolves load("lib/<name>.star") inside the tree: no other source
// exists, and nothing is read from the filesystem.
type loader struct {
	srcs        map[string][]byte
	lim         *runLimits // nil for a first Load
	ctx         *callCtx
	cache       map[string]*loaded
	stack       []string
	predeclared starlark.StringDict // pure and capability modules, no registrars

	mu      sync.Mutex
	threads []*starlark.Thread // every thread of the run, for the memory watchdog
	over    bool               // the watchdog fired: later threads start cancelled
}

// cancelAll is the memory watchdog's cancel: it stops every thread of the run,
// those still to start included.
func (l *loader) cancelAll(reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.over = true
	for _, t := range l.threads {
		t.Cancel(reason)
	}
}

type loaded struct {
	globals starlark.StringDict
	err     error
	running bool
}

func (l *loader) newThread(name string) *starlark.Thread {
	t := &starlark.Thread{Name: name, Load: l.load, Print: func(*starlark.Thread, string) {}}
	t.SetLocal(ctxKey, l.ctx)
	l.mu.Lock()
	l.threads = append(l.threads, t)
	if l.over {
		t.Cancel(memoryMsg())
	}
	l.mu.Unlock()
	max := MaxLoadSteps
	if l.lim != nil {
		// 0 would mean unlimited: a spent budget leaves one step.
		max = l.lim.start(t)
		if max == 0 {
			max = 1
		}
	}
	t.SetMaxExecutionSteps(max)
	return t
}

// exec runs one source file on thread and charges its steps to the call.
func (l *loader) exec(t *starlark.Thread, name string, src []byte, env starlark.StringDict) (starlark.StringDict, error) {
	g, err := starlark.ExecFileOptions(fileOptions, t, name, src, env)
	if l.lim != nil {
		l.lim.charge(t)
	}
	return g, err
}

func (l *loader) load(_ *starlark.Thread, module string) (starlark.StringDict, error) {
	if !core.IsLibPath(module) {
		return nil, fmt.Errorf("cannot load %q: a plugin loads only lib/<name>.star from its own tree", module)
	}
	if e, ok := l.cache[module]; ok {
		if e.running {
			return nil, fmt.Errorf("load cycle: %s -> %s", strings.Join(l.stack, " -> "), module)
		}
		return e.globals, e.err
	}
	src, ok := l.srcs[module]
	if !ok {
		return nil, fmt.Errorf("cannot load %q: no such file in the plugin", module)
	}
	e := &loaded{running: true}
	l.cache[module] = e
	l.stack = append(l.stack, module)
	defer func() { l.stack = l.stack[:len(l.stack)-1] }()
	globals, err := l.exec(l.newThread(module), module, src, l.predeclared)
	e.running = false
	if err != nil {
		e.err = err
		return nil, err
	}
	globals.Freeze()
	e.globals = globals
	return globals, nil
}
