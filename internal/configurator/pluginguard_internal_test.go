package configurator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func segs(p string) []string { return strings.Split(p, "/") }

func staleList(ps ...string) [][]string {
	var out [][]string
	for _, p := range ps {
		out = append(out, segs(p))
	}
	return out
}

// Every refusal of the MCP path rules, with the exploit each one closes.
func TestCheckPluginMCPPaths_Refusals(t *testing.T) {
	cfg := ".fork/ext-*/mcp.json"
	for name, c := range map[string]struct {
		config string
		stale  []string
		want   string
	}{
		"stale is the base client's settings file":  {cfg, []string{".claude.json"}, "no path below its client directory"},
		"stale in a credentials directory":          {cfg, []string{".docker/config.json"}, "no plugin provider may write"},
		"stale in an ssh directory":                 {cfg, []string{".ssh/known.json"}, "no plugin provider may write"},
		"stale in a built-in client directory":      {cfg, []string{".codex/config.toml"}, "client directory of codex"},
		"stale in another application's directory":  {cfg, []string{".config/gcloud/credentials.json"}, "does not lie below"},
		"stale beside, not below, the config dir":   {cfg, []string{".other/x.json"}, "does not lie below"},
		"stale is the config":                       {cfg, []string{".fork/ext-*/mcp.json"}, "mcp.config itself"},
		"stale pattern matching the config":         {cfg, []string{".fork/*/mcp.json"}, "mcp.config itself"},
		"stale without a config of its own":         {"", []string{".fork/st/x.json"}, "needs an mcp.config"},
		"stale in a .git directory":                 {".gitfork/ext-*/mcp.json", []string{".gitfork/a/x.json"}, "no plugin provider may write"},
		"stale with two stars":                      {cfg, []string{".fork/*/*.json"}, "more than one"},
		"config with two stars":                     {".fork/*/*/mcp.json", nil, "more than one"},
		"config glob spanning every app":            {".config/*/config.json", nil, "replaces part of the client directory"},
		"config glob spanning the data root":        {".local/share/*/c.json", nil, "replaces part of the client directory"},
		"config glob under a denied directory":      {".aws/*/c.json", nil, "no plugin provider may write"},
		"config glob in a built-in client dir":      {".claude/*/c.json", nil, "client directory of claude"},
		"config glob in a .git directory":           {".gitx/*/c.json", nil, "no plugin provider may write"},
		"config is a built-in MCP file":             {".kiro/settings/mcp.json", nil, "client directory of kiro"},
		"config is a file directly in the home":     {".claude.json", nil, "no path below its client directory"},
		"config literal in a credentials directory": {".docker/config.json", nil, "no plugin provider may write"},
	} {
		var config []string
		if c.config != "" {
			config = segs(c.config)
		}
		err := CheckPluginMCPPaths("fork", config, staleList(c.stale...))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v (want %q)", name, err, c.want)
		}
	}
}

func TestCheckPluginMCPPaths_Accepts(t *testing.T) {
	for _, c := range []struct {
		config string
		stale  []string
	}{
		{"", nil},
		{".fork/ext-*/mcp.json", []string{".fork/st-*/servers.json"}},
		{".vscode/extensions/acme.fork-*/settings/mcp.json", []string{".vscode/extensions/acme.fork-*/settings/cache.json"}},
		{".fork/mcp.json", []string{".fork/derived.json"}},
		{"Library/Application Support/Fork/*/mcp.json", nil},
	} {
		var config []string
		if c.config != "" {
			config = segs(c.config)
		}
		if err := CheckPluginMCPPaths("fork", config, staleList(c.stale...)); err != nil {
			t.Errorf("%v refused: %v", c, err)
		}
	}
}

// The registry is the authority: a spec the rules refuse never registers, so
// Apply and removeStale never see it.
func TestRegisterPlugin_RefusesUnsafeMCPPaths(t *testing.T) {
	registeredPlugins(t)
	for name, s := range map[string]PluginSpec{
		"stale .claude.json": {Name: "fork", Base: string(ProviderClaudeCode), MCPConfig: segs(".fork/ext-*/mcp.json"), MCPStale: staleList(".claude.json")},
		"stale docker":       {Name: "fork", Base: string(ProviderClaudeCode), MCPConfig: segs(".fork/ext-*/mcp.json"), MCPStale: staleList(".docker/config.json")},
		"glob over apps":     {Name: "fork", Base: string(ProviderClaudeCode), MCPConfig: segs(".config/*/config.json")},
	} {
		if err := RegisterPlugin(s); err == nil {
			t.Errorf("%s: registered", name)
		}
		if _, err := ReplacePlugins([]PluginSpec{s}); err == nil {
			t.Errorf("%s: replaced", name)
		}
	}
}

// removeStale checks again before it deletes: a spec that got into the registry
// without the registration check removes nothing.
func TestRemoveStale_RefusesAnUnsafeTemplate(t *testing.T) {
	registeredPlugins(t, globPlugin())
	home := t.TempDir()
	victim := filepath.Join(home, ".claude.json")
	writeAll(t, map[string]string{victim: `{"settings":1}`})
	pluginsMu.Lock()
	plugins[0].spec.MCPStale = staleList(".claude.json")
	pluginsMu.Unlock()
	notes := removeStale("fork-ext", home)
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("the client's settings file was removed: %v", err)
	}
	if !strings.Contains(strings.Join(notes, "\n"), "none removed") {
		t.Errorf("no refusal note: %v", notes)
	}
}

func TestIsBuiltinMCPFile(t *testing.T) {
	for p, want := range map[string]bool{
		".claude.json":                                true,
		".kiro/settings/mcp.json":                     true,
		filepath.FromSlash(".kiro/settings/mcp.json"): true,
		".fork/ext-1/mcp.json":                        false,
	} {
		if IsBuiltinMCPFile(p) != want {
			t.Errorf("IsBuiltinMCPFile(%q) = %v", p, !want)
		}
	}
}

// A pattern writes at most maxVersionMatches files; over the cap it writes none
// and says which pattern matched how many.
func TestApplyGlob_OverTheCapWritesNothing(t *testing.T) {
	registeredPlugins(t, globPlugin())
	home := t.TempDir()
	files := map[string]string{}
	var all []string
	for _, n := range []string{"ext-1", "ext-2", "ext-3", "ext-4"} {
		p := filepath.Join(home, ".fork", n, "mcp.json")
		files[p] = `{}`
		all = append(all, p)
	}
	writeAll(t, files)
	r := emitGlob(t)
	written, err := Apply([]*EmitResult{r}, home, false)
	if err != nil || len(written) != 0 {
		t.Fatalf("Apply = %v, %v", written, err)
	}
	for _, p := range all {
		if got, _ := os.ReadFile(p); string(got) != `{}` {
			t.Errorf("%s was written: %s", p, got)
		}
	}
	w := strings.Join(r.Warnings, "\n")
	if !strings.Contains(w, ".fork/ext-*/mcp.json") || !strings.Contains(w, "matches 4 files") {
		t.Errorf("no cap warning naming the pattern and the count: %v", r.Warnings)
	}
}

// A match that is a built-in provider's MCP file is skipped with a warning.
func TestGlobMatches_SkipsABuiltInMCPFile(t *testing.T) {
	home := t.TempDir()
	builtin := filepath.Join(home, ".kiro", "settings", "mcp.json")
	own := filepath.Join(home, ".kiro", "other", "mcp.json")
	writeAll(t, map[string]string{builtin: `{}`, own: `{}`})
	rels, warns, _ := globMatches(Descriptor{Provider: "fork", MCPConfigGlob: ".kiro/*/mcp.json"}, home)
	if len(rels) != 1 || rels[0] != filepath.Join(".kiro", "other", "mcp.json") {
		t.Errorf("matches %v", rels)
	}
	if !strings.Contains(strings.Join(warns, "\n"), "built-in") {
		t.Errorf("no warning: %v", warns)
	}
}

// A glob-matched TOML file whose bytes did not change is not a write: the stale
// files are removed only after a change, as for a JSON match.
func TestApplyGlob_NoOpTOMLSyncRemovesNothing(t *testing.T) {
	registeredPlugins(t, PluginSpec{
		Name: "fork-toml", Base: string(ProviderCodex), Active: true, MCPMode: "native",
		MCPConfig: segs(".fork/ext-*/config.toml"),
		MCPStale:  staleList(".fork/st-*/derived.toml"),
	})
	home := t.TempDir()
	cfg := filepath.Join(home, ".fork", "ext-1", "config.toml")
	stale := filepath.Join(home, ".fork", "st-1", "derived.toml")
	writeAll(t, map[string]string{cfg: "", stale: "x = 1\n"})
	emit := func() *EmitResult {
		r, err := EmitServer("cartographer", ServerSpec{Type: "http", URL: "https://example.com/mcp"}, "fork-toml")
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if written, err := Apply([]*EmitResult{emit()}, home, false); err != nil || len(written) != 1 {
		t.Fatalf("first Apply = %v, %v", written, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("the stale file stayed after a changing write")
	}
	writeAll(t, map[string]string{stale: "x = 1\n"})
	if written, err := Apply([]*EmitResult{emit()}, home, false); err != nil || len(written) != 0 {
		t.Fatalf("second Apply = %v, %v", written, err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Errorf("a no-op sync removed the stale file: %v", err)
	}
}
