package mcpprobe

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Kind classifies a network error. The empty Kind means no error.
type Kind string

const (
	KindDNS                 Kind = "dns"
	KindRefused             Kind = "refused"
	KindTimeout             Kind = "timeout"
	KindTLSUnknownAuthority Kind = "tls_unknown_authority"
	KindTLSOther            Kind = "tls_other"
	KindOther               Kind = "other"
)

// DialTimeout is how long Dial waits for DNS, TCP and TLS together: the same 15 s
// as gitx.FetchTimeout (internal/gitx/gitx.go), so a network that drops packets
// is reported as a timeout instead of hanging the command.
const DialTimeout = 15 * time.Second

// Classify says which fault err is. TLS comes before the timeout test because a
// handshake that fails is not a slow network, and DNS before it because a
// resolver that does not answer is a name problem to the person reading.
func Classify(err error) Kind {
	if err == nil {
		return ""
	}
	var unknown x509.UnknownAuthorityError
	if errors.As(err, &unknown) {
		return KindTLSUnknownAuthority
	}
	var verify *tls.CertificateVerificationError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var record tls.RecordHeaderError
	if errors.As(err, &verify) || errors.As(err, &hostname) || errors.As(err, &invalid) || errors.As(err, &record) {
		return KindTLSOther
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return KindDNS
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return KindTimeout
	}
	// The text is the fallback for Windows, where the errno of a refused connection
	// is not syscall.ECONNREFUSED.
	if errors.Is(err, syscall.ECONNREFUSED) || strings.Contains(err.Error(), "refused") {
		return KindRefused
	}
	if strings.HasPrefix(err.Error(), "tls:") || strings.Contains(err.Error(), "tls: ") {
		return KindTLSOther
	}
	return KindOther
}

// Hint is one sentence on what to do about k, empty when there is nothing to add.
func Hint(k Kind) string {
	switch k {
	case KindTLSUnknownAuthority:
		return "the server probably does not send its intermediate CA; curl and the OS fetch it, Node and Go do not: serve the full chain, or set NODE_EXTRA_CA_CERTS"
	case KindTLSOther:
		return "the certificate is expired, issued for another name, or the port does not speak TLS"
	case KindDNS:
		return "the name does not resolve from this machine; check the network or VPN"
	case KindRefused:
		return "nothing listens on that address and port, or a firewall rejects it"
	case KindTimeout:
		return "the host did not answer in time; a VPN or a firewall that drops packets looks like this"
	}
	return ""
}

// Dial resolves the host of rawURL, opens a TCP connection and, for https, runs
// the TLS handshake, then closes it. It sends no MCP bytes. timeout 0 means
// DialTimeout. The system proxy is not used: the probe answers whether this
// machine reaches the host itself.
func Dial(ctx context.Context, rawURL string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = DialTimeout
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("invalid url")
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(u.Hostname(), port))
	if err != nil {
		return err
	}
	defer conn.Close()
	if u.Scheme != "https" {
		return nil
	}
	tc := tls.Client(conn, &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12})
	return tc.HandshakeContext(ctx)
}
