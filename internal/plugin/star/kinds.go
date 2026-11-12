package star

import (
	"fmt"
	"github.com/BeppeTemp/cartographer/internal/plugin/starcore"
	"regexp"
	"slices"
	"strings"

	"go.starlark.net/starlark"

	"github.com/BeppeTemp/cartographer/internal/plugin"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
)

// This file is the client half of the runtime: the three kinds of registration
// (command, tool, event) with the registrars that make them, the Program a
// client holds, and the client-only modules (files, kb) it adds. The runtime
// under it (load.go, call.go, register.go) knows no kind.

// Kind of a registration.
const (
	KindCommand = "command"
	KindTool    = "tool"
	KindEvent   = "event"
)

var (
	commandRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	toolRe    = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,47}$`)
	// events are the moments a plugin can hook; sync and connect run them in a
	// later change.
	events = []string{"sync.after", "connect.after"}
)

// registrars are the builtins only plugin.star has (a lib file is not given
// them, so plugin.star alone says what the plugin contributes) and that only
// work while it loads: an impl function that calls one later is refused.
func registrars() starlark.StringDict {
	return starlark.StringDict{
		"command": starlark.NewBuiltin("command", registerCommand),
		"tool":    starlark.NewBuiltin("tool", registerTool),
		"on":      starlark.NewBuiltin("on", registerEvent),
	}
}

// clientOptions are what a client adds to the runtime.
func clientOptions() starcore.Options {
	return starcore.Options{Registrars: registrars(), Extra: starlark.StringDict{"files": filesModule(), "kb": kbModule()}}
}

// Program is a loaded plugin script for a client: the runtime's script and the
// call of its command, tool and event registrations.
type Program struct {
	*starcore.Script
}

// Check is core.Options.CheckScripts: the problems of a tree's scripts.
func Check(side core.Side, files []core.TreeFile) []string {
	if side != core.SideClient {
		return nil // no other side has a script entry in this build
	}
	if _, err := Load(files); err != nil {
		return []string{err.Error()}
	}
	return nil
}

// Load runs plugin.star once, with lib/*.star loadable from it, and reports
// what it registered. It reads the files it is given and nothing else. A tree
// without plugin.star and without lib/ is not a script plugin: (nil, nil).
func Load(files []core.TreeFile) (*Program, error) {
	s, err := starcore.LoadScript(files, clientOptions())
	if s == nil || err != nil {
		return nil, err
	}
	return &Program{s}, nil
}

// Line is the registration as the preview and `plugin validate` print it,
// without its position: it is what two versions are compared on.
func Line(r Registration, plug string) string {
	switch r.Kind {
	case KindCommand:
		return fmt.Sprintf("command %s %s", plug, r.Name)
	case KindTool:
		s := "tool " + plugin.ToolName(plug, r.Name)
		if r.Writes {
			s += " (writes)"
		}
		return s
	}
	return "event " + r.Name
}

// Lines are the registrations in the order Sorted lists them.
func (p *Program) Lines(plug string) []string {
	regs := p.Sorted()
	out := make([]string, len(regs))
	for i, r := range regs {
		out[i] = Line(r, plug)
	}
	return out
}

func registerCommand(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	reg, pos, err := starcore.Registering(thread, "command")
	if err != nil {
		return nil, err
	}
	var name, help, usage string
	var impl starlark.Value
	if err := starlark.UnpackArgs("command", args, kwargs, "name", &name, "impl", &impl, "help", &help, "usage?", &usage); err != nil {
		return nil, err
	}
	if !commandRe.MatchString(name) || name == "help" {
		return nil, fmt.Errorf("command: name %q must match %s and not be help", name, commandRe)
	}
	fn, err := starcore.ImplFunc("command", impl)
	if err != nil {
		return nil, err
	}
	if err := starcore.CheckText("command", "help", help); err != nil {
		return nil, err
	}
	if err := starcore.CheckText("command", "usage", usage); err != nil {
		return nil, err
	}
	return starlark.None, reg.Add(starcore.Registration{Kind: KindCommand, Name: name, Help: help, Usage: usage, Pos: pos, Impl: fn}, "command "+name)
}

func registerTool(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	reg, pos, err := starcore.Registering(thread, "tool")
	if err != nil {
		return nil, err
	}
	var name, desc string
	var impl, params starlark.Value = nil, starlark.None
	var writes bool
	if err := starlark.UnpackArgs("tool", args, kwargs, "name", &name, "impl", &impl, "description", &desc, "params?", &params, "writes?", &writes); err != nil {
		return nil, err
	}
	if !toolRe.MatchString(name) {
		return nil, fmt.Errorf("tool: name %q must match %s", name, toolRe)
	}
	fn, err := starcore.ImplFunc("tool", impl)
	if err != nil {
		return nil, err
	}
	if err := starcore.CheckText("tool", "description", desc); err != nil {
		return nil, err
	}
	schema, err := starcore.ParamsSchema(thread, params)
	if err != nil {
		return nil, fmt.Errorf("tool %s: params: %w", name, err)
	}
	return starlark.None, reg.Add(starcore.Registration{Kind: KindTool, Name: name, Help: desc, Params: schema, Writes: writes, Pos: pos, Impl: fn}, "tool "+name)
}

func registerEvent(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	reg, pos, err := starcore.Registering(thread, "on")
	if err != nil {
		return nil, err
	}
	var event string
	var impl starlark.Value
	if err := starlark.UnpackArgs("on", args, kwargs, "event", &event, "impl", &impl); err != nil {
		return nil, err
	}
	if !slices.Contains(events, event) {
		return nil, fmt.Errorf("on: unknown event %q, expected one of %s", event, strings.Join(events, ", "))
	}
	fn, err := starcore.ImplFunc("on", impl)
	if err != nil {
		return nil, err
	}
	return starlark.None, reg.Add(starcore.Registration{Kind: KindEvent, Name: event, Pos: pos, Impl: fn}, "event "+event)
}
