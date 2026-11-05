package provisioning

import (
	"reflect"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

func TestReportNotesSplitsDegradedAndSkipped(t *testing.T) {
	d, s := ReportNotes([]ProjectionNote{
		{NoteDegraded, "agent/x", "agent -> skill"},
		{NoteDegradedSkipped, "agent/c", "a skill of that name exists"},
		{NoteSkipped, "skill/y", "requires agent/x (skipped: strict_tools)"},
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
	t.Cleanup(func() { capabilityOverride = nil })
	capabilityOverride = func(_ configurator.Provider, _ Scope, id Capability) (CapStatus, bool) {
		switch id {
		case CapAgentDelegate, CapAgentInvoke, "hook.Stop":
			return CapUnverified, true
		}
		return CapVerified, true
	}
	hookFiles := []ArtifactFile{{Path: "hook.json", Content: []byte(`{"event":"Stop","command":"./run.sh"}`)}, {Path: "run.sh", Content: []byte("#!/bin/sh\n"), Executable: true}}
	hook := Artifact{Kind: "hook", Name: "stopper", Source: "kb:kb-a", ContentHash: ContentHashFiles(hookFiles), Files: hookFiles}

	// An agent and a hook not yet approved are not installed: nothing to report.
	m := manifestOf(agentArt("helper", helperAgent), hook)
	res, err := Apply(m, ApplyOptions{Provider: p, BaseDir: t.TempDir(), ApprovedMCP: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []Capability{CapAgentDelegate, CapAgentInvoke}; !reflect.DeepEqual(res.Unverified, want) {
		t.Errorf("unapproved hook must not count: Unverified = %v, want %v", res.Unverified, want)
	}

	approved := map[string]string{hook.Source + "\x00" + ApprovalName(hook): hook.ContentHash}
	res, err = Apply(m, ApplyOptions{Provider: p, BaseDir: t.TempDir(), ApprovedMCP: approved})
	if err != nil {
		t.Fatal(err)
	}
	if want := []Capability{CapAgentDelegate, CapAgentInvoke, "hook.Stop"}; !reflect.DeepEqual(res.Unverified, want) {
		t.Errorf("Unverified = %v, want %v", res.Unverified, want)
	}

	// A verified cell is not reported; a skill alone reports nothing.
	capabilityOverride = func(_ configurator.Provider, _ Scope, id Capability) (CapStatus, bool) {
		return CapVerified, true
	}
	res, err = Apply(m, ApplyOptions{Provider: p, BaseDir: t.TempDir(), ApprovedMCP: approved})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Unverified) != 0 {
		t.Errorf("verified cells must not be reported: %v", res.Unverified)
	}
}
