package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

// cmdDisconnect removes the requested agent provider(s) — default "all" = every
// provider currently connected in .cartographer.yaml — from this machine: the
// MCP server entry in the provider's config file, the skill files the lockfile
// recorded as managed for it, and the provider's own entries in the lockfile and
// .cartographer.yaml. Idempotent — disconnecting an already-disconnected
// provider succeeds with exit 0. A provider whose config or managed files sit
// behind a symlink is kept connected (D148) and the exit status is 1, the
// others being disconnected as usual.
func cmdDisconnect(args []string) int {
	var target string

	fs := flag.NewFlagSet("disconnect", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Print what would be removed without removing")
	agentsCSV := fs.String("agents", "", "Comma-separated agent subset: claude,codex")
	if code := bindPositionals("disconnect", parseFlagsAnywhere(fs, args), &target); code != 0 {
		return code
	}

	dir, err := clientconfig.TargetDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}

	var connectedAgents []string
	if cfg, err := clientconfig.Load(dir); err == nil {
		connectedAgents = cfg.Agents
	}

	providers, err := resolveDisconnectProviders(target, *agentsCSV, connectedAgents)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	if len(providers) == 0 {
		fmt.Println("no connected provider matches; nothing to disconnect")
		return 0
	}

	res, err := doDisconnect(disconnectOptions{Providers: providers, Dir: dir, DryRun: *dryRun})
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}

	printDisconnectSummary(res, *dryRun)
	if kept := res.keptConnected(); len(kept) > 0 {
		fmt.Fprintf(os.Stderr, "Error: kept connected because a destination is a symlink: %s (replace the link with a regular file or directory, then run disconnect again)\n", strings.Join(kept, ", "))
		return 1
	}
	return 0
}

// resolveDisconnectProviders resolves the disconnect positional target argument:
//   - "" or "all" → every provider listed in connectedAgents (.cartographer.yaml)
//   - an explicit provider name → that provider, whether or not it is currently
//     connected (idempotent: disconnecting an unconnected provider is a no-op,
//     not an error — same spirit as resolveTargetProviders for connect/sync)
func resolveDisconnectProviders(target, csv string, connectedAgents []string) ([]string, error) {
	if csv != "" {
		if target != "" {
			return nil, fmt.Errorf("--agents cannot be used with positional provider %q", target)
		}
		return resolveProviderCSV(csv)
	}
	if target == "" || target == "all" {
		return append([]string(nil), connectedAgents...), nil
	}
	return resolveProvider(target)
}

// disconnectOptions bundles the parameters of a disconnect operation, shared by
// the CLI `disconnect` subcommand and the TUI disconnect confirmation (tui.go).
type disconnectOptions struct {
	Providers []string
	Dir       string
	DryRun    bool
	// KeepToken leaves the private token file in place when the last provider
	// goes (reconnect, D699).
	KeepToken bool
}

// disconnectProviderResult is the per-provider outcome of a disconnect pass.
type disconnectProviderResult struct {
	Provider      string
	ConfigRemoved bool
	Pruned        []provisioning.ManagedFile
	// Workspaces is what the provider's workspace projections wrote, per
	// repository, pruned the way `workspace unbind` + `sync` would (#1324).
	Workspaces []workspacePruned
	// Refused holds why the provider's MCP config entry was left in place: its
	// config file, or a directory above it, is a symlink (D148). The provider
	// then stays connected, with its managed files and its lockfile section, so
	// the next disconnect retries and reports again. The MCP entry may already
	// have been removed (ConfigRemoved) when it is the managed files that were
	// refused.
	Refused []string
}

// workspacePruned is one repository a disconnect cleaned: Path is its root,
// Pruned the managed files removed under it.
type workspacePruned struct {
	Path   string
	Pruned []provisioning.ManagedFile
	// Kept names what was left in the repository and why: a file changed since
	// the sync, or a lockfile path that does not lie inside the repository.
	Kept []string
}

// disconnectResult is the outcome of doDisconnect.
type disconnectResult struct {
	Providers []disconnectProviderResult
	// TimerUninstalled is set when the sync timer was removed because no
	// remaining client needs it (D325). It is not an operator opt-out.
	TimerUninstalled bool
}

// keptConnected lists the providers a refusal (D148) left connected.
func (r disconnectResult) keptConnected() []string {
	var kept []string
	for _, pr := range r.Providers {
		if len(pr.Refused) > 0 {
			kept = append(kept, pr.Provider)
		}
	}
	return kept
}

// doDisconnect is the single source of truth for "disconnect" business logic,
// shared by cmdDisconnect (CLI) and the TUI. For each provider in
// opts.Providers it:
//  1. removes the MCP server entry from the provider's config file
//     (configurator.Remove);
//  2. prunes every skill file the lockfile has recorded as managed for that
//     provider (provisioning.PruneManaged) — the full managed set, not a diff
//     against a freshly fetched manifest, since disconnect must not require the
//     server to be reachable;
//  3. drops the provider from the lockfile and from .cartographer.yaml.
//
// If the lockfile ends up with no providers, the lockfile is removed.
// .cartographer.yaml is never removed (D64): with no agents left it is saved
// with an empty agents list, preserving server_url/server_name/auth/token/
// kbs as the defaults for the next connect. DryRun computes everything
// without writing.
func doDisconnect(opts disconnectOptions) (disconnectResult, error) {
	var res disconnectResult
	if len(opts.Providers) == 0 {
		return res, nil
	}

	// Same client-state lock every other mutating path takes (D172): a
	// disconnect racing a sync would otherwise resurrect the provider entry
	// it just dropped, or lose the one the sync recorded.
	if !opts.DryRun {
		release, err := provisioning.LockClientState(opts.Dir, provisioning.DefaultClientLockTimeout)
		if err != nil {
			return res, err
		}
		defer release()
	}

	cfg, err := clientconfig.Load(opts.Dir)
	if err != nil {
		cfg = clientconfig.Default()
	} else {
		// Before the lockfile is read: a removed client's section would
		// otherwise keep len(lockFile.Providers) above zero, and the lockfile
		// and token file of a fully disconnected client would stay (D898).
		handleUnsupportedProviders(opts.Dir, cfg, !opts.DryRun)
	}
	lockPath := lockFilePath(opts.Dir)
	lockFile, err := provisioning.ReadLockFile(lockPath)
	if err != nil {
		return disconnectResult{}, fmt.Errorf("read lockfile: %w", err)
	}

	claudeDirs, _ := claudeConfigDirs(opts.Dir, cfg)
	var disconnected []string // the providers whose entry was not refused
	for _, p := range opts.Providers {
		pr := disconnectProviderResult{Provider: p}

		_, removed, err := removeMCPEntries(cfg.ServerName, cfg.KnownKBs, []string{p}, opts.Dir, claudeDirs, cfg.Auth, cfg.TokenEnv(), opts.DryRun)
		if errors.Is(err, safewrite.ErrSymlinkDestination) {
			// One provider's symlinked config does not stop the others.
			pr.Refused = refusalWarnings(err)
			res.Providers = append(res.Providers, pr)
			continue
		}
		if err != nil {
			return disconnectResult{}, fmt.Errorf("remove config for %s: %w", p, err)
		}
		pr.ConfigRemoved = removed[p]

		lock := lockFile.ForProvider(p)
		// The managed paths are relative to the base dir the provider actually
		// materialized under, which the Lock records when it is not opts.Dir
		// (D141) — pruning against the wrong one silently no-ops.
		pruned, err := provisioning.PruneManaged(lock.Managed, provisioning.LockBaseDir(lock, opts.Dir), opts.DryRun)
		var pruneRefused *provisioning.PruneRefusedError
		if errors.As(err, &pruneRefused) {
			// Some managed files sit behind a symlink (D148): what could be
			// pruned was, and the provider stays connected with its whole
			// lockfile section so the next disconnect retries the rest.
			pr.Pruned = pruned
			pr.Refused = append(pr.Refused, pruneRefused.Error())
			res.Providers = append(res.Providers, pr)
			continue
		}
		if err != nil {
			return disconnectResult{}, fmt.Errorf("prune skills for %s: %w", p, err)
		}
		pr.Pruned = pruned

		// The provider's workspace projections go with it (#1324): the files the
		// lockfile records for them, exactly as `workspace unbind` + `sync` would.
		// Without this the repositories keep the files while the binding that
		// pointed at them is gone, and nothing would ever look at them again.
		wsRefused, err := pruneProviderWorkspaces(&lockFile, p, opts.Dir, opts.DryRun, &pr)
		if err != nil {
			return disconnectResult{}, err
		}
		if len(wsRefused) > 0 {
			pr.Refused = append(pr.Refused, wsRefused...)
			res.Providers = append(res.Providers, pr)
			continue
		}

		if !opts.DryRun {
			delete(lockFile.Providers, p)
		}
		disconnected = append(disconnected, p)
		res.Providers = append(res.Providers, pr)
	}

	if opts.DryRun {
		return res, nil
	}

	if len(lockFile.Providers) == 0 && len(disconnected) == len(opts.Providers) {
		// The token file goes with the last provider (D699). reconnect keeps it:
		// it disconnects every provider and connects them again, possibly from
		// a shell without the variable.
		if !opts.KeepToken {
			if err := clientconfig.RemoveToken(opts.Dir); err != nil {
				return disconnectResult{}, err
			}
		}
	}
	// The lockfile goes with the last provider too, unless a workspace section
	// of a provider kept connected (a refusal) is still in it.
	if len(lockFile.Providers) == 0 && len(lockFile.Workspaces) == 0 && len(disconnected) == len(opts.Providers) {
		if err := os.Remove(lockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return disconnectResult{}, fmt.Errorf("remove lockfile: %w", err)
		}
	} else if err := provisioning.WriteLockFile(lockPath, lockFile); err != nil {
		return disconnectResult{}, err
	}

	// cfg (server_url/server_name/auth/token/kbs) is preserved even
	// when Agents ends up empty (D64): it seeds the next `connect` — server_url
	// in particular, so a disconnect-then-reconnect doesn't fall back to
	// the local default when the user was pointed at a real server. Only
	// the agents list is zeroed; the file itself is never deleted.
	cfg.Agents = removeStrings(cfg.Agents, disconnected)
	// The bindings of a disconnected provider go too: what they pointed at has
	// just been cleaned, and a binding of a provider that is not connected
	// would deliver nothing.
	for _, p := range disconnected {
		delete(cfg.Workspaces, p)
		delete(cfg.Scopes, p)
	}
	if err := clientconfig.Save(opts.Dir, cfg); err != nil {
		return disconnectResult{}, err
	}

	// D325: the timer exists for clients with no reliable session hook; when
	// none remains it has no purpose. hooklessProviders, not
	// providersNeedingSyncTimer: the latter returns nil while the timer is
	// installed, which would read as "nobody needs it". SyncTimerOptOut is left
	// alone: this removal is not the operator's choice.
	if len(hooklessProviders(cfg.Agents)) == 0 {
		if st, err := syncTimerStatusFn(); err == nil && st.Installed {
			if err := syncTimerUninstallFn(); err == nil {
				res.TimerUninstalled = true
			}
		}
	}

	return res, nil
}

// printDisconnectSummary prints a one-line-per-outcome summary of a disconnect
// pass (removed/pruned/skipped), mirroring printApplySummary's style.
func printDisconnectSummary(res disconnectResult, dryRun bool) {
	prefix := ""
	if dryRun {
		prefix = "[dry-run] "
	}
	for _, pr := range res.Providers {
		if len(pr.Refused) > 0 {
			if pr.ConfigRemoved {
				fmt.Printf("%s[%s] removed mcp config entry\n", prefix, pr.Provider)
			}
			printPrunedFiles(pr.Provider, pr.Pruned, prefix)
			printPrunedWorkspaces(pr, prefix)
			for _, w := range pr.Refused {
				fmt.Printf("%s[%s] kept connected: %s\n", prefix, pr.Provider, w)
			}
			continue
		}
		if pr.ConfigRemoved {
			fmt.Printf("%s[%s] removed mcp config entry\n", prefix, pr.Provider)
		} else {
			fmt.Printf("%s[%s] skipped mcp config entry (not found)\n", prefix, pr.Provider)
		}
		printPrunedFiles(pr.Provider, pr.Pruned, prefix)
		printPrunedWorkspaces(pr, prefix)
		if len(pr.Pruned) == 0 && len(pr.Workspaces) == 0 {
			fmt.Printf("%s[%s] skipped skill prune (nothing managed)\n", prefix, pr.Provider)
		}
	}
	if res.TimerUninstalled {
		fmt.Println("sync timer uninstalled (no remaining client needs it)")
	}
}

func printPrunedFiles(provider string, files []provisioning.ManagedFile, prefix string) {
	seen := map[string]bool{}
	for _, mf := range files {
		clean := filepath.ToSlash(mf.Path)
		if mf.Kind == "instructions" && seen[clean] {
			continue
		}
		seen[clean] = true
		fmt.Printf("%s[%s] pruned %s\n", prefix, provider, clean)
	}
}

// removeStrings returns a copy of list with every element present in remove
// filtered out, preserving order.
func removeStrings(list, remove []string) []string {
	removeSet := make(map[string]bool, len(remove))
	for _, r := range remove {
		removeSet[r] = true
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		if !removeSet[v] {
			out = append(out, v)
		}
	}
	return out
}

// pruneProviderWorkspaces removes what provider's workspace projections wrote,
// each under its own repository, leaving what another provider's projection of
// the same repository still records (D364), and drops the projections from
// lockFile (in a dry run too, on the caller's in-memory copy, so a second
// provider disconnected in the same pass sees what the first one left). A file
// that no longer hashes to what the sync wrote is the user's now and stays, and
// so does a lockfile path that is not inside the repository: the repository is
// a working copy, not a directory Cartographer owns. It returns why a
// repository was refused (D148); the provider then stays connected and the next
// disconnect retries.
func pruneProviderWorkspaces(lockFile *provisioning.LockFile, provider, clientDir string, dryRun bool, pr *disconnectProviderResult) ([]string, error) {
	var refused []string
	for _, key := range lockFile.Projections() {
		if key.Global() || key.Provider != provider {
			continue
		}
		lock := lockFile.ForProjection(key)
		// A workspace lock records its repository as base dir (SetProjection);
		// anything else would resolve the paths against another directory.
		if lock.BaseDir != key.Workspace {
			pr.Workspaces = append(pr.Workspaces, workspacePruned{Path: key.Workspace,
				Kept: []string{fmt.Sprintf("everything: the lockfile records base dir %q for this workspace", lock.BaseDir)}})
			// Reported, not retried: nothing in this section can be trusted to be
			// ours, and keeping it would hold the provider connected for good.
			lockFile.RemoveProjection(key)
			continue
		}
		base := provisioning.LockBaseDir(lock, clientDir)
		keep := lockFile.CoOwnedPaths(key)
		managed := provisioning.WithoutCoOwned(lock.Managed, keep)

		var kept []string
		inside := make([]provisioning.ManagedFile, 0, len(managed))
		for _, mf := range managed {
			if filepath.IsAbs(mf.Path) || !filepath.IsLocal(mf.Path) {
				kept = append(kept, fmt.Sprintf("%s: not a path inside the repository", filepath.ToSlash(mf.Path)))
				continue
			}
			inside = append(inside, mf)
		}
		modified := map[string]string{}
		why := map[string]string{}
		for _, f := range provisioning.VerifyManaged(provisioning.Lock{Managed: inside, ProjectScope: lock.ProjectScope}, configurator.Provider(provider), base) {
			// Fail closed: only a file that is gone, or a registration that
			// is gone, is safe to prune. Modified, unreadable and unknown
			// (no recorded hash, pre-D138) content stays.
			switch f.Reason {
			case provisioning.DriftMissing, provisioning.DriftUnregistered, provisioning.DriftUnrunnable:
			default:
				modified[f.Kind+"\x00"+f.Name] = filepath.ToSlash(f.Path)
				why[f.Kind+"\x00"+f.Name] = f.Reason
			}
		}
		toPrune := make([]provisioning.ManagedFile, 0, len(inside))
		keptArtifacts := map[string]bool{}
		for _, mf := range inside {
			key := mf.Kind + "\x00" + mf.Name
			if path, ok := modified[key]; ok {
				if !keptArtifacts[key] {
					keptArtifacts[key] = true
					if why[key] == provisioning.DriftModified {
						kept = append(kept, path+": changed since sync")
					} else {
						kept = append(kept, path+": could not be verified ("+why[key]+")")
					}
				}
				continue
			}
			toPrune = append(toPrune, mf)
		}

		pruned, err := provisioning.PruneManaged(toPrune, base, dryRun)
		var pruneRefused *provisioning.PruneRefusedError
		if errors.As(err, &pruneRefused) || errors.Is(err, provisioning.ErrSymlinkDestination) {
			pr.Workspaces = append(pr.Workspaces, workspacePruned{Path: key.Workspace, Pruned: pruned, Kept: kept})
			refused = append(refused, fmt.Sprintf("workspace %s: %v", key.Workspace, err))
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("prune %s: %w", key.String(), err)
		}
		pr.Workspaces = append(pr.Workspaces, workspacePruned{Path: key.Workspace, Pruned: pruned, Kept: kept})
		lockFile.RemoveProjection(key)
		if dryRun {
			continue
		}
		// The exclusions go with the files, but only this provider's: another
		// provider still bound to the repository keeps its paths excluded (#656).
		if err := writeWorkspaceExclusions(key.Workspace, workspaceProviders(*lockFile, nil, key.Workspace), workspaceOwnership(lockFile, nil, nil, key.Workspace)); err != nil {
			if !provisioning.ExcludeWarning(err) {
				return nil, fmt.Errorf("prune %s: %w", key.String(), err)
			}
			fmt.Fprintf(os.Stderr, "warning: workspace %s: git exclude left unchanged: %v\n", key.Workspace, err)
		}
	}
	return refused, nil
}

// printPrunedWorkspaces names each repository a disconnect cleaned and the
// files it removed there.
func printPrunedWorkspaces(pr disconnectProviderResult, prefix string) {
	for _, w := range pr.Workspaces {
		fmt.Printf("%s[%s] cleaned the projection in %s\n", prefix, pr.Provider, w.Path)
		printPrunedFiles(pr.Provider, w.Pruned, prefix)
		for _, k := range w.Kept {
			fmt.Printf("%s[%s] kept %s\n", prefix, pr.Provider, k)
		}
	}
}
