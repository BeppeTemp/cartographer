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

const principalKey contextKey = 0

// Permission is one immutable allow rule. Empty Maps, Journals and Types are
// wildcards; non-empty selectors are intersected. There are intentionally no
// deny rules: permissions are unioned, which keeps policy evaluation
// deterministic and order-independent.
type Permission struct {
	KB       string
	Write    bool
	Maps     []string
	Journals []string
	Types    []string
}

// Policy is the complete, immutable policy assigned to a principal.
type Policy struct {
	Admin       bool
	Permissions []Permission
}

// Principal identifies the caller without retaining its bearer secret.
type Principal struct {
	ID     string
	Policy Policy
}

// LocalAdminPrincipal is used for auth-disabled HTTP and trusted stdio. It is
// explicit so handlers never infer authority from a missing context value.
func LocalAdminPrincipal() Principal {
	return Principal{ID: "local-admin", Policy: Policy{Admin: true}}
}

// Allows reports whether a policy allows an operation on a resource. mapName
// and journalName are mutually exclusive resource descriptors; typeName may
// be empty for non-concept resources. rw implies r.
func (p Policy) Allows(kbName, mapName, journalName, typeName string, write bool) bool {
	if p.Admin {
		return true
	}
	for _, rule := range p.Permissions {
		if rule.KB != kbName || (write && !rule.Write) {
			continue
		}
		if !matchesSelector(rule.Maps, mapName) || !matchesSelector(rule.Journals, journalName) || !matchesSelector(rule.Types, typeName) {
			continue
		}
		return true
	}
	return false
}

// AllowsWholeKB requires an unselected permission. It is used for operations
// which cannot safely be scoped to a partial collection.
func (p Policy) AllowsWholeKB(kbName string, write bool) bool {
	if p.Admin {
		return true
	}
	for _, rule := range p.Permissions {
		if rule.KB == kbName && (!write || rule.Write) && len(rule.Maps) == 0 && len(rule.Journals) == 0 && len(rule.Types) == 0 {
			return true
		}
	}
	return false
}

// HasKBAccess reports whether any rule grants the requested mode on a KB,
// regardless of resource selectors. It gates protocol metadata only.
func (p Policy) HasKBAccess(kbName string, write bool) bool {
	if p.Admin {
		return true
	}
	for _, rule := range p.Permissions {
		if rule.KB == kbName && (!write || rule.Write) {
			return true
		}
	}
	return false
}

func matchesSelector(values []string, value string) bool {
	if len(values) == 0 {
		return true
	}
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

// TokenStore manages valid bearer tokens stored as SHA-256 hashes, each mapped
// to its per-KB scopes. A nil or empty scope list means full access (admin):
// the token is not restricted to any KB.
type TokenStore struct{ hashes map[string]Principal }

// ScopedToken pairs a plaintext bearer token with the KB scopes it grants.
type ScopedToken struct {
	Token     string
	Scopes    []KBScope
	Principal string
	Policy    Policy
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
	ts := &TokenStore{hashes: make(map[string]Principal)}
	for _, st := range tokens {
		if st.Token != "" {
			h := hashToken(st.Token)
			policy := clonePolicy(st.Policy)
			if !policy.Admin {
				// Legacy scopes are unioned with any role-derived permissions
				// rather than replaced by them, so a deployment can migrate one
				// token at a time without silently dropping either half.
				for _, scope := range st.Scopes {
					policy.Permissions = append(policy.Permissions, Permission{KB: scope.KB, Write: scope.Write})
				}
				// A token that carries neither scopes nor permissions is the
				// pre-scopes admin token: preserve its historical full access.
				if len(policy.Permissions) == 0 {
					policy.Admin = true
				}
			}
			id := st.Principal
			if id == "" {
				id = "token-" + h[:16]
			}
			ts.hashes[h] = Principal{ID: id, Policy: clonePolicy(policy)}
		}
	}
	return ts
}

// IsEnabled returns true if authentication is required.
func (ts *TokenStore) IsEnabled() bool {
	return len(ts.hashes) > 0
}

// Validate checks if the given token is valid. The returned ID is never
// derived from the plaintext token.
func (ts *TokenStore) Validate(token string) (agentID string, ok bool) {
	h := hashToken(token)
	p, ok := ts.hashes[h]
	if ok {
		agentID = p.ID
	}
	return agentID, ok
}

// ScopesOf returns the KB scopes granted to the given token, and whether the
// token is valid. A valid token with nil/empty scopes has full access.
func (ts *TokenStore) ScopesOf(token string) ([]KBScope, bool) {
	p, ok := ts.hashes[hashToken(token)]
	if !ok || p.Policy.Admin {
		return nil, ok
	}
	var scopes []KBScope
	for _, rule := range p.Policy.Permissions {
		if len(rule.Maps) == 0 && len(rule.Journals) == 0 && len(rule.Types) == 0 {
			scopes = append(scopes, KBScope{KB: rule.KB, Write: rule.Write})
		}
	}
	return scopes, ok
}

// PrincipalOf returns the authenticated immutable principal.
func (ts *TokenStore) PrincipalOf(token string) (Principal, bool) {
	p, ok := ts.hashes[hashToken(token)]
	return clonePrincipal(p), ok
}

// Middleware returns an HTTP middleware that validates the Authorization: Bearer header.
// If auth is disabled, passes through. On failure, returns 401 with WWW-Authenticate header.
// On success, injects the token's scopes into the request context.
func (ts *TokenStore) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ts.IsEnabled() || isPublicPath(r.URL.Path) {
			next.ServeHTTP(w, r.WithContext(ContextWithPrincipal(r.Context(), LocalAdminPrincipal())))
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
		principal, _ := ts.PrincipalOf(token)
		ctx := ContextWithPrincipal(r.Context(), principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// WellKnownProtectedResourcePath is the RFC 9728 discovery path for OAuth
// Protected Resource Metadata. It is exempted from authentication here (by
// definition it must be public) and served by mcpserver.MultiKBServer.Handler
// using ProtectedResourceMetadata.
const WellKnownProtectedResourcePath = "/.well-known/oauth-protected-resource"

// isPublicPath reports whether a request path must be reachable without auth:
// the health endpoint (used by k8s liveness/readiness probes) and the OAuth
// protected-resource metadata (RFC 9728, which by definition must be public).
func isPublicPath(path string) bool {
	return path == "/health" ||
		path == WellKnownProtectedResourcePath
}

// ScopesFromContext reports the plain per-KB scopes of the principal stored
// by ContextWithPrincipal. Returns nil for an admin principal (unrestricted,
// so no scope list describes it) and for rules narrowed to specific
// maps/journals/types, which a flat KBScope cannot express.
func ScopesFromContext(ctx context.Context) []KBScope {
	p := PrincipalFromContext(ctx)
	if p.Policy.Admin {
		return nil
	}
	var out []KBScope
	for _, rule := range p.Policy.Permissions {
		if len(rule.Maps) == 0 && len(rule.Journals) == 0 && len(rule.Types) == 0 {
			out = append(out, KBScope{KB: rule.KB, Write: rule.Write})
		}
	}
	return out
}

func ContextWithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalKey, clonePrincipal(principal))
}
func PrincipalFromContext(ctx context.Context) Principal {
	p, ok := ctx.Value(principalKey).(Principal)
	if !ok {
		return Principal{}
	}
	return clonePrincipal(p)
}

func clonePrincipal(p Principal) Principal {
	p.Policy = clonePolicy(p.Policy)
	return p
}

func clonePolicy(p Policy) Policy {
	source := p.Permissions
	p.Permissions = make([]Permission, len(p.Permissions))
	for i, rule := range source {
		rule.Maps = append([]string(nil), rule.Maps...)
		rule.Journals = append([]string(nil), rule.Journals...)
		rule.Types = append([]string(nil), rule.Types...)
		p.Permissions[i] = rule
	}
	return p
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
