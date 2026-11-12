package surveyor

import (
	"fmt"
	"maps"
	"net/url"
	"path/filepath"
	"slices"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/surveyor/connector"
	"github.com/BeppeTemp/cartographer/internal/surveyor/recipe"
)

// addPluginPoints turns the recipes/ and connectors/ of every pinned plugin into
// ExecRecipes and Connectors under their qualified names, so the registry, the
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
		var declared, hosts []string
		if perm := p.Manifest.Components[core.SideSurveyor].Permissions; perm != nil {
			declared, hosts = perm.Env, perm.Net
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
		mapped := func(names ...string) (map[string]string, error) {
			out := map[string]string{}
			for _, n := range names {
				if !slices.Contains(declared, n) {
					return nil, fail("uses the variable %s, which the manifest does not declare under permissions.env", n)
				}
				out[n] = pin.Env[n]
			}
			return out, nil
		}

		local := map[string]bool{}
		for _, ct := range p.Contributions["connectors"] {
			k := ct.Connector
			q := recipe.Qualified(name, ct.Name)
			if !recipe.ValidQualified(q) {
				return fail("connector %s: %q is not a valid qualified name (at most %d characters, no - at the edge of a part)", ct.Name, q, recipe.MaxQualifiedLen)
			}
			local[ct.Name] = true
			cfg := connector.Config{Name: q, Transport: k.Transport, Args: k.Args, Tools: k.Tools, CallTimeout: defaultConnectorTimeout, MaxResultBytes: defaultConnectorResult}
			if k.CallTimeout > 0 {
				cfg.CallTimeout = k.CallTimeout
			}
			if k.MaxResult > 0 {
				cfg.MaxResultBytes = k.MaxResult
			}
			var err error
			if k.Transport == connector.TransportStdio {
				cfg.Command = k.Command
				if !k.CommandOnPath {
					cfg.Command = filepath.Join(root, "connectors", filepath.FromSlash(k.Command))
				}
				cfg.Env, err = mapped(k.Env...)
			} else {
				u, _ := url.Parse(k.URL) // judged by the loader
				if !recipe.HostAllowed(u.Hostname(), hosts) {
					return fail("connector %s: the host %s is not under permissions.net of the manifest", ct.Name, u.Hostname())
				}
				cfg.URL = k.URL
				var names []string
				for _, n := range k.Headers {
					names = append(names, n)
				}
				if k.TokenEnv != "" {
					names = append(names, k.TokenEnv)
				}
				var m map[string]string
				if m, err = mapped(names...); err == nil {
					cfg.TokenEnv = m[k.TokenEnv]
					cfg.Headers = map[string]string{}
					for h, n := range k.Headers {
						cfg.Headers[h] = m[n]
					}
				}
			}
			if err != nil {
				return err
			}
			c.Connectors = append(c.Connectors, cfg)
		}

		for _, rc := range p.Contributions["recipes"] {
			r := rc.Recipe
			q := recipe.Qualified(name, rc.Name)
			if !recipe.ValidQualified(q) {
				return fail("recipe %s: %q is not a valid qualified name (at most %d characters, no - at the edge of a part)", rc.Name, q, recipe.MaxQualifiedLen)
			}
			conn := r.Connector
			switch {
			case conn == "" || conn == "git" || conn == "fetch":
			case local[conn]:
				conn = recipe.Qualified(name, conn)
			default:
				return fail("recipe %s: connector %q is not a connector of this plugin (git, fetch or one under connectors/)", rc.Name, conn)
			}
			ec := recipe.ExecConfig{Name: q, Shape: r.Shape, Command: filepath.Join(root, "recipes", rc.Name, filepath.FromSlash(r.Command)), Args: r.Args,
				Env: maps.Clone(pin.Env), Connector: conn, Instructions: r.Instructions, Settings: pin.Settings,
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
	if err := connector.CheckAliases(c.Connectors); err != nil {
		return fmt.Errorf("plugins: %v", err)
	}
	return nil
}
