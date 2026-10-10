package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A secret field written as "${NAME}" is read from the environment (#680):
// the docs show it, and the alternative is a plaintext token in the file.
func TestLoadExpandsEnvRefsInSecretFields(t *testing.T) {
	t.Setenv("CARTO_TEST_TOK", "s3cret")
	t.Setenv("CARTO_TEST_SEED", "abcd")
	path := filepath.Join(t.TempDir(), "c.yaml")
	yaml := "auth:\n  tokens:\n    - token: ${CARTO_TEST_TOK}\n      id: bot\n      author_name: bot\n      author_email: bot@example.com\n    - literal-token\naudit:\n  key_seed: ${CARTO_TEST_SEED}\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Auth.Tokens[0].Token; got != "s3cret" {
		t.Errorf("tokens[0] = %q, want the env value", got)
	}
	if got := cfg.Auth.Tokens[1].Token; got != "literal-token" {
		t.Errorf("tokens[1] = %q, a literal stays as written", got)
	}
	if cfg.Audit.KeySeed != "abcd" {
		t.Errorf("key_seed = %q, want the env value", cfg.Audit.KeySeed)
	}
}

// An unset variable refuses startup, naming the field: an empty token or seed
// would be a silent failure.
func TestLoadRefusesUnsetEnvRef(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(path, []byte("auth:\n  tokens:\n    - token: ${CARTO_TEST_UNSET_TOK}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "auth.tokens[0].token") || !strings.Contains(err.Error(), "CARTO_TEST_UNSET_TOK") {
		t.Fatalf("err = %v, want a refusal naming the field and the variable", err)
	}
}
