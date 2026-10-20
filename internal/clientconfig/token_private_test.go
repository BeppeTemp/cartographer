package clientconfig_test

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

func modeOf(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// A file holding a literal token ends up private on the unchanged-bytes Save
// path, on the realignment (file and backup), and Load warns while it is open.
func TestLiteralTokenFileIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits mean nothing on Windows")
	}
	dir := t.TempDir()
	cfg := clientconfig.Default()
	cfg.Auth, cfg.Token = true, "s3cr3t-literal"
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	p := clientconfig.Path(dir)
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(loaded.Warnings, "\n"), "chmod 600") || strings.Contains(strings.Join(loaded.Warnings, "\n"), "s3cr3t-literal") {
		t.Errorf("Load warnings = %v", loaded.Warnings)
	}
	// Save of the same content: bytes unchanged, mode must still be tightened.
	if err := clientconfig.Save(dir, loaded); err != nil {
		t.Fatal(err)
	}
	if m := modeOf(t, p); m&0o077 != 0 {
		t.Errorf("unchanged Save left mode %04o", m)
	}

	// Realignment: file and backup stay private.
	if err := os.WriteFile(p, []byte("server_url: http://127.0.0.1:39273/mcp\ntoken: s3cr3t-literal\ntrust: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, backup, err := clientconfig.RealignFile(dir, "0.30.0")
	if err != nil {
		t.Fatal(err)
	}
	if backup == "" {
		t.Fatal("expected a backup")
	}
	for _, f := range []string{p, backup} {
		if m := modeOf(t, f); m&0o077 != 0 {
			t.Errorf("%s has mode %04o", f, m)
		}
	}
}

// token_env is never lost without a word.
func TestRealignTokenEnvIsNeverDroppedSilently(t *testing.T) {
	cases := []struct {
		name, yaml, wantToken, wantInReport string
	}{
		{"empty token", "token: \"\"\ntoken_env: MY_VAR\n", "token: ${MY_VAR}", "token_env"},
		{"literal token wins", "token: s3cr3t-literal\ntoken_env: MY_VAR\n", "token: s3cr3t-literal", "MY_VAR"},
		{"ref token wins", "token: ${OTHER}\ntoken_env: MY_VAR\n", "token: ${OTHER}", "${OTHER}"},
		{"invalid name", "token_env: not a name\n", "", "not an environment variable name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(clientconfig.Path(dir), []byte(c.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			rep, _, err := clientconfig.RealignFile(dir, "0.30.0")
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Join(rep.Lines(), "\n")
			if !strings.Contains(lines, "token_env") || !strings.Contains(lines, c.wantInReport) {
				t.Errorf("report does not name token_env / %q:\n%s", c.wantInReport, lines)
			}
			if strings.Contains(lines, "s3cr3t-literal") {
				t.Errorf("report leaks the literal token:\n%s", lines)
			}
			raw := readFile(t, clientconfig.Path(dir))
			if c.wantToken != "" && !strings.Contains(raw, c.wantToken) {
				t.Errorf("file lacks %q:\n%s", c.wantToken, raw)
			}
			if hasKey(raw, "token_env") {
				t.Errorf("token_env survived:\n%s", raw)
			}
		})
	}
	// Load, before any realignment, says the same for a conflicting pair.
	dir := t.TempDir()
	if err := os.WriteFile(clientconfig.Path(dir), []byte("token: ${OTHER}\ntoken_env: MY_VAR\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(cfg.Warnings, "\n"), "MY_VAR") {
		t.Errorf("Load warnings = %v", cfg.Warnings)
	}
}
