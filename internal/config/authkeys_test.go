package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadAuthYAML(t *testing.T, body string) (*Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "server.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(p)
}

func TestLoadRefusesUnknownAuthKey(t *testing.T) {
	cases := map[string]struct{ yaml, key string }{
		"scope for scopes": {"auth:\n  tokens:\n    - {token: abc, scope: [\"kb:docs:r\"]}\n", "scope"},
		"role for roles":   {"auth:\n  tokens:\n    - {token: abc, role: reader}\n", "role"},
		"map for maps":     {"auth:\n  roles:\n    - name: r\n      rules:\n        - {kb: docs, access: r, map: [infra]}\n", "map"},
		"auth level":       {"auth:\n  mod: \"on\"\n", "mod"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := loadAuthYAML(t, c.yaml)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), `"`+c.key+`"`) || !strings.Contains(err.Error(), "line") {
				t.Errorf("error should name the key %q and its line: %v", c.key, err)
			}
		})
	}
}

func TestLoadAcceptsKnownAuthKeys(t *testing.T) {
	cfg, err := loadAuthYAML(t, `auth:
  mode: "on"
  roles:
    - name: r
      rules:
        - {kb: docs, access: r, maps: [infra], journals: [j], types: [Runbook]}
  tokens:
    - plain
    - {token: abc, id: ci, scopes: ["kb:docs:r"], roles: [r]}
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Auth.Tokens) != 2 || len(cfg.Auth.Roles) != 1 {
		t.Errorf("tokens=%d roles=%d", len(cfg.Auth.Tokens), len(cfg.Auth.Roles))
	}
}

func TestUnknownKeyOutsideAuthStaysLenient(t *testing.T) {
	if _, err := loadAuthYAML(t, "retired_key: 1\n"); err != nil {
		t.Fatalf("a key outside auth must not stop the load: %v", err)
	}
}
