package main

import (
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
			serverURL:        "http://127.0.0.1:8080/mcp",
			clientVersion:    "v1.2.3",
			serverVersion:    "v1.2.2",
			serviceInstalled: true,
			want: []string{
				"version skew: client v1.2.3 ≠ server v1.2.2",
				"local service may still run the old binary — run: cartographer service restart",
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
