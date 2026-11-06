package main

import (
	"fmt"
	"io"
	"slices"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/plugin"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// registerPluginProviders registers the providers of the approved plugin trees
// with the registry and the destination cells (D-new-plugin-providers). It runs
// in main before any command loads the client config: that load drops every
// provider name the registry does not know (D898), so a provider registered later
// would be deleted from the agents, the scopes and the lock sections.
//
// It reads the `plugins` key alone (clientconfig.LoadPlugins), opens the tree of
// each approved digest from the store (no git, no network, no lock) and
// registers the providers of every tree that opens and validates, active when
// the plugin is enabled; a disabled plugin's providers stay registered inactive,
// so what they installed can be retired. A plugin whose approved tree cannot be
// used registers nothing and makes the registration incomplete; so does a
// provider the registries refuse (a name two plugins claim, a colliding cell).
// While it is incomplete clientconfig drops nothing (PluginProvidersComplete):
// an unreadable store never costs the user a provider. It runs once per
// process, at start; calling it again replaces the whole registered set, which a
// command that changes a plugin's state does to re-register after the change.
func registerPluginProviders(errw io.Writer) {
	dir, err := clientconfig.TargetDir()
	if err != nil {
		return
	}
	plugins, err := clientconfig.LoadPlugins(dir)
	if err != nil {
		// The command reports the config itself; until then nothing is known.
		clientconfig.PluginProvidersComplete = func(map[string]clientconfig.PluginEntry) bool { return false }
		return
	}
	if len(plugins) == 0 {
		// The common case costs the one partial read above and registers nothing.
		_, _ = configurator.ReplacePlugins(nil)
		provisioning.ReplacePluginCells(nil, dir)
		clientconfig.PluginProvidersComplete = func(map[string]clientconfig.PluginEntry) bool { return true }
		return
	}
	complete := true
	var specs []configurator.PluginSpec
	var cells []provisioning.PluginCells
	names := make([]string, 0, len(plugins))
	for n := range plugins {
		names = append(names, n)
	}
	sort.Strings(names)
	store, storeErr := pluginStoreFn()
	env := plugin.DefaultProviderEnv()
	env.Home = dir
	for _, n := range names {
		e := plugins[n]
		if e.Approved == "" {
			continue // pending approval: nothing to register, nothing to protect
		}
		if storeErr != nil {
			complete = false
			continue
		}
		var tree *plugin.Plugin
		st, p := plugin.State(n, e, store, pluginOpts())
		switch st {
		case plugin.StatusActive:
			tree = p
		case plugin.StatusDisabled:
			// State does not open a disabled plugin; its providers are still
			// needed, inactive, to retire what they installed.
			if t, err := store.Open(n, e.Approved, pluginOpts()); err == nil {
				tree = &t
			}
		}
		if tree == nil {
			complete = false
			fmt.Fprintf(errw, "warning: plugin %s is %s: its providers are not registered this run\n", escapeControl(n), st)
			continue
		}
		for _, r := range tree.Providers(env) {
			if err := configurator.CheckPlugin(r.Registration(n, e.Enabled)); err != nil {
				complete = false
				fmt.Fprintf(errw, "warning: plugin %s: %s\n", escapeControl(n), escapeControl(err.Error()))
				continue
			}
			specs = append(specs, r.Registration(n, e.Enabled))
			cells = append(cells, r.Cells(e.Enabled))
		}
	}
	// Cells first: a provider whose cells are refused is not registered, so the
	// registry never holds a provider the destination lookups do not know.
	notes, refusedCells := provisioning.ReplacePluginCells(cells, dir)
	for _, w := range notes {
		fmt.Fprintln(errw, "warning:", escapeControl(w))
	}
	drop := map[string]bool{}
	for _, r := range refusedCells {
		complete = false
		drop[r.Name] = true
		fmt.Fprintf(errw, "warning: plugin provider %s is not registered: %s\n", escapeControl(r.Name), escapeControl(r.Err.Error()))
	}
	specs = slices.DeleteFunc(specs, func(s configurator.PluginSpec) bool { return drop[s.Name] })
	refused, err := configurator.ReplacePlugins(specs)
	if err != nil {
		complete = false
		fmt.Fprintln(errw, "warning: plugin providers are not registered:", escapeControl(err.Error()))
	}
	for _, name := range refused {
		complete = false
		fmt.Fprintf(errw, "warning: plugin provider %s is declared by two plugins: neither is registered\n", escapeControl(name))
	}
	// A claimed name lost in the registry must not keep cells either.
	for _, name := range refused {
		provisioning.UnregisterPluginCells(name)
	}
	clientconfig.PluginProvidersComplete = func(map[string]clientconfig.PluginEntry) bool { return complete }
}

// retiredPluginProviders names the registered providers of a plugin that is no
// longer active and that this client still has connected (an agents entry or a
// lock section): sync disconnects them before it computes the targets. A plugin
// whose tree cannot be read registered nothing, so it retires nothing.
func retiredPluginProviders(cfg *clientconfig.Config, lf provisioning.LockFile) []string {
	var out []string
	for _, rp := range configurator.RegisteredPlugins() {
		if rp.Spec.Active {
			continue
		}
		name := rp.Spec.Name
		_, locked := lf.Providers[name]
		for _, w := range lf.Workspaces {
			if _, ok := w.Providers[name]; ok {
				locked = true
			}
		}
		if slices.Contains(cfg.Agents, name) || locked {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// retirePluginProviders disconnects, under the client lock the caller holds and
// before the targets are used, every provider of a disabled plugin that is still
// connected: its MCP entry, its managed files and its lock section go
// (doDisconnectLocked). It returns the targets without them. The provider stays
// registered inactive until the plugin is removed or enabled again. A
// refused prune keeps the provider connected and is reported, as a disconnect
// does. cfg is updated in memory to what the disconnect saved.
func retirePluginProviders(st syncStreams, dir string, cfg *clientconfig.Config, targets []string, dryRun bool) []string {
	lf, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		return targets // a broken lockfile is reported by the code that needs it
	}
	retired := retiredPluginProviders(cfg, lf)
	if len(retired) == 0 {
		return targets
	}
	res, err := doDisconnectLocked(disconnectOptions{Providers: retired, Dir: dir, DryRun: dryRun, KeepToken: true})
	if err != nil {
		fmt.Fprintf(st.err(), "warning: could not retire the providers of a disabled plugin: %v\n", err)
		return targets
	}
	prefix := ""
	if dryRun {
		prefix = "[dry-run] "
	}
	var gone []string
	for _, pr := range res.Providers {
		if len(pr.Refused) > 0 {
			for _, w := range pr.Refused {
				fmt.Fprintf(st.err(), "warning: [%s] kept connected: %s\n", escapeControl(pr.Provider), escapeControl(w))
			}
			continue
		}
		fmt.Fprintf(st.out(), "%s[%s] retired: its plugin is disabled\n", prefix, escapeControl(pr.Provider))
		gone = append(gone, pr.Provider)
	}
	if !dryRun {
		cfg.Agents = removeStrings(cfg.Agents, gone)
		for _, p := range gone {
			delete(cfg.Workspaces, p)
			delete(cfg.Scopes, p)
		}
	}
	return removeStrings(targets, gone)
}
