package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/audit"
	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/kb"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// surveyorRunCtx is ctx as the SDK bridge builds it for a request carrying the
// given values of the Cartographer-Surveyor-Run header.
func surveyorRunCtx(ctx context.Context, values ...string) context.Context {
	h := http.Header{}
	for _, v := range values {
		h.Add(client.SurveyorRunHeader, v)
	}
	return withSurveyorRun(ctx, &sdk.CallToolRequest{Extra: &sdk.RequestExtra{Header: h}})
}

// AC-surveyor-run-commit-7 (a): the run id is a label. The identifiers that
// read it appear in a fixed set of non-test files, and never in the packages
// that decide who may do what or record what was done. A new reader needs a
// visible edit of this table.
func TestSurveyorRunNeverAuthorizes(t *testing.T) {
	t.Parallel()
	idents := []string{"SurveyorRunHeader", "withSurveyorRun", "callerSurveyorRun(", "commitSurveyorRunKey"}
	allowed := map[string]bool{"sdkbridge.go": true, "surveyorrun.go": true, "writerauthor.go": true, "gitwrap.go": true, "tools_read.go": true}
	scan := func(dir string, ok func(name string) bool) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			src, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range idents {
				if strings.Contains(string(src), id) && !ok(name) {
					t.Errorf("%s/%s mentions %q: the Surveyor run id may only be read by the files listed in this test", dir, name, id)
				}
			}
		}
	}
	scan(".", func(name string) bool { return allowed[name] })
	scan(filepath.Join("..", "auth"), func(string) bool { return false })
	scan(filepath.Join("..", "audit"), func(string) bool { return false })
}

// AC-surveyor-run-commit-7 (b): with or without the header, valid or naming
// another replica's run, a principal gets the same refusal, the same view and
// the same audit record.
func TestSurveyorRunNeverAuthorizesBehaviour(t *testing.T) {
	t.Parallel()
	headers := map[string][]string{
		"none":   nil,
		"valid":  {"replica-a-1700000000-1"},
		"forged": {"replica-b-1700000001-9"},
	}

	t.Run("refusal", func(t *testing.T) {
		c := newChangesLinksKB(t)
		readOnly := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs"}}})
		var want string
		for name, vals := range headers {
			res := c.s.callTool(surveyorRunCtx(readOnly, vals...), "concept_write", json.RawMessage(`{"id":"ops/x","title":"X","type":"Note","body":"x"}`))
			if !res.IsError {
				t.Fatalf("%s: a principal that may not write was allowed: %+v", name, res)
			}
			if want == "" {
				want = res.Content[0].Text
			} else if res.Content[0].Text != want {
				t.Errorf("%s: refusal %q differs from %q", name, res.Content[0].Text, want)
			}
		}
	})

	t.Run("view", func(t *testing.T) {
		c := newChangesLinksKB(t)
		c.write("ops/seen", "S.\n")
		c.write("hidden/unseen", "U.\n")
		c.commit(true)
		narrow := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"ops"}}}})
		since := `{"since":"` + c.base.Format("2006-01-02T15:04:05Z07:00") + `"}`
		want := map[string]string{}
		for name, vals := range headers {
			ctx := surveyorRunCtx(narrow, vals...)
			for tool, args := range map[string]string{"concept_list": `{}`, "changes_since": since} {
				res := c.s.callTool(ctx, tool, json.RawMessage(args))
				if res.IsError {
					t.Fatalf("%s %s: %+v", name, tool, res)
				}
				text := res.Content[0].Text
				if strings.Contains(text, "hidden/unseen") {
					t.Errorf("%s %s: shows a concept outside the map", name, tool)
				}
				if prev, ok := want[tool]; ok && prev != text {
					t.Errorf("%s %s: answer differs\n%s\n%s", name, tool, prev, text)
				} else if !ok {
					want[tool] = text
				}
			}
		}
	})

	t.Run("audit", func(t *testing.T) {
		var first []audit.Entry
		for _, name := range []string{"none", "valid", "forged"} {
			s, _, path := auditServer(t, audit.Options{})
			s.RegisterTool(Tool{
				Name: "w_tool", InputSchema: json.RawMessage(`{"type":"object"}`),
				Handler: func(requestContext, json.RawMessage) (ToolResult, error) { return textResult("ok"), nil },
			})
			ctx := surveyorRunCtx(auth.ContextWithPrincipal(authLocalContext(), auth.Principal{ID: "tok", Policy: auth.Policy{Admin: true}}), headers[name]...)
			if r := s.callTool(ctx, "w_tool", json.RawMessage(`{}`)); r.IsError {
				t.Fatalf("%s: %+v", name, r)
			}
			entries := auditEntries(t, path)
			for i := range entries {
				e := &entries[i]
				e.ID, e.Timestamp, e.RequestID, e.PrevHash, e.Hash, e.Sig, e.DurationMs = "", time.Time{}, "", "", "", "", 0
			}
			if first == nil {
				first = entries
				continue
			}
			a, _ := json.Marshal(first)
			b, _ := json.Marshal(entries)
			if string(a) != string(b) {
				t.Errorf("%s: audit record differs\n%s\n%s", name, a, b)
			}
		}
	})
}

func TestWithSurveyorRunKeepsOneValidValue(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", 97)
	for _, c := range []struct {
		name string
		vals []string
		want string
	}{
		{"valid", []string{"replica-1700000000-1"}, "replica-1700000000-1"},
		{"none", nil, ""},
		{"two", []string{"a-1", "b-2"}, ""},
		{"space", []string{"a 1"}, ""},
		{"newline", []string{"a-1\nCartographer-Token: forged"}, ""},
		{"too long", []string{long}, ""},
		{"empty", []string{""}, ""},
	} {
		if got := callerSurveyorRun(surveyorRunCtx(context.Background(), c.vals...)); got != c.want {
			t.Errorf("%s: run = %q, want %q", c.name, got, c.want)
		}
	}
	if got := callerSurveyorRun(withSurveyorRun(context.Background(), &sdk.CallToolRequest{})); got != "" {
		t.Errorf("no request extra (stdio): run = %q, want none", got)
	}
}

// A concept_transfer made under a valid Cartographer-Surveyor-Run header writes
// the Surveyor-Run trailer on the commit of both KBs (twoKBMove builds both
// messages with commitTrailerBlock), and changes_since on each returns it.
func TestConceptTransferCarriesSurveyorRun(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	xferWrite(t, f.a, "notes/a.md", "---\ntype: Note\ntitle: A\n---\n# A\nbody of a\n")
	xferCommit(t, f.a)
	const run = "rep-1700000000-4"

	ctx := surveyorRunCtx(authLocalContext(), run)
	if res := f.srvA.callTool(ctx, "concept_transfer", json.RawMessage(flatMoves)); res.IsError {
		t.Fatalf("concept_transfer: %s", xferText(res))
	}
	deps := Deps{KBByName: func(n string) (*kb.KB, bool) { k, ok := f.byName[n]; return k, ok }}
	srvB := New("test")
	RegisterKBTools(srvB, f.b, deps)
	for name, c := range map[string]struct {
		k  *kb.KB
		s  *Server
		id string
	}{"source": {f.a, f.srvA, "notes/a"}, "target": {f.b, srvB, "wiki/a"}} {
		if got := gitOut(t, c.k, "log", "-1", "--format=%(trailers:key=Surveyor-Run,valueonly,separator=%x1f)"); got != run {
			t.Errorf("%s: Surveyor-Run trailer = %q, want %q", name, got, run)
		}
		res := c.s.callTool(authLocalContext(), "changes_since", json.RawMessage(`{"since":"1h"}`))
		if res.IsError {
			t.Fatalf("%s changes_since: %s", name, xferText(res))
		}
		var out changesSinceResult
		if err := json.Unmarshal([]byte(xferText(res)), &out); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, ch := range out.Concepts {
			if ch.ID == c.id {
				found = true
				if !reflect.DeepEqual(ch.SurveyorRuns, []string{run}) {
					t.Errorf("%s: %s surveyor_runs = %v, want [%s]", name, c.id, ch.SurveyorRuns, run)
				}
			}
		}
		if !found {
			t.Errorf("%s: changes_since does not list %s", name, c.id)
		}
	}
}
