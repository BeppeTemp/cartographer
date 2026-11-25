package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// DefaultTLSHSTSMaxAge is the Strict-Transport-Security max-age of a TLS
// listener (about six months).
const DefaultTLSHSTSMaxAge = 4380 * time.Hour

// TLSHealthOff is the tls.health_addr value that runs no health listener.
const TLSHealthOff = "off"

// TLSConfig is the `tls:` block: the HTTP listener served over TLS 1.3. The
// certificate and key are tls.crt and tls.key of the credentials directory,
// never configured by path. A later `client_auth` key (mTLS) is not a setting
// yet: an unknown key under tls: stops the load (checkAuthKeys), never a warning.
type TLSConfig struct {
	Enabled bool
	// HealthAddr is the explicit loopback listener of /health and /ready, or
	// TLSHealthOff; empty means the derived default (TLSHealthAddr).
	HealthAddr     string
	HSTSMaxAge     time.Duration
	ReloadInterval time.Duration
}

// rawTLS mirrors the `tls:` block; pointers tell absent from a zero value.
type rawTLS struct {
	Enabled        *bool   `yaml:"enabled"`
	HealthAddr     *string `yaml:"health_addr"`
	HSTSMaxAge     *string `yaml:"hsts_max_age"`
	ReloadInterval *string `yaml:"reload_interval"`
}

func applyRawTLS(t *TLSConfig, r rawTLS) error {
	if r.Enabled != nil {
		t.Enabled = *r.Enabled
	}
	if r.HealthAddr != nil {
		t.HealthAddr = strings.TrimSpace(*r.HealthAddr)
	}
	for _, d := range []struct {
		key string
		in  *string
		out *time.Duration
	}{
		{"tls.hsts_max_age", r.HSTSMaxAge, &t.HSTSMaxAge},
		{"tls.reload_interval", r.ReloadInterval, &t.ReloadInterval},
	} {
		if d.in == nil {
			continue
		}
		v, err := time.ParseDuration(strings.TrimSpace(*d.in))
		if err != nil {
			return fmt.Errorf("%s: %w", d.key, err)
		}
		if v < 0 {
			return fmt.Errorf("%s %s: must not be negative", d.key, v)
		}
		*d.out = v
	}
	return nil
}

// IsLoopbackListenAddr reports whether a listen address binds only the
// loopback interface. A bare port (":39273") or a wildcard host binds every
// interface, so it is not loopback.
func IsLoopbackListenAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// TLSHealthAddr is the health listener address in effect: "" when TLS is off
// or tls.health_addr is off, the explicit key when set, else 127.0.0.1 on the
// public port plus one. "" also when the default cannot be derived (a
// non-numeric port, 0 or 65535); ValidateTLS refuses that configuration.
func TLSHealthAddr(listen string, t TLSConfig) string {
	if !t.Enabled || t.HealthAddr == TLSHealthOff {
		return ""
	}
	if t.HealthAddr != "" {
		return t.HealthAddr
	}
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return ""
	}
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 || n >= 65535 {
		return ""
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(n+1))
}

// LocalProbeAddr is the address the local /health and /ready probes read: the
// health listener when TLS is on, else the public address. Never use it for
// /mcp: the health listener answers 404 to everything but /health and /ready.
func (c *Config) LocalProbeAddr() string {
	if c.TLS.Enabled {
		return TLSHealthAddr(c.HTTP, c.TLS)
	}
	return c.HTTP
}

// ValidateTLS judges the tls settings against the listen address: an explicit
// health_addr is off or a loopback host:port other than the public address, and
// with TLS on the default must be derivable. Called at load and again once the
// environment and flags are merged.
func ValidateTLS(c *Config) error {
	t := c.TLS
	if t.HealthAddr != "" && t.HealthAddr != TLSHealthOff {
		host, port, err := net.SplitHostPort(t.HealthAddr)
		if err != nil || host == "" || port == "" {
			return fmt.Errorf("tls.health_addr %q: want a loopback host:port or %q", t.HealthAddr, TLSHealthOff)
		}
		if !IsLoopbackListenAddr(t.HealthAddr) {
			return fmt.Errorf("tls.health_addr %q: must be a loopback address (127.0.0.0/8, [::1] or localhost)", t.HealthAddr)
		}
		if t.HealthAddr == c.HTTP {
			return fmt.Errorf("tls.health_addr %q: must differ from the public address", t.HealthAddr)
		}
	}
	if t.Enabled && c.HTTP != "" && t.HealthAddr == "" && TLSHealthAddr(c.HTTP, t) == "" {
		return fmt.Errorf("tls.health_addr: the public address %q has no port the default can derive from (port 0 or 65535): set tls.health_addr or %q", c.HTTP, TLSHealthOff)
	}
	return nil
}
