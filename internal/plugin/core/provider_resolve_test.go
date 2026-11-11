package core

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

func parsed(t *testing.T, stem, src string) ProviderSpec {
	t.Helper()
	s, problems := ParseProvider(stem, []byte(src))
	if len(problems) != 0 {
		t.Fatalf("%s: %v", stem, problems)
	}
	return s
}

func fakeEnv(goos string, vars map[string]string) ProviderEnv {
	home := "/home/user"
	switch goos {
	case "darwin":
		home = "/Users/user"
	case "windows":
		home = `C:\Users\user`
	}
	return ProviderEnv{
		GOOS: goos, Home: home,
		Getenv:    func(k string) string { return vars[k] },
		Documents: func(h string) string { return h + "/Documents" },
	}
}

func winVars() map[string]string {
	return map[string]string{"LOCALAPPDATA": `C:\Users\user\AppData\Local`, "APPDATA": `C:\Users\user\AppData\Roaming`}
}

// symlinkOrSkip links link to target; a host that cannot is skipped.
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
}

func TestProviderFixturesResolveOnEveryOS(t *testing.T) {
	for _, tc := range []struct{ name, src string }{{"fork-cli", forkCLI}, {"fork-ext", forkExt}} {
		spec := parsed(t, tc.name, tc.src)
		for _, goos := range []string{"darwin", "linux", "windows"} {
			r, reasons := ResolveProvider(spec, fakeEnv(goos, winVars()))
			if r.Name != tc.name || len(r.DetectDirs) == 0 {
				t.Errorf("%s/%s: %+v", tc.name, goos, r)
			}
			if len(reasons) != 0 {
				t.Errorf("%s/%s: unexpected reasons %v", tc.name, goos, reasons)
			}
			if tc.name != "fork-ext" {
				continue
			}
			wantConfig := []string{".vscode", "extensions", "acme.fork-ext-*", "settings", "mcp.json"}
			if !reflect.DeepEqual(r.MCPConfig, wantConfig) || len(r.MCPStale) != 1 || r.Global.Hook == nil || r.Global.Instructions == nil {
				t.Errorf("fork-ext/%s: %+v", goos, r)
			}
			if got, want := r.Global.Instructions.File, []string{".fork-ext-rules", "FORK-EXT.md"}; !reflect.DeepEqual(got, want) {
				t.Errorf("fork-ext/%s: instructions %v, want %v", goos, got, want)
			}
		}
	}
}

func TestResolveProviderPerOS(t *testing.T) {
	spec := parsed(t, "fork-cli", forkCLI)
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
		r, reasons := ResolveProvider(spec, fakeEnv(c.goos, c.vars))
		if len(reasons) != 0 || r.Global.Skill == nil || !reflect.DeepEqual(r.Global.Skill.Dir, c.want) {
			t.Errorf("%s: got %+v reasons %v, want %v", c.goos, r.Global.Skill, reasons, c.want)
			continue
		}
		if r.Global.Agent.AgentFormat != "opencode" {
			t.Errorf("%s: agent/skill cell: %+v %+v", c.goos, r.Global.Agent, r.Global.Skill)
		}
		if !reflect.DeepEqual(r.Project.Skill.Dir, []string{".fork-cli", "skills"}) {
			t.Errorf("%s: project skill %v", c.goos, r.Project.Skill.Dir)
		}
	}
}

func TestResolveProviderDropsPathsOutsideHome(t *testing.T) {
	spec := parsed(t, "fork-cli", forkCLI)
	for _, c := range []struct {
		goos string
		vars map[string]string
		want string
	}{
		{"windows", map[string]string{"LOCALAPPDATA": `D:\Local`}, "outside the home"},
		{"windows", nil, "not set"},
		{"linux", map[string]string{"XDG_CONFIG_HOME": "/mnt/other"}, "outside the home"},
	} {
		r, reasons := ResolveProvider(spec, fakeEnv(c.goos, c.vars))
		if r.Global.Skill != nil || r.Global.Instructions != nil {
			t.Errorf("%s: cells must be absent: %+v", c.goos, r.Global)
		}
		if !strings.Contains(strings.Join(reasons, "|"), c.want) {
			t.Errorf("%s: reasons %v want %q", c.goos, reasons, c.want)
		}
		if r.Project.Skill == nil {
			t.Errorf("%s: the project cell must survive", c.goos)
		}
	}
	// {app_data} exists on windows only.
	odd := parsed(t, "fork-ext", swapText(t, forkExt, `default: "{home}/.vscode/extensions/acme.fork-ext-*/settings/mcp.json"`, `default: "{app_data}/x/mcp.json"`))
	if r, reasons := ResolveProvider(odd, fakeEnv("linux", nil)); r.MCPConfig != nil || len(reasons) != 1 || !strings.Contains(reasons[0], "windows only") {
		t.Errorf("{app_data} on linux: %v %v", r.MCPConfig, reasons)
	}
}

// The Documents folder may lie outside home (a redirected folder): a detect dir
// under it is kept only when it lies below home. No written cell is under it.
func TestResolveProviderDocumentsOutsideHome(t *testing.T) {
	home, docs := t.TempDir(), t.TempDir()
	// The real OS: the temp dirs are this machine's paths (backslashes on Windows).
	env := ProviderEnv{GOOS: runtime.GOOS, Home: home, Getenv: func(string) string { return "" }, Documents: func(string) string { return docs }}
	odd := parsed(t, "fork-ext", swapText(t, forkExt, `["{home}/.vscode/extensions/acme.fork-ext-*"]`, `["{documents}/x"]`))
	r, reasons := ResolveProvider(odd, env)
	if len(r.DetectDirs) != 0 || !strings.Contains(strings.Join(reasons, "|"), "outside the home") {
		t.Errorf("detect under a Documents outside home: %v %v", r.DetectDirs, reasons)
	}
	env.Documents = func(h string) string { return filepath.Join(h, "Documents") }
	if r, reasons = ResolveProvider(odd, env); !reflect.DeepEqual(r.DetectDirs, [][]string{{"Documents", "x"}}) || len(reasons) != 0 {
		t.Errorf("detect under a Documents below home: %v %v", r.DetectDirs, reasons)
	}
}

// A path with a `*` is checked for links up to the segment before the first one:
// an MCP file behind a linked folder is dropped like any other.
func TestResolveProviderChecksLinksBeforeAGlob(t *testing.T) {
	spec := parsed(t, "fork-ext", forkExt)
	home, elsewhere := t.TempDir(), t.TempDir()
	symlinkOrSkip(t, elsewhere, filepath.Join(home, ".vscode"))
	env := ProviderEnv{GOOS: "linux", Home: home, Getenv: func(string) string { return "" }}
	r, reasons := ResolveProvider(spec, env)
	if r.MCPConfig != nil || len(r.MCPStale) != 0 || !strings.Contains(strings.Join(reasons, "|"), "symlink") {
		t.Errorf("mcp behind a linked folder: %v %v %v", r.MCPConfig, r.MCPStale, reasons)
	}
	if len(r.DetectDirs) == 0 {
		t.Error("detection only reads: a link must not drop it")
	}
}

// A home-anchored cell behind a link is dropped, as the hook writers refuse it.
func TestResolveProviderHomeLinks(t *testing.T) {
	spec := parsed(t, "fork-ext", forkExt)
	home, elsewhere := t.TempDir(), t.TempDir()
	symlinkOrSkip(t, elsewhere, filepath.Join(home, ".fork-ext"))
	env := ProviderEnv{GOOS: "linux", Home: home, Getenv: func(string) string { return "" }}
	r, reasons := ResolveProvider(spec, env)
	if r.Global.Hook != nil || !strings.Contains(strings.Join(reasons, "|"), "symlink") {
		t.Errorf("hook behind a link: %+v %v", r.Global.Hook, reasons)
	}
	if r.Global.Instructions == nil {
		t.Error("the other cells must survive")
	}
}

// A home that holds pattern characters is a literal path.
func TestResolveProviderHomeWithPatternCharacters(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home[1]")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := parsed(t, "fork-ext", forkExt)
	env := ProviderEnv{GOOS: "linux", Home: home, Getenv: func(string) string { return "" }}
	r, reasons := ResolveProvider(spec, env)
	if len(reasons) != 0 || r.Global.Hook == nil || !reflect.DeepEqual(r.MCPConfig, []string{".vscode", "extensions", "acme.fork-ext-*", "settings", "mcp.json"}) {
		t.Fatalf("resolved %+v with %v", r, reasons)
	}
	for _, p := range r.Paths(home) {
		if strings.HasPrefix(p, "global hook: ") && !strings.HasPrefix(p, "global hook: "+filepath.ToSlash(home)+"/") {
			t.Errorf("home mangled: %s", p)
		}
	}
}

func TestResolvedProviderPaths(t *testing.T) {
	r, _ := ResolveProvider(parsed(t, "fork-ext", forkExt), fakeEnv("linux", nil))
	want := []string{
		"global hook: /home/user/.fork-ext/cartographer-hooks",
		"global instructions: /home/user/.fork-ext-rules/FORK-EXT.md",
		"global mcp config: /home/user/.vscode/extensions/acme.fork-ext-*/settings/mcp.json",
		"global mcp stale: /home/user/.vscode/extensions/acme.fork-ext-*/settings/mcp-cache.json",
		"project instructions: <project>/AGENTS.md",
		"project skill: <project>/.fork-ext/skills",
	}
	if got := r.Paths("/home/user"); !reflect.DeepEqual(got, want) {
		t.Errorf("Paths =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A resolved provider registers beside the built-ins: the registry sees the
// declared mode, file and detection, and every built-in list is unchanged.
func TestResolvedProviderRegisters(t *testing.T) {
	t.Cleanup(func() { _, _ = configurator.ReplacePlugins(nil) })
	builtins := len(configurator.ProviderList())
	ext, _ := ResolveProvider(parsed(t, "fork-ext", forkExt), fakeEnv("linux", nil))
	cli, _ := ResolveProvider(parsed(t, "fork-cli", forkCLI), fakeEnv("linux", nil))
	if _, err := configurator.ReplacePlugins([]configurator.PluginSpec{ext.Registration("team", true), cli.Registration("team", false)}); err != nil {
		t.Fatal(err)
	}
	d, ok := configurator.Lookup("fork-ext")
	if !ok || d.MCPConfigPath != "" || d.MCPConfigGlob != ".vscode/extensions/acme.fork-ext-*/settings/mcp.json" || len(d.ConfigDirGlobs) != 1 || d.MCPMode != "" {
		t.Fatalf("fork-ext = %+v, %v", d, ok)
	}
	if !configurator.UsesCLI("fork-cli") || configurator.UsesCLI("fork-ext") {
		t.Error("only fork-cli reaches Cartographer through the shell")
	}
	if s, _ := configurator.PluginOf("fork-cli"); s.Origin != "team" || s.Active {
		t.Errorf("spec = %+v", s)
	}
	if len(configurator.ProviderList()) != builtins {
		t.Error("a registration entered a built-in list")
	}
}

// An MCP path the cell rules refuse takes the provider's whole MCP side out with
// a reason, never only that path: a provider left without mcp.config would write
// the base's file.
func TestResolveProviderDropsUnsafeMCPPaths(t *testing.T) {
	cfg := `default: "{home}/.vscode/extensions/acme.fork-ext-*/settings/mcp.json"`
	stale := `default: ["{home}/.vscode/extensions/acme.fork-ext-*/settings/mcp-cache.json"]`
	for name, c := range map[string]struct{ cfg, stale, want string }{
		"claude settings as stale": {cfg, `default: ["{home}/.claude.json"]`, "no path below its client directory"},
		"credentials as stale":     {cfg, `default: ["{home}/.docker/config.json"]`, "no plugin provider may write"},
		"glob in a denied dir":     {`default: "{home}/.aws/ext-*/mcp.json"`, stale, "no plugin provider may write"},
		"stale outside the config": {cfg, `default: ["{home}/.config/gcloud/credentials.json"]`, "lies neither below"},
	} {
		spec := parsed(t, "fork-ext", swapText(t, swapText(t, forkExt, cfg, c.cfg), stale, c.stale))
		r, reasons := ResolveProvider(spec, fakeEnv("linux", nil))
		if r.MCPConfig != nil || r.MCPStale != nil || r.MCPMode != configurator.MCPModeNone || !strings.Contains(strings.Join(reasons, "|"), c.want) {
			t.Errorf("%s: config %v stale %v mode %q reasons %v", name, r.MCPConfig, r.MCPStale, r.MCPMode, reasons)
		}
		if r.Registration("team", true).MCPMode != configurator.MCPModeNone {
			t.Errorf("%s: the registration still writes MCP", name)
		}
	}
}

// An mcp.config the resolver drops (here: outside home) takes the MCP side out
// with the mode: the registration must not fall back to the base's file.
func TestResolveProviderDroppedMCPConfigWritesNothing(t *testing.T) {
	spec := parsed(t, "fork-ext", swapText(t, swapText(t, forkExt, `default: "{home}/.vscode/extensions/acme.fork-ext-*/settings/mcp.json"`, `default: "{xdg_config}/acme/mcp.json"`), `  stale:
    default: ["{home}/.vscode/extensions/acme.fork-ext-*/settings/mcp-cache.json"]
`, ""))
	r, reasons := ResolveProvider(spec, fakeEnv("linux", map[string]string{"XDG_CONFIG_HOME": "/mnt/other"}))
	if r.MCPConfig != nil || r.MCPMode != configurator.MCPModeNone || !strings.Contains(strings.Join(reasons, "|"), "outside the home") {
		t.Errorf("config %v mode %q reasons %v", r.MCPConfig, r.MCPMode, reasons)
	}
	if r.Registration("team", true).MCPMode != configurator.MCPModeNone {
		t.Error("the registration still writes MCP")
	}
}

// A custom XDG_CONFIG_HOME under home does not take a path out of the .config
// denylist: the part below the root is checked as if below .config.
func TestResolveProviderCustomXDGKeepsDenylist(t *testing.T) {
	env := fakeEnv("linux", map[string]string{"XDG_CONFIG_HOME": "/home/user/cfg"})
	for _, dir := range []string{"git/skills", "systemd/user", "autostart/x", "fish/conf.d", "environment.d/x", "crush/skills"} {
		spec := parsed(t, "fork-cli", swapText(t, forkCLI, `linux: "{xdg_config}/fork-cli/skills"`, `linux: "{xdg_config}/`+dir+`"`))
		r, reasons := ResolveProvider(spec, env)
		// crush/skills is the client directory of a built-in provider.
		if r.Global.Skill != nil {
			t.Errorf("%s: the cell must be dropped: %+v", dir, r.Global.Skill)
		}
		if !strings.Contains(strings.Join(reasons, "|"), "below {xdg_config}") {
			t.Errorf("%s: reasons %v", dir, reasons)
		}
	}
	// An MCP file below the same root.
	spec := parsed(t, "fork-ext", swapText(t, swapText(t, forkExt, `default: "{home}/.vscode/extensions/acme.fork-ext-*/settings/mcp.json"`, `default: "{xdg_config}/git/mcp.json"`), `  stale:
    default: ["{home}/.vscode/extensions/acme.fork-ext-*/settings/mcp-cache.json"]
`, ""))
	if r, _ := ResolveProvider(spec, env); r.MCPConfig != nil || r.MCPMode != configurator.MCPModeNone {
		t.Errorf("mcp.config below the root: %v %q", r.MCPConfig, r.MCPMode)
	}
	// A permitted directory still resolves, to the custom root.
	if r, reasons := ResolveProvider(parsed(t, "fork-cli", forkCLI), env); r.Global.Skill == nil || strings.Join(r.Global.Skill.Dir, "/") != "cfg/fork-cli/skills" {
		t.Errorf("fork-cli: %+v %v", r.Global.Skill, reasons)
	}
}

// instrFile is the global instructions block of forkCLI, one value for every OS.
const instrFile = "      file:\n        darwin: \"{xdg_config}/fork-cli/FORK-CLI.md\"\n        linux: \"{xdg_config}/fork-cli/FORK-CLI.md\"\n        windows: \"{local_app_data}/fork-cli/FORK-CLI.md\"\n"

// A global instructions file under {documents} resolves below home; a Documents
// folder outside home drops the cell with a reason; a redirected one obeys the
// denials of Documents/<x>. The paths are strings of each OS (fakeEnv), so the
// test holds on every host: a Windows home is `C:\Users\user` and its separators
// are converted only for GOOS windows.
func TestResolveProviderDocumentsInstructions(t *testing.T) {
	vars := map[string]string{"LOCALAPPDATA": `C:\Users\user\AppData\Local`}
	for _, goos := range []string{"linux", "darwin", "windows"} {
		env := fakeEnv(goos, vars)
		spec := parsed(t, "fork-cli", swapText(t, forkCLI, instrFile, "      file: \"{documents}/VendorExt/Rules/cartographer.md\"\n"))
		r, reasons := ResolveProvider(spec, env)
		if r.Global.Instructions == nil || !reflect.DeepEqual(r.Global.Instructions.File, []string{"Documents", "VendorExt", "Rules", "cartographer.md"}) {
			t.Fatalf("%s: instructions = %+v, reasons %v", goos, r.Global.Instructions, reasons)
		}
		if r.Global.Skill == nil {
			t.Errorf("%s: the other cells must survive", goos)
		}

		out := fakeEnv(goos, vars)
		out.Documents = func(string) string {
			return map[string]string{"windows": `D:\Docs`, "darwin": "/Volumes/Docs", "linux": "/mnt/docs"}[goos]
		}
		r, reasons = ResolveProvider(spec, out)
		if r.Global.Instructions != nil || !strings.Contains(strings.Join(reasons, "|"), "{documents} resolves outside the home directory") {
			t.Errorf("%s outside home: %+v %v", goos, r.Global.Instructions, reasons)
		}

		// A redirected folder below home (OneDrive), spelled with the OS's separators.
		red := fakeEnv(goos, vars)
		red.Documents = func(h string) string {
			return h + map[bool]string{true: `\OneDrive\Documents`, false: "/OneDrive/Documents"}[goos == "windows"]
		}
		r, reasons = ResolveProvider(spec, red)
		if r.Global.Instructions == nil || !reflect.DeepEqual(r.Global.Instructions.File, []string{"OneDrive", "Documents", "VendorExt", "Rules", "cartographer.md"}) {
			t.Fatalf("%s redirected: %+v %v", goos, r.Global.Instructions, reasons)
		}
		denied := parsed(t, "fork-cli", swapText(t, forkCLI, instrFile, "      file: \"{documents}/PowerShell/x.md\"\n"))
		r, reasons = ResolveProvider(denied, red)
		if r.Global.Instructions != nil || !strings.Contains(strings.Join(reasons, "|"), "below {documents}") {
			t.Errorf("%s redirected PowerShell: %+v %v", goos, r.Global.Instructions, reasons)
		}
	}
}

// A skill directory that is a dangling link is left out, and the reason reaches
// the caller through LeftOut (#1790): Providers() used to discard it.
func TestResolveProviderKeepsTheReasonOfADroppedCell(t *testing.T) {
	spec := parsed(t, "fork-cli", `schema: 1
name: fork-cli
display_name: Fork CLI
base: crush
detect:
  binaries: [fork-cli]
mcp:
  mode: cli
paths:
  global:
    skill: {dir: "{home}/.fork-cli/skills"}
`)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".fork-cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, filepath.Join(home, "missing"), filepath.Join(home, ".fork-cli", "skills"))
	env := ProviderEnv{GOOS: "linux", Home: home, Getenv: func(string) string { return "" }}
	r, reasons := ResolveProvider(spec, env)
	if r.Global.Skill != nil || len(r.LeftOut) != 1 || !reflect.DeepEqual(r.LeftOut, reasons) ||
		!strings.Contains(r.LeftOut[0], "paths.global.skill.dir") || !strings.Contains(r.LeftOut[0], "symlink") {
		t.Fatalf("skill=%v leftOut=%v reasons=%v", r.Global.Skill, r.LeftOut, reasons)
	}
	p := Plugin{Contributions: map[string][]Contribution{"providers": {{Provider: &spec}}}}
	if got := p.Providers(env); len(got) != 1 || len(got[0].LeftOut) != 1 {
		t.Errorf("Providers() lost the reasons: %+v", got)
	}
}

// An MCP path under a redirected {documents} obeys the denials of Documents/<x>.
func TestResolveProviderRedirectedDocumentsMCPPathIsChecked(t *testing.T) {
	spec := parsed(t, "fork-ext", forkExt)
	spec.MCP.Config.byOS = map[string]string{"default": "{documents}/PowerShell/x/mcp.json"}
	for _, goos := range []string{"linux", "darwin", "windows"} {
		env := fakeEnv(goos, nil)
		env.Documents = func(h string) string {
			return h + map[bool]string{true: `\OneDrive\Documents`, false: "/OneDrive/Documents"}[goos == "windows"]
		}
		r, reasons := ResolveProvider(spec, env)
		if r.MCPConfig != nil || !strings.Contains(strings.Join(reasons, "|"), "below {documents}") {
			t.Errorf("%s: config = %v, reasons %v", goos, r.MCPConfig, reasons)
		}
	}
}
