package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
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
	for _, want := range []string{"drift (manifest new, lock old)", "skill 0/1", "+ hook/h [kb:x] trust=needs_approval", "new hook:", "~ skill/s [kb:x] trust=verified", "- agent/a (a.json)", "cartographer sync --auto-trust"} {
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
	arts := snapshotArtifacts([]provisioning.Artifact{{Kind: "mcp", Name: "approved", Source: "kb:kb", ContentHash: "same"}, {Kind: "mcp", Name: "stale", Source: "kb:kb", ContentHash: "new"}, {Kind: "mcp", Name: "pending", Source: "kb:kb", ContentHash: "x"}, {Kind: "mcp", Name: "verified", Source: "kb:kb", ContentHash: "x", Signed: true}}, cfg)
	want := []string{"approved", "approval_stale", "needs_approval", "verified"}
	for i := range want {
		if arts[i].Trust != want[i] {
			t.Errorf("%s trust=%q want %q", arts[i].Name, arts[i].Trust, want[i])
		}
	}
}

func TestSnapshotArtifactsBuiltIn(t *testing.T) {
	cfg := clientconfig.Default()
	arts := snapshotArtifacts([]provisioning.Artifact{{Kind: "hook", Name: "bootstrap", Source: "kb:kb", BuiltIn: true}}, cfg)
	if got := arts[0].Trust; got != "built_in" {
		t.Errorf("trust=%q want built_in", got)
	}
}
