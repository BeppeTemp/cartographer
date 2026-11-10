package provisioning

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// ResolveProjections: requirements, the full chain, visibility (D1656, WP5).

func reqSkill(name string, reqs ...string) contract.Artifact {
	return skillArt(name, "---\nname: "+name+"\ndescription: d\nrequires: ["+strings.Join(reqs, ", ")+"]\n---\nbody\n")
}

func reqAgent(name string, reqs ...string) contract.Artifact {
	return agentArt(name, "---\nname: "+name+"\ndescription: d\nrequires: ["+strings.Join(reqs, ", ")+"]\n---\nbody\n")
}

func resolveOne(provider configurator.Provider, arts ...contract.Artifact) contract.ProjectionResult {
	in := contract.ProjectionInput{Key: "p", Provider: provider, Scope: contract.ScopeGlobal, Manifest: manifestOf(arts...)}
	return contract.ResolveProjections([]contract.ProjectionInput{in}, contract.ProjectOptions{})["p"]
}

func noteFor(r contract.ProjectionResult, id string) string {
	for _, n := range r.Notes {
		if n.Kind == contract.NoteSkipped && n.Artifact == id {
			return n.Reason
		}
	}
	return ""
}

func TestResolveNoRequiresIsUntouched(t *testing.T) {
	m := manifestOf(skillArt("a", "---\nname: a\ndescription: d\n---\nb\n"), agentArt("helper", helperAgent))
	for _, p := range []configurator.Provider{configurator.ProviderClaudeCode, configurator.ProviderCrush} {
		want, _ := ProjectManifest(m, p, contract.ScopeGlobal, contract.ProjectOptions{})
		got := contract.ResolveProjections([]contract.ProjectionInput{{Key: "k", Provider: p, Scope: contract.ScopeGlobal, Manifest: m}}, contract.ProjectOptions{})["k"]
		if got.Manifest.Revision != want.Revision || len(got.Manifest.Artifacts) != len(want.Artifacts) {
			t.Errorf("%s: revision %s vs %s", p, got.Manifest.Revision, want.Revision)
		}
		for _, n := range got.Notes {
			if n.Kind == contract.NoteSkipped {
				t.Errorf("%s: unexpected skip %+v", p, n)
			}
		}
	}
}

func TestResolveChainOfThree(t *testing.T) {
	// c requires a missing artifact; b requires c; a requires b.
	r := resolveOne(configurator.ProviderClaudeCode, reqSkill("a", "skill:b"), reqSkill("b", "skill:c"), reqSkill("c", "skill:gone"), skillArt("ok", "---\nname: ok\ndescription: d\n---\n"))
	if got := strings.Join(projectedNames(r.Manifest), ","); got != "skill/ok" {
		t.Fatalf("projected %q, want only skill/ok", got)
	}
	if got := noteFor(r, "skill/c"); got != "requires skill/gone (not provided to this client)" {
		t.Errorf("c: %q", got)
	}
	if got := noteFor(r, "skill/a"); got != "requires skill/b (skipped: requires skill/c (skipped: requires skill/gone (not provided to this client)))" {
		t.Errorf("a: %q", got)
	}
	if r.Manifest.Revision != contract.ComputeRevision(r.Manifest.Artifacts) {
		t.Error("revision not recomputed")
	}
}

func TestResolveCycle(t *testing.T) {
	r := resolveOne(configurator.ProviderClaudeCode, reqSkill("x", "skill:y"), reqSkill("y", "skill:x"))
	if len(r.Manifest.Artifacts) != 2 {
		t.Errorf("a cycle all present must stay: %v", projectedNames(r.Manifest))
	}
	r = resolveOne(configurator.ProviderClaudeCode, reqSkill("x", "skill:y"), reqSkill("y", "skill:x", "skill:gone"))
	if len(r.Manifest.Artifacts) != 0 {
		t.Errorf("a cycle with a missing member must go: %v", projectedNames(r.Manifest))
	}
}

func TestResolveRequirementOnDegradedAgentAndStrictAgent(t *testing.T) {
	// helper is degraded on Crush: skill:s requiring agent:helper is satisfied.
	r := resolveOne(configurator.ProviderCrush, agentArt("helper", helperAgent), reqSkill("s", "agent:helper"))
	if got := strings.Join(projectedNames(r.Manifest), ","); got != "skill/helper,skill/s" {
		t.Errorf("degraded agent must satisfy: %q", got)
	}
	// A strict_tools agent is skipped on Crush: the chain says why (gap c).
	strict := agentArt("auditor", "---\nname: auditor\ndescription: d\ntools: Read\nstrict_tools: true\n---\nb\n")
	r = resolveOne(configurator.ProviderCrush, strict, reqSkill("s", "agent:auditor"))
	if len(r.Manifest.Artifacts) != 0 {
		t.Fatalf("projected %v", projectedNames(r.Manifest))
	}
	if got := noteFor(r, "skill/s"); got != "requires agent/auditor (skipped: strict_tools)" {
		t.Errorf("chain: %q", got)
	}
	// Requirements of a degraded agent apply to its skill.
	r = resolveOne(configurator.ProviderCrush, reqAgent("helper", "skill:gone"))
	if len(r.Manifest.Artifacts) != 0 || noteFor(r, "agent/helper") == "" {
		t.Errorf("a degraded agent keeps its requirements: %v %v", projectedNames(r.Manifest), r.Notes)
	}
}

func TestResolveCapabilityNeedsVerified(t *testing.T) {
	t.Cleanup(func() { contract.CapabilityOverride = nil })
	status := contract.CapUnverified
	contract.CapabilityOverride = func(_ configurator.Provider, _ contract.Scope, id contract.Capability) (contract.CapStatus, bool) {
		if id == contract.CapAgentInvoke {
			return status, true
		}
		return "", false
	}
	s := reqSkill("s", "agent.invoke")
	if r := resolveOne(configurator.ProviderClaudeCode, s); len(r.Manifest.Artifacts) != 0 || noteFor(r, "skill/s") != "requires capability agent.invoke (it is unverified on this client)" {
		t.Errorf("unverified must not satisfy: %v %v", projectedNames(r.Manifest), r.Notes)
	}
	status = contract.CapVerified
	if r := resolveOne(configurator.ProviderClaudeCode, s); len(r.Manifest.Artifacts) != 1 {
		t.Errorf("verified must satisfy: %v", r.Notes)
	}
}

func TestResolveBundleSkillsAreNeverRemoved(t *testing.T) {
	b := reqSkill("b", "skill:gone")
	b.Source = "bundle"
	if r := resolveOne(configurator.ProviderClaudeCode, b); len(r.Manifest.Artifacts) != 1 {
		t.Errorf("bundle artifact removed: %v", r.Notes)
	}
}

func TestResolveUnreadableRequiresIsUnsatisfied(t *testing.T) {
	bad := skillArt("bad", "---\nname: bad\ndescription: d\nrequires: nope\n---\n")
	user := reqSkill("user", "skill:bad")
	r := resolveOne(configurator.ProviderClaudeCode, bad, user)
	if len(r.Manifest.Artifacts) != 0 {
		t.Fatalf("an unreadable requires must skip the artifact: %v", projectedNames(r.Manifest))
	}
	if got := noteFor(r, "skill/bad"); !strings.HasPrefix(got, "requires unreadable: ") {
		t.Errorf("cause: %q", got)
	}
	if got := noteFor(r, "skill/user"); !strings.HasPrefix(got, "requires skill/bad (skipped: requires unreadable: ") {
		t.Errorf("dependent: %q", got)
	}
}

func TestResolveLongChainIsFastAndBounded(t *testing.T) {
	const n = 2000
	var arts []contract.Artifact
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("s%d", i)
		if i == 0 {
			arts = append(arts, reqSkill(name, "skill:gone"))
		} else {
			arts = append(arts, reqSkill(name, fmt.Sprintf("skill:s%d", i-1)))
		}
	}
	start := time.Now()
	r := resolveOne(configurator.ProviderClaudeCode, arts...)
	if len(r.Manifest.Artifacts) != 0 {
		t.Fatalf("%d left", len(r.Manifest.Artifacts))
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v", d)
	}
	if got := noteFor(r, fmt.Sprintf("skill/s%d", n-1)); len(got) > 1000 || !strings.Contains(got, "...") {
		t.Errorf("cause chain not bounded (%d bytes)", len(got))
	}
	// The same chain with everything present is satisfied.
	arts[0] = skillArt("s0", "---\nname: s0\ndescription: d\n---\n")
	if r := resolveOne(configurator.ProviderClaudeCode, arts...); len(r.Manifest.Artifacts) != n {
		t.Errorf("satisfied chain lost artifacts: %d", len(r.Manifest.Artifacts))
	}
}

func TestResolveVisibilityAcrossProjections(t *testing.T) {
	need := reqSkill("need", "skill:base")
	base := skillArt("base", "---\nname: base\ndescription: d\n---\n")
	mk := func(p configurator.Provider) map[string]contract.ProjectionResult {
		return contract.ResolveProjections([]contract.ProjectionInput{
			{Key: "g", Provider: p, Scope: contract.ScopeGlobal, Manifest: manifestOf(base)},
			{Key: "w", Provider: p, Scope: contract.ScopeProject, Workspace: "/ws/repo", Manifest: manifestOf(need)},
		}, contract.ProjectOptions{})
	}
	// The global projection is visible from a workspace.
	if r := mk(configurator.ProviderOpenCode)["w"]; len(r.Manifest.Artifacts) != 1 {
		t.Errorf("global projection must satisfy: %v", r.Notes)
	}
	// An ancestor workspace satisfies a skill requirement only where proven (OpenCode).
	anc := func(p configurator.Provider) contract.ProjectionResult {
		return contract.ResolveProjections([]contract.ProjectionInput{
			{Key: "a", Provider: p, Scope: contract.ScopeProject, Workspace: "/ws", Manifest: manifestOf(base)},
			{Key: "w", Provider: p, Scope: contract.ScopeProject, Workspace: "/ws/repo", Manifest: manifestOf(need)},
		}, contract.ProjectOptions{})["w"]
	}
	if r := anc(configurator.ProviderOpenCode); len(r.Manifest.Artifacts) != 1 {
		t.Errorf("opencode ancestor skill must satisfy: %v", r.Notes)
	}
	if r := anc(configurator.ProviderCodex); len(r.Manifest.Artifacts) != 0 {
		t.Errorf("an unproven ancestor must not satisfy: %v", projectedNames(r.Manifest))
	}
	// A sibling workspace is not an ancestor.
	r := contract.ResolveProjections([]contract.ProjectionInput{
		{Key: "a", Provider: configurator.ProviderOpenCode, Scope: contract.ScopeProject, Workspace: "/ws/other", Manifest: manifestOf(base)},
		{Key: "w", Provider: configurator.ProviderOpenCode, Scope: contract.ScopeProject, Workspace: "/ws/repo", Manifest: manifestOf(need)},
	}, contract.ProjectOptions{})["w"]
	if len(r.Manifest.Artifacts) != 0 {
		t.Errorf("a sibling must not satisfy: %v", projectedNames(r.Manifest))
	}
	// A change in the global projection changes the workspace projection's revision.
	with := contract.ResolveProjections([]contract.ProjectionInput{
		{Key: "g", Provider: configurator.ProviderOpenCode, Scope: contract.ScopeGlobal, Manifest: manifestOf(base)},
		{Key: "w", Provider: configurator.ProviderOpenCode, Scope: contract.ScopeProject, Workspace: "/ws/repo", Manifest: manifestOf(need)},
	}, contract.ProjectOptions{})["w"]
	without := contract.ResolveProjections([]contract.ProjectionInput{
		{Key: "g", Provider: configurator.ProviderOpenCode, Scope: contract.ScopeGlobal, Manifest: manifestOf()},
		{Key: "w", Provider: configurator.ProviderOpenCode, Scope: contract.ScopeProject, Workspace: "/ws/repo", Manifest: manifestOf(need)},
	}, contract.ProjectOptions{})["w"]
	if with.Manifest.Revision == without.Manifest.Revision {
		t.Error("revision must follow the visible set")
	}
}

func TestResolveIsOrderIndependent(t *testing.T) {
	a := reqSkill("a", "skill:b")
	b := reqSkill("b", "skill:gone")
	r1 := resolveOne(configurator.ProviderClaudeCode, a, b)
	r2 := resolveOne(configurator.ProviderClaudeCode, b, a)
	if noteFor(r1, "skill/a") != noteFor(r2, "skill/a") || r1.Manifest.Revision != r2.Manifest.Revision {
		t.Errorf("order changed the outcome:\n%v\n%v", r1.Notes, r2.Notes)
	}
}
