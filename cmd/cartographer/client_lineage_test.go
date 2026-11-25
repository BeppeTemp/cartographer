package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

func providerLineageHome(t *testing.T) string {
	t.Helper()
	home, _ := lineageHome(t)
	return home
}

func loadCfg(t *testing.T, home string) *clientconfig.Config {
	t.Helper()
	cfg, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// AC-lineage-26
func TestClientBindLineage(t *testing.T) {
	home := providerLineageHome(t)
	var code int
	withStdout(t, func() { code = cmdClient([]string{"bind", "codex", "client-kb", "--no-lineage"}) })
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	cfg := loadCfg(t, home)
	if b := cfg.Clients["codex"]; !b.NoLineage || !slices.Equal(b.KBs, []string{"client-kb"}) {
		t.Fatalf("binding = %+v", b)
	}
	out := withStdout(t, func() { cmdClient([]string{"show", "codex"}) })
	if !strings.Contains(out, "[no lineage]") || !strings.Contains(out, "client bind codex client-kb --no-lineage") {
		t.Errorf("show without lineage:\n%s", out)
	}

	withStdout(t, func() { code = cmdClient([]string{"bind", "codex", "client-kb", "--exclude", "core-kb"}) })
	if code != 0 {
		t.Fatalf("exclude: exit %d", code)
	}
	if b := loadCfg(t, home).Clients["codex"]; b.NoLineage || !slices.Equal(b.Exclude, []string{"core-kb"}) {
		t.Fatalf("exclude replaces the opt-out: %+v", b)
	}

	// Bound and excluded at once is a usage error, and so is --no-lineage with --exclude.
	for _, args := range [][]string{
		{"bind", "codex", "core-kb", "--exclude", "core-kb"},
		{"bind", "codex", "client-kb", "--no-lineage", "--exclude", "core-kb"},
		{"bind", "codex", "client-kb", "--exclude"},
	} {
		withStderr(t, func() { code = cmdClient(args) })
		if code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}

	// A bind replaces the options whole: without a flag they go off, and the
	// KB the old exclude named can be bound by the same command.
	withStdout(t, func() { code = cmdClient([]string{"bind", "codex", "core-kb"}) })
	if b := loadCfg(t, home).Clients["codex"]; code != 0 || len(b.Exclude) != 0 || b.NoLineage || !slices.Contains(b.KBs, "core-kb") {
		t.Errorf("rebind without flags: exit %d, %+v", code, b)
	}
}

// The first bind of a provider with no explicit binding holds `kbs` alone,
// not the default every-known-KB list: excluding a known KB is allowed.
func TestClientBindFirstBindExclude(t *testing.T) {
	home := providerLineageHome(t)
	var code int
	withStdout(t, func() { code = cmdClient([]string{"bind", "codex", "client-kb", "--exclude", "core-kb"}) })
	if b := loadCfg(t, home).Clients["codex"]; code != 0 || !slices.Equal(b.Exclude, []string{"core-kb"}) {
		t.Fatalf("exit %d, %+v", code, b)
	}
}

// A stored exclude naming a KB must not block the bind that replaces it.
func TestClientBindOfExcludedKBWithNewOptions(t *testing.T) {
	home := providerLineageHome(t)
	var code int
	withStdout(t, func() { code = cmdClient([]string{"bind", "codex", "client-kb", "--exclude", "core-kb"}) })
	for _, args := range [][]string{
		{"bind", "codex", "core-kb", "--exclude", "other-kb"},
		{"bind", "codex", "core-kb", "--no-lineage"},
	} {
		withStdout(t, func() { code = cmdClient(args) })
		b := loadCfg(t, home).Clients["codex"]
		if code != 0 || !slices.Contains(b.KBs, "core-kb") || slices.Contains(b.Exclude, "core-kb") {
			t.Fatalf("%v: exit %d, %+v", args, code, b)
		}
		withStdout(t, func() { cmdClient([]string{"unbind", "codex", "core-kb"}) })
		withStdout(t, func() { code = cmdClient([]string{"bind", "codex", "client-kb", "--exclude", "core-kb"}) })
	}
}

// AC-lineage-25 (binding half): the stored options replace whole.
func TestClientBindRefusesAnExcludedKB(t *testing.T) {
	cfg := clientconfig.Default()
	if err := cfg.Bind("codex", "client-kb"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetProviderLineage("codex", false, []string{"core-kb"}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Bind("codex", "core-kb"); err == nil {
		t.Fatal("Bind of an excluded KB must be refused")
	}
	if b := cfg.Clients["codex"]; slices.Contains(b.KBs, "core-kb") {
		t.Fatalf("binding changed: %+v", b)
	}
}

func TestRenameResolvesBoundAndExcluded(t *testing.T) {
	kbs, ex := renameInBinding([]string{"kb-a", "kb-b"}, []string{"core-kb", "kb-x"}, "kb-x", "kb-b")
	if !slices.Equal(kbs, []string{"kb-a", "kb-b"}) || !slices.Equal(ex, []string{"core-kb"}) {
		t.Fatalf("got %v / %v", kbs, ex)
	}
	kbs, ex = renameInBinding([]string{"kb-x"}, []string{"core-kb"}, "kb-x", "kb-n")
	if !slices.Equal(kbs, []string{"kb-n"}) || !slices.Equal(ex, []string{"core-kb"}) {
		t.Fatalf("plain rename: %v / %v", kbs, ex)
	}
}

// The rename rewrites the binding in place: its lineage options are carried
// to the new name, not dropped.
func TestRenameRewriteBindingsKeepsLineageOptions(t *testing.T) {
	home := providerLineageHome(t)
	cfg := loadCfg(t, home)
	if err := cfg.Bind("codex", "client-kb"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetProviderLineage("codex", false, []string{"core-kb"}); err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	if err := rewriteBindings("client-kb", "client-new"); err != nil {
		t.Fatal(err)
	}
	b := loadCfg(t, home).Clients["codex"]
	if !slices.Equal(b.KBs, []string{"client-new"}) || !slices.Equal(b.Exclude, []string{"core-kb"}) {
		t.Fatalf("binding = %+v", b)
	}
}
