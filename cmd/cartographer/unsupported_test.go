package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// D898: a client config and lockfile written while a provider was supported
// keep working after the registry drops it. The fixture names "hermes" because
// that is the provider that was removed; the behaviour is generic ("a name the
// registry no longer knows").
func TestSyncSkipsAProviderTheRegistryNoLongerKnows(t *testing.T) {
	srv := syncPullServer(t, func(kb string) (string, bool) {
		return syncPullPayload(t, kb, "skill-"+kb), false
	})
	defer srv.Close()

	dir := t.TempDir()
	oldHome := t.TempDir() // where the removed client's files were delivered
	oldFile := filepath.Join(oldHome, "skill-inbox", "demo", "cartographer", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(oldFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldFile, []byte("delivered earlier\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()

	cfg := &clientconfig.Config{
		ServerURL: srv.URL + "/mcp", ServerName: "cartographer",
		Agents: []string{"claude", "hermes"}, KnownKBs: []string{"one"}, Trust: true,
		Clients: map[string]clientconfig.ClientBinding{
			"claude": {KBs: []string{"one"}},
			"hermes": {KBs: []string{"one"}},
		},
		Scopes:     map[string]string{"hermes": clientconfig.ScopeWorkspace},
		Workspaces: map[string][]clientconfig.WorkspaceBinding{"hermes": {{Path: workspace, KBs: []string{"one"}}}},
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	hermesLock := provisioning.Lock{
		Provider: "hermes", BaseDir: oldHome,
		Managed: []provisioning.ManagedFile{{Kind: "skill", Name: "demo", Path: "skill-inbox/demo/cartographer/SKILL.md", ContentHash: "h"}},
	}
	lf := provisioning.LockFile{
		Providers: map[string]provisioning.Lock{"hermes": hermesLock},
		Workspaces: map[string]provisioning.WorkspaceLocks{
			workspace: {Providers: map[string]provisioning.Lock{"hermes": {Provider: "hermes", BaseDir: workspace, ProjectScope: true,
				Managed: []provisioning.ManagedFile{{Kind: "skill", Name: "demo", Path: ".hermes/skills/demo/SKILL.md", ContentHash: "h"}}}}},
		},
	}
	wsFile := filepath.Join(workspace, ".hermes", "skills", "demo", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(wsFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wsFile, []byte("project file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := provisioning.WriteLockFile(lockFilePath(dir), lf); err != nil {
		t.Fatal(err)
	}

	loaded, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(loaded.Agents, ",") != "claude" || strings.Join(loaded.Unsupported, ",") != "hermes" {
		t.Fatalf("Load: agents=%v unsupported=%v", loaded.Agents, loaded.Unsupported)
	}
	if _, ok := loaded.Clients["hermes"]; ok || len(loaded.Workspaces) != 0 || len(loaded.Scopes) != 0 {
		t.Fatalf("Load kept the unknown provider's bindings: %+v %+v %+v", loaded.Clients, loaded.Workspaces, loaded.Scopes)
	}

	var runErr error
	stderr := withStderr(t, func() {
		_, runErr = runSync(dir, loaded, syncOptions{})
	})
	if runErr != nil {
		t.Fatalf("runSync: %v", runErr)
	}

	if n := strings.Count(stderr, "hermes: this agent client is no longer supported"); n != 1 {
		t.Errorf("notice printed %d times, want once:\n%s", n, stderr)
	}
	if !strings.Contains(stderr, "("+oldHome+")") || !strings.Contains(stderr, "remove them by hand") {
		t.Errorf("notice must name the recorded base_dir and say the files stay:\n%s", stderr)
	}
	// Nothing delivered earlier was touched, nothing was written under it.
	for _, f := range []string{oldFile, wsFile} {
		b, err := os.ReadFile(f)
		if err != nil || len(b) == 0 {
			t.Errorf("%s was touched: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "skill-inbox")); !os.IsNotExist(err) {
		t.Errorf("the removed client resolved to the home directory: %v", err)
	}
	// claude is unchanged by the removal.
	if _, err := os.Stat(filepath.Join(dir, ".claude", "skills", "skill-one", "SKILL.md")); err != nil {
		t.Errorf("claude was not synced: %v", err)
	}

	// The config no longer lists it, and neither does the lockfile.
	after, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Unsupported) != 0 || strings.Join(after.Agents, ",") != "claude" {
		t.Errorf("config after sync: agents=%v unsupported=%v", after.Agents, after.Unsupported)
	}
	raw, _ := os.ReadFile(clientconfig.Path(dir))
	if strings.Contains(string(raw), "hermes") {
		t.Errorf("the config file still names the removed client:\n%s", raw)
	}
	lockAfter, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lockAfter.Providers["hermes"]; ok || len(lockAfter.Workspaces) != 0 {
		t.Errorf("lock sections of the removed client survived: %+v", lockAfter)
	}
	if len(lockAfter.ForProvider("claude").Managed) == 0 {
		t.Error("claude lost its lock entry")
	}

	// A second run has nothing left to say.
	again, _ := clientconfig.Load(dir)
	stderr = withStderr(t, func() { _, runErr = runSync(dir, again, syncOptions{}) })
	if runErr != nil || strings.Contains(stderr, "no longer supported") {
		t.Errorf("second run: err=%v stderr=%q", runErr, stderr)
	}
}

// A dry run reports the dropped client but rewrites nothing.
func TestDryRunReportsAnUnsupportedProviderWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	cfg := &clientconfig.Config{ServerURL: "http://127.0.0.1:1/mcp", ServerName: "cartographer", Agents: []string{"claude", "hermes"}, Trust: true}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(clientconfig.Path(dir))
	loaded, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	stderr := withStderr(t, func() { handleUnsupportedProviders(dir, loaded, false) })
	if !strings.Contains(stderr, "hermes: this agent client is no longer supported") {
		t.Errorf("no notice:\n%s", stderr)
	}
	after, _ := os.ReadFile(clientconfig.Path(dir))
	if string(before) != string(after) {
		t.Error("a read-only run rewrote the config")
	}
}

// `disconnect all` on a config that still lists a removed client: its lock
// section must not keep the lockfile and the token file alive, the notice is
// printed once, and nothing under the old base dir is touched (D898).
func TestDisconnectAllCleansARemovedProviderSection(t *testing.T) {
	dir := t.TempDir()
	oldHome := t.TempDir()
	oldFile := filepath.Join(oldHome, "skill-inbox", "demo", "cartographer", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(oldFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldFile, []byte("delivered earlier\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &clientconfig.Config{ServerURL: "http://127.0.0.1:1/mcp", ServerName: "cartographer", Agents: []string{"claude", "hermes"}, Trust: true}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.WriteToken(dir, "tok"); err != nil {
		t.Fatal(err)
	}
	lf := provisioning.LockFile{Providers: map[string]provisioning.Lock{
		"claude": {Provider: "claude"},
		"hermes": {Provider: "hermes", BaseDir: oldHome, Managed: []provisioning.ManagedFile{{Kind: "skill", Name: "demo", Path: "skill-inbox/demo/cartographer/SKILL.md", ContentHash: "h"}}},
	}}
	if err := provisioning.WriteLockFile(lockFilePath(dir), lf); err != nil {
		t.Fatal(err)
	}

	var err error
	stderr := withStderr(t, func() {
		_, err = doDisconnect(disconnectOptions{Providers: []string{"claude"}, Dir: dir})
	})
	if err != nil {
		t.Fatalf("doDisconnect: %v", err)
	}
	if n := strings.Count(stderr, "hermes: this agent client is no longer supported"); n != 1 {
		t.Errorf("notice printed %d times:\n%s", n, stderr)
	}
	if _, err := os.Stat(lockFilePath(dir)); !os.IsNotExist(err) {
		t.Errorf("lockfile survived a full disconnect: %v", err)
	}
	if _, err := os.Stat(clientconfig.TokenPath(dir)); !os.IsNotExist(err) {
		t.Errorf("token file survived a full disconnect: %v", err)
	}
	if b, err := os.ReadFile(oldFile); err != nil || len(b) == 0 {
		t.Errorf("a delivered file was touched: %v", err)
	}
}
