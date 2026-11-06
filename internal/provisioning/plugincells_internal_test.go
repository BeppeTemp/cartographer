package provisioning

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

const testHome = "/home/user"

func cellDir(segs ...string) *PluginCell { return &PluginCell{Dir: segs} }

// forkCLI is a fork of Crush: skill, agent (OpenCode layout) and instructions
// cells in a directory of its own, project skill and AGENTS.md.
func forkCLI() PluginCells {
	return PluginCells{
		Name: "fork-cli", Base: "crush", Active: true,
		Global: PluginScopeCells{
			Skill:        cellDir(".fork-cli", "skills"),
			Agent:        &PluginCell{Dir: []string{".fork-cli", "agents"}, AgentFormat: "opencode"},
			Instructions: &PluginCell{File: []string{".fork-cli", "FORK-CLI.md"}},
		},
		Project: PluginScopeCells{
			Skill:        cellDir(".fork-cli", "skills"),
			Instructions: &PluginCell{File: []string{"AGENTS.md"}},
		},
	}
}

// forkExt is a fork of Cline with a hook cell of its own.
func forkExt() PluginCells {
	return PluginCells{
		Name: "fork-ext", Base: "cline", Active: true,
		Global: PluginScopeCells{
			Skill:        cellDir(".fork-ext-plugin", "skills"),
			Instructions: &PluginCell{File: []string{".fork-ext-plugin", "rules", "cartographer.md"}},
			Hook:         cellDir(".fork-ext", "cartographer-hooks"),
		},
	}
}

const forkHookDir = ".fork-ext/cartographer-hooks"

func registerCells(t *testing.T, c PluginCells) []string {
	t.Helper()
	notes, err := RegisterPluginCells(c, testHome)
	if err != nil {
		t.Fatalf("RegisterPluginCells(%s): %v", c.Name, err)
	}
	t.Cleanup(func() { UnregisterPluginCells(c.Name) })
	return notes
}

func registerPluginProvider(t *testing.T, spec configurator.PluginSpec) {
	t.Helper()
	if _, err := configurator.ReplacePlugins([]configurator.PluginSpec{spec}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = configurator.ReplacePlugins(nil) })
}

func TestPluginCells_Destinations(t *testing.T) {
	registerCells(t, forkCLI())
	p := configurator.Provider("fork-cli")
	for _, c := range []struct {
		scope Scope
		kind  string
		want  string
	}{
		{ScopeGlobal, "skill", ".fork-cli/skills/x"},
		{ScopeGlobal, "agent", ".fork-cli/agents/x.md"},
		{ScopeGlobal, "instructions", ".fork-cli/FORK-CLI.md"},
		{ScopeGlobal, "hook", ""},
		{ScopeGlobal, "mcp", ""},
		{ScopeProject, "skill", ".fork-cli/skills/x"},
		{ScopeProject, "instructions", "AGENTS.md"},
		{ScopeProject, "agent", ""},
	} {
		if got := destDirScoped(c.kind, "x", p, c.scope); got != c.want {
			t.Errorf("scope %d %s = %q, want %q", c.scope, c.kind, got, c.want)
		}
	}
	if !SupportsProjectScope(p) {
		t.Error("a provider with project cells supports the project scope")
	}
	// A built-in is untouched, and an unregistered name has no cell.
	if _, ok := cellFor(ScopeGlobal, "skill", "nobody"); ok {
		t.Error("cellFor invented a cell")
	}
	UnregisterPluginCells("fork-cli")
	if got := destDir("skill", "x", p); got != "" {
		t.Errorf("an unregistered provider kept a cell: %q", got)
	}
}

func TestPluginCells_AgentFormatSuffixAndTranslation(t *testing.T) {
	c := forkCLI()
	c.Global.Agent.AgentFormat = "copilot"
	registerCells(t, c)
	p := configurator.Provider("fork-cli")
	if got := destDir("agent", "x", p); !strings.HasSuffix(got, "/x.agent.md") {
		t.Errorf("copilot-format agent = %q", got)
	}
	UnregisterPluginCells("fork-cli")

	registerCells(t, forkCLI())
	in := "---\nname: a\ndescription: Reads\nproviders:\n  opencode:\n    permission: {edit: deny}\n---\nBody.\n"
	out, err := translateAgentForProvider(p, "a", []byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if s := string(out); !strings.Contains(s, "description: Reads") || !strings.Contains(s, "mode: subagent") || strings.Contains(s, "permission") {
		t.Errorf("an opencode-format agent of a crush fork reads no providers.opencode block:\n%s", s)
	}
	UnregisterPluginCells("fork-cli")

	// A fork of OpenCode honours the base's providers block.
	oc := forkCLI()
	oc.Name, oc.Base = "fork-oc", "opencode"
	registerCells(t, oc)
	if out, err := translateAgentForProvider("fork-oc", "a", []byte(in)); err != nil || !strings.Contains(string(out), "permission: {edit: deny}") {
		t.Errorf("providers.<base> block not honoured: %v\n%s", err, out)
	}
	UnregisterPluginCells("fork-oc")

	// claude format: verbatim.
	cl := forkCLI()
	cl.Global.Agent.AgentFormat = "claude"
	registerCells(t, cl)
	plain := "---\nname: a\ndescription: Reads\n---\nBody.\n"
	if out, _ := translateAgentForProvider(p, "a", []byte(plain)); string(out) != plain {
		t.Errorf("claude-format agent is not verbatim:\n%s", out)
	}
}

func TestPluginCells_OneAgentFormatPerProvider(t *testing.T) {
	c := forkCLI()
	c.Project.Agent = &PluginCell{Dir: []string{".fork-cli", "agents"}, AgentFormat: "copilot"}
	if _, err := RegisterPluginCells(c, testHome); err == nil || !strings.Contains(err.Error(), "agent format") {
		UnregisterPluginCells("fork-cli")
		t.Fatalf("two agent formats were registered: %v", err)
	}
}

// Overlap with a built-in or another registered provider refuses the whole
// provider; a project cell equal to a built-in's (AGENTS.md) is accepted.
func TestPluginCells_OverlapRefusals(t *testing.T) {
	registerCells(t, forkCLI())
	UnregisterPluginCells("fork-cli")

	onClaude := forkCLI()
	onClaude.Global.Skill = cellDir(".claude", "skills")
	_, err := RegisterPluginCells(onClaude, testHome)
	if err == nil || !strings.Contains(err.Error(), ".claude/skills") || !strings.Contains(err.Error(), "claude") {
		t.Fatalf("a skills dir equal to Claude Code's must be refused naming both, got %v", err)
	}
	if got := destDir("skill", "x", "fork-cli"); got != "" {
		t.Errorf("a refused provider left a cell: %q", got)
	}

	under := forkCLI()
	under.Global.Skill = cellDir(".config", "crush", "skills", "mine")
	if _, err := RegisterPluginCells(under, testHome); err == nil {
		UnregisterPluginCells("fork-cli")
		t.Error("a root under a built-in root must be refused")
	}

	registerCells(t, forkCLI())
	other := forkCLI()
	other.Name = "fork-two"
	if _, err := RegisterPluginCells(other, testHome); err == nil || !strings.Contains(err.Error(), "plugin provider fork-cli") {
		UnregisterPluginCells("fork-two")
		t.Fatalf("a root shared with another provider must be refused naming it, got %v", err)
	}
	// Registering the same provider again is a replacement, not a collision.
	if _, err := RegisterPluginCells(forkCLI(), testHome); err != nil {
		t.Errorf("re-registering: %v", err)
	}
	// The name of a built-in is never a plugin provider.
	b := forkCLI()
	b.Name = "claude"
	if _, err := RegisterPluginCells(b, testHome); err == nil {
		t.Error("a built-in's name was registered")
	}
}

// ReplacePluginCells refuses both claimants of a root, whatever their order.
func TestReplacePluginCells_RefusesBothClaimants(t *testing.T) {
	a, b := forkCLI(), forkCLI()
	b.Name = "fork-two"
	for _, order := range [][]PluginCells{{a, b, forkExt()}, {b, a, forkExt()}} {
		_, refused := ReplacePluginCells(order, testHome)
		if len(refused) != 2 || refused[0].Name != "fork-cli" || refused[1].Name != "fork-two" {
			t.Fatalf("refused = %+v", refused)
		}
		if _, ok := pluginFor("fork-ext"); !ok {
			t.Fatal("the unrelated provider was dropped")
		}
		if _, ok := pluginFor("fork-cli"); ok {
			t.Fatal("a refused claimant stayed registered")
		}
	}
	ReplacePluginCells(nil, testHome)
	if _, ok := pluginFor("fork-ext"); ok {
		t.Error("an empty replacement left a provider")
	}
}

func TestPluginCells_GlobalCellsStayInTheirOwnClientDirectory(t *testing.T) {
	for name, segs := range map[string][]string{
		"in the home":          {"fork.md"},
		"in .config":           {".config", "fork.md"},
		"ssh":                  {".ssh", "fork"},
		"git config":           {".config", "git", "x"},
		"launch agents":        {"Library", "LaunchAgents", "x"},
		"local bin":            {".local", "bin", "x"},
		"cartographer's own":   {"cartographer-x", "skills"},
		"a built-in directory": {".claude", "x"},
	} {
		c := forkCLI()
		c.Global.Skill = cellDir(segs...)
		if _, err := RegisterPluginCells(c, testHome); err == nil {
			UnregisterPluginCells("fork-cli")
			t.Errorf("%s: %v was registered", name, segs)
		}
	}
}

func TestPluginCells_ProjectCellsAvoidGitAndCI(t *testing.T) {
	for _, segs := range [][]string{{".git", "hooks"}, {".github", "workflows"}, {".husky", "x"}, {"a", ".git", "x"}, {".envrc"}} {
		c := forkCLI()
		c.Project.Skill = cellDir(segs...)
		if _, err := RegisterPluginCells(c, testHome); err == nil {
			UnregisterPluginCells("fork-cli")
			t.Errorf("project cell %v was registered", segs)
		}
	}
}

func TestPluginCells_RootsArePruneBoundaries(t *testing.T) {
	registerCells(t, forkCLI())
	registerCells(t, forkExt())
	for _, b := range []string{filepath.Join(".fork-cli", "skills"), filepath.Join(".fork-cli", "agents"), filepath.Join(".fork-ext-plugin", "skills"), filepath.Join(".fork-ext", "cartographer-hooks")} {
		if !isPluginBoundary(b) {
			t.Errorf("%q is not a prune boundary", b)
		}
	}
	UnregisterPluginCells("fork-cli")
	if isPluginBoundary(filepath.Join(".fork-cli", "skills")) {
		t.Error("an unregistered provider's boundary survived")
	}
}

// Skill, agent and instructions block reach a temp home through Apply and are
// pruned when the KB drops them, the provider's root directory staying.
func TestPluginCells_ApplyAndPrune(t *testing.T) {
	registerCells(t, forkCLI())
	registerPluginProvider(t, configurator.PluginSpec{Name: "fork-cli", Base: "crush", MCPMode: "cli", Active: true})

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
	for _, rel := range []string{".fork-cli/skills/s1/SKILL.md", ".fork-cli/agents/a1.md", ".fork-cli/FORK-CLI.md"} {
		if _, err := os.Stat(filepath.Join(home, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s not written: %v (unsupported %+v)", rel, err, res.Unsupported)
		}
	}
	// The cli sentence names the provider in the block it is told to read.
	if b, _ := os.ReadFile(filepath.Join(home, ".fork-cli", "FORK-CLI.md")); !strings.Contains(string(b), "cartographer call --provider fork-cli") {
		t.Errorf("a cli provider's block lacks its provider name:\n%s", b)
	}

	empty, err := BuildManifest(nil, map[string]string{"kb-a": t.TempDir()}, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(FilterForProvider(empty, p), ApplyOptions{KBRoots: roots, Provider: p, BaseDir: home, Lock: res.NewLock}); err != nil {
		t.Fatal(err)
	}
	// The skills folder is itself a prune boundary: it stays, empty.
	for _, rel := range []string{".fork-cli/skills/s1", ".fork-cli/agents/a1.md"} {
		if _, err := os.Stat(filepath.Join(home, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Errorf("%s survived the prune: %v", rel, err)
		}
	}
	if fi, err := os.Stat(filepath.Join(home, ".fork-cli")); err != nil || !fi.IsDir() {
		t.Errorf("the provider's root was pruned: %v", err)
	}
}

func TestPluginCells_CapabilitiesFollowCellsAndDeclaration(t *testing.T) {
	registerCells(t, forkCLI())
	p := configurator.Provider("fork-cli")
	if got := CapabilityStatus(p, ScopeGlobal, CapSkill); got != CapabilityStatus("crush", ScopeGlobal, CapSkill) {
		t.Errorf("skill of a fork of a probed base = %s, want the base's", got)
	}
	// A different agent layout is not the base's probe.
	if got := CapabilityStatus(p, ScopeGlobal, CapAgentDelegate); got != CapUnverified {
		t.Errorf("agent.delegate with another layout = %s, want unverified", got)
	}
	if got := CapabilityStatus(p, ScopeGlobal, CapMCP); got != CapAbsent {
		t.Errorf("mcp without a cell = %s, want absent", got)
	}
	if got := CapabilityStatus(p, ScopeProject, CapAgentDelegate); got != CapAbsent {
		t.Errorf("agent without a project cell = %s, want absent", got)
	}
	if got := CapabilityStatus(p, ScopeGlobal, Capability("hook.PostToolUse")); got != CapAbsent {
		t.Errorf("hook capability = %s, want absent", got)
	}
	UnregisterPluginCells("fork-cli")

	c := forkCLI()
	c.Capabilities = map[string]string{"skill": "verified", "agent.invoke": "verified"}
	c.InvokeHint = "@name"
	registerCells(t, c)
	if got := CapabilityStatus(p, ScopeGlobal, CapSkill); got != CapVerified {
		t.Errorf("declared skill = %s", got)
	}
	if got := InvokeHint(p, ScopeGlobal); got != "@name" {
		t.Errorf("invoke hint = %q", got)
	}
}

func TestPluginCells_OwnMCPFileIsACell(t *testing.T) {
	c := forkCLI()
	c.MCPFile = []string{".fork-cli", "mcp.json"}
	registerCells(t, c)
	if got := destDir("mcp", "", "fork-cli"); got != ".fork-cli/mcp.json" {
		t.Errorf("mcp cell = %q", got)
	}
}

// The hook cell is built only on a base with the shared dispatcher; the project
// scope and the other kinds are untouched.
func TestPluginHookCellIsBuiltForTheSharedDispatcher(t *testing.T) {
	if notes := registerCells(t, forkExt()); len(notes) != 0 {
		t.Fatalf("notes = %q", notes)
	}
	p := configurator.Provider("fork-ext")
	if got := destDir("hook", "x", p); got != forkHookDir+"/x" {
		t.Errorf("hook destination = %q", got)
	}
	if got := destDirScoped("hook", "x", p, ScopeProject); got != "" {
		t.Errorf("project hook destination = %q", got)
	}
	if _, ok := mechanismFor(p); !ok {
		t.Error("no mechanism for a provider with a hook cell")
	}
	if !SupportsSessionHook(p) {
		t.Error("SupportsSessionHook must answer as the base does")
	}
	if hookProviderFromPath(forkHookDir+"/x/hook.json") != "cline" {
		t.Error("a plugin provider's hook files are removed through the base's registrar")
	}
	if !hookEventReaches("PostToolUse", p) || hookEventReaches("Stop", p) {
		t.Error("event reach must follow the base")
	}
}

func TestPluginHookCellIsLostOnABaseWithoutTheSharedDispatcher(t *testing.T) {
	c := forkExt()
	c.Base = "pi"
	notes := registerCells(t, c)
	if len(notes) != 1 || !strings.Contains(notes[0], "hook cell ignored") {
		t.Fatalf("notes = %q", notes)
	}
	p := configurator.Provider("fork-ext")
	if got := destDir("hook", "x", p); got != "" {
		t.Errorf("hook destination = %q, want none", got)
	}
	if _, ok := mechanismFor(p); ok {
		t.Error("a provider without a hook cell has no hook mechanism")
	}
	if SupportsSessionHook(p) {
		t.Error("SupportsSessionHook = true without a hook cell")
	}
}

func pluginHookArtifact(name, event string) Artifact {
	return builtInHook(name, "h-"+name, map[string]string{
		"hook.json": `{"event":"` + event + `","command":"./extra.sh"}`,
		"extra.sh":  "#!/bin/sh\ncat >/dev/null\necho 'from " + name + "' >&2\nexit 2\n",
	})
}

func applyHooks(t *testing.T, p configurator.Provider, base string, lock Lock, arts ...Artifact) AppliedResult {
	t.Helper()
	res, err := Apply(Manifest{Revision: "r", Artifacts: arts}, ApplyOptions{Provider: p, BaseDir: base, Lock: lock, SkipLockWrite: true})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

const postToolPayload = `{"postToolUse":{"toolName":"x","parameters":{},"result":"ok","success":true}}`

// The dispatcher is shared and never written by a plugin provider: a hook of
// Cline and one of the provider share one dispatcher per event, the provider's
// retirement keeps it while Cline's hook remains, and removes it with the last.
func TestPluginHooksShareTheDispatcherAndRetirementKeepsIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix path")
	}
	registerCells(t, forkExt())
	registerPluginProvider(t, configurator.PluginSpec{Name: "fork-ext", Base: "cline", Active: true})
	base, _ := clineStubBase(t)
	fork := configurator.Provider("fork-ext")

	resF := applyHooks(t, fork, base, Lock{}, pluginHookArtifact("vhook", "PostToolUse"))
	resC := applyHooks(t, configurator.ProviderCline, base, Lock{}, pluginHookArtifact("chook", "PostToolUse"))
	if len(resF.Written) == 0 || len(resC.Written) == 0 {
		t.Fatalf("written: %v / %v", resF.Written, resC.Written)
	}
	entries, err := os.ReadDir(clineHooksDir(base))
	if err != nil || len(entries) != 1 || entries[0].Name() != "PostToolUse" {
		t.Fatalf("one dispatcher expected: %v %v", entries, err)
	}
	if got := DispatchClineHooks(base, "PostToolUse", []byte(postToolPayload)).ContextModification; !strings.Contains(got, "from vhook") || !strings.Contains(got, "from chook") {
		t.Fatalf("context = %q", got)
	}

	applyHooks(t, fork, base, resF.NewLock)
	if _, err := os.Stat(clineDispatcherPath(base, "PostToolUse")); err != nil {
		t.Fatalf("the dispatcher left with the provider's hook: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, filepath.FromSlash(forkHookDir+"/vhook"))); !os.IsNotExist(err) {
		t.Errorf("the provider's hook files stayed: %v", err)
	}
	applyHooks(t, configurator.ProviderCline, base, resC.NewLock)
	if _, err := os.Stat(clineDispatcherPath(base, "PostToolUse")); !os.IsNotExist(err) {
		t.Errorf("the dispatcher outlived its last hook: %v", err)
	}
}

func TestPluginHookLeavesAForeignDispatcherAlone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix path")
	}
	registerCells(t, forkExt())
	registerPluginProvider(t, configurator.PluginSpec{Name: "fork-ext", Base: "cline", Active: true})
	base, _ := clineStubBase(t)
	path := clineDispatcherPath(base, "PostToolUse")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho mine\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := applyHooks(t, "fork-ext", base, Lock{}, pluginHookArtifact("vhook", "PostToolUse"))
	if len(res.Warnings) == 0 {
		t.Error("the unwired hook must be reported")
	}
	if b, _ := os.ReadFile(path); string(b) != "#!/bin/sh\necho mine\n" {
		t.Errorf("the user's file changed: %q", b)
	}
}

// Dispatch enumerates a provider's root only while the provider is registered
// AND its plugin is active: a root that left the registry, or whose plugin was
// retracted, is not read even if its files remain.
func TestDispatchFindsAPluginRootOnlyWhileRegisteredAndActive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix path")
	}
	base := clineBase(t)
	dir := filepath.Join(base, filepath.FromSlash(forkHookDir), "vhook")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hook.json"), []byte(`{"event":"PostToolUse","command":"./run.sh"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\ncat >/dev/null\necho ran >&2\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ran := func() bool {
		return strings.Contains(dispatch(base, "PostToolUse", postToolPayload).ContextModification, "ran")
	}
	if ran() {
		t.Fatal("an unregistered provider's hook ran")
	}
	registerCells(t, forkExt())
	if !ran() {
		t.Fatal("a registered, active provider's hook did not run")
	}
	inactive := forkExt()
	inactive.Active = false
	if _, err := RegisterPluginCells(inactive, testHome); err != nil {
		t.Fatal(err)
	}
	if ran() {
		t.Fatal("the hook of a retracted plugin ran")
	}
	if pluginHookOwner(forkHookDir+"/vhook/hook.json") != "cline" {
		t.Error("an inactive provider's files must still be unwired through the base")
	}
	UnregisterPluginCells("fork-ext")
	if ran() {
		t.Fatal("a retired provider's hook ran")
	}
}

func TestPluginHookDirOverlappingTheBaseIsRefused(t *testing.T) {
	c := forkExt()
	c.Global.Hook = cellDir(".cline", "cartographer-hooks", "sub")
	if _, err := RegisterPluginCells(c, testHome); err == nil {
		UnregisterPluginCells("fork-ext")
		t.Fatal("an overlapping hook directory was registered")
	}
}

func TestPluginHookDirInTheDispatcherFolderIsRefused(t *testing.T) {
	for _, dir := range [][]string{{"Documents", "Cline", "Hooks"}, {"Documents", "Cline", "Hooks", "sub"}, {"Documents", "Cline"}} {
		c := forkExt()
		c.Global.Hook = cellDir(dir...)
		if _, err := RegisterPluginCells(c, testHome); err == nil {
			UnregisterPluginCells("fork-ext")
			t.Errorf("%v: a hook directory in the dispatcher folder was registered", dir)
		}
	}
}

// Any global cell is kept out of the dispatcher folder, not only the hook cell.
func TestPluginCellsAreKeptOutOfTheDispatcherFolder(t *testing.T) {
	for kind, mod := range map[string]func(*PluginCells){
		"instructions": func(c *PluginCells) {
			c.Global.Instructions = &PluginCell{File: []string{"Documents", "Cline", "Hooks", "PreToolUse.ps1"}}
		},
		"skill": func(c *PluginCells) { c.Global.Skill = cellDir("Documents", "Cline", "Hooks", "skills") },
	} {
		c := forkExt()
		mod(&c)
		_, err := RegisterPluginCells(c, testHome)
		if err == nil {
			UnregisterPluginCells("fork-ext")
			t.Errorf("%s: a cell in the dispatcher folder was registered", kind)
		} else if !strings.Contains(err.Error(), "shared hook dispatchers") && !strings.Contains(err.Error(), "Documents/Cline") {
			t.Errorf("%s: refused for another reason: %v", kind, err)
		}
	}
}

// A hook of the same name in Cline's root and a provider's: pruning the
// provider's keeps the dispatcher and Cline's hook keeps running.
func TestPluginHookPruneDoesNotHideAClineHookOfTheSameName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix path")
	}
	registerCells(t, forkExt())
	registerPluginProvider(t, configurator.PluginSpec{Name: "fork-ext", Base: "cline", Active: true})
	base, _ := clineStubBase(t)
	fork := configurator.Provider("fork-ext")
	resF := applyHooks(t, fork, base, Lock{}, pluginHookArtifact("x", "PostToolUse"))
	applyHooks(t, configurator.ProviderCline, base, Lock{}, pluginHookArtifact("x", "PostToolUse"))
	applyHooks(t, fork, base, resF.NewLock)
	if _, err := os.Stat(clineDispatcherPath(base, "PostToolUse")); err != nil {
		t.Fatalf("the dispatcher left with the provider's hook: %v", err)
	}
	if got := DispatchClineHooks(base, "PostToolUse", []byte(postToolPayload)).ContextModification; got != "from x" {
		t.Fatalf("Cline's own x no longer runs: %q", got)
	}
}
