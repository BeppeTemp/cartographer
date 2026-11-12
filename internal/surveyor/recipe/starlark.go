package recipe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/plugin/starcore"
	"github.com/BeppeTemp/cartographer/internal/plugin/starcore/recipes"
	"github.com/BeppeTemp/cartographer/internal/surveyor/connector"
)

// StarConfig is one Starlark recipe a pinned plugin registers in surveyor.star.
// The ExecConfig carries what both kinds share (the qualified name, shape,
// env mapping, limits, instructions and settings; Command and Args are not
// used): a Starlark recipe is judged by the same parsers as an exec one.
type StarConfig struct {
	ExecConfig
	Program *recipes.Program
	Local   string // the name the script registered it under
	Plugin  string
	// Version is sent in http.request's User-Agent.
	Version     string
	Permissions *core.Permissions       // the manifest's, surveyor side
	Declared    map[string]core.Setting // the manifest's settings
	// PrivateHosts are plugins.<name>.allow_private_hosts, each declared by the
	// manifest's net_private (the loader checks): the two keys of the grant.
	PrivateHosts []string
	// Tune sets the runtime's test seams on a call. Only the plugin test kit
	// (recipetest, for the recorded replay) and tests set it; the Surveyor never.
	Tune func(*recipes.Request)
}

// NewStarlark returns the recipe that calls a verb of a Starlark recipe. It
// shares the exec recipes' slot (one script at a time per replica: the runtime's
// heap watchdog is process-wide) and their redaction of the mapped variables.
func NewStarlark(c StarConfig) Recipe {
	e := &execRecipe{c: c.ExecConfig}
	e.inproc = func(ctx context.Context, body []byte) ([]byte, error) {
		var head struct {
			Verb string `json:"verb"`
		}
		_ = json.Unmarshal(body, &head)
		req := recipes.Request{
			Recipe: c.Local, Verb: head.Verb, Body: body, Plugin: c.Plugin, Version: c.Version,
			Permissions: c.Permissions, Declared: c.Declared, Values: c.Settings, PrivateHosts: c.PrivateHosts,
			// env.get reads only the names the manifest declares (the runtime
			// checks) and, through this mapping, only the Surveyor variables
			// the operator mapped to them.
			LookupEnv: func(name string) (string, bool) {
				v, ok := c.Env[name]
				if !ok {
					return "", false
				}
				// runInProc has already refused an unset or empty mapped variable.
				val, err := connector.EnvValue(v)
				return val, err == nil
			},
		}
		if c.Tune != nil {
			c.Tune(&req)
		}
		out, err := c.Program.Call(ctx, req)
		if errors.Is(err, recipes.ErrNotDeclared) {
			return nil, ErrUnsupported
		}
		return out, err
	}
	return e
}

// runInProc is runScript for a Starlark recipe. A failure is described with the
// mapped variables redacted, as a child's stderr is.
func (e *execRecipe) runInProc(ctx context.Context, fc string, body []byte, read func(io.Reader) (bool, error)) error {
	// As ChildEnv does for a child: every mapped variable must be set and
	// non-empty, or the verb fails naming the variable (never a value).
	if _, err := connector.ChildEnv(e.c.Env); err != nil {
		return fail(fc, "%v", err)
	}
	runCtx, cancel := context.WithTimeout(ctx, e.c.Timeout)
	defer cancel()
	out, err := e.inproc(runCtx, body)
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case runCtx.Err() != nil:
		return fail(fc, "timeout after %s", e.c.Timeout)
	case errors.Is(err, ErrUnsupported):
		return ErrUnsupported
	case err != nil:
		msg := err.Error()
		var ce *starcore.CallError
		if errors.As(err, &ce) && ce.Kind != starcore.Failed {
			msg = ce.Kind.String() + ": " + ce.Msg
		}
		return fail(fc, "%s", strings.TrimPrefix(e.excerpt(msg), ": "))
	}
	_, err = read(bytes.NewReader(out))
	return err
}
