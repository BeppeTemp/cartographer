package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
)

// ErrModelAuth is wrapped by the error of a 401 or 403 from the model API, so
// a caller can say "check the key variable" without parsing text.
var ErrModelAuth = errors.New("worker: model API rejected the credential")

const (
	maxAttempts      = 3
	maxRetryAfter    = 30 * time.Second
	maxResponseBytes = 16 << 20
	maxErrorBody     = 512
	defaultTimeout   = 120 * time.Second
)

// Option tunes a model client.
type Option func(*transport)

// WithHTTPClient replaces the base HTTP client (tests); redirects still obey
// the client package's policy.
func WithHTTPClient(hc *http.Client) Option { return func(t *transport) { t.base = hc } }

// WithAllowInsecureHTTP lets the endpoint be a non-loopback http:// URL (the
// container case: a model server on a private network).
func WithAllowInsecureHTTP(allow bool) Option { return func(t *transport) { t.allowInsecure = allow } }

// WithRetryDelay sets the first backoff step (default 1 s; the second is
// twice it). Tests use a few milliseconds.
func WithRetryDelay(d time.Duration) Option { return func(t *transport) { t.retryDelay = d } }

// transport is the HTTP half shared by both providers. key is a secret: the
// owning types redact themselves, and every error text is scrubbed of it.
type transport struct {
	baseURL       string
	key           string
	model         string
	base          *http.Client
	allowInsecure bool
	retryDelay    time.Duration
	hc            *http.Client
}

func newTransport(baseURL, key, model string, opts []Option) (*transport, error) {
	t := &transport{baseURL: strings.TrimRight(baseURL, "/"), key: key, model: model, retryDelay: time.Second}
	for _, o := range opts {
		o(t)
	}
	if t.baseURL == "" {
		return nil, errors.New("worker: model base URL is required")
	}
	if err := client.CheckServerURL(t.baseURL, t.allowInsecure); err != nil {
		return nil, err
	}
	if t.base == nil {
		t.base = &http.Client{Timeout: defaultTimeout}
	}
	hc := client.HTTPClient(t.base, t.allowInsecure)
	policy := hc.CheckRedirect
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		// Go drops Authorization on a cross-host redirect but keeps custom
		// headers such as x-api-key: drop both whenever the host:port changes.
		if len(via) > 0 && req.URL.Host != via[0].URL.Host {
			req.Header.Del("x-api-key")
			req.Header.Del("Authorization")
		}
		return policy(req, via)
	}
	t.hc = hc
	return t, nil
}

// scrub removes the key from text that may reach an error.
func (t *transport) scrub(s string) string {
	if t.key == "" {
		return s
	}
	return strings.ReplaceAll(s, t.key, "[redacted]")
}

// post sends body to baseURL+path with retries and returns the 2xx body.
func (t *transport) post(ctx context.Context, path string, headers map[string]string, body []byte) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		out, retryAfter, retry, err := t.once(ctx, path, headers, body)
		if err == nil {
			return out, nil
		}
		if !retry || attempt == maxAttempts-1 {
			return nil, err
		}
		if err := sleepCtx(ctx, t.retryWait(attempt, retryAfter)); err != nil {
			return nil, err
		}
	}
}

// retryWait is the wait before the attempt after failure number n (0-based):
// the Retry-After seconds when given (capped), else exponential backoff.
func (t *transport) retryWait(n int, retryAfter string) time.Duration {
	if retryAfter != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && secs >= 0 {
			d := time.Duration(secs) * time.Second
			if d > maxRetryAfter {
				d = maxRetryAfter
			}
			return d
		}
	}
	return t.retryDelay << n
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func retryable(status int) bool {
	switch status {
	case 429, 500, 502, 503, 504, 529:
		return true
	}
	return false
}

// once performs one attempt.
func (t *transport) once(ctx context.Context, path string, headers map[string]string, body []byte) (out []byte, retryAfter string, retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, "", false, fmt.Errorf("worker: build model request: %s", t.scrub(err.Error()))
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := t.hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", false, ctx.Err()
		}
		return nil, "", true, fmt.Errorf("worker: model request failed: %s", t.scrub(err.Error()))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", false, ctx.Err()
		}
		return nil, "", true, fmt.Errorf("worker: read model response: %s", t.scrub(err.Error()))
	}
	if len(data) > maxResponseBytes {
		return nil, "", false, fmt.Errorf("worker: model response over %d bytes", maxResponseBytes)
	}
	if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
		return data, "", false, nil
	}
	snippet := data
	if len(snippet) > maxErrorBody {
		snippet = snippet[:maxErrorBody]
	}
	msg := t.scrub(fmt.Sprintf("model API returned HTTP %d: %s", resp.StatusCode, snippet))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, "", false, fmt.Errorf("%w: %s", ErrModelAuth, msg)
	}
	return nil, resp.Header.Get("Retry-After"), retryable(resp.StatusCode), errors.New("worker: " + msg)
}
