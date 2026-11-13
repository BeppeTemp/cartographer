package mcpexec

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func bridgeLaunch(t *testing.T, url string) *Launch {
	t.Helper()
	desc := fmt.Sprintf(`{"type":"http","url":%q,"headers":{"Authorization":"Bearer ${secret:services/svc#TOKEN}","X-Plain":"${HOST_VAR}"}}`, url)
	l, err := Resolve(context.Background(), newDeps(desc), "kb-a/svc")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

type seen struct {
	method, session, proto, auth, plain, accept, body string
}

type rec struct {
	mu   sync.Mutex
	reqs []seen
}

func (r *rec) add(q *http.Request) seen {
	b, _ := io.ReadAll(q.Body)
	s := seen{q.Method, q.Header.Get("Mcp-Session-Id"), q.Header.Get("MCP-Protocol-Version"),
		q.Header.Get("Authorization"), q.Header.Get("X-Plain"), q.Header.Get("Accept"), string(b)}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, s)
	return s
}

func runBridge(t *testing.T, l *Launch, in string) (string, string, error) {
	t.Helper()
	var out, errw bytes.Buffer
	err := l.Bridge(context.Background(), strings.NewReader(in), &out, &errw)
	return out.String(), errw.String(), err
}

func TestBridgeSessionHeadersSSEAndDelete(t *testing.T) {
	r := &rec{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		s := r.add(q)
		switch {
		case q.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case strings.Contains(s.body, `"initialize"`):
			w.Header().Set("Mcp-Session-Id", "sess-1")
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`)
		case strings.Contains(s.body, "notifications/"):
			w.WriteHeader(http.StatusAccepted)
		default:
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\ndata: {\"jsonrpc\":\"2.0\",\"id\":2,\ndata: \"result\":{}}\n\n")
		}
	}))
	defer srv.Close()
	in := `{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"
	out, errw, err := runBridge(t, bridgeLaunch(t, srv.URL), in)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], "2025-06-18") || !strings.Contains(lines[1], "progress") || !strings.Contains(lines[2], `"id":2`) {
		t.Fatalf("output %q", out)
	}
	if len(r.reqs) != 4 {
		t.Fatalf("%d requests: %+v", len(r.reqs), r.reqs)
	}
	for i, q := range r.reqs {
		if q.auth != "Bearer "+planted || q.plain != "hv" {
			t.Fatalf("request %d headers: %+v", i, q)
		}
		if i > 0 && q.session != "sess-1" {
			t.Fatalf("request %d lost the session id", i)
		}
		if i > 1 && q.proto != "2025-06-18" {
			t.Fatalf("request %d lost the protocol version", i)
		}
		if i < 3 && !strings.Contains(q.accept, "text/event-stream") {
			t.Fatalf("request %d Accept %q", i, q.accept)
		}
	}
	if r.reqs[3].method != http.MethodDelete {
		t.Fatalf("last request %s, want DELETE on EOF", r.reqs[3].method)
	}
	if strings.Contains(out+errw, planted) {
		t.Fatal("the secret reached out or stderr")
	}
}

func TestBridgeFailuresAreJSONRPCErrorsWithoutTheSecret(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"401": func(w http.ResponseWriter, q *http.Request) {
			w.WriteHeader(401)
			io.WriteString(w, "bad token "+planted)
		},
		"500": func(w http.ResponseWriter, q *http.Request) {
			w.WriteHeader(500)
			io.WriteString(w, "boom "+planted)
		},
		"redirect": func(w http.ResponseWriter, q *http.Request) {
			http.Redirect(w, q, "http://127.0.0.1:1/"+planted, 302)
		},
		"empty": func(w http.ResponseWriter, q *http.Request) { w.WriteHeader(200) },
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(h)
			defer srv.Close()
			out, errw, err := runBridge(t, bridgeLaunch(t, srv.URL), `{"jsonrpc":"2.0","id":7,"method":"tools/list"}`+"\n")
			if err != nil || !strings.Contains(out, `"id":7`) || !strings.Contains(out, `"code":-32000`) {
				t.Fatalf("err %v, out %q", err, out)
			}
			if strings.Contains(out+errw, planted) || strings.Contains(out+errw, "Bearer") {
				t.Fatalf("a secret leaked: %q %q", out, errw)
			}
			if name == "401" && !strings.Contains(out, "Authorization") {
				t.Fatalf("401 does not name the header: %q", out)
			}
		})
	}
}

func TestBridgeTransportErrorHasNoSecretAndNotificationsStaySilent(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // connection refused
	l := bridgeLaunch(t, url)
	l.secrets = append(l.secrets, "127.0.0.1") // a value inside the transport error must be scrubbed too
	out, errw, err := runBridge(t, l, `{"jsonrpc":"2.0","id":3,"method":"ping"}`+"\n"+`{"jsonrpc":"2.0","method":"notifications/x"}`+"\n")
	if err != nil || strings.Count(out, "\n") != 1 || !strings.Contains(out, `"id":3`) {
		t.Fatalf("err %v, out %q", err, out)
	}
	if strings.Contains(out+errw, "127.0.0.1") || strings.Contains(out+errw, planted) {
		t.Fatalf("leak: %q %q", out, errw)
	}
}

func TestBridgeRefusesPlainHTTPToARemoteHostAndStdio(t *testing.T) {
	l := bridgeLaunch(t, "https://example.com/mcp")
	l.URL = "http://example.com/mcp"
	if _, _, err := runBridge(t, l, ""); err == nil || strings.Contains(err.Error(), planted) {
		t.Fatalf("err %v", err)
	}
	l.Spec.Type = "stdio"
	if _, _, err := runBridge(t, l, ""); err == nil {
		t.Fatal("a stdio descriptor was bridged")
	}
}
