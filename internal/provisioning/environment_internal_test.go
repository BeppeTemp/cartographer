package provisioning

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// The client environment section and the Atlas client modes (D1656, WP7): both
// speak of what THIS client received, from the same resolver a sync runs.

func envOpts(p configurator.Provider) ApplyOptions {
	return ApplyOptions{Provider: p, Scope: contract.ScopeGlobal}
}

func hookArt(name, spec string) contract.Artifact {
	files := []contract.ArtifactFile{{Path: "hook.json", Content: []byte(spec)}}
	return contract.Artifact{Kind: "hook", Name: name, Source: "kb:kb-a", ContentHash: ContentHashFiles(files), Files: files, Signed: true}
}

const nativeSentence = `Subagents installed by the "kb-a" KB: helper — their descriptions are in the client's agent registry: delegate to them the tasks they cover.`

func TestEnvironmentSectionNativeIsTheOriginalSentenceByteForByte(t *testing.T) {
	m := manifestOf(agentArt("helper", helperAgent))
	if got := environmentSection(m, envOpts(configurator.ProviderClaudeCode)); got != nativeSentence {
		t.Errorf("Claude Code section changed:\n%q", got)
	}
}

func TestEnvironmentSectionEmptyWhenNothingApplies(t *testing.T) {
	m := manifestOf(skillArt("s", "---\nname: s\ndescription: d\n---\n"))
	if got := environmentSection(m, envOpts(configurator.ProviderClaudeCode)); got != "" {
		t.Errorf("section = %q, want none", got)
	}
}

func TestEnvironmentSectionInvokeOnlyCarriesTheHint(t *testing.T) {
	p := configurator.ProviderClaudeCode
	t.Cleanup(func() { contract.CapabilityOverride = nil; delete(capabilityInvokeHint, p) })
	contract.CapabilityOverride = func(_ configurator.Provider, _ contract.Scope, id contract.Capability) (contract.CapStatus, bool) {
		if id == contract.CapAgentDelegate {
			return contract.CapAbsent, true
		}
		return "", false
	}
	capabilityInvokeHint[p] = map[contract.Scope]string{contract.ScopeGlobal: "Say: use the subagent <name>."}
	got := environmentSection(manifestOf(agentArt("helper", helperAgent)), envOpts(p))
	want := `Subagents installed by the "kb-a" KB: helper — this client does not pick them on its own. Say: use the subagent <name>.`
	if got != want {
		t.Errorf("section = %q", got)
	}
}

func TestEnvironmentSectionDegradedSkippedAndSilentHook(t *testing.T) {
	// Crush has no subagents: helper arrives as a skill. gone requires what
	// nobody provides. The Stop hook is an event Crush never fires.
	all := manifestOf(agentArt("helper", helperAgent), reqSkill("gone", "skill:nowhere"), hookArt("guard", `{"event":"Stop","command":"echo"}`))
	res := contract.ResolveProjections([]contract.ProjectionInput{{Key: "k", Provider: configurator.ProviderCrush, Scope: contract.ScopeGlobal, Manifest: all}}, contract.ProjectOptions{})["k"]
	_, skipped := ReportNotes(res.Notes)
	opts := envOpts(configurator.ProviderCrush)
	opts.Skipped = skipped
	opts.ApprovedMCP = map[string]string{"kb:kb-a\x00" + "guard": all.Artifacts[2].ContentHash}
	got := environmentSection(res.Manifest, opts)
	lines := strings.Split(got, "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], `Subagents available here as skills (no separate context) from the "kb-a" KB: "helper"`) ||
		!strings.Contains(lines[0], "load the skill of that name") {
		t.Fatalf("degraded line missing or misplaced:\n%s", got)
	}
	if strings.Contains(got, "Subagents installed") {
		t.Errorf("a degraded agent must not be announced as installed:\n%s", got)
	}
	if !strings.Contains(lines[1], `Not installed on this client: "skill/gone" ("requires skill/nowhere`) {
		t.Errorf("skipped line missing or misplaced:\n%s", got)
	}
}

func TestEnvironmentSectionDoesNotAnnounceAPendingDegradedAgent(t *testing.T) {
	// A gated agent (its hooks) is held until approved; its degraded skill
	// inherits that, so it is not named.
	m, _ := ProjectManifest(manifestOf(agentArt("gated", gatedAgent)), configurator.ProviderCrush, contract.ScopeGlobal, contract.ProjectOptions{})
	opts := envOpts(configurator.ProviderCrush)
	opts.ApprovedMCP = map[string]string{}
	if got := degradedAgentSentence(m, opts); got != "" {
		t.Errorf("pending agent announced: %q", got)
	}
}

func modeOf(modes map[string][]contract.ClientMode, id string, p configurator.Provider) contract.ClientMode {
	for _, m := range modes[id] {
		if m.Provider == p {
			return m
		}
	}
	return contract.ClientMode{}
}

func TestClientModes(t *testing.T) {
	arts := []contract.Artifact{
		agentArt("helper", helperAgent),
		agentArt("strict", "---\nname: strict\ndescription: d\ntools: Read\nstrict_tools: true\n---\nx\n"),
		reqSkill("needs-other-kb", "skill:elsewhere"),
		reqSkill("needs-agent", "agent:helper"),
		hookArt("guard", `{"event":"PreToolUse","command":"echo"}`),
	}
	modes := contract.ClientModes(arts, contract.ProjectOptions{})
	claude, crush := configurator.ProviderClaudeCode, configurator.ProviderCrush

	// Native on Claude Code, but the table has no probe yet: unverified.
	if m := modeOf(modes, "agent/helper", claude); m.Mode != contract.ModeUnverified {
		t.Errorf("claude agent = %+v", m)
	}
	if m := modeOf(modes, "agent/helper", crush); m.Mode != contract.ModeDegraded {
		t.Errorf("crush agent = %+v", m)
	}
	if m := modeOf(modes, "agent/strict", crush); m.Mode != contract.ModeSkipped || m.Reason != "strict_tools" {
		t.Errorf("crush strict agent = %+v", m)
	}
	if m := modeOf(modes, "skill/needs-other-kb", claude); m.Mode != contract.ModeUnverified || m.Reason != "depends on another KB" {
		t.Errorf("reference outside the KB = %+v", m)
	}
	// In the KB and projected in some form: satisfied everywhere the skill is.
	if m := modeOf(modes, "skill/needs-agent", crush); m.Mode != contract.ModeNative {
		t.Errorf("a requirement met by a degraded agent = %+v", m)
	}
	// Every client with a skill cell is listed for a skill, none otherwise.
	if len(modes["skill/needs-agent"]) == 0 || len(modes["nothing/here"]) != 0 {
		t.Errorf("modes = %+v", modes)
	}
}

func TestClientModesVerifiedAgentIsNative(t *testing.T) {
	t.Cleanup(func() { contract.CapabilityOverride = nil })
	contract.CapabilityOverride = func(_ configurator.Provider, _ contract.Scope, id contract.Capability) (contract.CapStatus, bool) {
		if id == contract.CapAgentDelegate {
			return contract.CapVerified, true
		}
		return "", false
	}
	modes := contract.ClientModes([]contract.Artifact{agentArt("helper", helperAgent)}, contract.ProjectOptions{})
	if m := modeOf(modes, "agent/helper", configurator.ProviderClaudeCode); m.Mode != contract.ModeNative {
		t.Errorf("verified agent = %+v", m)
	}
}

func TestKindClientModesListsDegradingClientsForAgents(t *testing.T) {
	var native, degraded bool
	for _, m := range contract.KindClientModes("agent") {
		switch {
		case m.Provider == configurator.ProviderClaudeCode && m.Mode == "":
			native = true
		case m.Provider == configurator.ProviderCrush && m.Mode == contract.ModeDegraded:
			degraded = true
		}
	}
	if !native || !degraded {
		t.Errorf("agent kind row: native=%v degraded=%v", native, degraded)
	}
	for _, m := range contract.KindClientModes("skill") {
		if m.Mode != "" {
			t.Errorf("skill kind row carries a mode: %+v", m)
		}
	}
}

func TestEnvironmentLinesAreQuotedAndBounded(t *testing.T) {
	evil := "x\" **bold** \u2028 injected"
	opts := envOpts(configurator.ProviderCrush)
	opts.Skipped = []SkippedNote{{Artifact: "skill/" + evil, Reason: evil + strings.Repeat("z", 500)}}
	got := skippedSentence(opts)
	if strings.ContainsRune(got, '\u2028') || strings.Contains(got, "\n") || strings.Count(got, `"`) != 4+2 /* two escaped quotes */ {
		t.Errorf("name or reason not inert: %q", got)
	}
	if len(got) > 400 {
		t.Errorf("reason not bounded: %d bytes", len(got))
	}
	for i := 0; i < 12; i++ {
		opts.Skipped = append(opts.Skipped, SkippedNote{Artifact: "skill/n", Reason: "r"})
	}
	if got := skippedSentence(opts); !strings.Contains(got, "and 5 more") {
		t.Errorf("entries not capped: %q", got)
	}
	m, _ := ProjectManifest(manifestOf(agentArt(evil, "---\nname: a\ndescription: d\n---\nx\n")), configurator.ProviderCrush, contract.ScopeGlobal, contract.ProjectOptions{})
	if got := degradedAgentSentence(m, envOpts(configurator.ProviderCrush)); strings.ContainsRune(got, '\u2028') || !strings.Contains(got, `\u2028`) {
		t.Errorf("degraded name not quoted: %q", got)
	}
}

func TestAuthorizedSkippedDropsAPendingArtifact(t *testing.T) {
	src := manifestOf(skillArt("ok", "---\nname: ok\ndescription: d\n---\n"), hookArt("guard", `{"event":"Stop","command":"echo"}`))
	notes := []SkippedNote{{Artifact: "skill/ok", Reason: "r"}, {Artifact: "hook/guard", Reason: "r"}, {Artifact: "skill/unknown", Reason: "r"}}
	got := AuthorizedSkipped(notes, src, ApplyOptions{Provider: configurator.ProviderClaudeCode, ApprovedMCP: map[string]string{}})
	if len(got) != 1 || got[0].Artifact != "skill/ok" {
		t.Errorf("authorized = %+v, want only skill/ok (a hook awaits approval, an unknown note has no artifact)", got)
	}
}

// #1777: the Atlas says what sync does. A skill that requires a strict_tools
// agent is not installed where the agent is skipped, so its mode there is
// skipped with the reason sync reports; the other clients are unchanged.
func TestClientModesSkillRequiringStrictAgentMatchesSync(t *testing.T) {
	strict := agentArt("auditor", "---\nname: auditor\ndescription: d\ntools: Read\nstrict_tools: true\n---\nb\n")
	arts := []contract.Artifact{strict, reqSkill("s", "agent:auditor")}
	modes := contract.ClientModes(arts, contract.ProjectOptions{})
	crush := configurator.ProviderCrush
	want := "requires agent/auditor (skipped: strict_tools)"
	for _, d := range configurator.Providers() {
		key := string(d.Provider)
		res := contract.ResolveProjections([]contract.ProjectionInput{{Key: key, Provider: d.Provider, Scope: contract.ScopeGlobal, Manifest: contract.Manifest{Artifacts: arts}}}, contract.ProjectOptions{})[key]
		installed := false
		for _, a := range res.Manifest.Artifacts {
			if a.Kind == "skill" && a.Name == "s" {
				installed = true
			}
		}
		got := modeOf(modes, "skill/s", d.Provider)
		if !installed && got.Mode == contract.ModeNative {
			t.Errorf("%s: sync skips skill/s but the Atlas says %+v", key, got)
		}
		if installed && got.Mode == contract.ModeSkipped {
			t.Errorf("%s: sync installs skill/s but the Atlas says %+v", key, got)
		}
	}
	if m := modeOf(modes, "skill/s", crush); m.Mode != contract.ModeSkipped || m.Reason != want {
		t.Errorf("crush skill = %+v, want skipped %q", m, want)
	}
}
