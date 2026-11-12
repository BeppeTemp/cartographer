package auth

// Per-user identity from a trusted proxy (D1894, Proxy identity).
//
// An SSO proxy authenticates the person and forwards who it is in a header. The
// server believes the header only from a TCP peer the operator listed AND on a
// request that carries a bearer token declared proxy: true; the person then
// gets the permissions their groups map to, clamped by that token's own policy.

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxIdentityField = 128
	maxIdentityGroup = 64
)

// Identity is who a proxy says made the request.
type Identity struct {
	User   string
	Groups []string
	// Via is the id of the proxy token the request came in on.
	Via string
}

// IdentityResolver reads the identity of a request that arrived through a
// trusted proxy token. found false: the request carries none and stays the
// proxy token's own. An error is a malformed identity (answered 400). The
// header reader is the default; a plugin may supply another one, and the trust
// checks and the group mapping run in either case.
type IdentityResolver func(r *http.Request, via Principal) (id Identity, found bool, err error)

// The two failures an IdentityHook reports: the proxy's request is refused with
// 503 (the hook is down under fail_closed) or 403 (the hook denied the user).
var (
	ErrIdentityUnavailable = errors.New("identity service unavailable")
	ErrIdentityDenied      = errors.New("identity denied")
)

// IdentityHook may replace the identity the proxy header named. It runs only
// for a trusted peer on a proxy token whose header carries an identity (header,
// groups and all), after the default reader. supplied false: no opinion, or a
// failure the operator chose to ignore (fail_open), and header stays in force.
// The answer is only names: the group mapping and the clamp run on it as on
// any other identity, so it can never exceed the proxy token.
type IdentityHook func(r *http.Request, via Principal, header Identity) (id Identity, supplied bool, err error)

// ProxyConfig is the compiled proxy_identity section.
type ProxyConfig struct {
	Trusted         []netip.Prefix
	UserHeader      string
	GroupsHeader    string
	GroupsSeparator string
	// GroupPolicies is the permission set each group maps to (the union of its
	// roles), before the clamp.
	GroupPolicies map[string]Policy
	// Resolver replaces the header reader when set.
	Resolver IdentityResolver
	// Hook is the operator's identity hook, nil when there is none.
	Hook IdentityHook
}

// ParseTrustedAddresses reads CIDRs strictly: a /0 prefix (trust everyone) and
// anything that is not a prefix or an address is an error. A bare address is a
// host prefix. There is no implicit loopback trust.
func ParseTrustedAddresses(list []string) ([]netip.Prefix, error) {
	if len(list) == 0 {
		return nil, errors.New("trusted_addresses is required")
	}
	out := make([]netip.Prefix, 0, len(list))
	for _, s := range list {
		s = strings.TrimSpace(s)
		p, err := netip.ParsePrefix(s)
		if err != nil {
			a, aerr := netip.ParseAddr(s)
			if aerr != nil {
				return nil, fmt.Errorf("trusted_addresses: %q is not a CIDR or an address", s)
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		if p.Bits() == 0 {
			return nil, fmt.Errorf("trusted_addresses: %q trusts every address", s)
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

func (c *ProxyConfig) trusts(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	a = a.WithZone("").Unmap()
	for _, p := range c.Trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// headerIdentity is the default IdentityResolver.
func (c *ProxyConfig) headerIdentity(r *http.Request, via Principal) (Identity, bool, error) {
	vals := r.Header.Values(c.UserHeader)
	// Repetition first: an empty first value must not hide a second one.
	if len(vals) > 1 {
		return Identity{}, false, errors.New("the user header is repeated")
	}
	if len(vals) == 0 || strings.TrimSpace(vals[0]) == "" {
		return Identity{}, false, nil
	}
	id := Identity{User: strings.TrimSpace(vals[0]), Via: via.ID}
	if c.GroupsHeader != "" {
		sep := c.GroupsSeparator
		if sep == "" {
			sep = ","
		}
		for _, g := range strings.Split(strings.Join(r.Header.Values(c.GroupsHeader), sep), sep) {
			if g = strings.TrimSpace(g); g != "" {
				id.Groups = append(id.Groups, g)
			}
		}
	}
	return id, true, nil
}

// checkIdentity refuses, never truncates, a value outside the limits.
func checkIdentity(id Identity) error {
	field := func(what, v string) error {
		if v == "" || len(v) > maxIdentityField || !utf8.ValidString(v) || strings.ContainsAny(v, "<>") {
			return fmt.Errorf("%s is empty, longer than %d bytes or has a character that is not allowed", what, maxIdentityField)
		}
		for _, r := range v {
			if !unicode.IsPrint(r) {
				return fmt.Errorf("%s has a character that is not printable", what)
			}
		}
		return nil
	}
	if err := field("user", id.User); err != nil {
		return err
	}
	if len(id.Groups) > maxIdentityGroup {
		return fmt.Errorf("more than %d groups", maxIdentityGroup)
	}
	for _, g := range id.Groups {
		if err := field("group", g); err != nil {
			return err
		}
	}
	return nil
}

// Clamp narrows p by a ceiling: a permission survives only on a KB where the
// ceiling has an unselected (whole-KB) rule of the mode it needs, so a ceiling
// scoped to maps, journals or types grants nothing here (ValidateAuth refuses
// such a proxy token; this is the defence in depth). It loses Write where the
// ceiling cannot write whole, and takes the ceiling's restrictions: artifacts
// it cannot write, secrets it cannot see (any mode) or cannot resolve (write).
// The proxy token is the ceiling, so a read-only SSO bearer yields read-only
// people and the person never holds an authority the token lacks.
func Clamp(p, ceiling Policy) Policy {
	if ceiling.Admin {
		return p
	}
	out := Policy{}
	for _, perm := range p.Permissions {
		if !ceiling.AllowsWholeKB(perm.KB, false) {
			continue
		}
		perm.Write = perm.Write && ceiling.AllowsWholeKB(perm.KB, true)
		perm.ReadOnlyArtifacts = perm.ReadOnlyArtifacts || !ceiling.AllowsArtifactWrite(perm.KB)
		perm.NoSecrets = perm.NoSecrets || !ceiling.SecretsVisible(perm.KB) || (perm.Write && !ceiling.AllowsSecrets(perm.KB))
		out.Permissions = append(out.Permissions, perm)
	}
	return out
}

// resolve turns a request on a proxy token into the person's principal. ok
// false: respond with status and msg. A request that is not from a trusted
// peer, or has no identity, passes as the token's own.
func (c *ProxyConfig) resolve(r *http.Request, token Principal) (p Principal, status int, msg string) {
	if c == nil || !c.trusts(r.RemoteAddr) {
		return token, 0, ""
	}
	read := c.Resolver
	if read == nil {
		read = c.headerIdentity
	}
	id, found, err := read(r, token)
	if err != nil {
		log.Printf("auth: proxy %q sent a malformed identity: %v", token.ID, err)
		return Principal{}, http.StatusBadRequest, "malformed identity"
	}
	if !found {
		return token, 0, ""
	}
	// The header identity is judged BEFORE the hook sees it: over-limit proxy
	// input never reaches a plugin.
	if err := checkIdentity(id); err != nil {
		log.Printf("auth: proxy %q sent an invalid identity: %v", token.ID, err)
		return Principal{}, http.StatusBadRequest, "invalid identity"
	}
	if c.Hook != nil {
		hid, supplied, herr := c.Hook(r, token, id)
		switch {
		case errors.Is(herr, ErrIdentityUnavailable):
			return Principal{}, http.StatusServiceUnavailable, "identity service unavailable"
		case errors.Is(herr, ErrIdentityDenied):
			return Principal{}, http.StatusForbidden, "identity denied"
		case herr != nil: // a hook bug is never a pass: keep the header identity out of it
			return Principal{}, http.StatusServiceUnavailable, "identity service unavailable"
		case supplied:
			// The plugin's answer has the same limits as a header.
			if err := checkIdentity(hid); err != nil {
				log.Printf("auth: identity hook for proxy %q answered an invalid identity: %v", token.ID, err)
				return Principal{}, http.StatusBadRequest, "invalid identity"
			}
			id = hid
		}
	}
	id.Via = token.ID
	var mapped Policy
	for _, g := range id.Groups {
		mapped.Permissions = append(mapped.Permissions, c.GroupPolicies[g].Permissions...)
	}
	mapped = Clamp(mapped, token.Policy)
	if len(mapped.Permissions) == 0 {
		log.Printf("auth: user %q behind proxy %q has no group that maps to a role", id.User, token.ID)
		return Principal{}, http.StatusForbidden, "no role for this user"
	}
	return Principal{ID: "user:" + id.User, Policy: mapped, Kind: KindHuman, Via: id.Via, Groups: id.Groups}, 0, ""
}
