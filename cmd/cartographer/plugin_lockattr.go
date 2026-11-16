package main

import (
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// pluginOfSection is the plugin a lock section of an unregistered provider
// belongs to, and whether that came from the section's own `plugin` record
// (recorded by our sync) or was inferred (#2136).
//
// A lockfile written before `Lock.Plugin` never gains it for a provider whose
// plugin is out of range or not fetched, since only a registered provider is
// attributed. The inference is deliberately narrow and answers "" (unattributed)
// whenever two plugins could claim the section:
//   - the provider is unknown to the registry this run (never a built-in);
//   - the plugin is the one every `plugin:<x>` source among the section's entries
//     names (a plugin's artifacts reach every provider, so this only says it was
//     applied here), or, when the entries name none (a plugin that ships only
//     providers), the only configured plugin whose approved tree cannot be read;
//   - that plugin's tree is unreadable or declares the provider, and no other
//     configured plugin has an unreadable tree (it could be the owner) or declares
//     the provider.
func pluginOfSection(cfg *clientconfig.Config, lock provisioning.Lock, provider string, store core.Store) (plugin string, inferred bool) {
	if lock.Plugin != "" {
		return lock.Plugin, false
	}
	if _, known := configurator.Lookup(configurator.Provider(provider)); known || cfg == nil {
		return "", false
	}
	o := pluginOpts()
	o.IgnoreRange = true
	var unreadable []string
	for name, e := range cfg.Plugins {
		if e.Approved == "" {
			continue
		}
		if _, err := store.Open(name, e.Approved, o); err != nil {
			unreadable = append(unreadable, name)
		}
	}
	p := ""
	for _, mf := range lock.Managed {
		x := provisioning.PluginName(mf.Source)
		if x == "" {
			continue
		}
		if p != "" && x != p {
			return "", false
		}
		p = x
	}
	if p == "" && len(unreadable) == 1 {
		p = unreadable[0]
	}
	if e, ok := cfg.Plugin(p); p == "" || !ok || e.Approved == "" {
		return "", false
	}
	for name, e := range cfg.Plugins {
		if e.Approved == "" {
			continue
		}
		t, err := store.Open(name, e.Approved, o)
		if err != nil {
			if name != p {
				return "", false // could be the owner: ambiguous
			}
			continue
		}
		declares := false
		for _, pn := range pluginProviderNames(t) {
			declares = declares || pn == provider
		}
		if declares != (name == p) {
			return "", false
		}
	}
	return p, true
}
