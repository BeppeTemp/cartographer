package main

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
)

func newPositionalFlagSet() (*flag.FlagSet, *string, *bool) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	kb := fs.String("kb", "", "")
	dry := fs.Bool("dry-run", false, "")
	return fs, kb, dry
}

// #1266: the positional is honoured wherever it stands among the flags.
func TestParseFlagsAnywhere_Shapes(t *testing.T) {
	for name, tc := range map[string]struct {
		args    []string
		pos     []string
		kb      string
		dry     bool
		bindOne string
		code    int
	}{
		"leading":  {args: []string{"claude", "--kb", "all", "--dry-run"}, pos: []string{"claude"}, kb: "all", dry: true, bindOne: "claude"},
		"trailing": {args: []string{"--kb", "all", "--dry-run", "claude"}, pos: []string{"claude"}, kb: "all", dry: true, bindOne: "claude"},
		"between":  {args: []string{"--kb", "all", "claude", "--dry-run"}, pos: []string{"claude"}, kb: "all", dry: true, bindOne: "claude"},
		"none":     {args: []string{"--kb", "x"}, kb: "x"},
		"two":      {args: []string{"claude", "--kb", "all", "codex"}, pos: []string{"claude", "codex"}, kb: "all", bindOne: "claude", code: 2},
		"after --": {args: []string{"--kb", "all", "--", "claude"}, pos: []string{"claude"}, kb: "all", bindOne: "claude"},
	} {
		t.Run(name, func(t *testing.T) {
			fs, kb, dry := newPositionalFlagSet()
			pos := parseFlagsAnywhere(fs, tc.args)
			if !reflect.DeepEqual(pos, tc.pos) {
				t.Fatalf("positionals = %v, want %v", pos, tc.pos)
			}
			if *kb != tc.kb || *dry != tc.dry {
				t.Errorf("flags kb=%q dry=%v, want kb=%q dry=%v", *kb, *dry, tc.kb, tc.dry)
			}
			var target string
			code := bindPositionals("t", pos, &target)
			if code != tc.code || target != tc.bindOne {
				t.Errorf("bind = (%d, %q), want (%d, %q)", code, target, tc.code, tc.bindOne)
			}
		})
	}
}

// #1266: two positionals are a usage error for the subcommands that took one.
func TestSubcommands_ExtraPositionalIsUsageError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for name, run := range map[string]func() int{
		"connect":    func() int { return cmdConnect([]string{"claude", "--no-input", "--dry-run", "codex"}) },
		"disconnect": func() int { return cmdDisconnect([]string{"--dry-run", "claude", "codex"}) },
		"reconnect":  func() int { return cmdReconnect([]string{"claude", "--dry-run", "codex"}) },
		"kb repair":  func() int { return cmdKBRepair([]string{"kb-a", "--apply", "kb-b"}) },
		"service":    func() int { return cmdServiceStart([]string{"extra"}) },
	} {
		if code := run(); code != 2 {
			t.Errorf("%s with an extra positional exited %d, want 2", name, code)
		}
	}
}

// #1267: a codex block that lacks a name's table is not announced as removing it.
func TestRemoveMCPEntries_CodexListsOnlyEntriesPresent(t *testing.T) {
	dir := t.TempDir()
	scfg := &configurator.ServerConfig{Name: "wiki", URL: "http://localhost:8080/mcp"}
	r, err := configurator.Emit(scfg, configurator.ProviderCodex)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configurator.Apply([]*configurator.EmitResult{r}, dir, false); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, ".codex", "config.toml")
	before, _ := os.ReadFile(cfgPath)

	names, removed, err := removeMCPEntries("wiki", []string{"kb-a", "kb-b"}, []string{"codex"}, dir, nil, false, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := names["codex"]; !reflect.DeepEqual(got, []string{"wiki"}) {
		t.Errorf("dry run listed %v, want only the entry that exists: [wiki]", got)
	}
	if !removed["codex"] {
		t.Error("the block exists, so the provider still counts as having something removed")
	}
	if after, _ := os.ReadFile(cfgPath); string(after) != string(before) {
		t.Error("a dry run changed the codex config")
	}

	// Nothing of ours at all: no removal line, whatever KBs are known.
	empty := t.TempDir()
	names, _, err = removeMCPEntries("wiki", []string{"kb-a"}, []string{"codex"}, empty, nil, false, "", true)
	if err != nil || len(names["codex"]) != 0 {
		t.Errorf("empty home: names=%v err=%v", names, err)
	}
}

// #1267 and #1268: connect codex, sync, then a dry run lists neither a removal
// of an entry that is not there nor a rewrite that would change nothing; the
// first dry run after a fresh connect lists the files the real run writes.
func TestSyncDryRun_IsTheCompletePlanAndNamesNoPhantomRemoval(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := prefixAwareMCPServer(t, map[string]string{"one": "", "two": ""}, "sync_pull", func(kb string) string {
		return syncPullPayload(t, kb, "skill-"+kb)
	})
	defer srv.Close()

	dir := t.TempDir()
	cfg := &clientconfig.Config{
		ServerURL: srv.URL + "/mcp", ServerName: "cartographer",
		Agents: []string{"codex"}, KnownKBs: []string{"one", "two"},
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	derived, _ := clientconfig.DerivedPath()

	fresh := withStdout(t, func() {
		if _, err := runSync(dir, cfg, syncOptions{DryRun: true}); err != nil {
			t.Fatalf("dry-run: %v", err)
		}
	})
	if !strings.Contains(fresh, "would write "+derived) {
		t.Errorf("a fresh dry run must name the derived file %s:\n%s", derived, fresh)
	}
	if _, err := os.Stat(derived); !os.IsNotExist(err) {
		t.Error("the dry run wrote the derived file")
	}

	withStdout(t, func() {
		if _, err := runSync(dir, cfg, syncOptions{}); err != nil {
			t.Fatalf("sync: %v", err)
		}
	})
	if _, err := os.Stat(derived); err != nil {
		t.Fatalf("the real sync did not write the derived file: %v", err)
	}

	again := withStdout(t, func() {
		if _, err := runSync(dir, cfg, syncOptions{DryRun: true}); err != nil {
			t.Fatalf("dry-run after sync: %v", err)
		}
	})
	for _, bad := range []string{"would remove MCP entry", "would rewrite", "would write " + derived} {
		if strings.Contains(again, bad) {
			t.Errorf("a dry run after a real sync must not say %q:\n%s", bad, again)
		}
	}
}

// #1268: the .cartographer.yaml rewrite is named when its bytes would change.
func TestSyncDryRun_NamesTheConfigRewrite(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := prefixAwareMCPServer(t, map[string]string{"one": ""}, "sync_pull", func(kb string) string {
		return syncPullPayload(t, kb, "skill-"+kb)
	})
	defer srv.Close()
	dir := t.TempDir()
	// A hand-written file the schema comments would reformat.
	if err := os.WriteFile(clientconfig.Path(dir), []byte("server_url: "+srv.URL+"/mcp\nserver_name: cartographer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Agents = []string{"claude"}
	out := withStdout(t, func() {
		if _, err := runSync(dir, cfg, syncOptions{DryRun: true}); err != nil {
			t.Fatalf("dry-run: %v", err)
		}
	})
	if !strings.Contains(out, "would rewrite "+clientconfig.Path(dir)) {
		t.Errorf("dry run must name the .cartographer.yaml rewrite:\n%s", out)
	}
}
