package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/config"
)

func expiredFixture(t *testing.T, body string) *config.Config {
	t.Helper()
	t.Setenv("CARTOGRAPHER_TOKENS", "")
	t.Setenv("CARTOGRAPHER_AUTH", "")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadServeConfig(serveFlagSet(), config.FlagOverrides{}, path)
	if err != nil {
		t.Fatalf("a token that expired while configured must not stop the start: %v", err)
	}
	return cfg
}

const expiredEntry = "    - token: tok-old-secret-0123\n      id: old\n      kind: client\n      expires_at: \"2020-01-01T00:00:00Z\"\n      scopes: [kb:kb-a:r]\n"

// Start with an expired token and a valid one: the config loads, the store
// refuses the expired one and accepts the other, and the log names the id and
// date but never the value (D-new-expired-token-at-start).
func TestStartKeepsAnExpiredTokenRefused(t *testing.T) {
	cfg := expiredFixture(t, reloadBase+expiredEntry)
	buf := reloadLog(t)
	scoped := scopedTokensWithRoles(cfg.Auth.Tokens, cfg.Auth.Roles)
	store := auth.NewScopedTokenStore(scoped).RequireAuth()
	logExpiredTokens(scoped, time.Now())
	if _, ok := store.Validate("tok-old-secret-0123"); ok {
		t.Error("the expired token is accepted")
	}
	if _, ok := store.Validate("tok-aaaa-0123456789"); !ok {
		t.Error("the valid token is refused")
	}
	out := buf.String()
	if !strings.Contains(out, `"old"`) || !strings.Contains(out, "2020-01-01T00:00:00Z") {
		t.Errorf("the expired token is not named with its date:\n%s", out)
	}
	if strings.Contains(out, "tok-old-secret") || strings.Contains(out, "tok-aaaa") {
		t.Errorf("a token value reached the log:\n%s", out)
	}
}

// Every token expired: no fatal, and authentication still enforces (never
// fail-open): resolveAuth turns it on and the store refuses the request.
func TestStartAllExpiredStillEnforcesAuth(t *testing.T) {
	cfg := expiredFixture(t, "http: \"127.0.0.1:9090\"\nauth:\n  mode: auto\n  tokens:\n"+expiredEntry)
	on, err := resolveAuth(cfg.Auth)
	if err != nil || !on {
		t.Fatalf("resolveAuth = %v, %v; want on", on, err)
	}
	scoped := scopedTokensWithRoles(cfg.Auth.Tokens, cfg.Auth.Roles)
	store := auth.NewScopedTokenStore(scoped).RequireAuth()
	if !store.IsEnabled() || store.TokenCount() == 0 {
		t.Errorf("store enabled = %v, count = %d; want enforcing with the token counted", store.IsEnabled(), store.TokenCount())
	}
	if _, ok := store.Validate("tok-old-secret-0123"); ok {
		t.Error("the expired token is accepted")
	}
}

// An unparseable expires_at stays a start error.
func TestStartUnparseableExpiryStaysFatal(t *testing.T) {
	t.Setenv("CARTOGRAPHER_TOKENS", "")
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := reloadBase + "    - token: tok-bad-0123456789\n      id: bad\n      kind: client\n      expires_at: \"tomorrow\"\n      scopes: [kb:kb-a:r]\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadServeConfig(serveFlagSet(), config.FlagOverrides{}, path); err == nil || !strings.Contains(err.Error(), "RFC 3339") {
		t.Errorf("err = %v, want the RFC 3339 error", err)
	}
}
