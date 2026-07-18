package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode"
)

// contextKey is a private type for context keys to avoid collisions.
type contextKey int

const scopesKey contextKey = 0

// TokenStore manages valid bearer tokens stored as SHA-256 hashes, each mapped
// to its per-KB scopes. A nil or empty scope list means full access (admin):
// the token is not restricted to any KB.
type TokenStore struct {
	hashes map[string][]KBScope // sha256 hex of each valid token -> scopes (nil/empty = full access)
}

// ScopedToken pairs a plaintext bearer token with the KB scopes it grants.
type ScopedToken struct {
	Token  string
	Scopes []KBScope
}

// NewTokenStore creates a token store from plain token strings, each granted full
// access (nil scopes). If tokens is nil or empty, auth is disabled.
func NewTokenStore(tokens []string) *TokenStore {
	scoped := make([]ScopedToken, 0, len(tokens))
	for _, t := range tokens {
		scoped = append(scoped, ScopedToken{Token: t})
	}
	return NewScopedTokenStore(scoped)
}

// NewScopedTokenStore creates a token store where each token carries its own
// per-KB scopes. A token with nil/empty Scopes has full access. If tokens is
// nil or empty, auth is disabled.
func NewScopedTokenStore(tokens []ScopedToken) *TokenStore {
	ts := &TokenStore{hashes: make(map[string][]KBScope)}
	for _, st := range tokens {
		if st.Token != "" {
			ts.hashes[hashToken(st.Token)] = st.Scopes
		}
	}
	return ts
}

// IsEnabled returns true if authentication is required.
func (ts *TokenStore) IsEnabled() bool {
	return len(ts.hashes) > 0
}

// Validate checks if the given token is valid.
// Returns agentID (first 8 chars of token) and whether the token is valid.
func (ts *TokenStore) Validate(token string) (agentID string, ok bool) {
	h := hashToken(token)
	_, ok = ts.hashes[h]
	if ok {
		agentID = truncate(token, 8)
	}
	return agentID, ok
}

// ScopesOf returns the KB scopes granted to the given token, and whether the
// token is valid. A valid token with nil/empty scopes has full access.
func (ts *TokenStore) ScopesOf(token string) ([]KBScope, bool) {
	scopes, ok := ts.hashes[hashToken(token)]
	return scopes, ok
}

// Middleware returns an HTTP middleware that validates the Authorization: Bearer header.
// If auth is disabled, passes through. On failure, returns 401 with WWW-Authenticate header.
// On success, injects the token's scopes into the request context.
func (ts *TokenStore) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ts.IsEnabled() || isPublicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		token, ok := extractBearer(r)
		if !ok {
			unauthorized(w)
			return
		}
		if _, valid := ts.Validate(token); !valid {
			unauthorized(w)
			return
		}
		scopes, _ := ts.ScopesOf(token)
		ctx := ContextWithScopes(r.Context(), scopes)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// isPublicPath reports whether a request path must be reachable without auth:
// the health endpoint (used by k8s liveness/readiness probes) and the OAuth
// protected-resource metadata (RFC 9728, which by definition must be public).
func isPublicPath(path string) bool {
	return path == "/health" ||
		path == "/.well-known/oauth-protected-resource"
}

// ScopesFromToken extracts KB scopes from a bearer token.
// For static tokens (no JWT payload) it returns empty scopes — the seam for future OAuth JWT support.
// Deprecated as an enforcement path: Middleware now uses TokenStore.ScopesOf, which returns the
// scopes configured for the token at startup. This seam remains for a future OAuth JWT integration
// where scopes are extracted from the token itself rather than looked up in the store.
func ScopesFromToken(_ string) []KBScope {
	return nil
}

// ContextWithScopes stores scopes in the request context.
func ContextWithScopes(ctx context.Context, scopes []KBScope) context.Context {
	return context.WithValue(ctx, scopesKey, scopes)
}

// ScopesFromContext retrieves scopes previously stored by ContextWithScopes.
// Returns nil if no scopes were set.
func ScopesFromContext(ctx context.Context) []KBScope {
	v, _ := ctx.Value(scopesKey).([]KBScope)
	return v
}

// Forbidden writes a 403 response in the standard format used across the HTTP
// transport for access-denied cases (auth scope checks, per-tool RBAC).
func Forbidden(w http.ResponseWriter) {
	http.Error(w, "forbidden", http.StatusForbidden)
}

// GenerateToken creates a cryptographically random bearer token (32 bytes, hex encoded).
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: generate token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// ProtectedResourceMetadata returns JSON for /.well-known/oauth-protected-resource (RFC 9728).
func ProtectedResourceMetadata(issuer, resourceID string) []byte {
	m := map[string]any{
		"resource":                 resourceID,
		"bearer_methods_supported": []string{"header"},
		"authorization_servers":    []string{issuer},
	}
	b, _ := json.Marshal(m)
	return b
}

// KBScope represents per-KB access rights extracted from a token.
type KBScope struct {
	KB    string // KB name
	Write bool   // true = rw, false = r
}

// ParseScopes parses a scope string like "kb:docs:rw kb:notes:r" (or
// ";"-separated, e.g. "kb:docs:rw;kb:notes:r") into KBScope entries.
func ParseScopes(scope string) []KBScope {
	var out []KBScope
	for _, part := range strings.FieldsFunc(scope, func(r rune) bool {
		return unicode.IsSpace(r) || r == ';'
	}) {
		// expected format: kb:<name>:<r|rw>
		segs := strings.SplitN(part, ":", 3)
		if len(segs) != 3 || segs[0] != "kb" || segs[1] == "" {
			continue
		}
		ks := KBScope{KB: segs[1]}
		if segs[2] == "rw" {
			ks.Write = true
		}
		out = append(out, ks)
	}
	return out
}

// HasAccess checks if the scopes include at least read (or write) access to the given KB.
func HasAccess(scopes []KBScope, kbName string, write bool) bool {
	for _, s := range scopes {
		if s.KB == kbName {
			if !write || s.Write {
				return true
			}
		}
	}
	return false
}

// --- internal helpers ---

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func extractBearer(r *http.Request) (string, bool) {
	v := r.Header.Get("Authorization")
	if !strings.HasPrefix(v, "Bearer ") {
		return "", false
	}
	token := strings.TrimPrefix(v, "Bearer ")
	if token == "" {
		return "", false
	}
	return token, true
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="cartographer"`)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}
