package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/plugin"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// checkPlugins is the doctor section for the user's `plugins` key: the store,
// invalid trees, versions waiting for approval, name collisions and providers a
// registration refused. It reads the store and the lockfile and writes nothing,
// and it reports nothing at all when no plugin is configured. Every string that
// comes from a plugin, its source or the config is escaped: a finding is printed
// to a terminal. lock is the lockfile when it could be read (collisions are then
// also judged against the KB and bundled files it records).
//
// With only set (--provider) the run is narrowed to that provider: only the
// refusals that concern it are reported.
func checkPlugins(cfg *clientconfig.Config, only string, lock *provisioning.LockFile) []doctorFinding {
	if cfg == nil || len(cfg.Plugins) == 0 {
		return nil
	}
	if only != "" {
		var out []doctorFinding
		for _, is := range pluginProviderIssues {
			if is.Provider == only {
				out = append(out, providerIssueFinding(is))
			}
		}
		return out
	}
	names := make([]string, 0, len(cfg.Plugins))
	for n := range cfg.Plugins {
		names = append(names, n)
	}
	sort.Strings(names)
	store, storeErr := pluginStoreFn()
	var out []doctorFinding
	if storeErr != nil {
		out = append(out, doctorFinding{
			Check: "plugin-store", Severity: doctorError,
			Message: "the plugin store cannot be used: " + escapeControl(storeErr.Error()),
			Fix:     "make the user cache directory usable, then run cartographer sync",
		})
	}
	for _, n := range names {
		out = append(out, checkOnePlugin(n, cfg.Plugins[n], store, storeErr)...)
	}
	out = append(out, checkPluginCollisions(cfg, lock)...)
	for _, is := range pluginProviderIssues {
		out = append(out, providerIssueFinding(is))
	}
	return out
}

// checkOnePlugin judges the approved tree of an enabled plugin and the installed
// one of any plugin. A disabled plugin is a state, not a finding, and its approved
// tree is not read.
func checkOnePlugin(name string, e clientconfig.PluginEntry, store plugin.Store, storeErr error) []doctorFinding {
	var out []doctorFinding
	n := escapeControl(name)
	update := "cartographer plugin update " + n
	if e.Enabled && e.Approved != "" && storeErr == nil {
		_, err := store.Open(name, e.Approved, pluginOpts())
		switch {
		case err == nil:
		case errors.Is(err, plugin.ErrNotFetched):
			out = append(out, doctorFinding{Check: "plugin-store", Severity: doctorWarning,
				Message: fmt.Sprintf("plugin %s is approved but its tree is not in the store: nothing is installed from it and what it installed stays", n),
				Fix:     update})
		case errors.Is(err, plugin.ErrInvalid):
			out = append(out, doctorFinding{Check: "plugin-invalid", Severity: doctorError,
				Message: fmt.Sprintf("plugin %s: the approved tree is not valid: %s", n, escapeControl(err.Error())),
				Fix:     update})
		default:
			out = append(out, doctorFinding{Check: "plugin-store", Severity: doctorWarning,
				Message: fmt.Sprintf("plugin %s: the stored tree is modified or unreadable: %s", n, escapeControl(err.Error())),
				Fix:     update})
		}
	}
	if e.Installed != "" && e.Installed != e.Approved && storeErr == nil {
		// A pending or newer tree that does not validate cannot be approved.
		if _, err := store.Open(name, e.Installed, pluginOpts()); errors.Is(err, plugin.ErrInvalid) {
			out = append(out, doctorFinding{Check: "plugin-invalid", Severity: doctorWarning,
				Message: fmt.Sprintf("plugin %s: the installed version cannot be approved: %s", n, escapeControl(err.Error())),
				Fix:     update})
		}
	}
	switch {
	case e.Approved == "":
		out = append(out, doctorFinding{Check: "plugin-pending", Severity: doctorWarning,
			Message: fmt.Sprintf("plugin %s has never been approved: it installs nothing", n),
			Fix:     fmt.Sprintf("cartographer plugin show %s, then cartographer plugin approve %s", n, n)})
	case e.Installed != e.Approved:
		out = append(out, doctorFinding{Check: "plugin-pending", Severity: doctorInfo,
			Message: fmt.Sprintf("plugin %s has a newer version installed than the approved one: the approved one stays active", n),
			Fix:     fmt.Sprintf("cartographer plugin show %s, then cartographer plugin approve %s", n, n)})
	}
	return out
}

// checkPluginCollisions runs DropPluginCollisions over what the active plugins
// contribute and what the lockfile records as installed from a KB or the bundle.
// The server is not asked: a KB artifact that a sync has already dropped because
// of a plugin is not in the lockfile, and the sync that dropped it printed
// the collision.
func checkPluginCollisions(cfg *clientconfig.Config, lock *provisioning.LockFile) []doctorFinding {
	arts := loadActivePlugins(cfg).Artifacts
	if len(arts) == 0 {
		return nil
	}
	seen := map[string]bool{}
	if lock != nil {
		for _, proj := range lock.Projections() {
			for _, mf := range lock.ForProjection(proj).Managed {
				if mf.Source == "" || contract.IsPluginSource(mf.Source) {
					continue
				}
				k := mf.Kind + "\x00" + mf.Name + "\x00" + mf.Source
				if seen[k] {
					continue
				}
				seen[k] = true
				arts = append(arts, contract.Artifact{Kind: mf.Kind, Name: mf.Name, Source: mf.Source})
			}
		}
	}
	_, dropped := provisioning.DropPluginCollisions(arts)
	var out []doctorFinding
	for _, c := range dropped {
		// describePluginCollision escapes and leads with "warning: ".
		out = append(out, doctorFinding{Check: "plugin-collision", Severity: doctorWarning,
			Message: strings.TrimPrefix(describePluginCollision(c), "warning: "),
			Fix:     "rename the artifact in the plugin or in the other source, or disable the plugin: cartographer plugin disable <name>"})
	}
	return out
}

func providerIssueFinding(is pluginProviderIssue) doctorFinding {
	who := "a plugin provider"
	if is.Provider != "" {
		who = "provider " + escapeControl(is.Provider)
	}
	if is.Plugin != "" {
		who += " of plugin " + escapeControl(is.Plugin)
	}
	if is.LeftOut {
		fix := "fix the provider descriptor in the plugin"
		if is.Plugin != "" {
			fix = "cartographer plugin show " + escapeControl(is.Plugin) + ", fix the provider descriptor in the plugin and run cartographer plugin update " + escapeControl(is.Plugin)
		}
		if strings.Contains(is.Reason, "refusing to write through symlink") {
			fix = "list the link under providers." + escapeControl(is.Provider) + ".allow_symlinks in .cartographer.yaml (cartographer client symlinks " + escapeControl(is.Provider) + " --add <link>) or replace the link, then run cartographer sync"
		}
		return doctorFinding{Check: "plugin-provider", Severity: doctorWarning,
			Message: who + " is registered without a cell: " + escapeControl(is.Reason), Fix: fix}
	}
	fix := "fix the provider in the plugin and run cartographer plugin update <name>"
	if is.Plugin != "" {
		fix = "cartographer plugin show " + escapeControl(is.Plugin) + ", fix the provider in the plugin and run cartographer plugin update " + escapeControl(is.Plugin)
	}
	return doctorFinding{Check: "plugin-provider", Severity: doctorWarning,
		Message: who + " was not registered: " + escapeControl(is.Reason), Fix: fix}
}
