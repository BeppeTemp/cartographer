package main

import (
	"fmt"
	"io"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
)

// statusStoreUnavailable is the state of an enabled, approved plugin when the
// plugin store itself cannot be used (no cache directory): neither "not fetched"
// nor "modified" would be true. doctor says why.
const statusStoreUnavailable core.Status = "store unavailable"

// pluginStatus is one configured plugin as `status` and `agents` report it.
// Additive to the status schema, present only when a plugin is configured.
type pluginStatus struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	// Source is the redacted remote or the folder; Ref and SHA say where it was
	// read (SHA is empty for a folder).
	Source    string `json:"source"`
	Ref       string `json:"ref,omitempty"`
	SHA       string `json:"sha,omitempty"`
	Installed string `json:"installed,omitempty"`
	Approved  string `json:"approved,omitempty"`
	// State is one of core.Status, or "store unavailable".
	State string `json:"state"`
	// UpdateAvailable: a newer tree is installed than the approved one.
	UpdateAvailable bool `json:"update_available"`
}

// pluginStateOf is core.State with the store's own failure folded in.
func pluginStateOf(store core.Store, storeErr error, name string, e clientconfig.PluginEntry) core.Status {
	if storeErr != nil && e.Enabled && e.Approved != "" {
		return statusStoreUnavailable
	}
	st, _ := core.State(name, e, store, pluginOpts())
	return st
}

// snapshotPlugins lists the configured plugins by name; nil when none is
// configured, so nothing about plugins is printed or encoded for nobody.
func snapshotPlugins(cfg *clientconfig.Config) []pluginStatus {
	if cfg == nil || len(cfg.Plugins) == 0 {
		return nil
	}
	names := make([]string, 0, len(cfg.Plugins))
	for n := range cfg.Plugins {
		names = append(names, n)
	}
	sort.Strings(names)
	store, storeErr := pluginStoreFn()
	out := make([]pluginStatus, 0, len(names))
	for _, n := range names {
		e := cfg.Plugins[n]
		out = append(out, pluginStatus{
			Name: n, Version: e.Version, Source: gitx.RedactRemoteURL(e.Source), Ref: e.Ref, SHA: e.SHA,
			Installed: e.Installed, Approved: e.Approved, State: string(pluginStateOf(store, storeErr, n, e)),
			UpdateAvailable: e.Approved != "" && e.Installed != e.Approved,
		})
	}
	return out
}

// shortID is the first n characters of a digest or sha.
func shortID(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// printPluginSection writes the plugins of a snapshot and the providers they
// declare. Every string comes from the user's config or a plugin, so every one
// is escaped. Nothing is printed when no plugin is configured.
func printPluginSection(w io.Writer, s statusSnapshot) {
	if len(s.Plugins) == 0 {
		return
	}
	fmt.Fprintln(w, "plugins:")
	for _, p := range s.Plugins {
		id := shortID(p.SHA, 7)
		if id == "" {
			id = shortID(p.Installed, 7)
		}
		src := p.Source
		if p.Ref != "" {
			src += "@" + p.Ref
		}
		line := fmt.Sprintf("  %s %s  %s  %s  %s", p.Name, dashIfEmpty(p.Version), p.State, dashIfEmpty(id), src)
		if p.UpdateAvailable {
			line += "  update available"
		}
		fmt.Fprintln(w, escapeControl(line))
		if p.State == string(core.StatusPending) {
			fmt.Fprintf(w, "    pending approval: cartographer plugin approve %s\n", escapeControl(p.Name))
		}
		for _, pr := range s.Providers {
			if pr.Plugin != p.Name {
				continue
			}
			state := "not detected"
			switch {
			case pr.Connected:
				state = "connected"
			case pr.Installed:
				state = "detected"
			}
			fmt.Fprintf(w, "    provider %s (plugin %s, base %s): %s\n", escapeControl(pr.Name), escapeControl(pr.Plugin), escapeControl(pr.Base), state)
			for _, is := range pluginProviderIssues {
				if is.LeftOut && is.Provider == pr.Name {
					fmt.Fprintf(w, "    [%s] warning: cell left out: %s\n", escapeControl(is.Provider), escapeControl(is.Reason))
				}
			}
		}
	}
}
