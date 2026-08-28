package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
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

	if _, err := runSync(dir, cfg, syncOptions{DryRun: *dryRun, AutoTrust: *autoTrust, NoHeal: *noHeal}); err != nil {
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
	// Reconcile the provider MCP entries from the mounted KB list before
	// sync_pull. On an unreachable server no local entry or persisted KB list
	// changes; fetchMergedManifest below then reports the ordinary sync error.
	facts, healthErr := enumerateKBs(cfg.ServerURL, cfg.Auth, cfg.TokenEnv)
	kbs := facts.Names
	if healthErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: MCP entry reconciliation skipped, server unreachable: %v\n", healthErr)
	} else {
		// The server that answers now may not be the one this client's state
		// was materialized against (D142): say so once, then sync normally.
		if notice := serverChangeNotice(dir, cfg.Agents, facts.Version); notice != "" {
			fmt.Println(notice)
		}
		entryKBs := kbs
		if !facts.Listed {
			entryKBs = nil
		}
		entries, err := entriesForKBs(cfg.ServerName, cfg.ServerURL, entryKBs)
		if err != nil {
			return syncResult{}, err
		}
		if _, err := removeMCPEntries(cfg.ServerName, cfg.KBs, cfg.Agents, dir, cfg.Auth, cfg.TokenEnv, opts.DryRun); err != nil {
			return syncResult{}, err
		}
		_, warnings, err := applyMCPEntries(entries, cfg.Agents, dir, cfg.Auth, cfg.TokenEnv, opts.DryRun)
		if err != nil {
			return syncResult{}, err
		}
		if w := kiroFlatNamespaceWarning(cfg.Agents, entries, effectiveToolPrefixes(facts, healthErr), healthErr); w != "" {
			warnings = append(warnings, w)
		}
		for _, w := range warnings {
			fmt.Fprintf(os.Stderr, "warning: %s\n", w)
		}
		printMCPEntryLines(cfg.Agents, entryNames(entries), opts.DryRun)
		if !opts.DryRun {
			cfg.KBs = kbs
			if err := clientconfig.Save(dir, cfg); err != nil {
				return syncResult{}, err
			}
		}
	}

	m, err := fetchMergedManifest(cfg)
	if err != nil {
		return syncResult{}, err
	}

	// Do not write even the local bootstrap hook until the complete remote
	// manifest has passed its content and signature checks.
	if err := ensureBootstrapForProviders(cfg.Agents, dir, opts.DryRun); err != nil {
		return syncResult{}, err
	}

	results, err := materializeForProviders(m, cfg.Agents, dir, facts.Version, cfg.Trust || opts.AutoTrust, opts.DryRun, opts.NoHeal, cfg.SearchRoots, cfg.Paths, cfg.ApprovedMCPHashes())
	if err != nil {
		return syncResult{}, err
	}
	printApplySummary(dir, results, opts.DryRun)
	if opts.DryRun {
		fmt.Printf("would sync to revision %s\n", m.Revision)
		return syncResult{Revision: m.Revision}, nil
	}
	fmt.Printf("synced to revision %s\n", m.Revision)
	return syncResult{Revision: m.Revision}, nil
}
