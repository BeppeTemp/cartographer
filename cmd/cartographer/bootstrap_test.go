package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/config"
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

	remoteURL := "file://" + bareDir
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

	remoteURL := "file://" + bareDir
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

	if err := setupGitSSH(config.GitConfig{}); err != nil {
		t.Fatalf("setupGitSSH: %v", err)
	}
	if v, ok := os.LookupEnv("GIT_SSH_COMMAND"); ok {
		t.Errorf("GIT_SSH_COMMAND set unexpectedly: %q", v)
	}
}

func TestSetupGitSSHSetsCommand(t *testing.T) {
	restore := unsetEnvForTest(t, "GIT_SSH_COMMAND")
	defer restore()

	if err := setupGitSSH(config.GitConfig{SSHKey: "/k", KnownHosts: "/kh"}); err != nil {
		t.Fatalf("setupGitSSH: %v", err)
	}
	got := os.Getenv("GIT_SSH_COMMAND")
	want := "ssh -i /k -o UserKnownHostsFile=/kh -o StrictHostKeyChecking=yes"
	if got != want {
		t.Errorf("GIT_SSH_COMMAND = %q, want %q", got, want)
	}
}

func TestSetupGitSSHEnvironmentWins(t *testing.T) {
	restore := unsetEnvForTest(t, "GIT_SSH_COMMAND")
	defer restore()

	if err := os.Setenv("GIT_SSH_COMMAND", "ssh -i /already-set"); err != nil {
		t.Fatal(err)
	}
	if err := setupGitSSH(config.GitConfig{SSHKey: "/k"}); err != nil {
		t.Fatalf("setupGitSSH: %v", err)
	}
	if got := os.Getenv("GIT_SSH_COMMAND"); got != "ssh -i /already-set" {
		t.Errorf("GIT_SSH_COMMAND overridden: %q", got)
	}
}

// TestGitEnvForKB_FullSpec verifies that a fully-populated KBSpec produces
// GIT_SSH_COMMAND and GIT_COMMITTER_NAME/EMAIL entries drawn entirely from
// the spec, ignoring the global GitConfig.
func TestGitEnvForKB_FullSpec(t *testing.T) {
	spec := config.KBSpec{
		SSHKey:         "/spec/key",
		KnownHosts:     "/spec/known_hosts",
		AuthorName:     "Spec Author",
		AuthorEmail:    "spec-author@wiki.local",
		CommitterName:  "Spec Committer",
		CommitterEmail: "spec-committer@wiki.local",
	}
	g := config.GitConfig{
		SSHKey:         "/global/key",
		KnownHosts:     "/global/known_hosts",
		AuthorName:     "Global Author",
		AuthorEmail:    "global-author@wiki.local",
		CommitterName:  "Global Committer",
		CommitterEmail: "global-committer@wiki.local",
	}

	got := gitEnvForKB(spec, g, "wiki-kb")
	want := []string{
		"GIT_SSH_COMMAND=ssh -i /spec/key -o UserKnownHostsFile=/spec/known_hosts -o StrictHostKeyChecking=yes",
		"GIT_COMMITTER_NAME=Spec Committer",
		"GIT_COMMITTER_EMAIL=spec-committer@wiki.local",
	}
	if !equalStringSlices(got, want) {
		t.Errorf("gitEnvForKB(full spec) = %v, want %v", got, want)
	}
}

// TestGitEnvForKB_EmptySpec verifies that an empty KBSpec falls back
// entirely to the global GitConfig for SSH and committer identity.
func TestGitEnvForKB_EmptySpec(t *testing.T) {
	g := config.GitConfig{
		SSHKey:      "/global/key",
		AuthorName:  "Global Author",
		AuthorEmail: "global-author@wiki.local",
		// CommitterName/Email left unset: committer falls back to author.
	}

	got := gitEnvForKB(config.KBSpec{}, g, "")
	want := []string{
		"GIT_SSH_COMMAND=ssh -i /global/key",
		"GIT_COMMITTER_NAME=Global Author",
		"GIT_COMMITTER_EMAIL=global-author@wiki.local",
	}
	if !equalStringSlices(got, want) {
		t.Errorf("gitEnvForKB(empty spec) = %v, want %v", got, want)
	}
}

// TestGitEnvForKB_CommitterFallsBackToAuthor verifies the fallback cascade
// for the committer: spec committer > spec author > global committer >
// global author.
func TestGitEnvForKB_CommitterFallsBackToAuthor(t *testing.T) {
	spec := config.KBSpec{
		AuthorName:  "Spec Author",
		AuthorEmail: "spec-author@wiki.local",
		// CommitterName/Email left unset: falls back to spec author.
	}
	g := config.GitConfig{
		CommitterName:  "Global Committer",
		CommitterEmail: "global-committer@wiki.local",
	}

	got := gitEnvForKB(spec, g, "")
	want := []string{
		"GIT_COMMITTER_NAME=Spec Author",
		"GIT_COMMITTER_EMAIL=spec-author@wiki.local",
	}
	if !equalStringSlices(got, want) {
		t.Errorf("gitEnvForKB(committer fallback to author) = %v, want %v", got, want)
	}
}

// TestGitEnvForKB_NoIdentityAtAll verifies that gitEnvForKB returns an empty
// (nil) env when neither the spec nor the global config carry any identity
// or SSH override — the pre-M3 behaviour (run with the process environment).
func TestGitEnvForKB_NoIdentityAtAll(t *testing.T) {
	got := gitEnvForKB(config.KBSpec{}, config.GitConfig{}, "")
	if len(got) != 0 {
		t.Errorf("gitEnvForKB(no identity) = %v, want empty", got)
	}
}

// TestGitTokenCredentialEnv_FilePresent verifies that gitTokenCredentialEnv
// injects a GIT_CONFIG_* credential.helper referencing the token file's path
// when <tokenDir>/<name>.token exists, and that the token content itself
// never appears in the returned env (only the path does) (D53).
func TestGitTokenCredentialEnv_FilePresent(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "wiki-kb.token")
	if err := os.WriteFile(tokenPath, []byte("s3cr3t-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := gitTokenCredentialEnv(dir, "wiki-kb")
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

// TestGitTokenCredentialEnv_FileAbsent verifies that no env is injected when
// the token file does not exist, even if tokenDir/name are set.
func TestGitTokenCredentialEnv_FileAbsent(t *testing.T) {
	dir := t.TempDir()
	if got := gitTokenCredentialEnv(dir, "no-such-kb"); got != nil {
		t.Errorf("gitTokenCredentialEnv(file absent) = %v, want nil", got)
	}
}

// TestGitTokenCredentialEnv_NoTokenDir verifies the no-op when TokenDir is
// unset.
func TestGitTokenCredentialEnv_NoTokenDir(t *testing.T) {
	if got := gitTokenCredentialEnv("", "wiki-kb"); got != nil {
		t.Errorf("gitTokenCredentialEnv(no dir) = %v, want nil", got)
	}
}

// TestGitEnvForKB_TokenDirInjectsCredentialHelper verifies gitEnvForKB wires
// GitConfig.TokenDir through to the credential.helper env alongside the
// existing SSH/committer entries.
func TestGitEnvForKB_TokenDirInjectsCredentialHelper(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "wiki-kb.token")
	if err := os.WriteFile(tokenPath, []byte("tok"), 0o600); err != nil {
		t.Fatal(err)
	}

	g := config.GitConfig{TokenDir: dir}
	got := gitEnvForKB(config.KBSpec{}, g, "wiki-kb")
	found := false
	for _, e := range got {
		if strings.HasPrefix(e, "GIT_CONFIG_VALUE_0=") && strings.Contains(e, tokenPath) {
			found = true
		}
	}
	if !found {
		t.Errorf("gitEnvForKB with TokenDir = %v, want a GIT_CONFIG_VALUE_0 entry referencing %q", got, tokenPath)
	}
}

// TestGitEnvForKB_TokenDirNoFileNoInjection verifies that gitEnvForKB stays
// as before (no GIT_CONFIG_* entries) when the per-KB token file is absent.
func TestGitEnvForKB_TokenDirNoFileNoInjection(t *testing.T) {
	dir := t.TempDir()
	g := config.GitConfig{TokenDir: dir}
	got := gitEnvForKB(config.KBSpec{}, g, "wiki-kb")
	if len(got) != 0 {
		t.Errorf("gitEnvForKB with TokenDir but no token file = %v, want empty", got)
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
