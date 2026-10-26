package provisioning

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestCodexProjectTrusted: a Codex workspace projection is only active when the
// project is trusted, and absence of evidence is never evidence of trust.
func TestCodexProjectTrusted(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	ws := "/Users/someone/work/work"
	other := "/Users/someone/work/homelab"

	// No config at all.
	if CodexProjectTrusted(cfg, ws) {
		t.Error("a missing config reported the project as trusted")
	}

	content := `
model = "o3"

[projects."/Users/someone/work/work"]
trust_level = "trusted"

[projects."/Users/someone/work/homelab"]
trust_level = "untrusted"
`
	if err := os.WriteFile(cfg, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if !CodexProjectTrusted(cfg, ws) {
		t.Error("a trusted project was reported as untrusted")
	}
	if CodexProjectTrusted(cfg, other) {
		t.Error("an explicitly untrusted project was reported as trusted")
	}
	if CodexProjectTrusted(cfg, "/Users/someone/work/absent") {
		t.Error("a project with no record was reported as trusted")
	}
	// A trailing separator names the same directory.
	if !CodexProjectTrusted(cfg, ws+"/") {
		t.Error("a trailing separator changed the answer")
	}
}

func trustCfg(t *testing.T) (base, cfg string) {
	t.Helper()
	base = t.TempDir()
	return base, CodexConfigPath(base)
}

func writeCfg(t *testing.T, cfg, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
}

func readCfg(t *testing.T, cfg string) string {
	t.Helper()
	b, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTrustCodexProjectCreatesMissingFile(t *testing.T) {
	_, cfg := trustCfg(t)
	ws := filepath.Join(t.TempDir(), "work")
	res, err := TrustCodexProject(cfg, ws)
	if err != nil || res.Status != TrustWritten {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	want := "[projects.\"" + ws + "\"]\ntrust_level = \"trusted\"\n"
	if runtime.GOOS == "windows" {
		want = "[projects.'" + ws + "']\ntrust_level = \"trusted\"\n"
	}
	if got := readCfg(t, cfg); got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(cfg)
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
		}
	}
	if !CodexProjectTrusted(cfg, ws) {
		t.Error("the written table is not read back as trusted")
	}
}

func TestTrustCodexProjectAppendsAndKeepsEveryByte(t *testing.T) {
	for _, tail := range []string{"\n", ""} {
		_, cfg := trustCfg(t)
		orig := "# my notes\nmodel = \"o3\" # inline\n\n[projects.\"/elsewhere\"]\ntrust_level = \"trusted\"\n\n# >>> cartographer:begin\n[mcp_servers.x]\nurl = \"u\"\n# <<< cartographer:end" + tail
		writeCfg(t, cfg, orig)
		ws := "/some/work"
		res, err := TrustCodexProject(cfg, ws)
		if err != nil || res.Status != TrustWritten {
			t.Fatalf("res=%+v err=%v", res, err)
		}
		got := readCfg(t, cfg)
		if !strings.HasPrefix(got, orig) {
			t.Errorf("original bytes are not an exact prefix:\n%q", got)
		}
		added := strings.TrimPrefix(got, orig)
		if strings.Count(added, "[") != 1 || !strings.HasSuffix(added, "trust_level = \"trusted\"\n") {
			t.Errorf("appended text = %q", added)
		}
		if strings.Contains(added, "cartographer:") {
			t.Error("the table carries a Cartographer marker")
		}
		if fi, _ := os.Stat(cfg); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o640 {
			t.Errorf("mode changed to %v", fi.Mode().Perm())
		}
		if !CodexProjectTrusted(cfg, ws) {
			t.Error("not trusted after write")
		}
		// Second call: no change.
		res, err = TrustCodexProject(cfg, ws)
		if err != nil || res.Status != TrustAlreadyTrusted || readCfg(t, cfg) != got {
			t.Errorf("second call res=%+v err=%v changed=%v", res, err, readCfg(t, cfg) != got)
		}
	}
}

func TestTrustCodexProjectRefusesAnyOtherDeclaration(t *testing.T) {
	// The path and its key are built the way the writer builds them: on Windows
	// filepath.Clean turns "/some/work" into `\some\work`, which is a literal key.
	ws := filepath.Clean("/some/work")
	key := trustKey(ws)
	cases := map[string]string{
		"untrusted":  "[projects." + key + "]\ntrust_level = \"untrusted\"\n",
		"dotted":     "projects." + key + ".trust_level = \"trusted\"\n",
		"dotted-any": "projects.\"/other\".trust_level = \"trusted\"\n",
		"inline":     "projects = { \"/other\" = { trust_level = \"trusted\" } }\n",
		"bare table": "[projects]\n",
		"comment":    "# trust " + ws + " later\nmodel = \"o3\"\n",
		// The same path spelled the other way a config may carry it.
		"slash spelling":   "[projects.\"" + filepath.ToSlash(ws) + "\"]\ntrust_level = \"untrusted\"\n",
		"escaped spelling": "[projects.\"" + strings.ReplaceAll(ws, `\`, `\\`) + "\"]\ntrust_level = \"untrusted\"\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			_, cfg := trustCfg(t)
			writeCfg(t, cfg, content)
			res, err := TrustCodexProject(cfg, ws)
			if err != nil || res.Status != TrustRefused || res.Reason == "" || !strings.Contains(res.Line, ws) {
				t.Fatalf("res=%+v err=%v", res, err)
			}
			if readCfg(t, cfg) != content {
				t.Error("a refused call changed the file")
			}
		})
	}
}

func TestTrustCodexProjectRefusesALink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	t.Run("file", func(t *testing.T) {
		base, cfg := trustCfg(t)
		target := filepath.Join(t.TempDir(), "real.toml")
		if err := os.WriteFile(target, []byte("model = \"o3\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(base, ".codex"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, cfg); err != nil {
			t.Fatal(err)
		}
		res, err := TrustCodexProject(cfg, "/some/work")
		if err != nil || res.Status != TrustRefused {
			t.Fatalf("res=%+v err=%v", res, err)
		}
		if b, _ := os.ReadFile(target); string(b) != "model = \"o3\"\n" {
			t.Error("the link target was written through")
		}
	})
	t.Run("directory", func(t *testing.T) {
		base, cfg := trustCfg(t)
		realDir := t.TempDir()
		if err := os.Symlink(realDir, filepath.Join(base, ".codex")); err != nil {
			t.Fatal(err)
		}
		res, err := TrustCodexProject(cfg, "/some/work")
		if err != nil || res.Status != TrustRefused {
			t.Fatalf("res=%+v err=%v", res, err)
		}
		if entries, _ := os.ReadDir(realDir); len(entries) != 0 {
			t.Error("something was written through the linked directory")
		}
	})
}

func TestTrustCodexProjectRefusesAQuote(t *testing.T) {
	_, cfg := trustCfg(t)
	res, err := TrustCodexProject(cfg, "/some/\"work")
	if err != nil || res.Status != TrustRefused {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if _, err := os.Stat(cfg); err == nil {
		t.Error("a file was created")
	}
}

// A Windows path is written as a literal string and read back on every platform.
func TestTrustCodexProjectBackslashPath(t *testing.T) {
	_, cfg := trustCfg(t)
	ws := `C:\Users\user\work`
	if filepath.Clean(ws) != ws {
		t.Skip("filepath.Clean rewrites backslashes on this platform; the literal form is exercised on Windows")
	}
	res, err := TrustCodexProject(cfg, ws)
	if err != nil || res.Status != TrustWritten {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got := readCfg(t, cfg); !strings.Contains(got, "[projects.'"+ws+"']") {
		t.Errorf("not a literal string: %q", got)
	}
	if !CodexProjectTrusted(cfg, ws) {
		t.Error("backslash path not read back as trusted")
	}
}

func TestManualLineBackslashIsLiteral(t *testing.T) {
	if got := ManualLine(`C:\a\b`); !strings.HasPrefix(got, `[projects.'C:\a\b']`) {
		t.Errorf("ManualLine = %q", got)
	}
}

func TestTrustCodexProjectWriteFailureKeepsOriginal(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a read-only directory that is enforced")
	}
	_, cfg := trustCfg(t)
	writeCfg(t, cfg, "model = \"o3\"\n")
	if err := os.Chmod(filepath.Dir(cfg), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(cfg), 0o755) })
	res, err := TrustCodexProject(cfg, "/some/work")
	if err == nil || res.Status == TrustWritten {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if readCfg(t, cfg) != "model = \"o3\"\n" {
		t.Error("original file damaged")
	}
}
