package star

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/plugin"
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

func loadError(err error) *LoadError {
	var ev *starlark.EvalError
	if errors.As(err, &ev) {
		msg := ev.Msg
		for i := len(ev.CallStack) - 1; i >= 0; i-- {
			// The innermost frame of a builtin error is the builtin itself, which has no source.
			if p := ev.CallStack[i].Pos; p.IsValid() && p.Filename() != "<builtin>" {
				msg = fmt.Sprintf("%s:%d:%d: %s", p.Filename(), p.Line, p.Col, ev.Msg)
				break
			}
		}
		return &LoadError{Msg: msg, Trace: ev.Backtrace()}
	}
	return &LoadError{Msg: err.Error()}
}

// Check is plugin.Options.CheckScripts: the problems of a tree's scripts.
func Check(files []plugin.TreeFile) []string {
	if _, err := Load(files); err != nil {
		return []string{err.Error()}
	}
	return nil
}

// Load runs plugin.star once, with lib/*.star loadable from it, and reports
// what it registered. It reads the files it is given and nothing else. A tree
// without plugin.star and without lib/ is not a script plugin: (nil, nil).
func Load(files []plugin.TreeFile) (*Program, error) {
	srcs := map[string][]byte{}
	libs := 0
	for _, f := range files {
		if !plugin.IsScriptPath(f.Path) {
			continue
		}
		switch {
		case f.Path != plugin.ScriptEntry && !plugin.IsLibPath(f.Path):
			return nil, &LoadError{Msg: fmt.Sprintf("%s: only plugin.star and lib/<name>.star are scripts", f.Path)}
		case len(f.Content) > plugin.MaxScriptBytes:
			return nil, &LoadError{Msg: fmt.Sprintf("%s: %d bytes, over the %d cap of a script", f.Path, len(f.Content), plugin.MaxScriptBytes)}
		}
		if f.Path != plugin.ScriptEntry {
			libs++
		}
		srcs[f.Path] = f.Content
	}
	if len(srcs) == 0 {
		return nil, nil
	}
	_, ok := srcs[plugin.ScriptEntry]
	if !ok {
		return nil, &LoadError{Msg: "lib/ needs a plugin.star at the plugin root"}
	}
	if libs > plugin.MaxLibFiles {
		return nil, &LoadError{Msg: fmt.Sprintf("lib/: %d files, over the %d cap", libs, plugin.MaxLibFiles)}
	}

	return run(srcs, &settingsValue{}, nil, nil)
}

// run executes plugin.star over srcs. settings is the `settings` every function
// of this run binds; extra is added to the predeclared names (a test seam, and
// where later changes hand the host adapters over). A CALL runs it again for
// its own thread, with its own settings: see settingsValue.
func run(srcs map[string][]byte, settings *settingsValue, extra starlark.StringDict, lim *runLimits) (*Program, error) {
	entry := srcs[plugin.ScriptEntry]
	reg := &registry{}
	ctx := &callCtx{phase: phaseLoad, reg: reg}
	pure, caps := pureModules(), merge(capabilityModules(settings), extra)
	if lim != nil {
		defer lim.watch()()
	}
	l := &loader{srcs: srcs, ctx: ctx, lim: lim, cache: map[string]*loaded{}, predeclared: merge(pure, caps)}
	thread := l.newThread(plugin.ScriptEntry)
	entryEnv := merge(pure, caps, registrars())
	globals, err := l.exec(thread, plugin.ScriptEntry, entry, entryEnv)
	if err != nil {
		return nil, loadError(err)
	}
	globals.Freeze()

	p := &Program{Registrations: reg.regs, globals: globals, srcs: srcs}
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
}

type loaded struct {
	globals starlark.StringDict
	err     error
	running bool
}

func (l *loader) newThread(name string) *starlark.Thread {
	t := &starlark.Thread{Name: name, Load: l.load, Print: func(*starlark.Thread, string) {}}
	t.SetLocal(ctxKey, l.ctx)
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
	if !plugin.IsLibPath(module) {
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
