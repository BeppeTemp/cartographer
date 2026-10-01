package provisioning_test

// A KB agent's `tools` allow-list is enforced by Claude Code only (D283):
// every other client must either say it widened the agent or, for an agent
// marked strict_tools, not receive it.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func restrictedAgentManifest(t *testing.T, agents map[string]string) (provisioning.Manifest, string) {
	t.Helper()
	kbRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(kbRoot, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, src := range agents {
		if err := os.WriteFile(filepath.Join(kbRoot, "agents", name+".md"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb-a": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	return m, kbRoot
}

const (
	explorerSrc = "---\nname: explorer\ndescription: Reads only\ntools: Read, Grep\n---\nBody.\n"
	strictSrc   = "---\nname: auditor\ndescription: Reads only\ntools: [Read, Grep]\nstrict_tools: true\n---\nBody.\n"
	plainSrc    = "---\nname: helper\ndescription: General\n---\nBody.\n"
)

func applyAgents(t *testing.T, m provisioning.Manifest, kbRoot string, p configurator.Provider, lock provisioning.Lock) (provisioning.AppliedResult, string) {
	t.Helper()
	base := t.TempDir()
	res, err := provisioning.Apply(provisioning.FilterForProvider(m, p), provisioning.ApplyOptions{
		AutoTrust: true, KBRoots: map[string]string{"kb-a": kbRoot}, Provider: p, BaseDir: base, Lock: lock,
	})
	if err != nil {
		t.Fatalf("Apply %s: %v", p, err)
	}
	return res, base
}

func warningsMentioning(res provisioning.AppliedResult, sub string) []string {
	var out []string
	for _, w := range res.Warnings {
		if strings.Contains(w, sub) {
			out = append(out, w)
		}
	}
	return out
}

func TestApply_WidenedAgentWarnsNamingAgentAndClient(t *testing.T) {
	m, kbRoot := restrictedAgentManifest(t, map[string]string{"explorer": explorerSrc, "helper": plainSrc})
	for _, p := range []configurator.Provider{configurator.ProviderOpenCode, configurator.ProviderCodex, configurator.ProviderKiro, configurator.ProviderAntigravity} {
		res, _ := applyAgents(t, m, kbRoot, p, provisioning.Lock{})
		got := warningsMentioning(res, `agent "explorer" declares tools: Read, Grep`)
		if len(got) != 1 || !strings.Contains(got[0], string(p)) {
			t.Errorf("%s: want one warning naming agent, tools and client, got %v", p, res.Warnings)
		}
		if w := warningsMentioning(res, `"helper"`); len(w) != 0 {
			t.Errorf("%s: an agent with no tools must not warn: %v", p, w)
		}
	}
}

func TestApply_WidenedAgentWarnsOnEveryRun(t *testing.T) {
	m, kbRoot := restrictedAgentManifest(t, map[string]string{"explorer": explorerSrc})
	res, base := applyAgents(t, m, kbRoot, configurator.ProviderCodex, provisioning.Lock{})
	res2, err := provisioning.Apply(provisioning.FilterForProvider(m, configurator.ProviderCodex), provisioning.ApplyOptions{
		AutoTrust: true, KBRoots: map[string]string{"kb-a": kbRoot}, Provider: configurator.ProviderCodex, BaseDir: base, Lock: res.NewLock,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Written) != 0 && len(writtenOfKind(res2.Written, "agent")) != 0 {
		t.Fatalf("second run rewrote the agent: %+v", res2.Written)
	}
	if len(warningsMentioning(res2, `agent "explorer"`)) != 1 {
		t.Errorf("an unchanged widened agent must still be reported: %v", res2.Warnings)
	}
}

func TestApply_ClaudeKeepsToolsAndDoesNotWarn(t *testing.T) {
	m, kbRoot := restrictedAgentManifest(t, map[string]string{"explorer": explorerSrc})
	res, base := applyAgents(t, m, kbRoot, configurator.ProviderClaudeCode, provisioning.Lock{})
	if w := warningsMentioning(res, "tools"); len(w) != 0 {
		t.Errorf("claude enforces tools, no warning expected: %v", w)
	}
	data, err := os.ReadFile(filepath.Join(base, ".claude", "agents", "explorer.md"))
	if err != nil || !strings.Contains(string(data), "tools: Read, Grep") {
		t.Errorf("claude must receive tools verbatim: %v %s", err, data)
	}
}

func TestApply_StrictAgentIsSkippedNotWidened(t *testing.T) {
	m, kbRoot := restrictedAgentManifest(t, map[string]string{"auditor": strictSrc, "helper": plainSrc})
	for _, p := range []configurator.Provider{configurator.ProviderOpenCode, configurator.ProviderCodex, configurator.ProviderKiro} {
		// Through the filter (the client path): the agent is simply not part of
		// what this provider is offered.
		res, base := applyAgents(t, m, kbRoot, p, provisioning.Lock{})
		for _, w := range res.Written {
			if w.Name == "auditor" {
				t.Errorf("%s: strict agent written: %+v", p, w)
			}
		}
		if got := writtenOfKind(res.Written, "agent"); len(got) != 1 || got[0].Name != "helper" {
			t.Errorf("%s: only the unrestricted agent should be written: %+v", p, got)
		}
		if w := warningsMentioning(res, `"auditor"`); len(w) != 0 {
			t.Errorf("%s: a skipped agent was not widened, nothing to warn: %v", p, w)
		}
		_ = base

		// Unfiltered manifest (a caller that skips FilterForProvider): Apply
		// itself must still refuse to widen, and report it as unsupported.
		base2 := t.TempDir()
		res2, err := provisioning.Apply(m, provisioning.ApplyOptions{
			AutoTrust: true, KBRoots: map[string]string{"kb-a": kbRoot}, Provider: p, BaseDir: base2,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(res2.Unsupported) != 1 || res2.Unsupported[0].Name != "auditor" {
			t.Errorf("%s: strict agent must be reported unsupported: %+v", p, res2.Unsupported)
		}
		for _, w := range res2.Written {
			if w.Name == "auditor" {
				t.Errorf("%s: strict agent written: %+v", p, w)
			}
		}
	}
	// Claude enforces the list, so the strict agent is installed there.
	res, _ := applyAgents(t, m, kbRoot, configurator.ProviderClaudeCode, provisioning.Lock{})
	if got := writtenOfKind(res.Written, "agent"); len(got) != 2 {
		t.Errorf("claude must receive both agents: %+v", got)
	}
}

func TestApply_StrictAgentInstalledEarlierIsPruned(t *testing.T) {
	// Sync 1: the agent is not strict, so codex received it (widened).
	loose := strings.Replace(strictSrc, "strict_tools: true\n", "", 1)
	m1, kbRoot := restrictedAgentManifest(t, map[string]string{"auditor": loose})
	res1, base := applyAgents(t, m1, kbRoot, configurator.ProviderCodex, provisioning.Lock{})
	path := filepath.Join(base, ".codex", "agents", "auditor.toml")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// Sync 2: the author marks it strict; the widened copy must go.
	if err := os.WriteFile(filepath.Join(kbRoot, "agents", "auditor.md"), []byte(strictSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	m2, err := provisioning.BuildManifest(nil, map[string]string{"kb-a": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provisioning.Apply(m2, provisioning.ApplyOptions{
		AutoTrust: true, KBRoots: map[string]string{"kb-a": kbRoot}, Provider: configurator.ProviderCodex, BaseDir: base, Lock: res1.NewLock,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the widened copy survived strict_tools: %v", err)
	}
}

func TestParseAgentRestriction(t *testing.T) {
	cases := []struct {
		name, src string
		want      *provisioning.AgentRestriction
	}{
		{"none", plainSrc, nil},
		{"no frontmatter", "Body only", nil},
		{"csv", explorerSrc, &provisioning.AgentRestriction{Tools: "Read, Grep"}},
		{"list strict", strictSrc, &provisioning.AgentRestriction{Tools: "Read, Grep", Strict: true}},
		{"strict without tools is nothing to protect", "---\nname: x\nstrict_tools: true\n---\nb", nil},
	}
	for _, c := range cases {
		got := provisioning.ParseAgentRestriction([]byte(c.src))
		if (got == nil) != (c.want == nil) || (got != nil && !reflect.DeepEqual(*got, *c.want)) {
			t.Errorf("%s: got %+v want %+v", c.name, got, c.want)
		}
	}
}
