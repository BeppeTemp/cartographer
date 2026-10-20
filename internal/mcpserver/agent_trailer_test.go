package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// asClient returns ctx as seen by a tool call from the named MCP client.
func asClient(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, callerClientKey{}, name)
}

func writeAs(t *testing.T, s *Server, ctx context.Context, id string, extra map[string]any) {
	t.Helper()
	args := map[string]any{"id": id, "frontmatter": map[string]any{"type": "Note", "title": id}, "body": "# T\n\n[[hub/anchor]]\n"}
	for key, v := range extra {
		args[key] = v
	}
	raw, _ := json.Marshal(args)
	if res := s.callTool(ctx, "concept_write", raw); res.IsError {
		t.Fatalf("concept_write %s: %+v", id, res)
	}
}

func headTrailers(t *testing.T, dir, key string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "log", "-1", "--format=%(trailers:key="+key+",valueonly,separator=|)").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func headMessage(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "log", "-1", "--format=%B").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimRight(string(out), "\n")
}

func TestGitWrap_AgentTrailer(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	// A page of an ad hoc map needs a concept to link out to (D907 orphan).
	fm, _ := okf.ParseFrontmatter("type: Note\ntitle: Anchor")
	if _, err := k.WriteConcept("test/anchor", fm, "# Anchor\n", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOp("test: seed"); err != nil {
		t.Fatal(err)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	linked := map[string]any{"body": "# T\n\nSee [[test/anchor]].\n"}

	// (a) the caller's clientInfo name becomes an Agent trailer.
	writeAs(t, s, asClient(authLocalContext(), "client-a"), "test/a", linked)
	if got := headTrailers(t, k.Root, "Agent"); got != "client-a" {
		t.Errorf("Agent trailer = %q, want client-a", got)
	}

	// (c) no client: the message is exactly what it was before this trailer.
	writeAs(t, s, authLocalContext(), "test/none", linked)
	if got, want := headMessage(t, k.Root), "concept_write: test/none\n\n"+tokenTrailer; got != want {
		t.Errorf("no client: message = %q, want %q", got, want)
	}
	writeAs(t, s, asClient(authLocalContext(), " \n\t "), "test/blank", linked)
	if got := headMessage(t, k.Root); strings.Contains(got, "Agent") {
		t.Errorf("blank client name: message = %q, want no Agent trailer", got)
	}

	// (d) a newline in the name must not forge a trailer.
	writeAs(t, s, asClient(authLocalContext(), "client-a\nReason: forged\nAgent: other"), "test/forge", linked)
	if got := headTrailers(t, k.Root, "Reason"); got != "" {
		t.Errorf("forged Reason trailer = %q", got)
	}
	if got := headTrailers(t, k.Root, "Agent"); got != "client-a Reason: forged Agent: other" {
		t.Errorf("Agent trailer = %q, want one normalised line", got)
	}

	// (e) a long multibyte name is cut on a rune boundary within the bound.
	writeAs(t, s, asClient(authLocalContext(), strings.Repeat("é", 100)), "test/long", linked)
	got := headTrailers(t, k.Root, "Agent")
	if len(got) == 0 || len(got) > maxClientFieldLen || !utf8Valid(got) {
		t.Errorf("long Agent = %d bytes %q, want 1..%d valid bytes", len(got), got, maxClientFieldLen)
	}
}

func TestGitWrap_AgentTrailerOrder(t *testing.T) {
	t.Parallel()
	// (b) Reason, Agent and the token in one trailer paragraph.
	k := gateKB(t)
	s := gateSeed(t, k, map[string]string{"ops/p": draftPage})
	mv := `{"source_id":"ops/p","target_id":"ops/p2","reason":"why"}`
	if text, isErr := callJSON(t, s, asClient(authLocalContext(), "client-a"), "concept_move", mv); isErr {
		t.Fatalf("move: %s", text)
	}
	out, err := exec.Command("git", "-C", k.Root, "log", "-1", "--format=%B").Output()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "interpret-trailers", "--parse")
	cmd.Stdin = strings.NewReader(string(out))
	parsed, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	want := "Reason: why\nAgent: client-a\n" + tokenTrailer + "\n"
	if string(parsed) != want {
		t.Errorf("trailers = %q, want %q", parsed, want)
	}
}

func TestGitWrap_ExternalChangesCommitHasNoAgent(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	// Leave an uncommitted file so the next write commits it first (D357).
	c := &changesLinksKB{t: t, k: k}
	c.write("test/stray", "x\n")
	writeAs(t, s, asClient(authLocalContext(), "client-a"), "test/after", nil)
	subj := func(rev string) string {
		out, err := exec.Command("git", "-C", k.Root, "log", "-1", "--format=%s", rev).Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	agentOf := func(rev string) string {
		out, err := exec.Command("git", "-C", k.Root, "log", "-1", "--format=%(trailers:key=Agent,valueonly)", rev).Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	if subj("HEAD~1") != externalChangesSubject || agentOf("HEAD~1") != "" {
		t.Errorf("HEAD~1 = %q agent %q, want the external changes commit without an Agent", subj("HEAD~1"), agentOf("HEAD~1"))
	}
	if agentOf("HEAD") != "client-a" {
		t.Errorf("HEAD agent = %q, want client-a", agentOf("HEAD"))
	}
}

func TestChangesSince_AgentsAndHistory(t *testing.T) {
	t.Parallel()
	c := newChangesLinksKB(t)
	a := asClient(authLocalContext(), "client-a")
	b := asClient(authLocalContext(), "client-b")
	writeAs(t, c.s, a, "ops/page", nil)
	writeAs(t, c.s, b, "ops/page", map[string]any{"body": "# V2\n"})
	writeAs(t, c.s, authLocalContext(), "ops/page", map[string]any{"body": "# V3\n"})
	writeAs(t, c.s, a, "other/page", nil)

	since := fmt.Sprintf(`{"since":%q}`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339))
	type result struct {
		Concepts []struct {
			ID     string   `json:"id"`
			Agents []string `json:"agents"`
		} `json:"concepts"`
		ByAgent map[string]int `json:"commits_by_agent"`
	}
	run := func(ctx context.Context) (result, string) {
		text, isErr := callJSON(t, c.s, ctx, "changes_since", since)
		if isErr {
			t.Fatalf("changes_since: %s", text)
		}
		var r result
		if err := json.Unmarshal([]byte(text), &r); err != nil {
			t.Fatal(err)
		}
		return r, text
	}

	r, _ := run(authLocalContext())
	for _, con := range r.Concepts {
		if con.ID == "ops/page" && strings.Join(con.Agents, ",") != "client-b,client-a" {
			t.Errorf("ops/page agents = %v, want [client-b client-a] (the unattributed commit adds none)", con.Agents)
		}
	}
	if r.ByAgent["client-a"] != 2 || r.ByAgent["client-b"] != 1 || len(r.ByAgent) != 2 {
		t.Errorf("commits_by_agent = %v, want client-a:2 client-b:1", r.ByAgent)
	}

	// A principal that does not see the whole KB learns no per-agent counts.
	narrow := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"ops"}}}})
	r, text := run(narrow)
	if r.ByAgent != nil || strings.Contains(text, "commits_by_agent") {
		t.Errorf("restricted principal got commits_by_agent: %s", text)
	}

	// concept_history carries the agent per revision.
	h, _ := callJSON(t, c.s, authLocalContext(), "concept_history", `{"id":"ops/page"}`)
	var hist struct {
		Revisions []map[string]any `json:"revisions"`
	}
	if err := json.Unmarshal([]byte(h), &hist); err != nil {
		t.Fatal(err)
	}
	var seq []string
	for _, rev := range hist.Revisions {
		agent, _ := rev["agent"].(string)
		seq = append(seq, agent)
	}
	if strings.Join(seq, ",") != ",client-b,client-a" {
		t.Errorf("history agents = %q, want newest first with the unattributed one empty", seq)
	}
}
