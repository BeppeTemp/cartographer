package provisioning

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// Providers the hold/approve/update contract is checked on: Claude Code writes a
// settings.json registration, OpenCode a generated plugin.
var approvalProviders = []configurator.Provider{configurator.ProviderClaudeCode, configurator.ProviderOpenCode}

func hookArtifact(hash, script string) Artifact {
	return Artifact{
		Kind: "hook", Name: "greet", Source: "kb:kb-a", ContentHash: hash,
		Files: []ArtifactFile{
			{Path: "hook.json", Content: []byte(`{"event":"SessionStart","command":"./run.sh"}` + "\n")},
			{Path: "run.sh", Content: []byte(script), Executable: true},
		},
	}
}

func applyArtifact(t *testing.T, p configurator.Provider, baseDir string, lock Lock, approved map[string]string, a Artifact) AppliedResult {
	t.Helper()
	res, err := Apply(Manifest{Revision: a.ContentHash, Artifacts: []Artifact{a}}, ApplyOptions{
		Provider: p, BaseDir: baseDir, Lock: lock, ApprovedMCP: approved,
	})
	if err != nil {
		t.Fatalf("Apply %s: %v", p, err)
	}
	return res
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// installedAt is where a provider puts an artifact's files.
func installedAt(p configurator.Provider, base string, a Artifact) string {
	return filepath.Join(base, destDirScoped(a.Kind, a.Name, p, ScopeGlobal))
}

func TestApply_UnapprovedKBHookIsPendingAndNotInstalled(t *testing.T) {
	for _, p := range approvalProviders {
		base := t.TempDir()
		a := hookArtifact("h1", "#!/bin/sh\nexit 0\n")
		res := applyArtifact(t, p, base, Lock{}, nil, a)
		if len(res.NeedsApproval) != 1 || res.NeedsApproval[0].Kind != "hook" {
			t.Fatalf("%s: want the hook pending, got %+v", p, res.NeedsApproval)
		}
		if exists(installedAt(p, base, a)) || len(res.NewLock.Managed) != 0 {
			t.Errorf("%s: an unapproved hook was installed", p)
		}
		entries, _ := os.ReadDir(base)
		for _, e := range entries {
			if e.Name() == ".claude" || e.Name() == ".config" {
				t.Errorf("%s: an unapproved hook left %s behind (registration)", p, e.Name())
			}
		}
	}
}

func TestApply_ApprovedHashInstallsHookAndChangeIsPendingAgain(t *testing.T) {
	for _, p := range approvalProviders {
		base := t.TempDir()
		approved := map[string]string{"kb:kb-a\x00hook/greet": "h1"}
		a1 := hookArtifact("h1", "#!/bin/sh\necho one\n")
		res := applyArtifact(t, p, base, Lock{}, approved, a1)
		if len(res.NeedsApproval) != 0 {
			t.Fatalf("%s: approved hook still pending: %+v", p, res.NeedsApproval)
		}
		script := filepath.Join(installedAt(p, base, a1), "run.sh")
		if !exists(script) || len(res.NewLock.Managed) == 0 {
			t.Fatalf("%s: approved hook not installed", p)
		}

		// New content, same approval: pending, and the installed files stay as they were.
		a2 := hookArtifact("h2", "#!/bin/sh\necho two\n")
		res2 := applyArtifact(t, p, base, res.NewLock, approved, a2)
		if len(res2.NeedsApproval) != 1 {
			t.Fatalf("%s: a changed hook must be pending again, got %+v", p, res2.NeedsApproval)
		}
		if data, _ := os.ReadFile(script); string(data) != "#!/bin/sh\necho one\n" {
			t.Errorf("%s: a pending update was written: %q", p, data)
		}
		if len(res2.NewLock.Managed) != len(res.NewLock.Managed) || len(res2.Pruned) != 0 {
			t.Errorf("%s: a pending update changed the lock or pruned files: %+v", p, res2.Pruned)
		}
		// The next run is the same: still pending, still untouched.
		res3 := applyArtifact(t, p, base, res2.NewLock, approved, a2)
		if len(res3.NeedsApproval) != 1 || len(res3.NewLock.Managed) != len(res.NewLock.Managed) {
			t.Errorf("%s: pending state not stable: %+v", p, res3.NeedsApproval)
		}
	}
}

// A hook installed before the approval contract existed: unchanged on the server,
// it is reported pending, and nothing on disk is touched or removed.
func TestApply_PreExistingHookIsReportedPendingAndKept(t *testing.T) {
	for _, p := range approvalProviders {
		base := t.TempDir()
		a := hookArtifact("h1", "#!/bin/sh\nexit 0\n")
		installed := applyArtifact(t, p, base, Lock{}, map[string]string{"kb:kb-a\x00hook/greet": "h1"}, a)
		res := applyArtifact(t, p, base, installed.NewLock, nil, a)
		if len(res.NeedsApproval) != 1 {
			t.Fatalf("%s: want the installed hook reported pending, got %+v", p, res.NeedsApproval)
		}
		if !exists(filepath.Join(installedAt(p, base, a), "run.sh")) || len(res.Pruned) != 0 || len(res.NewLock.Managed) != len(installed.NewLock.Managed) {
			t.Errorf("%s: an installed hook must stay untouched: pruned=%+v", p, res.Pruned)
		}
	}
}

// The server signs whatever a token wrote: a verified signature is origin, never consent.
func TestApply_SignedHookStillNeedsApproval(t *testing.T) {
	base := t.TempDir()
	a := hookArtifact("h1", "#!/bin/sh\nexit 0\n")
	a.Signed = true
	res := applyArtifact(t, configurator.ProviderClaudeCode, base, Lock{}, nil, a)
	if len(res.NeedsApproval) != 1 || exists(installedAt(configurator.ProviderClaudeCode, base, a)) {
		t.Fatalf("a signed hook must wait for approval: %+v", res.NeedsApproval)
	}
}

func TestApply_ApprovalOfAnMCPDescriptorDoesNotApproveAHookOfTheSameName(t *testing.T) {
	base := t.TempDir()
	res := applyArtifact(t, configurator.ProviderClaudeCode, base, Lock{}, map[string]string{"kb:kb-a\x00greet": "h1"}, hookArtifact("h1", "#!/bin/sh\nexit 0\n"))
	if len(res.NeedsApproval) != 1 {
		t.Fatalf("an MCP approval approved a hook: %+v", res.NeedsApproval)
	}
}

// No approval unblocks a kind the provider has no destination for (D50): it is
// Unsupported, never pending. Crush has no agent destination.
func TestApply_UnapprovedArtifactOnAProviderWithoutADestinationIsUnsupportedNotPending(t *testing.T) {
	agent := Artifact{Kind: "agent", Name: "a", Source: "kb:kb-a", ContentHash: "a",
		Files: []ArtifactFile{{Path: "a.md", Content: []byte("---\nname: a\ndescription: d\nhooks:\n  Stop: []\n---\nbody\n")}}}
	res := applyArtifact(t, configurator.ProviderCrush, t.TempDir(), Lock{}, nil, agent)
	if len(res.NeedsApproval) != 0 || len(res.Unsupported) != 1 {
		t.Fatalf("want Unsupported only, got pending=%+v unsupported=%+v", res.NeedsApproval, res.Unsupported)
	}
}

func scriptedSkill(name string, exec bool, frontmatterExtra string) Artifact {
	return Artifact{Kind: "skill", Name: name, Source: "kb:kb-a", ContentHash: "s-" + name, Files: []ArtifactFile{
		{Path: "SKILL.md", Content: []byte("---\nname: " + name + "\ndescription: Use when testing.\n" + frontmatterExtra + "---\nbody\n")},
		{Path: "scripts/run.sh", Content: []byte("#!/bin/sh\n"), Executable: exec},
	}}
}

func TestApply_ExecutableFileInASkillNeedsApprovalPlainSkillDoesNot(t *testing.T) {
	p := configurator.ProviderClaudeCode
	base := t.TempDir()
	plain := scriptedSkill("plain", false, "")
	res := applyArtifact(t, p, base, Lock{}, nil, plain)
	if len(res.NeedsApproval) != 0 || !exists(installedAt(p, base, plain)) {
		t.Fatalf("a skill with no executable file must install: %+v", res.NeedsApproval)
	}
	base = t.TempDir()
	scripted := scriptedSkill("scripted", true, "")
	res = applyArtifact(t, p, base, Lock{}, nil, scripted)
	if len(res.NeedsApproval) != 1 || exists(installedAt(p, base, scripted)) {
		t.Fatalf("a skill shipping an executable file must wait: %+v", res.NeedsApproval)
	}
	res = applyArtifact(t, p, base, Lock{}, map[string]string{"kb:kb-a\x00skill/scripted": "s-scripted"}, scripted)
	if len(res.NeedsApproval) != 0 || !exists(filepath.Join(installedAt(p, base, scripted), "scripts", "run.sh")) {
		t.Fatalf("an approved executable skill must install: %+v", res.NeedsApproval)
	}
}

// A skill or agent whose frontmatter declares hooks or MCP servers has the client
// run commands without any executable file: it waits too.
func TestArtifactRequiresApproval_FrontmatterHooksAndMCPServers(t *testing.T) {
	for name, extra := range map[string]string{
		"hooks":              "hooks:\n  PreToolUse:\n    - command: ./x.sh\n",
		"mcpServers":         "mcpServers:\n  - name: x\n",
		"providers.claude":   "providers:\n  claude:\n    hooks:\n      Stop: []\n",
		"providers.opencode": "providers:\n  opencode:\n    mcp_servers: {}\n",
	} {
		if !ArtifactRequiresApproval(scriptedSkill("s", false, extra), ApplyOptions{}) {
			t.Errorf("skill %s: must require approval", name)
		}
		agent := Artifact{Kind: "agent", Name: "a", Source: "kb:kb-a", ContentHash: "a",
			Files: []ArtifactFile{{Path: "a.md", Content: []byte("---\nname: a\ndescription: d\n" + extra + "---\nbody\n")}}}
		if !ArtifactRequiresApproval(agent, ApplyOptions{}) {
			t.Errorf("agent %s: must require approval", name)
		}
	}
	plainAgent := Artifact{Kind: "agent", Name: "a", Source: "kb:kb-a", ContentHash: "a",
		Files: []ArtifactFile{{Path: "a.md", Content: []byte("---\nname: a\ndescription: Mentions hooks in prose only\ntools: Read\n---\nhooks: not frontmatter\n")}}}
	if ArtifactRequiresApproval(plainAgent, ApplyOptions{}) {
		t.Error("an agent with no hooks/mcp key in its frontmatter must install")
	}

	// And Apply holds it until approved, on a provider that writes agents verbatim.
	p := configurator.ProviderClaudeCode
	base := t.TempDir()
	agent := Artifact{Kind: "agent", Name: "a", Source: "kb:kb-a", ContentHash: "a",
		Files: []ArtifactFile{{Path: "a.md", Content: []byte("---\nname: a\ndescription: d\nhooks:\n  Stop: []\n---\nbody\n")}}}
	res := applyArtifact(t, p, base, Lock{}, nil, agent)
	if len(res.NeedsApproval) != 1 || exists(installedAt(p, base, agent)) {
		t.Fatalf("an agent declaring hooks must wait: %+v", res.NeedsApproval)
	}
	res = applyArtifact(t, p, base, Lock{}, map[string]string{"kb:kb-a\x00agent/a": "a"}, agent)
	if len(res.NeedsApproval) != 0 || !exists(installedAt(p, base, agent)) {
		t.Fatalf("the approved agent must install: %+v", res.NeedsApproval)
	}
}

func TestArtifactRequiresApproval_BundleAndBuiltInNever(t *testing.T) {
	bundled := Artifact{Kind: "hook", Name: "x", Source: "bundle", Files: []ArtifactFile{{Path: "run.sh", Executable: true}}}
	builtIn := Artifact{Kind: "hook", Name: "x", Source: "kb:kb-a", BuiltIn: true}
	if ArtifactRequiresApproval(bundled, ApplyOptions{}) || ArtifactRequiresApproval(builtIn, ApplyOptions{}) {
		t.Error("a bundled or built-in artifact must never wait for approval")
	}
	if !artifactAuthorized(builtIn, ApplyOptions{}) {
		t.Error("a built-in artifact must be authorized")
	}
}

func TestFrontmatterDeclaresCommands_Table(t *testing.T) {
	cases := []struct {
		name, in string
		want     bool
	}{
		{"plain", "---\nname: a\ndescription: d\n---\nbody\n", false},
		{"no frontmatter", "just text\nhooks: x\n", false},
		{"empty frontmatter", "---\n---\nbody\n", false},
		{"hooks top level", "---\nname: a\nhooks:\n  Stop: []\n---\n", true},
		{"mcpServers", "---\nmcpServers:\n  x: {}\n---\n", true},
		{"mcp_servers any case", "---\nMCP_Servers: {}\n---\n", true},
		{"mcp-servers", "---\nmcp-servers: {}\n---\n", true},
		{"nested providers", "---\nproviders:\n  claude:\n    hooks: {}\n---\n", true},
		{"indented dashes inside a block scalar do not close", "---\ndescription: |\n  text\n  ---\nhooks: {}\n---\nbody\n", true},
		{"deeply nested in a list", "---\nx:\n  - y:\n      - hooks: 1\n---\n", true},
		{"flow mapping", "---\n{name: x, hooks: {Stop: []}}\n---\n", true},
		{"flow mapping nested", "---\nproviders: {claude: {hooks: {}}}\n---\n", true},
		{"escaped quoted key", "---\n\"h\\x6fo\\x6bs\": {}\n---\n", true},
		{"single quoted key", "---\n'hooks': {}\n---\n", true},
		{"complex key", "---\n? hooks\n: {}\n---\n", true},
		{"complex key mapping", "---\n? {hooks: 1}\n: 1\n---\n", true},
		{"anchor and alias", "---\nbase: &b\n  hooks: {}\nuse: *b\n---\n", true},
		{"merge key", "---\nbase: &b\n  mcpServers: {}\nagent:\n  <<: *b\n---\n", true},
		{"BOM", "\ufeff---\nhooks: {}\n---\n", true},
		{"trailing whitespace on delimiters", "--- \t\nhooks: {}\n--- \n", true},
		{"dots close", "---\nhooks: {}\n...\nbody\n", true},
		{"CR only line endings", "---\rname: a\rhooks: {}\r---\rbody\r", true},
		{"CRLF", "---\r\nhooks: {}\r\n---\r\n", true},
		{"leading blank lines", "\n\n---\nhooks: {}\n---\n", true},
		{"unterminated", "---\nname: a\n", true},
		{"invalid yaml", "---\nname: [unclosed\n---\n", true},
		{"hook named only in a value", "---\nname: hooks\ndescription: mcpServers\n---\n", false},
		{"similar key", "---\nhookss: 1\nmy_hooks: 2\n---\n", false},
	}
	for _, c := range cases {
		if got := FrontmatterDeclaresCommands([]byte(c.in)); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
