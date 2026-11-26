package main

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// AC-cad-6: `client bind <provider> --all` records the marker with the server
// unreachable and says what the provider was bound to; `--all` with a name or a
// lineage flag is a usage error and changes nothing.
func TestClientBindAll(t *testing.T) {
	dir := writeClientCfg(t, []string{"claude", "codex"}, []string{"alpha", "beta"},
		map[string]clientconfig.ClientBinding{"claude": {KBs: []string{"alpha"}, NoLineage: true}})

	for _, args := range [][]string{
		{"bind", "claude", "alpha", "--all"},
		{"bind", "claude", "--all", "alpha"},
		{"bind", "claude", "--all", "--no-lineage"},
		{"bind", "--all"},
		{"bind", "nobody", "--all"},
	} {
		var code int
		withStderr(t, func() { code = cmdClient(args) })
		if code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	if b := loadClientCfg(t, dir).Clients["claude"]; b.FollowsServer || strings.Join(b.KBs, ",") != "alpha" {
		t.Fatalf("a refused bind changed the binding: %+v", b)
	}

	out := withStdout(t, func() {
		if code := cmdClient([]string{"bind", "claude", "--all"}); code != 0 {
			t.Errorf("bind --all = %d, want 0", code)
		}
	})
	for _, want := range []string{"claude now follows every KB the server mounts (it was bound to alpha)", "run `cartographer sync` to apply"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	cfg := loadClientCfg(t, dir)
	if b := cfg.Clients["claude"]; !b.FollowsServer || len(b.KBs) != 0 || b.NoLineage {
		t.Errorf("binding = %+v", b)
	}

	// The other direction, and the origin shown on the way.
	if list := withStdout(t, func() { cmdClient([]string{"list"}) }); !strings.Contains(list, "all (follows the server)") {
		t.Errorf("list does not show the origin:\n%s", list)
	}
	if show := withStdout(t, func() { cmdClient([]string{"show", "claude"}) }); !strings.Contains(show, "origin     all (follows the server)") {
		t.Errorf("show does not show the origin:\n%s", show)
	}
	withStdout(t, func() { cmdClient([]string{"bind", "claude", "beta"}) })
	if b := loadClientCfg(t, dir).Clients["claude"]; b.FollowsServer || strings.Join(b.KBs, ",") != "beta" {
		t.Errorf("bind after the marker = %+v", b)
	}
	withStdout(t, func() { cmdClient([]string{"bind", "claude", "--all"}) })
	out = withStdout(t, func() { cmdClient([]string{"reset", "claude"}) })
	if !strings.Contains(out, "the all binding is gone") || !strings.Contains(out, "follows the server through the default") {
		t.Errorf("reset output:\n%s", out)
	}
	if loadClientCfg(t, dir).FollowsServer("claude") {
		t.Error("reset kept the marker")
	}
}
