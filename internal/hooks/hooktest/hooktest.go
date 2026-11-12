// Package hooktest is the contract kit of the hook protocol: a test server that
// stands in for a hook (it records each request and answers from a script) and
// the validation of a request or a response against the published JSON Schemas
// in docs/schemas/hooks, one file per point. The points' own tests and the
// conformance test use it; a plugin author's kit can wrap it.
package hooktest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
)

// Recorded is one request a Server received.
type Recorded struct {
	Point   string // the Cartographer-Hook header
	Version string // the Cartographer-Hook-Version header
	Header  http.Header
	Body    []byte
}

// Fields decodes the body.
func (r Recorded) Fields() map[string]any {
	var m map[string]any
	_ = json.Unmarshal(r.Body, &m)
	return m
}

// Script answers a request: the status and the body (a string, []byte or any
// value encoded as JSON).
type Script func(Recorded) (status int, body any)

// Answer is a Script that always answers 200 with body.
func Answer(body string) Script { return func(Recorded) (int, any) { return http.StatusOK, body } }

// Server is an httptest server that serves one hook point.
type Server struct {
	*httptest.Server
	script Script

	mu        sync.Mutex
	requests  []Recorded
	responses [][]byte
}

// Cleaner is the part of testing.TB a Server needs.
type Cleaner interface{ Cleanup(func()) }

// New starts a Server answering with script; it is closed at the test's end.
func New(t Cleaner, script Script) *Server {
	s := &Server{script: script}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	rec := Recorded{Point: r.Header.Get("Cartographer-Hook"), Version: r.Header.Get("Cartographer-Hook-Version"), Header: r.Header.Clone(), Body: body}
	status, out := s.script(rec)
	var b []byte
	switch v := out.(type) {
	case string:
		b = []byte(v)
	case []byte:
		b = v
	default:
		b, _ = json.Marshal(v)
	}
	s.mu.Lock()
	s.requests = append(s.requests, rec)
	s.responses = append(s.responses, b)
	s.mu.Unlock()
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// Requests returns the requests received so far, in order.
func (s *Server) Requests() []Recorded {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Recorded(nil), s.requests...)
}

// Responses returns the bodies answered so far, in order.
func (s *Server) Responses() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.responses...)
}

// Validate checks body (a request or a response, kind "request" or
// "response") of point against <dir>/<point>.json, dir being the test's path to
// docs/schemas/hooks.
func Validate(dir, point, kind string, body []byte) error {
	raw, err := os.ReadFile(filepath.Join(dir, point+".json"))
	if err != nil {
		return err
	}
	var root jsonschema.Schema
	if err := json.Unmarshal(raw, &root); err != nil {
		return fmt.Errorf("%s.json: %w", point, err)
	}
	def, ok := root.Defs[kind]
	if !ok {
		return fmt.Errorf("%s.json has no $defs/%s", point, kind)
	}
	rs, err := def.Resolve(&jsonschema.ResolveOptions{})
	if err != nil {
		return fmt.Errorf("%s.json $defs/%s: %w", point, kind, err)
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return fmt.Errorf("%s %s: not JSON: %w", point, kind, err)
	}
	if err := rs.Validate(v); err != nil {
		return fmt.Errorf("%s %s: %w", point, kind, err)
	}
	return nil
}
