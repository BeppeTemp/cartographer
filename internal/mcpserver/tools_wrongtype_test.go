package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/BeppeTemp/cartographer/internal/kbtest"
)

// TestHandlersRefuseWrongTypedArguments: the Unmarshal error of these handlers
// used to be dropped, so a wrong-typed argument ran the tool with its defaults
// (lint over the whole KB, a snapshot with the default message, ...).
func TestHandlersRefuseWrongTypedArguments(t *testing.T) {
	t.Parallel()
	k := kbtest.New(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{BundleFS: fstest.MapFS{}})
	for _, tc := range []struct{ tool, args string }{
		{"validate", `{"scope": 42}`},
		{"lint", `{"scope": 42}`},
		{"index_get", `{"path": 3}`},
		{"log_tail", `{"n": "5"}`},
		{"contradiction_report", `{"scope": 42}`},
		{"sync_check", `{"applied_revision": 123}`},
		{"snapshot", `{"message": 123}`},
	} {
		res := s.callTool(authLocalContext(), tc.tool, json.RawMessage(tc.args))
		if !res.IsError || !strings.Contains(res.Content[0].Text, "invalid params") {
			t.Errorf("%s %s: want invalid params, got %+v", tc.tool, tc.args, res)
		}
	}
	// No arguments at all still works (json.Unmarshal(nil) would fail).
	for _, tool := range []string{"validate", "lint", "index_get", "log_tail", "contradiction_report", "sync_check"} {
		res := s.callTool(authLocalContext(), tool, nil)
		if res.IsError && strings.Contains(res.Content[0].Text, "invalid params") {
			t.Errorf("%s with no arguments: %s", tool, res.Content[0].Text)
		}
	}
}

func TestWorkListHugeOffsetGivesEmptyPage(t *testing.T) {
	t.Parallel()
	s, _ := workServer(t)
	out := workCall(t, s, `{"offset": 9223372036854775807}`)
	if len(out.Entries) != 0 {
		t.Fatalf("entries = %d, want an empty page", len(out.Entries))
	}
	if out.NextOffset != nil {
		t.Fatalf("next_offset = %d, want none", *out.NextOffset)
	}
}
