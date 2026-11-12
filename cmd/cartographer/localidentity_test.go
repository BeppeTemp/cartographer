package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

func stubLocalIdent(t *testing.T, ident string, perFolder bool) {
	t.Helper()
	oldGlobal, oldLocal := globalIdentFn, localIdentFn
	globalIdentFn = func() (string, bool) { return ident, perFolder }
	localIdentFn = func(string) (string, string, bool) {
		if ident == "" {
			return "", "", false
		}
		return "Ada Example", "ada@example.test", true
	}
	t.Cleanup(func() { globalIdentFn, localIdentFn = oldGlobal, oldLocal })
}

func mountOneKB(t *testing.T, mutate func(*config.Config)) *kb.KB {
	t.Helper()
	data := t.TempDir()
	if _, err := kb.Init(filepath.Join(data, "kb-a")); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Data = data
	cfg.HTTP = "127.0.0.1:0"
	mutate(cfg)
	t.Setenv("K_SERVICE", "")
	bs := bootstrapKBs(cfg, func(string) {})
	t.Cleanup(func() {
		for _, ix := range bs.sqlIdxs {
			ix.Close()
		}
	})
	if len(bs.kbs) != 1 {
		t.Fatalf("mounted %v", bs.names)
	}
	return bs.kbs[0]
}

// A local server with no git.identities entry commits as the user running it;
// with no identity to derive, it marks the KB so a write is refused when the KB
// has a remote. A shared server (tokens) and an explicit entry are untouched.
func TestBootstrapKBs_LocalServerDerivesTheUsersGitIdentity(t *testing.T) {
	stubLocalIdent(t, "Ada Example <ada@example.test>", false)
	k := mountOneKB(t, func(*config.Config) {})
	if n, e, ok := k.ConfiguredIdentity(); !ok || n != "Ada Example" || e != "ada@example.test" {
		t.Errorf("local server identity = %q <%q> %v, want the user's git identity", n, e, ok)
	}

	shared := mountOneKB(t, func(c *config.Config) {
		c.Auth = config.AuthConfig{Mode: "on", Tokens: []config.TokenSpec{{Token: "tok-example-0123456789abcdef", Scopes: []string{"kb:kb-a:rw"}}}}
	})
	if _, _, ok := shared.ConfiguredIdentity(); ok {
		t.Error("a shared server must not take the identity of the user running it")
	}

	explicit := mountOneKB(t, func(c *config.Config) {
		c.GitIdentities = map[string]string{config.IdentityDefault: "Bob Example <bob@example.test>"}
	})
	if n, _, _ := explicit.ConfiguredIdentity(); n != "Bob Example" {
		t.Errorf("git.identities must win over the derived identity, got %q", n)
	}

	stubLocalIdent(t, "", false)
	none := mountOneKB(t, func(*config.Config) {})
	if _, _, ok := none.ConfiguredIdentity(); ok {
		t.Error("no identity to derive: none is configured")
	}
}

// connect writes the identity it found into git.identities.default, once, and
// says at once when there is none.
func TestEnsureLocalGitIdentity(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("http: \"127.0.0.1:39273\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stubLocalIdent(t, "Ada Example <ada@example.test>", false)
	var out bytes.Buffer
	ensureLocalGitIdentity(&out, cfgPath, true)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if n, e, ok := cfg.IdentityFor("kb-a"); !ok || n != "Ada Example" || e != "ada@example.test" {
		t.Errorf("git.identities.default = %q <%q> %v after connect:\n%s", n, e, ok, out.String())
	}
	if !strings.Contains(out.String(), "git.identities.default") {
		t.Errorf("connect must say what it wrote: %q", out.String())
	}
	out.Reset()
	ensureLocalGitIdentity(&out, cfgPath, true)
	if out.Len() != 0 {
		t.Errorf("a second connect says nothing, got %q", out.String())
	}

	stubLocalIdent(t, "", false)
	bare := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(bare, []byte("http: \"127.0.0.1:39273\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	ensureLocalGitIdentity(&out, bare, true)
	if !strings.Contains(out.String(), "git.identities.default") || !strings.Contains(out.String(), "user.email") {
		t.Errorf("a missing identity is named with the keys to set, got %q", out.String())
	}
	if data, _ := os.ReadFile(bare); strings.Contains(string(data), "git:") {
		t.Errorf("nothing is written without an identity:\n%s", data)
	}
	out.Reset()
	ensureLocalGitIdentity(&out, bare, false)
	if !strings.Contains(out.String(), "no git identity") {
		t.Errorf("status reports the missing identity too, got %q", out.String())
	}
}

// The preview lists the commits without any identity and names the key to set;
// only --apply needs one.
func TestKBReauthorPreviewWithoutIdentity(t *testing.T) {
	e := newReauthorEnv(t, false)
	commitInKB(t, e.root, "a.md", "local-admin", "cartographer@host.example.test")
	out, errOut, code := e.run(t, false, false)
	if code != 0 || !strings.Contains(out, "local-admin <cartographer@host.example.test>") || !strings.Contains(out, "git.identities.default") {
		t.Errorf("code=%d out=%q err=%q, want the commit listed and the key named", code, out, errOut)
	}
	out, _, code = e.run(t, false, true)
	if code != 0 || !strings.Contains(out, `"identity_missing"`) {
		t.Errorf("json preview code=%d out=%s", code, out)
	}
}

// A token server on a loopback URL (an SSH tunnel) is a shared server: connect
// never writes the user's identity into its file.
func TestEnsureLocalGitIdentity_SkipsATokenServer(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	body := "http: \"127.0.0.1:39273\"\nauth:\n  tokens:\n    - token: tok-example-0123456789abcdef\n      scopes: [\"kb:kb-a:rw\"]\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	stubLocalIdent(t, "Ada Example <ada@example.test>", false)
	var out bytes.Buffer
	ensureLocalGitIdentity(&out, cfgPath, true)
	if data, _ := os.ReadFile(cfgPath); string(data) != body || out.Len() != 0 {
		t.Errorf("a token server was touched: out=%q file:\n%s", out.String(), data)
	}
	stubLocalIdent(t, "", false)
	ensureLocalGitIdentity(&out, cfgPath, false)
	if out.Len() != 0 {
		t.Errorf("a token server is told about a missing identity: %q", out.String())
	}
}
