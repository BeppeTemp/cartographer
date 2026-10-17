package kb

import "time"

// The two timings of the git sync are constants, not settings (D903): nobody
// needed a different value for a reason of their own, and a wrong one only
// slowed a server down or stranded a push.
const (
	// DefaultSyncInWindow is the freshness window of the pull before a read or
	// a write (D76/WP3): within it of the last successful pull, the pull is
	// skipped.
	DefaultSyncInWindow = 30 * time.Second
	// DefaultSyncOutDebounce is the debounce of the background push (D76/WP4):
	// a burst of writes coalesces into one push this long after the last one.
	DefaultSyncOutDebounce = 3 * time.Second
)

// SyncOutDebounceFor is the push debounce on the platform described by getenv
// (os.Getenv in the server). It is DefaultSyncOutDebounce, except on Cloud Run,
// which freezes an instance's CPU once the response is sent: a push left to a
// background worker would be stranded until the next request. There every push
// is made before the write returns (a debounce of zero), which costs one push
// of latency per write and loses nothing (D903). Cloud Run sets K_SERVICE on a
// service's every revision (Knative serving sets it too, where a synchronous
// push is merely harmless); no key is needed.
func SyncOutDebounceFor(getenv func(string) string) time.Duration {
	if getenv("K_SERVICE") != "" {
		return 0
	}
	return DefaultSyncOutDebounce
}
