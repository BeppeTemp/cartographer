package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOriginDefaultLayer(t *testing.T) {
	c := Default()
	for _, p := range []string{"credentials_dir", "http", "kbs[a].write_gate"} {
		if got := c.OriginOf(p); got != OriginDefault {
			t.Errorf("%s origin = %s, want default", p, got)
		}
	}
}

func TestOriginFileLayer(t *testing.T) {
	c, err := Load(writeCfg(t, "http: \":1\"\nauth:\n  mode: on\nkbs:\n  - remote: https://example.com/u/kb-a.git\n    write_gate: error\n  - remote: https://example.com/u/kb-b.git\n    name: renamed\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"http", "auth.mode", "kbs", "kbs[kb-a].remote", "kbs[kb-a].write_gate", "kbs[renamed].remote", "kbs[renamed].name"} {
		if got := c.OriginOf(p); got != OriginFile {
			t.Errorf("%s origin = %s, want file", p, got)
		}
	}
	if got := c.OriginOf("credentials_dir"); got != OriginDefault {
		t.Errorf("credentials_dir origin = %s, want default", got)
	}
	if got := c.OriginOf("kbs[kb-a].doctor_budget"); got != OriginDefault {
		t.Errorf("unset per-KB key origin = %s, want default", got)
	}
}

// A value written in the file equal to its default is still the file's.
func TestOriginFileExplicitDefaultValue(t *testing.T) {
	c, err := Load(writeCfg(t, "credentials_dir: /k\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.OriginOf("credentials_dir"); got != OriginFile {
		t.Errorf("origin = %s, want file", got)
	}
}

func TestOriginEnvOverridesFile(t *testing.T) {
	c, err := Load(writeCfg(t, "http: \":1\"\ncredentials_dir: /k\n"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CARTOGRAPHER_HTTP", ":2")
	t.Setenv("CARTOGRAPHER_KB", "/data/kb-env")
	FromEnv(c)
	if got := c.OriginOf("http"); got != OriginEnv {
		t.Errorf("http origin = %s, want env", got)
	}
	if got := c.OriginOf("credentials_dir"); got != OriginFile {
		t.Errorf("credentials_dir origin = %s, want file", got)
	}
	if got := c.OriginOf("kbs[kb-env].path"); got != OriginEnv {
		t.Errorf("env KB origin = %s, want env", got)
	}
}

func TestOriginFlagOverridesEnv(t *testing.T) {
	c := Default()
	t.Setenv("CARTOGRAPHER_TOOLS_PROFILE", "full")
	FromEnv(c)
	if got := c.OriginOf("tools.profile"); got != OriginEnv {
		t.Fatalf("origin = %s, want env", got)
	}
	full, kb := "agent", "/x/kb-flag"
	ApplyFlags(c, FlagOverrides{ToolsProfile: &full, KB: &kb})
	if got := c.OriginOf("tools.profile"); got != OriginFlag {
		t.Errorf("origin = %s, want flag", got)
	}
	if got := c.OriginOf("kbs[kb-flag].path"); got != OriginFlag {
		t.Errorf("flag KB origin = %s, want flag", got)
	}
}

func TestOriginSurvivesReordering(t *testing.T) {
	a, _ := Load(writeCfg(t, "kbs:\n  - remote: https://example.com/u/kb-a.git\n    write_gate: error\n  - remote: https://example.com/u/kb-b.git\n"))
	b, _ := Load(writeCfg(t, "kbs:\n  - remote: https://example.com/u/kb-b.git\n  - remote: https://example.com/u/kb-a.git\n    write_gate: error\n"))
	for _, c := range []*Config{a, b} {
		if c.OriginOf("kbs[kb-a].write_gate") != OriginFile || c.OriginOf("kbs[kb-b].write_gate") != OriginDefault {
			t.Errorf("origins by name wrong: %v", c.Origins)
		}
	}
}

// Every server-level schema key has a value in the view, so a key added to the
// schema cannot silently drop out of the config view.
func TestServerValueCoversSchema(t *testing.T) {
	c := Default()
	for _, k := range Schema.Keys {
		if strings.HasPrefix(k.Path, "kbs[].") {
			continue
		}
		if v, _ := c.serverValue(k.Path); v == "unknown key "+k.Path {
			t.Errorf("serverValue has no case for %s", k.Path)
		}
	}
	v := c.Settings([]string{"kb-a"})
	if len(v.KBs) != 1 || len(v.KBs[0].Entries) == 0 {
		t.Fatalf("view: %+v", v.KBs)
	}
}

func TestSettingsRedactsSecrets(t *testing.T) {
	c := Default()
	c.Auth.Tokens = []TokenSpec{{Token: "tok-secret-1"}}
	c.Audit.KeySeed = "seed-secret-2"
	c.KBs = []KBSpec{{Remote: "https://example.com/u/kb-a.git"}}
	v := c.Settings([]string{"kb-a"})
	var b strings.Builder
	for _, e := range v.Server {
		b.WriteString(e.Key + "=" + toString(e.Value) + "\n")
	}
	for _, e := range v.KBs[0].Entries {
		b.WriteString(e.Key + "=" + toString(e.Value) + "\n")
	}
	out := b.String()
	for _, secret := range []string{"tok-secret-1", "seed-secret-2"} {
		if strings.Contains(out, secret) {
			t.Errorf("secret %q leaked into the view", secret)
		}
	}
	for _, want := range []string{"auth.tokens=(set)", "audit.key_seed=(set)"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}
	if v2 := Default().Settings(nil); toString(find(v2.Server, "auth.tokens").Value) != SecretUnset {
		t.Errorf("unset secret = %v", find(v2.Server, "auth.tokens").Value)
	}
}

func find(es []Entry, key string) Entry {
	for _, e := range es {
		if e.Key == key {
			return e
		}
	}
	return Entry{}
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
