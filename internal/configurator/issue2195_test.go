package configurator_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// #2195: with only an opencode.jsonc, the opencode.json is created beside it
// (OpenCode reads both), the .jsonc is left alone and one note says so.
func TestApply_OpenCodeJSONCOnlyGetsASiblingAndANote(t *testing.T) {
	dir := t.TempDir()
	jsonc := filepath.Join(dir, filepath.FromSlash(configurator.OpenCodeConfigPath)+"c")
	if err := os.MkdirAll(filepath.Dir(jsonc), 0o755); err != nil {
		t.Fatal(err)
	}
	const body = "{\n  // mine\n  \"theme\": \"dark\"\n}\n"
	if err := os.WriteFile(jsonc, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	apply := func() *configurator.EmitResult {
		r, err := configurator.Emit(&configurator.ServerConfig{Name: "cartographer", URL: "https://example.com/mcp"}, configurator.ProviderOpenCode)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := configurator.Apply([]*configurator.EmitResult{r}, dir, false); err != nil {
			t.Fatal(err)
		}
		return r
	}
	count := func(r *configurator.EmitResult) int {
		n := 0
		for _, w := range r.Warnings {
			if strings.Contains(w, "opencode.jsonc") && strings.Contains(w, "keep the Cartographer entry in opencode.json") {
				n++
			}
		}
		return n
	}
	if n := count(apply()); n != 1 {
		t.Errorf("first apply: %d notes, want 1", n)
	}
	if got, _ := os.ReadFile(jsonc); string(got) != body {
		t.Errorf("the .jsonc was changed: %q", got)
	}
	if _, err := os.Stat(strings.TrimSuffix(jsonc, "c")); err != nil {
		t.Errorf("opencode.json was not created: %v", err)
	}
	if n := count(apply()); n != 0 {
		t.Errorf("second apply: %d notes, want none once the file exists", n)
	}
}

// With both files, the .json is the one managed (OpenCode reads both).
func TestApply_OpenCodeJSONWithJSONCIsManaged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, filepath.FromSlash(configurator.OpenCodeConfigPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{path, path + "c"} {
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r, err := configurator.Emit(&configurator.ServerConfig{Name: "cartographer", URL: "https://example.com/mcp"}, configurator.ProviderOpenCode)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configurator.Apply([]*configurator.EmitResult{r}, dir, false); err != nil {
		t.Fatal(err)
	}
}

// #2195: a config.toml that is not TOML is refused like a JSON one.
func TestApplyCodex_InvalidTOMLIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const body = "model = \"x\nthis is not toml\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := configurator.Emit(&configurator.ServerConfig{Name: "cartographer", URL: "https://example.com/mcp"}, configurator.ProviderCodex)
	if err != nil {
		t.Fatal(err)
	}
	_, err = configurator.Apply([]*configurator.EmitResult{r}, dir, false)
	if err == nil || !strings.Contains(err.Error(), "not valid TOML") || !strings.Contains(err.Error(), "fix or delete it") {
		t.Fatalf("want a not-valid-TOML refusal, got %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != body {
		t.Errorf("the file was changed: %q", got)
	}
}

// #2195: a URL with a query is written with "&" in every JSON provider, in a
// file created by the emit as in one merged into.
func TestApply_URLsKeepTheirAmpersand(t *testing.T) {
	const url = "https://example.com/mcp?kb=a&kb=b"
	escaped := "\\u00" + "26" // the escape encoding/json applies to an ampersand by default
	for _, p := range []configurator.Provider{
		configurator.ProviderClaudeCode, configurator.ProviderOpenCode, configurator.ProviderKiro,
		configurator.ProviderAntigravity, configurator.ProviderCopilot,
	} {
		dir := t.TempDir()
		r, err := configurator.Emit(&configurator.ServerConfig{Name: "cartographer", URL: url}, p)
		if err != nil {
			t.Fatal(p, err)
		}
		written, err := configurator.Apply([]*configurator.EmitResult{r}, dir, false)
		if err != nil || len(written) == 0 {
			t.Fatal(p, written, err)
		}
		data, _ := os.ReadFile(written[0])
		if strings.Contains(string(data), escaped) || !strings.Contains(string(data), "kb=a&kb=b") {
			t.Errorf("%s wrote an escaped ampersand:\n%s", p, data)
		}
	}
}
