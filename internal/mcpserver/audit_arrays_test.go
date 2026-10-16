package mcpserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/audit"
)

// artifact_move addresses its artifacts through an array, which the audit trail
// used to drop: the entry recorded the target KB and not what moved.
func TestAuditRecordsArtifactMovePaths(t *testing.T) {
	t.Parallel()
	s, _, path := auditServer(t, audit.Options{})
	s.RegisterTool(Tool{
		Name:        "artifact_move",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(requestContext, json.RawMessage) (ToolResult, error) {
			return textResult("moved"), nil
		},
	})
	callTool(t, s, "artifact_move", `{"target_kb":"kb-b","paths":["skills/s","hooks/guard","agents/a.md"],"reason":"free text"}`)
	entries := auditEntries(t, path)
	if len(entries) == 0 {
		t.Fatal("no audit entries")
	}
	for _, e := range entries {
		if e.Resources["target_kb"] != "kb-b" || e.Resources["paths"] != "skills/s, hooks/guard, agents/a.md" {
			t.Errorf("%s event Resources = %v, want target_kb and the joined paths", e.Phase, e.Resources)
		}
		if _, ok := e.Resources["reason"]; ok {
			t.Errorf("a field outside the allow-list reached the audit: %v", e.Resources)
		}
	}
}

func TestExtractResourcesStringArrays(t *testing.T) {
	t.Parallel()
	extract := func(args string) map[string]string {
		return extractResources("artifact_move", json.RawMessage(args))
	}
	t.Run("an array of strings is joined", func(t *testing.T) {
		got := extract(`{"paths":["a","b"]}`)
		if got["paths"] != "a, b" {
			t.Errorf("Resources = %v", got)
		}
	})
	t.Run("empty elements are skipped and an empty array is absent", func(t *testing.T) {
		if got := extract(`{"paths":["a","","b"]}`); got["paths"] != "a, b" {
			t.Errorf("Resources = %v", got)
		}
		if got := extract(`{"paths":[]}`); got != nil {
			t.Errorf("an empty array recorded %v", got)
		}
		if got := extract(`{"paths":[""]}`); got != nil {
			t.Errorf("an array of empty strings recorded %v", got)
		}
	})
	t.Run("an array that is not all strings is dropped", func(t *testing.T) {
		for _, args := range []string{`{"paths":[1,2]}`, `{"paths":["a",{"x":1}]}`, `{"paths":{"a":"b"}}`} {
			if got := extract(args); got != nil {
				t.Errorf("%s recorded %v", args, got)
			}
		}
	})
	t.Run("only the first items are kept and the rest is counted", func(t *testing.T) {
		items := make([]string, 30)
		for i := range items {
			items[i] = fmt.Sprintf("skills/s%02d", i)
		}
		raw, _ := json.Marshal(map[string]any{"paths": items})
		got := extract(string(raw))["paths"]
		if !strings.HasPrefix(got, "skills/s00, skills/s01") || !strings.Contains(got, "skills/s19") || strings.Contains(got, "skills/s20") || !strings.HasSuffix(got, "(+10 more)") {
			t.Errorf("paths = %q", got)
		}
	})
	t.Run("the value is bounded in bytes and stays valid UTF-8", func(t *testing.T) {
		long := strings.Repeat("é", 200)
		raw, _ := json.Marshal(map[string]any{"paths": []string{long, long, long}})
		got := extract(string(raw))["paths"]
		if len(got) > 512 || !utf8.ValidString(got) || !strings.HasSuffix(got, " more)") {
			t.Errorf("len %d valid %v: %q", len(got), utf8.ValidString(got), got)
		}
		raw, _ = json.Marshal(map[string]any{"paths": []string{strings.Repeat("é", 600)}})
		got = extract(string(raw))["paths"]
		if len(got) == 0 || len(got) > 512 || !utf8.ValidString(got) {
			t.Errorf("one oversized item: len %d valid %v", len(got), utf8.ValidString(got))
		}
	})
	t.Run("a tool without the field in its allow-list records no array", func(t *testing.T) {
		if got := extractResources("concept_read", json.RawMessage(`{"id":"a","paths":["x"]}`)); got["paths"] != "" {
			t.Errorf("a non-allow-listed array reached the audit: %v", got)
		}
	})
}
