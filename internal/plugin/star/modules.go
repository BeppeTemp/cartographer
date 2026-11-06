package star

import (
	"errors"
	"fmt"
	"maps"

	"go.starlark.net/lib/json"
	"go.starlark.net/lib/math"
	"go.starlark.net/lib/time"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

type phase int

const (
	phaseLoad phase = iota + 1
	phaseCall
)

// ctxKey is the thread-local that holds the *callCtx of a run.
const ctxKey = "cartographer.plugin.ctx"

// callCtx is what a run knows besides its Starlark values; one per thread, and
// threads are never shared. The modules read it when they are called, not when
// they are created, because a Starlark function keeps the predeclared names of
// the load that made it (the trap the package comment names).
type callCtx struct {
	phase phase
	reg   *registry // only while loading plugin.star
}

func ctxOf(thread *starlark.Thread) *callCtx {
	c, _ := thread.Local(ctxKey).(*callCtx)
	return c
}

// loading reports whether thread is running plugin.star's LOAD.
func loading(thread *starlark.Thread) *callCtx {
	if c := ctxOf(thread); c != nil && c.phase == phaseLoad {
		return c
	}
	return nil
}

// pureModules are the modules without effects. time.now reads the clock, so it
// fails while loading: the registered set must not depend on it.
func pureModules() starlark.StringDict {
	tm := maps.Clone(time.Module.Members)
	tm["now"] = starlark.NewBuiltin("now", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if loading(thread) != nil {
			return nil, errors.New("time.now is not available while plugin.star loads: what a plugin registers must not depend on the clock")
		}
		return starlark.Call(thread, time.Module.Members["now"], args, kwargs)
	})
	return starlark.StringDict{
		"json": &starlarkstruct.Module{Name: "json", Members: maps.Clone(json.Module.Members)},
		"math": &starlarkstruct.Module{Name: "math", Members: maps.Clone(math.Module.Members)},
		"time": &starlarkstruct.Module{Name: "time", Members: tm},
	}
}

// capabilityFuncs lists the functions of each capability module. A later change
// gives them bodies; until then, and always while loading, a call is refused
// with an error naming why.
var capabilityFuncs = map[string][]string{
	"kb":    {"call"},
	"http":  {"request"},
	"files": {"read", "write", "remove", "glob"},
	"env":   {"get"},
	"log":   {"debug", "info", "warn", "error"},
}

func capabilityModules() starlark.StringDict {
	out := starlark.StringDict{"settings": settingsValue{}}
	for mod, fns := range capabilityFuncs {
		m := &starlarkstruct.Module{Name: mod, Members: starlark.StringDict{}}
		for _, fn := range fns {
			full := mod + "." + fn
			m.Members[fn] = starlark.NewBuiltin(full, func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
				if loading(thread) != nil {
					return nil, fmt.Errorf("%s is not available while plugin.star loads: it only registers commands, tools and events", full)
				}
				return nil, fmt.Errorf("%s is not available yet", full)
			})
		}
		out[mod] = m
	}
	return out
}

// settingsValue is the `settings` name. A read like settings.x reaches
// Attr, which has no thread: it cannot find the context of the call, so unlike
// the functions above it cannot be late-bound through the thread. While loading
// that is exactly right (a setting can change without a new approval, so the
// registered set must not read one); the CALL phase has to give a function the
// settings some other way, and must not rely on this type.
type settingsValue struct{}

var _ starlark.HasAttrs = settingsValue{}

func (settingsValue) String() string        { return "<settings>" }
func (settingsValue) Type() string          { return "settings" }
func (settingsValue) Freeze()               {}
func (settingsValue) Truth() starlark.Bool  { return true }
func (settingsValue) Hash() (uint32, error) { return 0, errors.New("unhashable type: settings") }
func (settingsValue) AttrNames() []string   { return nil }
func (settingsValue) Attr(name string) (starlark.Value, error) {
	return nil, fmt.Errorf("settings.%s is not available while plugin.star loads: a setting can change without a new approval, so what a plugin registers cannot read one", name)
}
