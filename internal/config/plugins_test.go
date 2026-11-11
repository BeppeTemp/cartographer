package config

import (
	"strings"
	"testing"
)

const goodDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestPlugins_Load(t *testing.T) {
	dir := t.TempDir()
	cfg, err := loadComponents(t, "plugins_dir: "+dir+"\nplugins:\n  alpha:\n    digest: "+goodDigest+"\n    settings: {limit: 3, label: x}\n  beta:\n    digest: "+goodDigest+"\n    enabled: false\n")
	if err != nil {
		t.Fatal(err)
	}
	pins := cfg.PluginPins()
	if cfg.PluginsDir != dir || len(pins) != 2 || !pins["alpha"].Enabled || pins["beta"].Enabled || pins["alpha"].Digest != goodDigest {
		t.Fatalf("%q %+v", cfg.PluginsDir, pins)
	}
	if pins := cfg.Plugins; pins["alpha"].Line != 3 || pins["beta"].Line != 6 {
		t.Fatalf("lines: %+v", pins)
	}
	if cfg, err = loadComponents(t, "web:\n  enabled: true\n"); err != nil || len(cfg.Plugins) != 0 || len(cfg.PluginPins()) != 0 {
		t.Fatalf("absent keys change nothing: %v %v", cfg, err)
	}
	if _, err = loadComponents(t, "plugins_dir: relative/store\n"); err != nil {
		t.Fatalf("plugins_dir alone is harmless: %v", err)
	}
}

func TestPlugins_Refusals(t *testing.T) {
	dir := t.TempDir()
	for name, tc := range map[string]struct{ body, want string }{
		"no dir":        {"plugins:\n  alpha: {digest: " + goodDigest + "}\n", "plugins_dir: required"},
		"relative dir":  {"plugins_dir: store\nplugins:\n  alpha: {digest: " + goodDigest + "}\n", "must be an absolute path"},
		"no digest":     {"plugins_dir: " + dir + "\nplugins:\n  alpha: {enabled: true}\n", "plugins.alpha: the digest is not 64"},
		"short digest":  {"plugins_dir: " + dir + "\nplugins:\n  alpha: {digest: abc}\n", "plugins.alpha: the digest is not 64"},
		"upper digest":  {"plugins_dir: " + dir + "\nplugins:\n  alpha: {digest: " + strings.ToUpper(goodDigest) + "}\n", "plugins.alpha: the digest is not 64"},
		"bad name":      {"plugins_dir: " + dir + "\nplugins:\n  ../x: {digest: " + goodDigest + "}\n", "not a valid plugin name"},
		"unknown key":   {"plugins_dir: " + dir + "\nplugins:\n  alpha: {digest: " + goodDigest + ", digests: x}\n", `unknown key "digests" under plugins.alpha`},
		"not a mapping": {"plugins_dir: " + dir + "\nplugins:\n  alpha: " + goodDigest + "\n", "cannot unmarshal"},
		"nested value":  {"plugins_dir: " + dir + "\nplugins:\n  alpha: {digest: " + goodDigest + ", settings: {k: [1]}}\n", "plugins.alpha.settings.k: must be a scalar"},
		"disabled bad":  {"plugins_dir: " + dir + "\nplugins:\n  alpha: {digest: nope, enabled: false}\n", "plugins.alpha: the digest is not 64"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadComponents(t, tc.body)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}
