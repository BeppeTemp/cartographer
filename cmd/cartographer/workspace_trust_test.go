package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// trustHome sets up a home with a client config and a workspace folder, the
// way `connect` would have left them. Returns home and the canonical folder.
func trustHome(t *testing.T) (home, ws string) {
	t.Helper()
	h, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", h)
	t.Setenv("USERPROFILE", h)
	cfg := clientconfig.Default()
	cfg.KnownKBs = []string{"kb-a"}
	if err := clientconfig.Save(h, cfg); err != nil {
		t.Fatal(err)
	}
	w, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return h, w
}

func trustStatus(t *testing.T, home string) []workspaceStatus {
	t.Helper()
	cfg, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	return workspaceStatuses(cfg, "codex", home)
}

func TestWorkspaceBindCodexTrust(t *testing.T) {
	home, ws := trustHome(t)
	cfgPath := provisioning.CodexConfigPath(home)
	var code int
	out := withStdout(t, func() { code = cmdWorkspaceBind([]string{"codex", ws, "--kb", "kb-a", "--trust"}) })
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !provisioning.CodexProjectTrusted(cfgPath, ws) {
		t.Fatal("the folder is not trusted after --trust")
	}
	if st := trustStatus(t, home); len(st) != 1 || st[0].State != "ok" {
		t.Errorf("status after --trust: %+v", st)
	}
	// Twice: one table.
	before, _ := os.ReadFile(cfgPath)
	out = withStdout(t, func() { code = cmdWorkspaceBind([]string{"codex", ws, "--kb", "kb-a", "--trust"}) })
	after, _ := os.ReadFile(cfgPath)
	if code != 0 || string(before) != string(after) || !strings.Contains(out, "already trusted") {
		t.Errorf("second --trust: code=%d out=%q changed=%v", code, out, string(before) != string(after))
	}
	if n := strings.Count(string(after), "[projects."); n != 1 {
		t.Errorf("%d project tables", n)
	}
	// Unbind leaves the table alone and does not mention trust.
	out = withStdout(t, func() { code = cmdWorkspaceUnbind([]string{"codex", ws}) })
	if code != 0 || strings.Contains(out, "trusted") {
		t.Errorf("unbind: code=%d out=%q", code, out)
	}
	if now, _ := os.ReadFile(cfgPath); string(now) != string(after) {
		t.Error("unbind changed the Codex config")
	}
}

func TestWorkspaceBindCodexWithoutTrustPrintsTheLine(t *testing.T) {
	home, ws := trustHome(t)
	cfgPath := provisioning.CodexConfigPath(home)
	orig := "model = \"o3\"\n"
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	var code int
	out := withStdout(t, func() { code = cmdWorkspaceBind([]string{"codex", ws, "--kb", "kb-a"}) })
	if code != 0 || !strings.Contains(out, provisioning.ManualLine(ws)) {
		t.Errorf("code=%d out=%q", code, out)
	}
	if b, _ := os.ReadFile(cfgPath); string(b) != orig {
		t.Error("bind without the flag wrote to the Codex config")
	}
	st := trustStatus(t, home)
	if len(st) != 1 || st[0].State != "inactive" || !strings.Contains(st[0].Detail, "--trust") {
		t.Errorf("status: %+v", st)
	}
	// Once trusted, the hint is gone.
	if _, err := provisioning.TrustCodexProject(cfgPath, ws); err != nil {
		t.Fatal(err)
	}
	out = withStdout(t, func() { cmdWorkspaceBind([]string{"codex", ws, "--kb", "kb-a"}) })
	if strings.Contains(out, "trust_level") {
		t.Errorf("hint printed for a trusted folder: %q", out)
	}
}

func TestWorkspaceBindTrustOnAnotherProviderIsAUsageError(t *testing.T) {
	home, ws := trustHome(t)
	var code int
	errOut := withStderr(t, func() { code = cmdWorkspaceBind([]string{"claude", ws, "--kb", "kb-a", "--trust"}) })
	if code != 2 || !strings.Contains(errOut, "claude") {
		t.Errorf("code=%d err=%q", code, errOut)
	}
	cfg, _ := clientconfig.Load(home)
	if len(cfg.WorkspaceBindings("claude")) != 0 {
		t.Error("the workspace was bound despite the usage error")
	}
}

func TestWorkspaceBindTrustRefusalKeepsTheBinding(t *testing.T) {
	home, ws := trustHome(t)
	cfgPath := provisioning.CodexConfigPath(home)
	orig := "[projects.\"" + ws + "\"]\ntrust_level = \"untrusted\"\n"
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	var code int
	errOut := withStderr(t, func() { code = cmdWorkspaceBind([]string{"codex", ws, "--kb", "kb-a", "--trust"}) })
	if code == 0 || !strings.Contains(errOut, "binding was saved") || !strings.Contains(errOut, "trust_level = \"trusted\"") {
		t.Errorf("code=%d err=%q", code, errOut)
	}
	if b, _ := os.ReadFile(cfgPath); string(b) != orig {
		t.Error("an explicit untrusted was overridden")
	}
	cfg, _ := clientconfig.Load(home)
	if len(cfg.WorkspaceBindings("codex")) != 1 {
		t.Error("the binding was not saved")
	}
}

func TestDerivedBindingIsInactiveWithTheLine(t *testing.T) {
	home, ws := trustHome(t)
	cfg := workspaceScoped("codex", "kb-a")
	cfg.SetDerived(clientconfig.DerivedState{Bindings: map[string][]clientconfig.DerivedBinding{"codex": {{Path: ws, KBs: []string{"kb-a"}}}}})
	st := workspaceStatuses(cfg, "codex", home)
	if len(st) != 1 || st[0].State != "inactive" || !strings.Contains(st[0].Detail, "trust_level = \"trusted\"") {
		t.Errorf("status: %+v", st)
	}
	if _, err := os.Stat(provisioning.CodexConfigPath(home)); err == nil {
		t.Error("a derived binding wrote the Codex config")
	}
}
