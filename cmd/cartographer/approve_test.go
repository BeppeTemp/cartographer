package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

func TestParseApproveMCPArgs(t *testing.T) {
	name, kb, yes, ok := parseApproveMCPArgs([]string{"tools", "--kb", "wiki", "--yes"})
	if !ok || name != "tools" || kb != "wiki" || !yes {
		t.Fatalf("got %q %q %v %v", name, kb, yes, ok)
	}
	if _, _, _, ok := parseApproveMCPArgs([]string{"--yes"}); ok {
		t.Fatal("missing name accepted")
	}
	if _, _, _, ok := parseApproveMCPArgs([]string{"tools", "--kb"}); ok {
		t.Fatal("missing kb accepted")
	}
}

func approveFixture(t *testing.T, interactive bool) (home string, artifacts []contract.Artifact) {
	t.Helper()
	home = t.TempDir()
	setHome(t, home)
	if err := clientconfig.Save(home, &clientconfig.Config{ServerURL: "https://cartographer.example/mcp", Agents: []string{"claude"}}); err != nil {
		t.Fatal(err)
	}
	artifacts = []contract.Artifact{
		{Kind: "hook", Name: "guard", Source: "kb:kb-a", ContentHash: "hook-hash-0123456789", Files: []contract.ArtifactFile{
			{Path: "hook.json", Content: []byte(`{"event":"SessionStart","command":"./run.sh"}`)},
			{Path: "run.sh", Content: []byte("#!/bin/sh\necho SCRIPT-BODY\n"), Executable: true},
		}},
		{Kind: "skill", Name: "plain", Source: "kb:kb-a", ContentHash: "skill-hash", Files: []contract.ArtifactFile{
			{Path: "SKILL.md", Content: []byte("---\nname: plain\ndescription: d\n---\n")},
		}},
	}
	oldM, oldI := approveManifest, approveInteractive
	approveManifest = func(*clientconfig.Config) (contract.Manifest, error) {
		return contract.Manifest{Revision: "r", Artifacts: artifacts}, nil
	}
	approveInteractive = func() bool { return interactive }
	t.Cleanup(func() { approveManifest, approveInteractive = oldM, oldI })
	return home, artifacts
}

func TestCmdApprove_HookStoresKindNameKeyAndShowsContent(t *testing.T) {
	home, _ := approveFixture(t, false)
	var code int
	out := withStdout(t, func() { code = cmdApprove([]string{"hook", "guard", "--kb", "kb-a", "--yes"}) })
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{`"command":"./run.sh"`, "SCRIPT-BODY", "run.sh", "(executable)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output must show %q:\n%s", want, out)
		}
	}
	cfg, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.MCPApprovals["kb-a"]["hook/guard"].ContentHash; got != "hook-hash-0123456789" {
		t.Fatalf("approval stored under hook/guard = %q (%+v)", got, cfg.MCPApprovals)
	}
	if _, bare := cfg.MCPApprovals["kb-a"]["guard"]; bare {
		t.Error("a hook approval must not be stored under the bare MCP key")
	}

	// Revoke without --kb finds the source through the <kind>/<name> key, and
	// says the installed hook stays.
	out = withStdout(t, func() { code = cmdApprove([]string{"revoke", "hook", "guard"}) })
	if code != 0 || !strings.Contains(out, "stays installed") {
		t.Fatalf("revoke = %d, output %q", code, out)
	}
	cfg, _ = clientconfig.Load(home)
	if len(cfg.MCPApprovals) != 0 {
		t.Errorf("approval not revoked: %+v", cfg.MCPApprovals)
	}
}

func TestCmdApprove_RefusesWhatNeedsNoApproval(t *testing.T) {
	home, _ := approveFixture(t, false)
	if code := cmdApprove([]string{"skill", "plain", "--kb", "kb-a", "--yes"}); code != 2 {
		t.Fatalf("a plain skill must be refused, exit %d", code)
	}
	cfg, _ := clientconfig.Load(home)
	if len(cfg.MCPApprovals) != 0 {
		t.Errorf("nothing must be stored: %+v", cfg.MCPApprovals)
	}
	if code := cmdApprove([]string{"widget", "x"}); code != 2 {
		t.Errorf("unknown kind exit %d, want 2", code)
	}
}

func TestCmdApprove_NonInteractiveWithoutYesDoesNotApprove(t *testing.T) {
	home, _ := approveFixture(t, false)
	if code := cmdApprove([]string{"hook", "guard", "--kb", "kb-a"}); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	cfg, _ := clientconfig.Load(home)
	if len(cfg.MCPApprovals) != 0 {
		t.Errorf("approved without --yes: %+v", cfg.MCPApprovals)
	}
}

// A revoke of an approval that was never given says so, with or without --kb;
// with several sources it is ambiguous and names them.
func TestCmdApprove_RevokeOfAMissingApprovalSaysSo(t *testing.T) {
	approveFixture(t, false)
	for _, args := range [][]string{
		{"revoke", "hook", "guard", "--kb", "kb-a"},
		{"revoke", "hook", "guard"},
	} {
		var code int
		stderr := withStderr(t, func() { withStdout(t, func() { code = cmdApprove(args) }) })
		if code != 2 || !strings.Contains(stderr, "no approval for hook guard") {
			t.Errorf("%v: exit %d, stderr %q, want exit 2 and 'no approval ... to revoke'", args, code, stderr)
		}
		if strings.Contains(stderr, "ambiguous") {
			t.Errorf("%v: zero approvals reported as ambiguous: %q", args, stderr)
		}
	}
}

// reindex and kb repair used to turn a Load error into the defaults and talk to
// 127.0.0.1; an invalid config must be named instead.
func TestLoadClientConfigOrDefault(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if cfg, err := loadClientConfigOrDefault(); err != nil || cfg == nil {
		t.Fatalf("no file: cfg=%v err=%v, want the defaults", cfg, err)
	}
	if err := os.WriteFile(clientconfig.Path(home), []byte("server_url: [unterminated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := loadClientConfigOrDefault()
	if err == nil || !strings.Contains(err.Error(), clientconfig.FileName) {
		t.Errorf("an invalid config must be reported with its file name, got %v", err)
	}
	var code int
	stderr := withStderr(t, func() { code = cmdReindex(nil) })
	if code == 0 || !strings.Contains(stderr, "cannot be read") {
		t.Errorf("reindex = %d, stderr %q, want the parse failure named", code, stderr)
	}
}

// A client config that is there but invalid is named, not reported as missing:
// sync and the workspace commands used to send the operator to `connect`, which
// refuses to replace the file.
func TestInvalidClientConfigIsNamedBySyncAndWorkspace(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := os.WriteFile(clientconfig.Path(home), []byte("server_url: [unterminated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var code int
	stderr := withStderr(t, func() { withStdout(t, func() { code = cmdSync(nil) }) })
	if code != 2 || !strings.Contains(stderr, "cannot be read") || strings.Contains(stderr, "no client config found") {
		t.Errorf("sync = %d, stderr %q, want the unreadable file named", code, stderr)
	}
	if _, _, err := loadWorkspaceConfig(); err == nil || !strings.Contains(err.Error(), "cannot be read") {
		t.Errorf("loadWorkspaceConfig = %v, want the unreadable file named", err)
	}
	if _, _, release, err := loadWorkspaceConfigLocked(); err == nil || !strings.Contains(err.Error(), "cannot be read") {
		t.Errorf("loadWorkspaceConfigLocked = %v, want the unreadable file named", err)
	} else if release != nil {
		release()
	}
	// Missing file keeps the old advice.
	missing := t.TempDir()
	setHome(t, missing)
	if _, _, err := loadWorkspaceConfig(); err == nil || !strings.Contains(err.Error(), "cartographer connect") {
		t.Errorf("missing config: %v, want the connect advice", err)
	}
}

// The preview of an MCP descriptor lists what changes how the process starts:
// the env_literal constants and the variables it reads, not only the target.
func TestApprovalPreviewListsEnvLiteralAndReadVars(t *testing.T) {
	desc := `{"type":"stdio","command":"srv","env":{"K":"${EXAMPLE_KEY}"},"env_literal":{"NODE_OPTIONS":"--require /tmp/x.js","LD_PRELOAD":"/tmp/y.so"}}`
	a := contract.Artifact{Kind: "mcp", Name: "svc", Source: "kb:kb-a", ContentHash: "abcdef0123456789", Files: []contract.ArtifactFile{{Path: "svc.json", Content: []byte(desc)}}}
	var b strings.Builder
	if err := printApprovalPreview(&b, a, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"env_literal: NODE_OPTIONS=--require /tmp/x.js", "env_literal: LD_PRELOAD=/tmp/y.so", "reads: EXAMPLE_KEY"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("preview misses %q:\n%s", want, b.String())
		}
	}
}

func mcpArtifact(hash, url string) contract.Artifact {
	return contract.Artifact{Kind: "mcp", Name: "tools", Source: "kb:kb-a", ContentHash: hash, Files: []contract.ArtifactFile{
		{Path: "tools.json", Content: []byte(`{"type":"http","url":"` + url + `","headers":{"X":"${VAR}"}}`)},
	}}
}

func TestParseApproveSyncFlagIsStrippedBeforeParsing(t *testing.T) {
	approveFixture(t, false)
	calls := 0
	old := syncFn
	syncFn = func(args []string) int { calls++; return 7 }
	t.Cleanup(func() { syncFn = old })
	var code int
	withStdout(t, func() { code = cmdApprove([]string{"hook", "guard", "--kb", "kb-a", "--yes", "--sync"}) })
	if code != 7 || calls != 1 {
		t.Fatalf("exit %d, sync calls %d, want the sync's exit code once", code, calls)
	}
	// A declined approval, an error and a missing --yes never sync.
	calls = 0
	if code := cmdApprove([]string{"hook", "guard", "--kb", "kb-a", "--sync"}); code != 2 || calls != 0 {
		t.Fatalf("no --yes: exit %d, sync calls %d", code, calls)
	}
	if code := cmdApprove([]string{"skill", "plain", "--kb", "kb-a", "--yes", "--sync"}); code != 2 || calls != 0 {
		t.Fatalf("refused: exit %d, sync calls %d", code, calls)
	}
}

// --pending --sync syncs only after an approval was written: nothing pending, a
// declined prompt and an error never sync (docs/sync.md).
func TestApprovePendingSyncRunsOnlyAfterARealApproval(t *testing.T) {
	_, artifacts := approveFixture(t, false)
	calls := 0
	old := syncFn
	syncFn = func([]string) int { calls++; return 7 }
	t.Cleanup(func() { syncFn = old })
	run := func(args ...string) (code int) {
		withStderr(t, func() { withStdout(t, func() { code = cmdApprove(args) }) })
		return code
	}
	// Non-interactive without --yes is an error.
	if code := run("--pending", "--sync"); code != 2 || calls != 0 {
		t.Fatalf("no --yes: exit %d, sync calls %d", code, calls)
	}
	// A declined prompt.
	approveInteractive = func() bool { return true }
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldIn := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = oldIn })
	_, _ = w.WriteString("n\n")
	w.Close()
	if code := run("--pending", "--sync"); code != 0 || calls != 0 {
		t.Fatalf("declined: exit %d, sync calls %d, want 0 and none", code, calls)
	}
	approveInteractive = func() bool { return false }
	// A real approval syncs, once, with the sync's exit code.
	if code := run("--pending", "--yes", "--sync"); code != 7 || calls != 1 {
		t.Fatalf("approved: exit %d, sync calls %d, want 7 and one", code, calls)
	}
	// Nothing pending any more (everything is approved).
	calls = 0
	_ = artifacts
	if code := run("--pending", "--yes", "--sync"); code != 0 || calls != 0 {
		t.Fatalf("nothing pending: exit %d, sync calls %d, want 0 and none", code, calls)
	}
}

func TestCmdApprove_MCPReapprovalShowsTheDiff(t *testing.T) {
	home, _ := approveFixture(t, false)
	args := []string{"mcp", "tools", "--kb", "kb-a", "--yes"}
	set := func(a contract.Artifact) {
		approveManifest = func(*clientconfig.Config) (contract.Manifest, error) {
			return contract.Manifest{Revision: "r", Artifacts: []contract.Artifact{a}}, nil
		}
	}
	run := func() string {
		var code int
		out := withStdout(t, func() { code = cmdApprove(args) })
		if code != 0 {
			t.Fatalf("exit %d: %s", code, out)
		}
		return out
	}
	set(mcpArtifact("h1-0123456789ab", "https://a.example.com/mcp"))
	if out := run(); strings.Contains(out, "since approved") || strings.Contains(out, "previous approval") {
		t.Errorf("a first approval has nothing to compare:\n%s", out)
	}
	if out := run(); !strings.Contains(out, "unchanged since approved on") {
		t.Errorf("same hash:\n%s", out)
	}
	set(mcpArtifact("h2-0123456789ab", "https://b.example.com/mcp"))
	out := run()
	if !strings.Contains(out, `-   "url": "https://a.example.com/mcp"`) || !strings.Contains(out, `+   "url": "https://b.example.com/mcp"`) {
		t.Errorf("diff must show the old and new url:\n%s", out)
	}
	if strings.Contains(out, "${VAR}") || strings.Contains(out, `"type"`) {
		t.Errorf("unchanged lines must not be printed:\n%s", out)
	}
	cfg, _ := clientconfig.Load(home)
	if !strings.Contains(cfg.MCPApprovals["kb-a"]["tools"].Content, "b.example.com") {
		t.Errorf("the approved text is not stored: %+v", cfg.MCPApprovals)
	}

	// An approval made before the content was stored.
	if err := withClientConfig(home, func(c *clientconfig.Config) error {
		return c.ApproveMCP("kb-a", "tools", "h2-0123456789ab", time.Now())
	}); err != nil {
		t.Fatal(err)
	}
	set(mcpArtifact("h3-0123456789ab", "https://c.example.com/mcp"))
	if out := run(); !strings.Contains(out, "previous approval has no stored version") {
		t.Errorf("legacy approval:\n%s", out)
	}
}
