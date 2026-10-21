package main

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
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
	autoTrust := fs.Bool("auto-trust", false, "Removed (D906): accepted and ignored, because session-start hooks written by older versions still pass it")
	noHeal := fs.Bool("no-heal", false, "Report locally modified managed artifacts instead of restoring them from the server")
	logFile := fs.String("log-file", "", "Append this run's output to this file instead of stdout/stderr (created if absent; never rotated)")
	fs.Parse(args)
	warnRemovedAutoTrust(*autoTrust)

	// A scheduled sync has no console to report to: on Windows the task's own
	// history records exit codes only, and is disabled by default on many
	// machines, so without this a failing background sync leaves nothing to read
	// (D217). Redirected before the first line is printed.
	if *logFile != "" {
		f, err := redirectClientOutput(*logFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 2
		}
		defer f.Close()
	}

	dir, err := clientconfig.TargetDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	if !*dryRun {
		realignClientConfig(dir)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: no client config found in %s (run `cartographer connect` first): %v\n", dir, err)
		return 2
	}
	for _, w := range cfg.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s: %s\n", clientconfig.Path(dir), w)
	}
	if len(cfg.Agents) == 0 {
		// Nothing left to sync, but a config that listed only a client the
		// registry dropped (D898) must still be cleaned and say why.
		if !*dryRun {
			release, err := provisioning.LockClientState(dir, provisioning.DefaultClientLockTimeout)
			if err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				return 2
			}
			defer release()
		}
		handleUnsupportedProviders(dir, cfg, !*dryRun)
		fmt.Println("no agent connected (run `cartographer connect`)")
		return 0
	}

	if _, err := runSync(dir, cfg, syncOptions{DryRun: *dryRun, NoHeal: *noHeal, Clients: clients, RepairService: true}); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	if !*dryRun {
		// A brew upgrade runs neither connect nor upgrade-repair: the scheduled
		// sync is what reaches those clients (D704).
		removeLegacyDoctorTimer()
	}
	return 0
}

// syncOptions carries runSync's policy knobs. Both cmdSync and
// cartographer upgrade-repair (D121) construct this from an already loaded
// clientconfig.Config.
type syncOptions struct {
	DryRun bool
	// Clients restricts the run to these providers (--client, repeatable).
	// Empty means every connected provider. A provider left out is not
	// touched at all: not its MCP entries, not its artifacts, not its entry in
	// the lockfile (D170).
	Clients []string
	// NoHeal reports artifacts whose files diverged on disk instead of
	// restoring them (D139). upgrade-repair never sets it: repairing is its
	// entire purpose.
	NoHeal bool
	// RepairService replaces a native local service still running the
	// previous binary before syncing (D199). Only cmdSync sets it:
	// upgrade-repair has already verified the service itself.
	RepairService bool
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

	// Before the lock, so a run that cannot succeed takes none, and before the
	// first network call, so --dry-run is diagnosed exactly like a real sync.
	if err := preflightEnvironment(cfg); err != nil {
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
		// Under the lock, so concurrent session-start syncs replace a stale
		// service once: the next one in line finds it already current.
		if opts.RepairService {
			repairStaleServiceBeforeSync(cfg.ServerURL)
		}
	}
	// Under the lock: it rewrites the config and the lockfile (D898).
	handleUnsupportedProviders(dir, cfg, !opts.DryRun)

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
	facts, healthErr := enumerateKBs(cfg.ServerURL, cfg.Auth, cfg.Token, cfg.InsecureHTTPAllowed())
	kbs := facts.Names
	var entriesByProvider map[string][]mcpEntry
	if healthErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: MCP entry reconciliation skipped, server unreachable: %v\n", healthErr)
	} else {
		// The server that answers now may not be the one this client's state
		// was materialized against (D142): say so once, then sync normally.
		// The sync wording, not the status/doctor one: this run is the repair
		// (D220).
		if notice := syncServerChangeNotice(dir, targets, facts.Version); notice != "" {
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

	// D193: one applied state per projection. A provider in the default scope
	// has exactly one — the global catalogue, byte-identical to before — and a
	// workspace-scoped one has a bundle-only global projection plus one per
	// bound workspace.
	projections, err := allProjections(cfg, targets, dir)
	if err != nil {
		return syncResult{}, fmt.Errorf("%w (no configuration was modified)", err)
	}
	manifests, pullFailures, err := manifestsForProjectionsPartial(cfg, projections)
	if err != nil {
		return syncResult{}, fmt.Errorf("%w (no configuration was modified)", err)
	}
	// A KB that could not be pulled costs only the providers bound to it
	// (#350): they are dropped from this run before anything is written, so
	// they keep their previous state exactly as --client leaves out a
	// provider, and the run reports them at the end.
	var skipped []string
	targets, projections, skipped = dropUnbuiltProviders(targets, projections, manifests)
	for _, provider := range skipped {
		// Also out of the printed plan: its entries are not being written.
		delete(entriesByProvider, provider)
	}
	if len(targets) == 0 {
		return syncResult{}, fmt.Errorf("%w (no configuration was modified)", partialSyncError{Failures: pullFailures, Skipped: skipped})
	}

	if healthErr == nil {
		// removeMCPEntries is fed the UNION of every known KB, never a
		// provider's filtered binding (D170): managedEntryNames derives the
		// names this client may own from the list it is given, so passing the
		// filtered one would orphan an unbound KB's entry forever. Only the
		// targeted providers are touched — a provider skipped by --client keeps
		// its entries untouched.
		claudeDirs, claudeDirWarnings := claudeConfigDirs(dir, cfg)
		removedNames, _, err := removeMCPEntries(cfg.ServerName, previousKnownKBs, targets, dir, claudeDirs, cfg.Auth, cfg.TokenEnv(), opts.DryRun)
		if err != nil {
			return syncResult{}, err
		}
		authorWarnings := setEntryAuthors(cfg, entriesByProvider, globalIdentFn)
		_, warnings, err := applyMCPEntries(entriesByProvider, targets, dir, claudeDirs, cfg.Auth, cfg.TokenEnv(), opts.DryRun)
		if err != nil {
			return syncResult{}, err
		}
		warnings = append(warnings, authorWarnings...)
		if w := literalTokenWarning(cfg.Auth, cfg.Token); w != "" {
			warnings = append(warnings, w)
		}
		if slices.Contains(targets, string(configurator.ProviderClaudeCode)) {
			warnings = append(warnings, claudeDirWarnings...)
		}
		if w := kiroFlatNamespaceWarning(targets, entriesByProvider, effectiveToolPrefixes(facts, healthErr), healthErr); w != "" {
			warnings = append(warnings, w)
		}
		if w := toolIdentifierBudgetWarning(targets, entriesByProvider, facts, healthErr); w != "" {
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

	results, err := materializeForProviders(manifests, projections, dir, facts.Version, opts.DryRun, opts.NoHeal, portabilityOptions{SearchRoots: cfg.ScanRoots(), Paths: cfg.Paths}, kbOrderForProviders(cfg, targets), cfg.ApprovedMCPHashes())
	if err != nil {
		return syncResult{}, err
	}
	printApplySummary(dir, results, opts.DryRun)
	printSyncRevisions(results, targets, opts.DryRun)
	// D326: after the apply, so the lockfile names what was just materialised.
	// A dry run changes nothing and neither does it report, and an unreachable
	// server has nobody to report to.
	if !opts.DryRun && healthErr == nil {
		reportUsage(cfg, dir)
		reportClientSettings(cfg, dir)
	}
	if len(pullFailures) > 0 {
		return syncResult{Revision: commonRevision(results, targets)}, partialSyncError{Failures: pullFailures, Skipped: skipped}
	}
	return syncResult{Revision: commonRevision(results, targets)}, nil
}

// partialSyncError reports a sync that went ahead without some KBs (#350):
// the KBs whose pull failed, and the providers left untouched because they
// are bound to one of them. It is an error — the run did not do everything it
// was asked — but the providers not named here were applied.
type partialSyncError struct {
	Failures []kbPullFailure
	Skipped  []string
}

func (e partialSyncError) Error() string {
	var b strings.Builder
	if len(e.Failures) == 1 {
		b.WriteString("1 KB could not be pulled:")
	} else {
		fmt.Fprintf(&b, "%d KBs could not be pulled:", len(e.Failures))
	}
	for _, f := range e.Failures {
		fmt.Fprintf(&b, "\n  %s: %v", f.KB, f.Err)
	}
	if len(e.Skipped) > 0 {
		fmt.Fprintf(&b, "\nproviders bound to it kept their previous state: %s", strings.Join(e.Skipped, ", "))
	}
	return b.String()
}

// dropUnbuiltProviders removes from the run every provider that has a
// projection with no manifest — the ones manifestsForProjectionsPartial left
// out because they need a KB that could not be pulled. The whole provider is
// dropped, not only that projection: its MCP entries, bootstrap and lockfile
// entry then stay exactly as they were, which is what "kept its previous
// state" has to mean for the operator reading the report.
func dropUnbuiltProviders(targets []string, projections []syncProjection, manifests map[string]provisioning.Manifest) ([]string, []syncProjection, []string) {
	unbuilt := map[string]bool{}
	for _, p := range projections {
		if _, ok := manifests[projectionKey(p)]; !ok {
			unbuilt[p.Provider] = true
		}
	}
	if len(unbuilt) == 0 {
		return targets, projections, nil
	}
	var keptTargets, skipped []string
	for _, t := range targets {
		if unbuilt[t] {
			skipped = append(skipped, t)
		} else {
			keptTargets = append(keptTargets, t)
		}
	}
	var keptProjections []syncProjection
	for _, p := range projections {
		if !unbuilt[p.Provider] {
			keptProjections = append(keptProjections, p)
		}
	}
	sort.Strings(skipped)
	return keptTargets, keptProjections, skipped
}

// preflightEnvironment reports, in ONE error, every environment variable the
// sync needs and does not have. Without it a missing bearer token surfaces only
// as a 401 from sync_pull, deep in runSync (D222; the provider base directory
// it also used to check was removed with a client, D898).
// Whoever hits this is normally not at a console: the session-start bootstrap
// hook and the sync timer start from an environment that never inherited a
// login shell's exports.
//
// It is read-only and never contacts the server: it checks that a token
// EXISTS, not that it is accepted — only the server can judge that, and a wrong
// token is still a 401 further down. Nothing downstream is removed; this only
// moves the diagnosis earlier.
func preflightEnvironment(cfg *clientconfig.Config) error {
	// Before the lock and before any write: a refused URL leaves every file as
	// it was (D1023).
	if err := client.CheckServerURL(cfg.ServerURL, cfg.InsecureHTTPAllowed()); err != nil {
		return err
	}
	var missing []string

	// The same condition resolveToken applies: auth off, no token, or a literal
	// one, means nothing is missing from the environment here. An
	// exported-but-empty variable is a missing one — it sends nothing.
	// The token file (D699) counts as present: it is how an unattended run
	// gets the token. An unusable file is named, not reported as plain absent.
	if cfg.Auth && cfg.TokenEnv() != "" && cfg.LiteralToken() == "" {
		if tok, terr := resolveTokenErr(cfg); tok == "" {
			line := fmt.Sprintf("  $%s — bearer token for %s (or a private token file: run `cartographer reconnect` with $%[1]s set to write it)", cfg.TokenEnv(), cfg.ServerURL)
			if terr != nil {
				line += "\n    token file unusable: " + terr.Error()
			}
			missing = append(missing, line)
		}
	}

	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("sync needs environment variables that are not set (an unattended sync — session-start hook, timer, CI — does not inherit a login shell's exports):\n%s\n(no configuration was modified)",
		strings.Join(missing, "\n"))
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

// warnRemovedAutoTrust says once that --auto-trust no longer does anything
// (D906). The flag is still accepted: the session-start hook an older version
// wrote runs `cartographer sync --auto-trust`, and a flag error there would
// stop the very sync that rewrites the hook.
func warnRemovedAutoTrust(passed bool) {
	if passed {
		fmt.Fprintln(os.Stderr, "warning: --auto-trust is removed and ignored: everything a KB ships is installed, and an MCP descriptor needs `cartographer approve`")
	}
}
