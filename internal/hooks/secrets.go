package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// SecretRefPrefix starts the target of a secret reference a resolver hook
// serves: NAME=hook:<plugin>/<name>#<opaque key>. The sops form
// (NAME=secrets/file.sops.yaml#/pointer) is untouched.
const SecretRefPrefix = "hook:"

const (
	// MaxSecretTTL bounds the ttl_seconds a resolver may answer.
	MaxSecretTTL = 300 * time.Second
	// maxSecretCache bounds the resolved values kept in memory.
	maxSecretCache = 256
)

// SecretRef is one reference a resolver hook serves.
type SecretRef struct {
	Name string // the NAME the concept declares
	Hook string // <plugin>/<name> of a secret_resolver hook
	Key  string // opaque to the core: the vault's own reference
}

// SecretQuery is what the resolver is told about the request, besides the
// reference itself: the KB, the concept that declares the reference and the
// caller. Never the age key, never another reference's value.
type SecretQuery struct {
	KB        string
	ConceptID string
	Principal map[string]any
}

// IsResolver says whether name is a configured secret_resolver hook.
func (r *Registry) IsResolver(name string) bool {
	if r == nil {
		return false
	}
	e, ok := r.byName[name]
	return ok && e.Point == PointSecretResolver
}

// ResolveSecrets asks each reference's resolver for its value, one call per
// reference not in the cache. Every failure is an error naming the reference
// and never a URL, a key or a value: a resolver is fail_closed by
// construction, so no value means no resolution. The caller must not hold the
// KB lock. A value is cached only when the hook answers ttl_seconds, for at
// most that, MaxSecretTTL and the hook's cache_ttl; the cache is keyed by hook,
// KB and key, never by a value, and is never persisted nor logged.
func (r *Registry) ResolveSecrets(ctx context.Context, q SecretQuery, refs []SecretRef) (map[string]string, error) {
	out := make(map[string]string, len(refs))
	for _, ref := range refs {
		if !r.IsResolver(ref.Hook) {
			return nil, fmt.Errorf("secret_refs %s: resolver unavailable", ref.Name)
		}
		ck := secretKey{hook: ref.Hook, kb: q.KB, key: ref.Key}
		if v, ok := r.secrets.get(ck, r.now()); ok {
			out[ref.Name] = v
			continue
		}
		res := r.Decide(ctx, ref.Hook, Request{KB: q.KB, Fields: map[string]any{
			"concept_id": q.ConceptID, "name": ref.Name, "key": ref.Key, "principal": q.Principal,
		}})
		switch {
		case res.Err != nil || res.Unavailable || res.DryRun:
			return nil, fmt.Errorf("secret_refs %s: resolver unavailable", ref.Name)
		case res.Decision == Deny:
			if res.Reason != "" {
				return nil, fmt.Errorf("secret_refs %s: refused by the resolver: %s", ref.Name, res.Reason)
			}
			return nil, fmt.Errorf("secret_refs %s: refused by the resolver", ref.Name)
		}
		var v string
		if raw, ok := res.Answer.Raw["value"]; !ok || json.Unmarshal(raw, &v) != nil {
			return nil, fmt.Errorf("secret_refs %s: the resolver answered no value", ref.Name)
		}
		out[ref.Name] = v
		var ttl int64
		if raw, ok := res.Answer.Raw["ttl_seconds"]; ok && json.Unmarshal(raw, &ttl) == nil && ttl > 0 {
			h := r.byName[ref.Hook]
			d := time.Duration(ttl) * time.Second
			if d > MaxSecretTTL {
				d = MaxSecretTTL
			}
			if h.CacheTTL > 0 && d > h.CacheTTL {
				d = h.CacheTTL
			}
			now := r.now()
			r.secrets.put(ck, v, now, now.Add(d))
		}
	}
	return out, nil
}

type secretKey struct{ hook, kb, key string }

type secretEntry struct {
	value   string
	expires time.Time
}

// secretCache holds resolved values in memory, bounded to maxSecretCache.
type secretCache struct {
	mu sync.Mutex
	m  map[secretKey]secretEntry
}

func (c *secretCache) get(k secretKey, now time.Time) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	if !ok {
		return "", false
	}
	if !now.Before(e.expires) {
		delete(c.m, k)
		return "", false
	}
	return e.value, true
}

// put stores a value; a full cache first drops the expired entries, then the
// one that expires soonest.
func (c *secretCache) put(k secretKey, v string, now, expires time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[secretKey]secretEntry{}
	}
	if _, ok := c.m[k]; !ok && len(c.m) >= maxSecretCache {
		var soonest secretKey
		var at time.Time
		for key, e := range c.m {
			if !now.Before(e.expires) {
				delete(c.m, key)
				continue
			}
			if at.IsZero() || e.expires.Before(at) {
				soonest, at = key, e.expires
			}
		}
		if len(c.m) >= maxSecretCache {
			delete(c.m, soonest)
		}
	}
	c.m[k] = secretEntry{value: v, expires: expires}
}
