package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

func TestStatusJSONSchemaAndStates(t *testing.T) {
	ready := true
	cases := []statusSnapshot{
		{Schema: statusSchema, State: "in_sync", Reachable: true, Ready: &ready, Providers: []providerStatus{{Name: "claude", Connected: true, State: "in_sync"}}},
		{Schema: statusSchema, State: "not_configured", Providers: []providerStatus{}},
		{Schema: statusSchema, State: "unavailable", Error: &statusError{Code: "unreachable", Cause: "dial"}, Providers: []providerStatus{{Name: "codex", Connected: true, State: "unknown"}}},
		{Schema: statusSchema, State: "drift", Providers: []providerStatus{{Name: "claude", Connected: true, State: "drift", Added: []statusArtifact{{Kind: "hook", Name: "x"}}}}},
		{Schema: statusSchema, State: "version_skew", Server: "v1", Client: "v2"},
		{Schema: statusSchema, State: "stopped", Service: &serviceSnapshot{Installed: true}},
	}
	for _, s := range cases {
		out := withStdout(t, func() { renderStatus("json", s, 0) })
		var got statusSnapshot
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("JSON contaminated: %v: %q", err, out)
		}
		if got.Schema != statusSchema {
			t.Fatalf("schema=%q", got.Schema)
		}
	}
}

func TestClassifyNetworkErrorAndOutputValidation(t *testing.T) {
	if got := classifyNetworkError("http://127.0.0.1:39273/mcp", errors.New("boom")); !strings.Contains(got.Message, "service status") {
		t.Fatalf("loopback hint: %+v", got)
	}
	if got := classifyNetworkError("https://x/mcp", client.ErrUnauthorized); got.Code != "unauthorized" {
		t.Fatalf("auth code: %+v", got)
	}
	for _, args := range [][]string{{"--output", "yaml"}, {"--output=yaml"}} {
		if _, _, err := outputFlag(args); err == nil {
			t.Fatalf("%v accepted", args)
		}
	}
	if code := cmdAgents([]string{"--output", "yaml"}); code != 2 {
		t.Fatalf("agents exit=%d", code)
	}
	if code := cmdServiceStatus([]string{"--output", "yaml"}); code != 2 {
		t.Fatalf("service exit=%d", code)
	}
}

func TestStatusTableRetainsDriftDetails(t *testing.T) {
	s := statusSnapshot{Schema: statusSchema, Reachable: true, Client: "v1", Server: "v1", ServerURL: "http://x/mcp", State: "drift", Providers: []providerStatus{{Name: "claude", Connected: true, State: "drift", Revision: "new", LockRevision: "old", Kinds: "skill 0/1", Added: []statusArtifact{{Kind: "hook", Name: "h", Source: "kb:x", Signed: false}}, Updated: []statusArtifact{{Kind: "skill", Name: "s", Source: "kb:x", Signed: true}}, Removed: []statusArtifact{{Kind: "agent", Name: "a", Path: "a.json"}}}}}
	out := withStdout(t, func() { renderStatus("table", s, 1) })
	for _, want := range []string{"drift (manifest new, lock old)", "skill 0/1", "+ hook/h [kb:x] trust=needs_approval", "new hook: the sync registers it itself in .claude/settings.json", "~ skill/s [kb:x] trust=verified", "- agent/a (a.json)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %s", want, out)
		}
	}
}

func TestSnapshotArtifactsMCPApprovalStates(t *testing.T) {
	cfg := clientconfig.Default()
	if err := cfg.ApproveMCP("kb", "approved", "same", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := cfg.ApproveMCP("kb", "stale", "old", time.Now()); err != nil {
		t.Fatal(err)
	}
	arts := snapshotArtifacts([]contract.Artifact{{Kind: "mcp", Name: "approved", Source: "kb:kb", ContentHash: "same"}, {Kind: "mcp", Name: "stale", Source: "kb:kb", ContentHash: "new"}, {Kind: "mcp", Name: "pending", Source: "kb:kb", ContentHash: "x"}, {Kind: "mcp", Name: "signed", Source: "kb:kb", ContentHash: "x", Signed: true}}, cfg)
	// A signed descriptor still needs approval (D906): a signature is not consent.
	want := []string{"approved", "approval_stale", "needs_approval", "needs_approval"}
	for i := range want {
		if arts[i].Trust != want[i] {
			t.Errorf("%s trust=%q want %q", arts[i].Name, arts[i].Trust, want[i])
		}
	}
}

func TestSnapshotArtifactsBuiltIn(t *testing.T) {
	cfg := clientconfig.Default()
	arts := snapshotArtifacts([]contract.Artifact{{Kind: "hook", Name: "bootstrap", Source: "kb:kb", BuiltIn: true}}, cfg)
	if got := arts[0].Trust; got != "built_in" {
		t.Errorf("trust=%q want built_in", got)
	}
}

// D267: a hook whose registered command cannot run says why, in the table and
// in the JSON, because "unrunnable" alone leaves the operator guessing.
func TestStatusReportsAnUnrunnableHookCommand(t *testing.T) {
	diverged := snapshotDiverged([]provisioning.DriftFinding{{
		Kind: "hook", Name: "cartographer-bootstrap", Path: ".claude/hooks/cartographer-bootstrap",
		Reason: provisioning.DriftUnrunnable, Detail: "command C:\\x\\bootstrap.cmd has backslashes, which the shell running it removes",
	}})
	if len(diverged) != 1 || diverged[0].Detail == "" {
		t.Fatalf("diverged = %+v, want the finding with its detail", diverged)
	}
	s := statusSnapshot{Schema: statusSchema, Reachable: true, State: "drift", Providers: []providerStatus{{Name: "claude", Connected: true, State: "drift", Diverged: diverged}}}
	out := withStdout(t, func() { renderStatus("table", s, 1) })
	for _, want := range []string{"diverged on disk (unrunnable): hook/cartographer-bootstrap", "has backslashes"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %s", want, out)
		}
	}
}

// The registration hint is the provider's own (#1012): never another client's
// file, and never an instruction to register by hand.
func TestStatusHookHintIsPerProvider(t *testing.T) {
	for _, tc := range []struct {
		provider, want string
	}{
		{"claude", ".claude/settings.json"},
		{"codex", ".codex/hooks.json"},
		{"opencode", "plugins/cartographer-h.js"},
		{"kiro", ".kiro/hooks/cartographer.json"},
		{"copilot", "cartographer-h.json"},
	} {
		s := statusSnapshot{Schema: statusSchema, Reachable: true, Client: "v1", Server: "v1", ServerURL: "http://x/mcp", State: "drift", Providers: []providerStatus{{Name: tc.provider, Connected: true, State: "drift", Revision: "n", LockRevision: "o", Added: []statusArtifact{{Kind: "hook", Name: "h", Source: "kb:x"}}, Updated: []statusArtifact{{Kind: "hook", Name: "h", Source: "kb:x"}}}}}
		out := withStdout(t, func() { renderStatus("table", s, 1) })
		if !strings.Contains(out, tc.want) {
			t.Errorf("%s: missing %q in %s", tc.provider, tc.want, out)
		}
		if strings.Contains(out, "manually") || (tc.provider != "claude" && strings.Contains(out, "settings.json")) {
			t.Errorf("%s: hint is Claude-specific or manual: %s", tc.provider, out)
		}
	}
}

// A skill derived from a gated agent is approved as the agent, against the
// agent's hash: Apply installs it on that approval, so status must not call it
// stale.
func TestSnapshotArtifactsDerivedSkillUsesAgentApproval(t *testing.T) {
	agent := contract.Artifact{Kind: "agent", Name: "gated", Source: "kb:kb", ContentHash: "agent-hash",
		Files: []contract.ArtifactFile{{Path: "gated.md", Content: []byte("---\nname: gated\ndescription: d\nhooks:\n  Stop: []\n---\nb\n")}}}
	pm, _ := provisioning.ProjectManifest(contract.Manifest{Artifacts: []contract.Artifact{agent}}, configurator.ProviderCrush, contract.ScopeGlobal, contract.ProjectOptions{})
	if len(pm.Artifacts) != 1 || pm.Artifacts[0].Kind != "skill" {
		t.Fatalf("setup: %v", pm.Artifacts)
	}
	cfg := &clientconfig.Config{}
	if err := cfg.ApproveMCP("kb", "agent/gated", "agent-hash", time.Now()); err != nil {
		t.Fatal(err)
	}
	got := snapshotArtifacts(pm.Artifacts, cfg)
	if got[0].Trust != "approved" || got[0].Kind != "skill" {
		t.Errorf("derived skill: trust %q kind %q, want approved skill", got[0].Trust, got[0].Kind)
	}
}

func TestFormatKindStatus(t *testing.T) {
	m := contract.Manifest{
		Revision: "rev1",
		Artifacts: []contract.Artifact{
			{Kind: "skill", Name: "s1", ContentHash: "h1"},
			{Kind: "skill", Name: "s2", ContentHash: "h2"},
			{Kind: "agent", Name: "a1", ContentHash: "h3"},
			{Kind: "agent", Name: "a2", ContentHash: "h4"},
			{Kind: "hook", Name: "hk1", ContentHash: "h5"},
		},
	}
	lock := provisioning.Lock{
		Managed: []provisioning.ManagedFile{
			{Kind: "skill", Name: "s1", Path: "x", ContentHash: "h1"},
			{Kind: "agent", Name: "a1", Path: "y", ContentHash: "h3"},
			{Kind: "agent", Name: "a2", Path: "z", ContentHash: "h4"},
		},
	}

	got := formatKindStatus(m, lock, false, "")
	want := "skill 1/2 · agent 2/2 · hook 0/1"
	if got != want {
		t.Errorf("formatKindStatus: got %q, want %q", got, want)
	}
}

// A hook installed for a client that fires it only in some sessions is not
// reported as a bare count (D189): the figure names where it fires (D300).
func TestFormatKindStatus_HookLimitQualifiesInstalledHooks(t *testing.T) {
	m := contract.Manifest{Artifacts: []contract.Artifact{{Kind: "hook", Name: "hk1", ContentHash: "h1"}}}
	lock := provisioning.Lock{Managed: []provisioning.ManagedFile{{Kind: "hook", Name: "hk1", Path: "x", ContentHash: "h1"}}}
	limit := provisioning.SessionHookLimit(configurator.ProviderKiro)
	if limit == "" {
		t.Fatal("kiro declares no session hook limit")
	}
	want := "hook 1/1 (fires in " + limit + " only)"
	if got := formatKindStatus(m, lock, false, limit); got != want {
		t.Errorf("formatKindStatus = %q, want %q", got, want)
	}
	if got := formatKindStatus(m, provisioning.Lock{}, false, limit); got != "hook 0/1" {
		t.Errorf("nothing installed must stay a bare count, got %q", got)
	}
}

func TestFormatKindStatus_Empty(t *testing.T) {
	if got := formatKindStatus(contract.Manifest{}, provisioning.Lock{}, false, ""); got != "" {
		t.Errorf("formatKindStatus on empty manifest: got %q, want \"\"", got)
	}
}

func TestFormatKindStatus_UnknownKindAppendedAlphabetically(t *testing.T) {
	m := contract.Manifest{
		Artifacts: []contract.Artifact{
			{Kind: "skill", Name: "s1", ContentHash: "h1"},
			{Kind: "zzz-future-kind", Name: "f1", ContentHash: "h2"},
		},
	}
	got := formatKindStatus(m, provisioning.Lock{}, false, "")
	want := "skill 0/1 · zzz-future-kind 0/1"
	if got != want {
		t.Errorf("formatKindStatus: got %q, want %q", got, want)
	}
}

func TestSetupHint(t *testing.T) {
	no, yes := false, true
	local := "http://127.0.0.1:39273/mcp"
	tests := []struct {
		name string
		s    statusSnapshot
		want bool
	}{
		{"fresh machine: nothing answers, no service", statusSnapshot{ServerURL: local}, true},
		{"service installed but down is a service problem, not a setup one", statusSnapshot{ServerURL: local, Service: &serviceSnapshot{Installed: true}}, false},
		{"server up with no KB", statusSnapshot{ServerURL: local, Reachable: true, Ready: &no}, true},
		{"set up", statusSnapshot{ServerURL: local, Reachable: true, Ready: &yes, KBs: []string{"a"}}, false},
		{"a remote server is not this machine's to set up", statusSnapshot{ServerURL: "https://kb.example.com/mcp"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := setupHint(&tc.s) != ""; got != tc.want {
				t.Errorf("setupHint = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSnapshotArtifacts_NeedsNetwork(t *testing.T) {
	mk := func(name, host string) contract.Artifact {
		return contract.Artifact{Kind: "mcp", Name: name, Source: "kb:kb", ContentHash: "h", Files: []contract.ArtifactFile{{Path: name + ".json",
			Content: []byte(`{"type":"http","url":"https://` + host + `/mcp","headers":{"A":"${X}"},"requires":"vpn"}`)}}}
	}
	cfg := &clientconfig.Config{}
	for _, n := range []string{"up", "down"} {
		if err := cfg.ApproveMCP("kb", n, "h", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	old := statusDial
	t.Cleanup(func() { statusDial = old })
	calls := 0
	statusDial = func(_ context.Context, u string, _ time.Duration) error {
		calls++
		if strings.Contains(u, "down.") {
			return context.DeadlineExceeded
		}
		return nil
	}
	got := snapshotArtifacts([]contract.Artifact{mk("up", "up.example.com"), mk("down", "down.example.com"), {Kind: "mcp", Name: "pending", Source: "kb:kb", ContentHash: "x", Files: mk("up", "p.example.com").Files}}, cfg)
	if got[0].Reach != "" || got[1].Reach != "needs_network" || got[1].Needs != "vpn" || got[2].Reach != "" {
		t.Errorf("got %+v", got)
	}
	if calls != 2 {
		t.Errorf("dials = %d, want 2 (only approved descriptors that declare a network)", calls)
	}
}
