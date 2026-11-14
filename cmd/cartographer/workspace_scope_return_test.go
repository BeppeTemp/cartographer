package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// scopeReturnHome is a config dir with providers connected and one explicit
// workspace binding of the first, in workspace scope.
func scopeReturnHome(t *testing.T, provider string) (home, ws string) {
	t.Helper()
	home = t.TempDir()
	setHome(t, home)
	cfg := clientconfig.Default()
	cfg.ServerURL = deadServerURL(t)
	cfg.KnownKBs = []string{"kb-a"}
	cfg.Agents = []string{provider}
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	ws, _ = filepath.EvalSymlinks(t.TempDir())
	var code int
	withStderr(t, func() {
		withStdout(t, func() { code = cmdWorkspaceBind([]string{provider, ws, "--no-kb"}) })
	})
	if code != 0 {
		t.Fatalf("bind exit %d", code)
	}
	return home, ws
}

func saveDerivedFor(t *testing.T, home, provider string) {
	t.Helper()
	state := clientconfig.DerivedState{Bindings: map[string][]clientconfig.DerivedBinding{
		provider: {{Path: filepath.Join(home, "derived-repo"), KBs: []string{"kb-a"}}},
	}}
	if err := clientconfig.SaveDerived(home, state); err != nil {
		t.Fatal(err)
	}
}

func scopeOf(t *testing.T, home, provider string) (bool, int) {
	t.Helper()
	cfg, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.WorkspaceScope(provider) == clientconfig.ScopeWorkspace, len(cfg.Workspaces[provider])
}

// #1784: `unbind --all` returns a provider to the global scope although KB
// paths.yaml bindings are derived for it.
func TestWorkspaceUnbindAllReturnsToGlobalScope(t *testing.T) {
	home, _ := scopeReturnHome(t, "codex")
	saveDerivedFor(t, home, "codex")
	var code int
	out := withStdout(t, func() { code = cmdWorkspaceUnbind([]string{"codex", "--all"}) })
	scope, n := scopeOf(t, home, "codex")
	if code != 0 || scope || n != 0 || !strings.Contains(out, "sync") {
		t.Errorf("code=%d scope=%v explicit=%d out=%q", code, scope, n, out)
	}
}

// #1784: the last explicit binding with derived ones left keeps the scope and
// says how to leave it.
func TestWorkspaceUnbindLastExplicitKeepsScopeWhileDerived(t *testing.T) {
	home, ws := scopeReturnHome(t, "codex")
	saveDerivedFor(t, home, "codex")
	var code int
	out := withStdout(t, func() { code = cmdWorkspaceUnbind([]string{"codex", ws}) })
	scope, n := scopeOf(t, home, "codex")
	if code != 0 || !scope || n != 0 ||
		!strings.Contains(out, "stays in workspace scope through 1 bindings derived") || !strings.Contains(out, "unbind codex --all") {
		t.Errorf("code=%d scope=%v explicit=%d out=%q", code, scope, n, out)
	}
}

// Without derived state the last unbind still returns to the global scope.
func TestWorkspaceUnbindLastReturnsToGlobalWithoutDerived(t *testing.T) {
	home, ws := scopeReturnHome(t, "codex")
	var code int
	out := withStdout(t, func() { code = cmdWorkspaceUnbind([]string{"codex", ws}) })
	scope, _ := scopeOf(t, home, "codex")
	if code != 0 || scope || !strings.Contains(out, "returns to the global scope") {
		t.Errorf("code=%d scope=%v out=%q", code, scope, out)
	}
}

// `--all` on a provider in provider scope is a no-op that exits 0 and says so.
func TestWorkspaceUnbindAllNoopInProviderScope(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	cfg := clientconfig.Default()
	cfg.Agents = []string{"codex"}
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	var code int
	out := withStdout(t, func() { code = cmdWorkspaceUnbind([]string{"codex", "--all"}) })
	if code != 0 || !strings.Contains(out, "not in workspace scope") {
		t.Errorf("code=%d out=%q", code, out)
	}
}

// #1785: binding a provider whose workspace has no hook destination warns at
// the switch; one that has it does not.
func TestWorkspaceBindWarnsWhenNoHookDestination(t *testing.T) {
	for _, tc := range []struct {
		provider string
		warns    bool
	}{{"kiro", true}, {"claude", false}} {
		ws, _ := filepath.EvalSymlinks(t.TempDir())
		home := t.TempDir()
		setHome(t, home)
		cfg := clientconfig.Default()
		cfg.ServerURL = deadServerURL(t)
		cfg.Agents = []string{tc.provider}
		if err := clientconfig.Save(home, cfg); err != nil {
			t.Fatal(err)
		}
		var code int
		errOut := withStderr(t, func() {
			withStdout(t, func() { code = cmdWorkspaceBind([]string{tc.provider, ws, "--no-kb"}) })
		})
		got := strings.Contains(errOut, "no workspace destination for hook")
		if code != 0 || got != tc.warns {
			t.Errorf("%s: code=%d warned=%v want %v: %q", tc.provider, code, got, tc.warns, errOut)
		}
	}
}

// #1825: a bind on the home directory (also through a symlink to it) warns once
// and still succeeds; a folder below it does not warn.
func TestWorkspaceBindWarnsOnHomeDirectory(t *testing.T) {
	const marker = "bound to your home directory"
	for _, tc := range []string{"home", "symlink", "sub"} {
		home, _ := filepath.EvalSymlinks(t.TempDir())
		setHome(t, home)
		cfg := clientconfig.Default()
		cfg.ServerURL = deadServerURL(t)
		cfg.Agents = []string{"claude"}
		if err := clientconfig.Save(home, cfg); err != nil {
			t.Fatal(err)
		}
		target := home
		switch tc {
		case "symlink":
			target = filepath.Join(t.TempDir(), "link")
			if err := os.Symlink(home, target); err != nil {
				t.Skip("symlinks unavailable:", err)
			}
		case "sub":
			target = filepath.Join(home, "sub")
			if err := os.Mkdir(target, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		var code int
		errOut := withStderr(t, func() {
			withStdout(t, func() { code = cmdWorkspaceBind([]string{"claude", target, "--no-kb"}) })
		})
		want := 1
		if tc == "sub" {
			want = 0
		}
		if n := strings.Count(errOut, marker); code != 0 || n != want {
			t.Errorf("%s: code=%d warnings=%d want %d: %q", tc, code, n, want, errOut)
		}
		saved, err := clientconfig.Load(home)
		if err != nil || len(saved.WorkspaceBindings("claude")) != 1 {
			t.Errorf("%s: binding not saved (%v)", tc, err)
		}
	}
}

// S1: a mistyped provider is refused by `unbind --all`, as the single-path form does.
func TestWorkspaceUnbindAllRefusesUnknownProvider(t *testing.T) {
	scopeReturnHome(t, "codex")
	var code int
	errOut := withStderr(t, func() { code = cmdWorkspaceUnbind([]string{"codx", "--all"}) })
	if code != 1 || !strings.Contains(errOut, `unknown provider "codx"`) {
		t.Errorf("code=%d err=%q", code, errOut)
	}
}

// S2: explicit entries left in a hand-edited file outside workspace scope are
// removed too, or a later bind would make them live again.
func TestWorkspaceUnbindAllRemovesEntriesOutsideWorkspaceScope(t *testing.T) {
	home, _ := scopeReturnHome(t, "codex")
	cfg, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetWorkspaceScope("codex", clientconfig.ScopeProvider); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Workspaces["codex"]) == 0 {
		t.Fatal("setup: the explicit binding is gone with the scope")
	}
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	var code int
	out := withStdout(t, func() { code = cmdWorkspaceUnbind([]string{"codex", "--all"}) })
	scope, n := scopeOf(t, home, "codex")
	if code != 0 || scope || n != 0 || !strings.Contains(out, "removing them") {
		t.Errorf("code=%d scope=%v explicit=%d out=%q", code, scope, n, out)
	}
}

// C1: `connect --workspace` is the documented way into workspace scope and warns as `bind` does.
func TestConnectWorkspaceWarnsWhenNoHookDestination(t *testing.T) {
	for _, tc := range []struct {
		provider string
		warns    bool
	}{{"kiro", true}, {"claude", false}} {
		dir := t.TempDir()
		setHome(t, dir)
		ws, _ := filepath.EvalSymlinks(t.TempDir())
		var err error
		errOut := withStderr(t, func() {
			_, err = doConnect(connectOptions{Providers: []string{tc.provider}, Dir: dir, ServerURL: "http://127.0.0.1:1/mcp", Name: "cartographer", Workspace: ws})
		})
		if err != nil {
			t.Fatalf("%s: %v", tc.provider, err)
		}
		if got := strings.Contains(errOut, "no workspace destination for hook"); got != tc.warns {
			t.Errorf("%s: warned=%v want %v: %q", tc.provider, got, tc.warns, errOut)
		}
	}
}

// C2: the status path resolves with the same function as sync; a hook with no
// workspace destination is neither pending nor drift there, and the resolver
// carries the skipped note that sync prints and the instructions block names.
func TestStatusWorkspaceHookWithoutDestinationIsNotDrift(t *testing.T) {
	hook := contract.Artifact{Kind: "hook", Name: "guard", Source: "kb:kb-a",
		Files: []contract.ArtifactFile{{Path: "hook.json", Content: []byte(`{"event":"Stop","command":"x"}`)}}}
	ps := []syncProjection{{Provider: "kiro", Workspace: "/ws/r", Scope: contract.ScopeProject}}
	manifests := map[string]contract.Manifest{projectionKey(ps[0]): {Artifacts: []contract.Artifact{hook}}}
	resolved := resolveProjectionSet(ps, manifests, contract.ProjectOptions{})
	_, skipped := provisioning.ReportNotes(resolved[projectionKey(ps[0])].Notes)
	if len(skipped) != 1 || skipped[0].Artifact != "hook/guard" || !strings.Contains(skipped[0].Reason, "no workspace destination") {
		t.Fatalf("skipped = %+v", skipped)
	}
	pm, d, _, _ := workspaceProviderDiff(&clientconfig.Config{}, ps, manifests, resolved, provisioning.LockFile{Providers: map[string]provisioning.Lock{}}, t.TempDir())
	if len(pm.Artifacts) != 0 || len(d.Added) != 0 || len(d.Updated) != 0 || len(d.Removed) != 0 {
		t.Errorf("manifest=%v diff=%+v", pm.Artifacts, d)
	}
}

// C3: the kinds listed are the ones with a global cell and no project cell.
func TestWorkspaceLacksKinds(t *testing.T) {
	if got := provisioning.WorkspaceLacksKinds(configurator.ProviderKiro); !strings.Contains(strings.Join(got, ","), "hook") {
		t.Errorf("kiro lacks %v, want hook among them", got)
	}
	if got := provisioning.WorkspaceLacksKinds(configurator.ProviderClaudeCode); len(got) != 0 {
		t.Errorf("claude lacks %v, want none", got)
	}
}
