package main

import (
	"os"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// AC-transv-4
func TestClientKBTransversalFlag(t *testing.T) {
	dir := writeClientCfg(t, []string{"claude"}, []string{"kb-a"}, nil)

	out := withStdout(t, func() {
		if code := cmdClient([]string{"kb", "kb-a", "--transversal=true"}); code != 0 {
			t.Errorf("set = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "transversal") || !strings.Contains(out, "true") || !strings.Contains(out, "cartographer sync") {
		t.Errorf("output lacks the flag or the sync hint: %q", out)
	}
	if !loadClientCfg(t, dir).KBTransversal("kb-a") {
		t.Error("flag not saved")
	}

	errOut := withStderr(t, func() {
		if code := cmdClient([]string{"kb", "kb-a", "--transversal=maybe"}); code != 2 {
			t.Errorf("bad bool = %d, want 2", code)
		}
	})
	if !strings.Contains(errOut, `invalid --transversal "maybe" (want true or false)`) {
		t.Errorf("stderr: %q", errOut)
	}
	if !loadClientCfg(t, dir).KBTransversal("kb-a") {
		t.Error("a rejected value must not change the file")
	}

	withStdout(t, func() {
		if code := cmdClient([]string{"kb", "kb-a", "--transversal=false"}); code != 0 {
			t.Errorf("clear = %d, want 0", code)
		}
	})
	if cfg := loadClientCfg(t, dir); cfg.KBTransversal("kb-a") || cfg.KBOptions != nil {
		t.Errorf("false must remove the entry: %v", cfg.KBOptions)
	}
}

// AC-transv-20
func TestClientKBNamesProvidersWithoutEffect(t *testing.T) {
	dir := writeClientCfg(t, []string{"claude", "codex", "cline"}, []string{"kb-a"}, nil)
	cfg := loadClientCfg(t, dir)
	if err := cfg.SetWorkspaceScope("claude", clientconfig.ScopeWorkspace); err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}

	out := withStdout(t, func() {
		if code := cmdClient([]string{"kb", "kb-a", "--transversal=true"}); code != 0 {
			t.Errorf("set = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "codex: provider scope, the flag has no effect") {
		t.Errorf("provider-scope note missing:\n%s", out)
	}
	if !strings.Contains(out, "cline: cannot be scoped to a workspace (") {
		t.Errorf("unsupported-provider note missing:\n%s", out)
	}
	if strings.Contains(out, "claude:") {
		t.Errorf("a workspace-scope provider must have no note:\n%s", out)
	}

	// Not transversal: no notes at all.
	out = withStdout(t, func() {
		if code := cmdClient([]string{"kb", "kb-a", "--transversal=false"}); code != 0 {
			t.Errorf("clear = %d, want 0", code)
		}
	})
	if strings.Contains(out, "no effect") || strings.Contains(out, "cannot be scoped") {
		t.Errorf("notes printed for a KB that is not transversal:\n%s", out)
	}
}

// AC-transv-3: a legacy kb_options.<kb>.transversal is ignored, and every
// command that loads the client file says so, once.
func TestLegacyTransversalWarnsInEveryCommand(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	raw := "server_url: " + deadServerURL(t) + "\nserver_name: cartographer\nkb_options:\n  kb-a:\n    transversal: true\n"
	if err := os.WriteFile(clientconfig.Path(home), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	// The warning is printed once per process: the first command says it.
	errOut := withStderr(t, func() { withStdout(t, func() { cmdStatus([]string{"--output", "json"}) }) })
	if n := strings.Count(errOut, "kb_options.kb-a.transversal is ignored"); n != 1 {
		t.Errorf("status printed the warning %d times:\n%s", n, errOut)
	}
	errOut = withStderr(t, func() { withStdout(t, func() { cmdClient([]string{"kb", "kb-a"}) }) })
	if strings.Contains(errOut, "is ignored") {
		t.Errorf("a second command in the same process repeated it:\n%s", errOut)
	}
	if loadClientCfg(t, home).KBTransversal("kb-a") {
		t.Error("the legacy key was applied")
	}
}
