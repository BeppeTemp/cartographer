// Package auth implements bearer-token authorization for the HTTP transport:
// the token store, the middleware that resolves a request to a principal with
// a Policy, and the per-KB RBAC check the tools are gated on (`kb:<name>:r` /
// `kb:<name>:rw` scopes are parsed into Policy permissions). Disabled by default; stdio is trusted and never passes through it.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
)

// contextKey is a private type for context keys to avoid collisions.
type contextKey int

const principalKey contextKey = 0

// Permission is one immutable allow rule. Empty Maps, Journals and Types are
// wildcards; non-empty selectors are intersected. There are intentionally no
// deny rules: permissions are unioned, which keeps policy evaluation
// deterministic and order-independent.
//
// ReadOnlyArtifacts is therefore a narrower allow, not a deny: a rule that
// carries it grants what it grants today except the artifact write tools
// (skills, agents, hooks every client of the KB syncs and executes). A second
// rule of the same token without it restores them, as the union of any two
// rules does. NoSecrets is the same shape for secrets: the rule grants what it
// grants except authority over the KB's secrets (the secret tools, service
// resolution, and concept fields that point at a secret).
type Permission struct {
	KB                string
	Write             bool
	Maps              []string
	Journals          []string
	Types             []string
	ReadOnlyArtifacts bool
	NoSecrets         bool
}

// Policy is the complete, immutable policy assigned to a principal.
type Policy struct {
	Admin       bool
	Permissions []Permission
}

// Kind says what sort of caller a token stands for. The zero value is read as
// KindHuman, so every token written before kinds existed keeps its meaning.
type Kind string

const (
	KindHuman    Kind = "human"
	KindClient   Kind = "client"
	KindSurveyor Kind = "surveyor"
)

// ParseKind reads a configured kind strictly: an empty string is human, an
// unknown value is an error (a typo must not silently fall back to the widest
// kind).
func ParseKind(s string) (Kind, error) {
	switch Kind(s) {
	case "", KindHuman:
		return KindHuman, nil
	case KindClient:
		return KindClient, nil
	case KindSurveyor:
		return KindSurveyor, nil
	}
	return "", fmt.Errorf("kind %q: want %q, %q or %q", s, KindHuman, KindClient, KindSurveyor)
}

// NonHuman reports whether the kind is one of the machine kinds, whose tokens
// must be KB-scoped and expiring.
func (k Kind) NonHuman() bool { return k == KindClient || k == KindSurveyor }

// Principal identifies the caller without retaining its bearer secret.
type Principal struct {
	ID     string
	Policy Policy
	// Kind is what the token stands for; the zero value is human.
	Kind Kind
	// Via is the id of the token behind an identity that is not the token's
	// own (a person named by a trusted proxy); empty when the ID is the token's.
	Via string
	// Groups are the groups a trusted proxy named for the person.
	Groups []string
	// Local marks the principal the server itself creates for a trusted local
	// caller (stdio, or HTTP with authentication off). A token store never sets
	// it, so no configured token, whatever its name or policy, can claim it.
	Local bool
}

// LocalAdminPrincipal is used for auth-disabled HTTP and trusted stdio. It is
// explicit so handlers never infer authority from a missing context value.
func LocalAdminPrincipal() Principal {
	return Principal{ID: "local-admin", Policy: Policy{Admin: true}, Kind: KindHuman, Local: true}
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

// AllowsCollection reports whether a policy reaches anything inside a map or
// journal. The type selector is not consulted: a collection has no type, and
// passing an empty one to Allows matched no rule that names types — so a role
// narrowed to a map *and* a type could see no collection at all, and the UI
// told it no KB was visible (D228). Concept-level checks still apply the type.
func (p Policy) AllowsCollection(kbName, mapName, journalName string, write bool) bool {
	if p.Admin {
		return true
	}
	for _, rule := range p.Permissions {
		if rule.KB != kbName || (write && !rule.Write) {
			continue
		}
		if matchesSelector(rule.Maps, mapName) && matchesSelector(rule.Journals, journalName) {
			return true
		}
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

// AllowsArtifactWrite reports whether a policy may change a KB's artifacts
// (artifact_write, artifact_delete, artifact_move, skill_install): an
// unselected write rule that does not carry ReadOnlyArtifacts.
func (p Policy) AllowsArtifactWrite(kbName string) bool {
	if p.Admin {
		return true
	}
	for _, rule := range p.Permissions {
		if rule.KB == kbName && rule.Write && !rule.ReadOnlyArtifacts && len(rule.Maps) == 0 && len(rule.Journals) == 0 && len(rule.Types) == 0 {
			return true
		}
	}
	return false
}

// AllowsSecrets reports whether a policy has authority over a KB's secrets
// (secret_resolve, secret_set, service_get with resolve_secrets): an
// unselected write rule that does not carry NoSecrets.
func (p Policy) AllowsSecrets(kbName string) bool {
	if p.Admin {
		return true
	}
	for _, rule := range p.Permissions {
		if rule.KB == kbName && rule.Write && !rule.NoSecrets && len(rule.Maps) == 0 && len(rule.Journals) == 0 && len(rule.Types) == 0 {
			return true
		}
	}
	return false
}

// WithholdsSecrets reports whether a policy is restricted from the concept
// fields that point at a secret on a KB: it writes the KB through at least one
// rule that carries NoSecrets and has no unselected write rule without it
// (AllowsSecrets false). Fail-closed on mixed rules: a whole-KB NoSecrets rule
// is not undone by a scoped rule without the key, which restores the authority
// only inside its own selection. A token with no NoSecrets write rule is not
// restricted, as before the key existed.
func (p Policy) WithholdsSecrets(kbName string) bool {
	if p.Admin {
		return false
	}
	for _, rule := range p.Permissions {
		if rule.KB == kbName && rule.Write && rule.NoSecrets {
			return !p.AllowsSecrets(kbName)
		}
	}
	return false
}

// SecretsVisible reports whether a policy may see a KB's secret files in the
// Atlas (names and pointers, no value): an unselected rule of any mode that
// does not carry NoSecrets.
func (p Policy) SecretsVisible(kbName string) bool {
	if p.Admin {
		return true
	}
	for _, rule := range p.Permissions {
		if rule.KB == kbName && !rule.NoSecrets && len(rule.Maps) == 0 && len(rule.Journals) == 0 && len(rule.Types) == 0 {
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
// to the immutable policy it grants. Unrestricted access is declared by the
// caller (Policy.Admin), never inferred from an empty policy (D179).
type TokenStore struct {
	// snap is the immutable set of tokens in force. Replace swaps it whole, so
	// a request reads one consistent set and never a half-updated map; a
	// request already past lookup finishes with the principal it resolved.
	snap atomic.Pointer[tokenSnapshot]
	// now is the clock expiry is judged by; tests inject theirs.
	now func() time.Time
	// expiredLogged remembers the ids already reported as expired, so a client
	// retrying with a dead token does not flood the log (once per token).
	expiredLogged sync.Map
	// required is set by RequireAuth for a deployment that configured
	// authentication: such a store enforces even when empty (D179).
	required bool
}

// tokenSnapshot is one immutable generation of the store's tokens, keyed by
// the token hash. It is never modified after it is published.
type tokenSnapshot struct {
	hashes map[string]tokenEntry
	// proxy is the trusted-proxy identity in force; nil: the feature is off.
	proxy *ProxyConfig
}

// tokenEntry is what the store keeps per hash: the principal and when its
// token stops being valid (zero: never).
type tokenEntry struct {
	principal Principal
	expiresAt time.Time
	// proxy: the token may carry a person's identity (ScopedToken.Proxy).
	proxy bool
}

// lookupStatus tells why a lookup failed. It exists for the log only: the
// response to an unknown and to an expired token is the same 401 (no oracle).
type lookupStatus int

const (
	lookupOK lookupStatus = iota
	lookupUnknown
	lookupExpired
)

// ScopedToken pairs a plaintext bearer token with the KB scopes it grants.
type ScopedToken struct {
	Token     string
	Scopes    []KBScope
	Principal string
	Policy    Policy
	// Kind is the token's kind (zero: human).
	Kind Kind
	// Via is carried onto the principal (see Principal.Via).
	Via string
	// ExpiresAt is the instant the token stops being valid; zero never expires.
	ExpiresAt time.Time
	// Proxy marks the bearer of a trusted SSO proxy: only such a token's
	// requests may carry an identity header (ProxyConfig).
	Proxy bool
}

// NewTokenStore creates a token store from plain token strings, each granted
// full access. If tokens is nil or empty, auth is disabled.
//
// Admin is set explicitly rather than inferred from an empty permission set
// (D179): a bare token string IS the declaration of an unrestricted token, and
// stating it here is what lets the store refuse to invent that intent for
// anyone else.
func NewTokenStore(tokens []string) *TokenStore {
	scoped := make([]ScopedToken, 0, len(tokens))
	for _, t := range tokens {
		scoped = append(scoped, ScopedToken{Token: t, Policy: Policy{Admin: true}})
	}
	return NewScopedTokenStore(scoped)
}

// NewScopedTokenStore creates a token store where each token carries its own
// policy: role-derived permissions, legacy per-KB scopes, or an explicit
// Policy.Admin. A token whose policy ends up empty grants NOTHING — see the
// comment inside. Entries with an empty token value are skipped; a caller that
// requires authentication must mark the store with RequireAuth so an empty
// result fails closed rather than disabling enforcement.
func NewScopedTokenStore(tokens []ScopedToken) *TokenStore {
	ts := &TokenStore{now: time.Now}
	ts.snap.Store(buildSnapshot(tokens))
	return ts
}

// buildSnapshot hashes and compiles the tokens into a snapshot; entries with
// an empty token value are skipped.
func buildSnapshot(tokens []ScopedToken) *tokenSnapshot {
	snap := &tokenSnapshot{hashes: make(map[string]tokenEntry)}
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
				// Deliberately NOT promoted to admin when the result is empty
				// (D179). "The operator declared no restriction" and "every
				// restriction the operator declared failed to parse" both
				// arrive here as an empty permission set, and inferring admin
				// from that turned a scope typo into unrestricted access.
				// The legacy pre-scopes admin token is now declared by its
				// caller setting Policy.Admin explicitly.
			}
			id := st.Principal
			if id == "" {
				id = "token-" + h[:16]
			}
			kind := st.Kind
			if kind == "" {
				kind = KindHuman
			}
			if kind == KindSurveyor {
				// A surveyor never changes artifacts, whatever its rules (or
				// legacy scopes, unioned above) say. Forced here, after the
				// union, so no path to a permission escapes it.
				// Nor does it touch secrets (D1894, Secrets).
				for i := range policy.Permissions {
					policy.Permissions[i].ReadOnlyArtifacts = true
					policy.Permissions[i].NoSecrets = true
				}
			}
			snap.hashes[h] = tokenEntry{
				principal: Principal{ID: id, Policy: clonePolicy(policy), Kind: kind, Via: st.Via},
				expiresAt: st.ExpiresAt,
				proxy:     st.Proxy,
			}
		}
	}
	return snap
}

// Replace swaps the tokens in force for a new set, on a running store: a
// reload of the configuration (tokens, roles) needs no restart. The set is
// compiled exactly as at construction. A store that enforces authentication
// (RequireAuth) refuses a set with no usable token and keeps the old one, the
// fail-closed rule of D179 applied to a reload: an emptied file must not turn
// into a locked-out or, worse, an open server. A request in flight finishes
// with the principal it resolved (principals are immutable copies).
func (ts *TokenStore) Replace(tokens []ScopedToken) error {
	return ts.ReplaceAll(tokens, ts.snap.Load().proxy)
}

// ReplaceAll is Replace with the proxy identity section, swapped in the same
// snapshot so a request never sees new tokens with the old mapping.
func (ts *TokenStore) ReplaceAll(tokens []ScopedToken, proxy *ProxyConfig) error {
	snap := buildSnapshot(tokens)
	snap.proxy = proxy
	if ts.required && len(snap.hashes) == 0 {
		return errors.New("the new token set has no usable token and authentication is required")
	}
	ts.snap.Store(snap)
	return nil
}

// SetProxy installs the proxy identity section on a store being built, before
// it serves; a running store changes it through ReplaceAll.
func (ts *TokenStore) SetProxy(proxy *ProxyConfig) *TokenStore {
	snap := *ts.snap.Load()
	snap.proxy = proxy
	ts.snap.Store(&snap)
	return ts
}

// IsEnabled returns true if authentication is enforced for requests.
//
// A store built by RequireAuth enforces even when it holds no token, so that a
// configuration which asked for authentication and produced no usable
// credential fails closed instead of serving every request as a local admin
// (D179).
func (ts *TokenStore) IsEnabled() bool {
	return ts.required || len(ts.snap.Load().hashes) > 0
}

// TokenCount reports how many usable tokens the store holds — the count AFTER
// unusable entries were dropped, which is the number a caller must check
// against its own expectations rather than the number of configured records.
func (ts *TokenStore) TokenCount() int { return len(ts.snap.Load().hashes) }

// RequireAuth marks the store as enforcing regardless of how many tokens it
// holds. serve calls it whenever auth resolved to "on", so the decision to
// enforce is carried explicitly instead of being re-derived from the token
// count — the two disagreed, because the count that gated the decision was of
// configured records while the store had already dropped the unusable ones.
func (ts *TokenStore) RequireAuth() *TokenStore {
	ts.required = true
	return ts
}

// lookup resolves a token to its principal in one step, so a token cannot
// validate and then expire between two calls. An expired token is reported
// (once per token, by id, never by value) and otherwise indistinguishable from
// an unknown one.
func (ts *TokenStore) lookup(token string) (Principal, lookupStatus) {
	e, st := ts.lookupEntry(token)
	return clonePrincipal(e.principal), st
}

func (ts *TokenStore) lookupEntry(token string) (tokenEntry, lookupStatus) {
	e, ok := ts.snap.Load().hashes[hashToken(token)]
	if !ok {
		return tokenEntry{}, lookupUnknown
	}
	if !e.expiresAt.IsZero() && !ts.now().Before(e.expiresAt) {
		if _, seen := ts.expiredLogged.LoadOrStore(e.principal.ID, true); !seen {
			log.Printf("auth: token %q expired at %s, refused", e.principal.ID, e.expiresAt.UTC().Format(time.RFC3339))
		}
		return tokenEntry{}, lookupExpired
	}
	return e, lookupOK
}

// Validate checks if the given token is valid. The returned ID is never
// derived from the plaintext token.
func (ts *TokenStore) Validate(token string) (agentID string, ok bool) {
	p, st := ts.lookup(token)
	return p.ID, st == lookupOK
}

// PrincipalOf returns the authenticated immutable principal.
func (ts *TokenStore) PrincipalOf(token string) (Principal, bool) {
	p, st := ts.lookup(token)
	return p, st == lookupOK
}

// Middleware returns an HTTP middleware that validates the Authorization: Bearer header.
// If auth is disabled, passes through as the local admin. On failure, returns 401 with WWW-Authenticate header.
// On success, injects the token's principal into the request context. A public
// path (isPublicPath) is never refused: it carries the principal of a valid
// bearer when there is one and no principal otherwise.
func (ts *TokenStore) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ts.IsEnabled() {
			next.ServeHTTP(w, r.WithContext(ContextWithPrincipal(r.Context(), LocalAdminPrincipal())))
			return
		}
		if isPublicPath(r.URL.Path) {
			// TRAP: with auth on, a public path is exempt from *requiring* a
			// token, not from reading one, and it never carries the local
			// admin: a valid bearer brings its own principal, anything else
			// (no token, an invalid one: never a 401 here) brings none, so the
			// zero principal reads nothing. /health relies on it to name KBs
			// only to a caller that proved who it is.
			if token, ok := extractBearer(r); ok {
				if principal, valid := ts.PrincipalOf(token); valid {
					r = r.WithContext(ContextWithPrincipal(r.Context(), principal))
				}
			}
			next.ServeHTTP(w, r)
			return
		}
		token, ok := extractBearer(r)
		if !ok {
			unauthorized(w)
			return
		}
		// One snapshot for the token and the mapping, so a reload between the
		// two cannot pair a token with another generation's groups.
		proxy := ts.snap.Load().proxy
		entry, st := ts.lookupEntry(token)
		if st != lookupOK {
			unauthorized(w)
			return
		}
		principal := clonePrincipal(entry.principal)
		if entry.proxy && proxy != nil {
			// TRAP: the identity header is believed only from a trusted peer
			// AND on a proxy token (resolve checks the peer); on any other
			// token or from any other peer it is ignored, never an error.
			var status int
			var msg string
			if principal, status, msg = proxy.resolve(r, principal); status != 0 {
				http.Error(w, msg, status)
				return
			}
		}
		ctx := ContextWithPrincipal(r.Context(), principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// WellKnownProtectedResourcePath is the RFC 9728 discovery path for OAuth
// Protected Resource Metadata. It is exempted from authentication here (by
// definition it must be public) and served by mcpserver.MultiKBServer.Handler
// using ProtectedResourceMetadata.
const WellKnownProtectedResourcePath = "/.well-known/oauth-protected-resource"

// isPublicPath reports whether a request path must be reachable without a token
// (the middleware still reads one, to attach its principal):
// the health endpoint (used by k8s liveness/readiness probes), the OAuth
// protected-resource metadata (RFC 9728, which by definition must be public),
// and the embedded UI's own static assets.
//
// The UI is exempt because it has to be: it is the page that *asks* for the
// bearer token, so a browser must be able to load it while holding none. What
// it serves is the shipped bundle and nothing else — no server state, no KB
// content, no configuration — and everything it later reads comes from
// /api/ui/v1, which is not exempt and is filtered by the caller's principal.
// "/" is deliberately NOT exempt: it is an ordinary path as far as this
// middleware is concerned, and the courtesy redirect that sends a browser from
// it to the UI is applied outside the auth chain (webui.RedirectRoot). With
// the UI disabled none of the /ui paths is routed at all and they 404
// regardless of this exemption.
func isPublicPath(path string) bool {
	return path == "/health" ||
		path == WellKnownProtectedResourcePath ||
		path == "/ui" || strings.HasPrefix(path, "/ui/")
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
	if len(p.Groups) > 0 {
		p.Groups = append([]string(nil), p.Groups...)
	}
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
//
// Malformed components are skipped. That is tolerable ONLY because every
// configuration path validates with ParseScopesStrict first (D179): on its own,
// silently dropping a component makes a typo indistinguishable from an absent
// restriction, and a token whose every restriction vanished used to be read as
// an unrestricted legacy admin.
func ParseScopes(scope string) []KBScope {
	out, _ := parseScopes(scope)
	return out
}

// ParseScopesStrict parses scope and rejects anything ParseScopes would have
// discarded, naming the offending component. It never includes the whole input
// in the error: a scope field is a place credentials get pasted by mistake, and
// a startup error is a place they must not appear.
func ParseScopesStrict(scope string) ([]KBScope, error) {
	out, bad := parseScopes(scope)
	if len(bad) > 0 {
		return nil, fmt.Errorf("invalid scope %q: want kb:<name>:r|rw", bad[0])
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no scope declared: want kb:<name>:r|rw")
	}
	return out, nil
}

// parseScopes splits scope and reports both the components it understood and
// the ones it did not, so the tolerant and strict entry points cannot disagree
// about the grammar.
func parseScopes(scope string) (parsed []KBScope, malformed []string) {
	for _, part := range strings.FieldsFunc(scope, func(r rune) bool {
		return unicode.IsSpace(r) || r == ';'
	}) {
		// expected format: kb:<name>:<r|rw>
		segs := strings.SplitN(part, ":", 3)
		if len(segs) != 3 || segs[0] != "kb" || segs[1] == "" {
			malformed = append(malformed, part)
			continue
		}
		switch segs[2] {
		case "r":
			parsed = append(parsed, KBScope{KB: segs[1]})
		case "rw":
			parsed = append(parsed, KBScope{KB: segs[1], Write: true})
		default:
			// An unknown access value ("write", "*", "") is not read access:
			// reading it as one grants something nobody wrote.
			malformed = append(malformed, part)
		}
	}
	return parsed, malformed
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
