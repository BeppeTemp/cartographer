package main

import (
	"fmt"
	"log"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/plugin/star"
	_ "github.com/BeppeTemp/cartographer/internal/pluginserver" // registers the roles/ and presets/ families
)

// serverPresets are the presets of the pinned plugins, set once at start before
// any KB is mounted and handed to preset_list (a server's data, not a KB's).
var serverPresets []mcpserver.PresetEntry

// presetEntries lists the presets of the plugins as preset_list returns them.
func presetEntries(plugins []*core.Plugin) []mcpserver.PresetEntry {
	var out []mcpserver.PresetEntry
	for _, p := range plugins {
		for _, c := range p.Contributions["presets"] {
			e := mcpserver.PresetEntry{ID: p.Manifest.Name + "/" + c.Name, PluginVersion: p.Manifest.Version, Description: c.Preset.Description, Kind: c.Preset.Kind, Create: c.Preset.Create, Templates: []mcpserver.PresetTemplate{}}
			for _, t := range c.Preset.Templates {
				e.Templates = append(e.Templates, mcpserver.PresetTemplate{Slug: t.Slug, Path: "templates/" + t.Slug + ".md", Content: string(t.Content)})
			}
			out = append(out, e)
		}
	}
	return out
}

// loadServerPlugins opens the plugins pinned in the server config through the
// read-only store at plugins_dir. The digest in the config is the only consent:
// a tree that differs, a missing plugin, one without a server side or one this
// build cannot load makes the start fail, naming the plugin (never "loaded with
// a warning"). It runs once at start and not on the auth reload: changing a pin
// takes a restart.
func loadServerPlugins(cfg *config.Config) ([]*core.Plugin, error) {
	if len(cfg.Plugins) == 0 {
		return nil, nil
	}
	opts := core.Options{RunningVersion: version, Side: core.SideServer, CheckScripts: star.Check}
	plugins, probs := core.LoadPinned(cfg.PluginsDir, cfg.PluginPins(), core.SideServer, opts)
	if len(probs) > 0 {
		p := probs[0]
		err := fmt.Errorf("config: plugins.%s (line %d, plugins_dir %s): %s", p.Name, cfg.Plugins[p.Name].Line, cfg.PluginsDir, p.Reason)
		if len(probs) > 1 {
			err = fmt.Errorf("%w (and %d more)", err, len(probs)-1)
		}
		return nil, err
	}
	return plugins, nil
}

// logServerPlugins says one line per active plugin.
func logServerPlugins(plugins []*core.Plugin) {
	for _, p := range plugins {
		roles, presets := len(p.Contributions["roles"]), len(p.Contributions["presets"])
		d := p.Digest
		if len(d) > 12 {
			d = d[:12]
		}
		log.Printf("plugin %s %s %s: roles %d, presets %d", p.Manifest.Name, p.Manifest.Version, d, roles, presets)
	}
}
