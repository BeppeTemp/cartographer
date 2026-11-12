package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/hooks"
)

// maxIdentityCache bounds the answers kept; past it new ones are simply not
// cached (the breaker still protects the hook).
const maxIdentityCache = 4096

// identityHookFrom returns the identity hook of the registry as an
// auth.IdentityHook, nil when none is configured. The first identity hook
// serves (buildServerHooks refuses a second one). It is on the path of every
// request of a proxy token, so it answers from a cache keyed by everything the
// hook is told about the request, and the registry's breaker bounds a hook
// that is down. It runs in the auth middleware, never under a KB lock.
func identityHookFrom(reg *hooks.Registry) auth.IdentityHook {
	list := reg.ForPoint(hooks.PointIdentity)
	if len(list) == 0 {
		return nil
	}
	c := &identityCache{reg: reg, hook: list[0], now: time.Now, m: map[[sha256.Size]byte]identityEntry{}}
	return c.resolve
}

type identityEntry struct {
	id       auth.Identity
	supplied bool
	exp      time.Time
}

type identityCache struct {
	reg  *hooks.Registry
	hook hooks.Hook
	now  func() time.Time
	mu   sync.Mutex
	m    map[[sha256.Size]byte]identityEntry
}

func (c *identityCache) resolve(r *http.Request, via auth.Principal, header auth.Identity) (auth.Identity, bool, error) {
	headers := map[string][]string{}
	for _, n := range c.hook.Headers {
		if v := r.Header.Values(n); len(v) > 0 {
			headers[http.CanonicalHeaderKey(n)] = v
		}
	}
	// The key covers every input the hook sees except the peer, so two
	// requests that would get different answers never share one.
	key := sha256.Sum256([]byte(fmt.Sprintf("%q\x00%q\x00%q\x00%q", via.ID, header.User, header.Groups, headers)))
	now := c.now()
	c.mu.Lock()
	e, ok := c.m[key]
	c.mu.Unlock()
	if ok && now.Before(e.exp) {
		return e.id, e.supplied, nil
	}
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	out := c.reg.Decide(r.Context(), c.hook.Name, hooks.Request{Fields: map[string]any{
		"peer":           peer,
		"token_id":       via.ID,
		"headers":        headers,
		"proxy_identity": map[string]any{"user": header.User, "groups": header.Groups},
	}})
	if out.Decision == hooks.Deny {
		if out.Unavailable {
			return auth.Identity{}, false, auth.ErrIdentityUnavailable
		}
		return auth.Identity{}, false, auth.ErrIdentityDenied
	}
	if out.Err != nil || out.DryRun {
		return auth.Identity{}, false, nil // fail_open (or observing): the header identity stays
	}
	id, supplied, err := parseIdentityAnswer(out.Answer)
	if err != nil {
		log.Printf("hook %s: malformed identity answer: %v", c.hook.Name, err)
		if c.hook.FailurePolicy == hooks.FailClosed {
			return auth.Identity{}, false, auth.ErrIdentityUnavailable
		}
		return auth.Identity{}, false, nil
	}
	c.store(key, identityEntry{id: id, supplied: supplied, exp: now.Add(c.hook.CacheTTL)}, now)
	return id, supplied, nil
}

func (c *identityCache) store(key [sha256.Size]byte, e identityEntry, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= maxIdentityCache {
		for k, old := range c.m {
			if !now.Before(old.exp) {
				delete(c.m, k)
			}
		}
		if len(c.m) >= maxIdentityCache {
			return
		}
	}
	c.m[key] = e
}

// parseIdentityAnswer reads {user, groups}; no user is no opinion. The limits
// on both are the auth package's (checkIdentity), applied after this.
func parseIdentityAnswer(a hooks.Answer) (auth.Identity, bool, error) {
	raw, ok := a.Raw["user"]
	if !ok {
		return auth.Identity{}, false, nil
	}
	var id auth.Identity
	if err := json.Unmarshal(raw, &id.User); err != nil {
		return auth.Identity{}, false, fmt.Errorf("user: %v", err)
	}
	if id.User == "" {
		return auth.Identity{}, false, nil
	}
	if g, ok := a.Raw["groups"]; ok {
		if err := json.Unmarshal(g, &id.Groups); err != nil {
			return auth.Identity{}, false, fmt.Errorf("groups: %v", err)
		}
	}
	return id, true, nil
}
