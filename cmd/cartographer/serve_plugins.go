package main

import (
	"fmt"
	"log"
	"sort"
	"strings"

	_ "github.com/BeppeTemp/cartographer/internal/atlasext" // registers the views/ and apps/ points of the atlas side
	"github.com/BeppeTemp/cartographer/internal/branding"
	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/plugin/star"
	_ "github.com/BeppeTemp/cartographer/internal/pluginserver" // registers the roles/ and presets/ families
)

// serverPresets are the presets of the pinned plugins, set once at start before
// any KB is mounted and handed to preset_list (a server's data, not a KB's).
var serverPresets []mcpserver.PresetEntry

// serverBranding is the branding of the pinned plugins, nil without one, set
// once at start. The Atlas mount reads it.
var serverBranding *branding.Active

// logServerBranding says the branding in one line, and that a disabled Atlas
// ignores it.
func logServerBranding(b *branding.Active, webEnabled bool) {
	if b == nil {
		return
	}
	var has []string
	if b.Palette != nil {
		has = append(has, "accent")
	}
	if b.Meta.LogoLight != "" {
		has = append(has, "logo")
	}
	if b.Meta.Favicon != "" {
		has = append(has, "favicon")
	}
	log.Printf("branding from plugin %s: %s", b.Plugin, strings.Join(has, ", "))
	if !webEnabled {
		log.Printf("branding: web.enabled is false, the branding is checked and not served")
	}
}

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

// atlasPlugins are the pinned plugins that have an atlas side, set once at
// start and handed to the Atlas (MultiKBServer.SetAtlasPlugins).
var atlasPlugins []*core.Plugin

// loadPinnedPlugins opens the plugins pinned in the server config through the
// read-only store at plugins_dir, once per side the Meridian serves: server
// (roles, presets) and atlas (views, apps). The digest in the config is the
// only consent: a tree that differs, a missing plugin, one with neither side or
// one this build cannot load makes the start fail, naming the plugin (never
// "loaded with a warning"). A pin needs one of the two sides, not both. It runs
// once at start and not on the auth reload: changing a pin takes a restart.
func loadPinnedPlugins(cfg *config.Config) (server, atlas []*core.Plugin, err error) {
	if len(cfg.Plugins) == 0 {
		return nil, nil, nil
	}
	pins := cfg.PluginPins()
	server, sp := core.LoadPinned(cfg.PluginsDir, pins, core.SideServer, core.Options{RunningVersion: version, Side: core.SideServer, CheckScripts: star.Check})
	atlas, ap := core.LoadPinned(cfg.PluginsDir, pins, core.SideAtlas, core.Options{RunningVersion: version, Side: core.SideAtlas})
	// "no <side> side" is LoadPinned's reason for a plugin that simply has no
	// such side; it is a problem only when the plugin has neither.
	noSide := func(side core.Side) string { return fmt.Sprintf("no %s side", side) }
	atlasReason := map[string]string{}
	for _, p := range ap {
		atlasReason[p.Name] = p.Reason
	}
	var probs []core.PinProblem
	seen := map[string]bool{}
	for _, p := range sp {
		if p.Reason == noSide(core.SideServer) {
			r, failed := atlasReason[p.Name]
			if !failed {
				continue // atlas only, and it loaded
			}
			if r == noSide(core.SideAtlas) {
				p.Reason = "neither a server nor an atlas side"
			} else {
				p.Reason = r
			}
		}
		seen[p.Name] = true
		probs = append(probs, p)
	}
	for _, p := range ap {
		if p.Reason != noSide(core.SideAtlas) && !seen[p.Name] {
			probs = append(probs, p)
		}
	}
	if len(probs) > 0 {
		sort.SliceStable(probs, func(i, j int) bool { return probs[i].Name < probs[j].Name })
		p := probs[0]
		err := fmt.Errorf("config: plugins.%s (line %d, plugins_dir %s): %s", p.Name, cfg.Plugins[p.Name].Line, cfg.PluginsDir, p.Reason)
		if len(probs) > 1 {
			err = fmt.Errorf("%w (and %d more)", err, len(probs)-1)
		}
		return nil, nil, err
	}
	return server, atlas, nil
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
