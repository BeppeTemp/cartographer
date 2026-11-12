package starcore

import (
	"fmt"
	"slices"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

// CallPhase returns the context of a CALL, or why fn cannot run: while
// plugin.star loads no capability exists.
func CallPhase(thread *starlark.Thread, fn string) (*CallCtx, error) {
	c := CtxOf(thread)
	if c == nil || c.phase != phaseCall {
		return nil, fmt.Errorf("%s is not available while plugin.star loads: it only registers commands, tools and events", fn)
	}
	return c, nil
}

// denied builds the error of a capability the manifest does not grant. The text
// may hold script- or network-supplied data, so it is escaped here, once.
func DeniedErr(format string, a ...any) error {
	return &deniedError{msg: EscapeLines(fmt.Sprintf(format, a...))}
}

func LimitErr(format string, a ...any) error {
	return &limitError{msg: EscapeLines(fmt.Sprintf(format, a...))}
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
	c, err := CallPhase(thread, b.Name())
	if err != nil {
		return nil, err
	}
	if c.Perms == nil || len(c.Perms.Env) == 0 {
		return nil, DeniedErr("env.get needs permissions.env in the manifest")
	}
	if !slices.Contains(c.Perms.Env, name) {
		return nil, DeniedErr("env.get: %q is not in permissions.env", name)
	}
	v, ok := c.LookupEnv(name)
	if !ok {
		return starlark.None, nil
	}
	return starlark.String(v), nil
}
