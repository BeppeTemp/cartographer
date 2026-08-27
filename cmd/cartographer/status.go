package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/service"
)

const statusHealthTimeout = 5 * time.Second

// Indirection keeps cmdStatus's version report independently testable without
// a network connection or a real launchd/systemd service.
var (
	statusHealthFn = func(cfg *clientconfig.Config) (*client.Health, error) {
		return client.New(cfg.ServerURL, resolveToken(cfg)).Health(statusHealthTimeout)
	}
	statusManifestFn = fetchMergedManifest
	statusServiceFn  = func() (service.Status, error) { return service.NewManager().Status("") }
)

// cmdStatus reports the sync status of every connected provider against the
// configured server: in-sync or drift, with added/updated/removed detail.
// Exit codes: 0 in-sync, 1 drift, 2 error (missing config, unreachable server, ...).
func cmdStatus(args []string) int {
	output, remaining, err := outputFlag(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	if len(remaining) != 0 {
		fmt.Fprintln(os.Stderr, "Error: usage: cartographer status [--output table|json]")
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
	if len(cfg.Agents) == 0 {
		return renderStatus(output, emptySnapshot(), 0)
	}
	s := snapshotForConfig(dir, cfg, true)
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
	if s.Reachable {
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
	// The server this client's state was materialized against, when it is not
	// the one answering now (D142): inspectable without running a sync.
	if changed := snapshotMaterializedVersions(s); len(changed) > 0 {
		fmt.Printf("configured against server %s — now %s: run `cartographer reconnect` to rebuild the client configuration\n",
			strings.Join(changed, ", "), s.Server)
	}
	for _, p := range s.Providers {
		if !p.Connected {
			continue
		}
		if p.State == "in_sync" {
			fmt.Printf("[%s] in-sync (revision %s)\n", p.Name, p.Revision)
			if p.Kinds != "" {
				fmt.Printf("  %s\n", p.Kinds)
			}
			continue
		}
		if p.State != "drift" {
			fmt.Printf("[%s] %s\n", p.Name, strings.ReplaceAll(p.State, "_", "-"))
			continue
		}
		fmt.Printf("[%s] drift (manifest %s, lock %s)\n", p.Name, p.Revision, p.LockRevision)
		if p.Kinds != "" {
			fmt.Printf("  %s\n", p.Kinds)
		}
		for _, d := range p.Diverged {
			fmt.Printf("  diverged on disk (%s): %s/%s at %s\n", d.Trust, d.Kind, d.Name, d.Path)
		}
		unsigned, mcpPending := false, false
		for _, a := range p.Added {
			trust := statusArtifactTrust(a)
			fmt.Printf("  + %s/%s [%s] trust=%s\n", a.Kind, a.Name, a.Source, trust)
			if a.Kind == "mcp" && (trust == "needs_approval" || trust == "approval_stale") {
				mcpPending = true
			} else {
				unsigned = unsigned || trust == "needs_approval"
			}
			if a.Kind == "hook" {
				fmt.Printf("    new hook: after the sync, add the entry to settings.json manually (see hook.json in .claude/hooks/%s/)\n", a.Name)
			}
		}
		for _, a := range p.Updated {
			trust := statusArtifactTrust(a)
			fmt.Printf("  ~ %s/%s [%s] trust=%s\n", a.Kind, a.Name, a.Source, trust)
			if a.Kind == "mcp" && (trust == "needs_approval" || trust == "approval_stale") {
				mcpPending = true
			} else {
				unsigned = unsigned || trust == "needs_approval"
			}
			if a.Kind == "hook" {
				fmt.Printf("    hook updated: after the sync, verify the entry in settings.json (see hook.json in .claude/hooks/%s/)\n", a.Name)
			}
		}
		for _, a := range p.Removed {
			fmt.Printf("  - %s/%s (%s)\n", a.Kind, a.Name, a.Path)
		}
		if unsigned {
			fmt.Printf("  to approve the unsigned artifacts run: %s\n", autoTrustCommand())
		}
		if mcpPending {
			fmt.Println("  MCP requires point approval: cartographer approve mcp <name> --kb <kb>")
		}
	}

	// D140: a connected provider with no session hook syncs only on demand.
	// Once per invocation, not once per provider.
	var connected []string
	for _, p := range s.Providers {
		if p.Connected {
			connected = append(connected, p.Name)
		}
	}
	printSyncTimerHint(connected)

	return code
}

func statusArtifactTrust(a statusArtifact) string {
	if a.Trust != "" {
		return a.Trust
	}
	if a.Signed {
		return "verified"
	}
	return "needs_approval"
}

// printVersionStatus reports the binary versions before the artifact status.
// A failed health request is intentionally non-fatal here: the following
// sync_pull still performs the existing artifact check and preserves its exit
// code/error behaviour. Version skew is advisory, never provisioning drift.
func printVersionStatus(cfg *clientconfig.Config) {
	health, err := statusHealthFn(cfg)
	if err != nil {
		fmt.Printf("client %s — server unreachable (%s)\n", version, cfg.ServerURL)
		return
	}

	fmt.Printf("client %s — server %s (%s)\n", version, health.Version, cfg.ServerURL)
	if version == "" || health.Version == "" || version == "dev" || health.Version == "dev" || version == health.Version {
		return
	}

	fmt.Printf("version skew: client %s ≠ server %s\n", version, health.Version)
	if !isLoopbackURL(cfg.ServerURL) {
		return
	}
	if st, err := statusServiceFn(); err == nil && st.Installed {
		fmt.Println("local service may still run the old binary — run: cartographer upgrade-repair")
	}
}
