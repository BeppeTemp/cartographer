package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/service"
)

const statusHealthTimeout = 5 * time.Second

// Indirection keeps cmdStatus's version report independently testable without
// a network connection or a real launchd/systemd service.
var (
	statusHealthFn = func(cfg *clientconfig.Config) (*client.Health, error) {
		return client.New(cfg.ServerURL, resolveToken(cfg)).WithInsecureHTTP(cfg.InsecureHTTPAllowed()).Health(statusHealthTimeout)
	}
	// statusManifestsFn builds the per-projection manifests sync applies (D193).
	statusManifestsFn = manifestsForProjections
	statusServiceFn   = func() (service.Status, error) { return service.NewManager().Status("") }
)

// cmdStatus reports the sync status of every connected provider against the
// configured server: in-sync or drift, with added/updated/removed detail.
// Exit codes: 0 in-sync, 1 drift, 2 error (missing config, unreachable server, ...).
func cmdStatus(args []string) int {
	if hasHelpFlag(args) {
		fmt.Fprintln(os.Stdout, "Usage: cartographer status [--output table|json]")
		return 0
	}
	output, remaining, err := outputFlag(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	if len(remaining) != 0 {
		fmt.Fprintln(os.Stderr, "Usage: cartographer status [--output table|json]")
		return 2
	}

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
	handleUnsupportedProviders(dir, cfg, false)
	if len(cfg.Agents) == 0 {
		return renderStatus(output, emptySnapshot(), 0)
	}
	s := snapshotForConfig(dir, cfg, true)
	s.UnresolvedPlaceholders = snapshotUnresolvedPlaceholders(dir)
	code := 0
	if s.State == "drift" {
		code = 1
	}
	if s.State == "unavailable" || s.State == "error" {
		code = 2
	}
	return renderStatus(output, s, code)
}

func renderStatus(output string, s statusSnapshot, code int) int {
	if output == "json" {
		_ = json.NewEncoder(os.Stdout).Encode(s)
		return code
	}
	if s.State == "not_configured" {
		fmt.Println("no agent connected (run `cartographer connect`)")
		return code
	}
	if s.Error != nil && s.Error.Code == startingStatusCode {
		fmt.Println(s.Error.Message)
	} else if s.Reachable {
		fmt.Printf("client %s — server %s (%s)\n", s.Client, s.Server, s.ServerURL)
	} else if s.Error != nil {
		fmt.Println(s.Error.Message)
	}
	if s.State == "version_skew" {
		fmt.Printf("version skew: client %s ≠ server %s\n", s.Client, s.Server)
		if s.Service != nil && s.Service.Installed {
			fmt.Println("local service may still run the old binary — run: cartographer upgrade-repair")
		}
	}
	// D254: informational, like the skew above — neither changes the exit code.
	if u := s.Update; u != nil {
		line := fmt.Sprintf("update available: %s (installed %s)", u.Latest, s.Client)
		if u.Command != "" {
			line += " — " + u.Command
		}
		fmt.Println(line)
	}
	if s.ServerLatest != "" {
		fmt.Printf("server update available: %s (server %s) — for the server's operator\n", s.ServerLatest, s.Server)
	}
	// The server this client's state was materialized against, when it is not
	// the one answering now (D142): inspectable without running a sync.
	if changed := snapshotMaterializedVersions(s); len(changed) > 0 {
		fmt.Printf("configured against server %s — now %s: run `cartographer reconnect` to rebuild the client configuration\n",
			strings.Join(changed, ", "), s.Server)
	}
	// D187/D288: a topology switch (per-KB entries to the single routed one)
	// is healed by the next sync, which rewrites the entries to the live shape;
	// status only reports it, it never writes.
	if s.Reachable && s.MountMode != s.ConfiguredMountMode {
		fmt.Printf("mount mode changed: configured %s, server now serves %s — run `cartographer sync` to rewrite the MCP entries\n",
			mountModeLabel(s.ConfiguredMountMode), mountModeLabel(s.MountMode))
	}
	for _, p := range s.Providers {
		if !p.Connected {
			continue
		}
		if p.State == "in_sync" {
			fmt.Printf("[%s] in-sync (revision %s)\n", p.Name, p.Revision)
			printBindingLine(p)
			printWorkspaceLines(p.Workspaces)
			if p.Kinds != "" {
				fmt.Printf("  %s\n", p.Kinds)
			}
			printShadowedInstructionsLine(p)
			printInstructionsSizeLines(p)
			continue
		}
		if p.State != "drift" {
			fmt.Printf("[%s] %s\n", p.Name, strings.ReplaceAll(p.State, "_", "-"))
			continue
		}
		fmt.Printf("[%s] drift (manifest %s, lock %s)\n", p.Name, p.Revision, p.LockRevision)
		if p.Problem != "" {
			fmt.Printf("  cannot sync: %s\n", p.Problem)
		}
		printBindingLine(p)
		printWorkspaceLines(p.Workspaces)
		if p.Kinds != "" {
			fmt.Printf("  %s\n", p.Kinds)
		}
		printShadowedInstructionsLine(p)
		printInstructionsSizeLines(p)
		for _, d := range p.Diverged {
			fmt.Printf("  diverged on disk (%s): %s/%s at %s\n", d.Trust, d.Kind, d.Name, d.Path)
			if d.Detail != "" {
				fmt.Printf("    %s\n", d.Detail)
			}
		}
		approvalPending := false
		for _, a := range p.Added {
			trust := statusArtifactTrust(a)
			fmt.Printf("  + %s/%s [%s] trust=%s\n", a.Kind, a.Name, a.Source, trust)
			if trust == "needs_approval" || trust == "approval_stale" {
				approvalPending = true
			}
			if a.Kind == "hook" {
				printHookRegistrationHint(p.Name, a, "new hook")
			}
		}
		for _, a := range p.Updated {
			trust := statusArtifactTrust(a)
			fmt.Printf("  ~ %s/%s [%s] trust=%s\n", a.Kind, a.Name, a.Source, trust)
			if trust == "needs_approval" || trust == "approval_stale" {
				approvalPending = true
			}
			if a.Kind == "hook" {
				printHookRegistrationHint(p.Name, a, "hook updated")
			}
		}
		for _, a := range p.Removed {
			fmt.Printf("  - %s/%s (%s)\n", a.Kind, a.Name, a.Path)
		}
		if approvalPending {
			fmt.Println("  approval required: cartographer approve <kind> <name> --kb <kb>")
		}
	}

	// D262: informational, never a drift — the sync left the keys verbatim.
	if n := len(s.UnresolvedPlaceholders); n > 0 {
		fmt.Printf("%d placeholder(s) unresolved — run `cartographer paths`\n", n)
	}

	// D140: a connected provider with no session hook syncs only on demand.
	// Once per invocation, not once per provider.
	var connected []string
	for _, p := range s.Providers {
		// D320: a SessionHookLimit connect already showed is not repeated
		// here; doctor still reports it. A provider with no hook at all is
		// always named.
		if p.Connected && !(p.HookLimitAcked && provisioning.SessionHookLimit(configurator.Provider(p.Name)) != "") {
			connected = append(connected, p.Name)
		}
	}
	printSyncTimerHint(connected)

	return code
}

// printHookRegistrationHint says where the sync registers a pending hook.
// Every provider with a hook destination is registered by Cartographer itself
// (provisioning.hookMechanisms), so the line names the provider's own
// registration, read from the exported accessors rather than a second table,
// and asks nothing of the user. The file is relative to the scope's base
// directory: a provider with no registration of its own prints nothing.
func printHookRegistrationHint(provider string, a statusArtifact, lead string) {
	p := configurator.Provider(provider)
	// The same resolution sync prints with (#1300): a derived workspace binding
	// registers in its untracked .claude/settings.local.json.
	rel, plugin := hookRegistrationTarget(p, a.Name, provisioning.HookPluginRelPath(p, a.Name), a.HookSettings)
	switch {
	case rel == "":
	case plugin:
		fmt.Printf("    %s: the sync registers it itself, as the generated file %s\n", lead, filepath.ToSlash(rel))
	default:
		fmt.Printf("    %s: the sync registers it itself in %s\n", lead, filepath.ToSlash(rel))
	}
}

func statusArtifactTrust(a statusArtifact) string {
	if a.Trust != "" {
		return a.Trust
	}
	if a.Signed && a.Kind != "mcp" {
		return "verified"
	}
	return "needs_approval"
}

// printBindingLine reports which KBs a provider may receive and where that
// answer came from, plus the per-KB breakdown of what it actually holds (D170).
// The origin is not decoration: the same list of names means "declared" or
// "whatever the server happens to mount today" depending on it.
func printBindingLine(p providerStatus) {
	if p.BindingOrigin == "" {
		return
	}
	kbs := "none"
	if len(p.BoundKBs) > 0 {
		kbs = strings.Join(p.BoundKBs, ", ")
	}
	fmt.Printf("  kbs %s (%s)\n", kbs, p.BindingOrigin)
	if len(p.KBCounts) == 0 {
		return
	}
	sources := make([]string, 0, len(p.KBCounts))
	for source := range p.KBCounts {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	parts := make([]string, 0, len(sources))
	for _, source := range sources {
		parts = append(parts, fmt.Sprintf("%s %d", source, p.KBCounts[source]))
	}
	fmt.Printf("  from %s\n", strings.Join(parts, " · "))
}

// printShadowedInstructionsLine reports an instructions block that was written
// correctly into a file the provider does not read (D189). It reads as a fact
// about the provider's own precedence, not as a Cartographer failure, because
// that is what it is — and it says what is in force instead, since "not active"
// on its own leaves the operator without a next step.
func printShadowedInstructionsLine(p providerStatus) {
	if p.ShadowedInstructions == "" {
		return
	}
	fmt.Printf("  instructions not active: %s takes precedence and is not merged — run `cartographer doctor` for the two ways out\n",
		p.ShadowedInstructions)
}

// instructionsSizeLine is the warning for one over-threshold instructions
// block (D1182): the size, the tokens it costs on every model call
// (approximate), the largest KB regions and the way out.
func instructionsSizeLine(provider string, b instructionsBlockStatus) string {
	parts := make([]string, len(b.Largest))
	for i, k := range b.Largest {
		parts[i] = fmt.Sprintf("%s %.1f KiB", k.KB, float64(k.Bytes)/1024)
	}
	return fmt.Sprintf("[%s] instructions block %.1f KiB (~%.1fk tokens, approximate) over %d KiB: %s; keep curated text out of the block (drop `block: full`) or trim the skill list",
		provider, float64(b.Bytes)/1024, float64(b.ApproxTokens)/1000, b.Threshold/1024, strings.Join(parts, ", "))
}

// printInstructionsSizeLines prints the size warning of each over-threshold
// block. It informs and changes neither the state nor the exit status, like the
// unresolved-placeholders note (D262).
func printInstructionsSizeLines(p providerStatus) {
	for _, b := range p.InstructionsBlocks {
		if b.Over {
			fmt.Println(instructionsSizeLine(p.Name, b))
		}
	}
}

// mountModeLabel names a mount mode for a human, including the historical one
// that is recorded as an empty string.
func mountModeLabel(mode string) string {
	if mode == "" {
		return "one endpoint per KB"
	}
	return mode
}

// printInstructionsSizeWarnings tells, after a sync, which providers' managed
// instructions block is over the threshold (D1182). It reads the lockfile the
// apply just wrote, so a block that was not written (a refused write, a derived
// workspace binding) is not measured; a dry run wrote nothing and prints
// nothing. Informational: never an exit code.
func printInstructionsSizeWarnings(dir string, results map[string]provisioning.AppliedResult, dryRun bool) {
	if dryRun {
		return
	}
	lockFile, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		return
	}
	// snapshotInstructionsBlocks walks every projection of a provider, so a
	// provider is visited once however many workspaces it was applied to.
	seen := map[string]bool{}
	for _, key := range sortedKeys(results) {
		name, _ := splitProjectionKey(key)
		if seen[name] {
			continue
		}
		seen[name] = true
		for _, b := range snapshotInstructionsBlocks(lockFile, name, dir) {
			if b.Over {
				fmt.Println(instructionsSizeLine(name, b))
			}
		}
	}
}
