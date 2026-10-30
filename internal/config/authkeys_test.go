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
	if err == nil || !strings.Contains(err.Error(), "empty scope or role list") {
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

func TestEmptyRoleListAndMergedEmptyListsAreNotAdmin(t *testing.T) {
	for name, y := range map[string]string{
		"empty roles":         "auth:\n  tokens:\n    - {token: t, roles: []}\n",
		"merged empty scopes": "x: &a {scopes: []}\nauth:\n  tokens:\n    - {<<: *a, token: t1}\n",
		"merged empty roles":  "x: &a {roles: []}\nauth:\n  tokens:\n    - {<<: [*a], token: t1}\n",
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := loadAuthYAML(t, y)
			if err == nil {
				err = ValidateAuth(cfg.Auth)
			}
			if err == nil || !strings.Contains(err.Error(), "empty scope or role list") {
				t.Fatalf("want the empty list refused, got %v", err)
			}
		})
	}
}

func TestLoadRefusesEmptyRoleSelectors(t *testing.T) {
	for _, key := range []string{"maps", "journals", "types"} {
		for name, val := range map[string]string{"empty list": " []", "bare key": ""} {
			body := "auth:\n  roles:\n    - name: r\n      rules:\n        - kb: kb-a\n          access: r\n          " + key + ":" + val + "\n"
			_, err := loadAuthYAML(t, body)
			if err == nil {
				t.Errorf("%s as %s: want an error", key, name)
				continue
			}
			for _, want := range []string{`"r"`, "rule 0", key} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%s as %s: error %q lacks %q", key, name, err, want)
				}
			}
		}
	}
	// Omitted keys still mean "all"; a non-empty list is fine.
	ok := "auth:\n  roles:\n    - name: r\n      rules:\n        - kb: kb-a\n          access: r\n        - kb: kb-b\n          access: rw\n          maps: [infra]\n"
	if _, err := loadAuthYAML(t, ok); err != nil {
		t.Fatalf("omitted selectors must load: %v", err)
	}
}

func TestLoadRefusesMisspelledAuthSection(t *testing.T) {
	for _, key := range []string{"auht", "authentication", "auth_tokens", "token", "Auth", "authz", "auths", "aut"} {
		_, err := loadAuthYAML(t, key+":\n  mode: on\n")
		if err == nil || !strings.Contains(err.Error(), `did you mean "auth"`) {
			t.Errorf("top-level %q: want a did-you-mean error, got %v", key, err)
		}
	}
	for _, key := range []string{"retired_key", "audit_extra", "web_ui"} {
		if _, err := loadAuthYAML(t, key+": 1\n"); err != nil {
			t.Errorf("top-level %q must stay lenient: %v", key, err)
		}
	}
}

func TestShippedAuthExamplesLoad(t *testing.T) {
	t.Setenv("CARTOGRAPHER_TOKEN", "example-token")
	if _, err := Load(filepath.Join("..", "..", "config.example.yaml")); err != nil {
		t.Fatalf("config.example.yaml: %v", err)
	}
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "deployment.md"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, blk := range strings.Split(string(doc), "```yaml\n")[1:] {
		body, _, _ := strings.Cut(blk, "```")
		if !strings.Contains(body, "\nauth:") && !strings.HasPrefix(body, "auth:") {
			continue
		}
		n++
		body = strings.NewReplacer("[...]", "[]").Replace(body)
		if _, err := loadAuthYAML(t, body); err != nil {
			t.Errorf("docs/deployment.md auth example %d does not load: %v\n%s", n, err, body)
		}
	}
	if n == 0 {
		t.Fatal("no auth example found in docs/deployment.md")
	}
}

func TestRuleArtifactsKey(t *testing.T) {
	role := func(rule string) string {
		return "auth:\n  roles:\n    - name: w\n      rules:\n        - " + rule + "\n  tokens:\n    - {token: abc, roles: [w]}\n"
	}
	for rule, ok := range map[string]bool{
		"{kb: kb-a, access: rw, artifacts: r}":            true,
		"{kb: kb-a, access: rw, artifacts: rw}":           true,
		"{kb: kb-a, access: rw}":                          true,
		"{kb: kb-a, access: r, artifacts: r}":             true,
		"{kb: kb-a, access: rw, maps: [m], artifacts: r}": true,
		"{kb: kb-a, access: r, artifacts: rw}":            false,
		"{kb: kb-a, access: rw, artifacts: write}":        false,
		"{kb: kb-a, access: rw, artefacts: r}":            false,
	} {
		cfg, err := loadAuthYAML(t, role(rule))
		if err == nil {
			err = ValidateAuth(cfg.Auth)
		}
		if (err == nil) != ok {
			t.Errorf("%s: err = %v, want ok=%v", rule, err, ok)
		}
	}
}
