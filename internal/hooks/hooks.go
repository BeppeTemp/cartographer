// Package hooks calls the hook points of the core: a versioned HTTP webhook
// with a timeout, an operator-chosen failure policy, a circuit breaker and a
// dry-run mode. A hook supplies an input or denies; "no opinion" is the
// default, and no answer can turn a core deny into an allow. The package is a
// leaf (standard library only): the points that call it live in mcpserver, auth
// and cmd/cartographer, which import it.
package hooks

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Version is the hook protocol level this build speaks. It travels in the
// Cartographer-Hook-Version header and in both bodies; additive fields do not
// change it, a breaking change is a new version.
const Version = 1

// The hook points. A point is the place the core calls; a hook is the
// operator's entry that serves it.
const (
	PointWriteVeto      = "write_veto"
	PointAuditSink      = "audit_sink"
	PointSecretResolver = "secret_resolver"
	PointIdentity       = "identity"
)

// Failure policies, mandatory in every entry (there is no default).
const (
	FailOpen   = "fail_open"
	FailClosed = "fail_closed"
)

// Modes. Enforce is the default.
const (
	ModeEnforce = "enforce"
	ModeDryRun  = "dry_run"
)

const (
	// DefaultTimeout and MaxTimeout bound the whole call, connect and body read
	// included.
	DefaultTimeout = 2 * time.Second
	MaxTimeout     = 10 * time.Second
	// DefaultCacheTTL is the cache_ttl of the resolver and identity points when
	// the operator does not say.
	DefaultCacheTTL = 60 * time.Second
	// MaxCacheTTL bounds cache_ttl of the resolver and the identity point.
	MaxCacheTTL = 600 * time.Second
	// MaxReasonLen bounds a deny reason, in printable characters.
	MaxReasonLen = 200
	// maxIdentityHeaders bounds the headers an identity hook is handed.
	maxIdentityHeaders = 8
)

var (
	nameRe       = regexp.MustCompile(`^([a-z][a-z0-9-]*)/([a-z0-9][a-z0-9-]*)$`)
	headerNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)
	credentialRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// Hook is one resolved entry of the server config's hooks list.
type Hook struct {
	Name          string // <plugin>/<name>
	Point         string
	Plugin        string
	URL           string
	Credential    string // a file name in the credentials directory, never the secret
	Timeout       time.Duration
	FailurePolicy string
	Mode          string

	KBs       []string      // write_veto: narrow to these KBs; empty means every KB
	BatchSize int           // audit_sink
	Blocking  bool          // audit_sink
	CacheTTL  time.Duration // secret_resolver and identity
	Headers   []string      // identity: request headers handed to the hook
}

// AppliesTo reports whether a veto hook covers the KB.
func (h *Hook) AppliesTo(kb string) bool {
	if len(h.KBs) == 0 {
		return true
	}
	for _, k := range h.KBs {
		if k == kb {
			return true
		}
	}
	return false
}

// Normalize fills the defaults and judges the entry. Every error names the hook
// and never carries the credential's content.
func (h *Hook) Normalize() error {
	m := nameRe.FindStringSubmatch(h.Name)
	if m == nil {
		return fmt.Errorf("name %q: want <plugin>/<name> (lowercase letters, digits and dashes)", h.Name)
	}
	fail := func(f string, a ...any) error { return fmt.Errorf("hook %q: %s", h.Name, fmt.Sprintf(f, a...)) }
	switch h.Point {
	case PointWriteVeto, PointAuditSink, PointSecretResolver, PointIdentity:
	default:
		return fail("point %q: want write_veto, audit_sink, secret_resolver or identity", h.Point)
	}
	if h.Plugin != m[1] {
		return fail("plugin %q must be the namespace of the name (%q)", h.Plugin, m[1])
	}
	if err := checkURL(h.URL); err != nil {
		return fail("url: %v", err)
	}
	if !credentialRe.MatchString(h.Credential) {
		return fail("credential: want the name of a file in the credentials directory")
	}
	if h.Timeout == 0 {
		h.Timeout = DefaultTimeout
	}
	if h.Timeout < 0 || h.Timeout > MaxTimeout {
		return fail("timeout %s: want up to %s", h.Timeout, MaxTimeout)
	}
	switch h.FailurePolicy {
	case FailOpen, FailClosed:
	case "":
		return fail("failure_policy is required (fail_open or fail_closed): there is no default")
	default:
		return fail("failure_policy %q: want fail_open or fail_closed", h.FailurePolicy)
	}
	switch h.Mode {
	case "":
		h.Mode = ModeEnforce
	case ModeEnforce, ModeDryRun:
	default:
		return fail("mode %q: want enforce or dry_run", h.Mode)
	}
	if h.Point == PointSecretResolver && h.FailurePolicy != FailClosed {
		return fail("failure_policy: a secret resolver has nothing to open to, it must be fail_closed")
	}
	if h.Point == PointSecretResolver && h.Mode == ModeDryRun {
		// A resolver supplies a value or nothing: there is no observe-only
		// resolver, and silently enforcing one would hand out live values.
		return fail("mode: dry_run does not apply to a secret resolver (it supplies a value or nothing)")
	}
	if len(h.KBs) > 0 && h.Point != PointWriteVeto {
		return fail("kbs applies to write_veto only")
	}
	if (h.BatchSize != 0 || h.Blocking) && h.Point != PointAuditSink {
		return fail("batch_size and blocking apply to audit_sink only")
	}
	if h.CacheTTL != 0 && h.Point != PointSecretResolver && h.Point != PointIdentity {
		return fail("cache_ttl applies to secret_resolver and identity only")
	}
	if len(h.Headers) > 0 && h.Point != PointIdentity {
		return fail("headers applies to identity only")
	}
	if len(h.Headers) > maxIdentityHeaders {
		return fail("headers: at most %d", maxIdentityHeaders)
	}
	for _, name := range h.Headers {
		// The caller's bearer must never reach a plugin; the others are the
		// operator's explicit choice (a session cookie, a forwarded token).
		if !headerNameRe.MatchString(name) || strings.EqualFold(name, "Authorization") || strings.EqualFold(name, "Proxy-Authorization") {
			return fail("headers: %q is not an allowed header name", name)
		}
	}
	if h.CacheTTL == 0 && (h.Point == PointSecretResolver || h.Point == PointIdentity) {
		h.CacheTTL = DefaultCacheTTL
	}
	if h.CacheTTL < 0 || h.CacheTTL > MaxCacheTTL {
		return fail("cache_ttl %s: want up to %s", h.CacheTTL, MaxCacheTTL)
	}
	if h.Point == PointAuditSink {
		if h.BatchSize < 0 || h.BatchSize > 1000 {
			return fail("batch_size %d: want 1 to 1000", h.BatchSize)
		}
		if h.BatchSize == 0 {
			h.BatchSize = 100
		}
	}
	return nil
}

// NormalizeAll judges every entry and refuses a duplicate name.
func NormalizeAll(hs []Hook) error {
	seen := map[string]bool{}
	for i := range hs {
		if err := hs[i].Normalize(); err != nil {
			return fmt.Errorf("hooks[%d]: %w", i, err)
		}
		if seen[hs[i].Name] {
			return fmt.Errorf("hooks[%d]: hook %q is declared twice", i, hs[i].Name)
		}
		seen[hs[i].Name] = true
	}
	return nil
}

// checkURL accepts https, and http only to a loopback host (the client's rule,
// D1023). A URL with credentials in it is refused: the credential is a file,
// never part of the address.
func checkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("not a valid URL")
	}
	if u.Hostname() == "" {
		return fmt.Errorf("has no host")
	}
	if u.User != nil {
		return fmt.Errorf("must not carry credentials")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		if isLoopback(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("http:// is accepted only to a loopback host")
	}
	return fmt.Errorf("scheme must be https (or http to a loopback host)")
}

// isLoopback parses the host rather than matching a prefix: "127.0.0.1.example.com"
// is a DNS name, not loopback.
func isLoopback(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
