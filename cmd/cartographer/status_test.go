package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/service"
)

func TestCmdStatus_VersionReport(t *testing.T) {
	cases := []struct {
		name             string
		serverURL        string
		clientVersion    string
		serverVersion    string
		serviceInstalled bool
		want             []string
		dontWant         []string
	}{
		{
			name:          "matching versions",
			serverURL:     "https://cartographer.example/mcp",
			clientVersion: "v1.2.3",
			serverVersion: "v1.2.3",
			want:          []string{"client v1.2.3 — server v1.2.3 (https://cartographer.example/mcp)", "[claude] in-sync"},
			dontWant:      []string{"version skew:", "old binary"},
		},
		{
			name:          "remote skew",
			serverURL:     "https://cartographer.example/mcp",
			clientVersion: "v1.2.3",
			serverVersion: "v1.2.2",
			want:          []string{"version skew: client v1.2.3 ≠ server v1.2.2"},
			dontWant:      []string{"old binary"},
		},
		{
			name:             "loopback installed service",
			serverURL:        "http://127.0.0.1:39273/mcp",
			clientVersion:    "v1.2.3",
			serverVersion:    "v1.2.2",
			serviceInstalled: true,
			want: []string{
				"version skew: client v1.2.3 ≠ server v1.2.2",
				"local service may still run the old binary — run: cartographer upgrade-repair",
			},
		},
		{
			name:          "dev is silent",
			serverURL:     "https://cartographer.example/mcp",
			clientVersion: "dev",
			serverVersion: "v1.2.2",
			want:          []string{"client dev — server v1.2.2"},
			dontWant:      []string{"version skew:", "old binary"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			setHome(t, home)
			if err := clientconfig.Save(home, &clientconfig.Config{ServerURL: tc.serverURL, Agents: []string{"claude"}}); err != nil {
				t.Fatalf("save config: %v", err)
			}
			// Since D170 the lock records the revision of the manifest a
			// provider actually receives, so the fixture derives it from the
			// same manifest it serves rather than hardcoding a matching pair.
			served := provisioning.Manifest{Revision: "rev1"}
			appliedRev := provisioning.FilterForProvider(served, configurator.ProviderClaudeCode).Revision
			if err := provisioning.WriteLockFile(lockFilePath(home), provisioning.LockFile{Providers: map[string]provisioning.Lock{
				"claude": {Provider: "claude", AppliedRevision: appliedRev},
			}}); err != nil {
				t.Fatalf("write lockfile: %v", err)
			}

			oldVersion, oldHealth, oldManifest, oldService := version, statusHealthFn, statusManifestsFn, statusServiceFn
			version = tc.clientVersion
			statusHealthFn = func(*clientconfig.Config) (*client.Health, error) {
				return &client.Health{Version: tc.serverVersion}, nil
			}
			statusManifestsFn = func(_ *clientconfig.Config, ps []syncProjection) (map[string]provisioning.Manifest, error) {
				return uniformManifests(served, projectionProviders(ps)), nil
			}
			statusServiceFn = func() (service.Status, error) { return service.Status{Installed: tc.serviceInstalled}, nil }
			t.Cleanup(func() {
				version, statusHealthFn, statusManifestsFn, statusServiceFn = oldVersion, oldHealth, oldManifest, oldService
			})

			out := withStdout(t, func() {
				if code := cmdStatus(nil); code != 0 {
					t.Errorf("cmdStatus = %d, want 0", code)
				}
			})
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("output = %q, want %q", out, want)
				}
			}
			for _, dontWant := range tc.dontWant {
				if strings.Contains(out, dontWant) {
					t.Errorf("output = %q, must not contain %q", out, dontWant)
				}
			}
		})
	}
}

// D139: a managed artifact edited or deleted locally is drift, even when the
// manifest and the lockfile agree. status exits 1 where it used to exit 0.
func TestStatus_OnDiskDriftExitsOne(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := clientconfig.Save(home, &clientconfig.Config{ServerURL: "https://cartographer.example/mcp", Agents: []string{"claude"}}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	// A materialized skill, recorded in the lockfile with its materialized hash.
	skillDir := filepath.Join(home, ".claude", "skills", "runbooks")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("---\nname: runbooks\ndescription: d\n---\nBody.\n")
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	onDisk, err := provisioning.ContentHashDirOSForKind(skillDir, "skill")
	if err != nil {
		t.Fatalf("hash skill dir: %v", err)
	}
	served := provisioning.Manifest{Revision: "rev1", Artifacts: []provisioning.Artifact{
		{Kind: "skill", Name: "runbooks", Source: "kb:wiki", ContentHash: "source-hash", Signed: true},
	}}
	// D170: the lock carries the revision of the provider's own view.
	appliedRev := provisioning.FilterForProvider(served, configurator.ProviderClaudeCode).Revision
	lock := provisioning.Lock{Provider: "claude", AppliedRevision: appliedRev, Managed: []provisioning.ManagedFile{{
		Kind: "skill", Name: "runbooks", Path: ".claude/skills/runbooks/SKILL.md",
		ContentHash: "source-hash", MaterializedHash: onDisk,
	}}}
	if err := provisioning.WriteLockFile(lockFilePath(home), provisioning.LockFile{Providers: map[string]provisioning.Lock{"claude": lock}}); err != nil {
		t.Fatalf("write lockfile: %v", err)
	}

	oldVersion, oldHealth, oldManifest, oldService := version, statusHealthFn, statusManifestsFn, statusServiceFn
	version = "v1.0.0"
	statusHealthFn = func(*clientconfig.Config) (*client.Health, error) { return &client.Health{Version: "v1.0.0"}, nil }
	statusManifestsFn = func(_ *clientconfig.Config, ps []syncProjection) (map[string]provisioning.Manifest, error) {
		return uniformManifests(served, projectionProviders(ps)), nil
	}
	statusServiceFn = func() (service.Status, error) { return service.Status{}, nil }
	t.Cleanup(func() {
		version, statusHealthFn, statusManifestsFn, statusServiceFn = oldVersion, oldHealth, oldManifest, oldService
	})

	// Untouched: in-sync.
	out := withStdout(t, func() {
		if code := cmdStatus(nil); code != 0 {
			t.Errorf("cmdStatus on a clean tree = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "in-sync") {
		t.Errorf("output = %q, want in-sync", out)
	}

	// Edited by hand: drift, exit 1, and the finding is named.
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), append(body, []byte("local edit\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	out = withStdout(t, func() {
		if code := cmdStatus(nil); code != 1 {
			t.Errorf("cmdStatus on a locally modified artifact = %d, want 1", code)
		}
	})
	if !strings.Contains(out, "diverged on disk (modified)") || !strings.Contains(out, "skill/runbooks") {
		t.Errorf("output = %q, want the divergence reported", out)
	}
}

// D189: a managed instructions block written into a file the provider does not
// read is not installed. `instructions n/n` must not count it, and status must
// say so — the previous behaviour printed `instructions 1/1` while the agent had
// never seen the directives.
func TestStatus_ShadowedInstructionsNotCountedAsInstalled(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := clientconfig.Save(home, &clientconfig.Config{ServerURL: "https://cartographer.example/mcp", Agents: []string{"codex"}}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	rel := provisioning.InstructionsFile(configurator.ProviderCodex)
	path := filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	block := []byte("<!-- cartographer:instructions:begin -->\nx\n<!-- cartographer:instructions:end -->\n")
	if err := os.WriteFile(path, block, 0o644); err != nil {
		t.Fatal(err)
	}
	served := provisioning.Manifest{Revision: "rev1", Artifacts: []provisioning.Artifact{
		{Kind: "instructions", Name: "wiki", Source: "kb:wiki", ContentHash: "source-hash", Signed: true},
	}}
	appliedRev := provisioning.FilterForProvider(served, configurator.ProviderCodex).Revision
	lock := provisioning.Lock{Provider: "codex", AppliedRevision: appliedRev, Managed: []provisioning.ManagedFile{{
		Kind: "instructions", Name: "wiki", Path: rel, ContentHash: "source-hash",
	}}}
	if err := provisioning.WriteLockFile(lockFilePath(home), provisioning.LockFile{Providers: map[string]provisioning.Lock{"codex": lock}}); err != nil {
		t.Fatalf("write lockfile: %v", err)
	}

	oldVersion, oldHealth, oldManifest, oldService := version, statusHealthFn, statusManifestsFn, statusServiceFn
	version = "v1.0.0"
	statusHealthFn = func(*clientconfig.Config) (*client.Health, error) { return &client.Health{Version: "v1.0.0"}, nil }
	statusManifestsFn = func(_ *clientconfig.Config, ps []syncProjection) (map[string]provisioning.Manifest, error) {
		return uniformManifests(served, projectionProviders(ps)), nil
	}
	statusServiceFn = func() (service.Status, error) { return service.Status{}, nil }
	t.Cleanup(func() {
		version, statusHealthFn, statusManifestsFn, statusServiceFn = oldVersion, oldHealth, oldManifest, oldService
	})

	out := withStdout(t, func() { cmdStatus(nil) })
	if !strings.Contains(out, "instructions 1/1") {
		t.Fatalf("without an override the block is installed; output = %q", out)
	}
	if strings.Contains(out, "not active") {
		t.Errorf("nothing shadows it yet: %q", out)
	}

	// The user's own override appears. Codex reads it *instead of* AGENTS.md.
	if err := os.WriteFile(filepath.Join(home, ".codex", "AGENTS.override.md"), []byte("# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out = withStdout(t, func() { cmdStatus(nil) })
	if strings.Contains(out, "instructions 1/1") {
		t.Errorf("a shadowed block must not count as installed: %q", out)
	}
	if !strings.Contains(out, "instructions 0/1") {
		t.Errorf("expected instructions 0/1: %q", out)
	}
	if !strings.Contains(out, "not active") || !strings.Contains(out, "AGENTS.override.md") {
		t.Errorf("status must name the shadowing file: %q", out)
	}
}

// #1011: a provider in workspace scope (D193) is judged the way sync applies
// it: the global projection against the bundle only, each workspace against
// its own projection. The provider-wide manifest of every bound KB used to be
// compared with the bundle-only global lock, so a clean sync reported drift
// (exit 1) with every KB artifact listed as missing.
func TestStatus_WorkspaceScopedProviderInSyncAfterCleanSync(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	ws := t.TempDir()
	cfg := &clientconfig.Config{ServerURL: "https://cartographer.example/mcp", Agents: []string{"claude"}}
	if err := cfg.SetWorkspaceScope("claude", clientconfig.ScopeWorkspace); err != nil {
		t.Fatal(err)
	}
	if err := cfg.BindWorkspace("claude", ws, []string{"kb-a"}); err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	canon := cfg.WorkspaceBindings("claude")[0].Path

	bundle := provisioning.Artifact{Kind: "skill", Name: "cartographer-ops", Source: "bundle", ContentHash: "bundle-hash", Signed: true}
	kbSkill := provisioning.Artifact{Kind: "skill", Name: "runbooks", Source: "kb:kb-a", ContentHash: "kb-hash", Signed: true}
	claude := configurator.ProviderClaudeCode
	globalM := provisioning.Manifest{Artifacts: []provisioning.Artifact{bundle}}
	wsM := provisioning.Manifest{Artifacts: []provisioning.Artifact{kbSkill}}
	globalM.Revision = provisioning.FilterForProviderScoped(globalM, claude, provisioning.ScopeGlobal).Revision
	wsM.Revision = provisioning.FilterForProviderScoped(wsM, claude, provisioning.ScopeProject).Revision

	var lf provisioning.LockFile
	lf.SetProjection(provisioning.Projection{Provider: "claude"}, provisioning.Lock{AppliedRevision: globalM.Revision, Managed: []provisioning.ManagedFile{
		{Kind: "skill", Name: "cartographer-ops", Path: ".claude/skills/cartographer-ops/SKILL.md", ContentHash: "bundle-hash"},
	}}, "")
	lf.SetProjection(provisioning.Projection{Provider: "claude", Workspace: canon}, provisioning.Lock{AppliedRevision: wsM.Revision, Managed: []provisioning.ManagedFile{
		{Kind: "skill", Name: "runbooks", Path: ".claude/skills/runbooks/SKILL.md", ContentHash: "kb-hash"},
	}}, "")
	if err := provisioning.WriteLockFile(lockFilePath(home), lf); err != nil {
		t.Fatalf("write lockfile: %v", err)
	}

	oldVersion, oldHealth, oldManifest, oldService := version, statusHealthFn, statusManifestsFn, statusServiceFn
	version = "v1.0.0"
	statusHealthFn = func(*clientconfig.Config) (*client.Health, error) { return &client.Health{Version: "v1.0.0"}, nil }
	statusManifestsFn = func(_ *clientconfig.Config, ps []syncProjection) (map[string]provisioning.Manifest, error) {
		out := map[string]provisioning.Manifest{}
		for _, p := range ps {
			if p.BundleOnly {
				out[projectionKey(p)] = globalM
			} else {
				out[projectionKey(p)] = wsM
			}
		}
		return out, nil
	}
	statusServiceFn = func() (service.Status, error) { return service.Status{}, nil }
	t.Cleanup(func() {
		version, statusHealthFn, statusManifestsFn, statusServiceFn = oldVersion, oldHealth, oldManifest, oldService
	})

	// The files the lock names are absent on disk: the on-disk check reports
	// them, so write them (content is irrelevant without a materialized hash).
	for _, f := range []string{
		filepath.Join(home, ".claude/skills/cartographer-ops/SKILL.md"),
		filepath.Join(canon, ".claude/skills/runbooks/SKILL.md"),
	} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	out := withStdout(t, func() {
		if code := cmdStatus(nil); code != 0 {
			t.Errorf("cmdStatus after a clean sync = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "in-sync") || strings.Contains(out, "+ skill/") {
		t.Errorf("output = %q, want in-sync with no KB artifact listed as missing", out)
	}

	// A workspace artifact the sync has not materialized yet is real drift.
	lf.Workspaces[canon] = provisioning.WorkspaceLocks{Providers: map[string]provisioning.Lock{"claude": {BaseDir: canon, ProjectScope: true}}}
	if err := provisioning.WriteLockFile(lockFilePath(home), lf); err != nil {
		t.Fatal(err)
	}
	out = withStdout(t, func() {
		if code := cmdStatus(nil); code != 1 {
			t.Errorf("cmdStatus with a missing workspace artifact = %d, want 1", code)
		}
	})
	if !strings.Contains(out, "+ skill/runbooks") {
		t.Errorf("output = %q, want the missing workspace artifact", out)
	}
}

// projectionProviders lists the providers a set of projections belongs to, for
// the stubs of statusManifestsFn that serve one manifest per provider.
func projectionProviders(ps []syncProjection) []string {
	var out []string
	for _, p := range ps {
		if p.Workspace == "" {
			out = append(out, p.Provider)
		}
	}
	return out
}

// #1011 review: sync builds every projection of the run in ONE call, because a
// bundle-only projection receives the bundle depending on what the whole run
// pulls (buildProjectionManifests). status must do the same, or a
// workspace-scoped provider with no KB bindings next to a default-bound one is
// judged against an empty bundle and reported as drift.
func TestStatus_BuildsAllProjectionsInOneCall(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	cfg := &clientconfig.Config{ServerURL: "https://cartographer.example/mcp", Agents: []string{"claude", "codex"}}
	if err := cfg.SetWorkspaceScope("claude", clientconfig.ScopeWorkspace); err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}

	oldVersion, oldHealth, oldManifest, oldService := version, statusHealthFn, statusManifestsFn, statusServiceFn
	version = "v1.0.0"
	statusHealthFn = func(*clientconfig.Config) (*client.Health, error) { return &client.Health{Version: "v1.0.0"}, nil }
	var calls [][]syncProjection
	statusManifestsFn = func(_ *clientconfig.Config, ps []syncProjection) (map[string]provisioning.Manifest, error) {
		calls = append(calls, ps)
		out := map[string]provisioning.Manifest{}
		for _, p := range ps {
			out[projectionKey(p)] = provisioning.Manifest{}
		}
		return out, nil
	}
	statusServiceFn = func() (service.Status, error) { return service.Status{}, nil }
	t.Cleanup(func() {
		version, statusHealthFn, statusManifestsFn, statusServiceFn = oldVersion, oldHealth, oldManifest, oldService
	})

	withStdout(t, func() { cmdStatus(nil) })
	if len(calls) != 1 {
		t.Fatalf("manifests built in %d calls, want 1 (as sync does)", len(calls))
	}
	var bundleOnly, codexGlobal bool
	for _, p := range calls[0] {
		bundleOnly = bundleOnly || (p.Provider == "claude" && p.BundleOnly)
		codexGlobal = codexGlobal || (p.Provider == "codex" && p.Workspace == "" && !p.BundleOnly)
	}
	if !bundleOnly || !codexGlobal {
		t.Errorf("the single call must carry the bundle-only claude projection and the default codex one: %+v", calls[0])
	}
}

// #1011 review: when sync would refuse a provider before projecting it, the
// status card says why instead of a bare "drift".
func TestStatus_WorkspaceProblemNamesTheCause(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	ws := t.TempDir()
	cfg := &clientconfig.Config{ServerURL: "https://cartographer.example/mcp", Agents: []string{"claude"}}
	if err := cfg.SetWorkspaceScope("claude", clientconfig.ScopeWorkspace); err != nil {
		t.Fatal(err)
	}
	if err := cfg.BindWorkspace("claude", ws, []string{"kb-a"}); err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(ws); err != nil {
		t.Fatal(err)
	}

	oldVersion, oldHealth, oldManifest, oldService := version, statusHealthFn, statusManifestsFn, statusServiceFn
	version = "v1.0.0"
	statusHealthFn = func(*clientconfig.Config) (*client.Health, error) { return &client.Health{Version: "v1.0.0"}, nil }
	statusManifestsFn = func(_ *clientconfig.Config, ps []syncProjection) (map[string]provisioning.Manifest, error) {
		return map[string]provisioning.Manifest{}, nil
	}
	statusServiceFn = func() (service.Status, error) { return service.Status{}, nil }
	t.Cleanup(func() {
		version, statusHealthFn, statusManifestsFn, statusServiceFn = oldVersion, oldHealth, oldManifest, oldService
	})

	out := withStdout(t, func() {
		if code := cmdStatus(nil); code != 1 {
			t.Errorf("cmdStatus with a gone workspace = %d, want 1", code)
		}
	})
	if !strings.Contains(out, "cannot sync:") || !strings.Contains(out, "is gone") {
		t.Errorf("output = %q, want the cause named", out)
	}
}

// #967: a placeholder that now resolves elsewhere than the lockfile recorded
// leaves the materialized artifact stale; status must say so, as sync rewrites it.
func TestStatus_PlaceholderResolutionDriftIsDrift(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	save := func(path string) {
		t.Helper()
		if err := clientconfig.Save(home, &clientconfig.Config{ServerURL: "https://cartographer.example/mcp", Agents: []string{"claude"}, Paths: map[string]string{"tools": path}}); err != nil {
			t.Fatalf("save config: %v", err)
		}
	}
	skillDir := filepath.Join(home, ".claude", "skills", "runbooks")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: runbooks\ndescription: d\n---\nTools: /srv/old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	onDisk, err := provisioning.ContentHashDirOSForKind(skillDir, "skill")
	if err != nil {
		t.Fatal(err)
	}
	src := []byte("---\nname: runbooks\ndescription: d\n---\nTools: {{path:tools}}\n")
	served := provisioning.Manifest{Revision: "rev1", Artifacts: []provisioning.Artifact{{
		Kind: "skill", Name: "runbooks", Source: "kb:wiki", ContentHash: "source-hash", Signed: true,
		Files: []provisioning.ArtifactFile{{Path: "SKILL.md", Content: src}},
	}}}
	lock := provisioning.Lock{
		Provider: "claude", AppliedRevision: provisioning.FilterForProvider(served, configurator.ProviderClaudeCode).Revision,
		Managed: []provisioning.ManagedFile{{
			Kind: "skill", Name: "runbooks", Path: ".claude/skills/runbooks/SKILL.md",
			ContentHash: "source-hash", MaterializedHash: onDisk,
		}},
		ResolvedPlaceholders: map[string]string{"path:tools": "/srv/old"},
	}
	if err := provisioning.WriteLockFile(lockFilePath(home), provisioning.LockFile{Providers: map[string]provisioning.Lock{"claude": lock}}); err != nil {
		t.Fatal(err)
	}
	oldVersion, oldHealth, oldManifest, oldService := version, statusHealthFn, statusManifestsFn, statusServiceFn
	version = "v1.0.0"
	statusHealthFn = func(*clientconfig.Config) (*client.Health, error) { return &client.Health{Version: "v1.0.0"}, nil }
	statusManifestsFn = func(_ *clientconfig.Config, ps []syncProjection) (map[string]provisioning.Manifest, error) {
		return uniformManifests(served, projectionProviders(ps)), nil
	}
	statusServiceFn = func() (service.Status, error) { return service.Status{}, nil }
	t.Cleanup(func() {
		version, statusHealthFn, statusManifestsFn, statusServiceFn = oldVersion, oldHealth, oldManifest, oldService
	})

	save("/srv/old")
	out := withStdout(t, func() {
		if code := cmdStatus(nil); code != 0 {
			t.Errorf("unchanged resolution: cmdStatus = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "in-sync") {
		t.Errorf("output = %q, want in-sync", out)
	}

	save("/srv/new")
	out = withStdout(t, func() {
		if code := cmdStatus(nil); code != 1 {
			t.Errorf("changed resolution: cmdStatus = %d, want 1", code)
		}
	})
	if !strings.Contains(out, "runbooks") || strings.Contains(out, "in-sync") {
		t.Errorf("output = %q, want the stale skill reported as drift", out)
	}
}

// D1141: a hook installed before the approval contract and unchanged on the
// server is not in the diff, yet sync holds it pending: status says so too, and
// an approval makes it in sync again.
func TestStatus_HeldPreExistingHookIsPendingUntilApproved(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	cfg := &clientconfig.Config{ServerURL: "https://cartographer.example/mcp", Agents: []string{"claude"}}
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	served := provisioning.Manifest{Revision: "rev1", Artifacts: []provisioning.Artifact{{
		Kind: "hook", Name: "guard", Source: "kb:kb-a", ContentHash: "hook-hash",
		Files: []provisioning.ArtifactFile{{Path: "hook.json", Content: []byte(`{"event":"SessionStart","command":"./run.sh"}`)}},
	}}}
	appliedRev := provisioning.FilterForProvider(served, configurator.ProviderClaudeCode).Revision
	lock := provisioning.Lock{Provider: "claude", AppliedRevision: appliedRev, Managed: []provisioning.ManagedFile{{
		Kind: "hook", Name: "guard", Path: ".claude/hooks/guard/hook.json", ContentHash: "hook-hash",
	}}}
	hookDir := filepath.Join(home, ".claude", "hooks", "guard")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "hook.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := provisioning.WriteLockFile(lockFilePath(home), provisioning.LockFile{Providers: map[string]provisioning.Lock{"claude": lock}}); err != nil {
		t.Fatal(err)
	}
	oldVersion, oldHealth, oldManifest, oldService := version, statusHealthFn, statusManifestsFn, statusServiceFn
	version = "v1.0.0"
	statusHealthFn = func(*clientconfig.Config) (*client.Health, error) { return &client.Health{Version: "v1.0.0"}, nil }
	statusManifestsFn = func(_ *clientconfig.Config, ps []syncProjection) (map[string]provisioning.Manifest, error) {
		return uniformManifests(served, projectionProviders(ps)), nil
	}
	statusServiceFn = func() (service.Status, error) { return service.Status{}, nil }
	t.Cleanup(func() {
		version, statusHealthFn, statusManifestsFn, statusServiceFn = oldVersion, oldHealth, oldManifest, oldService
	})

	out := withStdout(t, func() {
		if code := cmdStatus(nil); code != 1 {
			t.Errorf("cmdStatus with a held hook = %d, want 1", code)
		}
	})
	if !strings.Contains(out, "hook/guard") || !strings.Contains(out, "needs_approval") {
		t.Errorf("output = %q, want the hook reported pending", out)
	}

	if err := cfg.ApproveMCP("kb-a", "hook/guard", "hook-hash", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	out = withStdout(t, func() {
		if code := cmdStatus(nil); code != 0 {
			t.Errorf("cmdStatus after approval = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "in-sync") {
		t.Errorf("output = %q, want in-sync", out)
	}
}

// #1188: an instructions projection a derived workspace deliberately does not
// write (its file is tracked, D1019) is not pending: the `instructions not
// written` note says so, and the diff must not list it as missing.
func TestWorkspaceProviderDiff_DerivedTrackedInstructionsAreNotPending(t *testing.T) {
	ws := t.TempDir()
	instr := provisioning.Artifact{Kind: "instructions", Name: "kb-a", Source: "kb:kb-a", ContentHash: "h1", Signed: true}
	m := provisioning.Manifest{Artifacts: []provisioning.Artifact{instr}}
	sp := syncProjection{Provider: "codex", Workspace: ws, BaseDir: ws, Scope: provisioning.ScopeProject, Derived: true, InstructionsSkipped: "AGENTS.md"}
	manifests := map[string]provisioning.Manifest{projectionKey(sp): m}
	var lf provisioning.LockFile

	_, d, _ := workspaceProviderDiff(&clientconfig.Config{}, []syncProjection{sp}, manifests, lf, t.TempDir())
	if len(d.Added) != 0 || len(d.Updated) != 0 || len(d.Removed) != 0 {
		t.Errorf("a deliberately unwritten projection is not drift: %+v", d)
	}

	// The same projection with the file untracked is written, so absent from
	// the lock it is pending: the filter is the skip, not the kind.
	sp.InstructionsSkipped = ""
	_, d, _ = workspaceProviderDiff(&clientconfig.Config{}, []syncProjection{sp}, manifests, lf, t.TempDir())
	if len(d.Added) != 1 {
		t.Errorf("an instructions block that will be written is pending: %+v", d)
	}
}
