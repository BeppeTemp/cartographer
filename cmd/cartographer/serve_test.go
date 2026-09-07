package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/config"
)

// TestResolveSopsAgeKeyFile covers the fallback chain (D53): explicit
// spec.SopsAgeKeyFile wins; otherwise <AgeKeyDir>/<name>.age if present;
// otherwise the global Sops.AgeKeyFile.
func TestResolveSopsAgeKeyFile(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "wiki-kb.age")
	if err := os.WriteFile(keyPath, []byte("age-key-material"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("explicit override wins", func(t *testing.T) {
		spec := config.KBSpec{SopsAgeKeyFile: "/explicit/key.age"}
		sops := config.SopsConfig{AgeKeyDir: dir, AgeKeyFile: "/global/key.age"}
		if got := resolveSopsAgeKeyFile(spec, sops, "wiki-kb"); got != "/explicit/key.age" {
			t.Errorf("got %q, want /explicit/key.age", got)
		}
	})

	t.Run("age_key_dir convention when file exists", func(t *testing.T) {
		sops := config.SopsConfig{AgeKeyDir: dir, AgeKeyFile: "/global/key.age"}
		if got := resolveSopsAgeKeyFile(config.KBSpec{}, sops, "wiki-kb"); got != keyPath {
			t.Errorf("got %q, want %q", got, keyPath)
		}
	})

	t.Run("falls back to global when convention file absent", func(t *testing.T) {
		sops := config.SopsConfig{AgeKeyDir: dir, AgeKeyFile: "/global/key.age"}
		if got := resolveSopsAgeKeyFile(config.KBSpec{}, sops, "no-such-kb"); got != "/global/key.age" {
			t.Errorf("got %q, want /global/key.age", got)
		}
	})

	t.Run("falls back to global when age_key_dir unset", func(t *testing.T) {
		sops := config.SopsConfig{AgeKeyFile: "/global/key.age"}
		if got := resolveSopsAgeKeyFile(config.KBSpec{}, sops, "wiki-kb"); got != "/global/key.age" {
			t.Errorf("got %q, want /global/key.age", got)
		}
	})
}

func TestCompleteIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, nameValue, email string
		wantErr                bool
	}{
		{name: "global complete", nameValue: "Bot", email: "bot@example.test"},
		{name: "per KB complete", nameValue: "KB Bot", email: "kb@example.test"},
		{name: "global partial author", nameValue: "Bot", wantErr: true},
		{name: "global partial email", email: "bot@example.test", wantErr: true},
		{name: "per KB partial author", nameValue: "Bot", wantErr: true},
		{name: "per KB partial email", email: "bot@example.test", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := completeIdentity(tc.nameValue, tc.email, tc.name)
			if (err != nil) != tc.wantErr {
				t.Fatalf("completeIdentity(%q, %q) error = %v", tc.nameValue, tc.email, err)
			}
		})
	}
}

// --- D179: the effective configuration is validated, not just the YAML ---

// TestLoadServeConfigValidatesEnvSuppliedScopes: an invalid scope arriving from
// the environment must stop startup. Validating only at YAML load left every
// environment- and flag-supplied token unchecked, and an unparsed scope used to
// widen a token to full admin rather than fail.
func TestLoadServeConfigValidatesEnvSuppliedScopes(t *testing.T) {
	cases := []struct {
		name    string
		tokens  string
		wantErr bool
	}{
		{"valid scope", "tok|kb:docs:rw", false},
		{"bare token stays a legacy admin", "tok", false},
		{"scope missing its access segment", "tok|kb:docs", true},
		{"unknown access value", "tok|kb:docs:write", true},
		{"entry with no token half", "|kb:docs:r", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CARTOGRAPHER_TOKENS", tc.tokens)
			t.Setenv("CARTOGRAPHER_CONFIG", "")

			fs := flag.NewFlagSet("serve", flag.ContinueOnError)
			_, err := loadServeConfig(fs, config.FlagOverrides{}, "")
			if (err != nil) != tc.wantErr {
				t.Fatalf("loadServeConfig err = %v, wantErr = %v", err, tc.wantErr)
			}
			if err != nil && strings.Contains(err.Error(), "tok") && !strings.Contains(err.Error(), "token 0") {
				t.Errorf("the diagnostic leaked the token value: %q", err)
			}
		})
	}
}

// TestScopedTokensWithRolesDeclaresLegacyAdmin: the intent has to be expressed
// at this layer, because only it can tell "no restriction was written" apart
// from "the restrictions written produced nothing".
func TestScopedTokensWithRolesDeclaresLegacyAdmin(t *testing.T) {
	out := scopedTokensWithRoles([]config.TokenSpec{
		{Token: "legacy"},
		{Token: "scoped", Scopes: []string{"kb:docs:r"}},
	}, nil)

	if !out[0].Policy.Admin {
		t.Error("a token with neither scopes nor roles must be declared admin")
	}
	if out[1].Policy.Admin {
		t.Error("a scoped token must not be admin")
	}
}
