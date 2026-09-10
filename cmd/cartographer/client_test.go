package main

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// writeClientCfg installs a .cartographer.yaml in a temporary HOME, which is
// what clientconfig.TargetDir resolves, and returns that directory.
func writeClientCfg(t *testing.T, agents, knownKBs []string, clients map[string]clientconfig.ClientBinding) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := clientconfig.Default()
	cfg.ServerURL = "http://localhost:39273/mcp"
	cfg.Agents = agents
	cfg.KnownKBs = knownKBs
	cfg.Clients = clients
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return home
}

func loadClientCfg(t *testing.T, dir string) *clientconfig.Config {
	t.Helper()
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func TestCmdClientListReportsOrigin(t *testing.T) {
	writeClientCfg(t,
		[]string{"claude", "codex"},
		[]string{"alpha", "beta"},
		map[string]clientconfig.ClientBinding{"claude": {KBs: []string{"alpha"}}},
	)

	out := withStdout(t, func() {
		if code := cmdClient([]string{"list"}); code != 0 {
			t.Errorf("cmdClient list = %d, want 0", code)
		}
	})

	for _, want := range []string{"claude", "explicit", "alpha", "codex", "default (all known)", "alpha, beta"} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q:\n%s", want, out)
		}
	}
}

func TestCmdClientShowListsUnboundKBs(t *testing.T) {
	writeClientCfg(t,
		[]string{"claude"},
		[]string{"alpha", "beta", "gamma"},
		map[string]clientconfig.ClientBinding{"claude": {KBs: []string{"beta"}}},
	)

	out := withStdout(t, func() {
		if code := cmdClient([]string{"show", "claude"}); code != 0 {
			t.Errorf("cmdClient show = %d, want 0", code)
		}
	})

	if !strings.Contains(out, "bound      beta") {
		t.Errorf("show output missing the bound KB:\n%s", out)
	}
	if !strings.Contains(out, "not bound  alpha, gamma") {
		t.Errorf("show output missing the unbound KBs:\n%s", out)
	}
}

// TestCmdClientBindAnnouncesTheReduction: creating the first binding narrows
// what a provider receives, and the output must say so — otherwise the change
// reads as additive.
func TestCmdClientBindAnnouncesTheReduction(t *testing.T) {
	dir := writeClientCfg(t, []string{"claude"}, []string{"alpha", "beta"}, nil)

	out := withStdout(t, func() {
		if code := cmdClient([]string{"bind", "claude", "alpha"}); code != 0 {
			t.Errorf("cmdClient bind = %d, want 0", code)
		}
	})

	if !strings.Contains(out, "now receives only the KBs bound to it") {
		t.Errorf("bind did not announce the reduction:\n%s", out)
	}
	if !strings.Contains(out, "run `cartographer sync` to apply") {
		t.Errorf("bind did not tell the user to sync:\n%s", out)
	}
	// D170 made the filtered projection real: the note saying bindings are
	// recorded but not enforced belonged to D169 and was false from D170 on.
	// Asserting its absence is what keeps it from coming back.
	if strings.Contains(out, "not yet enforced") {
		t.Errorf("bind still claims bindings are not enforced:\n%s", out)
	}

	cfg := loadClientCfg(t, dir)
	if kbs, explicit := cfg.BoundKBs("claude"); !explicit || strings.Join(kbs, ",") != "alpha" {
		t.Errorf("persisted binding = %v explicit=%v, want [alpha] explicit=true", kbs, explicit)
	}

	// A second bind on an already-explicit provider must not repeat the notice.
	out = withStdout(t, func() {
		cmdClient([]string{"bind", "claude", "beta"})
	})
	if strings.Contains(out, "now receives only the KBs bound to it") {
		t.Errorf("the reduction notice repeated on an already-bound provider:\n%s", out)
	}
}

// TestCmdClientBindWarnsOnUnadvertisedKB: binding a KB the server has not
// advertised is deliberately allowed — it may be mounted later — but warned.
func TestCmdClientBindWarnsOnUnadvertisedKB(t *testing.T) {
	dir := writeClientCfg(t, []string{"claude"}, []string{"alpha"}, nil)

	errOut := withStderr(t, func() {
		if code := cmdClient([]string{"bind", "claude", "not-yet-mounted"}); code != 0 {
			t.Errorf("cmdClient bind = %d, want 0 (an unadvertised KB is not an error)", code)
		}
	})

	if !strings.Contains(errOut, "not among the KBs the server last advertised") {
		t.Errorf("expected a warning on stderr, got:\n%s", errOut)
	}
	cfg := loadClientCfg(t, dir)
	if kbs, _ := cfg.BoundKBs("claude"); strings.Join(kbs, ",") != "not-yet-mounted" {
		t.Errorf("the binding was not persisted: %v", kbs)
	}
}

// TestCmdClientUnbindLastKeepsTheEntry: removing the last KB must mean "no
// KBs", never a silent return to "every known KB".
func TestCmdClientUnbindLastKeepsTheEntry(t *testing.T) {
	dir := writeClientCfg(t,
		[]string{"claude"},
		[]string{"alpha", "beta"},
		map[string]clientconfig.ClientBinding{"claude": {KBs: []string{"alpha"}}},
	)

	out := withStdout(t, func() {
		if code := cmdClient([]string{"unbind", "claude", "alpha"}); code != 0 {
			t.Errorf("cmdClient unbind = %d, want 0", code)
		}
	})

	if !strings.Contains(out, "bound to none") {
		t.Errorf("unbind output should report an empty binding:\n%s", out)
	}
	if !strings.Contains(out, "client reset claude") {
		t.Errorf("unbind should point at reset as the way back to the default:\n%s", out)
	}

	cfg := loadClientCfg(t, dir)
	if kbs, explicit := cfg.BoundKBs("claude"); !explicit || len(kbs) != 0 {
		t.Errorf("persisted binding = %v explicit=%v, want [] explicit=true", kbs, explicit)
	}
}

func TestCmdClientResetReturnsToDefault(t *testing.T) {
	dir := writeClientCfg(t,
		[]string{"claude"},
		[]string{"alpha", "beta"},
		map[string]clientconfig.ClientBinding{"claude": {KBs: []string{"alpha"}}},
	)

	out := withStdout(t, func() {
		if code := cmdClient([]string{"reset", "claude"}); code != 0 {
			t.Errorf("cmdClient reset = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "every known KB") {
		t.Errorf("reset output missing the default description:\n%s", out)
	}

	cfg := loadClientCfg(t, dir)
	if kbs, explicit := cfg.BoundKBs("claude"); explicit || strings.Join(kbs, ",") != "alpha,beta" {
		t.Errorf("persisted binding = %v explicit=%v, want the default", kbs, explicit)
	}
}

func TestCmdClientRejectsBadProviders(t *testing.T) {
	writeClientCfg(t, []string{"claude"}, []string{"alpha"}, nil)

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"unknown provider", []string{"bind", "nonesuch", "alpha"}, "unknown provider"},
		{"provider not connected", []string{"bind", "codex", "alpha"}, "is not connected"},
		{"empty KB name", []string{"bind", "claude", "alpha,,beta"}, "empty KB name"},
		{"unknown subcommand", []string{"frobnicate"}, "unknown subcommand"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errOut := withStderr(t, func() {
				if code := cmdClient(tc.args); code != 2 {
					t.Errorf("cmdClient %v = %d, want 2", tc.args, code)
				}
			})
			if !strings.Contains(errOut, tc.want) {
				t.Errorf("stderr missing %q:\n%s", tc.want, errOut)
			}
		})
	}
}

// TestCmdClientMutationsDoNotTouchKnownKBs: the binding commands are the only
// writers of `clients`, and must never rewrite the server-owned cache.
func TestCmdClientMutationsDoNotTouchKnownKBs(t *testing.T) {
	dir := writeClientCfg(t, []string{"claude"}, []string{"alpha", "beta"}, nil)

	withStdout(t, func() {
		cmdClient([]string{"bind", "claude", "alpha"})
	})

	cfg := loadClientCfg(t, dir)
	if strings.Join(cfg.KnownKBs, ",") != "alpha,beta" {
		t.Errorf("KnownKBs = %v, want the untouched cache [alpha beta]", cfg.KnownKBs)
	}
}
