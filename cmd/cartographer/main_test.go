package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestDiscoverKBPaths(t *testing.T) {
	base := t.TempDir()

	// Create subdirs: two regular, one dotfile, one file (not dir).
	dirs := []string{"alpha", "beta"}
	for _, d := range dirs {
		if err := os.Mkdir(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// dotfile dir — must be excluded
	if err := os.Mkdir(filepath.Join(base, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	// regular file — must be excluded
	if err := os.WriteFile(filepath.Join(base, "readme.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := discoverKBPaths(base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sort.Strings(got)
	want := []string{
		filepath.Join(base, "alpha"),
		filepath.Join(base, "beta"),
	}
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("got %d paths, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("path[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

// TestDiscoverKBPathsMissingDirCreated covers the D83 fix: a missing data
// dir must not fail server startup (launchd install races data dir
// creation with the first serve, and a removed data dir must not
// crash-loop the service). It is created and treated as empty.
func TestDiscoverKBPathsMissingDirCreated(t *testing.T) {
	base := filepath.Join(t.TempDir(), "does-not-exist-yet")

	got, err := discoverKBPaths(base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty result, got %v", got)
	}
	if info, statErr := os.Stat(base); statErr != nil || !info.IsDir() {
		t.Fatalf("expected data dir to be created at %q, stat: %v", base, statErr)
	}
}

func TestDiscoverKBPathsEmpty(t *testing.T) {
	base := t.TempDir()
	got, err := discoverKBPaths(base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty result, got %v", got)
	}
}

// withStdout redirects os.Stdout for the duration of f and returns what was written.
func withStdout(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	f()
	w.Close()
	os.Stdout = old

	buf := make([]byte, 64*1024)
	n, _ := r.Read(buf)
	return string(buf[:n])
}

func TestRunNoArgsPrintsUsage(t *testing.T) {
	out := withStdout(t, func() {
		if code := run(nil); code != 0 {
			t.Errorf("run(nil) = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "Usage:") {
		t.Errorf("run(nil) output = %q, want it to contain \"Usage:\"", out)
	}
}

func TestRunHelp(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		out := withStdout(t, func() {
			if code := run(args); code != 0 {
				t.Errorf("run(%v) = %d, want 0", args, code)
			}
		})
		if !strings.Contains(out, "serve") {
			t.Errorf("run(%v) output = %q, want it to list \"serve\"", args, out)
		}
	}
}

func TestRunVersionDispatch(t *testing.T) {
	origVersionFn := versionFn
	defer func() { versionFn = origVersionFn }()
	called := false
	versionFn = func() int {
		called = true
		return 7
	}

	if code := run([]string{"version"}); code != 7 {
		t.Errorf("run([version]) = %d, want 7", code)
	}
	if !called {
		t.Error("versionFn was not called")
	}
}

func TestRunServeDispatch(t *testing.T) {
	origServeFn := serveFn
	defer func() { serveFn = origServeFn }()
	var gotArgs []string
	serveFn = func(args []string) int {
		gotArgs = args
		return 42
	}

	if code := run([]string{"serve", "--http", ":9090"}); code != 42 {
		t.Errorf("run([serve ...]) = %d, want 42", code)
	}
	want := []string{"--http", ":9090"}
	if len(gotArgs) != len(want) {
		t.Fatalf("serveFn args = %v, want %v", gotArgs, want)
	}
	for i := range want {
		if gotArgs[i] != want[i] {
			t.Errorf("serveFn args[%d] = %q, want %q", i, gotArgs[i], want[i])
		}
	}
}

func TestRunClientCommandsDispatch(t *testing.T) {
	origAgents, origConnect, origDisconnect, origStatus, origSync, origService := agentsFn, connectFn, disconnectFn, statusFn, syncFn, serviceFn
	defer func() {
		agentsFn, connectFn, disconnectFn, statusFn, syncFn, serviceFn = origAgents, origConnect, origDisconnect, origStatus, origSync, origService
	}()

	cases := []struct {
		cmd string
		set func(fn func([]string) int)
	}{
		{"agents", func(fn func([]string) int) { agentsFn = fn }},
		{"connect", func(fn func([]string) int) { connectFn = fn }},
		{"disconnect", func(fn func([]string) int) { disconnectFn = fn }},
		{"status", func(fn func([]string) int) { statusFn = fn }},
		{"sync", func(fn func([]string) int) { syncFn = fn }},
		{"service", func(fn func([]string) int) { serviceFn = fn }},
	}
	for _, tc := range cases {
		var gotArgs []string
		tc.set(func(args []string) int {
			gotArgs = args
			return 5
		})
		if code := run([]string{tc.cmd, "--foo"}); code != 5 {
			t.Errorf("run([%s ...]) = %d, want 5", tc.cmd, code)
		}
		if len(gotArgs) != 1 || gotArgs[0] != "--foo" {
			t.Errorf("%s: args = %v, want [--foo]", tc.cmd, gotArgs)
		}
	}
}

func TestRunUnknownCommand(t *testing.T) {
	if code := run([]string{"bogus"}); code != 2 {
		t.Errorf("run([bogus]) = %d, want 2", code)
	}
}

func TestRunUnknownFlagAtRoot(t *testing.T) {
	if code := run([]string{"--kb", "/some/path"}); code != 2 {
		t.Errorf("run([--kb ...]) = %d, want 2", code)
	}
}
