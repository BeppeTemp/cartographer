package mcpprobe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMain re-executes the test binary as the stdio server under test, the
// pattern internal/surveyor/connector uses: it needs no interpreter. With
// PROBE_HELPER=die it writes to stderr and exits at once.
func TestMain(m *testing.M) {
	switch os.Getenv("PROBE_HELPER") {
	case "serve":
		if err := fakeServer().Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	case "spawn":
		// A server that starts a grandchild (npx starts node) and never answers.
		gc := exec.Command(os.Args[0])
		gc.Env = append(os.Environ(), "PROBE_HELPER=sleep")
		if err := gc.Start(); err != nil {
			os.Exit(1)
		}
		_ = os.WriteFile(os.Getenv("PROBE_PIDFILE"), []byte(strconv.Itoa(gc.Process.Pid)), 0o600)
		time.Sleep(5 * time.Minute)
		os.Exit(0)
	case "sleep":
		time.Sleep(5 * time.Minute)
		os.Exit(0)
	case "die":
		os.Stderr.WriteString("starting\nboom: token=sekrit-value rejected\n")
		os.Exit(3)
	}
	os.Exit(m.Run())
}

type noArgs struct{}

func fakeServer() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "fake", Version: "9"}, nil)
	for _, n := range []string{"alpha", "beta"} {
		mcp.AddTool(srv, &mcp.Tool{Name: n, Description: n}, func(context.Context, *mcp.CallToolRequest, noArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{}, nil, nil
		})
	}
	return srv
}

func TestRunStdioCountsTools(t *testing.T) {
	exe, _ := os.Executable()
	info, err := Run(context.Background(), Target{Command: exe, Env: append(os.Environ(), "PROBE_HELPER=serve")}, 20*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if info.Server != "fake" || strings.Join(info.Tools, ",") != "alpha,beta" || info.Protocol == "" {
		t.Errorf("info = %+v", info)
	}
}

func TestRunStdioChildThatExitsCarriesAScrubbedStderrTail(t *testing.T) {
	exe, _ := os.Executable()
	scrub := func(s string) string { return strings.ReplaceAll(s, "sekrit-value", "<redacted>") }
	_, err := Run(context.Background(), Target{Command: exe, Env: append(os.Environ(), "PROBE_HELPER=die")}, 20*time.Second, scrub)
	if err == nil {
		t.Fatal("a child that exits before answering must fail")
	}
	if !strings.Contains(err.Error(), "boom: token=<redacted> rejected") || strings.Contains(err.Error(), "sekrit-value") {
		t.Errorf("error = %q", err)
	}
}

func TestRunHTTPSendsHeadersAndCountsTools(t *testing.T) {
	var got string
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return fakeServer() }, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()
	info, err := Run(context.Background(), Target{URL: srv.URL, Headers: map[string]string{"Authorization": "Bearer abc"}}, 20*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Tools) != 2 || got != "Bearer abc" {
		t.Errorf("tools %v, Authorization %q", info.Tools, got)
	}
}

func TestRunHTTPTLSFaultKeepsItsKind(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()
	_, err := Run(context.Background(), Target{URL: srv.URL}, 20*time.Second, nil)
	if err == nil || SessionKind(err) != KindTLSUnknownAuthority {
		t.Fatalf("err = %v, kind %q", err, SessionKind(err))
	}
}

func TestTailBufferKeepsTheLastLines(t *testing.T) {
	b := &tailBuffer{max: 2}
	b.Write([]byte("a\nb\nc\npart"))
	if got := b.String(); got != "b\nc\npart" {
		t.Errorf("tail = %q", got)
	}
}

// A timeout takes the grandchild down with the child: it holds the secrets in
// its environment and must not outlive the probe.
func TestRunTimeoutKillsTheGrandchild(t *testing.T) {
	exe, _ := os.Executable()
	pidFile := filepath.Join(t.TempDir(), "pid")
	_, err := Run(context.Background(), Target{Command: exe, Env: append(os.Environ(), "PROBE_HELPER=spawn", "PROBE_PIDFILE="+pidFile)}, 1500*time.Millisecond, nil)
	if err == nil {
		t.Fatal("a server that never answers must time out")
	}
	b, rerr := os.ReadFile(pidFile)
	if rerr != nil {
		t.Fatalf("the helper did not start its grandchild: %v", rerr)
	}
	pid, _ := strconv.Atoi(string(b))
	deadline := time.Now().Add(5 * time.Second)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d is still running after the probe returned", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
