package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// symlinkedOpenCodeDir makes dir/.config/opencode a link to a real directory
// elsewhere: the whole OpenCode destination is behind a symlink (D148).
func symlinkedOpenCodeDir(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	real := filepath.Join(t.TempDir(), "dotfiles", "opencode")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(dir, ".config", "opencode")); err != nil {
		t.Fatal(err)
	}
}

// A provider whose destination is a symlink is "not connected", names what was
// refused, announces no MCP entry, and earns exit 3 (#1693).
func TestCmdConnect_ASymlinkedProviderIsNotConnectedAndExitsThree(t *testing.T) {
	srv := multiKBServer(t, `{"status":"ok","kbs":[{"name":"alpha"}]}`)
	defer srv.Close()
	dir := t.TempDir()
	setHome(t, dir)
	symlinkedOpenCodeDir(t, dir)
	var code int
	out := withStdout(t, func() {
		withStderr(t, func() {
			code = cmdConnect([]string{"opencode", "--no-input", "--server-url", srv.URL + "/mcp"})
		})
	})
	if code != exitPartiallyConnected {
		t.Errorf("exit = %d, want %d", code, exitPartiallyConnected)
	}
	if !strings.Contains(out, "not connected: opencode (MCP entry") || !strings.Contains(out, "skipped: destination is a symlink)") {
		t.Errorf("output = %q, want the not connected line naming the MCP entry", out)
	}
	if strings.Contains(out, "connected: opencode") && !strings.Contains(out, "not connected: opencode") {
		t.Errorf("output = %q, announces a connection", out)
	}
	if strings.Contains(out, "wrote MCP entry") {
		t.Errorf("output = %q, announces an MCP entry that was refused", out)
	}
}

// Only the MCP entry refused is "not connected"; only a hook refused is
// "partially connected": the agent still reaches Cartographer.
func TestPrintConnectResult_StatesByWhatWasRefused(t *testing.T) {
	cases := []struct {
		name    string
		skipped map[string][]string
		want    string
		code    int
	}{
		{"clean", nil, "connected: claude\n", 0},
		{"partial", map[string][]string{"claude": {"bootstrap hook", "skills"}}, "partially connected: claude (skipped: bootstrap hook, skills: destination is a symlink)", exitPartiallyConnected},
		{"mcp refused", map[string][]string{"claude": {"MCP entry", "skills"}}, "not connected: claude (MCP entry, skills skipped: destination is a symlink)", exitPartiallyConnected},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var code int
			claude := []string{"claude"}
			out := withStdout(t, func() {
				code = printConnectResult(t.TempDir(), claude, connectOptions{}, connectResult{Providers: claude, Skipped: c.skipped})
			})
			if code != c.code || !strings.Contains(out, c.want) {
				t.Errorf("code %d, output %q; want code %d containing %q", code, out, c.code, c.want)
			}
		})
	}
}

// A refused hook alone (Claude's settings.json is a link) is partial, in a real
// connect: the MCP entry is written and announced, the hook is not.
func TestDoConnect_ARefusedHookAloneIsPartial(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	srv := multiKBServer(t, `{"status":"ok","kbs":[{"name":"alpha"}]}`)
	defer srv.Close()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(target, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, ".claude", "settings.json")); err != nil {
		t.Fatal(err)
	}
	res, err := doConnect(connectOptions{Providers: []string{"claude"}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer"})
	if err != nil {
		t.Fatal(err)
	}
	skipped := res.Skipped["claude"]
	if len(skipped) == 0 || skipped[0] == partMCPEntry {
		t.Fatalf("skipped = %v, want the hook but not the MCP entry", skipped)
	}
	if len(res.MCPEntries) == 0 {
		t.Errorf("MCPEntries empty: the written entry must still be announced")
	}
	line, ok := providerStatusLine("claude", skipped)
	if ok || !strings.HasPrefix(line, "partially connected: claude") {
		t.Errorf("status = %q (%v), want partially connected", line, ok)
	}
}

// A refused MCP config in a real doConnect: not announced, named as skipped.
// One provider only: opencode and claude share the entry name, so a second
// provider's write would announce it and hide a regression of the filter.
func TestDoConnect_ARefusedMCPEntryIsNotAnnounced(t *testing.T) {
	srv := multiKBServer(t, `{"status":"ok","kbs":[{"name":"alpha"}]}`)
	defer srv.Close()
	dir := t.TempDir()
	symlinkedOpenCodeDir(t, dir)
	res, err := doConnect(connectOptions{Providers: []string{"opencode"}, Dir: dir, ServerURL: srv.URL + "/mcp", Name: "cartographer"})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Skipped["opencode"]; len(got) == 0 || got[0] != partMCPEntry {
		t.Errorf("opencode skipped = %v, want the MCP entry first", got)
	}
	if len(res.MCPEntries) != 0 {
		t.Errorf("MCPEntries = %v, want none: the only entry was refused", res.MCPEntries)
	}
}

// A refused removal (a prune behind a link) is a warning, not an unwired part:
// the provider stays connected (#1693).
func TestSkippedParts_ARefusedRemovalIsNotASkippedPart(t *testing.T) {
	applied := map[string]provisioning.AppliedResult{"claude": {
		RefusedRemovals: []provisioning.Artifact{{Kind: "skill", Name: "old"}},
	}}
	skipped := skippedParts(nil, nil, applied)
	if len(skipped["claude"]) != 0 {
		t.Fatalf("skipped = %v, want none", skipped["claude"])
	}
	if line, ok := providerStatusLine("claude", skipped["claude"]); !ok || line != "connected: claude" {
		t.Errorf("status = %q (%v), want connected", line, ok)
	}
	// A refused write of the same kind still counts.
	applied["claude"] = provisioning.AppliedResult{Refused: []provisioning.Artifact{{Kind: "skill", Name: "new"}}}
	if got := skippedParts(nil, nil, applied)["claude"]; len(got) != 1 || got[0] != "skills" {
		t.Errorf("skipped = %v, want [skills]", got)
	}
}

// sync shares printMCPEntryLines: a refused provider's entry is not announced.
func TestCmdSync_ARefusedEntryIsNotAnnounced(t *testing.T) {
	srv := multiKBServer(t, `{"status":"ok","kbs":[{"name":"alpha"}]}`)
	defer srv.Close()
	dir := t.TempDir()
	setHome(t, dir)
	cfg := &clientconfig.Config{ServerURL: srv.URL + "/mcp", ServerName: "wiki", Token: "${TOKEN}", Agents: []string{"opencode"}, KnownKBs: []string{"alpha"}}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	symlinkedOpenCodeDir(t, dir)
	out := withStdout(t, func() { withStderr(t, func() { cmdSync(nil) }) })
	if strings.Contains(out, "wrote MCP entry") {
		t.Errorf("output = %q, announces a refused entry", out)
	}
}

// Doctor must not send the operator to `cartographer reconnect` when the config
// is behind a link reconnect refuses too (#1693).
func TestRunDoctor_MCPEntryFixNamesTheSymlink(t *testing.T) {
	doctorStubs(t, []string{"opencode"}, false)
	dir := doctorFixture(t, "opencode")
	// Replace the written config's directory with a link to a copy lacking the entry.
	cfgDir := filepath.Join(dir, ".config", "opencode")
	real := filepath.Join(t.TempDir(), "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(cfgDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, cfgDir); err != nil {
		t.Fatal(err)
	}
	f := findingsFor(runDoctor(dir, ""), "mcp-entries")
	if len(f) == 0 {
		t.Fatal("no mcp-entries finding for a missing entry")
	}
	if strings.Contains(f[0].Fix, "cartographer reconnect") && !strings.Contains(f[0].Fix, "symlink") {
		t.Errorf("fix = %q, want it to name the symlink", f[0].Fix)
	}
	if !strings.Contains(f[0].Fix, cfgDir) {
		t.Errorf("fix = %q, want the link %s named", f[0].Fix, cfgDir)
	}
}
