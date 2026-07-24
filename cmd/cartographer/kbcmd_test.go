package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateKBName(t *testing.T) {
	cases := []struct {
		name    string
		wantErr bool
	}{
		{"alpha", false},
		{"alpha-beta", false},
		{"alpha_beta", false},
		{"Alpha123", false},
		{"", true},
		{"alpha/beta", true},
		{"../escape", true},
		{"alpha.beta", true},
		{"alpha beta", true},
		{"alpha:beta", true},
	}
	for _, tc := range cases {
		err := validateKBName(tc.name)
		if tc.wantErr && err == nil {
			t.Errorf("validateKBName(%q) = nil, want error", tc.name)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("validateKBName(%q) = %v, want nil", tc.name, err)
		}
	}
}

// withNoGuidance stubs printPostCreateGuidanceFn to a no-op for the
// duration of f, so cmdKBCreate tests never reach out over the network
// (real ~/.cartographer.yaml / service config on the test machine).
func withNoGuidance(t *testing.T, f func()) {
	t.Helper()
	orig := printPostCreateGuidanceFn
	printPostCreateGuidanceFn = func(bool) {}
	defer func() { printPostCreateGuidanceFn = orig }()
	f()
}

func TestCmdKBCreateScaffold(t *testing.T) {
	dataDir := t.TempDir()

	var code int
	out := withStdout(t, func() {
		withNoGuidance(t, func() {
			code = cmdKBCreate([]string{"alpha", "--data", dataDir})
		})
	})
	if code != 0 {
		t.Fatalf("cmdKBCreate = %d, want 0 (output: %s)", code, out)
	}

	kbPath := filepath.Join(dataDir, "alpha")
	for _, rel := range []string{"data/index.md", "data/log.md", ".git"} {
		if _, err := os.Stat(filepath.Join(kbPath, rel)); err != nil {
			t.Errorf("expected %s to exist: %v", rel, err)
		}
	}
}

func TestCmdKBCreateSecondRunError(t *testing.T) {
	dataDir := t.TempDir()

	withNoGuidance(t, func() {
		if code := cmdKBCreate([]string{"alpha", "--data", dataDir}); code != 0 {
			t.Fatalf("first cmdKBCreate = %d, want 0", code)
		}
	})

	var code int
	withNoGuidance(t, func() {
		code = cmdKBCreate([]string{"alpha", "--data", dataDir})
	})
	if code == 0 {
		t.Fatal("second cmdKBCreate = 0, want non-zero (already exists)")
	}
}

func TestCmdKBCreateInvalidName(t *testing.T) {
	dataDir := t.TempDir()

	var code int
	withNoGuidance(t, func() {
		code = cmdKBCreate([]string{"not/valid", "--data", dataDir})
	})
	if code == 0 {
		t.Fatal("cmdKBCreate with invalid name = 0, want non-zero")
	}
}

func TestRunKBDispatch(t *testing.T) {
	origKBFn := kbFn
	defer func() { kbFn = origKBFn }()
	var gotArgs []string
	kbFn = func(args []string) int {
		gotArgs = args
		return 9
	}

	if code := run([]string{"kb", "create", "alpha"}); code != 9 {
		t.Errorf("run([kb create alpha]) = %d, want 9", code)
	}
	want := []string{"create", "alpha"}
	if len(gotArgs) != len(want) {
		t.Fatalf("kbFn args = %v, want %v", gotArgs, want)
	}
	for i := range want {
		if gotArgs[i] != want[i] {
			t.Errorf("kbFn args[%d] = %q, want %q", i, gotArgs[i], want[i])
		}
	}
}
