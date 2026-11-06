package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/plugin"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// pluginSet is what the installed plugins contribute to one run.
type pluginSet struct {
	// Artifacts are the skills, agents and hooks of the active plugins, Files
	// in memory, read back from the store and re-validated to the approved
	// digest by plugin.State at this very call.
	Artifacts []provisioning.Artifact
	// Unreadable names the plugins that are enabled and approved but whose
	// approved tree cannot be used now (not fetched, modified, invalid, or no
	// store at all). They contribute nothing and retire nothing: what they
	// installed stays and nothing of it is pruned (the rule of an unreachable
	// KB). A disabled, pending or removed plugin is not here: it retracts.
	Unreadable map[string]bool
	// Issues is one line per plugin whose state deserves a warning.
	Issues []string
}

// loadActivePlugins is the one place a sync, status or approve pass turns the
// user's `plugins` key into artifacts. The store is a cache and the approved
// digest decides what installs (D1727): nothing here reads the "installed" one.
func loadActivePlugins(cfg *clientconfig.Config) pluginSet {
	var set pluginSet
	if cfg == nil || len(cfg.Plugins) == 0 {
		return set
	}
	names := make([]string, 0, len(cfg.Plugins))
	for n := range cfg.Plugins {
		names = append(names, n)
	}
	sort.Strings(names)
	store, storeErr := pluginStoreFn()
	set.Unreadable = map[string]bool{}
	for _, n := range names {
		e := cfg.Plugins[n]
		if !e.Enabled || e.Approved == "" {
			continue // disabled or pending approval: states, not warnings
		}
		if storeErr != nil {
			set.Unreadable[n] = true
			set.Issues = append(set.Issues, fmt.Sprintf("plugin %s: the plugin store cannot be used: %v", n, storeErr))
			continue
		}
		st, p := plugin.State(n, e, store, pluginOpts())
		switch st {
		case plugin.StatusActive:
			for _, a := range p.Artifacts {
				a.Version = p.Manifest.Version
				set.Artifacts = append(set.Artifacts, a)
			}
		case plugin.StatusNotFetched, plugin.StatusModified, plugin.StatusInvalid:
			set.Unreadable[n] = true
			set.Issues = append(set.Issues, fmt.Sprintf("plugin %s is %s: nothing is installed from it this run and what it installed stays", n, st))
		}
	}
	return set
}

// manifestPlugins is the ApplyOptions.ActivePlugins of a manifest: the plugins
// that contribute an artifact to it. A manifest carries a "plugin:" artifact
// only from loadActivePlugins (pullTarget refuses a served one and
// VerifiedManifest refuses a signed one), so this is the active set of that run.
func manifestPlugins(m provisioning.Manifest) map[string]bool {
	var out map[string]bool
	for _, a := range m.Artifacts {
		if name := provisioning.PluginName(a.Source); name != "" {
			if out == nil {
				out = map[string]bool{}
			}
			out[name] = true
		}
	}
	return out
}

// heldPlugins names the plugins whose installed files a run must leave alone.
func heldPlugins(cfg *clientconfig.Config) map[string]bool { return loadActivePlugins(cfg).Unreadable }

// fetchedPlugin is a tree fetched for a plugin missing from the store, held in
// memory until it is stored under the client lock.
type fetchedPlugin struct {
	name  string
	files []plugin.TreeFile
	p     plugin.Plugin
}

// fetchMissingPlugins runs once per sync BEFORE the client lock is taken: a
// network fetch of 15 s per source must not eat the 30 s the lock is waited
// for, or two unreachable sources would make concurrent syncs fail. It reads
// an approved plugin that is not in the store (a purged cache, a new machine)
// from the source and ref the user configured and returns the trees whose
// digest equals the approved one; it stores nothing and NEVER approves.
// Anything else is a warning and the run goes on without the plugin. A dry run
// fetches nothing.
func fetchMissingPlugins(st syncStreams, cfg *clientconfig.Config, dryRun bool) []fetchedPlugin {
	if cfg == nil || len(cfg.Plugins) == 0 {
		return nil
	}
	store, err := pluginStoreFn()
	if err != nil {
		return nil // reported by loadActivePlugins as an issue
	}
	names := make([]string, 0, len(cfg.Plugins))
	for n := range cfg.Plugins {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []fetchedPlugin
	for _, n := range names {
		e := cfg.Plugins[n]
		if !e.Enabled || e.Approved == "" {
			continue
		}
		if state, _ := plugin.State(n, e, store, pluginOpts()); state != plugin.StatusNotFetched {
			continue
		}
		if dryRun {
			fmt.Fprintf(st.out(), "[dry-run] would fetch plugin %s from %s\n", n, escapeControl(redactPluginSource(e)))
			continue
		}
		f, err := fetchApproved(n, e)
		if err != nil {
			fmt.Fprintf(st.err(), "warning: plugin %s: %v\n", n, err)
			continue
		}
		out = append(out, f)
	}
	return out
}

// storeFetchedPlugins runs under the client lock: it stores each fetched tree
// (re-checking that its digest is the approved one in cfg), then reports the
// plugins that still cannot be read.
func storeFetchedPlugins(st syncStreams, cfg *clientconfig.Config, fetched []fetchedPlugin) {
	if cfg == nil || len(cfg.Plugins) == 0 {
		return
	}
	if store, err := pluginStoreFn(); err == nil {
		for _, f := range fetched {
			if cfg.Plugins[f.name].Approved != f.p.Digest {
				continue
			}
			if err := store.Put(f.name, f.files, f.p.Digest, pluginOpts()); err != nil {
				fmt.Fprintf(st.err(), "warning: plugin %s: %v\n", f.name, err)
			}
		}
	}
	for _, issue := range loadActivePlugins(cfg).Issues {
		fmt.Fprintf(st.err(), "warning: %s\n", escapeControl(issue))
	}
}

func redactPluginSource(e clientconfig.PluginEntry) string {
	return pluginSourceSpec(e, true)
}

// pluginSourceSpec is the text plugin.ParseSource reads back for an entry.
func pluginSourceSpec(e clientconfig.PluginEntry, redact bool) string {
	src := e.Source
	if redact {
		src = gitx.RedactRemoteURL(src)
	}
	if e.Ref != "" {
		src += "@" + e.Ref
	}
	return src
}

// fetchApproved reads the plugin's tree again and returns it only if it is the approved one.
func fetchApproved(name string, e clientconfig.PluginEntry) (fetchedPlugin, error) {
	src, err := plugin.ParseSource(pluginSourceSpec(e, false))
	if err != nil {
		return fetchedPlugin{}, fmt.Errorf("cannot fetch: %v", err)
	}
	files, _, p, probs, err := pluginRead(src)
	switch {
	case err != nil:
		return fetchedPlugin{}, fmt.Errorf("cannot fetch from %s: %v", redactPluginSource(e), err)
	case len(probs) > 0:
		return fetchedPlugin{}, fmt.Errorf("the fetched tree is not a valid plugin (%s): nothing is installed from it", strings.Join(probs, "; "))
	case p.Digest != e.Approved:
		return fetchedPlugin{}, fmt.Errorf("the source no longer yields the approved version: reported as modified, run `cartographer plugin update %s` to review it", name)
	}
	return fetchedPlugin{name: name, files: files, p: p}, nil
}

// describePluginCollision words one dropped collision.
func describePluginCollision(c provisioning.PluginCollision) string {
	labels := make([]string, len(c.Sources))
	for i, s := range c.Sources {
		switch {
		case s == "bundle":
			labels[i] = "the bundle"
		case provisioning.IsPluginSource(s):
			labels[i] = "plugin " + strings.TrimPrefix(s, "plugin:")
		default:
			labels[i] = "KB " + strings.TrimPrefix(s, "kb:")
		}
	}
	tail := "installed from neither"
	if len(labels) > 2 {
		tail = "installed from none of them"
	}
	if slices.Contains(c.Sources, "bundle") {
		tail = "the bundled one stays, the plugin's is dropped"
	}
	return fmt.Sprintf("warning: %s %s is declared by %s: %s", escapeControl(c.Kind), escapeControl(c.Name), escapeControl(strings.Join(labels, " and ")), tail)
}

// mergeAcrossPlugins drops the collisions a plugin is part of, reports each
// through report (nil: silent), then merges strictly so KB against KB is still
// an error. Dropping comes first, or the strict merge would see plugin against
// KB as a plain KB collision or let one claimant win silently.
func mergeAcrossPlugins(arts []provisioning.Artifact, report func(provisioning.PluginCollision)) (provisioning.Manifest, error) {
	kept, dropped := provisioning.DropPluginCollisions(arts)
	if report != nil {
		for _, c := range dropped {
			report(c)
		}
	}
	return provisioning.MergeArtifactsStrict(kept)
}

// splitHeldPlugins separates the lock entries of the held plugins from the
// rest. Sync, status and doctor all judge a projection against the rest, so
// an unreadable plugin is neither pruned nor reported as drift.
func splitHeldPlugins(lock provisioning.Lock, held map[string]bool) (rest provisioning.Lock, heldEntries []provisioning.ManagedFile) {
	if len(held) == 0 {
		return lock, nil
	}
	rest = lock
	rest.Managed = nil
	for _, mf := range lock.Managed {
		if held[provisioning.PluginName(mf.Source)] {
			heldEntries = append(heldEntries, mf)
		} else {
			rest.Managed = append(rest.Managed, mf)
		}
	}
	return rest, heldEntries
}
