package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// deniedFixture makes opencode's destination directory read-only, so its write
// is denied while claude's is not, and returns the manifests, the base dir and
// the denied directory. Skipped where a mode bit does not deny (Windows, root).
func deniedFixture(t *testing.T) (map[string]contract.Manifest, string, string) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only directory does not deny the write here")
	}
	manifest := contract.Manifest{Revision: "rev-1", Artifacts: []contract.Artifact{{
		Kind: "skill", Name: "alpha", ContentHash: "hash-alpha", Source: "bundle", BuiltIn: true,
		Files: []contract.ArtifactFile{{Path: "SKILL.md", Content: []byte("# alpha\n")}},
	}}}
	agent := contract.Manifest{Revision: "rev-1", Artifacts: []contract.Artifact{{
		Kind: "agent", Name: "beta", ContentHash: "hash-beta", Source: "bundle", BuiltIn: true,
		Files: []contract.ArtifactFile{{Path: "beta.md", Content: []byte("# beta\n")}},
	}}}
	probe := t.TempDir()
	if _, err := materializeForProviders(map[string]contract.Manifest{"opencode": agent}, globalProjections([]string{"opencode"}, probe), probe, "", false, false, portabilityOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	probeLock, err := provisioning.ReadLockFile(lockFilePath(probe))
	if err != nil {
		t.Fatal(err)
	}
	managed := probeLock.ForProvider("opencode").Managed
	if len(managed) == 0 {
		t.Fatal("the probe recorded no managed file")
	}
	dir := t.TempDir()
	denied := filepath.Dir(filepath.Join(provisioning.LockBaseDir(probeLock.ForProvider("opencode"), dir), managed[0].Path))
	if err := os.MkdirAll(denied, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(denied, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(denied, 0o755) })
	return map[string]contract.Manifest{"claude": manifest, "opencode": agent}, dir, denied
}

func TestMaterializeWriteDeniedSkipsTheProviderAndGoesOn(t *testing.T) {
	manifests, dir, denied := deniedFixture(t)
	// opencode first: the old code stopped there and never reached claude.
	providers := []string{"opencode", "claude"}

	var den []writeDenial
	var errw bytes.Buffer
	results, err := materializeForProviders(manifests, globalProjections(providers, dir), dir, "", false, false, portabilityOptions{WriteDenied: &den, Streams: syncStreams{Err: &errw}}, nil)
	if err != nil {
		t.Fatalf("a denied write must not fail the pass: %v", err)
	}
	if len(den) != 1 || den[0].Provider != "opencode" || !strings.HasPrefix(den[0].Path, denied) {
		t.Fatalf("denials = %+v, want opencode under %s", den, denied)
	}
	if !strings.Contains(den[0].String(), "restore write access to "+den[0].Path) {
		t.Errorf("the line does not carry the remedy: %s", den[0])
	}
	if _, ok := results["claude"]; !ok {
		t.Error("claude was not applied")
	}
	lock, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(lock.ForProvider("claude").Managed) == 0 {
		t.Error("claude is not recorded")
	}
	if got := lock.ForProvider("opencode").Managed; len(got) != 0 {
		t.Errorf("the denied provider must keep its previous lock entry (none): %v", got)
	}

	// The exit and the report.
	var out bytes.Buffer
	code := syncUnreadableExit(&out, syncResult{WriteDenied: denialLines(den)})
	if code != exitPartiallyConnected || !strings.Contains(out.String(), "NOT synced") || !strings.Contains(out.String(), "opencode") {
		t.Errorf("exit %d, report %q", code, out.String())
	}

	// Rerun with access restored: the provider is applied and recorded.
	if err := os.Chmod(denied, 0o755); err != nil {
		t.Fatal(err)
	}
	den = nil
	if _, err := materializeForProviders(manifests, globalProjections(providers, dir), dir, "", false, false, portabilityOptions{WriteDenied: &den}, nil); err != nil || len(den) != 0 {
		t.Fatalf("rerun: err %v denials %v", err, den)
	}
	lock, _ = provisioning.ReadLockFile(lockFilePath(dir))
	if len(lock.ForProvider("opencode").Managed) == 0 {
		t.Error("opencode is not recorded after the rerun")
	}
}

func TestMaterializeWriteDeniedStaysFatalWithoutACollector(t *testing.T) {
	manifests, dir, _ := deniedFixture(t)
	_, err := materializeForProviders(manifests, globalProjections([]string{"opencode", "claude"}, dir), dir, "", false, false, portabilityOptions{}, nil)
	if err == nil || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("connect keeps the fatal error, got %v", err)
	}
}

func TestMaterializeOtherErrorsStayFatal(t *testing.T) {
	if isWriteDenied(errors.New("boom")) || isWriteDenied(nil) {
		t.Error("only a permission error is a denial")
	}
}

func TestEnsureBootstrapDeniedSettingsIsADenialNotAFailure(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only directory does not deny the write here")
	}
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(claudeDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(claudeDir, 0o755) })
	providers := []string{"claude", "codex"}
	if _, _, err := ensureBootstrapRefusals(providers, dir, false); err == nil {
		t.Fatal("connect keeps the fatal error")
	}
	warnings, denied, err := ensureBootstrapDenials(providers, dir, false)
	if err != nil {
		t.Fatalf("sync must go on: %v", err)
	}
	if len(denied) != 1 || denied[0].Provider != "claude" || len(warnings) == 0 {
		t.Fatalf("denied %+v warnings %v", denied, warnings)
	}
}

func TestLooksLikeAgentNamesSuggestsAgentsFlag(t *testing.T) {
	if !looksLikeAgentNames([]string{"codex", "opencode"}) || looksLikeAgentNames([]string{"https://example.com", "codex"}) {
		t.Error("looksLikeAgentNames")
	}
}

func TestServiceInstallConfigOnlyRefusesAnUnloadableConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfg := filepath.Join(home, "server.yaml")
	if err := os.WriteFile(cfg, []byte("kbs: [unclosed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var code int
	var out string
	errText := withStderr(t, func() {
		out = withStdout(t, func() { code = cmdServiceInstall([]string{"--config-only", "--config", cfg}) })
	})
	if code == 0 || strings.Contains(out, "config written") || !strings.Contains(errText, "cannot load") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errText)
	}
}

func TestConnectDisconnectSeveralAgentsSuggestAgentsFlag(t *testing.T) {
	for name, run := range map[string]func([]string) int{"connect": cmdConnect, "disconnect": cmdDisconnect} {
		var code int
		errText := withStderr(t, func() { code = run([]string{"codex", "opencode"}) })
		if code != 2 || !strings.Contains(errText, "--agents codex,opencode") {
			t.Errorf("%s: exit %d, stderr %q", name, code, errText)
		}
	}
}

func TestStatusJSONFlagNamesOutput(t *testing.T) {
	var code int
	errText := withStderr(t, func() { code = cmdStatus([]string{"--json"}) })
	if code != 2 || !strings.Contains(errText, "--output json") {
		t.Fatalf("exit %d, stderr %q", code, errText)
	}
}
