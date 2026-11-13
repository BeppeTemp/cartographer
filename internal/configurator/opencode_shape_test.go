package configurator_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// OpenCode reads two shapes of the same MCP section: the native V2
// "mcp"."servers" (what 2.x writes itself) and the V1 flat "mcp"."<name>" it
// still normalizes on load. The emitter writes the one the installed major
// reads, and a write converges an older shape of the same name, because a file
// holding both makes the native one win silently (D2056).

func openCodeServers(t *testing.T, content []byte) map[string]any {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(content, &root); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, content)
	}
	mcp, _ := root["mcp"].(map[string]any)
	servers, _ := mcp["servers"].(map[string]any)
	return servers
}

func TestEmitOpenCode_NativeShapeFromMajor2AndUnknown(t *testing.T) {
	for _, major := range []int{0, 2, 3} {
		cfg := &configurator.ServerConfig{Name: "wiki", URL: "https://mcp.example.test/mcp", AuthEnabled: true, TokenEnv: "CARTOGRAPHER_TOKENS"}
		r, err := configurator.Emit(cfg, configurator.ProviderOpenCode, configurator.ForOpenCodeMajor(major))
		if err != nil {
			t.Fatalf("major %d: %v", major, err)
		}
		servers := openCodeServers(t, r.Content)
		entry, ok := servers["wiki"].(map[string]any)
		if !ok {
			t.Fatalf("major %d: no wiki entry under mcp.servers", major)
		}
		if _, has := entry["enabled"]; has {
			t.Errorf("major %d: 2.x uses the inverse disabled; enabled must not be written", major)
		}
		if entry["oauth"] != false {
			t.Errorf("major %d: oauth = %v, want false beside a header credential", major, entry["oauth"])
		}
	}
}

func TestEmitOpenCode_V1ShapeOnMajor1(t *testing.T) {
	cfg := &configurator.ServerConfig{Name: "wiki", URL: "https://mcp.example.test/mcp", AuthEnabled: true, TokenEnv: "CARTOGRAPHER_TOKENS"}
	r, err := configurator.Emit(cfg, configurator.ProviderOpenCode, configurator.ForOpenCodeMajor(1))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(r.Content, &root); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	mcp := root["mcp"].(map[string]any)
	if _, has := mcp["servers"]; has {
		t.Error("1.x gets the V1 flat shape, not mcp.servers")
	}
	entry, ok := mcp["wiki"].(map[string]any)
	if !ok {
		t.Fatalf("no flat wiki entry: %s", r.Content)
	}
	if entry["enabled"] != true {
		t.Errorf("enabled = %v, want true (V1)", entry["enabled"])
	}
	if _, has := entry["oauth"]; has {
		t.Error("oauth: false is a V2 field; 1.x does not know it")
	}
}

func TestMCPServerNames_ReadsBothShapesAndSkipsMeta(t *testing.T) {
	root := map[string]any{"mcp": map[string]any{
		"servers": map[string]any{"native": map[string]any{"type": "remote"}},
		"legacy":  map[string]any{"type": "remote"},
		"timeout": map[string]any{"catalog": 30000},
	}}
	names := configurator.MCPServerNames(root, configurator.ProviderOpenCode)
	if len(names) != 2 || names[0] != "legacy" || names[1] != "native" {
		t.Errorf("names = %v, want [legacy native]", names)
	}
	if entry, ok := configurator.MCPServerEntry(root, configurator.ProviderOpenCode, "native"); !ok || entry == nil {
		t.Error("the native entry must be found")
	}
	if _, ok := configurator.MCPServerEntry(root, configurator.ProviderOpenCode, "timeout"); ok {
		t.Error("timeout is section metadata, not a server")
	}
}

// The incident: a native entry written by `opencode mcp add` for the same name
// shadows the flat one Cartographer writes, so a sync that keeps writing the
// flat entry never reaches the client.
func TestApply_OpenCodeConvergesTheNativeShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, filepath.FromSlash(configurator.OpenCodeConfigPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const before = `{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "cartographer": { "type": "remote", "url": "https://old.example.com/mcp", "enabled": true },
    "servers": {
      "cartographer": { "type": "remote", "url": "https://second.example.com/mcp" },
      "mine": { "type": "remote", "url": "https://example.com/mcp" }
    }
  }
}`
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &configurator.ServerConfig{Name: "cartographer", URL: "https://new.example.com/mcp", TokenEnv: "CARTOGRAPHER_TOKENS"}
	r, err := configurator.Emit(cfg, configurator.ProviderOpenCode)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configurator.Apply([]*configurator.EmitResult{r}, dir, false); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, data)
	}
	mcp := root["mcp"].(map[string]any)
	if _, has := mcp["cartographer"]; has {
		t.Errorf("the flat entry survived a native write and still shadows it:\n%s", data)
	}
	servers := mcp["servers"].(map[string]any)
	if got := servers["cartographer"].(map[string]any)["url"]; got != cfg.URL {
		t.Errorf("native cartographer url = %v, want %v", got, cfg.URL)
	}
	if _, kept := servers["mine"]; !kept {
		t.Error("another server of the user was dropped")
	}
}

// The inverse direction: a client read as 1.x gets the flat shape, and the
// native entry of the same name left by a 2.x era must not survive to shadow it
// the day the user upgrades.
func TestApply_OpenCodeLegacyWriteRemovesTheNativeSibling(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, filepath.FromSlash(configurator.OpenCodeConfigPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const before = `{"mcp":{"servers":{"cartographer":{"type":"remote","url":"https://second.example.com/mcp"},"mine":{"type":"remote","url":"https://example.com/mcp"}}}}`
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &configurator.ServerConfig{Name: "cartographer", URL: "https://new.example.com/mcp", TokenEnv: "CARTOGRAPHER_TOKENS"}
	r, err := configurator.Emit(cfg, configurator.ProviderOpenCode, configurator.ForOpenCodeMajor(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configurator.Apply([]*configurator.EmitResult{r}, dir, false); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, data)
	}
	mcp := root["mcp"].(map[string]any)
	if _, ok := mcp["cartographer"].(map[string]any); !ok {
		t.Fatalf("the flat entry was not written: %s", data)
	}
	servers, _ := mcp["servers"].(map[string]any)
	if _, has := servers["cartographer"]; has {
		t.Errorf("the native entry of the same name survived: %s", data)
	}
	if _, kept := servers["mine"]; !kept {
		t.Error("another server of the user was dropped")
	}
}

// A server may legitimately be named "servers" (the name alphabet allows it);
// removing it must take the entry, never the native section it is filed under.
func TestRemove_OpenCodeA_ServerNamedServersKeepsTheSection(t *testing.T) {
	root := map[string]any{"mcp": map[string]any{
		"servers": map[string]any{
			"mine":    map[string]any{"type": "remote"},
			"servers": map[string]any{"type": "remote"},
		},
	}}
	if !configurator.RemoveMCPServer(root, configurator.ProviderOpenCode, "servers") {
		t.Fatal("RemoveMCPServer reported nothing removed")
	}
	servers := root["mcp"].(map[string]any)["servers"].(map[string]any)
	if _, ok := servers["mine"]; !ok {
		t.Fatalf("the native section was deleted: %v", root)
	}
	if _, ok := servers["servers"]; ok {
		t.Error("the server named servers survived")
	}
}

// On the V1 flat map a name like "servers" would be the native section itself,
// so 1.x refuses it; 2.x carries it inside mcp.servers, where it is an ordinary
// (and removable) entry.
func TestEmitOpenCode_SectionNamesOnlyOnTheNativeShape(t *testing.T) {
	for _, name := range []string{"servers", "timeout"} {
		if _, err := configurator.Emit(&configurator.ServerConfig{Name: name, URL: "https://mcp.example.com/mcp"},
			configurator.ProviderOpenCode, configurator.ForOpenCodeMajor(1)); !errors.Is(err, configurator.ErrMCPNameUnrepresentable) {
			t.Errorf("%s on 1.x: err = %v, want ErrMCPNameUnrepresentable", name, err)
		}
		r, err := configurator.Emit(&configurator.ServerConfig{Name: name, URL: "https://mcp.example.com/mcp"},
			configurator.ProviderOpenCode, configurator.ForOpenCodeMajor(2))
		if err != nil {
			t.Fatalf("%s on 2.x: %v", name, err)
		}
		if entry, ok := openCodeServers(t, r.Content)[name]; !ok || entry == nil {
			t.Errorf("%s on 2.x: not written under mcp.servers", name)
		}
	}
}

// Every OpenCode emission states the path it wrote under: the merge never
// infers a shape again, so an emitter that forgot the path would write nothing
// (the safe failure) rather than corrupt the file.
func TestEmitOpenCode_ReportsThePathItWrote(t *testing.T) {
	cfg := &configurator.ServerConfig{Name: "wiki", URL: "https://mcp.example.com/mcp"}
	native, err := configurator.Emit(cfg, configurator.ProviderOpenCode)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(native.MCPServerPath, "."); got != "mcp.servers" {
		t.Errorf("native path = %q, want mcp.servers", got)
	}
	legacy, err := configurator.Emit(cfg, configurator.ProviderOpenCode, configurator.ForOpenCodeMajor(1))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(legacy.MCPServerPath, "."); got != "mcp" {
		t.Errorf("V1 path = %q, want mcp", got)
	}
}

// A provider with two shapes must state the one it wrote: the merge never
// guesses from the content, so a missing path writes nothing rather than
// treating an entry named "servers" as the native section.
func TestMergeMCPEmission_NoShapeGuessing(t *testing.T) {
	root := map[string]any{}
	emitted := map[string]any{"mcp": map[string]any{
		"servers": map[string]any{"type": "remote", "url": "https://example.com/mcp"},
	}}
	configurator.MergeMCPEmission(root, emitted, configurator.ProviderOpenCode, nil)
	if len(root) != 0 {
		t.Errorf("merge wrote without a path: %v", root)
	}
}

func TestRemove_OpenCodeRemovesBothShapes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, filepath.FromSlash(configurator.OpenCodeConfigPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const before = `{"$schema":"https://opencode.ai/config.json","mcp":{"cartographer":{"type":"remote","url":"https://old.example.com/mcp"},"servers":{"cartographer":{"type":"remote","url":"https://second.example.com/mcp"},"mine":{"type":"remote","url":"https://example.com/mcp"}}}}`
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, err := configurator.Remove(&configurator.ServerConfig{Name: "cartographer"}, configurator.ProviderOpenCode, dir, false)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !removed {
		t.Fatal("Remove reported nothing removed")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, data)
	}
	mcp := root["mcp"].(map[string]any)
	if _, has := mcp["cartographer"]; has {
		t.Errorf("the flat entry survived: %s", data)
	}
	servers := mcp["servers"].(map[string]any)
	if _, has := servers["cartographer"]; has {
		t.Errorf("the native entry survived: %s", data)
	}
	if _, kept := servers["mine"]; !kept {
		t.Error("another server of the user was dropped")
	}
}
