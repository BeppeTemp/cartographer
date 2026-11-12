package starcore

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/client"
)

// Exchange is one recorded answer of Replay.
type Exchange struct {
	Method string
	// URL is scheme://host/path, with or without its own query.
	URL string
	// Query is added to the URL's own query; the request's must equal the sum.
	Query   map[string]string
	Status  int // 0 is 200
	Headers map[string]string
	Body    string
}

// Replay makes http.request answer from recordings, for `plugin test`.
//
// It is not a seam: the host still runs the checks http.request makes before it
// goes to the network (method, scheme, credentials in the url, the
// permissions.net allow-list, the header rules, the caps and, for a recorded
// 3xx, the redirect rules), then answers from the recording. It can only
// ever hand back canned data and cannot reach a network, so a script cannot use
// it to learn anything; a request with no recording fails naming method and url.
//
// GAP: nothing is resolved or dialed, so the checks that judge the address a
// name resolves to do not run: the refusal of a private, loopback or link-local
// address and the two-key net_private grant (the manifest's permission and the
// operator's host list) are not exercised. A recipe that reaches a private host
// passes a replay and still needs the grant in a real Surveyor.
// TRAP: nothing the Surveyor runs may set CallSpec.Replay (internal/boundaries
// pins it): a Surveyor that replayed would poll recorded data as if it were live.
type Replay struct{ Exchanges []Exchange }

// RoundTrip answers the first exchange that matches the request.
func (r *Replay) RoundTrip(req *http.Request) (*http.Response, error) {
	for _, e := range r.Exchanges {
		if !e.matches(req) {
			continue
		}
		status := e.Status
		if status == 0 {
			status = http.StatusOK
		}
		h := http.Header{}
		for k, v := range e.Headers {
			h.Set(k, v)
		}
		return &http.Response{
			Status: fmt.Sprintf("%d %s", status, http.StatusText(status)), StatusCode: status,
			Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Header: h,
			Body: io.NopCloser(strings.NewReader(e.Body)), ContentLength: int64(len(e.Body)), Request: req,
		}, nil
	}
	return nil, fmt.Errorf("no recorded response for %s %s", req.Method, client.Escape(req.URL.String()))
}

func (e Exchange) matches(req *http.Request) bool {
	u, err := url.Parse(e.URL)
	if err != nil || !strings.EqualFold(e.Method, req.Method) {
		return false
	}
	if !strings.EqualFold(u.Scheme, req.URL.Scheme) || !strings.EqualFold(u.Host, req.URL.Host) || slash(u.Path) != slash(req.URL.Path) {
		return false
	}
	want := flat(u.Query())
	for k, v := range e.Query {
		want[k] = v
	}
	got := flat(req.URL.Query())
	if len(want) != len(got) {
		return false
	}
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			return false
		}
	}
	return true
}

func slash(p string) string {
	if p == "" {
		return "/"
	}
	return p
}

func flat(v url.Values) map[string]string {
	out := make(map[string]string, len(v))
	for k, vs := range v {
		out[k] = strings.Join(vs, ",")
	}
	return out
}
