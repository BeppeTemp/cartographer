package config

import (
	"strings"
	"testing"
	"time"
)

// AC-tls-native-3: the four tls.* keys parse from YAML, the environment and the
// flag override the file, and the defaults are the documented ones.
func TestTLSSettingsParseAndDefaults(t *testing.T) {
	cfg, err := Load(writeCfg(t, "http: \":8443\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TLS.Enabled || cfg.TLS.HealthAddr != "" || cfg.TLS.HSTSMaxAge != 4380*time.Hour || cfg.TLS.ReloadInterval != 30*time.Second {
		t.Errorf("defaults = %+v", cfg.TLS)
	}
	cfg.TLS.Enabled = true
	if got := TLSHealthAddr(cfg.HTTP, cfg.TLS); got != "127.0.0.1:8444" {
		t.Errorf("derived health addr = %q", got)
	}

	cfg, err = Load(writeCfg(t, `http: ":8443"
tls:
  enabled: true
  health_addr: "[::1]:9000"
  hsts_max_age: 0s
  reload_interval: 5s
`))
	if err != nil {
		t.Fatal(err)
	}
	want := TLSConfig{Enabled: true, HealthAddr: "[::1]:9000", HSTSMaxAge: 0, ReloadInterval: 5 * time.Second}
	if cfg.TLS != want {
		t.Errorf("TLS = %+v, want %+v", cfg.TLS, want)
	}
	if cfg.Origins["tls.enabled"] != OriginFile || cfg.Origins["tls.hsts_max_age"] != OriginFile {
		t.Errorf("origins = %v", cfg.Origins)
	}
	if got := TLSHealthAddr(cfg.HTTP, cfg.TLS); got != "[::1]:9000" {
		t.Errorf("explicit health addr = %q", got)
	}
	if got := cfg.LocalProbeAddr(); got != "[::1]:9000" {
		t.Errorf("LocalProbeAddr = %q", got)
	}

	// Env over file, flag over env.
	t.Setenv("CARTOGRAPHER_TLS_ENABLED", "off")
	if err := FromEnv(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.TLS.Enabled || cfg.Origins["tls.enabled"] != OriginEnv {
		t.Errorf("env: enabled=%v origin=%v", cfg.TLS.Enabled, cfg.Origins["tls.enabled"])
	}
	on := true
	ApplyFlags(cfg, FlagOverrides{TLSEnabled: &on})
	if !cfg.TLS.Enabled || cfg.Origins["tls.enabled"] != OriginFlag {
		t.Errorf("flag: enabled=%v origin=%v", cfg.TLS.Enabled, cfg.Origins["tls.enabled"])
	}
	if got := (&Config{HTTP: ":8443"}).LocalProbeAddr(); got != ":8443" {
		t.Errorf("TLS off: LocalProbeAddr = %q, want the public address", got)
	}
	t.Setenv("CARTOGRAPHER_TLS_ENABLED", "maybe")
	if err := FromEnv(Default()); err == nil || !strings.Contains(err.Error(), "CARTOGRAPHER_TLS_ENABLED") {
		t.Errorf("a bad boolean must name the variable: %v", err)
	}
}

// AC-tls-native-4: a bad tls setting is refused at load naming the key.
func TestTLSSettingsRefused(t *testing.T) {
	for name, tc := range map[string]struct{ yaml, want string }{
		"bad duration":      {"tls:\n  hsts_max_age: soon\n", "tls.hsts_max_age"},
		"negative duration": {"tls:\n  reload_interval: -1s\n", "tls.reload_interval"},
		"non loopback":      {"http: \":8443\"\ntls:\n  health_addr: \"10.0.0.5:9000\"\n", "tls.health_addr"},
		"wildcard":          {"http: \":8443\"\ntls:\n  health_addr: \":9000\"\n", "tls.health_addr"},
		"no port":           {"tls:\n  health_addr: \"127.0.0.1\"\n", "tls.health_addr"},
		"same as public":    {"http: \"127.0.0.1:8443\"\ntls:\n  health_addr: \"127.0.0.1:8443\"\n", "must differ"},
		"port 65535":        {"http: \":65535\"\ntls:\n  enabled: true\n", "tls.health_addr"},
		"port 0":            {"http: \":0\"\ntls:\n  enabled: true\n", "tls.health_addr"},
	} {
		_, err := Load(writeCfg(t, tc.yaml))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want one naming %q", name, err, tc.want)
		}
	}
	// off needs no derivable default; the default is not derived with TLS off.
	for _, y := range []string{
		"http: \":65535\"\ntls:\n  enabled: true\n  health_addr: off\n",
		"http: \":65535\"\n",
	} {
		if _, err := Load(writeCfg(t, y)); err != nil {
			t.Errorf("%q: %v", y, err)
		}
	}
	if got := TLSHealthAddr(":8443", TLSConfig{Enabled: true, HealthAddr: TLSHealthOff}); got != "" {
		t.Errorf("off: %q", got)
	}

	// client_auth stays free for a later release: today it is an unknown key,
	// and under tls: an unknown key is a load error, not a warning (a stale
	// security key must not be silently ignored).
	_, err := Load(writeCfg(t, "tls:\n  client_auth: require\n"))
	if err == nil || !strings.Contains(err.Error(), "client_auth") || !strings.Contains(err.Error(), "hsts_max_age") {
		t.Errorf("unknown tls key: err = %v, want one naming it and the valid keys", err)
	}
}
