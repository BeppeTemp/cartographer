package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
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
			code = cmdKBCreate([]string{"alpha", "--data", dataDir, "--no-remote"})
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
		if code := cmdKBCreate([]string{"alpha", "--data", dataDir, "--no-remote"}); code != 0 {
			t.Fatalf("first cmdKBCreate = %d, want 0", code)
		}
	})

	var code int
	withNoGuidance(t, func() {
		code = cmdKBCreate([]string{"alpha", "--data", dataDir, "--no-remote"})
	})
	if code == 0 {
		t.Fatal("second cmdKBCreate = 0, want non-zero (already exists)")
	}
}

func TestCmdKBCreateInvalidName(t *testing.T) {
	dataDir := t.TempDir()

	var code int
	withNoGuidance(t, func() {
		code = cmdKBCreate([]string{"not/valid", "--data", dataDir, "--no-remote"})
	})
	if code == 0 {
		t.Fatal("cmdKBCreate with invalid name = 0, want non-zero")
	}
}

// TestCmdKBCreateRequiresRemoteChoice covers D134's gate: neither flag, and
// both flags, are usage errors (exit 2) that create nothing.
func TestCmdKBCreateRequiresRemoteChoice(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no choice", nil},
		{"both", []string{"--remote", "file:///tmp/x.git", "--no-remote"}},
	}
	for _, tc := range cases {
		dataDir := t.TempDir()
		var code int
		withNoGuidance(t, func() {
			code = cmdKBCreate(append([]string{"alpha", "--data", dataDir}, tc.args...))
		})
		if code != 2 {
			t.Errorf("%s: cmdKBCreate = %d, want 2", tc.name, code)
		}
		if _, err := os.Stat(filepath.Join(dataDir, "alpha")); !os.IsNotExist(err) {
			t.Errorf("%s: KB created despite usage error: %v", tc.name, err)
		}
	}
}

// TestCmdKBCreateNoRemoteLeavesNoOrigin pins the opt-out: a KB is scaffolded,
// and it has no origin configured.
func TestCmdKBCreateNoRemoteLeavesNoOrigin(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping KB create remote test")
	}

	dataDir := t.TempDir()
	var code int
	withNoGuidance(t, func() {
		code = cmdKBCreate([]string{"alpha", "--data", dataDir, "--no-remote"})
	})
	if code != 0 {
		t.Fatalf("cmdKBCreate --no-remote = %d, want 0", code)
	}
	if _, err := gitx.RemoteURL(filepath.Join(dataDir, "alpha"), "origin"); err == nil {
		t.Error("--no-remote KB has an origin remote, want none")
	}
}

// TestCmdKBCreateWithRemotePushes checks the happy path end to end: origin is
// attached and the initial commit is reachable from the remote's branch.
func TestCmdKBCreateWithRemotePushes(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping KB create remote test")
	}

	tmp := t.TempDir()
	bare := filepath.Join(tmp, "alpha.git")
	mustRunGit(t, "", "init", "--bare", bare)
	remote := "file://" + bare

	dataDir := filepath.Join(tmp, "data")
	var code int
	withNoGuidance(t, func() {
		code = cmdKBCreate([]string{"alpha", "--data", dataDir, "--remote", remote})
	})
	if code != 0 {
		t.Fatalf("cmdKBCreate --remote = %d, want 0", code)
	}

	kbPath := filepath.Join(dataDir, "alpha")
	got, err := gitx.RemoteURL(kbPath, "origin")
	if err != nil {
		t.Fatalf("origin not configured: %v", err)
	}
	if got != remote {
		t.Errorf("origin = %q, want %q", got, remote)
	}
	local, err := gitx.HeadSHA(kbPath)
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	pushed, err := gitx.HeadSHAAt(bare, gitx.DefaultBranch)
	if err != nil {
		t.Fatalf("HeadSHAAt(bare, %s): %v", gitx.DefaultBranch, err)
	}
	if pushed != local {
		t.Errorf("remote %s = %s, want local HEAD %s", gitx.DefaultBranch, pushed, local)
	}
}

// TestCmdKBCreateRemoteFailureCleansScaffold pins the rollback invariant: an
// unreachable remote must not leave a mountable KB behind.
func TestCmdKBCreateRemoteFailureCleansScaffold(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping KB create remote test")
	}

	dataDir := t.TempDir()
	var code int
	withNoGuidance(t, func() {
		code = cmdKBCreate([]string{"alpha", "--data", dataDir, "--remote", "file:///does-not-exist/missing.git"})
	})
	if code == 0 {
		t.Fatal("cmdKBCreate with unreachable remote = 0, want non-zero")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "alpha")); !os.IsNotExist(err) {
		t.Fatalf("scaffold still exists after failed push: %v", err)
	}
}

func TestCmdKBClone(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping KB clone test")
	}

	tmp := t.TempDir()
	src := filepath.Join(tmp, "source")
	if _, err := kb.Init(src); err != nil {
		t.Fatalf("kb.Init(source): %v", err)
	}
	branchOut, err := os.ReadFile(filepath.Join(src, ".git", "HEAD"))
	if err != nil {
		t.Fatalf("read source HEAD: %v", err)
	}
	branch := string(branchOut[len("ref: refs/heads/"):])
	branch = branch[:len(branch)-1]

	bare := filepath.Join(tmp, "wiki-kb.git")
	mustRunGit(t, "", "init", "--bare", bare)
	mustRunGit(t, src, "remote", "add", "origin", bare)
	mustRunGit(t, src, "push", "origin", branch+":"+branch)
	mustRunGit(t, bare, "symbolic-ref", "HEAD", "refs/heads/"+branch)

	data := filepath.Join(tmp, "data")
	remote := "file://" + bare
	withNoGuidance(t, func() {
		if code := cmdKBClone([]string{remote, "--data", data}); code != 0 {
			t.Fatalf("cmdKBClone = %d, want 0", code)
		}
	})

	mounted := filepath.Join(data, "wiki-kb")
	if _, err := kb.Open(mounted); err != nil {
		t.Fatalf("kb.Open(mounted clone): %v", err)
	}
	for _, rel := range []string{"data/index.md", "data/log.md", ".git"} {
		if _, err := os.Stat(filepath.Join(mounted, rel)); err != nil {
			t.Errorf("mounted clone missing %s: %v", rel, err)
		}
	}
	if code := cmdKBClone([]string{remote, "--data", data}); code == 0 {
		t.Fatal("second cmdKBClone = 0, want already-exists error")
	}
}

func TestCmdKBCloneFailureCleansPartialDestination(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping KB clone test")
	}

	data := filepath.Join(t.TempDir(), "data")
	withNoGuidance(t, func() {
		if code := cmdKBClone([]string{"file:///does-not-exist/missing.git", "--data", data}); code == 0 {
			t.Fatal("cmdKBClone(missing remote) = 0, want error")
		}
	})
	if _, err := os.Stat(filepath.Join(data, "missing")); !os.IsNotExist(err) {
		t.Fatalf("failed clone destination still exists: %v", err)
	}
}

func TestCmdKBCloneRejectsNonOKFRemoteAndCleansDestination(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping KB clone test")
	}

	tmp := t.TempDir()
	src := filepath.Join(tmp, "source")
	mustRunGit(t, "", "init", src)
	mustRunGit(t, src, "config", "user.email", "test@wiki.local")
	mustRunGit(t, src, "config", "user.name", "Wiki Test")
	if err := os.WriteFile(filepath.Join(src, "stray.txt"), []byte("not an OKF KB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, src, "add", "stray.txt")
	mustRunGit(t, src, "commit", "-m", "stray")
	branchOut, err := os.ReadFile(filepath.Join(src, ".git", "HEAD"))
	if err != nil {
		t.Fatalf("read source HEAD: %v", err)
	}
	branch := string(branchOut[len("ref: refs/heads/"):])
	branch = branch[:len(branch)-1]

	bare := filepath.Join(tmp, "non-okf.git")
	mustRunGit(t, "", "init", "--bare", bare)
	mustRunGit(t, src, "remote", "add", "origin", bare)
	mustRunGit(t, src, "push", "origin", branch+":"+branch)
	mustRunGit(t, bare, "symbolic-ref", "HEAD", "refs/heads/"+branch)

	data := filepath.Join(tmp, "data")
	withNoGuidance(t, func() {
		if code := cmdKBClone([]string{"file://" + bare, "--data", data}); code == 0 {
			t.Fatal("cmdKBClone(non-OKF) = 0, want error")
		}
	})
	if _, err := os.Stat(filepath.Join(data, "non-okf")); !os.IsNotExist(err) {
		t.Fatalf("non-OKF clone still exists: %v", err)
	}
}

func TestCmdKBCloneNameDerivation(t *testing.T) {
	cases := []struct {
		remote  string
		want    string
		wantErr bool
	}{
		{"https://example.test/team/wiki.git", "wiki", false},
		{"ssh://git@example.test/team/wiki.git/", "wiki", false},
		{"git@example.test:team/wiki.git", "wiki", false},
		{"https://example.test/team/bad.name.git", "bad.name", true},
	}
	for _, tc := range cases {
		got := remoteKBName(tc.remote)
		if got != tc.want {
			t.Errorf("remoteKBName(%q) = %q, want %q", tc.remote, got, tc.want)
		}
		if err := validateKBName(got); (err != nil) != tc.wantErr {
			t.Errorf("validateKBName(remoteKBName(%q)) error = %v, wantErr %v", tc.remote, err, tc.wantErr)
		}
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
