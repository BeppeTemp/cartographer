package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// hasGitBinary skips the tests that shell out to git where it is absent.
func hasGitBinary() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

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
	printPostCreateGuidanceFn = func(string, bool) {}
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
// A failed push must not delete the scaffold: the local KB is complete and
// valid, only the push failed, and re-doing it by hand was the reported cost
// (D156). The command still exits non-zero and prints how to finish.
func TestCmdKBCreateRemoteFailureKeepsScaffold(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping KB create remote test")
	}

	dataDir := t.TempDir()
	var code int
	out := withStderr(t, func() {
		withNoGuidance(t, func() {
			code = cmdKBCreate([]string{"alpha", "--data", dataDir, "--remote", "file:///does-not-exist/missing.git"})
		})
	})
	if code == 0 {
		t.Fatal("cmdKBCreate with unreachable remote = 0, want non-zero")
	}
	scaffold := filepath.Join(dataDir, "alpha")
	if _, err := os.Stat(scaffold); err != nil {
		t.Fatalf("scaffold was removed after a failed push: %v", err)
	}
	// It must be a real KB, not a half-written directory.
	if _, err := kb.Open(scaffold); err != nil {
		t.Fatalf("kept scaffold is not a valid KB: %v", err)
	}
	for _, want := range []string{"only the push failed", "push -u origin", "Or remove it"} {
		if !strings.Contains(out, want) {
			t.Errorf("guidance missing %q\n---\n%s", want, out)
		}
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

// withClientServerURL puts a .cartographer.yaml with the given server_url in
// a temporary HOME, which is what clientconfig.TargetDir resolves to. An
// empty serverURL means "no client config on this machine".
func withClientServerURL(t *testing.T, serverURL string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if serverURL == "" {
		return
	}
	body := "schema_version: 1\nserver_url: " + serverURL + "\n"
	if err := os.WriteFile(filepath.Join(home, clientconfig.FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCheckLocalTarget: `kb create`/`kb clone` act on the LOCAL server's data
// dir. On a machine whose client points elsewhere, reporting "mounted" about a
// directory nothing reads is worse than failing, so it fails (D173).
func TestCheckLocalTarget(t *testing.T) {
	cases := []struct {
		name      string
		serverURL string
		dataFlag  string
		local     bool
		want      int
	}{
		{"client points at a remote server", "https://cartographer.example.com/mcp", "", false, 2},
		{"remote server with the explicit opt-out", "https://cartographer.example.com/mcp", "", true, 0},
		{"remote server but --data names the target", "https://cartographer.example.com/mcp", "/tmp/kbs", false, 0},
		{"loopback client", "http://127.0.0.1:39273/mcp", "", false, 0},
		{"no client config at all", "", "", false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withClientServerURL(t, tc.serverURL)
			var code int
			out := withStderr(t, func() { code = checkLocalTarget(tc.dataFlag, tc.local) })
			if code != tc.want {
				t.Fatalf("checkLocalTarget = %d, want %d (stderr: %s)", code, tc.want, out)
			}
			if tc.want != 0 && !strings.Contains(out, "--local") {
				t.Errorf("the error must name its opt-out: %s", out)
			}
		})
	}
}

// TestResolveServerDataDir_CustomConfig: a service installed with `--config
// <path>` was invisible to the resolver, which read the standard path only —
// so `kb create` silently used ~/cartographer-data instead of the directory
// the running server serves.
func TestResolveServerDataDir_CustomConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "custom.yaml")
	dataDir := filepath.Join(dir, "kbs")
	if err := os.WriteFile(cfgPath, []byte("data: "+dataDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolveServerDataDir(cfgPath); got != dataDir {
		t.Errorf("resolveServerDataDir(%q) = %q, want %q", cfgPath, got, dataDir)
	}
	// An unreadable path falls back to the default rather than failing: the
	// caller can still pass --data.
	if got := resolveServerDataDir(filepath.Join(dir, "absent.yaml")); got != defaultDataDir() {
		t.Errorf("missing config = %q, want the default data dir", got)
	}
}

// seedKBListDir builds a data dir with one valid KB, one git repo that is not
// a KB, and one plain directory.
func seedKBListDir(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	if _, err := kb.Init(filepath.Join(dataDir, "wiki")); err != nil {
		t.Fatal(err)
	}
	plainRepo := filepath.Join(dataDir, "notes")
	if err := os.MkdirAll(plainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := gitx.Init(plainRepo); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dataDir
}

func TestScanDataDir(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	dataDir := seedKBListDir(t)
	rows, err := scanDataDir(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]kbRow{}
	for _, r := range rows {
		got[r.Name] = r
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3: %+v", len(rows), rows)
	}
	if !got["wiki"].IsKB || !got["wiki"].IsRepo {
		t.Errorf("wiki should be a git repo and an OKF KB: %+v", got["wiki"])
	}
	if got["notes"].IsKB || !got["notes"].IsRepo {
		t.Errorf("notes is a repo but not a KB: %+v", got["notes"])
	}
	if got["scratch"].IsKB || got["scratch"].IsRepo {
		t.Errorf("scratch is neither: %+v", got["scratch"])
	}

	// A missing data dir is reported, never created: `kb list` writes nothing.
	absent := filepath.Join(t.TempDir(), "nope")
	if _, err := scanDataDir(absent); err == nil {
		t.Error("a missing data dir should be an error")
	}
	if _, err := os.Stat(absent); err == nil {
		t.Error("kb list must not create the data dir")
	}
}

// TestCmdKBList_IsReadOnly is the point of the command's design: kb.Open
// self-migrates the local git-exclude entry, so a listing that used it would
// write into every repository it scanned.
func TestCmdKBList_IsReadOnly(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	dataDir := seedKBListDir(t)
	before := treeFingerprint(t, dataDir)

	var code int
	out := withStdout(t, func() { code = cmdKBList([]string{"--data", dataDir}) })
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, want := range []string{"wiki", "notes", "scratch", "data dir:"} {
		if !strings.Contains(out, want) {
			t.Errorf("listing is missing %q:\n%s", want, out)
		}
	}
	if after := treeFingerprint(t, dataDir); after != before {
		t.Errorf("kb list modified the scanned tree:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// treeFingerprint records every file path, size and mtime under root,
// including .git internals: the point is to catch a write nobody intended.
func treeFingerprint(t *testing.T, root string) string {
	t.Helper()
	var sb strings.Builder
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(&sb, "%s %d %v\n", path, info.Size(), info.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return sb.String()
}

// TestPrintKBRows_UnreachableServer: absence of the signal is not evidence
// that nothing is mounted, so the column disappears and the reason is stated.
func TestPrintKBRows_UnreachableServer(t *testing.T) {
	var sb strings.Builder
	rows := []kbRow{{Name: "wiki", IsRepo: true, IsKB: true, Origin: "git@forge:team/wiki.git"}}
	printKBRows(&sb, "/data", rows, errors.New("connection refused"))
	out := sb.String()
	if strings.Contains(out, "MOUNTED") {
		t.Errorf("no MOUNTED column when the server could not be asked:\n%s", out)
	}
	if !strings.Contains(out, "could not be asked") {
		t.Errorf("the reason must be stated:\n%s", out)
	}

	sb.Reset()
	yes := true
	rows[0].Mounted = &yes
	printKBRows(&sb, "/data", rows, nil)
	if out := sb.String(); !strings.Contains(out, "MOUNTED") {
		t.Errorf("MOUNTED column expected when the server answered:\n%s", out)
	}
}

// TestCmdKBClone_Cleanup: a failed clone leaves nothing behind, and a
// pre-existing directory is never removed — the cleanup targets only what
// this command created.
func TestCmdKBClone_Cleanup(t *testing.T) {
	if !hasGitBinary() {
		t.Skip("git not available")
	}
	withClientServerURL(t, "")
	dataDir := t.TempDir()

	// A clone that fails: the partial directory must not survive.
	var code int
	withStderr(t, func() {
		withNoGuidance(t, func() {
			code = cmdKBClone([]string{filepath.Join(dataDir, "no-such-repo.git"), "alpha", "--data", dataDir, "--timeout", "30s"})
		})
	})
	if code == 0 {
		t.Fatal("cloning a non-existent remote should fail")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "alpha")); !os.IsNotExist(err) {
		t.Errorf("a failed clone left %s behind", filepath.Join(dataDir, "alpha"))
	}

	// A pre-existing directory is refused, and left exactly as it was.
	existing := filepath.Join(dataDir, "beta")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(existing, "keep.txt")
	if err := os.WriteFile(marker, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withStderr(t, func() {
		withNoGuidance(t, func() {
			code = cmdKBClone([]string{"https://forge.invalid/team/beta.git", "beta", "--data", dataDir})
		})
	})
	if code != 1 {
		t.Errorf("exit = %d, want 1 for an existing destination", code)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("a pre-existing directory was removed: %v", err)
	}
}
