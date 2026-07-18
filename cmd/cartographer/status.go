package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
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

	m, err := fetchMergedManifest(cfg)
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
