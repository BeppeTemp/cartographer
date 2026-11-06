package main

import (
	"fmt"
	"io"
	"os"
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

// pluginPrep is what `setup` and `connect` hand to preparePlugins.
type pluginPrep struct {
	Sources []string // the --plugin values
	Approve bool     // --approve-plugins
	// Ask: a question may be put to the user (not --no-input); it also needs a terminal.
	Ask      bool
	DryRun   bool
	Out, Err io.Writer
}

// installedNow is a plugin this run put in the store and the config: the only
// plugins --approve-plugins and the prompt can approve.
type installedNow struct {
	name  string
	p     plugin.Plugin
	entry clientconfig.PluginEntry
}

// preparePlugins is the plugin step shared by `setup` and `connect`, run before
// the providers are resolved: it installs each source not yet configured,
// fetches an approved plugin missing from the store, collects the consent for
// what it installed, and registers the providers again so this very process
// sees the approved ones. It returns an exit code (0 when nothing blocks the
// rest of the run). A plugin is approved only here and only if it was installed
// in this run, after its full approval content was printed; a plugin that is
// already configured is never approved, re-enabled or updated by it.
func preparePlugins(o pluginPrep) int {
	if o.Out == nil {
		o.Out = os.Stdout
	}
	if o.Err == nil {
		o.Err = os.Stderr
	}
	srcs := make([]plugin.Source, 0, len(o.Sources))
	seen := map[string]bool{}
	for _, s := range o.Sources {
		src, err := plugin.ParseSource(s)
		if err != nil {
			return pluginFail(2, "--plugin: %s", escapeControl(err.Error()))
		}
		if !seen[src.String()] {
			seen[src.String()] = true
			srcs = append(srcs, src)
		}
	}
	dir, err := clientconfig.TargetDir()
	if err != nil {
		return pluginFail(2, "%v", err)
	}
	cfg, err := loadConfigForWrite(dir)
	if err != nil {
		return pluginFail(1, "%v", err)
	}
	if len(srcs) == 0 && len(cfg.Plugins) == 0 {
		return 0
	}
	st := syncStreams{Out: o.Out, Err: o.Err}
	changed := false
	// The providers are registered again after any state change, even on a failure further down.
	defer func() {
		if changed {
			registerPluginProviders(o.Err)
		}
	}()

	fresh, code := installPluginSources(o, dir, cfg, srcs, &changed)
	if code != 0 {
		return code
	}

	// A plugin missing from the store is fetched before the lock is taken, and
	// stored under it; a failure is a warning and the run goes on.
	if cfg, err = loadConfigForWrite(dir); err != nil {
		return pluginFail(1, "%v", err)
	}
	if fetched := fetchMissingPlugins(st, cfg, o.DryRun); len(fetched) > 0 {
		if release, err := lockClient(dir); err != nil {
			fmt.Fprintf(o.Err, "warning: %s\n", escapeControl(err.Error()))
		} else {
			if c2, err := loadConfigForWrite(dir); err == nil {
				storeFetchedPlugins(st, c2, fetched)
				changed = true
			}
			release()
		}
	}
	if o.DryRun || len(fresh) == 0 {
		return 0
	}
	return consentInstalledPlugins(o, dir, fresh, &changed)
}

// installPluginSources installs each source that is not configured yet, as
// `plugin install` does without approving: the tree is stored and the entry
// written enabled but unapproved. A source already configured is skipped
// whatever its ref (moving it is `plugin update`); another source under a name
// already installed stops the run with the `plugin install` error.
func installPluginSources(o pluginPrep, dir string, cfg *clientconfig.Config, srcs []plugin.Source, changed *bool) ([]installedNow, int) {
	var fresh []installedNow
	store, storeErr := pluginStoreFn()
	for _, src := range srcs {
		recorded := src.Remote
		if !src.Git {
			recorded = src.Path
		}
		shown := escapeControl(gitx.RedactRemoteURL(src.String()))
		if names := pluginsFromSource(cfg, recorded); len(names) > 0 {
			fmt.Fprintf(o.Out, "plugin %s is already installed from %s\n", escapeControl(strings.Join(names, ", ")), shown)
			continue
		}
		if o.DryRun {
			fmt.Fprintf(o.Out, "[dry-run] would install plugin from %s\n", shown)
			continue
		}
		if storeErr != nil {
			return fresh, pluginFail(1, "%v", storeErr)
		}
		files, sha, p, probs, err := pluginRead(src)
		if err != nil {
			return fresh, pluginFail(1, "cannot read plugin %s: %s", shown, escapeControl(err.Error()))
		}
		if len(probs) > 0 {
			for _, pr := range probs {
				fmt.Fprintln(o.Err, escapeControl(pr))
			}
			return fresh, pluginFail(1, "%s is not a valid plugin; nothing was installed from it", shown)
		}
		name := p.Manifest.Name
		release, err := lockClient(dir)
		if err != nil {
			return fresh, pluginFail(1, "%v", err)
		}
		entry, code := func() (clientconfig.PluginEntry, int) {
			defer release()
			c, err := loadConfigForWrite(dir)
			if err != nil {
				return clientconfig.PluginEntry{}, pluginFail(1, "%v", err)
			}
			if cur, had := c.Plugin(name); had {
				if cur.Source != recorded {
					return cur, pluginFail(2, "%s is installed from %s: run `cartographer plugin remove %s` first", escapeControl(name), escapeControl(gitx.RedactRemoteURL(cur.Source)), escapeControl(name))
				}
				return cur, -1 // configured meanwhile from the same source
			}
			if err := store.Put(name, files, p.Digest, pluginOpts()); err != nil {
				return clientconfig.PluginEntry{}, pluginFail(1, "%v", err)
			}
			e := clientconfig.PluginEntry{Source: recorded, Ref: src.Ref, SHA: sha, Version: p.Manifest.Version, Installed: p.Digest, Enabled: true}
			if err := c.SetPlugin(name, e); err != nil {
				return e, pluginFail(1, "%v", err)
			}
			if err := clientconfig.Save(dir, c); err != nil {
				return e, pluginFail(1, "%v", err)
			}
			*changed = true
			cfg = c
			return e, -2
		}()
		switch code {
		case -2:
			fresh = append(fresh, installedNow{name: name, p: p, entry: entry})
		case -1:
		default:
			return fresh, code
		}
	}
	return fresh, 0
}

// pluginsFromSource names the configured plugins whose recorded source is recorded.
func pluginsFromSource(cfg *clientconfig.Config, recorded string) []string {
	var out []string
	for n, e := range cfg.Plugins {
		if e.Source == recorded {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// consentInstalledPlugins puts each plugin installed in this run to its consent.
// The full approval content is printed before any approval: with
// --approve-plugins the flag is the consent and the printed block its record,
// on a terminal each block is followed by the question, and with neither
// nothing is approved and the plugin stays pending. Plugins are approved in one
// locked pass that reloads the config.
func consentInstalledPlugins(o pluginPrep, dir string, fresh []installedNow, changed *bool) int {
	cfg, err := loadConfigForWrite(dir)
	if err != nil {
		return pluginFail(1, "%v", err)
	}
	var yes []installedNow
	for _, f := range fresh {
		shown := escapeControl(f.name)
		switch {
		case o.Approve:
			printConsent(o.Out, f.p, f.entry, cfg)
			yes = append(yes, f)
		case o.Ask && pluginInteractive():
			printConsent(o.Out, f.p, f.entry, cfg)
			if promptYesNo(fmt.Sprintf("Enable %s %s? [y/N] ", shown, escapeControl(f.p.Manifest.Version))) {
				yes = append(yes, f)
			} else {
				fmt.Fprintf(o.Out, "not approved: run `cartographer plugin approve %s` to enable it\n", shown)
			}
		default:
			fmt.Fprintf(o.Out, "pending approval: cartographer plugin approve %s\n", shown)
		}
	}
	if len(yes) == 0 {
		return 0
	}
	store, err := pluginStoreFn()
	if err != nil {
		return pluginFail(1, "%v", err)
	}
	release, err := lockClient(dir)
	if err != nil {
		return pluginFail(1, "%v", err)
	}
	defer release()
	if cfg, err = loadConfigForWrite(dir); err != nil {
		return pluginFail(1, "%v", err)
	}
	// Still the tree the user was shown, else nothing is approved for it.
	var todo []installedNow
	failed := false
	for _, f := range yes {
		if cur, ok := cfg.Plugin(f.name); !ok || cur.Installed != f.p.Digest {
			fmt.Fprintf(o.Err, "warning: %s changed while waiting for the answer; nothing was approved for it\n", escapeControl(f.name))
			failed = true
			continue
		}
		todo = append(todo, f)
	}
	// What is reported is printed once the file is saved.
	var report strings.Builder
	// A plugin whose dependency is not enabled yet waits for the others of this
	// run; what stays unmet is approved but not enabled (a first approval enables
	// only when `depends` is met), and enabling it is `plugin enable`.
	for len(todo) > 0 {
		var next []installedNow
		for _, f := range todo {
			if len(unmetDepends(cfg, store, f.name, f.p)) > 0 {
				next = append(next, f)
				continue
			}
			cur, _ := cfg.Plugin(f.name)
			cur.Approved = f.p.Digest
			if err := cfg.SetPlugin(f.name, cur); err != nil {
				return pluginFail(1, "%v", err)
			}
			fmt.Fprintf(&report, "Enabled %s %s\n", escapeControl(f.name), escapeControl(f.p.Manifest.Version))
		}
		if len(next) == len(todo) {
			for _, f := range next {
				cur, _ := cfg.Plugin(f.name)
				cur.Approved, cur.Enabled = f.p.Digest, false
				if err := cfg.SetPlugin(f.name, cur); err != nil {
					return pluginFail(1, "%v", err)
				}
				fmt.Fprintf(&report, "Approved %s %s but did not enable it:\n", escapeControl(f.name), escapeControl(f.p.Manifest.Version))
				for _, u := range unmetDepends(cfg, store, f.name, f.p) {
					fmt.Fprintf(&report, "  %s\n", escapeControl(u))
				}
				fmt.Fprintf(&report, "  run `cartographer plugin enable %s` once they are met\n", escapeControl(f.name))
			}
			break
		}
		todo = next
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		return pluginFail(1, "%v", err)
	}
	*changed = true
	fmt.Fprint(o.Out, report.String())
	// The previous trees are no longer kept; best effort, like every lifecycle command.
	if err := store.GC(keepMap(cfg)); err != nil {
		fmt.Fprintf(o.Err, "warning: plugin store cleanup: %s\n", escapeControl(err.Error()))
	}
	if failed {
		return 1
	}
	return 0
}
