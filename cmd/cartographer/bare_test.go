package main

import (
	"errors"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/service"
)

// stubBareStatus fixes the remote read bare `cartographer` shares with
// `status`: the health answer and the manifest the server serves.
func stubBareStatus(t *testing.T, health *client.Health, healthErr error, served provisioning.Manifest) {
	t.Helper()
	oldVersion, oldHealth, oldManifest, oldService := version, statusHealthFn, statusManifestsFn, statusServiceFn
	version = "v1.0.0"
	statusHealthFn = func(*clientconfig.Config) (*client.Health, error) { return health, healthErr }
	statusManifestsFn = func(_ *clientconfig.Config, ps []syncProjection) (map[string]provisioning.Manifest, error) {
		return uniformManifests(served, projectionProviders(ps)), nil
	}
	statusServiceFn = func() (service.Status, error) { return service.Status{}, nil }
	t.Cleanup(func() {
		version, statusHealthFn, statusManifestsFn, statusServiceFn = oldVersion, oldHealth, oldManifest, oldService
	})
}

func runBareOut(t *testing.T) string {
	t.Helper()
	return withStdout(t, func() {
		if code := runBare(); code != 0 {
			t.Errorf("runBare = %d, want 0 (a help screen is not a check)", code)
		}
	})
}

// The usage is the last thing printed, so the status sits above the long text.
func requireUsageLast(t *testing.T, out string) {
	t.Helper()
	i := strings.Index(out, "cartographer: the Agentic Wiki platform")
	if i < 0 || !strings.Contains(out[i:], "Usage:") {
		t.Fatalf("output must end with the usage:\n%s", out)
	}
	if strings.Contains(out[i:], "next:") || strings.Contains(out[i:], "details:") {
		t.Errorf("status lines must come before the usage:\n%s", out)
	}
}

func TestBare_NoConfigPointsAtSetup(t *testing.T) {
	setHome(t, t.TempDir())
	out := runBareOut(t)
	if !strings.HasPrefix(out, "next: run `cartographer setup`") {
		t.Errorf("output = %q, want the setup hint first", out)
	}
	requireUsageLast(t, out)
}

func TestBare_UnreadableConfigIsReportedNotFatal(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := os.WriteFile(clientconfig.Path(home), []byte("{not yaml"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runBareOut(t)
	if !strings.Contains(out, "client config cannot be read (fix or remove") {
		t.Errorf("output = %q", out)
	}
	requireUsageLast(t, out)
}

func TestBare_ConfigWithNoAgent(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := clientconfig.Save(home, &clientconfig.Config{ServerURL: "https://cartographer.example/mcp"}); err != nil {
		t.Fatal(err)
	}
	out := runBareOut(t)
	if !strings.Contains(out, "no agent connected (run `cartographer connect`)") {
		t.Errorf("output = %q", out)
	}
	requireUsageLast(t, out)
}

func TestBare_InSyncProviderAndServerLines(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := clientconfig.Save(home, &clientconfig.Config{ServerURL: "https://cartographer.example/mcp", Agents: []string{"claude"}}); err != nil {
		t.Fatal(err)
	}
	served := provisioning.Manifest{Revision: "rev1"}
	stubBareStatus(t, &client.Health{Version: "v1.0.0", LatestVersion: "v1.1.0"}, nil, served)
	appliedRev := provisioning.FilterForProvider(served, configurator.ProviderClaudeCode).Revision
	if err := provisioning.WriteLockFile(lockFilePath(home), provisioning.LockFile{Providers: map[string]provisioning.Lock{
		"claude": {Provider: "claude", AppliedRevision: appliedRev},
	}}); err != nil {
		t.Fatal(err)
	}
	out := runBareOut(t)
	for _, want := range []string{
		"client v1.0.0 — server v1.0.0 (https://cartographer.example/mcp)",
		"server update available: v1.1.0",
		"  [claude] in-sync\n",
		"details: cartographer status | cartographer doctor",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "revision") {
		t.Errorf("the bare screen is short: per-revision detail belongs to status:\n%s", out)
	}
	requireUsageLast(t, out)
}

func TestBare_DriftSuggestsSync(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := clientconfig.Save(home, &clientconfig.Config{ServerURL: "https://cartographer.example/mcp", Agents: []string{"claude"}}); err != nil {
		t.Fatal(err)
	}
	served := provisioning.Manifest{Revision: "rev2", Artifacts: []provisioning.Artifact{{Kind: "skill", Name: "runbooks", Source: "kb:wiki", ContentHash: "h", Signed: true}}}
	stubBareStatus(t, &client.Health{Version: "v1.0.0"}, nil, served)
	out := runBareOut(t)
	if !strings.Contains(out, "  [claude] drift - run `cartographer sync`") {
		t.Errorf("output = %q, want the drift line with the sync hint", out)
	}
	requireUsageLast(t, out)
}

func TestBare_UnreachableServerStillPrintsTheUsageAndExitsZero(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := clientconfig.Save(home, &clientconfig.Config{ServerURL: "https://cartographer.example/mcp", Agents: []string{"claude"}}); err != nil {
		t.Fatal(err)
	}
	stubBareStatus(t, nil, &net.OpError{Op: "dial", Err: errors.New("refused")}, provisioning.Manifest{})
	out := runBareOut(t)
	if !strings.Contains(out, "could not reach https://cartographer.example/mcp") {
		t.Errorf("output = %q, want the unreachable message", out)
	}
	if !strings.Contains(out, "  [claude] unknown") {
		t.Errorf("output = %q, want the provider as unknown", out)
	}
	requireUsageLast(t, out)
}

// The bare path is read-only: it must not create or change anything in the
// client directory.
func TestBare_WritesNothing(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	runBareOut(t)
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("bare cartographer wrote %d entries into the home directory", len(entries))
	}
}
