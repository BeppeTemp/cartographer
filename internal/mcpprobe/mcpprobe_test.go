package mcpprobe

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestClassifyRealErrors(t *testing.T) {
	tlsSrv := httptest.NewTLSServer(http.NotFoundHandler())
	defer tlsSrv.Close()

	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedAddr := closed.Addr().String()
	closed.Close()

	// A listener that never accepts: the TCP connect succeeds from the backlog and
	// the TLS handshake gets no answer.
	silent, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()

	// The test server's CA is not a system root.
	cases := []struct {
		name    string
		url     string
		timeout time.Duration
		want    Kind
	}{
		{"unknown authority", tlsSrv.URL, 5 * time.Second, KindTLSUnknownAuthority},
		{"refused", "http://" + closedAddr, 5 * time.Second, KindRefused},
		{"dns", "https://nohost.invalid/mcp", 5 * time.Second, KindDNS},
		{"timeout", "https://" + silent.Addr().String(), 100 * time.Millisecond, KindTimeout},
		{"plain http reachable", "http://" + silent.Addr().String(), 5 * time.Second, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Dial(context.Background(), c.url, c.timeout)
			if got := Classify(err); got != c.want {
				t.Fatalf("Classify(%v) = %q, want %q", err, got, c.want)
			}
		})
	}
	if Classify(nil) != "" {
		t.Error("nil error must classify as no kind")
	}
	if !strings.Contains(Hint(KindTLSUnknownAuthority), "NODE_EXTRA_CA_CERTS") {
		t.Error("the unknown-authority hint must name NODE_EXTRA_CA_CERTS")
	}
}

func TestUnresolved(t *testing.T) {
	env := map[string]string{"SET": "v", "EMPTY": ""}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	got := strings.Join(Unresolved([]string{"SET", "EMPTY", "MISSING"}, lookup), ",")
	if got != "EMPTY,MISSING" {
		t.Errorf("Unresolved = %q, want EMPTY,MISSING", got)
	}
}

func TestExitCode(t *testing.T) {
	cases := []struct {
		r    Result
		want int
	}{
		{Result{}, 0},
		{Result{Error: "initialize failed"}, 1},
		{Result{Unresolved: []string{"X"}}, 3},
		{Result{Unresolved: []string{"X"}, Net: KindTimeout}, 3},
		{Result{Net: KindDNS}, 4},
		{Result{Net: KindRefused}, 4},
		{Result{Net: KindTimeout}, 4},
		{Result{Net: KindOther}, 4},
		{Result{Net: KindTLSUnknownAuthority}, 5},
		{Result{Net: KindTLSOther}, 5},
	}
	for _, c := range cases {
		if got := ExitCode(c.r); got != c.want {
			t.Errorf("ExitCode(%+v) = %d, want %d", c.r, got, c.want)
		}
	}
}

func TestBlockedAll(t *testing.T) {
	dial := func(_ context.Context, u string, _ time.Duration) error {
		switch u {
		case "down":
			return context.DeadlineExceeded
		case "tls":
			return x509.UnknownAuthorityError{}
		}
		return nil
	}
	got := BlockedAll(context.Background(), []string{"up", "down", "tls"}, 0, dial)
	if got[0] || !got[1] || got[2] {
		t.Errorf("BlockedAll = %v, want [false true false] (a TLS fault is not the network)", got)
	}
}

// Text is never a kind: our own redirect refusal and any message that merely
// contains "refused" are not a connection nobody answered.
func TestClassifyIgnoresRefusedText(t *testing.T) {
	for _, err := range []error{
		ErrRedirectHost,
		fmt.Errorf("Get \"http://x\": %w", ErrRedirectHost),
		errors.New("login refused by the server"),
		errors.New("connection refused"),
	} {
		if got := Classify(err); got != KindOther {
			t.Errorf("Classify(%v) = %q, want %q", err, got, KindOther)
		}
	}
	if got := Classify(fmt.Errorf("dial: %w", syscall.ECONNREFUSED)); got != KindRefused {
		t.Errorf("a typed ECONNREFUSED = %q, want refused", got)
	}
	if got := Classify(fmt.Errorf("dial: %w", syscall.Errno(10061))); got != KindRefused {
		t.Errorf("WSAECONNREFUSED = %q, want refused", got)
	}
}
