package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func loadGitYAML(t *testing.T, body string) *Config {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// A KB's entry wins, then the default entry, then git.identity; a present empty
// entry means the KB has none (D950).
func TestIdentityFor_Precedence(t *testing.T) {
	t.Parallel()
	cfg := loadGitYAML(t, `git:
  identity: "Base One <base@example.test>"
  identities:
    kb-a: "Work Me <me@work.example.test>"
    kb-none: ""
`)
	for kb, want := range map[string]string{
		"kb-a":    "Work Me <me@work.example.test>",
		"kb-b":    "Base One <base@example.test>",
		"kb-none": "",
	} {
		name, email, ok := cfg.IdentityFor(kb)
		got := ""
		if ok {
			got = name + " <" + email + ">"
		}
		if got != want {
			t.Errorf("%s: %q, want %q", kb, got, want)
		}
	}
	cfg = loadGitYAML(t, "git:\n  identity: \"Base One <base@example.test>\"\n  identities:\n    default: \"Def One <def@example.test>\"\n")
	if n, e, ok := cfg.IdentityFor("kb-x"); !ok || n != "Def One" || e != "def@example.test" {
		t.Errorf("default entry: %q <%q> %v", n, e, ok)
	}
	if _, _, ok := loadGitYAML(t, "http: \":1\"\n").IdentityFor("kb-a"); ok {
		t.Error("nothing configured: the server identity applies")
	}
}

// An invalid value is dropped with a warning and never stops the start.
func TestLoad_InvalidGitIdentityIsDroppedWithAWarning(t *testing.T) {
	t.Parallel()
	cfg := loadGitYAML(t, "git:\n  identity: \"no email\"\n  identities:\n    kb-a: \"Ada <a@@b>\"\n    kb-b: \"Bob <bob@example.test>\"\n")
	if _, _, ok := cfg.IdentityFor("kb-a"); ok {
		t.Error("an invalid entry must not apply")
	}
	if n, _, ok := cfg.IdentityFor("kb-b"); !ok || n != "Bob" {
		t.Errorf("a valid entry next to an invalid one: %q %v", n, ok)
	}
	got := strings.Join(cfg.Warnings, "\n")
	for _, want := range []string{"git.identity is ignored", "git.identities.kb-a is ignored"} {
		if !strings.Contains(got, want) {
			t.Errorf("warnings lack %q:\n%s", want, got)
		}
	}
}

// The retired git.author_name and git.author_email are read as git.identity
// until the realignment writes the new key; MigrateGitIdentity writes it,
// keeping the file's comments, once.
func TestMigrateGitIdentity_FromAuthorKeys(t *testing.T) {
	t.Parallel()
	body := "# my server\nhttp: \":39273\"\ngit:\n  author_name: \"Ada Example\"\n  author_email: ada@example.test\n"
	cfg := loadGitYAML(t, body)
	if n, e, ok := cfg.IdentityFor("kb-a"); !ok || n != "Ada Example" || e != "ada@example.test" {
		t.Errorf("legacy keys read as git.identity: %q <%q> %v", n, e, ok)
	}
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	wrote, err := MigrateGitIdentity(p)
	if err != nil || !wrote {
		t.Fatalf("MigrateGitIdentity = %v, %v, want it to write", wrote, err)
	}
	out, _ := os.ReadFile(p)
	if !strings.Contains(string(out), "# my server") || !strings.Contains(string(out), `identity: "Ada Example <ada@example.test>"`) {
		t.Errorf("file after migration:\n%s", out)
	}
	if info, _ := os.Stat(p); runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 { // no POSIX modes on Windows
		t.Errorf("mode = %v, want 0640 kept", info.Mode().Perm())
	}
	if wrote, err := MigrateGitIdentity(p); err != nil || wrote {
		t.Errorf("second run = %v, %v, want nothing to do", wrote, err)
	}
	// An existing git.identity is never replaced.
	q := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(q, []byte("git:\n  author_name: A\n  author_email: a@example.test\n  identity: \"Keep Me <keep@example.test>\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if wrote, _ := MigrateGitIdentity(q); wrote {
		t.Error("an existing git.identity must be kept")
	}
	if n, _, _ := loadGitYAML(t, "git:\n  author_name: A\n  author_email: a@example.test\n  identity: \"Keep Me <keep@example.test>\"\n").IdentityFor("x"); n != "Keep Me" {
		t.Errorf("git.identity must win over the legacy keys: %q", n)
	}
}

// A file with a second YAML document is not rewritten: the rewrite would drop
// it, and a refused migration keeps the retired keys in place.
func TestMigrateGitIdentity_RefusesAMultiDocumentFile(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "config.yaml")
	body := "git:\n  author_name: A\n  author_email: a@example.test\n---\nextra: 1\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if wrote, err := MigrateGitIdentity(p); err == nil || wrote {
		t.Errorf("MigrateGitIdentity = %v, %v, want a refusal", wrote, err)
	}
	if got, _ := os.ReadFile(p); string(got) != body {
		t.Errorf("the file changed:\n%s", got)
	}
}

// The file a symlink points to is the one rewritten, and the link stays.
func TestMigrateGitIdentity_FollowsASymlink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	real := filepath.Join(dir, "real.yaml")
	link := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(real, []byte("git:\n  author_name: A\n  author_email: a@example.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks are not available here")
	}
	if wrote, err := MigrateGitIdentity(link); err != nil || !wrote {
		t.Fatalf("MigrateGitIdentity = %v, %v", wrote, err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink must stay a symlink")
	}
	if got, _ := os.ReadFile(real); !strings.Contains(string(got), "identity:") {
		t.Errorf("the target was not rewritten:\n%s", got)
	}
}
