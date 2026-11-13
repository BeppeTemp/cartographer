package main

import (
	"context"
	"crypto/x509"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/mcpexec"
	"github.com/BeppeTemp/cartographer/internal/mcpprobe"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

func probeFixture(t *testing.T, approvedHash string) {
	t.Helper()
	home := t.TempDir()
	setHome(t, home)
	cfg := &clientconfig.Config{ServerURL: "https://cartographer.example/mcp", Agents: []string{"claude"}}
	if approvedHash != "" {
		if err := cfg.ApproveMCP("kb-a", "svc", approvedHash, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	mk := func(source, name, body string) contract.Artifact {
		return contract.Artifact{Kind: "mcp", Name: name, Source: source, ContentHash: "h1", Files: []contract.ArtifactFile{{Path: name + ".json", Content: []byte(body)}}}
	}
	const http = `{"type":"http","url":"https://svc.example.com/mcp","headers":{"Authorization":"Bearer ${PROBE_TEST_TOKEN}"}}`
	arts := []contract.Artifact{
		mk("kb:kb-a", "svc", http),
		mk("kb:kb-a", "dup", http),
		mk("kb:kb-b", "dup", http),
	}
	oldM, oldL, oldD, oldR, oldS := approveManifest, probeLookupEnv, probeDial, probeResolve, probeSession
	probeResolve = func(context.Context, string) (*mcpexec.Launch, error) {
		return &mcpexec.Launch{Spec: contract.MCPServerSpec{Type: "http"}, URL: "https://svc.example.com/mcp"}, nil
	}
	probeSession = func(context.Context, mcpprobe.Target, time.Duration, func(string) string) (mcpprobe.Info, error) {
		return mcpprobe.Info{Protocol: "2025-06-18", Server: "fake", Version: "1", Tools: []string{"a", "b"}}, nil
	}
	approveManifest = func(*clientconfig.Config) (contract.Manifest, error) {
		return contract.Manifest{Revision: "r", Artifacts: arts}, nil
	}
	probeLookupEnv = func(string) (string, bool) { return "", false }
	probeDial = func(context.Context, string, time.Duration) error { return nil }
	t.Cleanup(func() {
		approveManifest, probeLookupEnv, probeDial, probeResolve, probeSession = oldM, oldL, oldD, oldR, oldS
	})
}

func runProbe(t *testing.T, args ...string) (code int, out, errOut string) {
	t.Helper()
	out = withStdout(t, func() { errOut = withStderr(t, func() { code = cmdMCPProbe(args) }) })
	return
}

func TestMCPProbe_TargetResolution(t *testing.T) {
	probeFixture(t, "h1")
	if code, _, e := runProbe(t, "kb-a/nope"); code != 2 || !strings.Contains(e, "no MCP descriptor") {
		t.Errorf("not found: exit %d, stderr %q", code, e)
	}
	if code, _, e := runProbe(t, "dup"); code != 2 || !strings.Contains(e, "kb-a/dup, kb-b/dup") {
		t.Errorf("ambiguous: exit %d, stderr %q", code, e)
	}
	if code, _, e := runProbe(t, "kb-a/dup"); code != 2 || !strings.Contains(e, "not approved") || !strings.Contains(e, "approve mcp dup --kb kb-a") {
		t.Errorf("not approved: exit %d, stderr %q", code, e)
	}
	if code, _, _ := runProbe(t); code != 2 {
		t.Errorf("no target: exit %d, want 2", code)
	}
}

func TestMCPProbe_UnresolvedVariableExits3NamingIt(t *testing.T) {
	probeFixture(t, "h1")
	code, out, _ := runProbe(t, "kb-a/svc")
	if code != 3 || !strings.Contains(out, "PROBE_TEST_TOKEN") || !strings.Contains(out, "unset in this shell") {
		t.Fatalf("exit %d, out %q", code, out)
	}
	probeLookupEnv = func(string) (string, bool) { return "sekrit-value", true }
	code, out, _ = runProbe(t, "kb-a/svc")
	if code != 0 || strings.Contains(out, "sekrit-value") {
		t.Fatalf("resolved: exit %d, out %q (a value must never be printed)", code, out)
	}
}

func TestMCPProbe_NetworkAndTLSExitCodes(t *testing.T) {
	probeFixture(t, "h1")
	probeLookupEnv = func(string) (string, bool) { return "v", true }
	probeDial = func(context.Context, string, time.Duration) error { return x509.UnknownAuthorityError{} }
	code, out, _ := runProbe(t, "--json", "kb-a/svc")
	if code != 5 || !strings.Contains(out, `"net":"tls_unknown_authority"`) || !strings.Contains(out, "NODE_EXTRA_CA_CERTS") {
		t.Errorf("tls: exit %d, out %q", code, out)
	}
	probeDial = func(context.Context, string, time.Duration) error { return context.DeadlineExceeded }
	if code, out, _ := runProbe(t, "kb-a/svc"); code != 4 || !strings.Contains(out, "network: timeout") {
		t.Errorf("timeout: exit %d, out %q", code, out)
	}
}

func TestMCPProbe_SessionReportsToolsAndExitsZero(t *testing.T) {
	probeFixture(t, "h1")
	probeLookupEnv = func(string) (string, bool) { return "v", true }
	code, out, _ := runProbe(t, "kb-a/svc")
	if code != 0 || !strings.Contains(out, "tools: 2") || !strings.Contains(out, "server fake 1") {
		t.Fatalf("exit %d, out %q", code, out)
	}
}

func TestMCPProbe_SessionFailureExits1AndNeverPrintsASecret(t *testing.T) {
	probeFixture(t, "h1")
	probeLookupEnv = func(string) (string, bool) { return "v", true }
	probeResolve = func(context.Context, string) (*mcpexec.Launch, error) {
		return &mcpexec.Launch{Spec: contract.MCPServerSpec{Type: "http", Headers: map[string]string{"Authorization": "Bearer ${PROBE_TEST_TOKEN}"}},
			URL: "https://svc.example.com/mcp", Headers: map[string]string{"Authorization": "Bearer sekrit-value"}}, nil
	}
	probeSession = func(_ context.Context, _ mcpprobe.Target, _ time.Duration, scrub func(string) string) (mcpprobe.Info, error) {
		return mcpprobe.Info{}, errors.New(scrub("server said: bad token Bearer sekrit-value"))
	}
	code, out, _ := runProbe(t, "kb-a/svc")
	if code != 1 || !strings.Contains(out, "session: failed") || strings.Contains(out, "sekrit-value") {
		t.Fatalf("exit %d, out %q", code, out)
	}
	// A TLS fault met by the session is exit 5 with the hint, like the dial.
	probeSession = func(context.Context, mcpprobe.Target, time.Duration, func(string) string) (mcpprobe.Info, error) {
		return mcpprobe.Info{}, x509.UnknownAuthorityError{}
	}
	if code, out, _ := runProbe(t, "kb-a/svc"); code != 5 || !strings.Contains(out, "NODE_EXTRA_CA_CERTS") {
		t.Errorf("tls in session: exit %d, out %q", code, out)
	}
	// A launch that cannot be resolved (server down, descriptor changed) is exit 1.
	probeResolve = func(context.Context, string) (*mcpexec.Launch, error) {
		return nil, errors.New("cannot reach the server")
	}
	if code, out, _ := runProbe(t, "kb-a/svc"); code != 1 || !strings.Contains(out, "cannot reach the server") {
		t.Errorf("resolve failure: exit %d, out %q", code, out)
	}
}

func TestMCPProbe_RequiresWordsANetworkFault(t *testing.T) {
	probeFixture(t, "h1")
	probeLookupEnv = func(string) (string, bool) { return "v", true }
	probeDial = func(context.Context, string, time.Duration) error { return context.DeadlineExceeded }
	oldM := approveManifest
	t.Cleanup(func() { approveManifest = oldM })
	approveManifest = func(*clientconfig.Config) (contract.Manifest, error) {
		a := contract.Artifact{Kind: "mcp", Name: "svc", Source: "kb:kb-a", ContentHash: "h1", Files: []contract.ArtifactFile{{Path: "svc.json",
			Content: []byte(`{"type":"http","url":"https://svc.example.com/mcp","headers":{"A":"${X}"},"requires":"vpn"}`)}}}
		return contract.Manifest{Artifacts: []contract.Artifact{a}}, nil
	}
	code, out, _ := runProbe(t, "kb-a/svc")
	if code != 4 || !strings.Contains(out, "needs vpn: svc.example.com did not answer (timeout)") {
		t.Errorf("exit %d, out %q", code, out)
	}
	if _, jout, _ := runProbe(t, "--json", "kb-a/svc"); !strings.Contains(jout, `"needs":"vpn"`) {
		t.Errorf("json %q", jout)
	}
	// A TLS fault is not the network: no label.
	probeDial = func(context.Context, string, time.Duration) error { return x509.UnknownAuthorityError{} }
	if code, out, _ := runProbe(t, "kb-a/svc"); code != 5 || strings.Contains(out, "needs vpn") {
		t.Errorf("tls: exit %d, out %q", code, out)
	}
}
