package mcpserver

import (
	"sync"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// capabilitiesTTL bounds how often a poller makes the server read a KB's
// secrets/ files and key files to answer /health and the Atlas list. A key
// added to credentials_dir (`kb secrets init`) shows within it, with no
// restart (#2150); kb_status is not cached.
const capabilitiesTTL = 15 * time.Second

type capabilitiesEntry struct {
	at  time.Time
	val map[string]KBCapability
}

// capabilitiesCache memoises kbCapabilities per KB name. The value does not
// depend on the caller, so the visibility checks stay outside it. Callers
// must not modify the returned map.
type capabilitiesCache struct {
	mu      sync.Mutex
	ttl     time.Duration // 0 means capabilitiesTTL
	entries map[string]capabilitiesEntry
	reads   int // derivations that reached the KB, for the tests
}

func (c *capabilitiesCache) get(name string, k *kb.KB, now time.Time) map[string]KBCapability {
	if k == nil {
		return nil
	}
	ttl := c.ttl
	if ttl == 0 {
		ttl = capabilitiesTTL
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[name]; ok && now.Sub(e.at) < ttl && !now.Before(e.at) {
		return e.val
	}
	c.reads++
	val := kbCapabilities(k)
	if c.entries == nil {
		c.entries = map[string]capabilitiesEntry{}
	}
	c.entries[name] = capabilitiesEntry{at: now, val: val}
	return val
}
