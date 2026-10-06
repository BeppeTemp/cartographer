package main

import (
	"log/slog"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// reportUsage is the post-apply step of `sync` that tells each server which
// skills and agents this machine's clients actually loaded (D326). It reads
// the local session transcripts (provisioning.ScanUsage, read-only) and posts
// only the per-artifact aggregate — name, kind, provider, last use, count — to
// the KB the artifact came from. No prompt, no message, no session id leaves
// the machine.
//
// It is best-effort and never blocks or fails a sync: a server too old to have
// the route, an unreachable one, a refused token all end in a debug line. It
// returns the number of entries the server accepted, for tests.
func reportUsage(cfg *clientconfig.Config, dir string) int {
	if !cfg.UsageScan {
		return 0
	}
	lf, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		slog.Debug("usage report skipped: lockfile unreadable", "err", err)
		return 0
	}
	usage, err := provisioning.ScanUsage(lf, dir, provisioning.DefaultUsageWindow, true)
	if err != nil || len(usage) == 0 {
		return 0
	}
	// Per source KB; a bundled artifact is the binary's, not any KB's.
	byKB := map[string][]provisioning.ArtifactUsage{}
	for _, u := range usage {
		if name, ok := strings.CutPrefix(u.Source, "kb:"); ok && name != "" {
			byKB[name] = append(byKB[name], u)
		}
	}
	kbNames := make([]string, 0, len(byKB))
	for name := range byKB {
		kbNames = append(kbNames, name)
	}
	sort.Strings(kbNames)
	c := client.New(cfg.ServerURL, resolveToken(cfg)).WithTokenEnv(tokenEnvName(cfg))
	sent := 0
	for _, name := range kbNames {
		if err := c.ReportUsage(name, byKB[name], probeTimeout); err != nil {
			slog.Debug("usage report not delivered", "kb", name, "err", err)
			continue
		}
		sent += len(byKB[name])
	}
	return sent
}
