package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// cmdSync re-fetches the manifest from the configured server (sync_pull) and
// re-applies it for every connected provider: materialize add/update, prune
// obsolete managed files, update the lockfile. Idempotent — running it twice on an
// unchanged server is a no-op. Loads the client config itself, then delegates
// the actual work to runSync — the same in-process runner `upgrade-repair`
// uses (D121), so both commands share one authorization/materialization path.
func cmdSync(args []string) int {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Print what would change without writing")
	var clients repeatedString
	fs.Var(&clients, "client", "Sync only this provider (repeatable); other providers are left untouched")
	autoTrust := fs.Bool("auto-trust", false, "Trust KB-sourced skills without explicit signature (one-time override; see the persisted `trust` setting in .cartographer.yaml)")
	noHeal := fs.Bool("no-heal", false, "Report locally modified managed artifacts instead of restoring them from the server")
	fs.Parse(args)

	dir, err := clientconfig.TargetDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: no client config found in %s (run `cartographer connect` first): %v\n", dir, err)
		return 2
	}
	if len(cfg.Agents) == 0 {
		fmt.Println("no agent connected (run `cartographer connect`)")
		return 0
	}

	if _, err := runSync(dir, cfg, syncOptions{DryRun: *dryRun, AutoTrust: *autoTrust, NoHeal: *noHeal, Clients: clients}); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	return 0
}

// syncOptions carries runSync's policy knobs. Both cmdSync and
// cartographer upgrade-repair (D121) construct this from an already loaded
// clientconfig.Config; automatic repair never sets AutoTrust, so it never
// implies a one-shot trust override beyond the persisted `trust` setting.
type syncOptions struct {
	DryRun    bool
	AutoTrust bool
	// Clients restricts the run to these providers (--client, repeatable).
	// Empty means every connected provider. A provider left out is not
	// touched at all: not its MCP entries, not its artifacts, not its entry in
	// the lockfile (D170).
	Clients []string
	// NoHeal reports artifacts whose files diverged on disk instead of
	// restoring them (D139). upgrade-repair never sets it: repairing is its
	// entire purpose.
	NoHeal bool
}

// syncResult is the subset of a completed sync a caller may need beyond the
// printed progress (e.g. upgrade-repair's success message).
type syncResult struct {
	Revision string
}

// runSync is the single in-process sync implementation: reconcile the
// provider MCP entries from the mounted KB list, pull and verify the merged
// manifest, write the session-start bootstrap, and materialize artifacts for
// every provider in cfg.Agents. It accepts an already loaded target
// directory/config and returns an error instead of an exit code, so it is
// callable both from cmdSync and from upgrade-repair without spawning a
// nested cartographer process or duplicating configurator/approval/
// signature/provisioning logic.
func runSync(dir string, cfg *clientconfig.Config, opts syncOptions) (syncResult, error) {
	targets, err := selectProviders(cfg.Agents, opts.Clients)
	if err != nil {
		return syncResult{}, err
	}

	// One writer at a time across PROCESSES: the session-start bootstrap hook
	// runs `cartographer sync` per agent session, so several are routinely in
	// flight at once and the loser of that race silently drops another
	// provider's lock entry (D172). A dry run writes nothing and needs none.
	if !opts.DryRun {
		release, err := provisioning.LockClientState(dir, provisioning.DefaultClientLockTimeout)
		if err != nil {
			return syncResult{}, err
		}
		defer release()
	}

	// A plan restricted to some providers must say so: read without the
	// command line that produced it, a partial plan looks like a complete one
	// (D170 WP2, D172 WP4).
	if opts.DryRun && len(opts.Clients) > 0 {
		fmt.Printf("[dry-run] plan for %s only; other connected providers are untouched\n", strings.Join(targets, ", "))
	}

	// The KB list as it was persisted: what this client may own MCP entries
	// for. Reconciliation compares it against what the server mounts now, so
	// it must be read before cfg.KnownKBs is refreshed in memory.
	previousKnownKBs := cfg.KnownKBs

	// Nothing is written before the manifest is fetched and verified (D172).
	// The previous order rewrote the providers' MCP entries and
	// .cartographer.yaml first, so a failed sync_pull, an unverifiable
	// signature or a cross-KB collision (D171) left a machine reconfigured for
	// a sync that never happened — and the error said nothing about it.
	facts, healthErr := enumerateKBs(cfg.ServerURL, cfg.Auth, cfg.TokenEnv)
	kbs := facts.Names
	var entriesByProvider map[string][]mcpEntry
	if healthErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: MCP entry reconciliation skipped, server unreachable: %v\n", healthErr)
	} else {
		// The server that answers now may not be the one this client's state
		// was materialized against (D142): say so once, then sync normally.
		if notice := serverChangeNotice(dir, targets, facts.Version); notice != "" {
			fmt.Println(notice)
		}
		entryKBs := kbs
		if !facts.Listed {
			entryKBs = nil
		}
		// D187: the live topology wins over the persisted one, and is persisted
		// with the rest of the server-owned cache below.
		routedPath := facts.RoutedPath
		cfg.ServerRoutedPath = routedPath
		cfg.ServerMountMode = ""
		if routedPath != "" {
			cfg.ServerMountMode = "routed"
		}
		var err error
		entriesByProvider, err = entriesByProviderForKBs(cfg, targets, cfg.ServerName, cfg.ServerURL, entryKBs, routedPath)
		if err != nil {
			return syncResult{}, err
		}
		// Refresh the in-memory cache so the manifest pull below resolves
		// default bindings against what the server mounts now. Persisting it
		// is step 6: until the manifest is in hand, nothing on disk changes.
		cfg.KnownKBs = kbs
	}

	manifests, err := manifestsForProviders(cfg, targets)
	if err != nil {
		return syncResult{}, fmt.Errorf("%w (no configuration was modified)", err)
	}

	if healthErr == nil {
		// removeMCPEntries is fed the UNION of every known KB, never a
		// provider's filtered binding (D170): managedEntryNames derives the
		// names this client may own from the list it is given, so passing the
		// filtered one would orphan an unbound KB's entry forever. Only the
		// targeted providers are touched — a provider skipped by --client keeps
		// its entries untouched.
		removedNames, _, err := removeMCPEntries(cfg.ServerName, previousKnownKBs, targets, dir, cfg.Auth, cfg.TokenEnv, opts.DryRun)
		if err != nil {
			return syncResult{}, err
		}
		_, warnings, err := applyMCPEntries(entriesByProvider, targets, dir, cfg.Auth, cfg.TokenEnv, opts.DryRun)
		if err != nil {
			return syncResult{}, err
		}
		if w := kiroFlatNamespaceWarning(targets, entriesByProvider, effectiveToolPrefixes(facts, healthErr), healthErr); w != "" {
			warnings = append(warnings, w)
		}
		for _, w := range warnings {
			fmt.Fprintf(os.Stderr, "warning: %s\n", w)
		}
		printMCPEntryRemovals(targets, removedNames, allEntryNames(entriesByProvider), opts.DryRun)
		printMCPEntryLines(targets, allEntryNames(entriesByProvider), opts.DryRun)
	}

	// Do not write even the local bootstrap hook until the complete remote
	// manifest has passed its content and signature checks.
	if err := ensureBootstrapForProviders(targets, dir, opts.DryRun); err != nil {
		return syncResult{}, err
	}

	if healthErr == nil {
		printKnownKBsChange(previousKnownKBs, kbs, opts.DryRun)
		if !opts.DryRun {
			if err := clientconfig.Save(dir, cfg); err != nil {
				return syncResult{}, err
			}
		}
	}

	results, err := materializeForProviders(manifests, targets, dir, facts.Version, cfg.Trust || opts.AutoTrust, opts.DryRun, opts.NoHeal, portabilityOptions{SearchRoots: cfg.SearchRoots, SearchDepth: cfg.SearchDepth, Paths: cfg.Paths}, kbOrderForProviders(cfg, targets), cfg.ApprovedMCPHashes())
	if err != nil {
		return syncResult{}, err
	}
	printApplySummary(dir, results, opts.DryRun)
	printSyncRevisions(results, targets, opts.DryRun)
	return syncResult{Revision: commonRevision(results, targets)}, nil
}

// selectProviders narrows cfg.Agents to the ones --client named, preserving
// cfg.Agents' order so the output is stable. An empty selection means all.
func selectProviders(agents []string, selected []string) ([]string, error) {
	if len(selected) == 0 {
		return agents, nil
	}
	connected := make(map[string]bool, len(agents))
	for _, a := range agents {
		connected[a] = true
	}
	want := make(map[string]bool, len(selected))
	for _, s := range selected {
		if !connected[s] {
			return nil, fmt.Errorf("provider %q is not connected (connected: %s)", s, strings.Join(agents, ", "))
		}
		want[s] = true
	}
	out := make([]string, 0, len(want))
	for _, a := range agents {
		if want[a] {
			out = append(out, a)
		}
	}
	return out, nil
}

// commonRevision returns the revision every targeted provider shares, or "" when
// they differ. Bindings make a single machine-wide revision meaningless: two
// providers receiving different KBs legitimately sit at different revisions.
// Unsupported-kind filtering (D184) is a second source of the same divergence:
// results carries each provider's post-FilterForProvider recorded revision
// (AppliedResult.NewLock.AppliedRevision), the same value `status` compares
// against, not the pre-projection manifest revision.
func commonRevision(results map[string]provisioning.AppliedResult, providers []string) string {
	common := ""
	for i, p := range providers {
		rev := results[p].NewLock.AppliedRevision
		if i == 0 {
			common = rev
			continue
		}
		if rev != common {
			return ""
		}
	}
	return common
}

// printSyncRevisions reports one line when every provider agrees — the ordinary
// case — and one line per provider when bindings, or unsupported-kind
// filtering (D184), made them diverge, so the output never implies an
// agreement that does not exist.
func printSyncRevisions(results map[string]provisioning.AppliedResult, providers []string, dryRun bool) {
	verb := "synced to"
	if dryRun {
		verb = "would sync to"
	}
	if rev := commonRevision(results, providers); rev != "" || len(providers) <= 1 {
		fmt.Printf("%s revision %s\n", verb, rev)
		return
	}
	for _, p := range providers {
		fmt.Printf("[%s] %s revision %s\n", p, verb, results[p].NewLock.AppliedRevision)
	}
}
