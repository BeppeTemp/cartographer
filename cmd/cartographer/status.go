package main

import (
	"flag"
	"fmt"
	"os"
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
		return client.New(cfg.ServerURL, resolveToken(cfg)).Health(statusHealthTimeout)
	}
	statusManifestFn = fetchMergedManifest
	statusServiceFn  = func() (service.Status, error) { return service.NewManager().Status("") }
)

// cmdStatus reports the sync status of every connected provider against the
// configured server: in-sync or drift, with added/updated/removed detail.
// Exit codes: 0 in-sync, 1 drift, 2 error (missing config, unreachable server, ...).
func cmdStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
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

	printVersionStatus(cfg)

	m, err := statusManifestFn(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	// cfg.Trust (persisted at connect time, see D54) upgrades kb:-sourced
	// artifacts to Signed:true before the diff, so the reported status is
	// honest: a trusted server never shows a leftover "needs approval".
	m = upgradeTrustedManifest(m, cfg.Trust)

	lockFile, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}

	drift := false
	for _, p := range cfg.Agents {
		lock := lockFile.ForProvider(p)
		// Diff and counts only over the kinds the provider supports (see
		// FilterForProvider): hook does not count as drift for opencode & co.
		// (agent does for opencode, D55 — it stays excluded only for codex/kiro).
		pm := provisioning.FilterForProvider(m, configurator.Provider(p))
		d := provisioning.ComputeDiff(pm, lock)
		if d.InSync {
			fmt.Printf("[%s] in-sync (revision %s)\n", p, pm.Revision)
			if kindLine := formatKindStatus(pm, lock); kindLine != "" {
				fmt.Printf("  %s\n", kindLine)
			}
			continue
		}
		drift = true
		fmt.Printf("[%s] drift (manifest %s, lock %s)\n", p, pm.Revision, lock.AppliedRevision)
		if kindLine := formatKindStatus(pm, lock); kindLine != "" {
			fmt.Printf("  %s\n", kindLine)
		}
		unsigned := false
		for _, a := range d.Added {
			fmt.Printf("  + %s/%s [%s] signed=%v\n", a.Kind, a.Name, a.Source, a.Signed)
			unsigned = unsigned || !a.Signed
			if a.Kind == "hook" {
				fmt.Printf("    new hook: after the sync, add the entry to settings.json manually (see hook.json in .claude/hooks/%s/)\n", a.Name)
			}
		}
		for _, a := range d.Updated {
			fmt.Printf("  ~ %s/%s [%s] signed=%v\n", a.Kind, a.Name, a.Source, a.Signed)
			unsigned = unsigned || !a.Signed
			if a.Kind == "hook" {
				fmt.Printf("    hook updated: after the sync, verify the entry in settings.json (see hook.json in .claude/hooks/%s/)\n", a.Name)
			}
		}
		for _, mf := range d.Removed {
			fmt.Printf("  - %s/%s (%s)\n", mf.Kind, mf.Name, mf.Path)
		}
		if unsigned {
			fmt.Printf("  to approve the unsigned artifacts run: %s\n", autoTrustCommand())
		}
	}

	if drift {
		return 1
	}
	return 0
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
		fmt.Println("local service may still run the old binary — run: cartographer service restart")
	}
}
