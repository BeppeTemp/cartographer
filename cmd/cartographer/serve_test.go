package main

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/audit"
	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/service"
)

// --- D905: the audit log is always on and signed ---

func auditTestConfig(t *testing.T, mode string) *config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Data = t.TempDir()
	cfg.Audit.Mode = mode
	return cfg
}

func appendAuditTestEntry(t *testing.T, l *audit.Log) {
	t.Helper()
	if err := l.Append(audit.Entry{Timestamp: time.Now(), Tool: "search", AgentID: "test", Outcome: "ok"}); err != nil {
		t.Fatal(err)
	}
}

// A fresh start opens the log under data and signs it with an audit.key it
// generates, 0600; the next start reads the same key. The seed is never logged,
// the public key a verification needs is.
func TestOpenAuditLogGeneratesTheKeyAndSigns(t *testing.T) {
	logs := captureLog(t)
	cfg := auditTestConfig(t, "")
	l, err := openAuditLog(cfg)
	if err != nil || l == nil {
		t.Fatalf("openAuditLog = %v, %v", l, err)
	}
	appendAuditTestEntry(t, l)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	keyPath := filepath.Join(cfg.CredentialsPath(), config.AuditKeyFile)
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("%s mode = %v, want 0600", keyPath, info.Mode().Perm())
	}
	kp, generated, err := loadAuditKey(cfg.CredentialsPath())
	if err != nil || generated {
		t.Fatalf("second load: generated=%v err=%v, want the key already there", generated, err)
	}
	sum, err := audit.VerifyAudit(cfg.AuditLogPath(), "", kp.Public)
	if err != nil || sum.Valid != 1 || sum.Unsigned != 0 {
		t.Errorf("verify = %+v, %v; want one signed entry", sum, err)
	}
	out := logs.String()
	if strings.Contains(out, kp.SeedToHex()) {
		t.Error("the audit seed is in the log")
	}
	for _, want := range []string{"audit key generated: " + keyPath, "--public-key "} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
}

// A credentials directory the server cannot write leaves the key missing: in
// best_effort the log runs unsigned after one warning naming the file, in
// required the server does not start.
func TestOpenAuditLogWithoutAWritableCredentialsDir(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A mode the log does not know is required there, so it is here too.
	for _, mode := range []string{"best_effort", "required", "requierd"} {
		t.Run(mode, func(t *testing.T) {
			logs := captureLog(t)
			cfg := auditTestConfig(t, mode)
			cfg.CredentialsDir = filepath.Join(blocker, "credentials")
			keyPath := filepath.Join(cfg.CredentialsDir, config.AuditKeyFile)
			l, err := openAuditLog(cfg)
			if mode != "best_effort" {
				if err == nil || l != nil || !strings.Contains(err.Error(), keyPath) {
					t.Fatalf("openAuditLog = %v, %v; want a refusal naming %s", l, err, keyPath)
				}
				return
			}
			if err != nil || l == nil {
				t.Fatalf("openAuditLog = %v, %v; want an unsigned log", l, err)
			}
			appendAuditTestEntry(t, l)
			if err := l.Close(); err != nil {
				t.Fatal(err)
			}
			if sum, err := audit.VerifyAudit(cfg.AuditLogPath(), "", nil); err != nil || sum.Unsigned != 1 {
				t.Errorf("verify = %+v, %v; want one unsigned entry", sum, err)
			}
			if out := logs.String(); !strings.Contains(out, "NOT signed") || !strings.Contains(out, keyPath) {
				t.Errorf("log lacks the warning naming %s:\n%s", keyPath, out)
			}
		})
	}
}

// A log has one writer: a second process on it (a second stdio session) runs
// without one in best_effort, and does not start in required.
func TestOpenAuditLogHasOneWriter(t *testing.T) {
	cfg := auditTestConfig(t, "best_effort")
	first, err := openAuditLog(cfg)
	if err != nil || first == nil {
		t.Fatalf("first = %v, %v", first, err)
	}
	defer first.Close()

	oldWait := audit.LockWait
	audit.LockWait = 50 * time.Millisecond
	t.Cleanup(func() { audit.LockWait = oldWait })
	logs := captureLog(t)
	second, err := openAuditLog(cfg)
	if err != nil || second != nil {
		t.Fatalf("second = %v, %v; want no log and no error", second, err)
	}
	if !strings.Contains(logs.String(), "WITHOUT an audit log") {
		t.Errorf("log lacks the warning:\n%s", logs)
	}
	cfg.Audit.Mode = "required"
	if _, err := openAuditLog(cfg); !errors.Is(err, audit.ErrLocked) {
		t.Errorf("required: err = %v, want ErrLocked", err)
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

// TestGeneratedLocalConfigIgnoresEnvTokens is the D268 contract end to end on
// the serve side: the server.yaml `service install` generates for a loopback
// address keeps auth off even with a CARTOGRAPHER_TOKENS in the environment
// (exported for another server), while CARTOGRAPHER_AUTH=on still turns it on
// deliberately, and a config without auth.mode keeps honouring the env tokens.
func TestGeneratedLocalConfigIgnoresEnvTokens(t *testing.T) {
	write := func(body string) string {
		p := filepath.Join(t.TempDir(), "server.yaml")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	generated := write(service.DefaultServerYAML(t.TempDir(), "127.0.0.1:39273"))
	legacy := write("http: \"127.0.0.1:39273\"\n")

	cases := []struct {
		name     string
		config   string
		authEnv  string
		wantAuth bool
	}{
		{"generated local config ignores env tokens", generated, "", false},
		{"CARTOGRAPHER_AUTH=on still enables auth", generated, "on", true},
		{"a config without auth.mode honours env tokens", legacy, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CARTOGRAPHER_TOKENS", "remote-token")
			t.Setenv("CARTOGRAPHER_AUTH", tc.authEnv)
			t.Setenv("CARTOGRAPHER_CONFIG", "")
			fs := flag.NewFlagSet("serve", flag.ContinueOnError)
			cfg, err := loadServeConfig(fs, config.FlagOverrides{}, tc.config)
			if err != nil {
				t.Fatalf("loadServeConfig: %v", err)
			}
			on, err := resolveAuth(cfg.Auth)
			if err != nil {
				t.Fatalf("resolveAuth: %v", err)
			}
			if on != tc.wantAuth {
				t.Errorf("auth enabled = %v, want %v (mode %q, %d token(s))", on, tc.wantAuth, cfg.Auth.Mode, len(cfg.Auth.Tokens))
			}
		})
	}
}
