package config

// Strict validation of the effective HTTP authentication configuration (D179).
//
// It runs on the CONFIGURATION THAT WILL ACTUALLY BE SERVED — after YAML,
// environment and flags have been merged — because that is the only shape whose
// mistakes reach a listener. Validating only the YAML left every
// environment- or flag-supplied token unchecked.
//
// The rule throughout: an invalid declaration is an error, never a silently
// narrower or wider interpretation. Every diagnostic names the offending token
// by index or principal ID and the category of the problem, and never contains a
// token value or a raw scope string — a scope field is where a credential gets
// pasted by mistake, and startup output is where it must not appear.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// ValidateAuth rejects an auth configuration that would grant more than the
// operator wrote. It is called for every mode, including "off": a latent typo
// must not become an exposure on the day someone enables authentication.
func ValidateAuth(a AuthConfig) error { return ValidateAuthAt(a, time.Now()) }

// ValidateAuthAt is ValidateAuth judged at a given instant: an expires_at not
// after it is a startup error (a typo must not silently disable a token).
func ValidateAuthAt(a AuthConfig, now time.Time) error {
	switch a.Mode {
	case "", "auto", "on", "off":
	default:
		return fmt.Errorf("mode %q: want \"auto\", \"on\" or \"off\"", a.Mode)
	}
	if a.ReloadInterval < 0 {
		return fmt.Errorf("reload_interval %s: must not be negative (0 stops the polling)", a.ReloadInterval)
	}
	if err := ValidateAuthRoles(a); err != nil {
		return err
	}
	proxyTokens := 0
	seenToken := make(map[string]string, len(a.Tokens))
	for i, tok := range a.Tokens {
		where := fmt.Sprintf("token %d", i)
		if tok.ID != "" {
			where = fmt.Sprintf("token %q", tok.ID)
		}
		if strings.TrimSpace(tok.Token) == "" {
			// resolveAuth counts configured records while the store keeps only
			// usable ones; an empty credential is exactly where those two
			// numbers diverged, and a store that silently ended up empty
			// stopped enforcing.
			return fmt.Errorf("%s: empty token value", where)
		}
		// Two entries with one secret overwrite each other in the token store
		// (last wins), so one of them would silently take the other's policy.
		// Compared by hash, and named by position: the value is never printed.
		sum := sha256.Sum256([]byte(tok.Token))
		h := hex.EncodeToString(sum[:])
		if prev, dup := seenToken[h]; dup {
			return fmt.Errorf("%s: same token value as %s", where, prev)
		}
		seenToken[h] = where
		if tok.ScopesDeclared && len(tok.Scopes) == 0 && len(tok.Roles) == 0 {
			// An empty list is not "no restriction": the admin token is the one
			// that writes neither scopes nor roles.
			return fmt.Errorf("%s: declares an empty scope or role list (omit both for an admin token)", where)
		}
		for _, scope := range tok.Scopes {
			if _, err := auth.ParseScopesStrict(scope); err != nil {
				return fmt.Errorf("%s: %w", where, err)
			}
		}
		kind, err := auth.ParseKind(tok.Kind)
		if err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		if tok.ExpiresAt != "" {
			exp, err := time.Parse(time.RFC3339, tok.ExpiresAt)
			if err != nil {
				return fmt.Errorf("%s: expires_at is not an RFC 3339 timestamp (like 2027-01-31T00:00:00Z)", where)
			}
			if !exp.After(now) {
				return fmt.Errorf("%s: expires_at %s is already past", where, tok.ExpiresAt)
			}
		}
		if strings.HasPrefix(tok.ID, "user:") {
			// "user:<name>" is the principal id of a person a proxy names.
			return fmt.Errorf("%s: an id starting with \"user:\" is reserved for people named by a proxy", where)
		}
		if strings.HasPrefix(tok.ID, auth.PluginPrefix) {
			// "plugin:<id>/..." is the principal of a token bound to a plugin.
			return fmt.Errorf("%s: an id starting with %q is reserved for tokens bound to a plugin (plugin:)", where, auth.PluginPrefix)
		}
		if tok.Plugin != "" {
			if kind != auth.KindClient {
				return fmt.Errorf("%s: a token bound to a plugin is kind client", where)
			}
			if tok.Proxy {
				return fmt.Errorf("%s: a token bound to a plugin is not a proxy token", where)
			}
			if strings.ContainsAny(tok.Plugin, "/: \t") {
				return fmt.Errorf("%s: plugin must be a plugin name, without \"/\" or \":\"", where)
			}
		}
		if tok.Proxy {
			proxyTokens++
			if err := wholeKBOnly(a, tok); err != nil {
				return fmt.Errorf("%s: %w", where, err)
			}
			if len(tok.Scopes) == 0 && len(tok.Roles) == 0 {
				return fmt.Errorf("%s: a proxy token is never admin: declare scopes or roles (they are the ceiling of every person behind it)", where)
			}
			if kind == auth.KindSurveyor {
				return fmt.Errorf("%s: a proxy token is kind human or client, not surveyor", where)
			}
		}
		if kind.NonHuman() {
			if tok.ExpiresAt == "" {
				return fmt.Errorf("%s: a %s token must declare expires_at", where, kind)
			}
			// An admin token is the one that writes neither scopes nor roles;
			// a machine token always names the KBs it may touch.
			if len(tok.Scopes) == 0 && len(tok.Roles) == 0 {
				return fmt.Errorf("%s: a %s token is never admin: declare scopes or roles naming its KBs", where, kind)
			}
		}
	}
	return validateProxyIdentity(a, proxyTokens)
}

// wholeKBOnly refuses a proxy token whose roles select maps, journals or types:
// its policy is the ceiling of every person behind it and is applied per KB, so
// a selector-scoped ceiling would not bound a whole-KB group role.
func wholeKBOnly(a AuthConfig, tok TokenSpec) error {
	for _, name := range tok.Roles {
		for _, r := range a.Roles {
			if r.Name != name {
				continue
			}
			for _, rule := range r.Rules {
				if len(rule.Maps)+len(rule.Journals)+len(rule.Types) > 0 {
					return fmt.Errorf("a proxy token's role %q selects maps, journals or types; the token is the ceiling of every person behind it and is applied per KB, so use whole-KB rules", name)
				}
			}
		}
	}
	return nil
}

// validateProxyIdentity checks the proxy_identity section and its tie to the
// proxy tokens: the section needs one, and a proxy token needs the section.
func validateProxyIdentity(a AuthConfig, proxyTokens int) error {
	p := a.ProxyIdentity
	if p == nil {
		if proxyTokens > 0 {
			return fmt.Errorf("a token is declared proxy: true but auth.proxy_identity is not")
		}
		return nil
	}
	if _, err := auth.ParseTrustedAddresses(p.TrustedAddresses); err != nil {
		return fmt.Errorf("proxy_identity: %w", err)
	}
	for _, h := range []struct{ key, v string }{{"user_header", p.UserHeader}, {"groups_header", p.GroupsHeader}} {
		if h.v == "" && h.key == "groups_header" {
			continue
		}
		if h.v == "" || strings.ContainsAny(h.v, " \t:") || strings.EqualFold(h.v, "Authorization") {
			return fmt.Errorf("proxy_identity: %s must be a header name (and not Authorization)", h.key)
		}
	}
	if strings.EqualFold(p.UserHeader, p.GroupsHeader) {
		return fmt.Errorf("proxy_identity: user_header and groups_header are the same header")
	}
	if proxyTokens == 0 {
		return fmt.Errorf("proxy_identity needs at least one token with proxy: true")
	}
	if len(p.GroupRoles) == 0 {
		return fmt.Errorf("proxy_identity: group_roles maps no group to a role, so nobody could sign in")
	}
	roles := make(map[string]bool, len(a.Roles))
	for _, r := range a.Roles {
		roles[r.Name] = true
	}
	for g, names := range p.GroupRoles {
		for _, name := range names {
			if !roles[name] {
				return fmt.Errorf("proxy_identity: group %q maps to unknown role %q", g, name)
			}
		}
	}
	return nil
}
