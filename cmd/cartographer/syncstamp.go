package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

// syncStampPrefix names the freshness stamps in the client state directory
// (D2342): one per --client selection, whose mtime is the
// end of the last successful sync of it.
const syncStampPrefix = ".cartographer-sync-stamp"

var syncStampKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// syncStampPath is the stamp of a selection (the --client values as given, so
// a provider that is not connected has its own stamp too), or "" when a name is
// not a plain identifier: such a run is never skipped nor stamped.
func syncStampPath(dir string, clients []string) string {
	keys := slices.Clone(clients)
	slices.Sort(keys)
	for _, k := range keys {
		if !syncStampKeyRe.MatchString(k) {
			return ""
		}
	}
	name := syncStampPrefix
	if len(keys) > 0 {
		name += "-" + strings.Join(keys, "+")
	}
	return filepath.Join(dir, name)
}

// syncGate is the freshness gate of one gated run.
type syncGate struct {
	dir, stamp string
	maxAge     time.Duration
}

// syncStampFresh says whether the last successful sync of the selection is
// younger than maxAge. It reads one file's mtime: no lock, no network. A stamp
// a little in the future (clock step, WSL2) counts as fresh, one far ahead of
// the clock does not.
func syncStampFresh(stamp string, maxAge time.Duration, version string) bool {
	if stamp == "" || maxAge <= 0 {
		return false
	}
	info, err := os.Stat(stamp)
	if err != nil {
		return false
	}
	// The content carries the binary version: a stamp of another one is stale,
	// so the repair that follows a brew upgrade is not delayed by the window.
	if data, err := os.ReadFile(stamp); err != nil || !strings.HasSuffix(strings.TrimSpace(string(data)), " "+version) {
		return false
	}
	age := time.Since(info.ModTime())
	return age < maxAge && age >= -usageClockSkew
}

// touchSyncStamp records a successful sync. The mtime is the datum, so the
// content carries the time: identical bytes would not move it.
func touchSyncStamp(dir, stamp, version string) {
	if stamp == "" {
		return
	}
	if err := safewrite.WriteFileAtomicNoFollow(dir, stamp, []byte(time.Now().UTC().Format(time.RFC3339Nano)+" "+version+"\n"), 0o600); err != nil {
		slog.Debug("sync stamp not written", "err", err)
	}
}

// connectedOnly narrows the --client values of a freshness-gated sync to the
// connected providers; when none is, it returns nil, which syncs every one. A
// session-start script bakes in its own client, which may not be connected
// here, and that must not turn into a silent failure behind `|| true`.
func connectedOnly(agents, clients []string) []string {
	var kept []string
	for _, c := range clients {
		if slices.Contains(agents, c) {
			kept = append(kept, c)
		}
	}
	return kept
}
