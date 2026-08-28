package main

// `cartographer reconnect` (D142): a full disconnect + connect cycle that
// rebuilds a provider's configuration, preserving every setting.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// treeSnapshot lists every file under root with its content, so two
// configurations can be compared byte for byte.
func treeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}

// A rebuild leaves exactly what a fresh connect would, and every setting
// survives the cycle: it is a rebuild, not a reset.
func TestDoReconnect_RebuildsAndPreservesSettings(t *testing.T) {
	srv := multiKBServer(t, `{"status":"ok","version":"1.4.0","kbs":[{"name":"alpha"}]}`)
	defer srv.Close()
	dir := t.TempDir()

	opts := connectOptions{
		Providers: []string{"claude"}, Dir: dir, ServerURL: srv.URL + "/mcp",
		Name: "cartographer", TokenEnv: "TOKEN", Trust: true,
		PinKeys: []string{"alpha=" + strings.Repeat("ab", 32)},
	}
	if _, err := doConnect(opts); err != nil {
		t.Fatalf("doConnect: %v", err)
	}
	before := treeSnapshot(t, dir)

	res, err := doReconnect(reconnectOptions{Target: "all", Dir: dir})
	if err != nil {
		t.Fatalf("doReconnect: %v", err)
	}
	if strings.Join(res.Providers, ",") != "claude" {
		t.Fatalf("rebuilt providers = %v, want [claude]", res.Providers)
	}
	if res.Disconnected == nil || res.Connected == nil {
		t.Fatal("both halves of the cycle must be reported")
	}
	if len(res.LeftDisconnected) != 0 {
		t.Errorf("a successful rebuild reported providers left disconnected: %v", res.LeftDisconnected)
	}

	after := treeSnapshot(t, dir)
	if len(before) != len(after) {
		t.Fatalf("file set changed: %d files before, %d after", len(before), len(after))
	}
	for name, content := range before {
		if after[name] != content {
			t.Errorf("%s changed across the rebuild:\n--- before\n%s\n--- after\n%s", name, content, after[name])
		}
	}

	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.Trust {
		t.Error("trust did not survive the rebuild")
	}
	if cfg.ServerURL != srv.URL+"/mcp" || cfg.ServerName != "cartographer" || cfg.TokenEnv != "TOKEN" {
		t.Errorf("server settings did not survive: %+v", cfg)
	}
	if len(cfg.SigningKeys["alpha"]) != 1 {
		t.Errorf("pinned signing keys did not survive: %+v", cfg.SigningKeys)
	}
	if strings.Join(cfg.Agents, ",") != "claude" {
		t.Errorf("agents after rebuild = %v, want [claude]", cfg.Agents)
	}
}

// A provider that was never connected is rebuilt anyway, and said to be.
func TestDoReconnect_ProviderNotPreviouslyConnected(t *testing.T) {
	srv := multiKBServer(t, `{"status":"ok","version":"1.4.0","kbs":[{"name":"alpha"}]}`)
	defer srv.Close()
	dir := t.TempDir()
	if _, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer"}); err != nil {
		t.Fatalf("doConnect: %v", err)
	}

	res, err := doReconnect(reconnectOptions{Target: "codex", Dir: dir})
	if err != nil {
		t.Fatalf("doReconnect: %v", err)
	}
	if strings.Join(res.Rebuilt, ",") != "codex" {
		t.Errorf("Rebuilt = %v, want [codex]", res.Rebuilt)
	}
	if _, err := os.Stat(filepath.Join(dir, ".codex", "config.toml")); err != nil {
		t.Errorf("codex was not configured: %v", err)
	}
}

// The invariant: a connect half that fails after a successful disconnect half
// must name the providers left without a configuration.
func TestDoReconnect_ConnectHalfFailsNamesDisconnected(t *testing.T) {
	srv := multiKBServer(t, `{"status":"ok","version":"1.4.0","kbs":[{"name":"alpha"}]}`)
	defer srv.Close()
	dir := t.TempDir()
	hermesHome := t.TempDir()
	t.Setenv("HERMES_HOME", hermesHome)
	if _, err := doConnect(connectOptions{Providers: []string{"hermes"}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer"}); err != nil {
		t.Fatalf("doConnect: %v", err)
	}

	// The provider's root disappears from the environment: the disconnect half
	// still works (the lockfile records the base dir), the connect half cannot.
	t.Setenv("HERMES_HOME", "")
	res, err := doReconnect(reconnectOptions{Target: "all", Dir: dir})
	if err == nil {
		t.Fatal("doReconnect succeeded with $HERMES_HOME unset")
	}
	if strings.Join(res.LeftDisconnected, ",") != "hermes" {
		t.Fatalf("LeftDisconnected = %v, want [hermes]", res.LeftDisconnected)
	}
	recovery := reconnectRecoveryCommand(res.LeftDisconnected, res.ConnectOptions)
	for _, want := range []string{"cartographer connect", "hermes", srv.URL} {
		if !strings.Contains(recovery, want) {
			t.Errorf("recovery command %q does not mention %q", recovery, want)
		}
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil || len(cfg.Agents) != 0 {
		t.Fatalf("agents after the failed rebuild = %v, err=%v", cfg.Agents, err)
	}
}

// --dry-run writes nothing at all: neither half touches disk.
func TestDoReconnect_DryRunWritesNothing(t *testing.T) {
	srv := multiKBServer(t, `{"status":"ok","version":"1.4.0","kbs":[{"name":"alpha"}]}`)
	defer srv.Close()
	dir := t.TempDir()
	if _, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer"}); err != nil {
		t.Fatalf("doConnect: %v", err)
	}
	before := treeSnapshot(t, dir)

	res, err := doReconnect(reconnectOptions{Target: "all", Dir: dir, DryRun: true})
	if err != nil {
		t.Fatalf("doReconnect: %v", err)
	}
	if len(res.LeftDisconnected) != 0 {
		t.Errorf("a dry run cannot leave anything disconnected: %v", res.LeftDisconnected)
	}
	after := treeSnapshot(t, dir)
	if len(before) != len(after) {
		t.Fatalf("dry run changed the file set: %d → %d", len(before), len(after))
	}
	for name, content := range before {
		if after[name] != content {
			t.Errorf("dry run rewrote %s", name)
		}
	}
}

// D142 WP1: the server version round-trips through the lockfile, and an
// unreachable server preserves what was recorded instead of erasing it.
func TestMaterializeForProviders_RecordsServerVersion(t *testing.T) {
	dir := t.TempDir()
	m := kbSkillManifest()

	if _, err := materializeForProviders(m, []string{"claude"}, dir, "1.4.0", true, false, false, portabilityOptions{}); err != nil {
		t.Fatalf("materializeForProviders: %v", err)
	}
	lockFile, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		t.Fatalf("read lockfile: %v", err)
	}
	if got := lockFile.ForProvider("claude").ServerVersion; got != "1.4.0" {
		t.Fatalf("recorded server version = %q, want 1.4.0", got)
	}

	// An offline sync (empty version) must not erase the knowledge.
	if _, err := materializeForProviders(m, []string{"claude"}, dir, "", true, false, false, portabilityOptions{}); err != nil {
		t.Fatalf("materializeForProviders offline: %v", err)
	}
	lockFile, err = provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		t.Fatalf("read lockfile: %v", err)
	}
	if got := lockFile.ForProvider("claude").ServerVersion; got != "1.4.0" {
		t.Errorf("offline sync changed the recorded version to %q", got)
	}
}

// A lockfile written before D142 has no server_version and loads unchanged.
func TestLockFile_WithoutServerVersion(t *testing.T) {
	dir := t.TempDir()
	raw := `{"version":2,"providers":{"claude":{"applied_revision":"r1","provider":"claude","managed":[]}}}`
	if err := os.WriteFile(lockFilePath(dir), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	lockFile, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		t.Fatalf("read lockfile: %v", err)
	}
	lock := lockFile.ForProvider("claude")
	if lock.ServerVersion != "" || lock.AppliedRevision != "r1" {
		t.Fatalf("pre-D142 lockfile changed meaning: %+v", lock)
	}
	// And it says nothing: an unknown version is never a server change.
	if notice := serverChangeNotice(dir, []string{"claude"}, "1.4.0"); notice != "" {
		t.Errorf("unknown recorded version produced a notice: %q", notice)
	}
}

// D142 WP2: one line, once per invocation, only when both versions are known
// and actually differ.
func TestServerChangeNotice(t *testing.T) {
	write := func(t *testing.T, versions map[string]string) string {
		t.Helper()
		dir := t.TempDir()
		providers := map[string]any{}
		for p, v := range versions {
			entry := map[string]any{"applied_revision": "r1", "provider": p, "managed": []any{}}
			if v != "" {
				entry["server_version"] = v
			}
			providers[p] = entry
		}
		data, err := json.Marshal(map[string]any{"version": 2, "providers": providers})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(lockFilePath(dir), data, 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	for _, tc := range []struct {
		name     string
		recorded map[string]string
		live     string
		want     bool
	}{
		{"changed", map[string]string{"claude": "1.3.0"}, "1.4.0", true},
		{"equal", map[string]string{"claude": "1.4.0"}, "1.4.0", false},
		{"recorded unknown", map[string]string{"claude": ""}, "1.4.0", false},
		{"live unknown", map[string]string{"claude": "1.3.0"}, "", false},
		{"recorded dev", map[string]string{"claude": "dev"}, "1.4.0", false},
		{"live dev", map[string]string{"claude": "1.3.0"}, "dev", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := write(t, tc.recorded)
			notice := serverChangeNotice(dir, []string{"claude"}, tc.live)
			if (notice != "") != tc.want {
				t.Fatalf("notice = %q, want reported=%v", notice, tc.want)
			}
			if tc.want && !strings.Contains(notice, "cartographer reconnect") {
				t.Errorf("notice does not recommend reconnect: %q", notice)
			}
		})
	}

	// Several providers recording different old versions are one fact about
	// the server, so still exactly one line — naming both.
	dir := write(t, map[string]string{"claude": "1.3.0", "codex": "1.2.0"})
	notice := serverChangeNotice(dir, []string{"claude", "codex"}, "1.4.0")
	if strings.Count(notice, "\n") != 0 {
		t.Errorf("notice is not a single line: %q", notice)
	}
	for _, want := range []string{"1.3.0", "1.2.0", "1.4.0"} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice %q does not mention %s", notice, want)
		}
	}
}

// The recorded version reaches the status snapshot, and only a real difference
// is reported.
func TestSnapshotMaterializedVersions(t *testing.T) {
	s := statusSnapshot{
		Reachable: true, Server: "1.4.0",
		Providers: []providerStatus{
			{Name: "claude", Connected: true, ServerVersion: "1.3.0"},
			{Name: "codex", Connected: true, ServerVersion: "1.4.0"},
			{Name: "kiro", Connected: true, ServerVersion: ""},
			{Name: "opencode", Connected: false, ServerVersion: "1.1.0"},
		},
	}
	got := snapshotMaterializedVersions(s)
	sort.Strings(got)
	if strings.Join(got, ",") != "1.3.0" {
		t.Fatalf("snapshotMaterializedVersions = %v, want [1.3.0]", got)
	}
	s.Reachable = false
	if got := snapshotMaterializedVersions(s); got != nil {
		t.Errorf("unreachable server reported %v", got)
	}
}
