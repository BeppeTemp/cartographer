package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
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
			t.Setenv("HOME", home)
			if err := clientconfig.Save(home, &clientconfig.Config{ServerURL: tc.serverURL, Agents: []string{"claude"}, Trust: true}); err != nil {
				t.Fatalf("save config: %v", err)
			}
			if err := provisioning.WriteLockFile(lockFilePath(home), provisioning.LockFile{Providers: map[string]provisioning.Lock{
				"claude": {Provider: "claude", AppliedRevision: "rev1"},
			}}); err != nil {
				t.Fatalf("write lockfile: %v", err)
			}

			oldVersion, oldHealth, oldManifest, oldService := version, statusHealthFn, statusManifestFn, statusServiceFn
			version = tc.clientVersion
			statusHealthFn = func(*clientconfig.Config) (*client.Health, error) {
				return &client.Health{Version: tc.serverVersion}, nil
			}
			statusManifestFn = func(*clientconfig.Config) (provisioning.Manifest, error) {
				return provisioning.Manifest{Revision: "rev1"}, nil
			}
			statusServiceFn = func() (service.Status, error) { return service.Status{Installed: tc.serviceInstalled}, nil }
			t.Cleanup(func() {
				version, statusHealthFn, statusManifestFn, statusServiceFn = oldVersion, oldHealth, oldManifest, oldService
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
	t.Setenv("HOME", home)
	if err := clientconfig.Save(home, &clientconfig.Config{ServerURL: "https://cartographer.example/mcp", Agents: []string{"claude"}, Trust: true}); err != nil {
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
	lock := provisioning.Lock{Provider: "claude", AppliedRevision: "rev1", Managed: []provisioning.ManagedFile{{
		Kind: "skill", Name: "runbooks", Path: filepath.Join(".claude", "skills", "runbooks", "SKILL.md"),
		ContentHash: "source-hash", MaterializedHash: onDisk,
	}}}
	if err := provisioning.WriteLockFile(lockFilePath(home), provisioning.LockFile{Providers: map[string]provisioning.Lock{"claude": lock}}); err != nil {
		t.Fatalf("write lockfile: %v", err)
	}

	oldVersion, oldHealth, oldManifest, oldService := version, statusHealthFn, statusManifestFn, statusServiceFn
	version = "v1.0.0"
	statusHealthFn = func(*clientconfig.Config) (*client.Health, error) { return &client.Health{Version: "v1.0.0"}, nil }
	statusManifestFn = func(*clientconfig.Config) (provisioning.Manifest, error) {
		return provisioning.Manifest{Revision: "rev1", Artifacts: []provisioning.Artifact{
			{Kind: "skill", Name: "runbooks", Source: "kb:wiki", ContentHash: "source-hash", Signed: true},
		}}, nil
	}
	statusServiceFn = func() (service.Status, error) { return service.Status{}, nil }
	t.Cleanup(func() {
		version, statusHealthFn, statusManifestFn, statusServiceFn = oldVersion, oldHealth, oldManifest, oldService
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
