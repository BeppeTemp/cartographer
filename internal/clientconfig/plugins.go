package clientconfig

import (
	"fmt"
	"regexp"
	"sort"
)

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
