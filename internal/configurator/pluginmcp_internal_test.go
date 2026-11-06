package configurator

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func globPlugin() PluginSpec {
	return PluginSpec{
		Name: "fork-ext", Base: string(ProviderCrush), Active: true, MCPMode: "native",
		MCPConfig: []string{".fork", "ext-*", "mcp.json"},
		MCPStale:  [][]string{{".fork", "st-*", "servers.json"}},
	}
}

func writeAll(t *testing.T, files map[string]string) {
	t.Helper()
	for p, c := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func emitGlob(t *testing.T) *EmitResult {
	t.Helper()
	r, err := EmitServer("cartographer", ServerSpec{Type: "http", URL: "https://example.com/mcp"}, "fork-ext")
	if err != nil {
		t.Fatal(err)
	}
	if r.FilePath != "" || r.FileGlob != ".fork/ext-*/mcp.json" {
		t.Fatalf("a pattern emitted FilePath %q FileGlob %q", r.FilePath, r.FileGlob)
	}
	return r
}

// The entry is written to every match, the stale files go after a write that
// changed bytes and never after a no-op write; the user's other servers stay.
func TestApplyGlob_WritesEveryMatchAndRemovesStaleOnlyOnChange(t *testing.T) {
	registeredPlugins(t, globPlugin())
	home := t.TempDir()
	a, b := filepath.Join(home, ".fork", "ext-1", "mcp.json"), filepath.Join(home, ".fork", "ext-2", "mcp.json")
	stale := filepath.Join(home, ".fork", "st-1", "servers.json")
	other := filepath.Join(home, ".fork", "st-1", "keep.json")
	writeAll(t, map[string]string{a: `{"mcp":{"mine":{"type":"local","command":["x"]}}}`, b: `{}`, stale: `{}`, other: `{}`})

	r := emitGlob(t)
	written, err := Apply([]*EmitResult{r}, home, false)
	if err != nil || len(written) != 2 {
		t.Fatalf("Apply = %v, %v", written, err)
	}
	for _, p := range []string{a, b} {
		got, _ := os.ReadFile(p)
		if !strings.Contains(string(got), "cartographer") {
			t.Errorf("%s lacks the entry:\n%s", p, got)
		}
	}
	if got, _ := os.ReadFile(a); !strings.Contains(string(got), `"mine"`) {
		t.Errorf("another server was lost:\n%s", got)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("the stale file stayed after a changing write")
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("a file the stale pattern does not match was removed: %v", err)
	}
	if !strings.Contains(strings.Join(r.Warnings, "\n"), "restart the window") {
		t.Errorf("no restart note: %v", r.Warnings)
	}

	// A no-op write leaves what the client regenerated alone.
	writeAll(t, map[string]string{stale: `{"user":"server"}`})
	r = emitGlob(t)
	if written, err := Apply([]*EmitResult{r}, home, false); err != nil || len(written) != 0 {
		t.Fatalf("second Apply = %v, %v", written, err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Errorf("a no-op write removed the stale file: %v", err)
	}
}

func TestApplyGlob_NoMatchIsAWarning(t *testing.T) {
	registeredPlugins(t, globPlugin())
	home := t.TempDir()
	r := emitGlob(t)
	written, err := Apply([]*EmitResult{r}, home, false)
	if err != nil || len(written) != 0 || !strings.Contains(strings.Join(r.Warnings, "\n"), "open the client once") {
		t.Fatalf("Apply = %v, %v, warnings %v", written, err, r.Warnings)
	}
	if _, err := os.Stat(filepath.Join(home, ".fork")); !os.IsNotExist(err) {
		t.Error("Apply created the client's folder")
	}
}

func TestApplyGlob_MoreThanThreeStaleOrALinkRemovesNothing(t *testing.T) {
	registeredPlugins(t, globPlugin())
	home := t.TempDir()
	tpl := filepath.Join(home, ".fork", "ext-1", "mcp.json")
	files := map[string]string{tpl: `{}`}
	var stale []string
	for _, n := range []string{"st-1", "st-2", "st-3", "st-4"} {
		p := filepath.Join(home, ".fork", n, "servers.json")
		files[p] = `{}`
		stale = append(stale, p)
	}
	writeAll(t, files)
	if _, err := Apply([]*EmitResult{emitGlob(t)}, home, false); err != nil {
		t.Fatal(err)
	}
	for _, p := range stale {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed although the pattern matched four files: %v", p, err)
		}
	}
}

func TestApplyGlob_ALinkedMatchIsNotFollowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges")
	}
	registeredPlugins(t, globPlugin())
	home, outside := t.TempDir(), t.TempDir()
	target := filepath.Join(outside, "mcp.json")
	writeAll(t, map[string]string{target: `{"mine":1}`})
	// A version folder that is a link: not a match. A file that is a link: refused.
	if err := os.MkdirAll(filepath.Join(home, ".fork", "ext-2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".fork", "ext-1")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, ".fork", "ext-2", "mcp.json")); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(home, ".fork", "st-1", "servers.json")
	writeAll(t, map[string]string{stale: `{}`})
	r := emitGlob(t)
	written, err := Apply([]*EmitResult{r}, home, false)
	if len(written) != 0 {
		t.Fatalf("wrote through a link: %v, %v", written, err)
	}
	if got, _ := os.ReadFile(target); string(got) != `{"mine":1}` {
		t.Errorf("the link's target changed: %s", got)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Errorf("stale files were removed without a write: %v", err)
	}
}

// A template that is not a JSON object is never touched.
func TestApplyGlob_ANonObjectFileIsNeverTouched(t *testing.T) {
	registeredPlugins(t, globPlugin())
	home := t.TempDir()
	tpl := filepath.Join(home, ".fork", "ext-1", "mcp.json")
	writeAll(t, map[string]string{tpl: `[1,2]`})
	if _, err := Apply([]*EmitResult{emitGlob(t)}, home, false); err == nil {
		t.Error("Apply accepted an array as a config")
	}
	if got, _ := os.ReadFile(tpl); string(got) != `[1,2]` {
		t.Errorf("file changed: %s", got)
	}
}

// Stale files are only the ones the stale templates match, behind no link.
func TestRemoveStale_NeverLeavesItsTemplates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges")
	}
	registeredPlugins(t, globPlugin())
	home, outside := t.TempDir(), t.TempDir()
	keep := filepath.Join(outside, "servers.json")
	writeAll(t, map[string]string{keep: `{}`})
	if err := os.MkdirAll(filepath.Join(home, ".fork"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".fork", "st-9")); err != nil {
		t.Fatal(err)
	}
	notes := removeStale("fork-ext", home)
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("a file behind a linked folder was removed: %v (notes %v)", err, notes)
	}
}

func TestRemoveGlob_TakesTheEntryOutOfEveryMatch(t *testing.T) {
	registeredPlugins(t, globPlugin())
	home := t.TempDir()
	a, b := filepath.Join(home, ".fork", "ext-1", "mcp.json"), filepath.Join(home, ".fork", "ext-2", "mcp.json")
	writeAll(t, map[string]string{a: `{}`, b: `{"mcp":{"mine":{"type":"local","command":["x"]}}}`})
	if _, err := Apply([]*EmitResult{emitGlob(t)}, home, false); err != nil {
		t.Fatal(err)
	}
	removed, err := Remove(&ServerConfig{Name: "cartographer", URL: "https://example.com/mcp"}, "fork-ext", home, false)
	if err != nil || !removed {
		t.Fatalf("Remove = %v, %v", removed, err)
	}
	for _, p := range []string{a, b} {
		if got, _ := os.ReadFile(p); strings.Contains(string(got), "cartographer") {
			t.Errorf("%s still holds the entry:\n%s", p, got)
		}
	}
	if got, _ := os.ReadFile(b); !strings.Contains(string(got), `"mine"`) {
		t.Errorf("another server was lost:\n%s", got)
	}
}

func TestMCPFilesAndNoneMode(t *testing.T) {
	registeredPlugins(t, globPlugin(), PluginSpec{Name: "fork-none", Base: string(ProviderCrush), MCPMode: MCPModeNone, Active: true}, PluginSpec{Name: "fork-cli", Base: string(ProviderCrush), MCPMode: MCPModeCLI, Active: true})
	home := t.TempDir()
	writeAll(t, map[string]string{filepath.Join(home, ".fork", "ext-1", "mcp.json"): `{}`})
	d, _ := Lookup("fork-ext")
	if got := d.MCPFiles(home); len(got) != 1 {
		t.Errorf("glob MCPFiles = %v", got)
	}
	crush, _ := Lookup(ProviderCrush)
	if got := crush.MCPFiles(home); len(got) != 1 || got[0] != filepath.Join(home, crush.ConfigPath()) {
		t.Errorf("literal MCPFiles = %v", got)
	}
	for _, n := range []Provider{"fork-none", "fork-cli"} {
		d, _ := Lookup(n)
		if got := d.MCPFiles(home); got != nil {
			t.Errorf("%s MCPFiles = %v", n, got)
		}
	}
	if !UsesNoMCP("fork-none") || UsesNoMCP("fork-cli") || UsesNoMCP("claude") || UsesNoMCP("unknown") {
		t.Error("UsesNoMCP")
	}
	if UsesCLI("fork-none") {
		t.Error("none is not cli")
	}
}
