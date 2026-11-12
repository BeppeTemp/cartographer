package surveyor

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/surveyor/recipe"
)

// addPluginPoints turns the recipes/ of every pinned plugin into
// ExecRecipes under their qualified names, so the registry, the
// poll and the unit executor need no second code path. It runs after the trees
// are verified and never runs a script: a recipe that does not answer spec or
// check is a `source test` failure, not a load error.
//
// TRAP: a plugin names no operator variable. The manifest declares the names its
// code sees (permissions.env); plugins.<name>.env maps each to one of the
// Surveyor's own variables. A mismatch either way stops the start, because a
// silently unmapped name would run the code without its credential and an
// undeclared mapping would hand the code a variable its approval never showed.
func (c *Config) addPluginPoints() error {
	for _, p := range c.Plugins {
		name := p.Manifest.Name
		pin := c.PluginPins[name]
		fail := func(format string, a ...any) error {
			return fmt.Errorf("plugins.%s (line %d): %s", name, pin.Line, fmt.Sprintf(format, a...))
		}
		var declared []string
		if perm := p.Manifest.Components[core.SideSurveyor].Permissions; perm != nil {
			declared = perm.Env
		}
		for _, n := range declared {
			if _, ok := pin.Env[n]; !ok {
				return fail("the manifest declares the variable %s: map it under plugins.%s.env to one of this Surveyor's variables", n, name)
			}
		}
		keys := make([]string, 0, len(pin.Env))
		for k := range pin.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !slices.Contains(declared, k) {
				return fail("env maps %s, which the plugin's manifest does not declare under permissions.env", k)
			}
		}
		root := filepath.Join(c.PluginsDir, name, pin.Digest)

		for _, rc := range p.Contributions["recipes"] {
			r := rc.Recipe
			q := recipe.Qualified(name, rc.Name)
			if !recipe.ValidQualified(q) {
				return fail("recipe %s: %q is not a valid qualified name (at most %d characters, no - at the edge of a part)", rc.Name, q, recipe.MaxQualifiedLen)
			}
			ec := recipe.ExecConfig{Name: q, Shape: r.Shape, Command: filepath.Join(root, "recipes", rc.Name, filepath.FromSlash(r.Command)), Args: r.Args,
				Env: maps.Clone(pin.Env), Connector: r.Connector, Instructions: r.Instructions, Settings: pin.Settings,
				Timeout: defaultExecTimeout, MaxOutput: defaultExecOutput, MaxContent: defaultExecContent}
			if r.Timeout > 0 {
				ec.Timeout = r.Timeout
			}
			if r.MaxOutput > 0 {
				ec.MaxOutput = r.MaxOutput
			}
			if r.MaxContent > 0 {
				ec.MaxContent = r.MaxContent
			}
			c.ExecRecipes = append(c.ExecRecipes, ec)
		}
	}
	return nil
}
