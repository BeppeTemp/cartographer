package star

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"

	"github.com/BeppeTemp/cartographer/internal/plugin"
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

	// The rest is set for a CALL only.
	plugin string
	log    io.Writer // the call's log sink: log.* and, for a tool or event, print
	ctx    context.Context
	// perms are the manifest's granted permissions; the capability modules of
	// the following changes read them here, never from the script.
	perms *plugin.Permissions
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
}

func capabilityModules(settings *settingsValue) starlark.StringDict {
	out := starlark.StringDict{"settings": settings, "log": logModule()}
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

// logModule writes to the call's log sink, never to stdout (a bridge's stdout
// is protocol). While loading there is no sink: the registered set must not
// depend on a side effect.
func logModule() *starlarkstruct.Module {
	m := &starlarkstruct.Module{Name: "log", Members: starlark.StringDict{}}
	for _, level := range []string{"info", "warn", "error"} {
		full := "log." + level
		m.Members[level] = starlark.NewBuiltin(full, func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
			var msg string
			if err := starlark.UnpackPositionalArgs(full, args, kwargs, 1, &msg); err != nil {
				return nil, err
			}
			c := ctxOf(thread)
			if c == nil || c.phase != phaseCall {
				return nil, fmt.Errorf("%s is not available while plugin.star loads: it only registers commands, tools and events", full)
			}
			prefix := "plugin " + c.plugin + ": "
			if level != "info" {
				prefix += level + ": "
			}
			emit(c.log, prefix, msg)
			return starlark.None, nil
		})
	}
	return m
}
