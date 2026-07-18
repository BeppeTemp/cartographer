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
// unchanged server is a no-op.
func cmdSync(args []string) int {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Print what would change without writing")
	autoTrust := fs.Bool("auto-trust", false, "Trust KB-sourced skills without explicit signature (one-time override; see the persisted `trust` setting in .cartographer.yaml)")
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

	// The bootstrap hook (D60) is purely local — it must be guaranteed even if
	// the manifest fetch below fails (server temporarily down): it is precisely
	// the hook that, on the next session, will kick off a successful sync.
	if err := ensureBootstrapForProviders(cfg.Agents, dir, *dryRun); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}

	m, err := fetchMergedManifest(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}

	results, err := materializeForProviders(m, cfg.Agents, dir, cfg.Trust || *autoTrust, *dryRun, cfg.SearchRoots, cfg.Paths)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	printApplySummary(dir, results)
	fmt.Printf("synced to revision %s\n", m.Revision)
	return 0
}
