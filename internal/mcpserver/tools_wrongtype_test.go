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

// TestWrongTypedArgumentNamesTheArgument: the error says the argument and the
// JSON type expected, never a Go struct field or a Go type.
func TestWrongTypedArgumentNamesTheArgument(t *testing.T) {
	t.Parallel()
	k := kbtest.New(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{BundleFS: fstest.MapFS{}})
	res := s.callTool(authLocalContext(), "log_tail", json.RawMessage(`{"n": "5"}`))
	got := res.Content[0].Text
	if !res.IsError || !strings.Contains(got, `argument "n" must be an integer (got string)`) ||
		strings.Contains(got, "Go ") {
		t.Fatalf("got %q", got)
	}
}

// TestGraphNeighborsRefusesAnUnknownConcept: an id that is no concept is an
// error, not an empty neighborhood.
func TestGraphNeighborsRefusesAnUnknownConcept(t *testing.T) {
	t.Parallel()
	k := kbtest.New(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{BundleFS: fstest.MapFS{}})
	res := s.callTool(authLocalContext(), "graph_neighbors", json.RawMessage(`{"id":"ops/nowhere"}`))
	if !res.IsError || !strings.Contains(res.Content[0].Text, "concept not found") {
		t.Fatalf("got %+v", res)
	}
}

// TestSearchShortQueryExplainsAnEmptyAnswer: a query under the 3-character
// minimum says why it found nothing.
func TestSearchShortQueryExplainsAnEmptyAnswer(t *testing.T) {
	t.Parallel()
	k := kbtest.New(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{BundleFS: fstest.MapFS{}})
	res := s.callTool(authLocalContext(), "search", json.RawMessage(`{"query":"db","record_miss":false}`))
	if res.IsError || !strings.Contains(res.Content[0].Text, "3 characters") {
		t.Fatalf("got %+v", res)
	}
}

// TestConceptNewWithoutAnyTemplateSaysSo: no empty "available templates:" list.
func TestConceptNewWithoutAnyTemplateSaysSo(t *testing.T) {
	t.Parallel()
	k := kbtest.New(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{BundleFS: fstest.MapFS{}})
	res := s.callTool(authLocalContext(), "concept_new", json.RawMessage(`{"template":"topic","id":"notes/x"}`))
	got := res.Content[0].Text
	if !res.IsError || !strings.Contains(got, "has no template yet") || strings.Contains(got, "truncated") {
		t.Fatalf("got %q", got)
	}
}

// TestServiceGetShortIDSaysTheFullID: a bare slug names the full id to use.
func TestServiceGetShortIDSaysTheFullID(t *testing.T) {
	t.Parallel()
	k := kbtest.New(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{BundleFS: fstest.MapFS{}})
	res := s.callTool(authLocalContext(), "service_get", json.RawMessage(`{"service_id":"kc2"}`))
	if !res.IsError || !strings.Contains(res.Content[0].Text, "full concept id") {
		t.Fatalf("got %+v", res)
	}
}
