package provisioning

import (
	"reflect"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

func TestReportNotesSplitsDegradedAndSkipped(t *testing.T) {
	d, s := ReportNotes([]contract.ProjectionNote{
		{Kind: contract.NoteDegraded, Artifact: "agent/x", Reason: "agent -> skill"},
		{Kind: contract.NoteDegradedSkipped, Artifact: "agent/c", Reason: "a skill of that name exists"},
		{Kind: contract.NoteSkipped, Artifact: "skill/y", Reason: "requires agent/x (skipped: strict_tools)"},
	})
	if want := []DegradedNote{{Artifact: "agent/x", As: "skill/x"}}; !reflect.DeepEqual(d, want) {
		t.Errorf("degraded = %+v, want %+v", d, want)
	}
	want := []SkippedNote{
		{Artifact: "agent/c", Reason: "not installed as a skill (a skill of that name exists)"},
		{Artifact: "skill/y", Reason: "requires agent/x (skipped: strict_tools)"},
	}
	if !reflect.DeepEqual(s, want) {
		t.Errorf("skipped = %+v, want %+v", s, want)
	}
	if d, s := ReportNotes(nil); d != nil || s != nil {
		t.Errorf("no notes must give nothing: %v %v", d, s)
	}
}

func TestApplyReportsUnverifiedCapabilitiesOfInstalledArtifacts(t *testing.T) {
	p := configurator.ProviderClaudeCode
	t.Cleanup(func() { contract.CapabilityOverride = nil })
	contract.CapabilityOverride = func(_ configurator.Provider, _ contract.Scope, id contract.Capability) (contract.CapStatus, bool) {
		switch id {
		case contract.CapAgentDelegate, contract.CapAgentInvoke, "hook.Stop":
			return contract.CapUnverified, true
		}
		return contract.CapVerified, true
	}
	hookFiles := []contract.ArtifactFile{{Path: "hook.json", Content: []byte(`{"event":"Stop","command":"./run.sh"}`)}, {Path: "run.sh", Content: []byte("#!/bin/sh\n"), Executable: true}}
	hook := contract.Artifact{Kind: "hook", Name: "stopper", Source: "kb:kb-a", ContentHash: ContentHashFiles(hookFiles), Files: hookFiles}

	// An agent and a hook not yet approved are not installed: nothing to report.
	m := manifestOf(agentArt("helper", helperAgent), hook)
	res, err := Apply(m, ApplyOptions{Provider: p, BaseDir: t.TempDir(), ApprovedMCP: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []contract.Capability{contract.CapAgentDelegate, contract.CapAgentInvoke}; !reflect.DeepEqual(res.Unverified, want) {
		t.Errorf("unapproved hook must not count: Unverified = %v, want %v", res.Unverified, want)
	}

	approved := map[string]string{hook.Source + "\x00" + ApprovalName(hook): hook.ContentHash}
	res, err = Apply(m, ApplyOptions{Provider: p, BaseDir: t.TempDir(), ApprovedMCP: approved})
	if err != nil {
		t.Fatal(err)
	}
	if want := []contract.Capability{contract.CapAgentDelegate, contract.CapAgentInvoke, "hook.Stop"}; !reflect.DeepEqual(res.Unverified, want) {
		t.Errorf("Unverified = %v, want %v", res.Unverified, want)
	}

	// A verified cell is not reported; a skill alone reports nothing.
	contract.CapabilityOverride = func(_ configurator.Provider, _ contract.Scope, id contract.Capability) (contract.CapStatus, bool) {
		return contract.CapVerified, true
	}
	res, err = Apply(m, ApplyOptions{Provider: p, BaseDir: t.TempDir(), ApprovedMCP: approved})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Unverified) != 0 {
		t.Errorf("verified cells must not be reported: %v", res.Unverified)
	}
}
