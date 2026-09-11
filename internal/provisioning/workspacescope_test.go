package provisioning

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// TestProjectDestinationMatrixIsComplete applies D137's completeness rule to
// the project-local half (D193): a cell missing by omission is
// indistinguishable from one unsupported on purpose, and only the second is a
// decision anyone made.
func TestProjectDestinationMatrixIsComplete(t *testing.T) {
	if err := validateProjectMatrix(); err != nil {
		t.Fatal(err)
	}
	for _, kind := range artifactKinds {
		for provider, cell := range projectDestinationMatrix[kind] {
			if cell.unsupported {
				continue
			}
			if len(cell.dir) == 0 {
				t.Errorf("projectDestinationMatrix[%q][%q] is supported but names no path", kind, provider)
			}
		}
	}
	for kind := range projectDestinationMatrix {
		known := false
		for _, k := range artifactKinds {
			if k == kind {
				known = true
			}
		}
		if !known {
			t.Errorf("projectDestinationMatrix has a row for unknown kind %q", kind)
		}
	}
}

// TestProjectDestinationsAreRelativeToTheWorkspace: every project-local path
// must stay inside the workspace. One that escaped would write a perimeter's
// artifacts outside the directory that was bound — the whole failure this plan
// exists to prevent, with extra steps.
func TestProjectDestinationsAreRelativeToTheWorkspace(t *testing.T) {
	for _, kind := range artifactKinds {
		for provider, cell := range projectDestinationMatrix[kind] {
			if cell.unsupported {
				continue
			}
			got := destDirScoped(kind, "demo", provider, ScopeProject)
			if got == "" {
				t.Errorf("%s × %s: supported cell resolved to an empty path", kind, provider)
				continue
			}
			if strings.HasPrefix(got, "/") || strings.HasPrefix(got, "..") {
				t.Errorf("%s × %s resolves outside the workspace: %q", kind, provider, got)
			}
		}
	}
}

// TestDestDirScoped_GlobalIsUnchanged: the scope axis is additive. destDir and
// the global scope must agree on every cell, or an existing installation moves
// its files on the next sync.
func TestDestDirScoped_GlobalIsUnchanged(t *testing.T) {
	for _, kind := range artifactKinds {
		for _, d := range configurator.Providers() {
			want := destDir(kind, "demo", d.Provider)
			got := destDirScoped(kind, "demo", d.Provider, ScopeGlobal)
			if got != want {
				t.Errorf("%s × %s: scoped global = %q, destDir = %q", kind, d.Provider, got, want)
			}
		}
	}
}

// TestProjectScopeDivergesWhereItMust spot-checks the cells the D193 audit
// found to differ from the global ones — these are the paths the clients
// actually read in a project, and getting one wrong ships a silent no-op.
func TestProjectScopeDivergesWhereItMust(t *testing.T) {
	cases := []struct {
		kind     string
		provider configurator.Provider
		want     string
	}{
		// Codex documents the repository skills path as .agents/skills; the
		// global cell stays .codex/skills for the reason D192 records.
		{"skill", configurator.ProviderCodex, ".agents/skills/demo"},
		// A project's MCP servers live in .mcp.json, not in ~/.claude.json.
		{"mcp", configurator.ProviderClaudeCode, ".mcp.json"},
		// The project's own instruction file, which a repository already has.
		{"instructions", configurator.ProviderClaudeCode, "CLAUDE.md"},
		{"instructions", configurator.ProviderCodex, "AGENTS.md"},
		{"instructions", configurator.ProviderOpenCode, "AGENTS.md"},
		{"skill", configurator.ProviderClaudeCode, ".claude/skills/demo"},
		{"skill", configurator.ProviderKiro, ".kiro/skills/demo"},
	}
	for _, tc := range cases {
		if got := destDirScoped(tc.kind, "demo", tc.provider, ScopeProject); got != tc.want {
			t.Errorf("%s × %s project destination = %q, want %q", tc.kind, tc.provider, got, tc.want)
		}
	}
}

// TestSupportsProjectScope_FailsClosed: a provider with no project-local scope
// says so. Decision 11 — never a false guarantee, and never a silent
// degradation to the global catalogue, which is the exposure this plan closes.
func TestSupportsProjectScope_FailsClosed(t *testing.T) {
	for _, p := range []configurator.Provider{
		configurator.ProviderClaudeCode, configurator.ProviderCodex,
		configurator.ProviderOpenCode, configurator.ProviderKiro,
	} {
		if !SupportsProjectScope(p) {
			t.Errorf("%s should support a project scope", p)
		}
		if r := ProjectScopeUnsupportedReason(p); r != "" {
			t.Errorf("%s reports an unsupported reason while supporting the scope: %s", p, r)
		}
	}
	for _, p := range []configurator.Provider{configurator.ProviderHermes, configurator.ProviderAntigravity} {
		if SupportsProjectScope(p) {
			t.Errorf("%s claims a project scope the audit did not find", p)
		}
		if ProjectScopeUnsupportedReason(p) == "" {
			t.Errorf("%s cannot project and gives no reason", p)
		}
	}
}

// TestProjectOwnedPaths covers what the repository-hygiene pass will exclude:
// it must name everything a projection can create, and nothing it cannot.
func TestProjectOwnedPaths(t *testing.T) {
	got := ProjectOwnedPaths(configurator.ProviderClaudeCode)
	want := map[string]bool{".mcp.json": false, "CLAUDE.md": false, ".claude/agents": false, ".claude/hooks": false, ".claude/skills": false}
	for _, p := range got {
		if _, ok := want[p]; !ok {
			t.Errorf("unexpected owned path %q", p)
			continue
		}
		want[p] = true
	}
	for p, seen := range want {
		if !seen {
			t.Errorf("owned path %q is missing: the hygiene pass would not exclude it", p)
		}
	}
	if len(ProjectOwnedPaths(configurator.ProviderHermes)) != 0 {
		t.Error("a provider with no project scope owns paths")
	}
}
