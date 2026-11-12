package config

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/hooks"
	"gopkg.in/yaml.v3"
)

// rawHook mirrors one `hooks:` entry. The durations are strings and the numbers
// pointers so an absent key keeps its default and an explicit bad one is judged.
type rawHook struct {
	Name          string   `yaml:"name"`
	Point         string   `yaml:"point"`
	Plugin        string   `yaml:"plugin"`
	URL           string   `yaml:"url"`
	Credential    string   `yaml:"credential"`
	Timeout       string   `yaml:"timeout"`
	FailurePolicy string   `yaml:"failure_policy"`
	Mode          string   `yaml:"mode"`
	KBs           []string `yaml:"kbs"`
	BatchSize     *int     `yaml:"batch_size"`
	Blocking      *bool    `yaml:"blocking"`
	CacheTTL      string   `yaml:"cache_ttl"`
	Headers       []string `yaml:"headers"`
}

// checkHookKeys refuses an unknown key under a hooks: entry: a misspelled
// failure_policy or mode would otherwise be dropped. It follows checkAuthKeys.
func checkHookKeys(data []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	for root.Kind == yaml.AliasNode && root.Alias != nil {
		root = root.Alias
	}
	if root.Kind != yaml.MappingNode {
		return nil
	}
	keys, vals := mappingPairs(root)
	for i, k := range keys {
		if k.Value == "hooks" {
			return checkKnownKeys(vals[i], reflect.TypeOf([]rawHook{}), "hooks")
		}
	}
	return nil
}

// looksLikeHooks reports whether an unknown top-level key is probably a
// misspelling of hooks (one edit away, or naming a hook; two edits would catch
// the retired top-level `tools`): left alone a `hook:` list would load silently and the
// veto the operator wrote would not exist.
func looksLikeHooks(key string) bool {
	k := strings.ToLower(key)
	return strings.Contains(k, "hook") || editDistance(k, "hooks") <= 1
}

func duration(field, s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%s %q: %v", field, s, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s %q: want a positive duration", field, s)
	}
	return d, nil
}

// hookSpecs validates the hooks list as far as the file alone allows. Whether
// the plugin is approved and the credential readable is the registry's, at
// start (hooks.NewRegistry); that the plugin is pinned in plugins is checked
// here by pluginsPinned.
func hookSpecs(raw []rawHook) ([]hooks.Hook, error) {
	var out []hooks.Hook
	for i, r := range raw {
		h := hooks.Hook{Name: r.Name, Point: r.Point, Plugin: r.Plugin, URL: r.URL, Credential: r.Credential,
			FailurePolicy: r.FailurePolicy, Mode: r.Mode, KBs: r.KBs, Headers: r.Headers}
		var err error
		if h.Timeout, err = duration("timeout", r.Timeout); err != nil {
			return nil, fmt.Errorf("hooks[%d] (%s): %w", i, r.Name, err)
		}
		if h.CacheTTL, err = duration("cache_ttl", r.CacheTTL); err != nil {
			return nil, fmt.Errorf("hooks[%d] (%s): %w", i, r.Name, err)
		}
		if r.BatchSize != nil {
			if *r.BatchSize < 1 {
				return nil, fmt.Errorf("hooks[%d] (%s): batch_size %d: want 1 to 1000", i, r.Name, *r.BatchSize)
			}
			h.BatchSize = *r.BatchSize
		}
		h.Blocking = r.Blocking != nil && *r.Blocking
		out = append(out, h)
	}
	if err := hooks.NormalizeAll(out); err != nil {
		return nil, err
	}
	return out, nil
}

// PluginTokensPinned refuses a token bound to a plugin the server does not pin
// (enabled) in plugins: a service identity is only for an approved plugin. It
// runs on the EFFECTIVE token set (after the environment and flag overrides),
// not in Load, so no source of tokens can bypass it.
func PluginTokensPinned(tokens []TokenSpec, pins map[string]PluginPin) error {
	for i, t := range tokens {
		if t.Plugin == "" {
			continue
		}
		if p, ok := pins[t.Plugin]; !ok || !p.Enabled {
			return fmt.Errorf("token %d: plugin %q is not pinned (and enabled) in plugins", i, t.Plugin)
		}
	}
	return nil
}

// DropUnpinnedPluginTokens is the reload form: the tokens bound to a plugin that
// is no longer pinned (and enabled) are left out and named in dropped, so the
// edit that unpins a plugin revokes its token without blocking the rest of the
// file. Startup refuses instead (PluginTokensPinned).
func DropUnpinnedPluginTokens(tokens []TokenSpec, pins map[string]PluginPin) (kept []TokenSpec, dropped []string) {
	for _, t := range tokens {
		if p, ok := pins[t.Plugin]; t.Plugin != "" && (!ok || !p.Enabled) {
			id := t.ID
			if id == "" {
				id = "(no id)"
			}
			dropped = append(dropped, id+" (plugin "+t.Plugin+")")
			continue
		}
		kept = append(kept, t)
	}
	return kept, dropped
}

// pluginsPinned refuses a hook whose plugin the server does not pin (enabled)
// in plugins: the pin is the consent, and without it there is no approved
// plugin to serve the point.
func pluginsPinned(hs []hooks.Hook, pins map[string]PluginPin) error {
	for i, h := range hs {
		if p, ok := pins[h.Plugin]; !ok || !p.Enabled {
			return fmt.Errorf("hooks[%d] (%s): plugin %q is not pinned (and enabled) in plugins", i, h.Name, h.Plugin)
		}
	}
	return nil
}
