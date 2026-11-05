package provisioning

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// ResolveProjections: requirements, the full chain, visibility (D1656, WP5).

func reqSkill(name string, reqs ...string) Artifact {
	return skillArt(name, "---\nname: "+name+"\ndescription: d\nrequires: ["+strings.Join(reqs, ", ")+"]\n---\nbody\n")
}

func reqAgent(name string, reqs ...string) Artifact {
	return agentArt(name, "---\nname: "+name+"\ndescription: d\nrequires: ["+strings.Join(reqs, ", ")+"]\n---\nbody\n")
}

func resolveOne(provider configurator.Provider, arts ...Artifact) ProjectionResult {
	in := ProjectionInput{Key: "p", Provider: provider, Scope: ScopeGlobal, Manifest: manifestOf(arts...)}
	return ResolveProjections([]ProjectionInput{in}, ProjectOptions{})["p"]
}

func noteFor(r ProjectionResult, id string) string {
	for _, n := range r.Notes {
		if n.Kind == NoteSkipped && n.Artifact == id {
			return n.Reason
		}
	}
	return ""
}

func TestResolveNoRequiresIsUntouched(t *testing.T) {
	m := manifestOf(skillArt("a", "---\nname: a\ndescription: d\n---\nb\n"), agentArt("helper", helperAgent))
	for _, p := range []configurator.Provider{configurator.ProviderClaudeCode, configurator.ProviderCrush} {
		want, _ := ProjectManifest(m, p, ScopeGlobal, ProjectOptions{})
		got := ResolveProjections([]ProjectionInput{{Key: "k", Provider: p, Scope: ScopeGlobal, Manifest: m}}, ProjectOptions{})["k"]
		if got.Manifest.Revision != want.Revision || len(got.Manifest.Artifacts) != len(want.Artifacts) {
			t.Errorf("%s: revision %s vs %s", p, got.Manifest.Revision, want.Revision)
		}
		for _, n := range got.Notes {
			if n.Kind == NoteSkipped {
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
	if r.Manifest.Revision != computeRevision(r.Manifest.Artifacts) {
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
	t.Cleanup(func() { capabilityOverride = nil })
	status := CapUnverified
	capabilityOverride = func(_ configurator.Provider, _ Scope, id Capability) (CapStatus, bool) {
		if id == CapAgentInvoke {
			return status, true
		}
		return "", false
	}
	s := reqSkill("s", "agent.invoke")
	if r := resolveOne(configurator.ProviderClaudeCode, s); len(r.Manifest.Artifacts) != 0 || noteFor(r, "skill/s") != "requires capability agent.invoke (it is unverified on this client)" {
		t.Errorf("unverified must not satisfy: %v %v", projectedNames(r.Manifest), r.Notes)
	}
	status = CapVerified
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

func TestResolveUnreadableRequiresWarnsAndInstalls(t *testing.T) {
	bad := skillArt("bad", "---\nname: bad\ndescription: d\nrequires: nope\n---\n")
	r := resolveOne(configurator.ProviderClaudeCode, bad)
	if len(r.Manifest.Artifacts) != 1 || len(r.Warnings) != 1 {
		t.Errorf("want installed with a warning: %v %v", projectedNames(r.Manifest), r.Warnings)
	}
}

func TestResolveVisibilityAcrossProjections(t *testing.T) {
	need := reqSkill("need", "skill:base")
	base := skillArt("base", "---\nname: base\ndescription: d\n---\n")
	mk := func(p configurator.Provider) map[string]ProjectionResult {
		return ResolveProjections([]ProjectionInput{
			{Key: "g", Provider: p, Scope: ScopeGlobal, Manifest: manifestOf(base)},
			{Key: "w", Provider: p, Scope: ScopeProject, Workspace: "/ws/repo", Manifest: manifestOf(need)},
		}, ProjectOptions{})
	}
	// The global projection is visible from a workspace.
	if r := mk(configurator.ProviderOpenCode)["w"]; len(r.Manifest.Artifacts) != 1 {
		t.Errorf("global projection must satisfy: %v", r.Notes)
	}
	// An ancestor workspace satisfies a skill requirement only where proven (OpenCode).
	anc := func(p configurator.Provider) ProjectionResult {
		return ResolveProjections([]ProjectionInput{
			{Key: "a", Provider: p, Scope: ScopeProject, Workspace: "/ws", Manifest: manifestOf(base)},
			{Key: "w", Provider: p, Scope: ScopeProject, Workspace: "/ws/repo", Manifest: manifestOf(need)},
		}, ProjectOptions{})["w"]
	}
	if r := anc(configurator.ProviderOpenCode); len(r.Manifest.Artifacts) != 1 {
		t.Errorf("opencode ancestor skill must satisfy: %v", r.Notes)
	}
	if r := anc(configurator.ProviderCodex); len(r.Manifest.Artifacts) != 0 {
		t.Errorf("an unproven ancestor must not satisfy: %v", projectedNames(r.Manifest))
	}
	// A sibling workspace is not an ancestor.
	r := ResolveProjections([]ProjectionInput{
		{Key: "a", Provider: configurator.ProviderOpenCode, Scope: ScopeProject, Workspace: "/ws/other", Manifest: manifestOf(base)},
		{Key: "w", Provider: configurator.ProviderOpenCode, Scope: ScopeProject, Workspace: "/ws/repo", Manifest: manifestOf(need)},
	}, ProjectOptions{})["w"]
	if len(r.Manifest.Artifacts) != 0 {
		t.Errorf("a sibling must not satisfy: %v", projectedNames(r.Manifest))
	}
	// A change in the global projection changes the workspace projection's revision.
	with := ResolveProjections([]ProjectionInput{
		{Key: "g", Provider: configurator.ProviderOpenCode, Scope: ScopeGlobal, Manifest: manifestOf(base)},
		{Key: "w", Provider: configurator.ProviderOpenCode, Scope: ScopeProject, Workspace: "/ws/repo", Manifest: manifestOf(need)},
	}, ProjectOptions{})["w"]
	without := ResolveProjections([]ProjectionInput{
		{Key: "g", Provider: configurator.ProviderOpenCode, Scope: ScopeGlobal, Manifest: manifestOf()},
		{Key: "w", Provider: configurator.ProviderOpenCode, Scope: ScopeProject, Workspace: "/ws/repo", Manifest: manifestOf(need)},
	}, ProjectOptions{})["w"]
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
