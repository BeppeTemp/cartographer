package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/settingschema"
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

// A KB's entry wins, then the default entry; a present empty
// entry means the KB has none (D950).
func TestIdentityFor_Precedence(t *testing.T) {
	t.Parallel()
	cfg := loadGitYAML(t, `git:
  identities:
    default: "Base One <base@example.test>"
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
	cfg = loadGitYAML(t, "git:\n  identity: \"Base One <base@example.test>\"\n")
	if n, e, ok := cfg.IdentityFor("kb-x"); !ok || n != "Base One" || e != "base@example.test" {
		t.Errorf("the retired git.identity is read as the default entry: %q <%q> %v", n, e, ok)
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

// The retired git.author_name and git.author_email are read as git.identities.default
// until the realignment writes the new key; MigrateGitIdentity writes it,
// keeping the file's comments, once.
func TestMigrateGitIdentity_FromAuthorKeys(t *testing.T) {
	t.Parallel()
	body := "# my server\nhttp: \":39273\"\ngit:\n  author_name: \"Ada Example\"\n  author_email: ada@example.test\n"
	cfg := loadGitYAML(t, body)
	if n, e, ok := cfg.IdentityFor("kb-a"); !ok || n != "Ada Example" || e != "ada@example.test" {
		t.Errorf("legacy keys read as git.identities.default: %q <%q> %v", n, e, ok)
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
	if !strings.Contains(string(out), "# my server") || !strings.Contains(string(out), `default: "Ada Example <ada@example.test>"`) {
		t.Errorf("file after migration:\n%s", out)
	}
	if info, _ := os.Stat(p); runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 { // no POSIX modes on Windows
		t.Errorf("mode = %v, want 0640 kept", info.Mode().Perm())
	}
	if wrote, err := MigrateGitIdentity(p); err != nil || wrote {
		t.Errorf("second run = %v, %v, want nothing to do", wrote, err)
	}
	// An existing git.identity (moved by the realignment) is never replaced.
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
	if got, _ := os.ReadFile(real); !strings.Contains(string(got), "default:") {
		t.Errorf("the target was not rewritten:\n%s", got)
	}
}

// git.identity and git.identities.default say the same thing: set to different
// values, Load refuses rather than choose one (Moved); equal values load.
func TestLoad_GitIdentityAndDefaultConflictIsRefused(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "config.yaml")
	body := "git:\n  identity: \"One <one@example.test>\"\n  identities:\n    default: \"Two <two@example.test>\"\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "git.identity") || !strings.Contains(err.Error(), "git.identities.default") {
		t.Fatalf("Load = %v, want a refusal naming both keys", err)
	}
	same := "git:\n  identity: \"One <one@example.test>\"\n  identities:\n    default: \"One <one@example.test>\"\n"
	if n, _, ok := loadGitYAML(t, same).IdentityFor("kb-a"); !ok || n != "One" {
		t.Errorf("equal values must load: %q %v", n, ok)
	}
}

// The realignment moves git.identity under git.identities.default, keeps a
// comment a person wrote, and refuses a file where the two disagree.
func TestRealignServerFile_MovesGitIdentity(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "config.yaml")
	body := "git:\n  # mine\n  identity: \"One <one@example.test>\"\n  identities:\n    kb-a: \"Two <two@example.test>\"\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, _, err := RealignServerFile(p, "0.0.0")
	if err != nil || !rep.Substantive() {
		t.Fatalf("RealignServerFile = %v, %v", rep, err)
	}
	out, _ := os.ReadFile(p)
	got := string(out)
	if hasKey(got, "identity") || !strings.Contains(got, "# mine") || !strings.Contains(got, `default: "One <one@example.test>"`) || !strings.Contains(got, "kb-a:") {
		t.Errorf("file after realignment:\n%s", got)
	}
	if n, _, ok := loadGitYAML(t, got).IdentityFor("kb-z"); !ok || n != "One" {
		t.Errorf("the moved value must still apply: %q %v", n, ok)
	}
	if rep2, _, err := RealignServerFile(p, "0.0.0"); err != nil || !rep2.Empty() {
		t.Errorf("second run = %v, %v, want nothing to do", rep2, err)
	}

	q := filepath.Join(t.TempDir(), "config.yaml")
	conflict := "git:\n  identity: \"One <one@example.test>\"\n  identities:\n    default: \"Two <two@example.test>\"\n"
	if err := os.WriteFile(q, []byte(conflict), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RealignServerFile(q, "0.0.0"); err == nil {
		t.Error("a conflicting pair must be refused")
	}
	if got, _ := os.ReadFile(q); string(got) != conflict {
		t.Errorf("a refused realignment changed the file:\n%s", got)
	}
}

// A bare `identities:` is filled, not duplicated, by the author-key migration
// and by the realignment of git.identity.
func TestGitIdentityMigrationsFillABareIdentities(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"author keys":  "git:\n  author_name: A\n  author_email: a@example.test\n  identities:\n",
		"git.identity": "git:\n  identity: \"A <a@example.test>\"\n  identities:\n",
	} {
		p := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := MigrateGitIdentity(p); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, _, err := RealignServerFile(p, "0.0.0"); err != nil {
			t.Fatalf("%s: realign: %v", name, err)
		}
		out, _ := os.ReadFile(p)
		if strings.Count(string(out), "identities:") != 1 || hasKey(string(out), "identity") {
			t.Errorf("%s: file:\n%s", name, out)
		}
		if n, _, ok := loadGitYAML(t, string(out)).IdentityFor("kb-a"); !ok || n != "A" {
			t.Errorf("%s: identity lost: %q %v\n%s", name, n, ok, out)
		}
	}
}

// With the real schema: a git.identity that carries the comment the schema
// wrote realigns to identities.default, that comment goes, a person's note stays,
// and an empty default next to a set git.identity is refused.
func TestRealignServerFile_GeneratedCommentOfGitIdentity(t *testing.T) {
	t.Parallel()
	gen := ""
	for _, m := range Retired.Moved {
		if m.From == "git.identity" {
			// The comment the schema wrote above the key in the last release.
			for _, l := range settingschema.Comment(settingschema.Key{Path: "git.identity", Scope: settingschema.ScopeServer, Type: "string", Default: `""`, Restart: true, Description: m.Description}) {
				gen += "  " + l + "\n"
			}
		}
	}
	p := filepath.Join(t.TempDir(), "config.yaml")
	body := "git:\n" + gen + "  # my note\n  identity: \"A <a@example.test>\"\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RealignServerFile(p, "0.0.0"); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(p)
	got := string(out)
	if strings.Contains(got, "server commits as in every KB without its own entry") || !strings.Contains(got, "# my note") || !strings.Contains(got, `default: "A <a@example.test>"`) {
		t.Errorf("file:\n%s", got)
	}

	q := filepath.Join(t.TempDir(), "config.yaml")
	empty := "git:\n  identity: \"A <a@example.test>\"\n  identities:\n    default: \"\"\n"
	if err := os.WriteFile(q, []byte(empty), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RealignServerFile(q, "0.0.0"); err == nil {
		t.Error("an empty default next to a set git.identity must be refused")
	}
	if _, err := Load(q); err == nil {
		t.Error("Load must refuse the same pair")
	}
}

// hasKey reports whether a YAML line starts with the key (a comment that merely
// names a longer key, like proxy_identity, is not one).
func hasKey(doc, key string) bool {
	for _, l := range strings.Split(doc, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), key+":") {
			return true
		}
	}
	return false
}

// SetDefaultGitIdentity is what `connect` writes on a personal install: it
// creates the git block when the file has none (or a bare one), keeps comments,
// and never replaces an identity that is already there.
func TestSetDefaultGitIdentity(t *testing.T) {
	t.Parallel()
	const ident = "Ada Example <ada@example.test>"
	for name, body := range map[string]string{
		"no git block":   "# my server\nhttp: \":39273\"\n",
		"bare git block": "# my server\nhttp: \":39273\"\ngit:\n",
		"git block":      "# my server\nhttp: \":39273\"\ngit:\n  sync: true\n",
	} {
		p := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		wrote, err := SetDefaultGitIdentity(p, ident)
		if err != nil || !wrote {
			t.Fatalf("%s: SetDefaultGitIdentity = %v, %v, want it to write", name, wrote, err)
		}
		out, _ := os.ReadFile(p)
		if !strings.Contains(string(out), "# my server") {
			t.Errorf("%s: the comment was lost:\n%s", name, out)
		}
		if n, e, ok := loadGitYAML(t, string(out)).IdentityFor("kb-a"); !ok || n != "Ada Example" || e != "ada@example.test" {
			t.Errorf("%s: written file reads back as %q <%q> %v:\n%s", name, n, e, ok, out)
		}
		if wrote, err := SetDefaultGitIdentity(p, "Other <other@example.test>"); err != nil || wrote {
			t.Errorf("%s: an existing default must be kept, got %v, %v", name, wrote, err)
		}
	}
	if _, err := SetDefaultGitIdentity(filepath.Join(t.TempDir(), "x.yaml"), "not an identity"); err == nil {
		t.Error("an invalid identity must be refused")
	}
}

func TestHasIdentityEntry(t *testing.T) {
	t.Parallel()
	if loadGitYAML(t, "http: \":1\"\n").HasIdentityEntry("kb-a") {
		t.Error("no entry reported")
	}
	if !loadGitYAML(t, "git:\n  identities:\n    default: \"\"\n").HasIdentityEntry("kb-a") {
		t.Error("an empty default entry is an explicit choice")
	}
	if !loadGitYAML(t, "git:\n  identities:\n    kb-a: \"Ada Example <ada@example.test>\"\n").HasIdentityEntry("kb-a") {
		t.Error("the KB's own entry")
	}
}
