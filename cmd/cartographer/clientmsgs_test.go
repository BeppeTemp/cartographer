package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/mcpprobe"
)

// #2200: the refusals and notes below each say what to do next.

func TestApproveUnknownNameIsNamedInTheRefusal(t *testing.T) {
	approveFixture(t, false)
	var code int
	errOut := withStderr(t, func() { code = cmdApprove([]string{"mcp", "nope", "--kb", "kb-a", "--yes"}) })
	if code != 2 || !strings.Contains(errOut, `no mcp artifact named "nope"`) || !strings.Contains(errOut, "in KB kb-a") || !strings.Contains(errOut, "approve --pending") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}

func TestApproveSyncDoesNotAdviseTheSyncItRuns(t *testing.T) {
	approveFixture(t, false)
	old := syncFn
	syncFn = func([]string) int { return 0 }
	t.Cleanup(func() { syncFn = old })
	out := withStdout(t, func() { cmdApprove([]string{"hook", "guard", "--kb", "kb-a", "--yes", "--sync"}) })
	if !strings.Contains(out, "approved hook guard") || strings.Contains(out, "run cartographer sync") {
		t.Errorf("--sync output advises the sync it runs:\n%s", out)
	}
	out = withStdout(t, func() { cmdApprove([]string{"hook", "guard", "--kb", "kb-a", "--yes"}) })
	if !strings.Contains(out, "run cartographer sync to materialize it") {
		t.Errorf("without --sync the advice stays:\n%s", out)
	}
}

func TestWorkspaceBindMissingFolderSaysSo(t *testing.T) {
	_, err := clientconfig.CanonicalWorkspacePath(filepath.Join(t.TempDir(), "nope"))
	if err == nil || !strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "lstat") {
		t.Errorf("err = %v, want a plain \"does not exist\" with the remedy", err)
	}
}

func TestMCPProbeSessionErrorPrintsOneLinePerLine(t *testing.T) {
	var b strings.Builder
	printProbe(&b, "kb-a", "svc", mcpprobe.Result{Transport: "stdio", Target: "srv", Error: "exited 1\nboom: no token\nsecond"})
	out := b.String()
	if strings.Contains(out, `\n`) || !strings.Contains(out, "session: failed: exited 1\n  boom: no token\n  second\n") {
		t.Errorf("stderr of the child is not one line each:\n%s", out)
	}
}

func TestClientUnbindOfAKBNotBoundSaysSo(t *testing.T) {
	dir := writeClientCfg(t, []string{"claude"}, []string{"kb-a", "kb-b"}, map[string]clientconfig.ClientBinding{
		"claude": {KBs: []string{"kb-a"}, KBsSet: true},
	})
	pointClientAt(t, dir, deadServerURL(t))
	var code int
	errOut := withStderr(t, func() { withStdout(t, func() { code = cmdClient([]string{"unbind", "claude", "kb-b"}) }) })
	if code != 0 || !strings.Contains(errOut, "kb-b was not bound to claude (bound: kb-a)") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
	errOut = withStderr(t, func() { withStdout(t, func() { code = cmdClient([]string{"unbind", "claude", "kb-a"}) }) })
	if strings.Contains(errOut, "not bound") {
		t.Errorf("a bound KB is reported as not bound: %q", errOut)
	}
}

func TestPathsUnsetNamesTheKeyAsTyped(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	writeClientConfig(t, home, "paths:\n  demo: /x\n")
	var code int
	out := withStdout(t, func() { code = cmdPaths([]string{"unset", "repo:demo"}) })
	if code != 0 || !strings.Contains(out, "paths: repo:demo removed") {
		t.Errorf("exit %d, out %q", code, out)
	}
}

func TestDoctorJSONWithOutputTableIsRefused(t *testing.T) {
	setHome(t, t.TempDir())
	var code int
	errOut := withStderr(t, func() { withStdout(t, func() { code = cmdDoctor([]string{"--json", "--output", "table"}) }) })
	if code != 2 || !strings.Contains(errOut, "--json") || !strings.Contains(errOut, "--output") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}
