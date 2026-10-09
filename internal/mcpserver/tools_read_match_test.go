package mcpserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type matchResp struct {
	ID      string `json:"id"`
	Match   string `json:"match"`
	Rev     string `json:"rev"`
	Matches []struct {
		Line  int    `json:"line"`
		Start int    `json:"start"`
		Text  string `json:"text"`
	} `json:"matches"`
	Omitted *int `json:"matches_omitted"`
}

func readMatch(t *testing.T, c *changesLinksKB, args string) (matchResp, ToolResult) {
	t.Helper()
	res := c.s.callTool(authLocalContext(), "concept_read", json.RawMessage(args))
	var out matchResp
	if !res.IsError {
		if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
			t.Fatal(err)
		}
	}
	return out, res
}

func TestConceptRead_Match(t *testing.T) {
	c := newChangesLinksKB(t)
	c.write("ops/grep", "one\nTwo Needle\nthree\nfour\nfive\nsix\nseven needle\neight\n")

	got, res := readMatch(t, c, `{"id":"ops/grep","match":"needle"}`)
	if res.IsError || len(got.Matches) != 2 {
		t.Fatalf("want 2 matches (case-insensitive), got %+v / %+v", got, res)
	}
	if m := got.Matches[0]; m.Line != 2 || m.Start != 2 || m.Text != "Two Needle" {
		t.Errorf("first match = %+v", m)
	}

	got, _ = readMatch(t, c, `{"id":"ops/grep","match":"needle","context":1}`)
	if m := got.Matches[0]; m.Line != 2 || m.Start != 1 || m.Text != "one\nTwo Needle\nthree" {
		t.Errorf("context window = %+v", m)
	}
	if len(got.Matches) != 2 {
		t.Errorf("distant windows must stay separate: %+v", got.Matches)
	}

	// Windows that overlap or touch merge; line stays the first hit.
	got, _ = readMatch(t, c, `{"id":"ops/grep","match":"needle","context":2}`)
	if len(got.Matches) != 1 || got.Matches[0].Line != 2 || got.Matches[0].Start != 1 || !strings.HasSuffix(got.Matches[0].Text, "eight\n") {
		t.Errorf("merged window = %+v", got.Matches)
	}

	// Context above the cap is clamped, not an error.
	if _, res := readMatch(t, c, `{"id":"ops/grep","match":"needle","context":999}`); res.IsError {
		t.Errorf("large context refused: %+v", res)
	}

	got, res = readMatch(t, c, `{"id":"ops/grep","match":"absent"}`)
	if res.IsError || got.Matches == nil || len(got.Matches) != 0 {
		t.Errorf("no hit must be matches: [], got %+v / %+v", got, res)
	}
	if !strings.Contains(res.Content[0].Text, `"matches": []`) {
		t.Errorf("matches must serialize as []: %s", res.Content[0].Text)
	}
}

func TestConceptRead_MatchErrors(t *testing.T) {
	c := newChangesLinksKB(t)
	c.write("ops/grep", "# H\n\nx\n")
	if _, res := readMatch(t, c, `{"id":"ops/grep","match":""}`); !res.IsError || !strings.Contains(res.Content[0].Text, "'match' must not be empty") {
		t.Errorf("empty match: %+v", res)
	}
	for _, other := range []string{`"section":"# H"`, `"outline":true`, `"with_content":true`} {
		_, res := readMatch(t, c, `{"id":"ops/grep","match":"x",`+other+`}`)
		name := strings.Trim(strings.SplitN(other, ":", 2)[0], `"`)
		if !res.IsError || !strings.Contains(res.Content[0].Text, "'match' is exclusive with '"+name+"'") {
			t.Errorf("match + %s: %+v", name, res)
		}
	}
}

func TestConceptRead_MatchCapAndGuard(t *testing.T) {
	c := newChangesLinksKB(t)
	var b strings.Builder
	for i := 0; i < 70; i++ {
		fmt.Fprintf(&b, "hit %d\n\n", i)
	}
	// Pad the page over the size guard: match must still answer.
	b.WriteString(strings.Repeat("filler line\n", conceptReadSizeGuard/12+10))
	c.write("ops/big", b.String())

	got, res := readMatch(t, c, `{"id":"ops/big","match":"hit "}`)
	if res.IsError || len(got.Matches) != matchMaxEntries || got.Omitted == nil || *got.Omitted != 20 {
		t.Fatalf("cap: %d entries, omitted %v, %+v", len(got.Matches), got.Omitted, res.IsError)
	}
}

func TestConceptRead_MatchAtRev(t *testing.T) {
	c := newChangesLinksKB(t)
	c.k.AutoCommit = true
	c.write("ops/page", "alpha old\n")
	c.commit(true)
	c.write("ops/page", "alpha new\n")
	c.commit(true)
	h, _ := c.history(`{"id":"ops/page"}`)
	got, res := readMatch(t, c, `{"id":"ops/page","match":"old","rev":"`+h.Revisions[1].SHA+`"}`)
	if res.IsError || len(got.Matches) != 1 || got.Rev != h.Revisions[1].SHA {
		t.Errorf("match at rev: %+v / %+v", got, res)
	}
	if got, _ := readMatch(t, c, `{"id":"ops/page","match":"old"}`); len(got.Matches) != 0 {
		t.Errorf("current version has no 'old': %+v", got)
	}
}
