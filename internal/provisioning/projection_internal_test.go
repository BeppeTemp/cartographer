package provisioning

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// A client with no subagents (Crush) receives a KB agent as a skill; Claude
// Code never does (D1656, WP4).

func agentArt(name, src string) contract.Artifact {
	files := []contract.ArtifactFile{{Path: name + ".md", Content: []byte(src)}}
	a := contract.Artifact{Kind: "agent", Name: name, Source: "kb:kb-a", ContentHash: ContentHashFiles(files), Files: files}
	a.Restriction = contract.ParseAgentRestriction(files[0].Content)
	return a
}

func skillArt(name, src string) contract.Artifact {
	files := []contract.ArtifactFile{{Path: "SKILL.md", Content: []byte(src)}}
	return contract.Artifact{Kind: "skill", Name: name, Source: "kb:kb-a", ContentHash: ContentHashFiles(files), Files: files}
}

func manifestOf(arts ...contract.Artifact) contract.Manifest {
	m := contract.Manifest{Artifacts: arts}
	m.Revision = contract.ComputeRevision(arts)
	return m
}

const (
	helperAgent = "---\nname: helper\ndescription: Helps with things\nmodel: opus\n---\nDo the work.\n"
	gatedAgent  = "---\nname: gated\ndescription: Runs a hook\nhooks:\n  Stop: []\n---\nDo.\n"
)

func projectedNames(m contract.Manifest) []string {
	var out []string
	for _, a := range m.Artifacts {
		out = append(out, a.Kind+"/"+a.Name)
	}
	return out
}

func TestProjectDegradesAgentWhereNoSubagents(t *testing.T) {
	m := manifestOf(agentArt("helper", helperAgent))
	pm, notes := ProjectManifest(m, configurator.ProviderCrush, contract.ScopeGlobal, contract.ProjectOptions{})
	if got := strings.Join(projectedNames(pm), ","); got != "skill/helper" {
		t.Fatalf("projected %q, want skill/helper", got)
	}
	d := pm.Artifacts[0]
	if d.DerivedFrom == nil || d.DerivedFrom.Kind != "agent" || d.DerivedFrom.ContentHash != m.Artifacts[0].ContentHash {
		t.Fatalf("DerivedFrom = %+v", d.DerivedFrom)
	}
	body := string(d.Files[0].Content)
	if !strings.HasPrefix(body, "---\nname: helper\ndescription: Helps with things\n---\n") || strings.Contains(body, "model:") {
		t.Errorf("frontmatter must carry name and description only:\n%s", body)
	}
	if !strings.Contains(body, contract.DegradedHeader) || !strings.HasSuffix(body, "Do the work.\n") {
		t.Errorf("header or body missing:\n%s", body)
	}
	if len(notes) != 1 || notes[0].Kind != contract.NoteDegraded || notes[0].Artifact != "agent/helper" {
		t.Errorf("notes = %+v", notes)
	}
	if pm.Revision == m.Revision {
		t.Error("the revision must describe the projection, not the source")
	}
}

func TestProjectClaudeCodeIsUnchanged(t *testing.T) {
	m := manifestOf(agentArt("helper", helperAgent), skillArt("s", "---\nname: s\ndescription: d\n---\nb\n"))
	pm, notes := ProjectManifest(m, configurator.ProviderClaudeCode, contract.ScopeGlobal, contract.ProjectOptions{})
	if len(notes) != 0 || pm.Revision != FilterForProvider(m, configurator.ProviderClaudeCode).Revision {
		t.Fatalf("Claude Code must project exactly what the filter kept: notes=%v", notes)
	}
	for _, a := range pm.Artifacts {
		if a.DerivedFrom != nil {
			t.Errorf("%s/%s derived on Claude Code", a.Kind, a.Name)
		}
	}
}

func TestProjectNeverDegradesStrictToolsAgent(t *testing.T) {
	m := manifestOf(agentArt("auditor", "---\nname: auditor\ndescription: Reads\ntools: Read\nstrict_tools: true\n---\nb\n"))
	pm, notes := ProjectManifest(m, configurator.ProviderCrush, contract.ScopeGlobal, contract.ProjectOptions{})
	if len(pm.Artifacts) != 0 || len(notes) != 0 {
		t.Fatalf("strict_tools agent must be skipped as today: %v %v", projectedNames(pm), notes)
	}
}

func TestProjectRealSkillWinsOverDegraded(t *testing.T) {
	m := manifestOf(agentArt("helper", helperAgent), skillArt("helper", "---\nname: helper\ndescription: Real\n---\nreal\n"))
	pm, notes := ProjectManifest(m, configurator.ProviderCrush, contract.ScopeGlobal, contract.ProjectOptions{})
	if len(pm.Artifacts) != 1 || pm.Artifacts[0].DerivedFrom != nil {
		t.Fatalf("the real skill must win: %v", projectedNames(pm))
	}
	if len(notes) != 1 || notes[0].Kind != contract.NoteDegradedSkipped {
		t.Errorf("notes = %+v", notes)
	}
}

func TestProjectAgentWithoutDescriptionIsSkippedAndReported(t *testing.T) {
	m := manifestOf(agentArt("nodesc", "---\nname: nodesc\n---\nb\n"))
	pm, notes := ProjectManifest(m, configurator.ProviderCrush, contract.ScopeGlobal, contract.ProjectOptions{})
	if len(pm.Artifacts) != 0 || len(notes) != 1 || notes[0].Kind != contract.NoteDegradedSkipped {
		t.Fatalf("got %v %+v", projectedNames(pm), notes)
	}
}

func TestProjectDerivedHashTracksAgentEdit(t *testing.T) {
	a1, _ := ProjectManifest(manifestOf(agentArt("helper", helperAgent)), configurator.ProviderCrush, contract.ScopeGlobal, contract.ProjectOptions{})
	a2, _ := ProjectManifest(manifestOf(agentArt("helper", helperAgent)), configurator.ProviderCrush, contract.ScopeGlobal, contract.ProjectOptions{})
	a3, _ := ProjectManifest(manifestOf(agentArt("helper", helperAgent+"more\n")), configurator.ProviderCrush, contract.ScopeGlobal, contract.ProjectOptions{})
	if a1.Artifacts[0].ContentHash != a2.Artifacts[0].ContentHash {
		t.Error("the derivation must be deterministic")
	}
	if a1.Artifacts[0].ContentHash == a3.Artifacts[0].ContentHash {
		t.Error("an agent edit must change the derived hash")
	}
}

func TestDegradedProvenanceNamesTheAgent(t *testing.T) {
	pm, _ := ProjectManifest(manifestOf(agentArt("helper", helperAgent)), configurator.ProviderCrush, contract.ScopeGlobal, contract.ProjectOptions{})
	d := pm.Artifacts[0]
	out := string(stampArtifact(d.Files[0].Content, d))
	for _, want := range []string{`Source: KB "kb-a", path agents/helper.md`, "Artifact content hash: " + d.DerivedFrom.ContentHash, "Derived by Cartographer as a skill on this client (no subagents)"} {
		if !strings.Contains(out, want) {
			t.Errorf("provenance lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "skills/helper") {
		t.Errorf("provenance must not point at a skill path that does not exist in the KB:\n%s", out)
	}
}

// Trap: the derived frontmatter has no hooks/MCP keys, so approval must be
// judged on the agent it came from, with the agent's hash.
func TestDegradedSkillInheritsAgentApproval(t *testing.T) {
	agent := agentArt("gated", gatedAgent)
	pm, _ := ProjectManifest(manifestOf(agent), configurator.ProviderCrush, contract.ScopeGlobal, contract.ProjectOptions{})
	d := pm.Artifacts[0]
	if !ArtifactRequiresApproval(d, ApplyOptions{}) {
		t.Fatal("a degraded gated agent must require approval")
	}
	if got := ApprovalName(d); got != "agent/gated" {
		t.Fatalf("ApprovalName = %q, want agent/gated", got)
	}
	base := t.TempDir()
	res, err := Apply(pm, ApplyOptions{Provider: configurator.ProviderCrush, BaseDir: base})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.NeedsApproval) != 1 || len(res.Written) != 0 {
		t.Fatalf("gated agent must stay needs_approval: %+v", res)
	}
	// What sync prints is what `approve <kind> <name>` looks up in the server's
	// manifest: the agent, not the derived skill.
	if h := res.NeedsApproval[0]; h.Kind != "agent" || h.Name != "gated" || h.ContentHash != agent.ContentHash {
		t.Fatalf("NeedsApproval must name the origin agent: %s/%s", h.Kind, h.Name)
	}
	opts := ApplyOptions{Provider: configurator.ProviderCrush, BaseDir: base, ApprovedMCP: map[string]string{"kb:kb-a\x00agent/gated": agent.ContentHash}}
	res, err = Apply(pm, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.NeedsApproval) != 0 || len(res.Written) != 1 || res.Written[0].DerivedFrom != "agent/gated" {
		t.Fatalf("approved agent must install: %+v", res)
	}
}

func applyProjected(t *testing.T, m contract.Manifest, p configurator.Provider, base string, lock Lock, dry bool) AppliedResult {
	t.Helper()
	pm, _ := ProjectManifest(m, p, contract.ScopeGlobal, contract.ProjectOptions{})
	res, err := Apply(pm, ApplyOptions{Provider: p, BaseDir: base, Lock: lock, DryRun: dry, ApprovedMCP: map[string]string{}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return res
}

func TestDegradedSkillLifecycle(t *testing.T) {
	p := configurator.ProviderCrush
	base := t.TempDir()
	m := manifestOf(agentArt("helper", helperAgent))

	dry := applyProjected(t, m, p, base, Lock{}, true)
	if len(dry.Written) != 1 {
		t.Fatalf("dry run reports one write, got %d", len(dry.Written))
	}
	if entries, _ := os.ReadDir(base); len(entries) != 0 {
		t.Fatalf("dry run wrote files: %v", entries)
	}

	res := applyProjected(t, m, p, base, Lock{}, false)
	if len(res.Written) != 1 || res.Written[0].Kind != "skill" || res.Written[0].DerivedFrom != "agent/helper" {
		t.Fatalf("first run: %+v", res.Written)
	}
	dest := filepath.Join(base, res.Written[0].Path)
	if b, err := os.ReadFile(dest); err != nil || !strings.Contains(string(b), contract.DegradedHeader) || !strings.Contains(string(b), "Derived by Cartographer") {
		t.Fatalf("degraded skill not written with header and provenance at %s: %v", dest, err)
	}

	again := applyProjected(t, m, p, base, res.NewLock, false)
	if len(again.Written) != 0 || len(again.Pruned) != 0 {
		t.Fatalf("second run must be idempotent: %+v", again)
	}

	m2 := manifestOf(agentArt("helper", helperAgent+"changed\n"))
	edit := applyProjected(t, m2, p, base, res.NewLock, false)
	if len(edit.Written) != 1 {
		t.Fatalf("an agent edit must rewrite the degraded skill: %+v", edit)
	}

	gone := applyProjected(t, manifestOf(), p, base, edit.NewLock, false)
	if len(gone.Pruned) != 1 {
		t.Fatalf("the agent leaving must prune the degraded skill: %+v", gone)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("degraded skill still on disk: %v", err)
	}
}

func TestDegradedSkillPrunedWhenClientGainsAgents(t *testing.T) {
	// The seam stands in for a client whose agent capability is absent, then
	// gains it: the degraded skill must leave and the native agent arrive.
	p2 := configurator.ProviderClaudeCode
	t.Cleanup(func() { contract.CapabilityOverride = nil })
	contract.CapabilityOverride = func(_ configurator.Provider, _ contract.Scope, id contract.Capability) (contract.CapStatus, bool) {
		if id == contract.CapAgentDelegate || id == contract.CapAgentInvoke {
			return contract.CapAbsent, true
		}
		return "", false
	}
	pm, notes := ProjectManifest(manifestOf(agentArt("helper", helperAgent)), p2, contract.ScopeGlobal, contract.ProjectOptions{})
	if len(notes) != 1 || notes[0].Kind != contract.NoteDegraded || pm.Artifacts[0].Kind != "skill" {
		t.Fatalf("a seam-injected client lacking agents degrades them: %v %+v", projectedNames(pm), notes)
	}
	base2 := t.TempDir()
	r1, err := Apply(pm, ApplyOptions{Provider: p2, BaseDir: base2, ApprovedMCP: map[string]string{}})
	if err != nil || len(r1.Written) != 1 || r1.Written[0].Kind != "skill" {
		t.Fatalf("degraded install: %v %+v", err, r1.Written)
	}
	contract.CapabilityOverride = nil
	pm2, notes2 := ProjectManifest(manifestOf(agentArt("helper", helperAgent)), p2, contract.ScopeGlobal, contract.ProjectOptions{})
	if len(notes2) != 0 || pm2.Artifacts[0].Kind != "agent" {
		t.Fatalf("capability gained: agent must be native again: %v", projectedNames(pm2))
	}
	r2, err := Apply(pm2, ApplyOptions{Provider: p2, BaseDir: base2, Lock: r1.NewLock, ApprovedMCP: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	var agentWritten bool
	for _, w := range r2.Written {
		if w.Kind == "agent" && w.DerivedFrom == "" {
			agentWritten = true
		}
	}
	if !agentWritten || len(r2.Pruned) == 0 {
		t.Fatalf("gain must write the native agent and prune the degraded skill: written=%+v pruned=%+v", r2.Written, r2.Pruned)
	}
	if _, err := os.Stat(filepath.Join(base2, r1.Written[0].Path)); !os.IsNotExist(err) {
		t.Errorf("degraded skill still on disk after the gain: %v", err)
	}
}

func TestDegradedWidenedWarning(t *testing.T) {
	m := manifestOf(agentArt("explorer", "---\nname: explorer\ndescription: Reads\ntools: Read, Grep\n---\nb\n"))
	res := applyProjected(t, m, configurator.ProviderCrush, t.TempDir(), Lock{}, false)
	var found bool
	for _, w := range res.Warnings {
		if strings.Contains(w, `agent "explorer"`) && strings.Contains(w, "degraded skill") {
			found = true
		}
	}
	if !found {
		t.Errorf("a degraded agent with tools must be reported as widened: %v", res.Warnings)
	}
}

// #1785: a hook the client carries globally but has no workspace destination
// for is reported, not dropped silently; a client with the cell is quiet.
func TestProjectReportsHookWithoutWorkspaceDestination(t *testing.T) {
	hook := contract.Artifact{Kind: "hook", Name: "guard", Source: "kb:kb-a"}
	m := manifestOf(hook)
	_, notes := ProjectManifest(m, configurator.ProviderKiro, contract.ScopeProject, contract.ProjectOptions{})
	if len(notes) != 1 || notes[0].Kind != contract.NoteSkipped || notes[0].Artifact != "hook/guard" ||
		!strings.Contains(notes[0].Reason, "no workspace destination") {
		t.Errorf("kiro project notes = %+v", notes)
	}
	if _, notes := ProjectManifest(m, configurator.ProviderKiro, contract.ScopeGlobal, contract.ProjectOptions{}); len(notes) != 0 {
		t.Errorf("global scope must be quiet: %+v", notes)
	}
	if _, notes := ProjectManifest(m, configurator.ProviderClaudeCode, contract.ScopeProject, contract.ProjectOptions{}); len(notes) != 0 {
		t.Errorf("claude has a project hook cell: %+v", notes)
	}
}
