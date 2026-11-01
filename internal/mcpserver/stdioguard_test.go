package mcpserver

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// A bad line never ends a stdio session (#1298): it is answered with the error
// HTTP gives it and the next line is read.
func TestStdioBadLinesAnswerErrorsAndSessionStaysOpen(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("0.1.0-m1")
	RegisterKBTools(s, k, Deps{})

	resps := runStdioSession(t, s.Run, []string{
		`garbage`,
		`[]`,
		`{"jsonrpc":"1.0","id":7,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":{"a":1},"method":"ping"}`,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}`,
	})
	if len(resps) != 5 {
		t.Fatalf("want 5 responses, got %d: %+v", len(resps), resps)
	}
	want := []struct {
		code int
		id   string
	}{{ErrCodeParseError, "null"}, {ErrCodeInvalidRequest, "null"}, {ErrCodeInvalidRequest, "7"}, {ErrCodeInvalidRequest, "null"}}
	for i, w := range want {
		e := resps[i].Error
		if e == nil || e.Code != w.code {
			t.Errorf("response %d: want code %d, got %+v", i, w.code, resps[i])
		}
		if got := string(resps[i].ID); got != w.id {
			t.Errorf("response %d: id %s, want %s", i, got, w.id)
		}
	}
	if resps[4].Error != nil || resps[4].Result == nil {
		t.Fatalf("initialize after bad lines: %+v", resps[4])
	}
}

func guardRun(t *testing.T, input string) (string, string) {
	t.Helper()
	pr, w, done := guardStdioDrain(strings.NewReader(input), io.Discard, 0)
	_ = w
	got, err := io.ReadAll(pr)
	if err != nil {
		t.Fatal(err)
	}
	pr.Close()
	<-done
	return string(got), ""
}

func TestGuardStdioPassesValidLinesUnchanged(t *testing.T) {
	t.Parallel()
	big := `{"jsonrpc":"2.0","id":9,"method":"ping","params":{"x":"` + strings.Repeat("a", 1<<20) + `"}}`
	lines := []string{
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`[{"jsonrpc":"2.0","id":2,"method":"ping"},{"jsonrpc":"2.0","id":3,"method":"ping"}]`,
		big,
	}
	got, _ := guardRun(t, strings.Join(lines, "\n")+"\n")
	if want := strings.Join(lines, "\n") + "\n"; got != want {
		t.Fatalf("lines changed: got %d bytes, want %d", len(got), len(want))
	}
}

func TestGuardStdioErrorLineThenNormalResponseBothArrive(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	pr, w, done := guardStdioDrain(strings.NewReader("garbage\n"+`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"), &out, 0)
	got, _ := io.ReadAll(pr)
	<-done
	if !strings.Contains(string(got), `"method":"ping"`) {
		t.Fatalf("valid line not forwarded: %q", got)
	}
	if _, err := w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}` + "\n")); err != nil {
		t.Fatal(err)
	}
	o := out.String()
	if !strings.Contains(o, `"code":-32700`) || !strings.Contains(o, `"result":{}`) {
		t.Fatalf("want both the error and the response on stdout, got %q", o)
	}
}

func TestGuardStdioGoroutineEndsWhenReaderIsClosed(t *testing.T) {
	t.Parallel()
	pr, _, done := guardStdioDrain(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"), io.Discard, 0)
	pr.Close() // nobody reads the line: the goroutine is blocked handing it over
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("guard goroutine leaked after the reader was closed")
	}
}

// stdinEOFRequests is what a piped smoke test sends: the handshake and one
// listing, then EOF with nothing held open (#1448).
const stdinEOFRequests = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"pipe","version":"0"}}}` + "\n" +
	`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
	`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"

// EOF on stdin right after the requests answers all of them, then the session
// ends (#1448). The SDK cancels every in-flight request when its reader ends,
// so the guard must hold the EOF back until the answers are written.
func TestStdioEOFAfterRequestsStillAnswersThemAll(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("0.1.0-m1")
	RegisterKBTools(s, k, Deps{})

	var out syncBuffer
	done := make(chan error, 1)
	go func() { done <- s.Run(strings.NewReader(stdinEOFRequests), &out) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("session did not end after EOF")
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || lines[0] == "" {
		t.Fatalf("want 2 responses (initialize, tools/list), got %d: %q", len(lines), out.String())
	}
	if !strings.Contains(lines[0], `"id":1`) || !strings.Contains(lines[1], `"id":2`) || !strings.Contains(lines[1], `"tools"`) {
		t.Fatalf("unexpected responses: %q", out.String())
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// A request nobody answers holds EOF back for the drain bound, no longer: a
// hung handler cannot keep the process alive for ever (#1448).
func TestGuardStdioDrainIsBounded(t *testing.T) {
	t.Parallel()
	pr, _, done := guardStdioDrain(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"), io.Discard, 200*time.Millisecond)
	start := time.Now()
	if _, err := io.ReadAll(pr); err != nil {
		t.Fatal(err)
	}
	<-done
	if d := time.Since(start); d < 150*time.Millisecond || d > 10*time.Second {
		t.Fatalf("EOF reached the SDK after %v, want about the 200ms bound", d)
	}
}

// EOF is held back until the answer is written, and a cancelled request counts
// as answered: the SDK never responds to it.
func TestGuardStdioHoldsEOFUntilAnswered(t *testing.T) {
	t.Parallel()
	in := `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n" +
		`{"jsonrpc":"2.0","id":"a","method":"ping"}` + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"a"}}` + "\n"
	pr, w, done := guardStdioDrain(strings.NewReader(in), io.Discard, time.Minute)
	finished := make(chan struct{})
	go func() { _, _ = io.ReadAll(pr); close(finished) }()
	select {
	case <-finished:
		t.Fatal("EOF delivered while request 1 is unanswered")
	case <-time.After(200 * time.Millisecond):
	}
	if _, err := w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}` + "\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("EOF still held after the last answer")
	}
	<-done
}

// Ids are matched as the SDK re-encodes them, not as the client wrote them:
// numbers by int64 truncation, strings by decoded value, the two kinds apart.
func TestPendingIDsKeyIdsAsTheSDKDoes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, request, answer string
		retired               bool
	}{
		{"integer", `1`, `1`, true},
		{"1.0 answered as 1", `1.0`, `1`, true},
		{"1e0 answered as 1", `1e0`, `1`, true},
		{"1.5 answered as 1", `1.5`, `1`, true},
		{"string with escape", `"ab"`, `"ab"`, true},
		{"number is not string", `1`, `"1"`, false},
		{"string is not number", `"1"`, `1`, false},
	}
	for _, c := range cases {
		for _, via := range []string{"response", "cancel"} {
			t.Run(c.name+"/"+via, func(t *testing.T) {
				t.Parallel()
				p := newPendingIDs()
				p.started([]byte(`{"jsonrpc":"2.0","id":` + c.request + `,"method":"ping"}`))
				if via == "response" {
					p.answered([]byte(`{"jsonrpc":"2.0","id":` + c.answer + `,"result":{}}`))
				} else {
					p.started([]byte(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":` + c.answer + `}}`))
				}
				if got := p.empty(); got != c.retired {
					t.Fatalf("request %s, %s %s: retired=%v, want %v", c.request, via, c.answer, got, c.retired)
				}
			})
		}
	}
}
