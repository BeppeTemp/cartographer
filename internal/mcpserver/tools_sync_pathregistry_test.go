package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// sync_pull serves the KB's paths.yaml as path_registry (D263): parsed, the
// malformed entries left out and named in issues, outside revision, and in
// the shape the client decodes into provisioning.PathRegistry.
func TestSyncPullServesPathRegistry(t *testing.T) {
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{BundleFS: fstest.MapFS{}})

	pull := func() map[string]json.RawMessage {
		t.Helper()
		text, isErr := callJSON(t, s, adminCtx, "sync_pull", `{}`)
		if isErr {
			t.Fatal(text)
		}
		var out map[string]json.RawMessage
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	before := pull()
	if _, ok := before["path_registry"]; ok {
		t.Fatal("path_registry must be omitted when the KB has no paths.yaml")
	}

	registry := "paths:\n  claude-home: {description: Claude Code's directory, default: ~/.claude}\n" +
		"  broken: {default: /Users/x}\n" +
		"repos:\n  kb-tools: {description: the tools repo, remote: git@example.com:owner/kb-tools.git}\n"
	if err := os.WriteFile(filepath.Join(k.Root, "paths.yaml"), []byte(registry), 0o644); err != nil {
		t.Fatal(err)
	}
	after := pull()
	var reg provisioning.PathRegistry
	if err := json.Unmarshal(after["path_registry"], &reg); err != nil {
		t.Fatalf("path_registry does not decode as provisioning.PathRegistry: %v (%s)", err, after["path_registry"])
	}
	want := provisioning.PathRegistry{
		Paths: map[string]provisioning.PathDecl{"claude-home": {Description: "Claude Code's directory", Default: "~/.claude"}},
		Repos: map[string]provisioning.PathDecl{"kb-tools": {Description: "the tools repo", Remote: "example.com/owner/kb-tools"}},
	}
	if !reflect.DeepEqual(reg, want) {
		t.Errorf("path_registry = %+v, want %+v", reg, want)
	}
	var issues []string
	_ = json.Unmarshal(after["issues"], &issues)
	if len(issues) != 1 || !strings.Contains(issues[0], "paths.broken") {
		t.Errorf("issues = %v, want the malformed entry named", issues)
	}
	if string(before["revision"]) != string(after["revision"]) {
		t.Errorf("paths.yaml changed the revision: %s -> %s", before["revision"], after["revision"])
	}

	// A file that is not a registry at all is an issue, never a failed pull.
	if err := os.WriteFile(filepath.Join(k.Root, "paths.yaml"), []byte("- not\n- a mapping\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := pull()
	if _, ok := broken["path_registry"]; ok {
		t.Error("an unparseable paths.yaml must not be served")
	}
	if !strings.Contains(string(broken["issues"]), "paths.yaml") {
		t.Errorf("issues = %s, want the parse failure", broken["issues"])
	}
}
