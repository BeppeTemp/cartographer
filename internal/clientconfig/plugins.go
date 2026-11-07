package clientconfig

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"

	"gopkg.in/yaml.v3"
)

// PluginProvidersComplete answers whether every provider the installed plugins
// declare is registered, so that a name the registry does not know is really
// unknown (D898). The default answers "unknown" as soon as a plugin is
// configured: a process that registered nothing (serve, a test) must not drop
// the agents, scopes and lock sections of a provider a plugin declares. The
// command replaces it at start, with the registration's own answer
// (registerPluginProviders). dropUnsupportedProviders and the lock-section
// drop skip everything while it says false, so a store that cannot be read
// drops nothing.
var PluginProvidersComplete = func(plugins map[string]PluginEntry) bool { return len(plugins) == 0 }

// LoadPlugins reads only the `plugins` key of dir's client config: a partial
// decode, no validation and no warnings, so the start of a command can register
// plugin providers before the full Load (which drops the names the registry does
// not know) without a second parse of everything. A missing file is no plugins.
func LoadPlugins(dir string) (map[string]PluginEntry, error) {
	data, err := os.ReadFile(Path(dir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("clientconfig: read %s: %w", Path(dir), err)
	}
	var doc struct {
		Plugins map[string]PluginEntry `yaml:"plugins"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("clientconfig: parse %s: %w", Path(dir), err)
	}
	return doc.Plugins, nil
}

// PluginEntry is one installed plugin of the `plugins` key. Only the plugin
// commands write it; a plugin's own files never do. Approved empty means the
// install is pending approval; the plugin is active only through plugin.State.
type PluginEntry struct {
	// Source is a git remote or an absolute folder, as plugin.ParseSource reads it.
	Source string `yaml:"source"`
	// Ref is the branch, tag or commit as typed; empty is the default branch.
	Ref string `yaml:"ref,omitempty"`
	// SHA is the commit Ref resolved to (40 hex); empty for a folder.
	SHA string `yaml:"sha,omitempty"`
	// Version is the manifest's, shown to the user.
	Version string `yaml:"version,omitempty"`
	// Installed and Approved are tree digests (64 hex). Installed is what was
	// fetched last, Approved what the user read and accepted.
	Installed string `yaml:"installed,omitempty"`
	Approved  string `yaml:"approved,omitempty"`
	Enabled   bool   `yaml:"enabled"`
	// Tools names the tools of the approved tree, written by plugin approve and
	// update from the load they already run for the preview. It only selects the
	// transport (Config.UsesBridge) and grants nothing: the bridge loads the real
	// plugin, so a stale or hand-edited value changes the entry type, never what runs.
	Tools []string `yaml:"tools,omitempty"`
	// Settings holds scalar values (string, bool, number) for the manifest's settings.
	Settings map[string]any `yaml:"settings,omitempty"`
}

// Same rule as the manifest's name (internal/plugin, which cannot be imported
// here); a test in that package keeps the two equal.
var (
	pluginNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,40}$`)
	sha1HexRe    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestHexRe  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Plugin returns the entry recorded for name.
func (c *Config) Plugin(name string) (PluginEntry, bool) {
	if c == nil {
		return PluginEntry{}, false
	}
	e, ok := c.Plugins[name]
	return e, ok
}

// SetPlugin records e under name after the same checks Load applies.
func (c *Config) SetPlugin(name string, e PluginEntry) error {
	if err := e.validate(name); err != nil {
		return err
	}
	if c.Plugins == nil {
		c.Plugins = map[string]PluginEntry{}
	}
	c.Plugins[name] = e
	return nil
}

// PluginProvidesTools reports whether an enabled, approved plugin lists tools
// (the hint of PluginEntry.Tools). Approved, not Installed: the approved tree
// is what runs while a newer one waits.
func (c *Config) PluginProvidesTools() bool {
	if c == nil {
		return false
	}
	for _, e := range c.Plugins {
		if e.Enabled && e.Approved != "" && len(e.Tools) > 0 {
			return true
		}
	}
	return false
}

// RemovePlugin drops name and reports whether it was there; the map goes once empty.
func (c *Config) RemovePlugin(name string) bool {
	if _, ok := c.Plugins[name]; !ok {
		return false
	}
	delete(c.Plugins, name)
	if len(c.Plugins) == 0 {
		c.Plugins = nil
	}
	return true
}

func (e PluginEntry) validate(name string) error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("plugins.%s: %s", name, fmt.Sprintf(format, a...))
	}
	switch {
	case !pluginNameRe.MatchString(name):
		return fmt.Errorf("plugins: %q is not a plugin name (%s)", name, pluginNameRe)
	case e.Source == "":
		return bad("source is empty")
	case e.SHA != "" && !sha1HexRe.MatchString(e.SHA):
		return bad("sha %q is not 40 hex characters", e.SHA)
	case e.Installed != "" && !digestHexRe.MatchString(e.Installed):
		return bad("installed %q is not a 64-hex digest", e.Installed)
	case e.Approved != "" && !digestHexRe.MatchString(e.Approved):
		return bad("approved %q is not a 64-hex digest", e.Approved)
	case e.Approved != "" && e.Installed == "":
		return bad("approved is set but installed is empty")
	}
	for k, v := range e.Settings {
		switch v.(type) {
		case string, bool, int, int64, uint64, float64:
		default:
			return bad("settings.%s must be a string, a bool or a number, not %T", k, v)
		}
	}
	return nil
}

// validatePlugins rejects an entry that fails the rules above, naming its key.
func (c *Config) validatePlugins(path string) error {
	names := make([]string, 0, len(c.Plugins))
	for n := range c.Plugins {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := c.Plugins[n].validate(n); err != nil {
			return fmt.Errorf("clientconfig: %s: %w", path, err)
		}
	}
	return nil
}
