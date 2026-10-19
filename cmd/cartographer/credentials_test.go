package main

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/config"
)

const testSeedHex = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

// writeCredential writes a file of a credentials directory, 0600, and returns
// its path.
func writeCredential(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// known_hosts holds public host keys, 0644 like OpenSSH's own: it is not
// reported as a credential file other users can read, while a key file is.
func TestKnownHostsIsNotALooseCredential(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permissions are not checked on Windows")
	}
	dir := t.TempDir()
	key := writeCredential(t, dir, "kb.ssh", "key")
	hosts := writeCredential(t, dir, config.KnownHostsFile, "host key")
	for _, p := range []string{key, hosts} {
		if err := os.Chmod(p, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	logs := captureLog(t)
	if env := gitEnvForKB("kb", dir); len(env) != 1 || !strings.Contains(env[0], hosts) {
		t.Fatalf("gitEnvForKB = %v, want the key with %s", env, hosts)
	}
	if out := logs.String(); strings.Contains(out, hosts) || !strings.Contains(out, key) {
		t.Errorf("want the key reported and known_hosts not:\n%s", out)
	}
}

// A KB's own file wins over the default, and a KB without one takes the default.
func TestCredentialFilePerKBWinsOverDefault(t *testing.T) {
	dir := t.TempDir()
	own := writeCredential(t, dir, "kb-a.token", "own")
	def := writeCredential(t, dir, "default.token", "default")

	if got, ok := credentialFile(dir, "kb-a", config.CredentialToken); !ok || got != own {
		t.Errorf("kb-a token = %q %v, want %q", got, ok, own)
	}
	if got, ok := credentialFile(dir, "kb-b", config.CredentialToken); !ok || got != def {
		t.Errorf("kb-b token = %q %v, want the default %q", got, ok, def)
	}
}

// The signing seed has no default: a signature names its KB.
func TestCredentialSigningNeverFallsBack(t *testing.T) {
	dir := t.TempDir()
	writeCredential(t, dir, "default.signing", testSeedHex)
	writeCredential(t, dir, "kb-a.signing", testSeedHex)

	if _, ok := credentialFile(dir, "kb-b", config.CredentialSigning); ok {
		t.Error("kb-b found a signing seed through a default")
	}
	if _, signed, err := loadArtifactSigner(dir, "kb-b"); signed || err != nil {
		t.Errorf("kb-b signer: signed=%v err=%v, want unsigned and no error", signed, err)
	}
	key, signed, err := loadArtifactSigner(dir, "kb-a")
	if err != nil || !signed || len(key) != ed25519.PrivateKeySize {
		t.Fatalf("kb-a signer: signed=%v err=%v", signed, err)
	}
}

// With no file, and no default, a credential behaves as the key being unset.
func TestCredentialNoFileBehavesAsUnset(t *testing.T) {
	for _, dir := range []string{"", t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		for _, kind := range []string{config.CredentialSSH, config.CredentialToken, config.CredentialAge, config.CredentialSigning} {
			if p, ok := credentialFile(dir, "kb-a", kind); ok || p != "" {
				t.Errorf("dir %q kind %s: %q %v, want nothing", dir, kind, p, ok)
			}
		}
		if got := resolveSopsAgeKeyFile(dir, "kb-a"); got != "" {
			t.Errorf("dir %q: age key %q, want none", dir, got)
		}
		if got := gitEnvForKB("kb-a", dir); len(got) != 0 {
			t.Errorf("dir %q: git env %v, want none", dir, got)
		}
	}
}

// A name that would leave the directory is not looked up.
func TestCredentialFileRejectsPathLikeNames(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	writeCredential(t, dir, "x.token", "outside")
	for _, name := range []string{"../x", "..", "a/b", `a\b`} {
		if p, ok := credentialFile(sub, name, config.CredentialToken); ok {
			t.Errorf("name %q resolved to %q", name, p)
		}
	}
}

func TestResolveSopsAgeKeyFile(t *testing.T) {
	dir := t.TempDir()
	own := writeCredential(t, dir, "wiki-kb.age", "age-key-material")
	def := writeCredential(t, dir, "default.age", "other")
	if got := resolveSopsAgeKeyFile(dir, "wiki-kb"); got != own {
		t.Errorf("own = %q, want %q", got, own)
	}
	if got := resolveSopsAgeKeyFile(dir, "no-such-kb"); got != def {
		t.Errorf("default = %q, want %q", got, def)
	}
}

// A file that group or others can read is warned about once, never refused.
func TestCredentialGroupReadableFileWarnsOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX modes")
	}
	dir := t.TempDir()
	loose := writeCredential(t, dir, "kb-a.age", "key")
	if err := os.Chmod(loose, 0o640); err != nil {
		t.Fatal(err)
	}
	tight := writeCredential(t, dir, "kb-b.age", "key")

	buf := captureLog(t)
	for i := 0; i < 3; i++ {
		if got, ok := credentialFile(dir, "kb-a", config.CredentialAge); !ok || got != loose {
			t.Fatalf("a loose file must still be used: %q %v", got, ok)
		}
	}
	if _, ok := credentialFile(dir, "kb-b", config.CredentialAge); !ok {
		t.Fatal("kb-b age not found")
	}
	out := buf.String()
	if n := strings.Count(out, "warning: credential file "+loose); n != 1 {
		t.Errorf("warned %d times about the loose file, want 1:\n%s", n, out)
	}
	if strings.Contains(out, tight) {
		t.Errorf("a 0600 file was warned about:\n%s", out)
	}
	if !strings.Contains(out, "chmod 600") {
		t.Errorf("the warning does not say what to do:\n%s", out)
	}
}

// The seed is never in an error: a wrong file names the file, not its content.
func TestLoadArtifactSignerBadSeedLeaksNothing(t *testing.T) {
	dir := t.TempDir()
	writeCredential(t, dir, "kb-a.signing", "not-a-hex-seed-zzzz\n")
	_, signed, err := loadArtifactSigner(dir, "kb-a")
	if err == nil || signed {
		t.Fatalf("signed=%v err=%v, want an error", signed, err)
	}
	if !strings.Contains(err.Error(), "kb-a.signing") || strings.Contains(err.Error(), "not-a-hex-seed") {
		t.Errorf("error = %v", err)
	}
	// Whitespace around a good seed is ignored (a file ends with a newline).
	writeCredential(t, dir, "kb-b.signing", "  "+testSeedHex+"\r\n")
	if _, signed, err := loadArtifactSigner(dir, "kb-b"); err != nil || !signed {
		t.Errorf("kb-b: signed=%v err=%v", signed, err)
	}
}

// Discovery leaves the server's own credentials and audit directories alone, but a KB that
// happens to be called credentials is a KB, and then credentials_dir refuses it.
func TestDiscoveryAndTheCredentialsDirectory(t *testing.T) {
	data := t.TempDir()
	for _, d := range []string{"kb-a", "credentials", "audit"} {
		if err := os.MkdirAll(filepath.Join(data, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	paths, err := discoverKBPaths(data, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || filepath.Base(paths[0]) != "kb-a" {
		t.Errorf("discovered %v, want only kb-a", paths)
	}
	cfg := &config.Config{Data: data}
	if err := checkCredentialsDir(cfg); err != nil {
		t.Errorf("a plain credentials dir is refused: %v", err)
	}

	writeIndex(t, filepath.Join(data, "credentials"))
	paths, err = discoverKBPaths(data, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Errorf("a KB named credentials is discovered as a KB, got %v", paths)
	}
	if err := checkCredentialsDir(cfg); err == nil || !strings.Contains(err.Error(), "is a KB") {
		t.Errorf("a credentials dir that is a KB must be refused, got %v", err)
	}
}

// Every commit of a KB stages its whole working tree, so a credentials_dir in
// one is refused, whether the KB exists yet or not and whether this server
// mounts it or not; a direct subdirectory of data that is the credentials
// directory is not discovered as a KB.
func TestCredentialsDirOutsideEveryKB(t *testing.T) {
	data := t.TempDir()
	other := t.TempDir()
	writeIndex(t, filepath.Join(data, "kb-a"))
	writeIndex(t, filepath.Join(other, "unmounted"))
	if err := os.MkdirAll(filepath.Join(data, "kb-b"), 0o755); err != nil {
		t.Fatal(err)
	}
	kbs := []config.KBSpec{
		{Path: filepath.Join(other, "local")},
		{Remote: "git@example.com:team/kb-c.git"},
	}
	for _, tc := range []struct {
		credDir, refused string
	}{
		{filepath.Join(data, "kb-a", "secrets"), filepath.Join(data, "kb-a")},
		{filepath.Join(data, "kb-b", "secrets"), filepath.Join(data, "kb-b")},
		{filepath.Join(other, "local", "secrets"), filepath.Join(other, "local")},
		{filepath.Join(other, "unmounted", "data", "secrets"), filepath.Join(other, "unmounted")},
		{filepath.Join(data, "kb-c"), filepath.Join(data, "kb-c")},
		{filepath.Join(data, "secrets"), ""},
		{filepath.Join(data, "credentials", "prod"), ""},
		{filepath.Join(data, ".secrets", "prod"), ""},
		{filepath.Join(other, "secrets"), ""},
		{data, ""},
	} {
		err := checkCredentialsDir(&config.Config{Data: data, CredentialsDir: tc.credDir, KBs: kbs})
		switch {
		case tc.refused == "" && err != nil:
			t.Errorf("%s refused: %v", tc.credDir, err)
		case tc.refused != "" && (err == nil || !strings.Contains(err.Error(), "inside the KB "+tc.refused+",")):
			t.Errorf("%s: err = %v, want it inside %s", tc.credDir, err, tc.refused)
		}
	}

	secrets := filepath.Join(data, "secrets")
	if err := os.MkdirAll(secrets, 0o700); err != nil {
		t.Fatal(err)
	}
	paths, err := discoverKBPaths(data, secrets)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		if p == secrets {
			t.Errorf("the credentials directory was discovered as a KB: %v", paths)
		}
	}
	if len(paths) != 2 {
		t.Errorf("discovered %v, want kb-a and kb-b", paths)
	}
}

func writeIndex(t *testing.T, kbRoot string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(kbRoot, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kbRoot, "data", "index.md"), []byte("# index\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A clone that failed names the file the KB would have been reached with.
func TestCredentialHint(t *testing.T) {
	dir := t.TempDir()
	got := credentialHint("https://git.example.com/user/kb-a.git", "kb-a", dir)
	for _, want := range []string{"kb-a.token", "default.token"} {
		if !strings.Contains(got, want) {
			t.Errorf("hint %q lacks %q", got, want)
		}
	}
	if got := credentialHint("ssh://git@git.example.com/user/kb-a.git", "kb-a", dir); !strings.Contains(got, "kb-a.ssh") {
		t.Errorf("ssh hint = %q", got)
	}
	writeCredential(t, dir, "default.token", "tok")
	if got := credentialHint("https://git.example.com/user/kb-a.git", "kb-a", dir); got != "" {
		t.Errorf("a KB that has its file gets a hint: %q", got)
	}
}
