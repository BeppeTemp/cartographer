package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/client"
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
	for _, want := range []string{"drift (manifest new, lock old)", "skill 0/1", "+ hook/h [kb:x] signed=false", "new hook:", "~ skill/s [kb:x] signed=true", "- agent/a (a.json)", "cartographer sync --auto-trust"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %s", want, out)
		}
	}
}
