package main

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/service"
)

// deadServerURL is an MCP URL on a port nobody listens on: the default endpoint
// may have a real server behind it on a maintainer machine.
func deadServerURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return "http://" + addr + "/mcp"
}

// pointClientAt rewrites the saved client config's server URL.
func pointClientAt(t *testing.T, dir, url string) {
	t.Helper()
	cfg := loadClientCfg(t, dir)
	cfg.ServerURL = url
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
}

// #1283: a binding to a KB the server does not mount is refused when the server
// answers, and the config is left as it was.
func TestClientBindRefusesAKBTheServerDoesNotMount(t *testing.T) {
	srv := multiKBServer(t, `{"status":"ok","kbs":[{"name":"kb-a"},{"name":"kb-b"}]}`)
	defer srv.Close()
	dir := writeClientCfg(t, []string{"claude"}, []string{"kb-a"}, nil)
	pointClientAt(t, dir, srv.URL+"/mcp")

	var code int
	errOut := withStderr(t, func() { code = cmdClient([]string{"bind", "claude", "nokb"}) })
	if code != 2 || !strings.Contains(errOut, "does not mount nokb") {
		t.Fatalf("bind of an unmounted KB = %d, stderr %q; want 2 and the refusal", code, errOut)
	}
	if _, explicit := loadClientCfg(t, dir).BoundKBs("claude"); explicit {
		t.Error("the refused bind was saved")
	}

	// A duplicate in the list is stored once.
	withStdout(t, func() { code = cmdClient([]string{"bind", "claude", "kb-b,kb-b"}) })
	if kbs, _ := loadClientCfg(t, dir).BoundKBs("claude"); strings.Join(kbs, ",") != "kb-b" {
		t.Errorf("bound = %v, want kb-b once", kbs)
	}
}

func TestWorkspaceBindRefusesAndDedupes(t *testing.T) {
	srv := multiKBServer(t, `{"status":"ok","kbs":[{"name":"kb-a"},{"name":"kb-b"}]}`)
	defer srv.Close()
	dir := writeClientCfg(t, []string{"claude"}, []string{"kb-a"}, nil)
	pointClientAt(t, dir, srv.URL+"/mcp")
	ws := t.TempDir()

	var code int
	errOut := withStderr(t, func() { code = cmdWorkspaceBind([]string{"claude", ws, "--kb", "nokb"}) })
	if code != 2 || !strings.Contains(errOut, "does not mount nokb") {
		t.Fatalf("workspace bind of an unmounted KB = %d, stderr %q", code, errOut)
	}
	if n := len(loadClientCfg(t, dir).Workspaces["claude"]); n != 0 {
		t.Errorf("the refused bind left %d workspace binding(s)", n)
	}

	withStdout(t, func() { code = cmdWorkspaceBind([]string{"claude", ws, "--kb", "kb-b,kb-b"}) })
	if code != 0 {
		t.Fatalf("bind exit = %d", code)
	}
	got := loadClientCfg(t, dir).Workspaces["claude"]
	if len(got) != 1 || strings.Join(got[0].KBs, ",") != "kb-b" {
		t.Errorf("workspaces = %+v, want one binding to kb-b once", got)
	}
}

// With no server the binding goes through, with a warning that the check was skipped.
func TestWorkspaceBindWithoutServerWarnsAndProceeds(t *testing.T) {
	dir := writeClientCfg(t, []string{"claude"}, nil, nil)
	pointClientAt(t, dir, deadServerURL(t))
	var code int
	errOut := withStderr(t, func() {
		withStdout(t, func() { code = cmdWorkspaceBind([]string{"claude", t.TempDir(), "--kb", "nokb"}) })
	})
	if code != 0 || !strings.Contains(errOut, "could not check that the server mounts nokb") {
		t.Errorf("exit %d, stderr %q; want 0 and the skipped-check warning", code, errOut)
	}
}

func TestWorkspaceUnbindOfAPathNeverBoundFails(t *testing.T) {
	dir := writeClientCfg(t, []string{"claude"}, nil, nil)
	pointClientAt(t, dir, deadServerURL(t))
	var code int
	out := withStdout(t, func() {
		errOut := withStderr(t, func() { code = cmdWorkspaceUnbind([]string{"claude", t.TempDir()}) })
		if !strings.Contains(errOut, "no workspace binding") {
			t.Errorf("stderr = %q", errOut)
		}
	})
	if code != 1 || strings.Contains(out, "unbound") {
		t.Errorf("exit %d, stdout %q; want 1 and no \"unbound\"", code, out)
	}
}

// #1283: -h is a success on every subcommand and prints usage without "Error:".
func TestHelpFlagExitsZeroEverywhere(t *testing.T) {
	setHome(t, t.TempDir())
	for _, args := range [][]string{
		{"agents", "-h"}, {"status", "-h"}, {"approve", "-h"}, {"approve", "revoke", "-h"},
		{"config", "-h"}, {"mcp", "-h"}, {"client", "bind", "-h"}, {"client", "update", "--help"},
		{"workspace", "bind", "-h"}, {"workspace", "unbind", "-h"}, {"update", "check", "-h"},
		{"service", "sync-timer", "-h"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var code int
			var errOut string
			out := withStdout(t, func() { errOut = withStderr(t, func() { code = run(args) }) })
			if code != 0 {
				t.Errorf("exit = %d, want 0", code)
			}
			if strings.Contains(out+errOut, "Error:") || strings.Contains(out+errOut, "unknown flag") {
				t.Errorf("help reads as an error:\n%s%s", out, errOut)
			}
			if !strings.Contains(strings.ToLower(out+errOut), "usage") {
				t.Errorf("no usage printed:\n%s%s", out, errOut)
			}
		})
	}
}

// #1283: agents and status judge "reachable" the same way.
func TestAgentsJSONReachableMatchesStatus(t *testing.T) {
	dir := writeClientCfg(t, []string{"claude"}, nil, nil)
	_ = dir
	old := statusHealthFn
	t.Cleanup(func() { statusHealthFn = old })
	statusHealthFn = func(*clientconfig.Config) (*client.Health, error) { return &client.Health{Version: "v1.0.0"}, nil }
	out := withStdout(t, func() { cmdAgents([]string{"--output", "json"}) })
	if !strings.Contains(out, `"reachable":true`) {
		t.Errorf("agents --output json with a live server:\n%s", out)
	}
	statusHealthFn = func(*clientconfig.Config) (*client.Health, error) { return nil, fmt.Errorf("down") }
	out = withStdout(t, func() { cmdAgents([]string{"--output", "json"}) })
	if !strings.Contains(out, `"reachable":false`) {
		t.Errorf("agents --output json with the server down:\n%s", out)
	}
}

// #1283: `paths set` echoes the id, kind included.
func TestPathsSetEchoesTheKind(t *testing.T) {
	writeClientCfg(t, []string{"claude"}, nil, nil)
	out := withStdout(t, func() {
		withStderr(t, func() {
			if code := cmdPaths([]string{"set", "repo:cart", t.TempDir()}); code != 0 {
				t.Errorf("exit = %d", code)
			}
		})
	})
	if !strings.HasPrefix(out, "paths: repo:cart -> ") {
		t.Errorf("echo = %q, want it to start with \"paths: repo:cart -> \"", out)
	}
}

// #1271: a sync timer refused for a non-account HOME is said as such, not as a failure.
func TestEnsureSyncTimerSandboxHomeIsNotAWarning(t *testing.T) {
	stubTimer(t, false)
	syncTimerInstallFn = func(time.Duration) error {
		return fmt.Errorf("%w (HOME=/x, account home /y)", service.ErrSandboxHome)
	}
	dir := t.TempDir()
	writeOptOut(t, dir, false)
	var out string
	errOut := withStderr(t, func() {
		out = withStdout(t, func() { ensureSyncTimer(dir, []string{"kiro"}) })
	})
	if !strings.Contains(out, "sync timer not installed: HOME is not this account's home (expected in tests)") {
		t.Errorf("stdout = %q", out)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want nothing", errOut)
	}
}

// #1271: kb list with no server says it is not running and how to start it.
func TestKBListNoServerNamesTheStartCommand(t *testing.T) {
	_, err := mountedKBNames(strings.TrimSuffix(deadServerURL(t), "/mcp"))
	if err == nil {
		t.Fatal("expected a connection error")
	}
	line := mountErrorLine(err)
	if !strings.Contains(line, "server is not running") || !strings.Contains(line, "cartographer service start") {
		t.Errorf("line = %q", line)
	}
	if strings.Contains(line, "refused") {
		t.Errorf("the raw error leaked: %q", line)
	}
	if other := mountErrorLine(fmt.Errorf("status 500")); !strings.Contains(other, "status 500") {
		t.Errorf("other failures keep their text: %q", other)
	}
}
