package provisioning_test

// `providers:` frontmatter on an agent (D291): the author's per-client native
// fields reach that client's agent file verbatim, and count as the restriction
// that keeps a strict agent from being skipped there.
//
// These tests prove the mechanism copies what the author wrote. They do NOT
// prove any client honours the keys used here: that was never run against the
// real clients (see agentnative.go).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

const nativeAgentSrc = `---
name: auditor
description: Reads only
tools: [Read, Grep]
strict_tools: true
providers:
  opencode:
    permission:
      edit: deny
      bash: deny
  codex: { sandbox_mode: read-only, approval_policy: "never" }
  kiro: { tools: ["read"], allowedTools: ["read"] }
  antigravity: { readonly: true }
---
Body.
`

func TestApply_ProvidersFieldsReachEachNativeFileVerbatim(t *testing.T) {
	m, kbRoot := restrictedAgentManifest(t, map[string]string{"auditor": nativeAgentSrc})
	cases := []struct {
		p    configurator.Provider
		file string
		want []string
	}{
		{configurator.ProviderOpenCode, ".config/opencode/agents/auditor.md", []string{"mode: subagent\npermission:\n  edit: deny\n  bash: deny\n---\n"}},
		{configurator.ProviderCodex, ".codex/agents/auditor.toml", []string{`sandbox_mode = "read-only"`, `approval_policy = "never"`}},
		{configurator.ProviderAntigravity, ".gemini/config/agents/auditor.md", []string{"subagent: true\nreadonly: true\n---\n"}},
	}
	for _, c := range cases {
		res, base := applyAgents(t, m, kbRoot, c.p, provisioning.Lock{})
		data, err := os.ReadFile(filepath.Join(base, filepath.FromSlash(c.file)))
		if err != nil {
			t.Fatalf("%s: strict agent with a providers entry must be installed: %v (unsupported: %+v)", c.p, err, res.Unsupported)
		}
		for _, w := range c.want {
			if !strings.Contains(string(data), w) {
				t.Errorf("%s: %q missing from\n%s", c.p, w, data)
			}
		}
		if strings.Contains(string(data), "Grep") {
			t.Errorf("%s: Claude's tools must still not be mapped:\n%s", c.p, data)
		}
		if w := warningsMentioning(res, `agent "auditor"`); len(w) != 0 {
			t.Errorf("%s: an agent with its own native restriction is not widened: %v", c.p, w)
		}
	}

	// Kiro: JSON, the author's fields after name/description/prompt.
	_, base := applyAgents(t, m, kbRoot, configurator.ProviderKiro, provisioning.Lock{})
	data, err := os.ReadFile(filepath.Join(base, ".kiro", "agents", "auditor.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("kiro file is not JSON: %v\n%s", err, data)
	}
	for _, k := range []string{"tools", "allowedTools"} {
		l, ok := got[k].([]any)
		if !ok || len(l) != 1 || l[0] != "read" {
			t.Errorf("kiro %s = %v, want [read]\n%s", k, got[k], data)
		}
	}
	if got["name"] != "auditor" || !strings.HasPrefix(got["prompt"].(string), "Body.") {
		t.Errorf("kiro base fields changed: %q", data)
	}
}

func TestApply_ProvidersEntryScopesStrictToTheOtherClients(t *testing.T) {
	// Only codex has an entry: codex installs it, the rest skip it.
	src := "---\nname: auditor\ndescription: d\ntools: Read\nstrict_tools: true\nproviders:\n  codex: { sandbox_mode: read-only }\n---\nBody.\n"
	m, kbRoot := restrictedAgentManifest(t, map[string]string{"auditor": src})
	res, _ := applyAgents(t, m, kbRoot, configurator.ProviderCodex, provisioning.Lock{})
	if got := writtenOfKind(res.Written, "agent"); len(got) != 1 {
		t.Fatalf("codex has a native restriction: %+v", got)
	}
	for _, p := range []configurator.Provider{configurator.ProviderOpenCode, configurator.ProviderKiro, configurator.ProviderAntigravity} {
		res, _ := applyAgents(t, m, kbRoot, p, provisioning.Lock{})
		if got := writtenOfKind(res.Written, "agent"); len(got) != 0 {
			t.Errorf("%s: no restriction, strict agent must be skipped: %+v", p, got)
		}
	}
}

func TestApply_ProvidersEntryStopsTheWidenedWarningOnlyThere(t *testing.T) {
	src := "---\nname: explorer\ndescription: d\ntools: Read\nproviders:\n  codex: { sandbox_mode: read-only }\n---\nBody.\n"
	m, kbRoot := restrictedAgentManifest(t, map[string]string{"explorer": src})
	if res, _ := applyAgents(t, m, kbRoot, configurator.ProviderCodex, provisioning.Lock{}); len(warningsMentioning(res, `"explorer"`)) != 0 {
		t.Errorf("codex: %v", res.Warnings)
	}
	res, _ := applyAgents(t, m, kbRoot, configurator.ProviderOpenCode, provisioning.Lock{})
	if w := warningsMentioning(res, "providers.opencode"); len(w) != 1 {
		t.Errorf("opencode must still warn, and point at providers.opencode: %v", res.Warnings)
	}
}

func TestApply_ProvidersReservedKeyOrUnknownClientFailsTheTranslation(t *testing.T) {
	for _, bad := range []string{
		"providers:\n  codex: { name: other }\n",
		"providers:\n  nope: { a: b }\n",
	} {
		src := "---\nname: x\ndescription: d\n" + bad + "---\nBody.\n"
		m, kbRoot := restrictedAgentManifest(t, map[string]string{"x": src})
		base := t.TempDir()
		_, err := provisioning.Apply(provisioning.FilterForProvider(m, configurator.ProviderCodex), provisioning.ApplyOptions{
			AutoTrust: true, KBRoots: map[string]string{"kb-a": kbRoot}, Provider: configurator.ProviderCodex, BaseDir: base,
		})
		if err == nil || !strings.Contains(err.Error(), "providers") {
			t.Errorf("%q: want an error naming providers, got %v", bad, err)
		}
		if _, statErr := os.Stat(filepath.Join(base, ".codex", "agents", "x.toml")); statErr == nil {
			t.Errorf("%q: nothing may be written", bad)
		}
	}
}

func TestParseAgentRestriction_ListsNativeClients(t *testing.T) {
	r := provisioning.ParseAgentRestriction([]byte(nativeAgentSrc))
	if r == nil || !strings.EqualFold(strings.Join(r.Native, ","), "antigravity,codex,kiro,opencode") {
		t.Fatalf("got %+v", r)
	}
}

func TestFrontmatter_NestedBlockIsNotFlattenedIntoSiblingKeys(t *testing.T) {
	raw, _, _ := okf.SplitFrontmatter(nativeAgentSrc)
	fm, err := okf.ParseFrontmatter(raw)
	if err != nil {
		t.Fatal(err)
	}
	// Before D291 the indented `tools:` lines became top-level keys.
	if v, _ := fm.Get("tools"); v == nil {
		t.Fatal("tools lost")
	}
	if _, ok := fm.Get("permission"); ok {
		t.Error("a nested key leaked to the top level")
	}
	if !strings.Contains(fm.Serialize(), "permission:\n      edit: deny") {
		t.Errorf("nested block lost on serialize:\n%s", fm.Serialize())
	}
}

// D320: an `@<server>` the client has no MCP entry for is named at sync; the
// agent is still installed with the entry verbatim (D291).
func TestSyncWarnsAbsentMCPServer(t *testing.T) {
	src := "---\nname: operator\ndescription: d\nproviders:\n  kiro: { tools: [\"@builtin\", \"@homeassistant/call_service\"] }\n---\nBody.\n"
	m, kbRoot := restrictedAgentManifest(t, map[string]string{"operator": src})

	res, base := applyAgents(t, m, kbRoot, configurator.ProviderKiro, provisioning.Lock{})
	w := warningsMentioning(res, "@homeassistant")
	if len(w) != 1 || !strings.Contains(w[0], `agent "operator" on kiro`) {
		t.Fatalf("want one warning naming agent, client and server, got %v", res.Warnings)
	}
	if len(warningsMentioning(res, "@builtin")) != 0 {
		t.Errorf("@builtin is not a server: %v", res.Warnings)
	}
	if _, err := os.Stat(filepath.Join(base, ".kiro", "agents", "operator.json")); err != nil {
		t.Errorf("the warning is advisory, the agent must still be installed: %v", err)
	}

	// Same agent, the server configured on the client: no warning.
	base = t.TempDir()
	mcp := filepath.Join(base, ".kiro", "settings", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(mcp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mcp, []byte(`{"mcpServers":{"homeassistant":{"command":"x"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := provisioning.Apply(provisioning.FilterForProvider(m, configurator.ProviderKiro), provisioning.ApplyOptions{
		AutoTrust: true, KBRoots: map[string]string{"kb-a": kbRoot}, Provider: configurator.ProviderKiro, BaseDir: base,
	})
	if err != nil {
		t.Fatal(err)
	}
	if w := warningsMentioning(res, "@homeassistant"); len(w) != 0 {
		t.Errorf("configured server must not warn: %v", w)
	}
}
