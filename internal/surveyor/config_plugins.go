package surveyor

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
)

// rawPluginPin is one plugins.<name> entry. Enabled is a pointer: absent is true.
type rawPluginPin struct {
	Digest   string            `yaml:"digest"`
	Enabled  *bool             `yaml:"enabled"`
	Settings map[string]any    `yaml:"settings"`
	Env      map[string]string `yaml:"env"`
	// AllowPrivateHosts is the Surveyor's half of the private-network grant.
	AllowPrivateHosts []string `yaml:"allow_private_hosts"`
}

// PluginPin is a plugins.<name> entry: the tree digest the operator approved,
// the Surveyor's only consent to run the plugin's surveyor side.
type PluginPin struct {
	Digest   string
	Enabled  bool
	Settings map[string]any
	// Env maps each variable name the manifest's surveyor permissions.env
	// declares to the name of the Surveyor's own variable that holds its value;
	// a plugin never names an operator variable itself.
	Env map[string]string
	// AllowPrivateHosts are the hosts of the manifest's net_private this
	// Surveyor lets a Starlark recipe reach on a private or loopback
	// address. Each must be declared there (addPluginPoints checks).
	AllowPrivateHosts []string
	// Line is the line of the entry in the file, for a refusal at start.
	Line int
}

// plugins validates plugins_dir and plugins: the shape of every pin, malformed
// ones included when disabled, so a typo never hides behind `enabled: false`.
// The trees are read by LoadPlugins at start, once the binary's version is known.
func (v *validator) plugins() error {
	if len(v.raw.Plugins) == 0 {
		return nil
	}
	dir := v.raw.PluginsDir
	if dir == "" {
		return v.errf("plugins_dir", v.line("plugins"), "required when plugins is set: the read-only store the pinned trees are read from")
	}
	if !filepath.IsAbs(dir) && v.dir != "" {
		dir = filepath.Join(v.dir, dir)
	}
	v.c.PluginsDir = dir
	names := make([]string, 0, len(v.raw.Plugins))
	for n := range v.raw.Plugins {
		names = append(names, n)
	}
	sort.Strings(names)
	v.c.PluginPins = map[string]PluginPin{}
	for _, n := range names {
		r := v.raw.Plugins[n]
		at := "plugins." + n
		ln := v.line("plugins", n)
		if err := core.CheckPin(n, core.Pin{Digest: strings.Repeat("0", 64)}); err != nil {
			return v.errf(at, ln, "%v", err)
		}
		if err := core.CheckPin(n, core.Pin{Digest: r.Digest}); err != nil {
			dl := v.line("plugins", n, "digest")
			if dl == 0 {
				dl = ln
			}
			return v.errf(at+".digest", dl, "%v", err)
		}
		for k, val := range r.Settings {
			switch val.(type) {
			case string, bool, int, int64, uint64, float64:
			default:
				return v.errf(at+".settings."+k, v.line("plugins", n, "settings"), "must be a scalar (string, number or boolean)")
			}
		}
		if err := envNames(r.Env); err != nil {
			return v.errf(at+".env", v.line("plugins", n, "env"), "%v", err)
		}
		if err := privateHosts(r.AllowPrivateHosts); err != nil {
			return v.errf(at+".allow_private_hosts", v.line("plugins", n, "allow_private_hosts"), "%v", err)
		}
		v.c.PluginPins[n] = PluginPin{Digest: r.Digest, Enabled: r.Enabled == nil || *r.Enabled, Settings: r.Settings, Env: r.Env,
			AllowPrivateHosts: r.AllowPrivateHosts, Line: ln}
	}
	return nil
}

// maxPrivateHosts is the length cap of allow_private_hosts, the manifest's for net_private.
const maxPrivateHosts = 16

// privateHosts checks the shape of allow_private_hosts: exact host names in the
// manifest's grammar, no duplicate. Whether the manifest declares each is
// judged once the tree is open.
func privateHosts(hosts []string) error {
	if len(hosts) > maxPrivateHosts {
		return fmt.Errorf("%d hosts, at most %d", len(hosts), maxPrivateHosts)
	}
	seen := map[string]bool{}
	for _, h := range hosts {
		if why := core.PrivateHostProblem(h); why != "" {
			return fmt.Errorf("%q %s", h, why)
		}
		if seen[h] {
			return fmt.Errorf("%q is listed twice", h)
		}
		seen[h] = true
	}
	return nil
}

// LoadPlugins opens the enabled pins through the read-only store at PluginsDir
// and keeps in Plugins those that have a surveyor side. Any enabled pin that
// does not load (a different digest, a missing or modified tree, no surveyor
// side, a side this build cannot load) is an error naming the key path and
// line: the Surveyor does not start (fail closed). opts carries the binary's
// version and script check; Side is set here.
func (c *Config) LoadPlugins(opts core.Options) error {
	if len(c.PluginPins) == 0 {
		return nil
	}
	pins := make(map[string]core.Pin, len(c.PluginPins))
	for n, p := range c.PluginPins {
		pins[n] = core.Pin{Enabled: p.Enabled, Digest: p.Digest}
	}
	opts.Side = core.SideSurveyor
	got, probs := core.LoadPinned(c.PluginsDir, pins, core.SideSurveyor, opts)
	if len(probs) > 0 {
		p := probs[0]
		err := fmt.Errorf("plugins.%s (line %d): %s", p.Name, c.PluginPins[p.Name].Line, p.Reason)
		if len(probs) > 1 {
			err = fmt.Errorf("%w (and %d more)", err, len(probs)-1)
		}
		return err
	}
	c.Plugins = got
	return c.addPluginPoints()
}
