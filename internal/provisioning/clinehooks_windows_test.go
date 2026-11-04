//go:build windows

package provisioning

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// The dispatcher run exactly as Cline's bundle runs it (IGh): PowerShell with
// -NoProfile -NonInteractive -ExecutionPolicy Bypass -File, a stub .cmd as the
// binary, a non-ASCII payload and path. Pins that a native command run with `&`
// inherits the redirected stdin of `powershell -File`.
func TestClineDispatcherRunsOnWindows(t *testing.T) {
	ps, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("no powershell")
	}
	dir := filepath.Join(t.TempDir(), "Zoë")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := filepath.Join(dir, "rec")
	stub := filepath.Join(dir, "cartographer.cmd")
	body := "@echo off\r\necho %* > \"" + rec + ".args\"\r\nfindstr \"^\" > \"" + rec + ".in\"\r\necho {}\r\n"
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "PostToolUse.ps1")
	if err := os.WriteFile(script, generateClineDispatcher("PostToolUse", stub, true), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script)
	cmd.Stdin = strings.NewReader("{\"note\":\"caff\\u00e8\"}\n")
	out, err := cmd.Output()
	if err != nil || !strings.Contains(string(out), "{}") {
		t.Fatalf("out %q err %v", out, err)
	}
	args, _ := os.ReadFile(rec + ".args")
	if got := strings.TrimSpace(string(args)); got != "hook dispatch --provider cline --event PostToolUse" {
		t.Errorf("args = %q", got)
	}
	if in, _ := os.ReadFile(rec + ".in"); !strings.Contains(string(in), "caff") {
		t.Errorf("payload did not reach the binary: %q", in)
	}
}

// clineDocumentsDir answers like the call Cline makes for Documents.
func TestClineDocumentsDirIsTheKnownFolderForTheRealHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	want, err := windows.KnownFolderPath(windows.FOLDERID_Documents, 0)
	if err != nil {
		t.Skip(err)
	}
	if got := clineDocumentsDir(home); got != want {
		t.Errorf("clineDocumentsDir(home) = %s, want %s", got, want)
	}
}
