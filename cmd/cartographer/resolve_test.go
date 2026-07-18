package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeClientConfig writes a minimal .cartographer.yaml into home so
// clientconfig.Load(home) succeeds — home must already be $HOME for the
// test (see t.Setenv("HOME", home) in each test below), since cmdResolve
// resolves the config dir via clientconfig.TargetDir() (os.UserHomeDir()).
func writeClientConfig(t *testing.T, home, extra string) {
	t.Helper()
	content := "server_url: http://localhost:8080/mcp\nserver_name: cartographer\n" + extra
	if err := os.WriteFile(filepath.Join(home, ".cartographer.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCmdResolve_UsageErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cases := [][]string{
		{},
		{"bogus"},
		{"repo:"},
		{"weird:key"},
		{"a", "b"},
	}
	for _, args := range cases {
		if code := cmdResolve(args); code != 2 {
			t.Errorf("cmdResolve(%v) = %d, want 2", args, code)
		}
	}
}

func TestCmdResolve_PathResolved(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeClientConfig(t, home, "paths:\n  design: /mnt/design\n")

	out := withStdout(t, func() {
		if code := cmdResolve([]string{"path:design"}); code != 0 {
			t.Errorf("cmdResolve = %d, want 0", code)
		}
	})
	if strings.TrimSpace(out) != "/mnt/design" {
		t.Errorf("output = %q, want /mnt/design", out)
	}
}

func TestCmdResolve_PathNotFound(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeClientConfig(t, home, "")

	if code := cmdResolve([]string{"path:missing"}); code != 1 {
		t.Errorf("cmdResolve = %d, want 1", code)
	}
}

func TestCmdResolve_RepoViaManualPathsOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeClientConfig(t, home, "paths:\n  cartographer: /home/x/repos/cartographer\n")

	out := withStdout(t, func() {
		if code := cmdResolve([]string{"repo:cartographer"}); code != 0 {
			t.Errorf("cmdResolve = %d, want 0", code)
		}
	})
	if strings.TrimSpace(out) != "/home/x/repos/cartographer" {
		t.Errorf("output = %q, want /home/x/repos/cartographer", out)
	}
}

func TestCmdResolve_RepoNotFound(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// search_roots defaults to ~/Documents, which doesn't exist in this temp
	// home: Scan finds nothing, Resolve must report a clean not-found error.
	writeClientConfig(t, home, "")

	if code := cmdResolve([]string{"repo:nonexistent"}); code != 1 {
		t.Errorf("cmdResolve = %d, want 1", code)
	}
}

func TestCmdResolve_NoConfigFileStillWorks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// No .cartographer.yaml at all: cmdResolve must fall back to
	// clientconfig.Default() rather than requiring `connect` first.
	if code := cmdResolve([]string{"path:missing"}); code != 1 {
		t.Errorf("cmdResolve = %d, want 1 (clean not-found, not a config error)", code)
	}
}

func TestCmdResolve_PathExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeClientConfig(t, home, "paths:\n  mine: \"~/design\"\n")

	out := withStdout(t, func() {
		if code := cmdResolve([]string{"path:mine"}); code != 0 {
			t.Errorf("cmdResolve = %d, want 0", code)
		}
	})
	want := filepath.Join(home, "design")
	if strings.TrimSpace(out) != want {
		t.Errorf("output = %q, want %q", strings.TrimSpace(out), want)
	}
}

func TestRunDispatchesResolve(t *testing.T) {
	old := resolveFn
	defer func() { resolveFn = old }()

	called := false
	resolveFn = func(args []string) int {
		called = true
		return 0
	}
	if code := run([]string{"resolve", "path:x"}); code != 0 {
		t.Errorf("run(resolve) = %d, want 0", code)
	}
	if !called {
		t.Error("resolveFn was not invoked by run()")
	}
}
