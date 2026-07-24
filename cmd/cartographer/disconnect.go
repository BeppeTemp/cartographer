package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// cmdDisconnect removes the requested agent provider(s) — default "all" = every
// provider currently connected in .cartographer.yaml — from this machine: the
// MCP server entry in the provider's config file, the skill files the lockfile
// recorded as managed for it, and the provider's own entries in the lockfile and
// .cartographer.yaml. Idempotent — disconnecting an already-disconnected
// provider succeeds with exit 0.
func cmdDisconnect(args []string) int {
	target, rest := splitPositional(args, "")

	fs := flag.NewFlagSet("disconnect", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Print what would be removed without removing")
	agentsCSV := fs.String("agents", "", "Comma-separated agent subset: claude,codex")
	fs.Parse(rest)

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
}

// disconnectProviderResult is the per-provider outcome of a disconnect pass.
type disconnectProviderResult struct {
	Provider      string
	ConfigRemoved bool
	Pruned        []provisioning.ManagedFile
}

// disconnectResult is the outcome of doDisconnect.
type disconnectResult struct {
	Providers []disconnectProviderResult
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
// with an empty agents list, preserving server_url/server_name/auth/token_env/
// trust/kbs as the defaults for the next connect. DryRun computes everything
// without writing.
func doDisconnect(opts disconnectOptions) (disconnectResult, error) {
	var res disconnectResult
	if len(opts.Providers) == 0 {
		return res, nil
	}

	cfg, err := clientconfig.Load(opts.Dir)
	if err != nil {
		cfg = clientconfig.Default()
	}
	lockPath := lockFilePath(opts.Dir)
	lockFile, err := provisioning.ReadLockFile(lockPath)
	if err != nil {
		return disconnectResult{}, fmt.Errorf("read lockfile: %w", err)
	}

	for _, p := range opts.Providers {
		pr := disconnectProviderResult{Provider: p}

		removed, err := removeMCPEntries(cfg.ServerName, cfg.KBs, []string{p}, opts.Dir, cfg.Auth, cfg.TokenEnv, opts.DryRun)
		if err != nil {
			return disconnectResult{}, fmt.Errorf("remove config for %s: %w", p, err)
		}
		pr.ConfigRemoved = removed[p]

		lock := lockFile.ForProvider(p)
		pruned, err := provisioning.PruneManaged(lock.Managed, opts.Dir, opts.DryRun)
		if err != nil {
			return disconnectResult{}, fmt.Errorf("prune skills for %s: %w", p, err)
		}
		pr.Pruned = pruned

		if !opts.DryRun {
			delete(lockFile.Providers, p)
		}
		res.Providers = append(res.Providers, pr)
	}

	if opts.DryRun {
		return res, nil
	}

	if len(lockFile.Providers) == 0 {
		if err := os.Remove(lockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return disconnectResult{}, fmt.Errorf("remove lockfile: %w", err)
		}
	} else if err := provisioning.WriteLockFile(lockPath, lockFile); err != nil {
		return disconnectResult{}, err
	}

	// cfg (server_url/server_name/auth/token_env/trust/kbs) is preserved even
	// when Agents ends up empty (D64): it seeds the next `connect` — server_url
	// in particular, so a disconnect-then-reconnect doesn't fall back to
	// http://localhost:8080/mcp when the user was pointed at a real server. Only
	// the agents list is zeroed; the file itself is never deleted.
	cfg.Agents = removeStrings(cfg.Agents, opts.Providers)
	if err := clientconfig.Save(opts.Dir, cfg); err != nil {
		return disconnectResult{}, err
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
		if pr.ConfigRemoved {
			fmt.Printf("%s[%s] removed mcp config entry\n", prefix, pr.Provider)
		} else {
			fmt.Printf("%s[%s] skipped mcp config entry (not found)\n", prefix, pr.Provider)
		}
		for _, mf := range pr.Pruned {
			fmt.Printf("%s[%s] pruned %s\n", prefix, pr.Provider, mf.Path)
		}
		if len(pr.Pruned) == 0 {
			fmt.Printf("%s[%s] skipped skill prune (nothing managed)\n", prefix, pr.Provider)
		}
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
