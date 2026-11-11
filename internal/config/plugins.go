package config

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"gopkg.in/yaml.v3"
)

// PluginPin is one `plugins:` entry: the tree digest the operator approved, the
// server's only consent to run a plugin's server side (no client approval
// exists here). Settings are scalars; the use site types them by the manifest.
type PluginPin struct {
	Digest   string
	Enabled  bool
	Settings map[string]any
	// Line is the line of the entry in the file, for a refusal at start.
	Line int
}

// rawPluginPin mirrors one `plugins.<name>` entry. Enabled is a pointer: absent
// means true.
type rawPluginPin struct {
	Digest   string         `yaml:"digest"`
	Enabled  *bool          `yaml:"enabled"`
	Settings map[string]any `yaml:"settings"`
}

// pluginPinKeys are the keys of an entry; any other is refused, because a
// misspelled `digest` would otherwise pin nothing.
var pluginPinKeys = map[string]bool{"digest": true, "enabled": true, "settings": true}

// PluginPins is Plugins as the pins core.LoadPinned takes.
func (c *Config) PluginPins() map[string]core.Pin {
	out := make(map[string]core.Pin, len(c.Plugins))
	for n, p := range c.Plugins {
		out[n] = core.Pin{Enabled: p.Enabled, Digest: p.Digest}
	}
	return out
}

// checkPluginKeys refuses an unknown key under a plugins: entry (the map keys
// are plugin names, so the generic struct check does not apply).
func checkPluginKeys(data []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}
	keys, vals := mappingPairs(root)
	for i, k := range keys {
		if k.Value != "plugins" || vals[i].Kind != yaml.MappingNode {
			continue
		}
		nameKeys, entries := mappingPairs(vals[i])
		for j, nk := range nameKeys {
			if entries[j].Kind != yaml.MappingNode {
				return fmt.Errorf("plugins.%s (line %d): must be a mapping with digest", nk.Value, nk.Line)
			}
			ks, _ := mappingPairs(entries[j])
			for _, ek := range ks {
				if !pluginPinKeys[ek.Value] {
					return fmt.Errorf("unknown key %q under plugins.%s (line %d, column %d)", ek.Value, nk.Value, ek.Line, ek.Column)
				}
			}
		}
	}
	return nil
}

// pluginPins validates plugins_dir and plugins as far as the file alone allows
// (the tree is checked at start by core.LoadPinned, which refuses a digest
// that does not match). Fail closed: a malformed pin stops the load.
func pluginPins(dir string, raw map[string]rawPluginPin) (map[string]PluginPin, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if dir == "" {
		return nil, fmt.Errorf("plugins_dir: required when plugins is set: the read-only store the pinned trees are read from")
	}
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("plugins_dir %q: must be an absolute path (a server has no config-relative root)", dir)
	}
	names := make([]string, 0, len(raw))
	for n := range raw {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make(map[string]PluginPin, len(raw))
	for _, n := range names {
		r := raw[n]
		pin := PluginPin{Digest: r.Digest, Enabled: r.Enabled == nil || *r.Enabled, Settings: r.Settings}
		if err := core.CheckPin(n, core.Pin{Digest: pin.Digest}); err != nil {
			return nil, fmt.Errorf("plugins.%s: %w", n, err)
		}
		for k, v := range pin.Settings {
			switch v.(type) {
			case string, bool, int, int64, uint64, float64:
			default:
				return nil, fmt.Errorf("plugins.%s.settings.%s: must be a scalar (string, number or boolean)", n, k)
			}
		}
		out[n] = pin
	}
	return out, nil
}

// pluginLines is the line of every plugins.<name> key in the file.
func pluginLines(data []byte) map[string]int {
	out := map[string]int{}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return out
	}
	keys, vals := mappingPairs(doc.Content[0])
	for i, k := range keys {
		if k.Value == "plugins" && vals[i].Kind == yaml.MappingNode {
			names, _ := mappingPairs(vals[i])
			for _, n := range names {
				out[n.Value] = n.Line
			}
		}
	}
	return out
}
