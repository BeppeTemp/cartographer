package main

import (
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/plugin"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

// registerPluginProviders registers the providers of the approved plugin trees
// with the registry and the destination cells (D1738). It runs
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
func registerPluginProviders(errw io.Writer) { registerPluginProvidersIgnoring(errw, "") }

// registerPluginProvidersIgnoring is registerPluginProviders for a command that
// must act on one plugin whose `cartographer` range the running version does not
// meet (plugin remove, plugin show): that plugin's tree is read without the range
// check and its providers are registered inactive, as for a disabled plugin, so
// the disconnect path can retire them. It never makes the plugin run.
func registerPluginProvidersIgnoring(errw io.Writer, ignoreRange string) {
	pluginProviderIssues = nil
	unregisteredWhy = nil
	dir, err := clientconfig.TargetDir()
	if err != nil {
		return
	}
	// The allow set is needed before resolution: the resolver refuses a link
	// the user listed otherwise. Load cannot run yet (D898), so the light reader
	// fills it; registerAllowedSymlinks replaces it with the validated set.
	// It holds for this registration only: the set in place is restored on the
	// way out, since a re-registration after a plugin change runs in a process
	// whose full Load already decided it.
	if paths, err := clientconfig.LoadAllowedSymlinks(dir); err == nil {
		saved := safewrite.AllowedLinks()
		safewrite.SetAllowedLinks(paths)
		defer safewrite.SetAllowedLinks(saved)
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
		var tree *core.Plugin
		enabled := e.Enabled
		st, p := core.State(n, e, store, pluginOpts())
		switch st {
		case core.StatusActive:
			tree = p
		case core.StatusDisabled:
			// State does not open a disabled plugin; its providers are still
			// needed, inactive, to retire what they installed.
			if t, err := store.Open(n, e.Approved, pluginOpts()); err == nil {
				tree = &t
			}
		case core.StatusInvalid:
			if n == ignoreRange {
				o := pluginOpts()
				o.IgnoreRange = true
				if t, err := store.Open(n, e.Approved, o); err == nil {
					tree, enabled = &t, false // read to be retired or shown, never run
				}
			}
		}
		if tree == nil {
			noteUnregistered(n, e, st, store)
			complete = false
			problem, fix := invalidReason(n, e, st, store)
			fmt.Fprintf(errw, "warning: plugin %s is %s (%s; %s): its providers are not registered this run\n", escapeControl(n), st, escapeControl(problem), escapeControl(fix))
			continue
		}
		for _, r := range tree.Providers(env) {
			if enabled {
				// A cell left out does not make the registration incomplete: the
				// provider is registered without it. It is said by sync, connect
				// and status, and by doctor, never at process start.
				for _, why := range r.LeftOut {
					pluginProviderIssues = append(pluginProviderIssues, pluginProviderIssue{Plugin: n, Provider: r.Registration(n, true).Name, Reason: why, LeftOut: true})
				}
			}
			if err := configurator.CheckPlugin(r.Registration(n, enabled)); err != nil {
				complete = false
				fmt.Fprintf(errw, "warning: plugin %s: %s\n", escapeControl(n), escapeControl(err.Error()))
				pluginProviderIssues = append(pluginProviderIssues, pluginProviderIssue{Plugin: n, Provider: r.Registration(n, enabled).Name, Reason: err.Error()})
				continue
			}
			specs = append(specs, r.Registration(n, enabled))
			cells = append(cells, plugin.ProviderCells(r, enabled))
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
		pluginProviderIssues = append(pluginProviderIssues, pluginProviderIssue{Provider: r.Name, Reason: r.Err.Error()})
		fmt.Fprintf(errw, "warning: plugin provider %s is not registered: %s\n", escapeControl(r.Name), escapeControl(r.Err.Error()))
	}
	specs = slices.DeleteFunc(specs, func(s configurator.PluginSpec) bool { return drop[s.Name] })
	refused, err := configurator.ReplacePlugins(specs)
	if err != nil {
		complete = false
		fmt.Fprintln(errw, "warning: plugin providers are not registered:", escapeControl(err.Error()))
		pluginProviderIssues = append(pluginProviderIssues, pluginProviderIssue{Reason: err.Error()})
	}
	for _, name := range refused {
		complete = false
		fmt.Fprintf(errw, "warning: plugin provider %s is declared by two plugins: neither is registered\n", escapeControl(name))
		pluginProviderIssues = append(pluginProviderIssues, pluginProviderIssue{Provider: name, Reason: "declared by two plugins: neither is registered"})
	}
	// A claimed name lost in the registry must not keep cells either.
	for _, name := range refused {
		provisioning.UnregisterPluginCells(name)
	}
	clientconfig.PluginProvidersComplete = func(map[string]clientconfig.PluginEntry) bool { return complete }
}

// invalidReason is why a plugin that is not active reads as it does and what
// fixes it: the manifest's own message when its approved tree does not open (for
// an unmet range, the range, the running version and the release candidate
// note), else the state, and the commands that fix it. The start-up warning,
// `plugin show` and the reason sync gives for a skipped provider all say this.
func invalidReason(name string, e clientconfig.PluginEntry, st core.Status, store core.Store) (problem, fix string) {
	problem = string(st)
	digest := e.Approved
	if digest == "" {
		digest = e.Installed
	}
	if _, err := store.Open(name, digest, pluginOpts()); err != nil {
		problem = strings.TrimPrefix(err.Error(), core.ErrInvalid.Error()+": ")
	}
	fix = fmt.Sprintf("run `cartographer plugin update %s` and approve it, or `cartographer plugin remove %s`", name, name)
	return problem, fix
}

// unregisteredWhy is, per provider name, why the last registration left it out
// when it could still tell which plugin declares it (the tree reads, only its
// state does not allow it to run). unregisteredReason reads it.
var unregisteredWhy map[string]string

// noteUnregistered records, for each provider the approved tree of a plugin that
// is not active declares, the reason it is not registered: the plugin, what is
// wrong with it (for an unmet range, the range and the running version) and the
// command that fixes it. A tree that cannot be read at all names no provider.
func noteUnregistered(name string, e clientconfig.PluginEntry, st core.Status, store core.Store) {
	o := pluginOpts()
	o.IgnoreRange = true
	t, err := store.Open(name, e.Approved, o)
	if err != nil {
		return
	}
	problem, fix := invalidReason(name, e, st, store)
	why := fmt.Sprintf("plugin %s is %s (%s): %s", name, st, problem, fix)
	for _, pn := range pluginProviderNames(t) {
		if unregisteredWhy == nil {
			unregisteredWhy = map[string]string{}
		}
		unregisteredWhy[pn] = why
	}
}

// unregisteredReason is "" for a provider the registry knows. For one it does
// not (a plugin that is invalid for the running version, not fetched, modified
// or removed), it is the reason, with the command that fixes it: sync skips such
// a provider with this text, and the checks that name a provider reuse it.
func unregisteredReason(provider string) string {
	if _, ok := configurator.Lookup(configurator.Provider(provider)); ok {
		return ""
	}
	if why, ok := unregisteredWhy[provider]; ok {
		return why
	}
	return "no plugin registers it this run (see the warnings above, and `cartographer plugin list`)"
}

// pluginProviderIssue is one provider a registration refused, kept for doctor
// (check plugin-provider): the warning on stderr scrolls away, the reason stays.
type pluginProviderIssue struct {
	Plugin   string // empty when the registry cannot say which plugin
	Provider string
	Reason   string
	// LeftOut marks a path the provider lost at resolution (a cell behind a
	// link): the provider is registered without it.
	LeftOut bool
}

// pluginProviderIssues is what the last registerPluginProviders refused. Like
// the registry it describes, it is replaced by each registration.
var pluginProviderIssues []pluginProviderIssue

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

// printLeftOutCells says, once per run, every cell a registered provider lost at
// resolution (a symlink not listed under allow_symlinks, a path outside its
// root), one line per reason. Only the commands that report on providers call
// it: registration also runs for serve and the bridge, which must stay quiet.
func printLeftOutCells(w io.Writer) {
	for _, is := range pluginProviderIssues {
		if is.LeftOut {
			fmt.Fprintf(w, "[%s] warning: cell left out: %s\n", escapeControl(is.Provider), escapeControl(is.Reason))
		}
	}
}
