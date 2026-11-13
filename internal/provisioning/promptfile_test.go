package provisioning

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

const promptFileAgent = "---\nname: x\ndescription: Kiro reads a file\nproviders:\n  kiro:\n    prompt_file: .claude/agents/x.md\n---\nStub body.\n"

func TestTranslateAgentForKiro_PromptRefReplacesBody(t *testing.T) {
	out, err := translateAgentForKiro("x", []byte(promptFileAgent), "file://../../.claude/agents/x.md")
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"name\": \"x\",\n  \"description\": \"Kiro reads a file\",\n  \"prompt\": \"file://../../.claude/agents/x.md\"\n}\n"
	if string(out) != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
}

// promptFileApply writes the agent in a KB and applies it for provider/scope on base.
func promptFileApply(t *testing.T, src, base string, provider configurator.Provider, scope contract.Scope, lock Lock) (AppliedResult, string) {
	t.Helper()
	kb := t.TempDir()
	if err := os.MkdirAll(filepath.Join(kb, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kb, "agents", "x.md"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := contract.BuildManifest(nil, map[string]string{"kb-a": kb}, contract.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Apply(m, ApplyOptions{KBRoots: map[string]string{"kb-a": kb}, Provider: provider, BaseDir: base, Scope: scope, Lock: lock})
	if err != nil {
		t.Fatal(err)
	}
	return res, kb
}

func TestPromptFile_AppliesReferenceInWorkspace(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, ".claude", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, ".claude", "agents", "x.md"), []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, _ := promptFileApply(t, promptFileAgent, base, configurator.ProviderKiro, contract.ScopeProject, Lock{})
	if len(res.Refused) != 0 || hasWarning(res.Warnings, "prompt_file") {
		t.Fatalf("refused %v warnings %v", res.Refused, res.Warnings)
	}
	got, err := os.ReadFile(filepath.Join(base, ".kiro", "agents", "x.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"prompt": "file://../../.claude/agents/x.md"`) || strings.Contains(string(got), "Stub body") {
		t.Fatalf("agent file:\n%s", got)
	}
}

func TestPromptFile_MissingFileWarnsAndWrites(t *testing.T) {
	base := t.TempDir()
	res, _ := promptFileApply(t, promptFileAgent, base, configurator.ProviderKiro, contract.ScopeProject, Lock{})
	if len(res.Refused) != 0 {
		t.Fatalf("a missing target must not refuse: %v", res.Refused)
	}
	if _, err := os.Stat(filepath.Join(base, ".kiro", "agents", "x.json")); err != nil {
		t.Fatal(err)
	}
	if !hasWarning(res.Warnings, `prompt_file ".claude/agents/x.md" does not exist`) {
		t.Fatalf("warnings %v", res.Warnings)
	}
}

func TestPromptFile_SymlinkOutsideHoldsAndCarriesLock(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "x.md"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, ".claude", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "x.md"), filepath.Join(base, ".claude", "agents", "x.md")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	// An earlier sync wrote the agent with its body.
	plain := "---\nname: x\ndescription: Kiro reads a file\n---\nStub body.\n"
	first, _ := promptFileApply(t, plain, base, configurator.ProviderKiro, contract.ScopeProject, Lock{})
	dest := filepath.Join(base, ".kiro", "agents", "x.json")
	before, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	res, _ := promptFileApply(t, promptFileAgent, base, configurator.ProviderKiro, contract.ScopeProject, first.NewLock)
	if len(res.Refused) != 1 || !hasWarning(res.Warnings, `agent "x" held for kiro: prompt_file ".claude/agents/x.md" escapes the workspace`) {
		t.Fatalf("refused %v warnings %v", res.Refused, res.Warnings)
	}
	after, _ := os.ReadFile(dest)
	if string(after) != string(before) {
		t.Fatal("a held agent rewrote its previous file")
	}
	var carried bool
	for _, mf := range res.NewLock.Managed {
		carried = carried || (mf.Kind == "agent" && mf.Name == "x")
	}
	if !carried {
		t.Fatalf("the lock lost the held agent: %+v", res.NewLock.Managed)
	}
}

func TestPromptFile_GlobalScopeHolds(t *testing.T) {
	base := t.TempDir()
	res, _ := promptFileApply(t, promptFileAgent, base, configurator.ProviderKiro, contract.ScopeGlobal, Lock{})
	if len(res.Refused) != 1 || !hasWarning(res.Warnings, "needs a workspace binding") {
		t.Fatalf("refused %v warnings %v", res.Refused, res.Warnings)
	}
	if _, err := os.Stat(filepath.Join(base, ".kiro", "agents", "x.json")); !os.IsNotExist(err) {
		t.Fatalf("a held agent was written: %v", err)
	}
}

// Other clients ignore the key and keep the body (and its provenance footer).
func TestPromptFile_OtherClientsKeepBody(t *testing.T) {
	src := "---\nname: x\ndescription: d\nproviders:\n  codex:\n    prompt_file: .claude/agents/x.md\n---\nStub body.\n"
	base := t.TempDir()
	res, _ := promptFileApply(t, src, base, configurator.ProviderCodex, contract.ScopeProject, Lock{})
	if len(res.Refused) != 0 {
		t.Fatalf("refused %v", res.Refused)
	}
	got, err := os.ReadFile(filepath.Join(base, ".codex", "agents", "x.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "Stub body.") || strings.Contains(string(got), "prompt_file") {
		t.Fatalf("codex file:\n%s", got)
	}
}

// Status and sync print the same line for a held agent.
func TestPromptFile_StatusIssueEqualsSyncWarning(t *testing.T) {
	base := t.TempDir()
	res, kb := promptFileApply(t, promptFileAgent, base, configurator.ProviderKiro, contract.ScopeGlobal, Lock{})
	m, err := contract.BuildManifest(nil, map[string]string{"kb-a": kb}, contract.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	issues := AgentPromptFileIssues(m, ApplyOptions{KBRoots: map[string]string{"kb-a": kb}, Provider: configurator.ProviderKiro, BaseDir: base, Scope: contract.ScopeGlobal})
	if len(issues) != 1 || !hasWarning(res.Warnings, issues[0]) {
		t.Fatalf("status %v vs sync %v", issues, res.Warnings)
	}
}

func hasWarning(ws []string, sub string) bool {
	for _, w := range ws {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}
