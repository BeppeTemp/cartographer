package hooktest

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/audit"
	"github.com/BeppeTemp/cartographer/internal/hooks"
)

var schemaDir = filepath.Join("..", "..", "..", "docs", "schemas", "hooks")

var points = []string{hooks.PointWriteVeto, hooks.PointAuditSink, hooks.PointSecretResolver, hooks.PointIdentity}

func TestSchemasResolve(t *testing.T) {
	t.Parallel()
	for _, p := range points {
		for _, kind := range []string{"request", "response"} {
			// A body that is not even an object proves the schema resolved and judged it.
			if err := Validate(schemaDir, p, kind, []byte(`"x"`)); err == nil || strings.Contains(err.Error(), "no $defs") || strings.Contains(err.Error(), "json:") {
				t.Errorf("%s %s: %v", p, kind, err)
			}
		}
	}
}

var exampleRe = regexp.MustCompile("(?s)<!-- hook-example: ([a-z_]+) (request|response) -->\\s*```json\\n(.*?)```")

// Every example body of docs/hooks.md is valid against its schema, and every
// point has a request and a response example.
func TestDocsHookExamplesValidate(t *testing.T) {
	t.Parallel()
	page, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "hooks.md"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, m := range exampleRe.FindAllStringSubmatch(string(page), -1) {
		if err := Validate(schemaDir, m[1], m[2], []byte(m[3])); err != nil {
			t.Errorf("docs/hooks.md example %s %s: %v", m[1], m[2], err)
		}
		seen[m[1]+" "+m[2]] = true
	}
	for _, p := range points {
		for _, kind := range []string{"request", "response"} {
			if !seen[p+" "+kind] {
				t.Errorf("docs/hooks.md has no %s %s example", p, kind)
			}
		}
	}
}

func registry(t *testing.T, h hooks.Hook) *hooks.Registry {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "acme.token"), []byte("tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.Plugin, h.Credential, h.FailurePolicy = "acme", "acme.token", hooks.FailClosed
	r, err := hooks.NewRegistry([]hooks.Hook{h}, hooks.Options{CredentialsDir: dir, Approved: func(string, string) bool { return true }, Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The requests the audit sink and the secret resolver really send, and the
// answers they accept, are valid against the published schemas.
func TestRecordedRequestsValidate(t *testing.T) {
	t.Parallel()
	sink := New(t, func(r Recorded) (int, any) {
		return http.StatusOK, map[string]any{"version": 1, "ack": r.Fields()["last_hash"]}
	})
	dir := t.TempDir()
	l, err := audit.OpenWithOptions(filepath.Join(dir, "audit.jsonl"), audit.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if _, err := l.AppendEvent(audit.Entry{RequestID: "r", Phase: audit.PhaseAttempt, Tool: "concept_read", Resources: map[string]string{"id": "notes/a"}}); err != nil {
		t.Fatal(err)
	}
	src := func(cursor string, limit int) ([]hooks.SinkEvent, bool, error) {
		es, gap, err := l.EventsAfter(cursor, limit)
		out := make([]hooks.SinkEvent, len(es))
		for i, e := range es {
			b, _ := json.Marshal(e)
			out[i] = hooks.SinkEvent{Hash: e.Hash, Entry: b}
		}
		return out, gap, err
	}
	sk, err := registry(t, hooks.Hook{Name: "acme/siem", Point: hooks.PointAuditSink, URL: sink.URL}).NewSink("acme/siem", src, nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	last, _ := l.TailFunc(1, nil)
	if err := sk.Await(context.Background(), last[0].Hash); err != nil {
		t.Fatal(err)
	}
	vault := New(t, Answer(`{"version":1,"value":"v","ttl_seconds":30}`))
	reg := registry(t, hooks.Hook{Name: "acme/vault", Point: hooks.PointSecretResolver, URL: vault.URL})
	if _, err := reg.ResolveSecrets(context.Background(), hooks.SecretQuery{KB: "kb-a", ConceptID: "ops/svc", Principal: map[string]any{"id": "user", "kind": "human", "via": "", "groups": nil}},
		[]hooks.SecretRef{{Name: "API", Hook: "acme/vault", Key: "prod/api"}}); err != nil {
		t.Fatal(err)
	}
	for point, s := range map[string]*Server{hooks.PointAuditSink: sink, hooks.PointSecretResolver: vault} {
		reqs, resps := s.Requests(), s.Responses()
		if len(reqs) == 0 {
			t.Fatalf("%s: no request recorded", point)
		}
		for i, r := range reqs {
			if r.Point != point || r.Version != "1" {
				t.Errorf("%s: headers %q %q", point, r.Point, r.Version)
			}
			if err := Validate(schemaDir, point, "request", r.Body); err != nil {
				t.Error(err)
			}
			if err := Validate(schemaDir, point, "response", resps[i]); err != nil {
				t.Error(err)
			}
		}
	}
}
