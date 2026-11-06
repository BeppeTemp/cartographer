package star

import (
	"fmt"
	"slices"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

// callPhase returns the context of a CALL, or why fn cannot run: while
// plugin.star loads no capability exists.
func callPhase(thread *starlark.Thread, fn string) (*callCtx, error) {
	c := ctxOf(thread)
	if c == nil || c.phase != phaseCall {
		return nil, fmt.Errorf("%s is not available while plugin.star loads: it only registers commands, tools and events", fn)
	}
	return c, nil
}

// denied builds the error of a capability the manifest does not grant. The text
// may hold script- or network-supplied data, so it is escaped here, once.
func denied(format string, a ...any) error {
	return &deniedError{msg: escapeLines(fmt.Sprintf(format, a...))}
}

func limited(format string, a ...any) error {
	return &limitError{msg: escapeLines(fmt.Sprintf(format, a...))}
}

func envModule() *starlarkstruct.Module {
	return &starlarkstruct.Module{Name: "env", Members: starlark.StringDict{
		"get": starlark.NewBuiltin("env.get", envGet),
	}}
}

// envGet returns a variable the manifest lists, or None when it is unset. There
// is no way to list the environment.
func envGet(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var name string
	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &name); err != nil {
		return nil, err
	}
	c, err := callPhase(thread, b.Name())
	if err != nil {
		return nil, err
	}
	if c.perms == nil || len(c.perms.Env) == 0 {
		return nil, denied("env.get needs permissions.env in the manifest")
	}
	if !slices.Contains(c.perms.Env, name) {
		return nil, denied("env.get: %q is not in permissions.env", name)
	}
	v, ok := c.lookupEnv(name)
	if !ok {
		return starlark.None, nil
	}
	return starlark.String(v), nil
}
