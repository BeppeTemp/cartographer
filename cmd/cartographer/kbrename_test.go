package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// seedRenameKB creates a data dir holding one valid KB called name.
func seedRenameKB(t *testing.T, name string) string {
	t.Helper()
	dataDir := t.TempDir()
	if _, err := kb.Init(filepath.Join(dataDir, name)); err != nil {
		t.Fatal(err)
	}
	return dataDir
}

func writeServerConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "server.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestPlanKBRename_Refusals: every refusal happens before anything moves, and
// names what to fix.
func TestPlanKBRename_Refusals(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	dataDir := seedRenameKB(t, "wiki")
	if err := os.MkdirAll(filepath.Join(dataDir, "taken"), 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		old, newer string
		wantErr    string
	}{
		{"missing source", "absent", "other", "not an OKF KB"},
		{"destination exists", "wiki", "taken", "already exists"},
		{"invalid new name", "wiki", "bad/name", "invalid KB name"},
		{"same name", "wiki", "wiki", "already called that"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := planKBRename(tc.old, tc.newer, dataDir, filepath.Join(dataDir, "no-config.yaml"))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}

	// A source that is a directory but not a KB is refused too: renaming it
	// would move something this command cannot vouch for.
	if err := os.MkdirAll(filepath.Join(dataDir, "plain"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := planKBRename("plain", "renamed", dataDir, filepath.Join(dataDir, "no-config.yaml")); err == nil {
		t.Error("a non-KB directory should not be renamable")
	}
}

// TestPlanKBRename_AmbiguousConfig: two entries could be the KB, so the
// command refuses rather than guessing which one to detach from its config.
func TestPlanKBRename_AmbiguousConfig(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	dataDir := seedRenameKB(t, "wiki")
	cfgPath := writeServerConfig(t, "data: "+dataDir+"\nkbs:\n"+
		"  - path: "+filepath.Join(dataDir, "wiki")+"\n"+
		"  - path: /elsewhere/wiki\n    name: wiki\n")

	_, err := planKBRename("wiki", "atlas", dataDir, cfgPath)
	if err == nil || !strings.Contains(err.Error(), "matches 2 entries") {
		t.Fatalf("err = %v, want an ambiguity refusal", err)
	}
	// Nothing moved.
	if _, statErr := os.Stat(filepath.Join(dataDir, "wiki")); statErr != nil {
		t.Errorf("the preflight moved the KB: %v", statErr)
	}
}

// TestCmdKBRename_HappyPath covers the two shapes: a KB with a kbs[] entry,
// and one mounted by discovery (created by `kb create`, no entry at all).
func TestCmdKBRename_HappyPath(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	t.Run("with a kbs entry", func(t *testing.T) {
		withClientServerURL(t, "")
		dataDir := seedRenameKB(t, "wiki")
		cfgPath := writeServerConfig(t, "# operator's own note\nhttp: \"127.0.0.1:39273\"\ndata: "+dataDir+"\n"+
			"kbs:\n  - path: "+filepath.Join(dataDir, "wiki")+"\n    tool_prefix: wk\n")

		var code int
		out := withStdout(t, func() {
			withNoGuidance(t, func() {
				code = cmdKBRename([]string{"wiki", "atlas", "--data", dataDir, "--config", cfgPath})
			})
		})
		if code != 0 {
			t.Fatalf("exit = %d, want 0:\n%s", code, out)
		}
		if _, err := os.Stat(filepath.Join(dataDir, "atlas", "data", "index.md")); err != nil {
			t.Errorf("the KB is not at its new path: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dataDir, "wiki")); !os.IsNotExist(err) {
			t.Error("the old directory survived")
		}
		body, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		got := string(body)
		if !strings.Contains(got, filepath.Join(dataDir, "atlas")) {
			t.Errorf("the config still points at the old path:\n%s", got)
		}
		// An explicit tool_prefix is preserved verbatim, and so are the
		// operator's comments: a rename must not reformat a hand-written file.
		if !strings.Contains(got, "tool_prefix: wk") {
			t.Errorf("explicit tool_prefix was not preserved:\n%s", got)
		}
		if !strings.Contains(got, "operator's own note") {
			t.Errorf("the operator's comment was dropped:\n%s", got)
		}
	})

	t.Run("mounted by discovery", func(t *testing.T) {
		withClientServerURL(t, "")
		dataDir := seedRenameKB(t, "wiki")
		cfgPath := writeServerConfig(t, "data: "+dataDir+"\n")

		var code int
		out := withStdout(t, func() {
			withNoGuidance(t, func() {
				code = cmdKBRename([]string{"wiki", "atlas", "--data", dataDir, "--config", cfgPath})
			})
		})
		if code != 0 {
			t.Fatalf("exit = %d, want 0:\n%s", code, out)
		}
		if !strings.Contains(out, "no kbs[] entry") {
			t.Errorf("a discovered KB should be reported as such:\n%s", out)
		}
		if _, err := os.Stat(filepath.Join(dataDir, "atlas")); err != nil {
			t.Errorf("the KB was not renamed: %v", err)
		}
	})
}

// TestPlanKBRename_ReportsWhatItDoesNotMigrate: a derived tool prefix renames
// every tool the agents see, and auth scopes are not rewritten by anyone.
func TestPlanKBRename_ReportsWhatItDoesNotMigrate(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	withClientServerURL(t, "")
	dataDir := seedRenameKB(t, "wiki")
	cfgPath := writeServerConfig(t, "data: "+dataDir+"\n"+
		"mcp:\n  tool_prefix_mode: kb-name\n"+
		"auth:\n  mode: \"on\"\n  tokens:\n    - token: s3cret\n      id: ops\n      scopes: [\"kb:wiki:rw\"]\n"+
		"kbs:\n  - path: "+filepath.Join(dataDir, "wiki")+"\n")

	p, err := planKBRename("wiki", "atlas", dataDir, cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !p.DerivedPrefix || p.OldPrefix == "" || p.NewPrefix == "" {
		t.Errorf("a derived prefix should be announced: %+v", p)
	}
	if len(p.Scopes) != 1 || !strings.Contains(p.Scopes[0], "ops") {
		t.Errorf("the scoped token should be reported: %v", p.Scopes)
	}

	var sb strings.Builder
	printRenamePreflight(&sb, p)
	out := sb.String()
	for _, want := range []string{"WARNING", "kb:wiki:rw", "NOT migrated"} {
		if !strings.Contains(out, want) {
			t.Errorf("preflight is missing %q:\n%s", want, out)
		}
	}
	// The preflight wrote nothing.
	if _, err := os.Stat(filepath.Join(dataDir, "wiki")); err != nil {
		t.Errorf("the preflight moved the KB: %v", err)
	}
}

// TestApplyKBRename_RollsBackOnConfigFailure: directory and config change
// together or not at all. An unwritable config leaves the KB under its
// original name.
func TestApplyKBRename_RollsBackOnConfigFailure(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	dataDir := seedRenameKB(t, "wiki")
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "server.yaml")
	if err := os.WriteFile(cfgPath, []byte("kbs:\n  - path: "+filepath.Join(dataDir, "wiki")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores the file mode this test relies on")
	}
	if err := os.Chmod(cfgPath, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(cfgPath, 0o600) })

	p := &renamePlan{
		Old: "wiki", New: "atlas",
		OldPath:    filepath.Join(dataDir, "wiki"),
		NewPath:    filepath.Join(dataDir, "atlas"),
		ConfigPath: cfgPath,
		EntryIndex: 0,
	}
	if err := applyKBRename(p); err == nil {
		t.Fatal("a config that cannot be written should fail the rename")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "wiki", "data", "index.md")); err != nil {
		t.Errorf("the directory was not renamed back: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "atlas")); !os.IsNotExist(err) {
		t.Error("the new directory survived a rolled-back rename")
	}
}
