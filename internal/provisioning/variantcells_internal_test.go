package provisioning

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientvariant"
	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// Placeholder descriptors: a fork of a CLI client (mcp through the shell) and a
// fork of an editor extension (skills only at project level, a hook cell).
const testForkCLI = `schema: 1
name: fork-cli
display_name: Fork CLI
base: crush
detect:
  binaries: [fork-cli]
mcp:
  mode: cli
paths:
  global:
    skill:
      dir:
        darwin: "{xdg_config}/fork-cli/skills"
        linux: "{xdg_config}/fork-cli/skills"
        windows: "{local_app_data}/fork-cli/skills"
    agent:
      format: opencode
      dir:
        darwin: "{xdg_config}/fork-cli/agents"
        linux: "{xdg_config}/fork-cli/agents"
        windows: "{local_app_data}/fork-cli/agents"
    instructions:
      file:
        darwin: "{xdg_config}/fork-cli/FORK-CLI.md"
        linux: "{xdg_config}/fork-cli/FORK-CLI.md"
        windows: "{local_app_data}/fork-cli/FORK-CLI.md"
  project:
    skill: {dir: ".fork-cli/skills"}
    instructions: {file: "AGENTS.md"}
capabilities:
  agent.delegate: verified
`

const testForkExt = `schema: 1
name: fork-ext
display_name: Fork Ext
base: cline
detect:
  dirs:
    default: ["{home}/.vscode/extensions/acme.fork-ext-*"]
mcp:
  mode: seed
  seed:
    template_glob:
      default: "{home}/.vscode/extensions/acme.fork-ext-*/mcp-resources/mcp-template.json"
    derived_glob:
      default: "{home}/.config/Code/User/globalStorage/acme.fork-ext/settings/mcp.enc"
paths:
  global:
    instructions:
      file:
        default: "{home}/Documents/Cline/Rules/cartographer.md"
    hook:
      dir:
        default: "{home}/.fork-ext/cartographer-hooks"
      scan: base
  project:
    skill: {dir: ".fork-ext-plugin/skills"}
    instructions: {file: "AGENTS.md"}
capabilities:
  agent.delegate: absent
`

func testVariant(t *testing.T, name, src, goos string, vars map[string]string) clientvariant.Variant {
	t.Helper()
	spec, err := clientvariant.Parse(name, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	home := "/home/user"
	if goos == "windows" {
		home = `C:\Users\user`
	}
	v, reasons := clientvariant.Resolve(spec, clientvariant.Env{GOOS: goos, Home: home, Getenv: func(k string) string { return vars[k] }})
	if goos == "windows" || goos == "linux" {
		for _, r := range reasons {
			t.Logf("resolve: %s", r)
		}
	}
	return v
}

func registerTestVariant(t *testing.T, v clientvariant.Variant) []string {
	t.Helper()
	notes, err := RegisterVariantCells(v)
	if err != nil {
		t.Fatalf("RegisterVariantCells(%s): %v", v.Name, err)
	}
	t.Cleanup(func() { UnregisterVariantCells(v.Name) })
	return notes
}

// A variant's cells answer through destDir and destDirScoped, per OS, and the
// windows block resolves through the Env seam on every OS.
func TestVariantCells_ForkCLIDestinations(t *testing.T) {
	winVars := map[string]string{"LOCALAPPDATA": `C:\Users\user\AppData\Local`}
	for _, c := range []struct {
		goos       string
		vars       map[string]string
		skill, ins string
	}{
		{"linux", nil, ".config/fork-cli/skills/x", ".config/fork-cli/FORK-CLI.md"},
		{"darwin", map[string]string{"XDG_CONFIG_HOME": "/home/user/.xdg"}, ".xdg/fork-cli/skills/x", ".xdg/fork-cli/FORK-CLI.md"},
		{"windows", winVars, "AppData/Local/fork-cli/skills/x", "AppData/Local/fork-cli/FORK-CLI.md"},
	} {
		t.Run(c.goos, func(t *testing.T) {
			v := testVariant(t, "fork-cli", testForkCLI, c.goos, c.vars)
			if c.goos == "darwin" {
				v = testVariant(t, "fork-cli", testForkCLI, "darwin", c.vars)
			}
			registerTestVariant(t, v)
			p := configurator.Provider("fork-cli")
			if got := destDir("skill", "x", p); got != c.skill {
				t.Errorf("skill = %q, want %q", got, c.skill)
			}
			if got := destDir("agent", "x", p); got != strings.Replace(c.skill, "skills/x", "agents/x.md", 1) {
				t.Errorf("agent = %q", got)
			}
			if got := destDir("instructions", "x", p); got != c.ins {
				t.Errorf("instructions = %q, want %q", got, c.ins)
			}
			for _, kind := range []string{"mcp", "hook"} {
				if got := destDir(kind, "x", p); got != "" {
					t.Errorf("%s = %q, want unsupported", kind, got)
				}
			}
			if got := destDirScoped("skill", "x", p, ScopeProject); got != ".fork-cli/skills/x" {
				t.Errorf("project skill = %q", got)
			}
			if got := destDirScoped("instructions", "x", p, ScopeProject); got != "AGENTS.md" {
				t.Errorf("project instructions = %q", got)
			}
			if got := destDirScoped("agent", "x", p, ScopeProject); got != "" {
				t.Errorf("project agent = %q, want unsupported", got)
			}
			if !SupportsProjectScope(p) {
				t.Error("SupportsProjectScope = false")
			}
		})
	}
}

// The hook cell is ignored until the base's registrar is wired: a destination
// without it would copy hook files that nothing runs.
func TestVariantCells_HookCellIsNotRegistered(t *testing.T) {
	notes := registerTestVariant(t, testVariant(t, "fork-ext", testForkExt, "linux", nil))
	if len(notes) != 1 || !strings.Contains(notes[0], "hook cell: applied by a later release") {
		t.Fatalf("notes = %q", notes)
	}
	p := configurator.Provider("fork-ext")
	if got := destDir("hook", "x", p); got != "" {
		t.Errorf("hook destination = %q, want none", got)
	}
	if got := destDirScoped("hook", "x", p, ScopeProject); got != "" {
		t.Errorf("project hook destination = %q", got)
	}
	// An absent kind is unsupported: no global skill, a project one, rules in Documents.
	if got := destDir("skill", "x", p); got != "" {
		t.Errorf("global skill = %q, want none", got)
	}
	if got := destDirScoped("skill", "x", p, ScopeProject); got != ".fork-ext-plugin/skills/x" {
		t.Errorf("project skill = %q", got)
	}
	if got := destDir("instructions", "x", p); got != "Documents/Cline/Rules/cartographer.md" {
		t.Errorf("instructions = %q", got)
	}
}

func TestVariantCells_AgentFormatSuffixAndTranslation(t *testing.T) {
	src := strings.Replace(testForkCLI, "format: opencode", "format: copilot", 1)
	registerTestVariant(t, testVariant(t, "fork-cli", src, "linux", nil))
	p := configurator.Provider("fork-cli")
	if got := destDir("agent", "x", p); !strings.HasSuffix(got, "/x.agent.md") {
		t.Errorf("copilot-format agent = %q", got)
	}
	UnregisterVariantCells("fork-cli")

	registerTestVariant(t, testVariant(t, "fork-cli", testForkCLI, "linux", nil))
	in := "---\nname: a\ndescription: Reads\nproviders:\n  opencode:\n    permission: {edit: deny}\n---\nBody.\n"
	out, err := translateAgentForProvider(p, "a", []byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if s := string(out); !strings.Contains(s, "description: Reads") || !strings.Contains(s, "mode: subagent") || strings.Contains(s, "permission") {
		t.Errorf("opencode-format agent of a crush fork reads no providers.opencode block:\n%s", s)
	}
	UnregisterVariantCells("fork-cli")

	// A fork of OpenCode honours the base's providers block.
	oc := strings.NewReplacer("fork-cli", "fork-oc", "base: crush", "base: opencode").Replace(testForkCLI)
	registerTestVariant(t, testVariant(t, "fork-oc", oc, "linux", nil))
	if out, err := translateAgentForProvider("fork-oc", "a", []byte(in)); err != nil || !strings.Contains(string(out), "permission: {edit: deny}") {
		t.Errorf("providers.<base> block not honoured: %v\n%s", err, out)
	}
	UnregisterVariantCells("fork-oc")
	registerTestVariant(t, testVariant(t, "fork-cli", testForkCLI, "linux", nil))
	UnregisterVariantCells("fork-cli")

	in = "---\nname: a\ndescription: Reads\n---\nBody.\n"
	claude := strings.Replace(testForkCLI, "format: opencode", "format: claude", 1)
	registerTestVariant(t, testVariant(t, "fork-cli", claude, "linux", nil))
	if out, _ := translateAgentForProvider(p, "a", []byte(in)); string(out) != in {
		t.Errorf("claude-format agent is not verbatim:\n%s", out)
	}
}

// Overlap with a built-in or another variant refuses the whole variant; a
// project cell equal to a built-in's (AGENTS.md) is accepted.
func TestVariantCells_OverlapRefusals(t *testing.T) {
	// fork-cli itself declares project instructions AGENTS.md: accepted.
	registerTestVariant(t, testVariant(t, "fork-cli", testForkCLI, "linux", nil))
	UnregisterVariantCells("fork-cli")

	onClaude := strings.NewReplacer(`{xdg_config}/fork-cli/skills`, `{home}/.claude/skills`).Replace(testForkCLI)
	_, err := RegisterVariantCells(testVariant(t, "fork-cli", onClaude, "linux", nil))
	if err == nil || !strings.Contains(err.Error(), ".claude/skills") || !strings.Contains(err.Error(), "claude") {
		t.Fatalf("a skills dir equal to Claude Code's must be refused naming both, got %v", err)
	}
	if got := destDir("skill", "x", "fork-cli"); got != "" {
		t.Errorf("a refused variant left a cell: %q", got)
	}

	under := strings.NewReplacer(`{xdg_config}/fork-cli/skills`, `{home}/.config/crush/skills/mine`).Replace(testForkCLI)
	if _, err := RegisterVariantCells(testVariant(t, "fork-cli", under, "linux", nil)); err == nil {
		t.Error("a root under a built-in root must be refused")
	}

	registerTestVariant(t, testVariant(t, "fork-cli", testForkCLI, "linux", nil))
	other := strings.ReplaceAll(testForkCLI, "fork-cli", "fork-two")
	other = strings.ReplaceAll(other, `{xdg_config}/fork-two/skills`, `{xdg_config}/fork-cli/skills`)
	if _, err := RegisterVariantCells(testVariant(t, "fork-two", other, "linux", nil)); err == nil || !strings.Contains(err.Error(), "variant fork-cli") {
		t.Fatalf("a root shared with another variant must be refused naming it, got %v", err)
	}
	// Registering the same variant again is a replacement, not a collision.
	if _, err := RegisterVariantCells(testVariant(t, "fork-cli", testForkCLI, "linux", nil)); err != nil {
		t.Errorf("re-registering: %v", err)
	}
}

// A variant's roots are prune boundaries (the first two segments of each cell
// directory) and follow the variant out of the registry.
func TestVariantCells_RootsArePruneBoundaries(t *testing.T) {
	registerTestVariant(t, testVariant(t, "fork-cli", testForkCLI, "linux", nil))
	registerTestVariant(t, testVariant(t, "fork-ext", testForkExt, "linux", nil))
	for _, b := range []string{filepath.Join(".config", "fork-cli"), filepath.Join(".fork-cli", "skills"), filepath.Join(".fork-ext-plugin", "skills"), filepath.Join("Documents", "Cline")} {
		if !isVariantBoundary(b) {
			t.Errorf("%q is not a prune boundary", b)
		}
	}
	for scope, cells := range variantCells["fork-cli"].cells {
		for kind, c := range cells {
			dir := c.dir
			if !c.named {
				dir = dir[:len(dir)-1]
			}
			if len(dir) == 0 {
				continue
			}
			if !isVariantBoundary(filepath.Join(dir[:min(len(dir), 2)]...)) {
				t.Errorf("scope %d %s: root of %v is not a boundary", scope, kind, c.dir)
			}
		}
	}
	UnregisterVariantCells("fork-cli")
	if isVariantBoundary(filepath.Join(".fork-cli", "skills")) {
		t.Error("an unregistered variant's boundary survived")
	}
}

// Skill, agent and instructions block reach a temp home through Apply and are
// pruned when the KB drops them, the variant's root directory staying.
func TestVariantCells_ApplyAndPrune(t *testing.T) {
	registerTestVariant(t, testVariant(t, "fork-cli", testForkCLI, "linux", nil))
	if err := configurator.ReplaceVariants([]configurator.VariantSpec{{Name: "fork-cli", Base: "crush", MCPMode: "cli"}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = configurator.ReplaceVariants(nil) })

	kb := t.TempDir()
	write := func(rel, s string) {
		t.Helper()
		p := filepath.Join(kb, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("skills/s1/SKILL.md", "---\nname: s1\ndescription: A skill\n---\nBody.\n")
	write("agents/a1.md", "---\nname: a1\ndescription: An agent\n---\nBody.\n")
	write("instructions.md", "Be kind.\n")
	roots := map[string]string{"kb-a": kb}
	m, err := BuildManifest(nil, roots, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	p := configurator.Provider("fork-cli")
	res, err := Apply(FilterForProvider(m, p), ApplyOptions{KBRoots: roots, Provider: p, BaseDir: home, ApprovedMCP: approveAll(m)})
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{".config/fork-cli/skills/s1/SKILL.md", ".config/fork-cli/agents/a1.md", ".config/fork-cli/FORK-CLI.md"} {
		if _, err := os.Stat(filepath.Join(home, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s not written: %v (unsupported %+v)", rel, err, res.Unsupported)
		}
	}
	agent, _ := os.ReadFile(filepath.Join(home, ".config", "fork-cli", "agents", "a1.md"))
	if !strings.Contains(string(agent), "description: An agent") || !strings.Contains(string(agent), "mode: subagent") {
		t.Errorf("agent file:\n%s", agent)
	}

	// The KB drops everything: all is pruned, the boundary stays.
	empty, err := BuildManifest(nil, map[string]string{"kb-a": t.TempDir()}, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(FilterForProvider(empty, p), ApplyOptions{KBRoots: roots, Provider: p, BaseDir: home, Lock: res.NewLock}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{".config/fork-cli/skills", ".config/fork-cli/agents/a1.md"} {
		if _, err := os.Stat(filepath.Join(home, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Errorf("%s survived the prune: %v", rel, err)
		}
	}
	if fi, err := os.Stat(filepath.Join(home, ".config", "fork-cli")); err != nil || !fi.IsDir() {
		t.Errorf("the variant's root was pruned: %v", err)
	}
}

func TestVariantCells_CapabilitiesFollowCellsAndDeclaration(t *testing.T) {
	registerTestVariant(t, testVariant(t, "fork-cli", testForkCLI, "linux", nil))
	registerTestVariant(t, testVariant(t, "fork-ext", testForkExt, "linux", nil))
	for _, c := range []struct {
		p     configurator.Provider
		scope Scope
		id    Capability
		want  CapStatus
	}{
		{"fork-cli", ScopeGlobal, CapSkill, CapUnverified},
		{"fork-cli", ScopeGlobal, CapAgentDelegate, CapVerified}, // declared
		{"fork-cli", ScopeGlobal, CapAgentInvoke, CapUnverified}, // base row
		{"fork-cli", ScopeProject, CapAgentDelegate, CapAbsent},  // no project agent cell beats the declaration
		{"fork-cli", ScopeGlobal, CapMCP, CapAbsent},             // mode cli: no mcp cell
		{"fork-cli", ScopeGlobal, "hook.SessionStart", CapAbsent},
		{"fork-ext", ScopeGlobal, CapSkill, CapAbsent},
		{"fork-ext", ScopeProject, CapSkill, CapUnverified},
		{"fork-ext", ScopeGlobal, CapAgentDelegate, CapAbsent},
		{"fork-ext", ScopeGlobal, "hook.PreToolUse", CapAbsent},
		{"fork-ext", ScopeGlobal, "no.such", CapAbsent},
	} {
		if got := CapabilityStatus(c.p, c.scope, c.id); got != c.want {
			t.Errorf("CapabilityStatus(%s, %v, %s) = %s, want %s", c.p, c.scope, c.id, got, c.want)
		}
	}
	if AgentMode("fork-ext", ScopeGlobal) != AgentDegraded {
		t.Error("fork-ext has no agent cell and must degrade")
	}
}

// unsupportedKindWarnings must not count a variant artifact: it has no
// destination on any client and is consumed by the client itself.
func TestUnsupportedKindWarningsSkipVariant(t *testing.T) {
	m := Manifest{Artifacts: []Artifact{{Kind: "variant", Name: "fork-cli", Source: "kb:kb-a"}}}
	for _, p := range []configurator.Provider{configurator.ProviderClaudeCode, configurator.ProviderCline} {
		if w := unsupportedKindWarnings(m, p); len(w) != 0 {
			t.Errorf("%s: %v", p, w)
		}
	}
}
