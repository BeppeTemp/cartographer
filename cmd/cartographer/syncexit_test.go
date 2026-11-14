package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

func syncExitFixture(t *testing.T, agents ...string) string {
	t.Helper()
	srv := multiKBServer(t, `{"status":"ok","version":"9","kbs":[{"name":"alpha"}]}`)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	setHome(t, dir)
	cfg := &clientconfig.Config{ServerURL: srv.URL + "/mcp", ServerName: "wiki", Token: "${TOKEN}", Agents: agents, KnownKBs: []string{"alpha"}}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	return dir
}

func syncCode(t *testing.T, args ...string) (code int, stderr string) {
	t.Helper()
	stderr = withStderr(t, func() { withStdout(t, func() { code = cmdSync(args) }) })
	return code, stderr
}

// #2099: a write the guard refused is not a clean sync. connect exits 3 for the
// same refusal (D1722); a dry run writes nothing and reports none.
func TestCmdSync_ARefusedMCPEntryExits3(t *testing.T) {
	dir := syncExitFixture(t, "opencode")
	symlinkedOpenCodeDir(t, dir)
	code, stderr := syncCode(t, "--dry-run")
	if code != 0 {
		t.Errorf("--dry-run = %d, want 0 (stderr %q)", code, stderr)
	}
	code, stderr = syncCode(t)
	if code != exitPartiallyConnected {
		t.Fatalf("sync = %d, want %d (stderr %q)", code, exitPartiallyConnected, stderr)
	}
	if !strings.Contains(stderr, "sync refused to write: opencode (MCP entry skipped)") {
		t.Errorf("stderr = %q, want the line naming provider and part", stderr)
	}
}

func TestCmdSync_ACleanSyncExits0(t *testing.T) {
	syncExitFixture(t, "claude")
	if code, stderr := syncCode(t); code != 0 {
		t.Errorf("sync = %d, want 0 (stderr %q)", code, stderr)
	}
}

// A refused instructions write (a symlinked CLAUDE.md, no allow list) reaches
// syncResult.Refused and from there the exit code.
func TestSyncRefusedExit_ARefusedInstructionsWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(t.TempDir(), "CLAUDE.md")
	if err := os.WriteFile(real, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(dir, ".claude", "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	m := instructionsManifest("kb-a", "body\n")
	applied, err := materializeForProviders(uniformManifests(m, []string{"claude"}), globalProjections([]string{"claude"}, dir), dir, "", false, false, portabilityOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	res := syncResult{Applied: applied, Refused: skippedParts(nil, nil, applied)}
	var w strings.Builder
	if code := syncRefusedExit(&w, res); code != exitPartiallyConnected {
		t.Fatalf("exit = %d, want %d (applied %+v)", code, exitPartiallyConnected, applied["claude"])
	}
	if !strings.Contains(w.String(), "claude (instructions") {
		t.Errorf("line = %q", w.String())
	}
	var none strings.Builder
	if code := syncRefusedExit(&none, syncResult{}); code != 0 || none.Len() != 0 {
		t.Errorf("clean result: exit %d, line %q", code, none.String())
	}
}
