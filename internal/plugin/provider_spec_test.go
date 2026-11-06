package plugin

import (
	"strconv"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

const forkCLI = `schema: 1
name: fork-cli
display_name: Fork CLI
base: crush
detect:
  binaries: [fork-cli]
  dirs:
    default: ["{home}/.config/fork-cli"]
    windows: ["{home}/.config/fork-cli", "{local_app_data}/fork-cli"]
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

const forkExt = `schema: 1
name: fork-ext
display_name: Fork Ext
base: cline
detect:
  dirs:
    default: ["{home}/.vscode/extensions/acme.fork-ext-*"]
mcp:
  mode: native
  config:
    default: "{home}/.vscode/extensions/acme.fork-ext-*/settings/mcp.json"
  stale:
    default: ["{home}/.vscode/extensions/acme.fork-ext-*/settings/mcp-cache.json"]
paths:
  global:
    instructions:
      file:
        default: "{home}/.fork-ext-rules/FORK-EXT.md"
    hook:
      dir:
        default: "{home}/.fork-ext/cartographer-hooks"
      scan: base
  project:
    skill: {dir: ".fork-ext/skills"}
    instructions: {file: "AGENTS.md"}
capabilities:
  agent.delegate: absent
`

func swapText(t *testing.T, src, old, repl string) string {
	t.Helper()
	if !strings.Contains(src, old) {
		t.Fatalf("fixture lacks %q", old)
	}
	return strings.Replace(src, old, repl, 1)
}

func TestProviderFixturesParse(t *testing.T) {
	for _, tc := range []struct{ name, src string }{{"fork-cli", forkCLI}, {"fork-ext", forkExt}} {
		s, problems := ParseProvider(tc.name, []byte(tc.src))
		if len(problems) != 0 {
			t.Fatalf("%s: %v", tc.name, problems)
		}
		if s.Name != tc.name {
			t.Errorf("%s: parsed as %q", tc.name, s.Name)
		}
	}
	// mcp.mode defaults to native, the agent format to claude.
	s, problems := ParseProvider("fork-ext", []byte(swapText(t, forkExt, "  mode: native\n", "")))
	if len(problems) != 0 || s.MCP.Mode != "native" {
		t.Fatalf("default mode: %q %v", s.MCP.Mode, problems)
	}
	s, problems = ParseProvider("fork-cli", []byte(swapText(t, forkCLI, "      format: opencode\n", "")))
	if len(problems) != 0 || s.Paths.Global.Agent.Format != "claude" {
		t.Fatalf("default format: %+v %v", s.Paths.Global.Agent, problems)
	}
}

func TestProviderSpecSizeCapIsTheArtifactCap(t *testing.T) {
	_, problems := ParseProvider("fork-cli", []byte(strings.Repeat("#", provisioning.ArtifactMaxFileSize+1)))
	if len(problems) != 1 || !strings.Contains(problems[0], "cap") {
		t.Fatalf("problems = %v", problems)
	}
}

func TestParseProviderRejects(t *testing.T) {
	cli := func(old, repl string) string { return swapText(t, forkCLI, old, repl) }
	ext := func(old, repl string) string { return swapText(t, forkExt, old, repl) }
	noDetect := swapText(t, swapText(t, forkCLI, "  binaries: [fork-cli]\n", ""),
		"  dirs:\n    default: [\"{home}/.config/fork-cli\"]\n    windows: [\"{home}/.config/fork-cli\", \"{local_app_data}/fork-cli\"]\n", "")
	cases := []struct{ name, src, stem, want string }{
		{"dotdot", cli(`".fork-cli/skills"`, `"../x"`), "fork-cli", "paths.project.skill.dir"},
		{"home dotdot", cli(`default: ["{home}/.config/fork-cli"]`, `default: ["{home}/../x"]`), "fork-cli", "detect.dirs"},
		{"backslash", cli(`default: ["{home}/.config/fork-cli"]`, `default: ["{home}/a\\b"]`), "fork-cli", "not allowed"},
		{"colon", cli(`default: ["{home}/.config/fork-cli"]`, `default: ["{home}/c:d"]`), "fork-cli", "not allowed"},
		{"absolute", cli(`default: ["{home}/.config/fork-cli"]`, `default: ["/etc/x"]`), "fork-cli", "must start with"},
		{"tilde", cli(`default: ["{home}/.config/fork-cli"]`, `default: ["{home}/~x"]`), "fork-cli", "starts with ~"},
		{"unknown anchor", cli(`default: ["{home}/.config/fork-cli"]`, `default: ["{cache}/x"]`), "fork-cli", "must start with"},
		{"anchor in project", cli(`".fork-cli/skills"`, `"{home}/x"`), "fork-cli", "takes no anchor"},
		{"documents in project", cli(`".fork-cli/skills"`, `"{documents}/x"`), "fork-cli", "takes no anchor"},
		{"star in the first segment", ext(`{home}/.vscode/extensions/acme.fork-ext-*"]`, `{home}/.vscode*/extensions"]`), "fork-ext", "detect.dirs"},
		{"two stars", ext(`acme.fork-ext-*"]`, `*/acme.fork-ext-*"]`), "fork-ext", "not allowed"},
		{"star form", ext(`acme.fork-ext-*"]`, `acme.*-ext"]`), "fork-ext", "not allowed"},
		{"star in the file name of mcp.config", ext(`settings/mcp.json"`, `settings/*.json"`), "fork-ext", "mcp.config"},
		{"star in a cell", cli(`{xdg_config}/fork-cli/agents"`, `{xdg_config}/fork-*/agents"`), "fork-cli", "not allowed"},
		{"unknown key", cli("mcp:\n", "bogus: 1\nmcp:\n"), "fork-cli", "bogus"},
		{"seed is not a key", ext("mcp:\n", "mcp:\n  seed: {}\n"), "fork-ext", "seed"},
		{"allow_call is not a key", cli("mode: cli", "mode: cli\n  allow_call: true"), "fork-cli", "allow_call"},
		{"mcp cell is not a key", ext("    instructions:\n      file:\n        default: \"{home}", "    mcp: {file: x}\n    instructions:\n      file:\n        default: \"{home}"), "fork-ext", "mcp"},
		{"documents in a global skill", cli(`darwin: "{xdg_config}/fork-cli/skills"`, `darwin: "{documents}/skills"`), "fork-cli", "paths.global.skill.dir.darwin"},
		{"documents in a global agent", cli(`darwin: "{xdg_config}/fork-cli/agents"`, `darwin: "{documents}/agents"`), "fork-cli", "paths.global.agent.dir.darwin"},
		{"documents in an instructions file", cli(`darwin: "{xdg_config}/fork-cli/FORK-CLI.md"`, `darwin: "{documents}/FORK-CLI.md"`), "fork-cli", "paths.global.instructions.file.darwin"},
		{"documents in a hook dir", ext(`default: "{home}/.fork-ext/cartographer-hooks"`, `default: "{documents}/hooks"`), "fork-ext", "paths.global.hook.dir.default"},
		{"documents in mcp.stale", ext(`default: ["{home}/.vscode/extensions/acme.fork-ext-*/settings/mcp-cache.json"]`, `default: ["{documents}/x/c.json"]`), "fork-ext", "mcp.stale.default[0]"},
		{"stale extension", ext("settings/mcp-cache.json", "settings/mcp-cache.txt"), "fork-ext", "mcp.stale.default[0]"},
		{"instructions without .md", cli("FORK-CLI.md\"\n        linux", ".bashrc\"\n        linux"), "fork-cli", "must be a .md file"},
		{"instructions file is a dotfile", cli(`file: "AGENTS.md"`, `file: ".zshrc"`), "fork-cli", "paths.project.instructions.file.default"},
		{"own state in a skill dir", cli(`".fork-cli/skills"`, `".cartographer/skills"`), "fork-cli", "own state"},
		{"own state, the config file", cli(`file: "AGENTS.md"`, `file: ".cartographer.yaml.md"`), "fork-cli", "own state"},
		{"own state under xdg_config", cli(`darwin: "{xdg_config}/fork-cli/agents"`, `darwin: "{xdg_config}/cartographer/agents"`), "fork-cli", "own state"},
		{"own state through a wildcard in detect", cli(`default: ["{home}/.config/fork-cli"]`, `default: ["{home}/.config/carto*"]`), "fork-cli", "detect.dirs.default[0]"},
		{"own state in mcp.config", ext("{home}/.vscode/extensions/acme.fork-ext-*/settings/mcp.json", "{home}/.config/cartographer/mcp.json"), "fork-ext", "own state"},
		{"own state in mcp.stale", ext(`default: ["{home}/.vscode/extensions/acme.fork-ext-*/settings/mcp-cache.json"]`, `default: ["{local_app_data}/Cartographer/c.json"]`), "fork-ext", "own state"},
		{"own state in a hook dir", ext(`.fork-ext/cartographer-hooks"`, `.cartographer/hooks"`), "fork-ext", "paths.global.hook.dir"},
		{"duplicated OS key", cli(`windows: "{local_app_data}/fork-cli/skills"`, `darwin: "{local_app_data}/fork-cli/skills"`), "fork-cli", "is given twice"},
		{"name != stem", forkCLI, "other-cli", "differs from the file name"},
		{"builtin name", cli("name: fork-cli", "name: crush"), "crush", "built-in"},
		{"all", cli("name: fork-cli", "name: all"), "all", "reserved"},
		{"unknown base", cli("base: crush", "base: nothing"), "fork-cli", "base"},
		{"a provider as base", cli("base: crush", "base: fork-ext"), "fork-cli", "base"},
		{"schema", cli("schema: 1", "schema: 2"), "fork-cli", "schema"},
		{"display name too long", cli("display_name: Fork CLI", "display_name: "+strings.Repeat("x", 41)), "fork-cli", "display_name"},
		{"mode exec", cli("mode: cli", "mode: exec"), "fork-cli", "mcp.mode"},
		{"config with cli", cli("mode: cli", "mode: cli\n  config: {default: \"{home}/.x/mcp.json\"}"), "fork-cli", "mcp.config: not allowed"},
		{"stale with cli", cli("mode: cli", "mode: cli\n  stale: {default: [\"{home}/.x/a\"]}"), "fork-cli", "mcp.stale: not allowed"},
		{"stale with none", cli("mode: cli", "mode: none\n  stale: {default: [\"{home}/.x/a\"]}"), "fork-cli", "mcp.stale: not allowed"},
		{"config extension", ext("settings/mcp.json", "settings/mcp.toml"), "fork-ext", "extension"},
		{"four stale entries", ext(`mcp-cache.json"]`, `mcp-cache.json", "{home}/a", "{home}/b", "{home}/c"]`), "fork-ext", "more than 3 entries"},
		{"documents in mcp.config", ext(`default: "{home}/.vscode/extensions/acme.fork-ext-*/settings/mcp.json"`, `default: "{documents}/x/mcp.json"`), "fork-ext", "mcp.config"},
		{"agent format kiro", cli("format: opencode", "format: kiro"), "fork-cli", "agent.format"},
		{"hook with a glob", ext(`.fork-ext/cartographer-hooks"`, `.fork-*/cartographer-hooks"`), "fork-ext", "paths.global.hook.dir"},
		{"hook with a command", ext("      scan: base", "      scan: base\n      command: run.sh"), "fork-ext", "command"},
		{"hook scan other", ext("scan: base", "scan: other"), "fork-ext", "paths.global.hook.scan"},
		{"hook without scan", ext("      scan: base\n", ""), "fork-ext", "paths.global.hook.scan"},
		{"hook in project", ext("    skill: {dir: \".fork-ext/skills\"}", "    hook: {dir: \".x\", scan: base}"), "fork-ext", "hook"},
		{"unknown OS key", cli("darwin: \"{xdg_config}/fork-cli/skills\"", "plan9: \"{xdg_config}/fork-cli/skills\""), "fork-cli", "unknown OS key"},
		{"capability value", cli("verified", "yes"), "fork-cli", "agent.delegate"},
		{"capability id", cli("agent.delegate", "no.such.id"), "fork-cli", "no.such.id"},
		{"two documents", forkCLI + "---\nschema: 1\n", "fork-cli", "more than one"},
		{"empty", "", "fork-cli", "empty"},
		{"oversized", strings.Repeat("#", provisioning.ArtifactMaxFileSize+1), "fork-cli", "cap"},
		{"no detection", noDetect, "fork-cli", "detect"},
		{"empty dirs list", swapText(t, noDetect, "detect:\n", "detect:\n  dirs: []\n"), "fork-cli", "detect: needs"},
		{"trailing dot", cli(`".fork-cli/skills"`, `".fork-cli/skills."`), "fork-cli", "ends in a dot"},
		{"dots only", cli(`".fork-cli/skills"`, `"a/..."`), "fork-cli", "ends in a dot"},
		{"trailing space", cli(`".fork-cli/skills"`, `".fork-cli/.. "`), "fork-cli", "ends in a dot or space"},
		{"device name", cli(`".fork-cli/skills"`, `"x/NUL"`), "fork-cli", "Windows device name"},
		{"device name with extension", cli(`".fork-cli/skills"`, `"x/com1.txt"`), "fork-cli", "Windows device name"},
		{"conin", cli(`".fork-cli/skills"`, `"x/CONIN$"`), "fork-cli", "Windows device name"},
		{"superscript com", cli(`".fork-cli/skills"`, `"x/COM¹"`), "fork-cli", "Windows device name"},
		{"space before extension", cli(`".fork-cli/skills"`, `"x/con .txt"`), "fork-cli", "Windows device name"},
		{"cell without a path", cli("    skill: {dir: \".fork-cli/skills\"}", "    skill: {}"), "fork-cli", "paths.project.skill.dir: required"},
		{"binary dotdot", cli("[fork-cli]", "[..]"), "fork-cli", "bare binary name"},
		{"binary dash", cli("[fork-cli]", "[-x]"), "fork-cli", "bare binary name"},
		{"binary with a slash", cli("[fork-cli]", "[a/b]"), "fork-cli", "bare binary name"},
	}
	for _, c := range cases {
		_, problems := ParseProvider(c.stem, []byte(c.src))
		if joined := strings.Join(problems, "\n"); len(problems) == 0 || !strings.Contains(joined, c.want) {
			t.Errorf("%s: problems = %v, want one containing %q", c.name, problems, c.want)
		}
	}
}

// {documents} is read by a detect dir and by no cell that is written: the only
// file Cartographer writes there is Cline's own dispatcher.
func TestProviderDocumentsAnchor(t *testing.T) {
	if _, problems := ParseProvider("fork-ext", []byte(swapText(t, forkExt, `["{home}/.vscode/extensions/acme.fork-ext-*"]`, `["{documents}/Cline"]`))); len(problems) != 0 {
		t.Fatalf("a detect dir under {documents}: %v", problems)
	}
	if _, problems := ParseProvider("fork-ext", []byte(swapText(t, forkExt, `skill: {dir: ".fork-ext/skills"}`, `skill: {dir: "{documents}/x"}`))); len(problems) == 0 {
		t.Fatal("a project cell under {documents} was accepted")
	}
}

func TestParseProviderRefusesAliases(t *testing.T) {
	big := strings.Repeat("a", 2000)
	var b strings.Builder
	b.WriteString("schema: 1\nname: fork-cli\ndisplay_name: X\nbase: crush\ndetect:\n  binaries: [&b \"" + big + "\"")
	b.WriteString(strings.Repeat(", *b", 5000) + "]\n")
	_, problems := ParseProvider("fork-cli", []byte(b.String()))
	if len(problems) != 1 || !strings.Contains(problems[0], "aliases") {
		t.Fatalf("problems = %v, want the alias refusal", problems)
	}
}

func TestParseProviderBoundsListsAndMessages(t *testing.T) {
	long := strings.Repeat("a", 5000)
	src := swapText(t, forkCLI, "binaries: [fork-cli]", "binaries: ["+strings.Repeat("\"/"+long+"\", ", 40)+"x]")
	_, problems := ParseProvider("fork-cli", []byte(src))
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, "more than 16 entries") || len(joined) > 4096 {
		t.Fatalf("list cap: %d bytes of problems", len(joined))
	}
	_, problems = ParseProvider("fork-cli", []byte(swapText(t, forkCLI, `".fork-cli/skills"`, `"`+long+`."`)))
	if joined := strings.Join(problems, "\n"); len(problems) == 0 || len(joined) > 512 {
		t.Fatalf("an oversized value must give a short problem, got %d bytes", len(joined))
	}
}

func TestParseProviderProblemsAreCompleteAndStable(t *testing.T) {
	src := strings.NewReplacer("schema: 1", "schema: 2", "base: crush", "base: nothing", "mode: cli", "mode: exec", "skill: {dir: \".fork-cli/skills\"}", "skill: {}").Replace(forkCLI)
	want := []string{
		"base: \"nothing\" is not a built-in provider",
		"mcp.mode: \"exec\" (want native, cli or none)",
		"paths.project.skill.dir: required",
		"schema: 2 is not supported (want 1)",
	}
	for i := 0; i < 20; i++ {
		_, problems := ParseProvider("fork-cli", []byte(src))
		if strings.Join(problems, "\n") != strings.Join(want, "\n") {
			t.Fatalf("round %d:\n%v\nwant:\n%v", i, problems, want)
		}
	}
}

func TestParseProviderBoundsCapabilities(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 10000; i++ { // near the size cap
		b.WriteString("  k" + strconv.Itoa(i) + ": absent\n")
	}
	src := swapText(t, forkCLI, "capabilities:\n", "capabilities:\n"+b.String())
	_, problems := ParseProvider("fork-cli", []byte(src))
	if joined := strings.Join(problems, "\n"); !strings.Contains(joined, "more than 64 entries") || len(joined) > 1024 {
		t.Fatalf("problems = %v", problems)
	}
}

func TestProviderNameClashes(t *testing.T) {
	a, _ := ParseProvider("fork-cli", []byte(forkCLI))
	b, _ := ParseProvider("fork-ext", []byte(forkExt))
	if got := ProviderNameClashes([]ProviderSpec{a, b}); len(got) != 0 {
		t.Fatalf("clashes = %v", got)
	}
	if got := ProviderNameClashes([]ProviderSpec{a, b, a}); len(got) != 1 || !strings.Contains(got[0], "fork-cli") {
		t.Fatalf("clashes = %v", got)
	}
}
