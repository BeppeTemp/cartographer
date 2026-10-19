package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

func hasGit() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func mustRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	fullArgs := args
	if dir != "" {
		fullArgs = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command("git", fullArgs...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out.String())
	}
}

// TestEnsureClonedKBFromLocalRemote clones a minimal KB from a local bare
// git repository (file:// remote) and verifies it opens as a valid KB.
func TestEnsureClonedKBFromLocalRemote(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping bootstrap clone test")
	}

	tmp := t.TempDir()

	// Source working KB, committed by kb.Init.
	srcDir := filepath.Join(tmp, "src-kb")
	if _, err := kb.Init(srcDir); err != nil {
		t.Fatalf("kb.Init(src): %v", err)
	}

	branchOut, err := exec.Command("git", "-C", srcDir, "symbolic-ref", "--short", "HEAD").Output()
	if err != nil {
		t.Fatalf("git symbolic-ref: %v", err)
	}
	branch := strings.TrimSpace(string(branchOut))

	// Bare remote the source KB is pushed to.
	bareDir := filepath.Join(tmp, "wiki-kb.git")
	mustRunGit(t, "", "init", "--bare", bareDir)
	mustRunGit(t, srcDir, "remote", "add", "origin", bareDir)
	mustRunGit(t, srcDir, "push", "origin", branch+":"+branch)
	// Ensure the bare repo's HEAD points at the pushed branch, so a clone
	// checks out a working tree (a fresh bare repo's HEAD may default to a
	// branch name that was never pushed).
	mustRunGit(t, bareDir, "symbolic-ref", "HEAD", "refs/heads/"+branch)

	dataDir := filepath.Join(tmp, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}

	remoteURL := fileURL(bareDir)
	name := remoteKBName(remoteURL)
	dest, err := ensureClonedKB(remoteURL, name, dataDir)
	if err != nil {
		t.Fatalf("ensureClonedKB: %v", err)
	}
	wantDest := filepath.Join(dataDir, "wiki-kb")
	if dest != wantDest {
		t.Errorf("dest = %q, want %q", dest, wantDest)
	}

	opened, err := kb.Open(dest)
	if err != nil {
		t.Fatalf("kb.Open(cloned): %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(opened.Root, "data", "index.md")); statErr != nil {
		t.Errorf("cloned KB missing data/index.md: %v", statErr)
	}

	// Second call must be idempotent: destination already has .git, no re-clone.
	dest2, err := ensureClonedKB(remoteURL, name, dataDir)
	if err != nil {
		t.Fatalf("ensureClonedKB (second call): %v", err)
	}
	if dest2 != dest {
		t.Errorf("second call dest = %q, want %q", dest2, dest)
	}
}

// TestEnsureClonedKBUsesExplicitName verifies that ensureClonedKB clones
// into <dataDir>/<name> using the caller-supplied name, not one re-derived
// from the remote URL — this is how KBSpec.Name (D53) reaches the clone
// destination.
func TestEnsureClonedKBUsesExplicitName(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH, skipping bootstrap clone test")
	}

	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "src-kb")
	if _, err := kb.Init(srcDir); err != nil {
		t.Fatalf("kb.Init(src): %v", err)
	}
	branchOut, err := exec.Command("git", "-C", srcDir, "symbolic-ref", "--short", "HEAD").Output()
	if err != nil {
		t.Fatalf("git symbolic-ref: %v", err)
	}
	branch := strings.TrimSpace(string(branchOut))

	bareDir := filepath.Join(tmp, "wiki-kb.git")
	mustRunGit(t, "", "init", "--bare", bareDir)
	mustRunGit(t, srcDir, "remote", "add", "origin", bareDir)
	mustRunGit(t, srcDir, "push", "origin", branch+":"+branch)
	mustRunGit(t, bareDir, "symbolic-ref", "HEAD", "refs/heads/"+branch)

	dataDir := filepath.Join(tmp, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}

	remoteURL := fileURL(bareDir)
	dest, err := ensureClonedKB(remoteURL, "custom-name", dataDir)
	if err != nil {
		t.Fatalf("ensureClonedKB: %v", err)
	}
	wantDest := filepath.Join(dataDir, "custom-name")
	if dest != wantDest {
		t.Errorf("dest = %q, want %q", dest, wantDest)
	}
}

func TestEnsureClonedKBRequiresDataDir(t *testing.T) {
	if _, err := ensureClonedKB("ssh://git@host/repo.git", "repo", ""); err == nil {
		t.Fatal("expected error when dataDir is empty, got nil")
	}
}

func TestEnsureClonedKBRequiresName(t *testing.T) {
	if _, err := ensureClonedKB("ssh://git@host/repo.git", "", "/some/data"); err == nil {
		t.Fatal("expected error when name is empty, got nil")
	}
}

func TestRemoteKBName(t *testing.T) {
	cases := map[string]string{
		"ssh://git@gitea.example.com:2222/user/wiki-kb.git": "wiki-kb",
		"git@host:user/repo.git":                            "repo",
		"https://example.com/org/my-kb.git":                 "my-kb",
		"/local/path/to/kb":                                 "kb",
		"file:///tmp/bare.git":                              "bare",
	}
	for in, want := range cases {
		if got := remoteKBName(in); got != want {
			t.Errorf("remoteKBName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSetupGitSSHNoopWithoutKey(t *testing.T) {
	restore := unsetEnvForTest(t, "GIT_SSH_COMMAND")
	defer restore()

	if err := setupGitSSH(t.TempDir()); err != nil {
		t.Fatalf("setupGitSSH: %v", err)
	}
	if v, ok := os.LookupEnv("GIT_SSH_COMMAND"); ok {
		t.Errorf("GIT_SSH_COMMAND set unexpectedly: %q", v)
	}
}

func TestSetupGitSSHSetsCommand(t *testing.T) {
	restore := unsetEnvForTest(t, "GIT_SSH_COMMAND")
	defer restore()

	dir := t.TempDir()
	key := writeCredential(t, dir, "default.ssh", "key-material")
	hosts := writeCredential(t, dir, "known_hosts", "host key")
	if err := setupGitSSH(dir); err != nil {
		t.Fatalf("setupGitSSH: %v", err)
	}
	got := os.Getenv("GIT_SSH_COMMAND")
	want := "ssh -i '" + key + "' -o 'UserKnownHostsFile=\"" + hosts + "\"' -o StrictHostKeyChecking=yes"
	if got != want {
		t.Errorf("GIT_SSH_COMMAND = %q, want %q", got, want)
	}
	if strings.Contains(got, "key-material") {
		t.Errorf("the key itself is in the command: %q", got)
	}
}

// git runs GIT_SSH_COMMAND through the shell: a path with a space, a quote or
// the backslashes of a Windows directory reaches ssh as one argument, and the
// known_hosts value is double-quoted for ssh's own split on whitespace.
func TestSSHCommandQuotesPaths(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh to run the command with")
	}
	key := `C:\Users\it's me\credentials\kb.ssh`
	hosts := `/srv/cred dir/known_hosts`
	cmd := sshCommand(key, hosts)
	out, err := exec.Command(sh, "-c", `ssh() { printf '%s\n' "$@"; }; `+cmd).Output()
	if err != nil {
		t.Fatalf("sh -c %q: %v", cmd, err)
	}
	want := "-i\n" + key + "\n-o\nUserKnownHostsFile=\"" + hosts + "\"\n-o\nStrictHostKeyChecking=yes\n"
	if string(out) != want {
		t.Errorf("ssh received\n%s\nwant\n%s", out, want)
	}
}

func TestSetupGitSSHEnvironmentWins(t *testing.T) {
	restore := unsetEnvForTest(t, "GIT_SSH_COMMAND")
	defer restore()

	if err := os.Setenv("GIT_SSH_COMMAND", "ssh -i /already-set"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeCredential(t, dir, "default.ssh", "key")
	if err := setupGitSSH(dir); err != nil {
		t.Fatalf("setupGitSSH: %v", err)
	}
	if got := os.Getenv("GIT_SSH_COMMAND"); got != "ssh -i /already-set" {
		t.Errorf("GIT_SSH_COMMAND overridden: %q", got)
	}
}

// TestGitEnvForKB_OwnSSHKey verifies that a KB's own <kb>.ssh produces its
// GIT_SSH_COMMAND and that default.ssh is left to setupGitSSH (the process
// environment), not repeated per KB. The environment carries no identity: the
// committer is the server's (D904).
func TestGitEnvForKB_OwnSSHKey(t *testing.T) {
	dir := t.TempDir()
	key := writeCredential(t, dir, "wiki-kb.ssh", "key")
	writeCredential(t, dir, "default.ssh", "other")
	hosts := writeCredential(t, dir, "known_hosts", "host key")

	got := gitEnvForKB("wiki-kb", dir)
	want := []string{
		"GIT_SSH_COMMAND=ssh -i '" + key + "' -o 'UserKnownHostsFile=\"" + hosts + "\"' -o StrictHostKeyChecking=yes",
	}
	if !equalStringSlices(got, want) {
		t.Errorf("gitEnvForKB(own key) = %v, want %v", got, want)
	}

	// Another KB has no file of its own: the default is the process's business.
	if got := gitEnvForKB("other-kb", dir); len(got) != 0 {
		t.Errorf("gitEnvForKB(default key only) = %v, want empty", got)
	}
}

// TestGitEnvForKB_NothingConfigured verifies that gitEnvForKB returns an empty
// (nil) env when there is no credentials directory to look in: git runs with
// the process environment.
func TestGitEnvForKB_NothingConfigured(t *testing.T) {
	got := gitEnvForKB("wiki-kb", "")
	if len(got) != 0 {
		t.Errorf("gitEnvForKB(nothing configured) = %v, want empty", got)
	}
}

// TestGitTokenCredentialEnv_FilePresent verifies that gitTokenCredentialEnv
// injects a GIT_CONFIG_* credential.helper referencing the token file's path,
// and that the token content itself never appears in the returned env (only
// the path does): the token is never in argv (D53, D905).
func TestGitTokenCredentialEnv_FilePresent(t *testing.T) {
	dir := t.TempDir()
	tokenPath := writeCredential(t, dir, "wiki-kb.token", "s3cr3t-token\n")

	got := gitTokenCredentialEnv(tokenPath)
	if len(got) != 3 {
		t.Fatalf("gitTokenCredentialEnv = %v, want 3 entries", got)
	}
	if got[0] != "GIT_CONFIG_COUNT=1" {
		t.Errorf("entry 0 = %q, want GIT_CONFIG_COUNT=1", got[0])
	}
	if got[1] != "GIT_CONFIG_KEY_0=credential.helper" {
		t.Errorf("entry 1 = %q, want GIT_CONFIG_KEY_0=credential.helper", got[1])
	}
	if !strings.Contains(got[2], "GIT_CONFIG_VALUE_0=") || !strings.Contains(got[2], tokenPath) {
		t.Errorf("entry 2 = %q, want it to reference %q", got[2], tokenPath)
	}
	for _, e := range got {
		if strings.Contains(e, "s3cr3t-token") {
			t.Errorf("token content leaked into env: %v", got)
		}
	}
}

// TestGitTokenCredentialEnv_NoPath verifies the no-op when there is no token
// file.
func TestGitTokenCredentialEnv_NoPath(t *testing.T) {
	if got := gitTokenCredentialEnv(""); got != nil {
		t.Errorf("gitTokenCredentialEnv(no path) = %v, want nil", got)
	}
}

// TestGitEnvForKB_TokenInjectsCredentialHelper verifies gitEnvForKB wires the
// KB's token file (own file, then default.token) through to the
// credential.helper env, without the token value anywhere in it.
func TestGitEnvForKB_TokenInjectsCredentialHelper(t *testing.T) {
	dir := t.TempDir()
	own := writeCredential(t, dir, "wiki-kb.token", "own-tok")
	def := writeCredential(t, dir, "default.token", "default-tok")

	for _, tc := range []struct{ kb, wantPath string }{
		{"wiki-kb", own},
		{"other-kb", def},
	} {
		got := gitEnvForKB(tc.kb, dir)
		found := false
		for _, e := range got {
			if strings.HasPrefix(e, "GIT_CONFIG_VALUE_0=") && strings.Contains(e, tc.wantPath) {
				found = true
			}
			if strings.Contains(e, "own-tok") || strings.Contains(e, "default-tok") {
				t.Errorf("token value in env entry %q", e)
			}
		}
		if !found {
			t.Errorf("gitEnvForKB(%s) = %v, want a GIT_CONFIG_VALUE_0 entry referencing %q", tc.kb, got, tc.wantPath)
		}
	}
}

// TestGitEnvForKB_TokenNoFileNoInjection verifies that gitEnvForKB adds no
// GIT_CONFIG_* entries when the KB has no token file and there is no default:
// the same as the key being unset.
func TestGitEnvForKB_TokenNoFileNoInjection(t *testing.T) {
	got := gitEnvForKB("wiki-kb", t.TempDir())
	if len(got) != 0 {
		t.Errorf("gitEnvForKB with no token file = %v, want empty", got)
	}
}

// TestResolveKBName covers the three resolution branches (D53): explicit
// spec.Name wins, then remote-derived, then path basename.
func TestResolveKBName(t *testing.T) {
	cases := []struct {
		name string
		spec config.KBSpec
		path string
		want string
	}{
		{
			name: "explicit name wins over remote",
			spec: config.KBSpec{Name: "custom", Remote: "ssh://git@host/wiki-kb.git"},
			want: "custom",
		},
		{
			name: "explicit name wins over path",
			spec: config.KBSpec{Name: "custom", Path: "/data/kb-locale"},
			path: "/data/kb-locale",
			want: "custom",
		},
		{
			name: "derived from remote",
			spec: config.KBSpec{Remote: "ssh://git@host/wiki-kb.git"},
			want: "wiki-kb",
		},
		{
			name: "derived from path basename",
			spec: config.KBSpec{Path: "/data/kb-locale"},
			path: "/data/kb-locale",
			want: "kb-locale",
		},
	}
	for _, c := range cases {
		if got := resolveKBName(c.spec, c.path); got != c.want {
			t.Errorf("%s: resolveKBName() = %q, want %q", c.name, got, c.want)
		}
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// unsetEnvForTest unsets key for the duration of the test and returns a
// func that restores the original value (or unsets it if it was unset).
func unsetEnvForTest(t *testing.T, key string) func() {
	t.Helper()
	orig, had := os.LookupEnv(key)
	os.Unsetenv(key)
	return func() {
		if had {
			os.Setenv(key, orig)
		} else {
			os.Unsetenv(key)
		}
	}
}

// TestEnsureClonedKBConfiguredBranch: a fresh clone lands on the `branch` its
// settings.yaml sets (D335, D903) — tracking it when the remote has it, created
// from the remote's default branch otherwise — and an existing clone is never
// switched. The setting is the one committed on the default branch, which is
// where a fresh clone starts.
func TestEnsureClonedKBConfiguredBranch(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	tmp := t.TempDir()
	for _, branch := range []string{"existing", "missing"} {
		srcDir := filepath.Join(tmp, "src-"+branch)
		if _, err := kb.Init(srcDir); err != nil {
			t.Fatalf("kb.Init(src): %v", err)
		}
		if err := os.WriteFile(filepath.Join(srcDir, kb.SettingsFile), []byte("branch: "+branch+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		mustRunGit(t, srcDir, "add", kb.SettingsFile)
		mustRunGit(t, srcDir, "-c", "user.name=user", "-c", "user.email=user@example.com", "commit", "-q", "-m", "settings: branch")
		bareDir := filepath.Join(tmp, branch+".git")
		mustRunGit(t, "", "init", "--bare", bareDir)
		mustRunGit(t, bareDir, "symbolic-ref", "HEAD", "refs/heads/main")
		mustRunGit(t, srcDir, "remote", "add", "origin", bareDir)
		mustRunGit(t, srcDir, "push", "origin", "main:main", "main:existing")
		remoteURL := fileURL(bareDir)

		dest, err := ensureClonedKB(remoteURL, "kb-"+branch, filepath.Join(tmp, "data"))
		if err != nil {
			t.Fatalf("ensureClonedKB(%s): %v", branch, err)
		}
		got, _ := gitx.Branch(dest)
		if got != branch {
			t.Fatalf("%s: branch = %q", branch, got)
		}
		// Existing clone: it is not switched, whatever the file now says.
		if _, err := ensureClonedKB(remoteURL, "kb-"+branch, filepath.Join(tmp, "data")); err != nil {
			t.Fatal(err)
		}
		if again, _ := gitx.Branch(dest); again != branch {
			t.Fatalf("%s: existing clone switched to %q", branch, again)
		}
		out, err := exec.Command("git", "-C", bareDir, "for-each-ref", "--format=%(refname:short)", "refs/heads/").Output()
		if err != nil {
			t.Fatal(err)
		}
		if branch == "missing" && strings.Contains(string(out), "missing") {
			t.Fatal("cloning created the configured branch on the remote")
		}
	}
}

// bareRemoteWithKB builds a bare repository holding a minimal KB and returns
// its file:// URL (shared by the atomic-clone and listen-first tests).
func bareRemoteWithKB(t *testing.T, tmp string) string {
	t.Helper()
	srcDir := filepath.Join(tmp, "src-kb")
	if _, err := kb.Init(srcDir); err != nil {
		t.Fatalf("kb.Init(src): %v", err)
	}
	out, err := exec.Command("git", "-C", srcDir, "symbolic-ref", "--short", "HEAD").Output()
	if err != nil {
		t.Fatalf("git symbolic-ref: %v", err)
	}
	branch := strings.TrimSpace(string(out))
	bareDir := filepath.Join(tmp, "wiki-kb.git")
	mustRunGit(t, "", "init", "--bare", bareDir)
	mustRunGit(t, srcDir, "remote", "add", "origin", bareDir)
	mustRunGit(t, srcDir, "push", "origin", branch+":"+branch)
	mustRunGit(t, bareDir, "symbolic-ref", "HEAD", "refs/heads/"+branch)
	return fileURL(bareDir)
}

// TestEnsureClonedKBIsAtomic: a clone killed mid-way must never look finished
// (D348). The clone goes to a dot-prefixed sibling and is renamed into place.
func TestEnsureClonedKBIsAtomic(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	tmp := t.TempDir()
	remote := bareRemoteWithKB(t, tmp)
	dataDir := filepath.Join(tmp, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// A leftover from a killed run is replaced, not reused.
	stale := filepath.Join(dataDir, ".wiki-kb.cloning")
	if err := os.MkdirAll(filepath.Join(stale, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	dest, err := ensureClonedKB(remote, "wiki-kb", dataDir)
	if err != nil {
		t.Fatalf("ensureClonedKB: %v", err)
	}
	if _, err := kb.Open(dest); err != nil {
		t.Fatalf("kb.Open: %v", err)
	}
	entries, _ := os.ReadDir(dataDir)
	if len(entries) != 1 || entries[0].Name() != "wiki-kb" {
		t.Errorf("data dir after success = %v, want only wiki-kb", entries)
	}

	// A failed clone leaves neither dest nor the temporary directory.
	bad := filepath.Join(tmp, "data2")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureClonedKB(fileURL(filepath.Join(tmp, "missing.git")), "gone", bad); err == nil || !strings.Contains(err.Error(), "clone ") {
		t.Fatalf("want a clone error, got %v", err)
	}
	if entries, _ := os.ReadDir(bad); len(entries) != 0 {
		t.Errorf("failed clone left %v", entries)
	}
}
