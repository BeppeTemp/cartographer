package worker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// provider adapts one wire format to the shared contract test.
type provider struct {
	name  string
	build func(url string, opts ...Option) (Model, error)
	// reply renders a fake server answer of the given kind: text, tool, trunc.
	reply func(kind string) string
	// keyHeader is the header the key travels in.
	keyHeader string
}

func providers() []provider {
	return []provider{
		{
			name: "anthropic", keyHeader: "x-api-key",
			build: func(url string, opts ...Option) (Model, error) {
				return NewAnthropic(url, canary, "m", opts...)
			},
			reply: func(kind string) string {
				switch kind {
				case "tool":
					return `{"content":[{"type":"text","text":"let me look"},{"type":"tool_use","id":"t1","name":"search","input":{"q":"x"}}],"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":5}}`
				case "trunc":
					return `{"content":[{"type":"text","text":"half"}],"stop_reason":"max_tokens","usage":{"input_tokens":10,"output_tokens":5}}`
				}
				return `{"content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`
			},
		},
		{
			name: "openai", keyHeader: "Authorization",
			build: func(url string, opts ...Option) (Model, error) {
				return NewOpenAI(url, canary, "m", opts...)
			},
			reply: func(kind string) string {
				switch kind {
				case "tool":
					return `{"choices":[{"message":{"content":"let me look","tool_calls":[{"id":"t1","type":"function","function":{"name":"search","arguments":"{\"q\":\"x\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`
				case "trunc":
					return `{"choices":[{"message":{"content":"half"},"finish_reason":"length"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`
				}
				return `{"choices":[{"message":{"content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`
			},
		},
	}
}

func serve(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestContract_Answers(t *testing.T) {
	for _, p := range providers() {
		t.Run(p.name, func(t *testing.T) {
			cases := []struct {
				kind string
				stop StopReason
				text string
				call bool
			}{
				{"text", StopEnd, "hello", false},
				{"tool", StopToolUse, "let me look", true},
				{"trunc", StopMaxTokens, "half", false},
			}
			for _, c := range cases {
				srv := serve(t, 200, p.reply(c.kind))
				m, err := p.build(srv.URL, WithRetryDelay(time.Millisecond))
				if err != nil {
					t.Fatal(err)
				}
				resp, err := m.Complete(context.Background(), Request{Messages: []Message{{Role: RoleUser, Text: "hi"}}})
				srv.Close()
				if err != nil {
					t.Fatalf("%s: %v", c.kind, err)
				}
				if resp.Stop != c.stop || resp.Text != c.text || (len(resp.ToolCalls) == 1) != c.call {
					t.Errorf("%s: %+v", c.kind, resp)
				}
				if resp.Usage.Total() != 15 || resp.Usage.Estimated {
					t.Errorf("%s usage = %+v", c.kind, resp.Usage)
				}
				if c.call {
					tc := resp.ToolCalls[0]
					if tc.ID != "t1" || tc.Name != "search" || string(tc.Arguments) != `{"q":"x"}` {
						t.Errorf("tool call = %+v", tc)
					}
				}
			}
		})
	}
}

func TestContract_RetryAndErrors(t *testing.T) {
	for _, p := range providers() {
		t.Run(p.name, func(t *testing.T) {
			ctx := context.Background()
			req := Request{Messages: []Message{{Role: RoleUser, Text: "hi"}}}

			// 503, 429 then success.
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				switch calls {
				case 1:
					w.WriteHeader(503)
				case 2:
					w.WriteHeader(429)
				default:
					_, _ = w.Write([]byte(p.reply("text")))
				}
			}))
			m, _ := p.build(srv.URL, WithRetryDelay(time.Millisecond))
			if _, err := m.Complete(ctx, req); err != nil || calls != 3 {
				t.Fatalf("retry then success: err=%v calls=%d", err, calls)
			}
			srv.Close()

			// Exhausted: 3 attempts, then the error, with the body snippet.
			calls = 0
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(500)
				_, _ = w.Write([]byte("overloaded " + canary))
			}))
			m, _ = p.build(srv.URL, WithRetryDelay(time.Millisecond))
			_, err := m.Complete(ctx, req)
			if err == nil || calls != 3 {
				t.Fatalf("exhausted: err=%v calls=%d", err, calls)
			}
			if containsCanary(err.Error()) {
				t.Errorf("key leaked in error: %v", err)
			}
			srv.Close()

			// 400 is final, one call; the body is capped at 512 bytes.
			calls = 0
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(400)
				_, _ = w.Write(make([]byte, 5000))
			}))
			m, _ = p.build(srv.URL, WithRetryDelay(time.Millisecond))
			_, err = m.Complete(ctx, req)
			if err == nil || calls != 1 || len(err.Error()) > 700 {
				t.Fatalf("400: err len=%d calls=%d", len(err.Error()), calls)
			}
			srv.Close()

			// 401 and 403 are ErrModelAuth.
			for _, st := range []int{401, 403} {
				srv = serve(t, st, "bad key")
				m, _ = p.build(srv.URL)
				_, err = m.Complete(ctx, req)
				srv.Close()
				if !isAuth(err) {
					t.Errorf("status %d: err = %v", st, err)
				}
			}

			// A 2xx body that is not JSON is an error.
			srv = serve(t, 200, "<html>")
			m, _ = p.build(srv.URL)
			if _, err = m.Complete(ctx, req); err == nil {
				t.Error("non-JSON 2xx accepted")
			}
			srv.Close()

			// Context cancelled mid-backoff returns promptly.
			srv = serve(t, 503, "")
			m, _ = p.build(srv.URL, WithRetryDelay(time.Minute))
			cctx, cancel := context.WithCancel(ctx)
			go func() { time.Sleep(50 * time.Millisecond); cancel() }()
			start := time.Now()
			_, err = m.Complete(cctx, req)
			srv.Close()
			if err == nil || time.Since(start) > 5*time.Second {
				t.Errorf("cancel: err=%v after %v", err, time.Since(start))
			}
		})
	}
}

func TestContract_RedactionAndRedirect(t *testing.T) {
	for _, p := range providers() {
		t.Run(p.name, func(t *testing.T) {
			m, err := p.build("http://127.0.0.1:1")
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range []string{sprint("%v", m), sprint("%+v", m), sprint("%#v", m), sprint("%s", m)} {
				if containsCanary(s) {
					t.Errorf("key printed: %s", s)
				}
			}
			// A connection error text never carries the key either.
			_, err = m.Complete(context.Background(), Request{})
			if err != nil && containsCanary(err.Error()) {
				t.Errorf("key in error: %v", err)
			}

			// Non-loopback http:// is refused unless opted in.
			if _, err := p.build("http://example.com/v1"); err == nil {
				t.Error("plain http to a non-loopback host accepted")
			}
			if _, err := p.build("http://example.com/v1", WithAllowInsecureHTTP(true)); err != nil {
				t.Errorf("opt-in refused: %v", err)
			}

			// A redirect to another host does not carry the key.
			var leaked string
			other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				leaked = r.Header.Get("x-api-key") + r.Header.Get("Authorization")
				_, _ = w.Write([]byte(p.reply("text")))
			}))
			defer other.Close()
			redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, other.URL+r.URL.Path, http.StatusTemporaryRedirect)
			}))
			defer redir.Close()
			m, _ = p.build(redir.URL)
			if _, err := m.Complete(context.Background(), Request{Messages: []Message{{Role: RoleUser, Text: "hi"}}}); err != nil {
				t.Fatalf("redirected call: %v", err)
			}
			if leaked != "" {
				t.Errorf("credential followed the redirect: %q", leaked)
			}
		})
	}
}

func decode(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	return v
}
