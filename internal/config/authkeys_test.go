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

func TestAuthKeysThroughMergeKeys(t *testing.T) {
	for name, y := range map[string]string{
		"sequence merge": "x: &a {scope: [\"kb:a:r\"]}\nauth:\n  tokens:\n    - {<<: [*a], token: t1}\n",
		"single merge":   "x: &a {scope: [\"kb:a:r\"]}\nauth:\n  tokens:\n    - {<<: *a, token: t1}\n",
		"root merge":     "base: &b\n  auth:\n    tokens:\n      - {token: t1, scope: [\"kb:a:r\"]}\n",
	} {
		t.Run(name, func(t *testing.T) {
			// the root-merge case needs the merge at the root
			if name == "root merge" {
				y = "base: &b\n  auth:\n    tokens:\n      - {token: t1, scope: [\"kb:a:r\"]}\n<<: *b\n"
			}
			_, err := loadAuthYAML(t, y)
			if err == nil || !strings.Contains(err.Error(), `"scope"`) {
				t.Fatalf("want an error naming scope, got %v", err)
			}
		})
	}
}

func TestMisspelledTopLevelSectionIsRefused(t *testing.T) {
	for _, y := range []string{
		"Auth:\n  tokens: [abc]\n",
		"tokens: [abc]\n",
		"roles: []\n",
	} {
		if _, err := loadAuthYAML(t, y); err == nil {
			t.Errorf("%q: expected an error", y)
		}
	}
}

func TestEmptyScopeListIsNotAdmin(t *testing.T) {
	for name, y := range map[string]string{
		"empty list": "auth:\n  tokens:\n    - {token: abc, scopes: []}\n",
		"null":       "auth:\n  tokens:\n    - {token: abc, scopes: }\n",
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := loadAuthYAML(t, y)
			if err == nil {
				err = ValidateAuth(cfg.Auth)
			}
			if err == nil {
				t.Fatal("expected an error")
			}
		})
	}
	// The admin token stays expressible: no scopes, no roles at all.
	cfg, err := loadAuthYAML(t, "auth:\n  tokens:\n    - abc\n    - {token: def}\n    - {token: ghi, scopes: [], roles: []}\n")
	if err == nil {
		err = ValidateAuth(cfg.Auth)
	}
	if err == nil || !strings.Contains(err.Error(), "empty scope list") {
		t.Fatalf("explicit empty lists must be refused, got %v", err)
	}
	cfg, err = loadAuthYAML(t, "auth:\n  tokens:\n    - abc\n    - {token: def}\n")
	if err != nil || ValidateAuth(cfg.Auth) != nil {
		t.Fatalf("admin tokens must load: %v", err)
	}
}

func TestEnvTokenEmptyScopesAndSpaces(t *testing.T) {
	for _, v := range []string{"tok|", "tok|;", "tok| ; "} {
		specs := parseTokenSpecs(v)
		if len(specs) != 1 || ValidateAuth(AuthConfig{Tokens: specs}) == nil {
			t.Errorf("%q: want one token refused by ValidateAuth, got %+v", v, specs)
		}
	}
	specs := parseTokenSpecs("tok| kb:a:r ; kb:b:rw, adm")
	if len(specs) != 2 || len(specs[0].Scopes) != 2 || specs[0].Scopes[0] != "kb:a:r" || specs[0].Scopes[1] != "kb:b:rw" {
		t.Fatalf("spaces around scopes must be trimmed: %+v", specs)
	}
	if specs[1].Token != "adm" || len(specs[1].Scopes) != 0 || specs[1].ScopesDeclared {
		t.Errorf("bare token is the admin form: %+v", specs[1])
	}
	if err := ValidateAuth(AuthConfig{Tokens: specs}); err != nil {
		t.Error(err)
	}
}
