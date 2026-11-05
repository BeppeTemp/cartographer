package clientvariant_test

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientvariant"
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

// forkExt uses base crush here: the Cline provider is not in this tree yet.
const forkExt = `schema: 1
name: fork-ext
display_name: Fork Ext
base: crush
detect:
  dirs:
    default: ["{home}/.vscode/extensions/acme.fork-ext-*"]
mcp:
  mode: seed
  seed:
    template_glob:
      default: "{home}/.vscode/extensions/acme.fork-ext-*/mcp-resources/mcp-template.json"
    derived_glob:
      darwin: "{home}/Library/Application Support/Code/User/globalStorage/acme.fork-ext/settings/mcp.enc"
      linux: "{home}/.config/Code/User/globalStorage/acme.fork-ext/settings/mcp.enc"
      windows: "{app_data}/Code/User/globalStorage/acme.fork-ext/settings/mcp.enc"
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

func envFor(goos string, vars map[string]string) clientvariant.Env {
	home := "/home/user"
	switch goos {
	case "darwin":
		home = "/Users/user"
	case "windows":
		home = `C:\Users\user`
	}
	return clientvariant.Env{GOOS: goos, Home: home, Getenv: func(k string) string { return vars[k] }}
}

func winVars() map[string]string {
	return map[string]string{"LOCALAPPDATA": `C:\Users\user\AppData\Local`, "APPDATA": `C:\Users\user\AppData\Roaming`}
}

func TestSizeCapMirrorsArtifactCap(t *testing.T) {
	if clientvariant.MaxSize != provisioning.ArtifactMaxFileSize {
		t.Fatalf("MaxSize %d != ArtifactMaxFileSize %d", clientvariant.MaxSize, provisioning.ArtifactMaxFileSize)
	}
}

func TestAppendixDescriptorsResolveOnEveryOS(t *testing.T) {
	for _, tc := range []struct{ name, src string }{{"fork-cli", forkCLI}, {"fork-ext", forkExt}} {
		spec, err := clientvariant.Parse(tc.name, []byte(tc.src))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		for _, goos := range []string{"darwin", "linux", "windows"} {
			v, reasons := clientvariant.Resolve(spec, envFor(goos, winVars()))
			if v.Name != tc.name || len(v.DetectDirs) == 0 {
				t.Errorf("%s/%s: %+v", tc.name, goos, v)
			}
			if tc.name == "fork-ext" && (v.HookDir == nil || v.SeedTemplate == nil || v.Global.Instructions == nil) {
				t.Errorf("fork-ext/%s: cells missing: %+v", goos, v)
			}
			// The {app_data} derived glob exists on windows only.
			if tc.name == "fork-ext" && (v.SeedDerived == nil) != false {
				t.Errorf("fork-ext/%s: derived glob lost (reasons %v)", goos, reasons)
			}
			if len(reasons) != 0 {
				t.Errorf("%s/%s: unexpected reasons %v", tc.name, goos, reasons)
			}
		}
	}
}

func TestResolvePerOS(t *testing.T) {
	spec, _ := clientvariant.Parse("fork-cli", []byte(forkCLI))
	cases := []struct {
		goos string
		vars map[string]string
		want []string
	}{
		{"darwin", nil, []string{".config", "fork-cli", "skills"}},
		{"linux", map[string]string{"XDG_CONFIG_HOME": "/home/user/cfg"}, []string{"cfg", "fork-cli", "skills"}},
		{"linux", map[string]string{"XDG_CONFIG_HOME": "/home/user/.cfg"}, []string{".cfg", "fork-cli", "skills"}},
		{"windows", winVars(), []string{"AppData", "Local", "fork-cli", "skills"}},
	}
	for _, c := range cases {
		v, reasons := clientvariant.Resolve(spec, envFor(c.goos, c.vars))
		if len(reasons) != 0 || v.Global.Skill == nil || !reflect.DeepEqual(v.Global.Skill.Dir, c.want) {
			t.Errorf("%s: got %+v reasons %v, want %v", c.goos, v.Global.Skill, reasons, c.want)
			continue
		}
		if v.Global.Agent.AgentFormat != "opencode" {
			t.Errorf("%s: agent/skill cell: %+v %+v", c.goos, v.Global.Agent, v.Global.Skill)
		}
		if !reflect.DeepEqual(v.Project.Skill.Dir, []string{".fork-cli", "skills"}) {
			t.Errorf("%s: project skill %v", c.goos, v.Project.Skill.Dir)
		}
	}
}

func TestResolveDropsPathsOutsideHome(t *testing.T) {
	spec, _ := clientvariant.Parse("fork-cli", []byte(forkCLI))
	for _, c := range []struct {
		goos string
		vars map[string]string
		want string
	}{
		{"windows", map[string]string{"LOCALAPPDATA": `D:\Local`}, "outside the home"},
		{"windows", nil, "not set"},
		{"linux", map[string]string{"XDG_CONFIG_HOME": "/mnt/other"}, "outside the home"},
	} {
		v, reasons := clientvariant.Resolve(spec, envFor(c.goos, c.vars))
		if v.Global.Skill != nil || v.Global.Instructions != nil {
			t.Errorf("%s: cells must be absent: %+v", c.goos, v.Global)
		}
		if len(reasons) == 0 || !strings.Contains(strings.Join(reasons, "|"), c.want) {
			t.Errorf("%s: reasons %v want %q", c.goos, reasons, c.want)
		}
		if v.Project.Skill == nil {
			t.Errorf("%s: the project cell must survive", c.goos)
		}
	}
	// {app_data} makes the block absent off Windows, with a reason.
	ext, _ := clientvariant.Parse("fork-ext", []byte(forkExt))
	if _, reasons := clientvariant.Resolve(ext, envFor("linux", nil)); len(reasons) != 0 {
		t.Errorf("linux uses its own block: %v", reasons)
	}
	src := strings.Replace(forkExt, `linux: "{home}/.config/Code`, `linux: "{app_data}/Code`, 1)
	odd, err := clientvariant.Parse("fork-ext", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if v, reasons := clientvariant.Resolve(odd, envFor("linux", nil)); v.SeedDerived != nil || len(reasons) != 1 || !strings.Contains(reasons[0], "windows only") {
		t.Errorf("{app_data} on linux: %v %v", v.SeedDerived, reasons)
	}
}

func TestParseRejects(t *testing.T) {
	swap := func(src, old, new string) string {
		if !strings.Contains(src, old) {
			t.Fatalf("fixture lacks %q", old)
		}
		return strings.Replace(src, old, new, 1)
	}
	cli := func(old, new string) string { return swap(forkCLI, old, new) }
	ext := func(old, new string) string { return swap(forkExt, old, new) }
	noDetect := swap(swap(forkCLI, "  binaries: [fork-cli]\n", ""),
		"  dirs:\n    default: [\"{home}/.config/fork-cli\"]\n    windows: [\"{home}/.config/fork-cli\", \"{local_app_data}/fork-cli\"]\n", "")
	cases := []struct{ name, src, stem, want string }{
		{"dotdot", cli(`".fork-cli/skills"`, `"../x"`), "fork-cli", "paths.project.skill.dir"},
		{"home dotdot", cli(`default: ["{home}/.config/fork-cli"]`, `default: ["{home}/../x"]`), "fork-cli", "detect.dirs"},
		{"backslash", cli(`default: ["{home}/.config/fork-cli"]`, `default: ["{home}/a\\b"]`), "fork-cli", "not allowed"},
		{"colon", cli(`default: ["{home}/.config/fork-cli"]`, `default: ["{home}/c:d"]`), "fork-cli", "not allowed"},
		{"absolute", cli(`default: ["{home}/.config/fork-cli"]`, `default: ["/etc/x"]`), "fork-cli", "must start with"},
		{"tilde", cli(`default: ["{home}/.config/fork-cli"]`, `default: ["{home}/~x"]`), "fork-cli", "starts with ~"},
		{"anchor in project", cli(`".fork-cli/skills"`, `"{home}/x"`), "fork-cli", "takes no anchor"},
		{"two stars", ext(`acme.fork-ext-*"]`, `*/acme.fork-ext-*"]`), "fork-ext", "not allowed"},
		{"star in last seed segment", ext(`mcp-template.json"`, `mcp-*.json"`), "fork-ext", "not allowed"},
		{"star in plain path", cli(`{xdg_config}/fork-cli/agents"`, `{xdg_config}/fork-*/agents"`), "fork-cli", "not allowed"},
		{"star mid detect", ext(`acme.fork-ext-*"]`, `acme.fork-ext-*/x"]`), "fork-ext", "not allowed"},
		{"unknown key", cli("mcp:\n", "bogus: 1\nmcp:\n"), "fork-cli", "bogus"},
		{"name != stem", forkCLI, "other-cli", "differs from the file name"},
		{"builtin name", cli("name: fork-cli", "name: crush"), "crush", "built-in"},
		{"all", cli("name: fork-cli", "name: all"), "all", "reserved"},
		{"unknown base", cli("base: crush", "base: nothing"), "fork-cli", "base"},
		{"schema", cli("schema: 1", "schema: 2"), "fork-cli", "schema"},
		{"mode exec", cli("mode: cli", "mode: exec"), "fork-cli", "mcp.mode"},
		{"seed without glob", ext("    derived_glob:", "    other_glob:"), "fork-ext", "other_glob"},
		{"seed without derived", ext("    derived_glob:\n      darwin: \"{home}/Library/Application Support/Code/User/globalStorage/acme.fork-ext/settings/mcp.enc\"\n      linux: \"{home}/.config/Code/User/globalStorage/acme.fork-ext/settings/mcp.enc\"\n      windows: \"{app_data}/Code/User/globalStorage/acme.fork-ext/settings/mcp.enc\"\n", ""), "fork-ext", "derived_glob"},
		{"seed in cli", cli("mode: cli", "mode: cli\n  seed:\n    template_glob: \"{home}/a/*/b\"\n    derived_glob: \"{home}/c\""), "fork-cli", "mcp.seed"},
		{"allow_call on a base without syntax", swap(cli("base: crush", "base: opencode"), "mode: cli", "mode: cli\n  allow_call: true"), "fork-cli", "allow_call"},
		{"file mode without file", cli("mode: cli", "mode: file"), "fork-cli", "mcp.file"},
		{"agent format kiro", cli("format: opencode", "format: kiro"), "fork-cli", "agent.format"},
		{"hook with a glob", ext(`.fork-ext/cartographer-hooks"`, `.fork-*/cartographer-hooks"`), "fork-ext", "paths.global.hook.dir"},
		{"hook with a command", ext("      scan: base", "      scan: base\n      command: run.sh"), "fork-ext", "command"},
		{"hook scan other", ext("scan: base", "scan: other"), "fork-ext", "paths.global.hook"},
		{"hook without scan", ext("      scan: base\n", ""), "fork-ext", "paths.global.hook"},
		{"hook in project", ext("    skill: {dir: \".fork-ext-plugin/skills\"}", "    hook: {dir: \".x\", scan: base}"), "fork-ext", "hook"},
		{"unknown OS key", cli("darwin: \"{xdg_config}/fork-cli/skills\"", "plan9: \"{xdg_config}/fork-cli/skills\""), "fork-cli", "unknown OS key"},
		{"capability value", cli("verified", "yes"), "fork-cli", "agent.delegate"},
		{"two documents", forkCLI + "---\nschema: 1\n", "fork-cli", "more than one"},
		{"empty", "", "fork-cli", "empty"},
		{"oversized", strings.Repeat("#", clientvariant.MaxSize+1), "fork-cli", "cap"},
		{"no detection", noDetect, "fork-cli", "detect"},
		{"empty dirs list", swap(noDetect, "detect:\n", "detect:\n  dirs: []\n"), "fork-cli", "detect: needs"},
		{"trailing dot", cli(`".fork-cli/skills"`, `".fork-cli/skills."`), "fork-cli", "ends in a dot"},
		{"dots only", cli(`".fork-cli/skills"`, `"a/..."`), "fork-cli", "ends in a dot"},
		{"trailing space", cli(`".fork-cli/skills"`, `".fork-cli/.. "`), "fork-cli", "ends in a dot or space"},
		{"device name", cli(`".fork-cli/skills"`, `"x/NUL"`), "fork-cli", "Windows device name"},
		{"device name with extension", cli(`".fork-cli/skills"`, `"x/com1.txt"`), "fork-cli", "Windows device name"},
		{"cell without a path", cli("    skill: {dir: \".fork-cli/skills\"}", "    skill: {}"), "fork-cli", "paths.project.skill.dir: required"},
		{"conin", cli(`".fork-cli/skills"`, `"x/CONIN$"`), "fork-cli", "Windows device name"},
		{"conout with extension", cli(`".fork-cli/skills"`, `"x/conout$.txt"`), "fork-cli", "Windows device name"},
		{"superscript com", cli(`".fork-cli/skills"`, `"x/COM¹"`), "fork-cli", "Windows device name"},
		{"superscript lpt", cli(`".fork-cli/skills"`, `"x/lpt³.log"`), "fork-cli", "Windows device name"},
		{"space before extension", cli(`".fork-cli/skills"`, `"x/con .txt"`), "fork-cli", "Windows device name"},
		{"binary dotdot", cli("[fork-cli]", "[..]"), "fork-cli", "bare binary name"},
		{"binary dash", cli("[fork-cli]", "[-x]"), "fork-cli", "bare binary name"},
	}
	for _, c := range cases {
		_, err := clientvariant.Parse(c.stem, []byte(c.src))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want it to contain %q", c.name, err, c.want)
		}
	}
}

// allow_call is refused with its own message, on any base and mode, until the
// permission rule is probed (D1699).
func TestAllowCallIsRefused(t *testing.T) {
	src := strings.Replace(forkCLI, "mode: cli", "mode: cli\n  allow_call: true", 1)
	_, err := clientvariant.Parse("fork-cli", []byte(src))
	if err == nil || !strings.Contains(err.Error(), "mcp.allow_call: not supported") {
		t.Fatalf("err = %v", err)
	}
	src = strings.Replace(src, "    instructions:\n      file:", "    mcp: {file: \"{home}/.config/fork-cli/cfg.json\"}\n    instructions:\n      file:", 1)
	if _, err := clientvariant.Parse("fork-cli", []byte(src)); err == nil || !strings.Contains(err.Error(), "allow_call") {
		t.Fatalf("with a config file: err = %v", err)
	}
}

func TestParseRefusesAliases(t *testing.T) {
	big := strings.Repeat("a", 2000)
	// Many aliases of one anchored scalar in a list: refused before any expansion.
	var b strings.Builder
	b.WriteString("schema: 1\nname: fork-cli\ndisplay_name: X\nbase: crush\ndetect:\n  binaries: [&b \"" + big + "\"")
	b.WriteString(strings.Repeat(", *b", 5000) + "]\n")
	_, err := clientvariant.Parse("fork-cli", []byte(b.String()))
	if err == nil || !strings.Contains(err.Error(), "aliases") {
		t.Fatalf("err = %v, want the alias refusal", err)
	}
}

func TestParseBoundsListsAndMessages(t *testing.T) {
	long := strings.Repeat("a", 5000)
	src := strings.Replace(forkCLI, "binaries: [fork-cli]", "binaries: ["+strings.Repeat("\"/"+long+"\", ", 40)+"x]", 1)
	_, err := clientvariant.Parse("fork-cli", []byte(src))
	if err == nil || !strings.Contains(err.Error(), "more than 16 entries") || len(err.Error()) > 4096 {
		n := 0
		if err != nil {
			n = len(err.Error())
		}
		t.Fatalf("list cap: err of %d bytes: %v", n, err != nil)
	}
	_, err = clientvariant.Parse("fork-cli", []byte(strings.Replace(forkCLI, `".fork-cli/skills"`, `"`+long+`."`, 1)))
	if err == nil || len(err.Error()) > 512 {
		t.Fatalf("an oversized value must give a short error")
	}
}

func TestParseErrorsAreCompleteAndStable(t *testing.T) {
	src := strings.NewReplacer("schema: 1", "schema: 2", "base: crush", "base: nothing", "mode: cli", "mode: exec", "skill: {dir: \".fork-cli/skills\"}", "skill: {}").Replace(forkCLI)
	want := strings.Join([]string{
		"base: \"nothing\" is not a built-in provider",
		"mcp.mode: \"exec\" (want file, cli or seed)",
		"paths.project.skill.dir: required",
		"schema: 2 is not supported (want 1)",
	}, "\n")
	for i := 0; i < 20; i++ {
		_, err := clientvariant.Parse("fork-cli", []byte(src))
		if err == nil || err.Error() != want {
			t.Fatalf("round %d:\n%v\nwant:\n%s", i, err, want)
		}
	}
}

func TestParseBoundsCapabilities(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 20000; i++ { // as many as the size cap allows
		b.WriteString("  k" + strconv.Itoa(i) + ": no\n")
	}
	src := strings.Replace(forkCLI, "capabilities:\n", "capabilities:\n"+b.String(), 1)
	_, err := clientvariant.Parse("fork-cli", []byte(src))
	if err == nil || !strings.Contains(err.Error(), "more than 64 entries") || len(err.Error()) > 1024 {
		t.Fatalf("err = %v", err)
	}
}
