package hooks

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func resolverReg(t *testing.T, url string) (*Registry, *clock, *logs) {
	t.Helper()
	return reg(t, func(h *Hook) {
		h.Name, h.Point, h.URL = "acme/vault", PointSecretResolver, url
	}, Options{})
}

func TestResolveSecrets_ValueAndPayload(t *testing.T) {
	t.Parallel()
	var got map[string]any
	s, hits := serve(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"version":1,"value":"v4lue"}`))
	})
	r, _, _ := resolverReg(t, s.URL)
	q := SecretQuery{KB: "kb-a", ConceptID: "ops/svc", Principal: map[string]any{"id": "user"}}
	refs := []SecretRef{{Name: "API", Hook: "acme/vault", Key: "prod/api"}}
	v, err := r.ResolveSecrets(context.Background(), q, refs)
	if err != nil || v["API"] != "v4lue" {
		t.Fatalf("%v %v", v, err)
	}
	if got["kb"] != "kb-a" || got["concept_id"] != "ops/svc" || got["name"] != "API" || got["key"] != "prod/api" || got["principal"].(map[string]any)["id"] != "user" {
		t.Fatalf("payload = %v", got)
	}
	// No ttl_seconds: never cached.
	if _, err := r.ResolveSecrets(context.Background(), q, refs); err != nil || hits.Load() != 2 {
		t.Fatalf("hits = %d, %v", hits.Load(), err)
	}
}

func TestResolveSecrets_TTLCache(t *testing.T) {
	t.Parallel()
	s, hits := serve(t, answer(`{"version":1,"value":"v4lue","ttl_seconds":30}`))
	r, ck, _ := resolverReg(t, s.URL)
	q := SecretQuery{KB: "kb-a"}
	refs := []SecretRef{{Name: "API", Hook: "acme/vault", Key: "prod/api"}}
	for i := 0; i < 3; i++ {
		if v, err := r.ResolveSecrets(context.Background(), q, refs); err != nil || v["API"] != "v4lue" {
			t.Fatalf("%v %v", v, err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("hits within the TTL = %d, want 1", hits.Load())
	}
	// Another KB is another entry: a resolver may answer per KB.
	if _, err := r.ResolveSecrets(context.Background(), SecretQuery{KB: "kb-b"}, refs); err != nil || hits.Load() != 2 {
		t.Fatalf("hits for another KB = %d, %v", hits.Load(), err)
	}
	ck.add(31 * time.Second)
	if _, err := r.ResolveSecrets(context.Background(), q, refs); err != nil || hits.Load() != 3 {
		t.Fatalf("hits after expiry = %d, %v", hits.Load(), err)
	}
	// The cache is keyed by hook, KB and key: a value is never part of a key.
	r.secrets.mu.Lock()
	for k := range r.secrets.m {
		if strings.Contains(k.hook+k.kb+k.key, "v4lue") {
			t.Errorf("a value is in the cache key %+v", k)
		}
	}
	r.secrets.mu.Unlock()
}

// The cache_ttl of the hook bounds the TTL the hook answers.
func TestResolveSecrets_CacheTTLBoundsTheAnswer(t *testing.T) {
	t.Parallel()
	s, hits := serve(t, answer(`{"version":1,"value":"v","ttl_seconds":300}`))
	r, ck, _ := reg(t, func(h *Hook) {
		h.Name, h.Point, h.URL, h.CacheTTL = "acme/vault", PointSecretResolver, s.URL, 10*time.Second
	}, Options{})
	refs := []SecretRef{{Name: "API", Hook: "acme/vault", Key: "k"}}
	_, _ = r.ResolveSecrets(context.Background(), SecretQuery{}, refs)
	ck.add(11 * time.Second)
	_, _ = r.ResolveSecrets(context.Background(), SecretQuery{}, refs)
	if hits.Load() != 2 {
		t.Fatalf("hits = %d, want 2 (cache_ttl 10s)", hits.Load())
	}
}

func TestResolveSecrets_Failures(t *testing.T) {
	t.Parallel()
	refs := []SecretRef{{Name: "API", Hook: "acme/vault", Key: "prod/api"}}
	for _, c := range []struct {
		name, body, want string
	}{
		{"no value", `{"version":1}`, "secret_refs API: the resolver answered no value"},
		{"deny", `{"version":1,"decision":"deny","reason":"not for you"}`, "secret_refs API: refused by the resolver: not for you"},
		{"wrong version", `{"version":2,"value":"v"}`, "secret_refs API: resolver unavailable"},
	} {
		s, _ := serve(t, answer(c.body))
		r, _, lg := resolverReg(t, s.URL)
		_, err := r.ResolveSecrets(context.Background(), SecretQuery{}, refs)
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
		if strings.Contains(strings.Join(lg.lines, "\n"), "prod/api") {
			t.Errorf("%s: the key reached the log", c.name)
		}
	}
	r, _, _ := resolverReg(t, "http://127.0.0.1:1/x")
	if _, err := r.ResolveSecrets(context.Background(), SecretQuery{}, refs); err == nil || err.Error() != "secret_refs API: resolver unavailable" {
		t.Errorf("down: %v", err)
	}
	if _, err := r.ResolveSecrets(context.Background(), SecretQuery{}, []SecretRef{{Name: "X", Hook: "acme/other", Key: "k"}}); err == nil {
		t.Error("an unconfigured resolver resolved")
	}
	var nilReg *Registry
	if nilReg.IsResolver("acme/vault") {
		t.Error("a nil registry has a resolver")
	}
}

// A resolver is fail_closed, and dry_run is refused: it does not apply to it.
func TestResolverConfig(t *testing.T) {
	t.Parallel()
	h := Hook{Name: "acme/vault", Point: PointSecretResolver, Plugin: "acme", URL: "https://example.com/v", Credential: "c", FailurePolicy: FailOpen}
	if err := h.Normalize(); err == nil || !strings.Contains(err.Error(), "must be fail_closed") {
		t.Fatalf("fail_open resolver: %v", err)
	}
	h.FailurePolicy, h.Mode = FailClosed, ModeDryRun
	if err := h.Normalize(); err == nil || !strings.Contains(err.Error(), "dry_run does not apply") {
		t.Fatalf("dry_run resolver: %v", err)
	}
	h.Mode = ""
	if err := h.Normalize(); err != nil || h.Mode != ModeEnforce {
		t.Fatalf("resolver: %v, mode %q", err, h.Mode)
	}
}

func TestSecretCacheIsBounded(t *testing.T) {
	t.Parallel()
	var c secretCache
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < maxSecretCache+10; i++ {
		c.put(secretKey{key: string(rune('a'+i%26)) + time.Duration(i).String()}, "v", now, now.Add(time.Duration(i+1)*time.Second))
	}
	if len(c.m) != maxSecretCache {
		t.Fatalf("len = %d, want %d", len(c.m), maxSecretCache)
	}
}
