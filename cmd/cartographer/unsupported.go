package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// handleUnsupportedProviders is the one place a client config that names a
// provider the registry no longer knows is dealt with (D898). clientconfig.Load
// has already dropped the name from the in-memory config, so nothing below can
// resolve it to a directory or prune for it; this reports it ONCE per run and,
// when write is set, makes the drop permanent: the config is saved without the
// name and the lockfile sections of every unknown provider are removed. No file
// Cartographer delivered earlier is touched — the notice names where they are
// (the base_dir the lock recorded, when it did) so the operator can remove them.
//
// write is false for read-only commands (status, doctor) and for --dry-run: they
// report, and the first command that writes does the rest. The notice is
// derived from the lockfile too, so a name already gone from the config but
// still in the lock is cleaned without a second message.
func handleUnsupportedProviders(dir string, cfg *clientconfig.Config, write bool) {
	lf, lockErr := provisioning.ReadLockFile(lockFilePath(dir))
	if lockErr != nil {
		// A broken lockfile is reported by the code that needs it; here it only
		// means no base_dir to mention and nothing to clean.
		lf = provisioning.LockFile{}
	}
	if cfg != nil {
		for _, name := range cfg.Unsupported {
			fmt.Fprintln(os.Stderr, unsupportedProviderNotice(name, recordedBaseDir(lf, name), write))
		}
	}
	if !write {
		if cfg != nil {
			cfg.Unsupported = nil
		}
		return
	}
	if cfg != nil && len(cfg.Unsupported) > 0 {
		if err := clientconfig.Save(dir, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not remove the unsupported agent clients from %s: %v\n", clientconfig.Path(dir), err)
		}
		cfg.Unsupported = nil
	}
	if lockErr == nil && dropUnsupportedLockSections(&lf) {
		if err := provisioning.WriteLockFile(lockFilePath(dir), lf); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not drop the unsupported agent clients from the lockfile: %v\n", err)
		}
	}
}

// unsupportedProviderNotice is the message for one unknown provider name.
func unsupportedProviderNotice(name, baseDir string, removed bool) string {
	where := ""
	if baseDir != "" {
		where = " (" + baseDir + ")"
	}
	entry := "its entry is ignored and sync or connect drops it from .cartographer.yaml"
	if removed {
		entry = "its entry was removed from .cartographer.yaml"
	}
	return fmt.Sprintf("%s: this agent client is no longer supported; %s. Files Cartographer delivered earlier are left in place%s; remove them by hand.", name, entry, where)
}

// recordedBaseDir is the base dir the lock recorded for a provider's global
// section, or "" when it recorded none.
func recordedBaseDir(lf provisioning.LockFile, name string) string {
	if l, ok := lf.Providers[name]; ok {
		return l.BaseDir
	}
	return ""
}

// dropUnsupportedLockSections removes, in memory, the lock sections (global and
// workspace) of every provider the registry does not know, and reports whether
// it removed any. Only the record goes: the files it named stay on disk.
func dropUnsupportedLockSections(lf *provisioning.LockFile) bool {
	known := func(name string) bool {
		_, ok := configurator.Lookup(configurator.Provider(name))
		return ok
	}
	changed := false
	for name := range lf.Providers {
		if !known(name) {
			delete(lf.Providers, name)
			changed = true
		}
	}
	workspaces := make([]string, 0, len(lf.Workspaces))
	for ws := range lf.Workspaces {
		workspaces = append(workspaces, ws)
	}
	sort.Strings(workspaces)
	for _, ws := range workspaces {
		w := lf.Workspaces[ws]
		removed := false
		for name := range w.Providers {
			if !known(name) {
				delete(w.Providers, name)
				removed = true
			}
		}
		if !removed {
			continue
		}
		changed = true
		if len(w.Providers) == 0 {
			delete(lf.Workspaces, ws)
		} else {
			lf.Workspaces[ws] = w
		}
	}
	return changed
}
