package mcpserver

import (
	"context"
	"sync"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// KBPush is the per-KB push state /health carries for humans' surfaces
// (`cartographer status`, `doctor`, the tray): present only while the push has
// failed or commits stay unpushed past kb.PushLagThreshold. Remedy is one of
// the fixed sentences of kb.Remedy, never git's own output.
type KBPush struct {
	State           string `json:"push_state"`
	UnpushedCommits int    `json:"unpushed_commits"`
	Remedy          string `json:"remedy,omitempty"`
}

// pushHealthTTL is how long a KB's push state is reused: /health is polled by
// the tray and status, and each reading runs git subprocesses.
const pushHealthTTL = 15 * time.Second

type pushHealthEntry struct {
	at  time.Time
	val *KBPush
}

// pushHealthCache memoises pushHealthFor per KB name. The value does not
// depend on the caller, so the scope check stays outside it.
type pushHealthCache struct {
	mu      sync.Mutex
	ttl     time.Duration // 0 means pushHealthTTL
	entries map[string]pushHealthEntry
	reads   int // readings that reached git, for the cache test
}

func (c *pushHealthCache) get(name string, k *kb.KB, now time.Time) *KBPush {
	ttl := c.ttl
	if ttl == 0 {
		ttl = pushHealthTTL
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[name]; ok && now.Sub(e.at) < ttl && !now.Before(e.at) {
		return e.val
	}
	c.reads++
	val := pushHealthFor(k, now)
	if c.entries == nil {
		c.entries = map[string]pushHealthEntry{}
	}
	c.entries[name] = pushHealthEntry{at: now, val: val}
	return val
}

// pushHealthVisible is the scope rule: sync_status, which reports the same
// data, needs the whole KB (resourceWhole, AllowsWholeKB), so a token narrowed
// to a selector of concepts reads the KB on /health and learns nothing of its
// replication.
func pushHealthVisible(ctx context.Context, name string) bool {
	p := auth.PrincipalFromContext(ctx)
	return p.Policy.Admin || p.Policy.AllowsWholeKB(name, false)
}

// pushHealthFor reports k's push state for /health, nil when nothing is
// worth showing.
func pushHealthFor(k *kb.KB, now time.Time) *KBPush {
	if k == nil {
		return nil
	}
	lag, ok := k.PushLag(now)
	if !ok {
		return nil
	}
	return &KBPush{State: lag.State, UnpushedCommits: lag.Unpushed, Remedy: kb.Remedy(lag.Error)}
}
