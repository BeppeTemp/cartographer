package starcore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/plugin/starcore/internal/seam"
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

// ctxKey is the thread-local that holds the *CallCtx of a run.
const ctxKey = "cartographer.plugin.ctx"

// CallCtx is what a run knows besides its Starlark values; one per thread, and
// threads are never shared. The modules read it when they are called, not when
// they are created, because a Starlark function keeps the predeclared names of
// the load that made it (the trap the package comment names).
type CallCtx struct {
	phase phase
	reg   *Registry // only while loading plugin.star

	// The rest is set for a CALL only.
	Plugin string
	Log    io.Writer // the call's log sink: log.* and, for a tool or event, print
	Ctx    context.Context
	// perms are the manifest's granted permissions; the capability modules of
	// the following changes read them here, never from the script.
	Perms *core.Permissions

	LookupEnv func(string) (string, bool)
	net       *netState
	// ext is the state the host's own modules keep for the call (Ext of the
	// CallSpec): the runtime never reads it.
	Ext any
}

func CtxOf(thread *starlark.Thread) *CallCtx {
	c, _ := thread.Local(ctxKey).(*CallCtx)
	return c
}

// loading reports whether thread is running plugin.star's LOAD.
func loading(thread *starlark.Thread) *CallCtx {
	if c := CtxOf(thread); c != nil && c.phase == phaseLoad {
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

func capabilityModules(settings *settingsValue) starlark.StringDict {
	return starlark.StringDict{"settings": settings, "log": logModule(), "http": httpModule(), "env": envModule()}
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
			c := CtxOf(thread)
			if c == nil || c.phase != phaseCall {
				return nil, fmt.Errorf("%s is not available while plugin.star loads: it only registers commands, tools and events", full)
			}
			prefix := "plugin " + c.Plugin + ": "
			if level != "info" {
				prefix += level + ": "
			}
			emit(c.Log, prefix, msg)
			return starlark.None, nil
		})
	}
	return m
}

// InCallPhase puts a bare thread in the CALL phase: the seam of tests that call
// a registered function outside Invoke, gated by the token only startest holds.
func InCallPhase(_ seam.Gate, t *starlark.Thread) { t.SetLocal(ctxKey, &CallCtx{phase: phaseCall}) }
