package provisioning

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCodexProjectTrusted: a Codex workspace projection is only active when the
// project is trusted, and absence of evidence is never evidence of trust.
func TestCodexProjectTrusted(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	ws := "/Users/someone/work/dante"
	other := "/Users/someone/work/homelab"

	// No config at all.
	if CodexProjectTrusted(cfg, ws) {
		t.Error("a missing config reported the project as trusted")
	}

	content := `
model = "o3"

[projects."/Users/someone/work/dante"]
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
