package hooks

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

// MaxBody caps a response body (and so the memory one hook can take).
const MaxBody = 64 << 10

// Decision is what a veto answers. There is no allow: any value other than
// "deny" is no opinion, so a hook can only turn a core allow into a deny.
type Decision int

const (
	NoOpinion Decision = iota
	Deny
)

// Request is what a point hands to Call: the KB it concerns, if any, and the
// point's own fields. The envelope's reserved keys (version, hook, request_id,
// time, kb) are set by Call and win over Fields.
type Request struct {
	KB     string
	Fields map[string]any
}

// Answer is a parsed response. Raw holds every field of the body, so a point
// reads its own (user, groups, value...) without this package knowing them.
type Answer struct {
	Decision Decision
	Reason   string // bounded and printable; for a Deny only
	Raw      map[string]json.RawMessage
}

// Client calls hooks. The zero value is not usable: use NewClient.
type Client struct {
	http *http.Client
	now  func() time.Time
}

// NewClient returns a Client over rt (nil: a clone of the default transport,
// which verifies TLS). A redirect is never followed whatever the transport:
// a 3xx is a non-2xx answer, hence a failure, so the bearer never leaves for
// another host.
func NewClient(rt http.RoundTripper, now func() time.Time) *Client {
	if rt == nil {
		rt = http.DefaultTransport.(*http.Transport).Clone()
	}
	if now == nil {
		now = time.Now
	}
	return &Client{now: now, http: &http.Client{
		Transport:     rt,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// Call POSTs one envelope and parses the answer. Any failure (timeout,
// connection, non-2xx, unparsable or oversized body, wrong version) is an
// error; applying the failure policy is the caller's. The deadline covers
// connect, headers and body read. The error is one of a fixed set of classes
// (see transportError) and never carries the URL, an address, a body or the
// credential. A call the caller cancelled returns ErrCanceled, which is not a
// failure of the hook.
func (c *Client) Call(ctx context.Context, h *Hook, credential string, req Request) (Answer, error) {
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	id, err := requestID()
	if err != nil {
		return Answer{}, err
	}
	env := map[string]any{}
	for k, v := range req.Fields {
		env[k] = v
	}
	env["version"], env["hook"], env["request_id"], env["time"] = Version, h.Name, id, c.now().UTC().Format(time.RFC3339Nano)
	delete(env, "kb")
	if req.KB != "" {
		env["kb"] = req.KB
	}
	body, err := json.Marshal(env)
	if err != nil {
		return Answer{}, fmt.Errorf("encode request: %w", err)
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return Answer{}, errors.New("build request")
	}
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("Cartographer-Hook", h.Point)
	hr.Header.Set("Cartographer-Hook-Version", fmt.Sprint(Version))
	hr.Header.Set("Cartographer-Request-Id", id)
	hr.Header.Set("Authorization", "Bearer "+credential)
	resp, err := c.http.Do(hr)
	if err != nil {
		return Answer{}, transportError(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Answer{}, fmt.Errorf("status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
	if err != nil {
		return Answer{}, transportError(ctx, err)
	}
	if len(data) > MaxBody {
		return Answer{}, fmt.Errorf("response body over %d bytes", MaxBody)
	}
	return parseAnswer(data)
}

// parseAnswer reads the response body: unknown fields are ignored, a version
// other than ours is a failure, and only the literal "deny" denies.
func parseAnswer(data []byte) (Answer, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return Answer{}, errors.New("response is not a JSON object")
	}
	var version int
	if json.Unmarshal(raw["version"], &version) != nil || version != Version {
		return Answer{}, fmt.Errorf("response version is not %d", Version)
	}
	a := Answer{Raw: raw}
	var decision string
	_ = json.Unmarshal(raw["decision"], &decision) // a missing or odd value is no opinion
	if decision == "deny" {
		a.Decision = Deny
		var reason string
		_ = json.Unmarshal(raw["reason"], &reason)
		a.Reason = SanitizeReason(reason)
	}
	return a, nil
}

// SanitizeReason keeps the printable characters of a hook's deny reason and cuts
// it at MaxReasonLen: it is shown to a caller, so it carries no control
// character, newline or unbounded text.
func SanitizeReason(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if !unicode.IsPrint(r) {
			continue
		}
		if n++; n > MaxReasonLen {
			break
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// ErrCanceled is the result of a call whose context the caller cancelled: the
// hook did not fail, so a caller must not count it against the hook.
var ErrCanceled = errors.New("canceled")

// transportError reduces a transport failure to a fixed class: timeout,
// canceled, dns, tls, connection refused or connection failed. The raw error
// is dropped on purpose: dial and DNS errors carry the host and the resolved
// address, and an operator's hook URL is not for a log or an audit entry.
func transportError(ctx context.Context, err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	var (
		ne  net.Error
		dns *net.DNSError
		ua  x509.UnknownAuthorityError
		ci  x509.CertificateInvalidError
		he  x509.HostnameError
		cv  *tls.CertificateVerificationError
		rh  tls.RecordHeaderError
	)
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		return ErrCanceled
	case errors.Is(ctx.Err(), context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()):
		return errors.New("timeout")
	case errors.As(err, &dns):
		return errors.New("dns lookup failed")
	case errors.As(err, &ua) || errors.As(err, &ci) || errors.As(err, &he) || errors.As(err, &cv) || errors.As(err, &rh):
		return errors.New("tls failure")
	case isConnRefused(err):
		return errors.New("connection refused")
	}
	return errors.New("connection failed")
}

func requestID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
