package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/plugin"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// The lifecycle after the first install: approve, update, enable, disable and
// remove. Every writer runs under the client lock with the config reloaded
// inside it, is idempotent, and calls registerPluginProviders after it changed
// a state, so a sync in the same process sees the providers of the new state.
// `depends` is check-only: nothing here installs, fetches or enables a dependency.

// pluginResult is the JSON of one lifecycle command.
type pluginResult struct {
	Schema  string   `json:"schema"`
	Action  string   `json:"action"`
	Name    string   `json:"name"`
	Changed bool     `json:"changed"`
	State   string   `json:"state,omitempty"`
	Message string   `json:"message,omitempty"`
	Pruned  []string `json:"pruned,omitempty"`
}

// lifecycleCtx is what every lifecycle command starts from.
type lifecycleCtx struct {
	dir    string
	store  plugin.Store
	json   bool
	yes    bool
	out    io.Writer // os.Stdout, or Discard in JSON mode
	action string
}

func (c lifecycleCtx) emit(r pluginResult) {
	r.Schema, r.Action = pluginSchema, c.action
	if c.json {
		printJSON(r)
	}
}

// newLifecycle parses `[<name>] [--yes] [--output ...]`: min..max positionals.
func newLifecycle(action string, args []string, min, max int, yes bool) (lifecycleCtx, []string, int) {
	c := lifecycleCtx{action: action, out: os.Stdout}
	output, args, err := outputFlag(args)
	var bools []string
	if yes {
		bools = []string{"--yes"}
	}
	pos, flags, ok := pluginArgs(args, bools...)
	if err != nil || !ok || len(pos) < min || len(pos) > max {
		return c, nil, pluginFail(2, "%s", pluginUsage)
	}
	c.json, c.yes = output == "json", flags["--yes"]
	if c.json {
		c.out = io.Discard
	}
	if c.store, err = pluginStoreFn(); err != nil {
		return c, nil, pluginFail(1, "%v", err)
	}
	if c.dir, err = clientconfig.TargetDir(); err != nil {
		return c, nil, pluginFail(2, "%v", err)
	}
	return c, pos, -1
}

// keepMap is what the store's GC preserves for the configured plugins.
func keepMap(cfg *clientconfig.Config) map[string]plugin.Keep {
	keep := map[string]plugin.Keep{}
	for n, e := range cfg.Plugins {
		keep[n] = plugin.Keep{Installed: e.Installed, Approved: e.Approved}
	}
	return keep
}

// gcStore is best effort and runs under the client lock the caller holds.
func gcStore(c lifecycleCtx, cfg *clientconfig.Config) {
	if err := c.store.GC(keepMap(cfg)); err != nil {
		fmt.Fprintf(os.Stderr, "warning: plugin store cleanup: %s\n", escapeControl(err.Error()))
	}
}

// locked takes the client lock, reloads the config inside it and hands
// the entry of name to fn; fn mutates cfg and the caller saves it.
func (c lifecycleCtx) locked(fn func(cfg *clientconfig.Config) int) int {
	release, err := lockClient(c.dir)
	if err != nil {
		return pluginFail(1, "%v", err)
	}
	defer release()
	cfg, err := clientconfig.Load(c.dir)
	if err != nil {
		return pluginFail(1, "%v", err)
	}
	return fn(cfg)
}

func unknownPlugin(name string) int {
	return pluginFail(2, "no plugin named %q (see `cartographer plugin list`)", name)
}

// dependents names the other enabled plugins whose approved tree depends on name.
func dependents(cfg *clientconfig.Config, store plugin.Store, name string) []string {
	var out []string
	for n, e := range cfg.Plugins {
		if n == name || !e.Enabled || e.Approved == "" {
			continue
		}
		if t, err := store.Open(n, e.Approved, pluginOpts()); err == nil {
			if _, ok := t.Manifest.Depends[name]; ok {
				out = append(out, n)
			}
		}
	}
	sort.Strings(out)
	return out
}

// unmetDepends returns one sentence per entry of p's `depends` that is not an
// enabled, active plugin whose version meets the range.
func unmetDepends(cfg *clientconfig.Config, store plugin.Store, name string, p plugin.Plugin) []string {
	var out []string
	deps := make([]string, 0, len(p.Manifest.Depends))
	for d := range p.Manifest.Depends {
		deps = append(deps, d)
	}
	sort.Strings(deps)
	for _, d := range deps {
		rng := p.Manifest.Depends[d]
		e, ok := cfg.Plugin(d)
		var dp *plugin.Plugin
		if ok {
			_, dp = plugin.State(d, e, store, pluginOpts())
		}
		switch {
		case dp == nil:
			out = append(out, fmt.Sprintf("%s needs %s %s: %s is not enabled", name, d, rng, d))
		case !plugin.RangeMetOK(rng, dp.Manifest.Version):
			out = append(out, fmt.Sprintf("%s needs %s %s: %s is %s", name, d, rng, d, dp.Manifest.Version))
		}
	}
	return out
}

// printConsent is the full text a consent covers: the preview, then the content
// of every artifact (hook scripts, hook.json, frontmatter that declares commands).
func printConsent(w io.Writer, p plugin.Plugin, e clientconfig.PluginEntry, cfg *clientconfig.Config) {
	pluginPreview(w, p, e, cfg)
	for _, a := range p.Artifacts {
		fmt.Fprintf(w, "%s %q:\n", a.Kind, a.Name)
		printApprovalContent(w, a, a.Files)
	}
}

// finishApprove records the consent under the lock: Approved := digest, enabled.
// It fails when the entry changed while the prompt was open.
func (c lifecycleCtx) finishApprove(name, digest, version string) int {
	code := c.locked(func(cfg *clientconfig.Config) int {
		cur, ok := cfg.Plugin(name)
		if !ok || cur.Installed != digest {
			return pluginFail(1, "%s changed while waiting for the answer; nothing was approved, run the command again", name)
		}
		cur.Approved, cur.Enabled = digest, true
		if err := cfg.SetPlugin(name, cur); err != nil {
			return pluginFail(1, "%v", err)
		}
		if err := clientconfig.Save(c.dir, cfg); err != nil {
			return pluginFail(1, "%v", err)
		}
		// The previous approved tree is no longer kept.
		gcStore(c, cfg)
		return -1
	})
	if code >= 0 {
		return code
	}
	registerPluginProviders(os.Stderr)
	fmt.Fprintf(c.out, "Enabled %s %s\n", name, version)
	c.emit(pluginResult{Name: name, Changed: true, State: string(plugin.StatusActive)})
	return 0
}

func pluginApprove(args []string) int {
	c, pos, code := newLifecycle("approve", args, 1, 1, true)
	if code >= 0 {
		return code
	}
	name := pos[0]
	cfg, _, code := pluginLoad()
	if code >= 0 {
		return code
	}
	e, ok := cfg.Plugin(name)
	if !ok {
		return unknownPlugin(name)
	}
	if e.Installed == "" || e.Approved == e.Installed {
		fmt.Fprintf(c.out, "nothing is waiting for approval for %s\n", name)
		c.emit(pluginResult{Name: name, Message: "nothing is waiting"})
		return 0
	}
	// Only the installed tree, read back and re-validated, can be approved.
	p, err := c.store.Open(name, e.Installed, pluginOpts())
	if err != nil {
		if errors.Is(err, plugin.ErrNotFetched) || errors.Is(err, plugin.ErrModified) || errors.Is(err, plugin.ErrInvalid) {
			return pluginFail(1, "the stored tree of %s cannot be used (%v): run `cartographer plugin update %s`; nothing was approved", name, err, name)
		}
		return pluginFail(1, "%v", err)
	}
	printConsent(c.out, p, e, cfg)
	if !c.yes {
		if c.json || !pluginInteractive() {
			return pluginFail(2, "non-interactive approval requires --yes")
		}
		if !promptYesNo(fmt.Sprintf("Enable %s %s? [y/N] ", name, e.Version)) {
			fmt.Println("not approved")
			return 0
		}
	}
	return c.finishApprove(name, e.Installed, p.Manifest.Version)
}

func pluginUpdate(args []string) int {
	c, pos, code := newLifecycle("update", args, 0, 1, true)
	if code >= 0 {
		return code
	}
	cfg, _, code := pluginLoad()
	if code >= 0 {
		return code
	}
	names := pos
	if len(pos) == 0 {
		for n := range cfg.Plugins {
			names = append(names, n)
		}
		sort.Strings(names)
	} else if _, ok := cfg.Plugin(pos[0]); !ok {
		return unknownPlugin(pos[0])
	}
	failed := false
	for _, n := range names {
		if rc := c.updateOne(n); rc != 0 {
			failed = true
		}
	}
	if failed {
		return 1
	}
	return 0
}

// updateOne re-acquires one plugin from its configured source, stores the tree
// and moves Installed only. It never approves without the user's answer, and the
// approved version stays active until then (Approved is never reset).
func (c lifecycleCtx) updateOne(name string) int {
	cfg, err := clientconfig.Load(c.dir)
	if err != nil {
		return pluginFail(1, "%v", err)
	}
	e, ok := cfg.Plugin(name)
	if !ok {
		return unknownPlugin(name)
	}
	fail := func(format string, a ...any) int {
		msg := fmt.Sprintf(format, a...)
		c.emit(pluginResult{Name: name, Message: msg})
		return pluginFail(1, "%s: %s; the entry is unchanged", name, msg)
	}
	src, err := plugin.ParseSource(pluginSourceSpec(e, false))
	if err != nil {
		return fail("cannot update: %v", err)
	}
	files, sha, p, probs, err := pluginRead(src)
	switch {
	case err != nil:
		return fail("cannot fetch from %s: %v", gitx.RedactRemoteURL(e.Source), err)
	case len(probs) > 0:
		return fail("the fetched tree is not a valid plugin (%s)", escapeControl(joinProblems(probs)))
	case p.Manifest.Name != name:
		return fail("the source now names the plugin %q", p.Manifest.Name)
	}
	if p.Digest == e.Approved && p.Digest == e.Installed {
		fmt.Fprintf(c.out, "%s %s is up to date\n", name, e.Version)
		c.emit(pluginResult{Name: name, Message: "up to date"})
		return 0
	}
	code := c.locked(func(cfg *clientconfig.Config) int {
		cur, ok := cfg.Plugin(name)
		if !ok || cur.Source != e.Source || cur.Ref != e.Ref {
			return pluginFail(1, "%s changed while it was fetched; run the update again", name)
		}
		if err := c.store.Put(name, files, p.Digest, pluginOpts()); err != nil {
			return pluginFail(1, "%v", err)
		}
		cur.SHA, cur.Version, cur.Installed = sha, p.Manifest.Version, p.Digest
		if err := cfg.SetPlugin(name, cur); err != nil {
			return pluginFail(1, "%v", err)
		}
		if err := clientconfig.Save(c.dir, cfg); err != nil {
			return pluginFail(1, "%v", err)
		}
		gcStore(c, cfg)
		e = cur
		return -1
	})
	if code >= 0 {
		return code
	}
	cfg, _ = clientconfig.Load(c.dir)
	if old, err := c.store.Open(name, e.Approved, pluginOpts()); e.Approved != "" && err == nil {
		env := plugin.DefaultProviderEnv()
		env.Home = c.dir
		plugin.DiffPrint(c.out, name, plugin.Diff(old, p, env))
		fmt.Fprintf(c.out, "the approved version %s stays active until %s is approved\n", escapeControl(old.Manifest.Version), escapeControl(p.Manifest.Version))
	} else {
		printConsent(c.out, p, e, cfg)
	}
	if !c.yes {
		if c.json || !pluginInteractive() || !promptYesNo(fmt.Sprintf("Enable %s %s? [y/N] ", name, p.Manifest.Version)) {
			fmt.Fprintf(c.out, "not approved: run `cartographer plugin approve %s` to enable it\n", name)
			c.emit(pluginResult{Name: name, Changed: true, Message: "update available, not approved"})
			return 0
		}
	}
	return c.finishApprove(name, p.Digest, p.Manifest.Version)
}

func joinProblems(probs []string) string {
	out := ""
	for i, p := range probs {
		if i > 0 {
			out += "; "
		}
		out += p
	}
	return out
}

func pluginEnable(args []string) int {
	c, pos, code := newLifecycle("enable", args, 1, 1, false)
	if code >= 0 {
		return code
	}
	name := pos[0]
	return c.locked(func(cfg *clientconfig.Config) int {
		e, ok := cfg.Plugin(name)
		if !ok {
			return unknownPlugin(name)
		}
		if e.Enabled && e.Approved != "" {
			fmt.Fprintf(c.out, "%s is already enabled\n", name)
			c.emit(pluginResult{Name: name, State: string(plugin.StatusActive)})
			return 0
		}
		if e.Approved == "" {
			return pluginFail(1, "%s is pending approval: run `cartographer plugin approve %s`", name, name)
		}
		probe := e
		probe.Enabled = true
		st, p := plugin.State(name, probe, c.store, pluginOpts())
		if p == nil {
			return pluginFail(1, "%s is %s: its approved tree cannot be used; run `cartographer plugin update %s`", name, st, name)
		}
		if unmet := unmetDepends(cfg, c.store, name, *p); len(unmet) > 0 {
			for _, u := range unmet {
				fmt.Fprintln(os.Stderr, "Error:", escapeControl(u))
			}
			return 1
		}
		e.Enabled = true
		if err := cfg.SetPlugin(name, e); err != nil {
			return pluginFail(1, "%v", err)
		}
		if err := clientconfig.Save(c.dir, cfg); err != nil {
			return pluginFail(1, "%v", err)
		}
		registerPluginProviders(os.Stderr)
		fmt.Fprintf(c.out, "Enabled %s %s\n", name, p.Manifest.Version)
		c.emit(pluginResult{Name: name, Changed: true, State: string(plugin.StatusActive)})
		return 0
	})
}

func pluginDisable(args []string) int {
	c, pos, code := newLifecycle("disable", args, 1, 1, false)
	if code >= 0 {
		return code
	}
	name := pos[0]
	return c.locked(func(cfg *clientconfig.Config) int {
		e, ok := cfg.Plugin(name)
		if !ok {
			return unknownPlugin(name)
		}
		if !e.Enabled {
			fmt.Fprintf(c.out, "%s is already disabled\n", name)
			c.emit(pluginResult{Name: name, State: string(plugin.StatusDisabled)})
			return 0
		}
		if deps := dependents(cfg, c.store, name); len(deps) > 0 {
			return pluginFail(1, "%s is needed by enabled %s: disable that first", name, joinProblems(deps))
		}
		e.Enabled = false
		if err := cfg.SetPlugin(name, e); err != nil {
			return pluginFail(1, "%v", err)
		}
		if err := clientconfig.Save(c.dir, cfg); err != nil {
			return pluginFail(1, "%v", err)
		}
		registerPluginProviders(os.Stderr)
		fmt.Fprintf(c.out, "Disabled %s; it stays installed and its contributions are retracted at the next sync\n", name)
		c.emit(pluginResult{Name: name, Changed: true, State: string(plugin.StatusDisabled)})
		return 0
	})
}

// pluginProviderNames are the providers the approved tree of name declares.
func pluginProviderNames(t plugin.Plugin) []string {
	var out []string
	for _, c := range t.Contributions["providers"] {
		out = append(out, c.Name)
	}
	sort.Strings(out)
	return out
}

// pluginRemove retires what the plugin installed, then forgets it, in an order
// a failure can be retried from: (1) its providers through the disconnect path,
// (2) its artifacts in every other provider section, pruned from the lockfile's
// own entries (never from paths computed from a tree), (3) the config entry and
// the stored trees. A plugin whose approved tree cannot be read is refused: its
// providers are unknown, so nothing it installed could be retired.
func pluginRemove(args []string) int {
	c, pos, code := newLifecycle("remove", args, 1, 1, false)
	if code >= 0 {
		return code
	}
	name := pos[0]
	rc := c.locked(func(cfg *clientconfig.Config) int {
		e, ok := cfg.Plugin(name)
		if !ok {
			return unknownPlugin(name)
		}
		if deps := dependents(cfg, c.store, name); len(deps) > 0 {
			return pluginFail(1, "%s is needed by enabled %s: disable or remove that first", name, joinProblems(deps))
		}
		var providers []string
		if e.Approved != "" {
			t, err := c.store.Open(name, e.Approved, pluginOpts())
			if err != nil {
				return pluginFail(1, "the approved tree of %s cannot be read (%v), so what it installed cannot be retired; run `cartographer sync` to fetch it again (or reinstall it), then remove it", name, err)
			}
			registered := map[string]bool{}
			for _, rp := range configurator.RegisteredPlugins() {
				if rp.Spec.Origin == name {
					registered[rp.Spec.Name] = true
				}
			}
			for _, pn := range pluginProviderNames(t) {
				if !registered[pn] {
					return pluginFail(1, "the provider %s of %s is not registered this run (see the warnings above), so it cannot be retired; nothing was removed", pn, name)
				}
				providers = append(providers, pn)
			}
		}
		var pruned []string
		failed := false
		// (1) providers: MCP entry, managed files, lock section.
		if len(providers) > 0 {
			res, err := doDisconnectLocked(disconnectOptions{Providers: providers, Dir: c.dir, KeepToken: true})
			if err != nil {
				return pluginFail(1, "%v", err)
			}
			for _, pr := range res.Providers {
				for _, f := range pr.Pruned {
					pruned = append(pruned, pr.Provider+": "+filepathSlash(f.Path))
				}
				for _, w := range pr.Refused {
					failed = true
					fmt.Fprintf(os.Stderr, "[%s] kept connected: %s\n", escapeControl(pr.Provider), escapeControl(w))
				}
			}
		}
		if failed {
			return pluginFail(1, "%s was not removed: fix the refusals above and run the command again", name)
		}
		// (2) the plugin's artifacts in every provider section.
		lockPath := lockFilePath(c.dir)
		lf, err := provisioning.ReadLockFile(lockPath)
		if err != nil {
			return pluginFail(1, "read lockfile: %v", err)
		}
		provs := make([]string, 0, len(lf.Providers))
		for p := range lf.Providers {
			provs = append(provs, p)
		}
		sort.Strings(provs)
		changed := false
		for _, p := range provs {
			lock := lf.Providers[p]
			var own []provisioning.ManagedFile
			for _, mf := range lock.Managed {
				if provisioning.PluginName(mf.Source) == name {
					own = append(own, mf)
				}
			}
			if len(own) == 0 {
				continue
			}
			done, err := provisioning.PruneManaged(own, provisioning.LockBaseDir(lock, c.dir), false)
			var refused *provisioning.PruneRefusedError
			if err != nil && !errors.As(err, &refused) {
				return pluginFail(1, "prune %s for %s: %v", p, name, err)
			}
			gone := map[string]bool{}
			for _, mf := range done {
				gone[mf.Kind+"\x00"+mf.Name+"\x00"+mf.Path] = true
				pruned = append(pruned, p+": "+filepathSlash(mf.Path))
			}
			kept := lock.Managed[:0:0]
			for _, mf := range lock.Managed {
				if !gone[mf.Kind+"\x00"+mf.Name+"\x00"+mf.Path] {
					kept = append(kept, mf)
				}
			}
			lock.Managed = kept
			lf.Providers[p] = lock
			changed = changed || len(done) > 0
			if refused != nil {
				failed = true
				fmt.Fprintf(os.Stderr, "[%s] kept: %s\n", escapeControl(p), escapeControl(refused.Error()))
			}
		}
		if changed {
			if err := provisioning.WriteLockFile(lockPath, lf); err != nil {
				return pluginFail(1, "write lockfile: %v", err)
			}
		}
		for _, l := range pruned {
			fmt.Fprintf(c.out, "pruned %s\n", escapeControl(l))
		}
		if failed {
			// Nothing after the prune is done: the next run retries the rest.
			return pluginFail(1, "%s was not removed: some of its files sit behind a symlink; fix that and run the command again", name)
		}
		// (3) the entry (reloaded: the disconnect saved the config) and the stored trees.
		fresh, err := clientconfig.Load(c.dir)
		if err != nil {
			return pluginFail(1, "%v", err)
		}
		fresh.RemovePlugin(name)
		if err := clientconfig.Save(c.dir, fresh); err != nil {
			return pluginFail(1, "%v", err)
		}
		gcStore(c, fresh)
		fmt.Fprintf(c.out, "Removed %s\n", name)
		c.emit(pluginResult{Name: name, Changed: true, Pruned: pruned})
		return 0
	})
	if rc == 0 {
		registerPluginProviders(os.Stderr)
	}
	return rc
}

func filepathSlash(p string) string {
	out := []byte(p)
	for i, b := range out {
		if b == '\\' {
			out[i] = '/'
		}
	}
	return string(out)
}
