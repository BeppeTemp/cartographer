package mcpserver

import (
	"bytes"
	"io"
	"strings"
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
	pr, w, done := guardStdio(strings.NewReader(input), io.Discard)
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
	pr, w, done := guardStdio(strings.NewReader("garbage\n"+`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"), &out)
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
	pr, _, done := guardStdio(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"), io.Discard)
	pr.Close() // nobody reads the line: the goroutine is blocked handing it over
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("guard goroutine leaked after the reader was closed")
	}
}
