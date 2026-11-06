package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/execbit"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// deliveryMachine installs the plugin folder (approved) into a fresh home and
// returns the home and its config, pointed at srvURL with one provider.
func deliveryMachine(t *testing.T, srvURL string, plugins ...string) (string, *clientconfig.Config) {
	t.Helper()
	home := pluginFixture(t)
	for _, folder := range plugins {
		if code, out := runPlugin(t, "install", folder, "--yes"); code != 0 {
			t.Fatalf("install %s: exit %d\n%s", folder, code, out)
		}
	}
	cfg, err := clientconfig.Load(home)
	if os.IsNotExist(err) {
		cfg, err = clientconfig.Default(), nil
	}
	if err != nil {
		t.Fatal(err)
	}
	cfg.ServerURL, cfg.ServerName, cfg.Agents = srvURL+"/mcp", "cartographer", []string{"claude"}
	return home, cfg
}

// lockedNames maps kind/name to source for the plugin-owned lock entries.
func lockedNames(t *testing.T, home string) map[string]string {
	t.Helper()
	lock, err := provisioning.ReadLockFile(lockFilePath(home))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, mf := range lock.ForProvider("claude").Managed {
		// Only what plugins installed: the synthetic hooks are not theirs.
		if provisioning.IsPluginSource(mf.Source) {
			out[mf.Kind+"/"+mf.Name] = mf.Source
		}
	}
	return out
}

func skillPath(home, name string) string {
	return filepath.Join(home, ".claude", "skills", name, "SKILL.md")
}

func TestSyncDeliversPluginSkillsAndPrunesOnDisable(t *testing.T) {
	srv := prefixAwareMCPServer(t, map[string]string{"one": ""}, "sync_pull", func(kb string) string {
		return syncPullPayload(t, kb, "kb-skill")
	})
	defer srv.Close()
	home, cfg := deliveryMachine(t, srv.URL, writePlugin(t, "team-forks", nil))
	cfg.KnownKBs = []string{"one"}
	// A KB that opted out of artifacts does not take the plugin's with it.
	off := false
	cfg.KBOptions = map[string]clientconfig.KBOption{"one": {Artifacts: &off}}

	if _, err := runSync(home, cfg, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(skillPath(home, "helper")); err != nil {
		t.Fatalf("the plugin skill is not installed: %v", err)
	}
	if got := lockedNames(t, home)["skill/helper"]; got != "plugin:team-forks" {
		t.Errorf("lock source = %q", got)
	}
	if _, err := os.Stat(skillPath(home, "kb-skill")); err == nil {
		t.Error("the opted-out KB skill was installed")
	}

	e := cfg.Plugins["team-forks"]
	e.Enabled = false
	if err := cfg.SetPlugin("team-forks", e); err != nil {
		t.Fatal(err)
	}
	if _, err := runSync(home, cfg, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(skillPath(home, "helper")); err == nil {
		t.Error("a disabled plugin's skill was not retracted")
	}
}

func TestSyncPluginHookIsRegisteredWhenActive(t *testing.T) {
	if !execbit.Supported {
		t.Skip("a hook script needs the execute bit")
	}
	srv := healthOnlyServer(t)
	defer srv.Close()
	folder := writePlugin(t, "team-forks", map[string]string{
		"hooks/guard/hook.json": `{"event":"Stop","command":"./run.sh"}`,
		"hooks/guard/run.sh":    "#!/bin/sh\nexit 0\n",
	})
	if err := os.Chmod(filepath.Join(folder, "hooks", "guard", "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	home, cfg := deliveryMachine(t, srv.URL, folder)
	// A machine bound to no KB still receives its plugins.
	cfg.Clients = map[string]clientconfig.ClientBinding{"claude": {KBs: []string{}, KBsSet: true}}

	if _, err := runSync(home, cfg, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := lockedNames(t, home)["hook/guard"]; got != "plugin:team-forks" {
		t.Errorf("the plugin hook is not installed: %v", lockedNames(t, home))
	}
	if _, err := os.Stat(skillPath(home, "helper")); err != nil {
		t.Errorf("the plugin skill is missing on a machine with no KB: %v", err)
	}
}

func TestSyncPendingPluginDeliversNothingAndDryRunWritesNothing(t *testing.T) {
	srv := healthOnlyServer(t)
	defer srv.Close()
	home := pluginFixture(t)
	if code, out := runPlugin(t, "install", writePlugin(t, "team-forks", nil)); code != 0 {
		t.Fatalf("install: exit %d\n%s", code, out)
	}
	cfg, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ServerURL, cfg.ServerName, cfg.Agents = srv.URL+"/mcp", "cartographer", []string{"claude"}
	cfg.Clients = map[string]clientconfig.ClientBinding{"claude": {KBs: []string{}, KBsSet: true}}
	if _, err := runSync(home, cfg, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := lockedNames(t, home); len(got) != 0 {
		t.Errorf("a plugin pending approval delivered %v", got)
	}

	// Approved: a dry run plans the install and writes no file.
	e := cfg.Plugins["team-forks"]
	e.Approved = e.Installed
	if err := cfg.SetPlugin("team-forks", e); err != nil {
		t.Fatal(err)
	}
	before := treeSnapshot(t, home)
	if _, err := runSync(home, cfg, syncOptions{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if after := treeSnapshot(t, home); len(after) != len(before) {
		t.Errorf("a dry run wrote files: %d before, %d after", len(before), len(after))
	}
}

func TestSyncPurgedStoreKeepsWhatIsInstalledAndPrunesNothing(t *testing.T) {
	srv := healthOnlyServer(t)
	defer srv.Close()
	folder := writePlugin(t, "team-forks", nil)
	home, cfg := deliveryMachine(t, srv.URL, folder)
	cfg.Clients = map[string]clientconfig.ClientBinding{"claude": {KBs: []string{}, KBsSet: true}}
	if _, err := runSync(home, cfg, syncOptions{}); err != nil {
		t.Fatal(err)
	}

	// The cache is gone and the source cannot be fetched again.
	store, _ := pluginStoreFn()
	if err := os.RemoveAll(store.Root); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(folder); err != nil {
		t.Fatal(err)
	}
	errOut := withStderr(t, func() {
		if _, err := runSync(home, cfg, syncOptions{}); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := os.Stat(skillPath(home, "helper")); err != nil {
		t.Errorf("an unreadable plugin retired its skill: %v", err)
	}
	if got := lockedNames(t, home)["skill/helper"]; got != "plugin:team-forks" {
		t.Errorf("the lock entry was dropped: %v", lockedNames(t, home))
	}
	if !strings.Contains(errOut, "plugin team-forks") {
		t.Errorf("no warning names the plugin:\n%s", errOut)
	}
}

func TestPluginCollisionsDropEveryClaimantAndAreReported(t *testing.T) {
	srv := prefixAwareMCPServer(t, map[string]string{"one": ""}, "sync_pull", func(kb string) string {
		return syncPullPayload(t, kb, "helper")
	})
	defer srv.Close()
	home, cfg := deliveryMachine(t, srv.URL, writePlugin(t, "team-forks", nil), writePlugin(t, "other-forks", nil))
	cfg.KnownKBs = []string{"one"}
	errOut := withStderr(t, func() {
		if _, err := runSync(home, cfg, syncOptions{}); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := os.Stat(skillPath(home, "helper")); err == nil {
		t.Error("a skill claimed by two plugins and a KB was installed")
	}
	for _, want := range []string{"skill helper is declared by", "KB one", "plugin other-forks", "plugin team-forks"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("collision report lacks %q:\n%s", want, errOut)
		}
	}
	if strings.Count(errOut, "skill helper is declared by") != 1 {
		t.Errorf("the collision must be reported once per run:\n%s", errOut)
	}
}

func TestKBAgainstKBCollisionStillStopsTheSyncWithPlugins(t *testing.T) {
	srv := prefixAwareMCPServer(t, map[string]string{"one": "", "two": ""}, "sync_pull", func(kb string) string {
		return syncPullPayload(t, kb, "shared")
	})
	defer srv.Close()
	home, cfg := deliveryMachine(t, srv.URL, writePlugin(t, "team-forks", nil))
	cfg.KnownKBs = []string{"one", "two"}
	if _, err := runSync(home, cfg, syncOptions{}); err == nil {
		t.Fatal("two KBs claiming one skill must still stop the sync")
	}
}

func TestPluginArtifactsReachOnlyTheGlobalProjection(t *testing.T) {
	_, cfg := deliveryMachine(t, "http://127.0.0.1:1", writePlugin(t, "team-forks", nil))
	cs := candidateSet{Named: map[string][]provisioning.Artifact{}, Plugins: loadActivePlugins(cfg).Artifacts}
	if len(cs.Plugins) != 1 {
		t.Fatalf("loaded %d plugin artifacts, want 1", len(cs.Plugins))
	}
	global := cs.forProjection(cfg, syncProjection{Provider: "claude"})
	if len(global) != 1 || global[0].Source != "plugin:team-forks" || global[0].Version != "1.0.0" {
		t.Errorf("global projection = %+v", global)
	}
	if got := cs.forProjection(cfg, syncProjection{Provider: "claude", Workspace: t.TempDir(), KBs: []string{"one"}}); len(got) != 0 {
		t.Errorf("a workspace projection received plugin artifacts: %+v", got)
	}
	// The global projection of a workspace-scoped provider keeps them too.
	if got := cs.forProjection(cfg, syncProjection{Provider: "claude", BundleOnly: true}); len(got) != 1 {
		t.Errorf("bundle-only global projection = %+v", got)
	}
}

func TestPluginHookAuthorizationDependsOnTheActiveSet(t *testing.T) {
	hook := provisioning.Artifact{Kind: "hook", Name: "guard", Source: "plugin:team-forks"}
	if provisioning.ArtifactAuthorized(hook, provisioning.ApplyOptions{}) {
		t.Error("a plugin hook was authorized with no active plugin")
	}
	m := provisioning.Manifest{Artifacts: []provisioning.Artifact{hook}}
	if !provisioning.ArtifactAuthorized(hook, provisioning.ApplyOptions{ActivePlugins: manifestPlugins(m)}) {
		t.Error("a plugin hook of an active plugin was not authorized")
	}
	kbHook := provisioning.Artifact{Kind: "hook", Name: "guard", Source: "kb:one"}
	if provisioning.ArtifactAuthorized(kbHook, provisioning.ApplyOptions{ActivePlugins: map[string]bool{"one": true}}) {
		t.Error("a KB hook still waits for its approval (D1141)")
	}
}

// The held lock entries of a plugin that cannot be read are not drift: status
// (and so doctor and the TUI, which read its snapshot) judges the projection
// without them, as sync does.
func TestStatusIsInSyncWhileAPluginStoreIsPurged(t *testing.T) {
	srv := healthOnlyServer(t)
	defer srv.Close()
	folder := writePlugin(t, "team-forks", nil)
	home, cfg := deliveryMachine(t, srv.URL, folder)
	cfg.Clients = map[string]clientconfig.ClientBinding{"claude": {KBs: []string{}, KBsSet: true}}
	if _, err := runSync(home, cfg, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if s := snapshotForConfig(home, cfg, false); s.State != "in_sync" {
		t.Fatalf("not in sync right after the sync: %q", s.State)
	}
	store, _ := pluginStoreFn()
	if err := os.RemoveAll(store.Root); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(folder); err != nil {
		t.Fatal(err)
	}
	if s := snapshotForConfig(home, cfg, false); s.State != "in_sync" {
		t.Errorf("a purged plugin store is reported as drift: state %q", s.State)
	}
}

func TestPluginSkillWithAnUnmetRequiresIsWithheld(t *testing.T) {
	srv := healthOnlyServer(t)
	defer srv.Close()
	home, cfg := deliveryMachine(t, srv.URL, writePlugin(t, "team-forks", map[string]string{
		"skills/needy/SKILL.md": "---\nname: needy\ndescription: Needs a hook that nobody ships.\nrequires: [hook:absent]\n---\n\n# Needy\n",
	}))
	cfg.Clients = map[string]clientconfig.ClientBinding{"claude": {KBs: []string{}, KBsSet: true}}
	out := withStdout(t, func() {
		if _, err := runSync(home, cfg, syncOptions{}); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := os.Stat(skillPath(home, "needy")); err == nil {
		t.Error("a skill whose requires is unmet was installed")
	}
	if _, err := os.Stat(skillPath(home, "helper")); err != nil {
		t.Errorf("the other plugin skill is missing: %v", err)
	}
	if !strings.Contains(out, "skill/needy") || !strings.Contains(out, "requires") {
		t.Errorf("the withheld skill is not reported with its reason:\n%s", out)
	}
}
