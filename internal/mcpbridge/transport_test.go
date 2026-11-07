package mcpbridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The bridge's own list of revisions is a copy; this is what keeps it equal to
// the one the server serves.
func TestKnownProtocolsMatchTheServer(t *testing.T) {
	if got, want := knownProtocols, mcp.SupportedProtocolVersions(); !slices.Equal(got, want) {
		t.Fatalf("knownProtocols = %v, the SDK serves %v", got, want)
	}
}

func TestNegotiate(t *testing.T) {
	if got := negotiate("2025-03-26"); got != "2025-03-26" {
		t.Fatalf("a known version is echoed, got %q", got)
	}
	for _, v := range []string{"", "1999-01-01"} {
		if got := negotiate(v); got != knownProtocols[0] {
			t.Fatalf("negotiate(%q) = %q, want the newest known", v, got)
		}
	}
}

// strictServer negotiates "negotiated" whatever the client asks and rejects any
// later request whose MCP-Protocol-Version header is not that.
func strictServer(t *testing.T, negotiated string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var m message
		_ = json.Unmarshal(body, &m)
		mu.Lock()
		seen = append(seen, m.Method+"="+r.Header.Get("MCP-Protocol-Version"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cartographer-Access", "read")
		switch {
		case m.Method == "initialize":
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":`+string(m.ID)+`,"result":{"protocolVersion":"`+negotiated+`","capabilities":{},"serverInfo":{"name":"x","version":"1"}}}`)
		case len(m.ID) == 0:
			w.WriteHeader(http.StatusAccepted)
		case r.Header.Get("MCP-Protocol-Version") != negotiated:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, "Unsupported protocol version")
		default:
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":`+string(m.ID)+`,"result":{"tools":[]}}`)
		}
	}))
	t.Cleanup(ts.Close)
	return ts, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

// handshake runs a session the way a client does: each message is sent after
// the previous request was answered (the bridge dispatches concurrently, so a
// script written all at once would race the handshake). It returns the
// responses by id.
func handshake(t *testing.T, b *Bridge, script ...string) map[string]map[string]any {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	b.In, b.Out, b.Log = inR, outW, io.Discard
	done := make(chan error, 1)
	go func() { done <- b.Run(context.Background()); outW.Close() }()
	sc := bufio.NewScanner(outR)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	got := map[string]map[string]any{}
	for _, msg := range script {
		_, _ = io.WriteString(inW, msg+"\n")
		var m message
		_ = json.Unmarshal([]byte(msg), &m)
		if !m.isRequest() {
			continue
		}
		for {
			if !sc.Scan() {
				t.Fatalf("stdout closed before the answer to %s: %v", msg, sc.Err())
			}
			var r map[string]any
			if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
				t.Fatal(err)
			}
			if r["method"] == "roots/list" { // the client has no roots to offer
				_, _ = io.WriteString(inW, `{"jsonrpc":"2.0","id":`+string(mustJSON(r["id"]))+`,"result":{"roots":[]}}`+"\n")
				continue
			}
			got[string(mustJSON(r["id"]))] = r
			break
		}
	}
	inW.Close()
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
	return got
}

// The server negotiates a version other than the one asked for; the bridge
// must send the server's from then on (it sent the client's, and every request
// after the handshake got a 400).
func TestBridgeSendsTheNegotiatedProtocolVersion(t *testing.T) {
	ts, seen := strictServer(t, "2025-03-26")
	rs := &routedServer{Server: ts}
	cfg := bridgeConfig(rs)
	ws := mkdir(t, t.TempDir())
	bind(t, cfg, ws, "kb-a")
	b := &Bridge{Cfg: cfg, Provider: "claude", Dir: ws, Token: testToken}
	got := handshake(t, b, initMsg,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if got["2"]["error"] != nil || got["2"]["result"] == nil {
		t.Fatalf("tools/list after the handshake: %v (requests %v)", got["2"], seen())
	}
}

// The locally answered handshake (roots case) answers a version it knows, and
// the requests after it carry that one.
func TestBridgeRootsHandshakeAnswersAKnownVersion(t *testing.T) {
	known := knownProtocols[0]
	ts, seen := strictServer(t, known)
	rs := &routedServer{Server: ts}
	cfg := bridgeConfig(rs)
	ws := mkdir(t, t.TempDir())
	bind(t, cfg, ws, "kb-a")
	// Bound folder: no roots wait, so the handshake is forwarded; use an
	// unbound one with roots to take the local answer.
	b := &Bridge{Cfg: cfg, Provider: "claude", Dir: t.TempDir(), Token: testToken}
	got := handshake(t, b,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01","capabilities":{"roots":{}},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	res, _ := got["1"]["result"].(map[string]any)
	if res["protocolVersion"] != known {
		t.Fatalf("answered %v, want %s (unknown version asked)", res["protocolVersion"], known)
	}
	if s := seen(); len(s) > 0 && !strings.HasSuffix(s[len(s)-1], "="+known) {
		t.Fatalf("a later request carried %v, want %s", s, known)
	}
}

// A pipe that feeds more than maxLine bytes without a newline is read no
// further than the bound, and Run returns the error, also during the roots wait.
func TestBridgeBoundsAnUnterminatedLine(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prefix string
	}{
		{"plain", ""},
		{"during roots wait", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{"roots":{}},"clientInfo":{"name":"t","version":"1"}}}` + "\n" +
			`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rs := newRoutedServer(t, "kb-a")
			pr, pw := io.Pipe()
			var wrote int64
			wdone := make(chan struct{})
			go func() {
				defer close(wdone)
				defer pw.Close()
				_, _ = io.WriteString(pw, tc.prefix)
				chunk := bytes.Repeat([]byte("a"), 1<<20)
				for i := 0; i < 64; i++ { // 64 MiB offered, 16 MiB allowed
					n, err := pw.Write(chunk)
					wrote += int64(n)
					if err != nil {
						return
					}
				}
			}()
			var out bytes.Buffer
			b := &Bridge{Cfg: bridgeConfig(rs), Provider: "claude", Dir: t.TempDir(), Token: testToken, In: pr, Out: &out, Log: io.Discard}
			err := b.Run(context.Background())
			if err == nil || !strings.Contains(err.Error(), "message too large") {
				t.Fatalf("Run = %v, want message too large", err)
			}
			pr.Close()
			<-wdone
			if wrote > 2*maxLine {
				t.Fatalf("the peer wrote %d bytes before the reader stopped, bound %d", wrote, maxLine)
			}
		})
	}
}

// After Run returns, the stdin reader does not stay blocked sending a line.
func TestBridgeReaderGoroutineEndsWithRun(t *testing.T) {
	rs := newRoutedServer(t, "kb-a")
	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Many lines and nobody to take them: with an unconditional send the reader
	// would block forever.
	in := strings.NewReader(strings.Repeat(`{"jsonrpc":"2.0","method":"notifications/x"}`+"\n", 10))
	b := &Bridge{Cfg: bridgeConfig(rs), Provider: "claude", Dir: t.TempDir(), Token: testToken, In: in, Out: io.Discard, Log: io.Discard}
	if err := b.Run(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before {
		t.Fatalf("goroutines: %d before, %d after Run", before, n)
	}
}

// The default client has no overall timeout; the session context bounds a call.
func TestBridgeDefaultClientHasNoTimeoutAndContextAbortsACall(t *testing.T) {
	hang := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-hang:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(hang); ts.Close() })
	rs := &routedServer{Server: ts}
	cfg := bridgeConfig(rs)
	ws := mkdir(t, t.TempDir())
	bind(t, cfg, ws, "kb-a")
	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	b := &Bridge{Cfg: cfg, Provider: "claude", Dir: ws, Token: testToken, In: pr, Out: io.Discard, Log: io.Discard}
	done := make(chan error, 1)
	go func() { done <- b.Run(ctx) }()
	_, _ = io.WriteString(pw, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`+"\n")
	time.Sleep(200 * time.Millisecond)
	b.mu.Lock()
	c := b.HTTP
	b.mu.Unlock()
	if c == nil || c.Timeout != 0 {
		t.Fatalf("default client = %+v, want no Timeout", c)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling the session did not abort the pending call")
	}
	pw.Close()
}

type failingReader struct {
	data string
	done bool
}

func (f *failingReader) Read(p []byte) (int, error) {
	if f.done {
		return 0, errors.New("connection reset")
	}
	f.done = true
	return copy(p, f.data), nil
}

// A stream that breaks after a partial event gives the request an error, not
// silence.
func TestRelayEventsBrokenStreamAnswersTheRequest(t *testing.T) {
	var out bytes.Buffer
	b := &Bridge{Out: &out, Log: io.Discard}
	b.relayEvents(&failingReader{data: "data: {\"jsonrpc\":\"2.0\",\"id\":7,\"resu"}, nil, message{ID: json.RawMessage("7"), Method: "tools/call"})
	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &m); err != nil {
		t.Fatalf("output %q: %v", out.String(), err)
	}
	e, _ := m["error"].(map[string]any)
	if string(mustJSON(m["id"])) != "7" || e == nil || e["code"] != float64(-32000) {
		t.Fatalf("got %v, want a -32000 error for id 7", m)
	}
}

// A notification relayed before the break does not answer the request: it still
// gets its error, and the truncated event is not forwarded.
func TestRelayEventsNotificationDoesNotAnswerTheRequest(t *testing.T) {
	for name, r := range map[string]io.Reader{
		"broken": io.MultiReader(strings.NewReader("data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\ndata: {\"jsonrpc\":\"2.0\",\"id\":7,\"resu"), &failingReader{done: true}),
		"eof":    strings.NewReader("data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\ndata: {\"jsonrpc\":\"2.0\",\"id\":7,\"resu"),
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			b := &Bridge{Out: &out, Log: io.Discard}
			b.relayEvents(r, nil, message{ID: json.RawMessage("7"), Method: "tools/call"})
			lines := strings.Split(strings.TrimSpace(out.String()), "\n")
			if len(lines) != 2 || strings.Contains(out.String(), "resu\"") {
				t.Fatalf("output %q, want the notification then one error", out.String())
			}
			var m map[string]any
			_ = json.Unmarshal([]byte(lines[1]), &m)
			if e, _ := m["error"].(map[string]any); e == nil || string(mustJSON(m["id"])) != "7" {
				t.Fatalf("last line %v, want a -32000 error for id 7", m)
			}
		})
	}
}

// Once stdin closes, a call the server never answers is aborted after the
// grace instead of keeping the process alive.
func TestBridgeEOFAbortsAPendingCall(t *testing.T) {
	old := eofGrace
	eofGrace = 200 * time.Millisecond
	t.Cleanup(func() { eofGrace = old })
	hang := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-hang:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(hang); ts.Close() })
	cfg := bridgeConfig(&routedServer{Server: ts})
	ws := mkdir(t, t.TempDir())
	bind(t, cfg, ws, "kb-a")
	b := &Bridge{Cfg: cfg, Provider: "claude", Dir: ws, Token: testToken,
		In: strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"), Out: io.Discard, Log: io.Discard}
	done := make(chan error, 1)
	go func() { done <- b.Run(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after stdin EOF with a pending call")
	}
}
