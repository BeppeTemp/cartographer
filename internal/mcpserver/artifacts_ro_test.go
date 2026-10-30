package mcpserver

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// An `artifacts: r` rule is a narrower allow (design D17): everything the
// whole-KB write grant gives except the four tools that take a caller-chosen
// artifact path and content.
func TestArtifactsReadOnlyRule(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	k.AuthName = "docs"
	flag := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true, ReadOnlyArtifacts: true}}}
	plain := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true}}}
	both := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true, ReadOnlyArtifacts: true}, {KB: "docs", Write: true}}}

	for _, tool := range []string{"artifact_write", "artifact_delete", "artifact_move", "skill_install"} {
		err := authorizeTool(flag, k, "docs", tool, json.RawMessage(`{}`))
		if err == nil || !strings.HasPrefix(err.Error(), "forbidden: artifacts_read_only: ") {
			t.Errorf("%s with artifacts: r: got %v", tool, err)
		}
		for name, p := range map[string]auth.Policy{"plain": plain, "union": both} {
			if err := authorizeTool(p, k, "docs", tool, json.RawMessage(`{}`)); err != nil {
				t.Errorf("%s (%s): %v", tool, name, err)
			}
		}
	}
	for _, tool := range []string{"artifact_read", "artifact_list", "skill_list", "log_append", "source_register", "map_create", "kb_status"} {
		if err := authorizeTool(flag, k, "docs", tool, json.RawMessage(`{}`)); err != nil {
			t.Errorf("%s with artifacts: r: %v", tool, err)
		}
	}
	if err := authorizeTool(flag, k, "docs", "concept_write", json.RawMessage(`{"id":"a/b","body":"x"}`)); err != nil {
		t.Errorf("concept_write: %v", err)
	}
	if err := authorizeTool(flag, k, "docs", "no_such_tool", json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "unclassified_tool") {
		t.Errorf("unclassified tool must still fail closed, got %v", err)
	}

	// artifact_move: the destination KB needs the grant too.
	ctx := func(p auth.Policy) context.Context {
		return auth.ContextWithPrincipal(context.Background(), auth.Principal{ID: "w", Policy: p})
	}
	dst := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true}, {KB: "other", Write: true, ReadOnlyArtifacts: true}}}
	if err := authorizeArtifactMoveTarget(ctx(dst), "other"); err == nil || !strings.Contains(err.Error(), "artifacts_read_only") {
		t.Errorf("move target with artifacts: r: %v", err)
	}
	if err := authorizeArtifactMoveTarget(ctx(dst), "docs"); err != nil {
		t.Errorf("move target without the flag: %v", err)
	}
}

// The rule is only as strong as the separation between artifact paths (KB
// root) and concepts/assets (under data/): concept_write and asset_write must
// not be a way to land a file under skills/, agents/, hooks/, templates/ or at
// paths.yaml. The KB root outside data/ must be byte-for-byte what it was,
// whatever each call answers.
func TestArtifactsReadOnlyRule_ConceptAndAssetWriteCannotReachArtifactPaths(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	k.AuthName = "docs"
	srv := New("test")
	srv.SetPolicyKB("docs")
	RegisterKBTools(srv, k, Deps{})
	admin := auth.ContextWithPrincipal(context.Background(), auth.LocalAdminPrincipal())

	outside := func() map[string]string {
		snap := map[string]string{}
		_ = filepath.WalkDir(k.Root, func(p string, d fs.DirEntry, err error) error {
			rel, _ := filepath.Rel(k.Root, p)
			if err != nil || rel == "." {
				return nil
			}
			if first := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]; first == "data" || first == ".git" || first == ".cartographer" {
				return filepath.SkipDir
			}
			// The git lock file is bookkeeping, created by the first write.
			if !d.IsDir() && rel != ".cartographer.lock" {
				b, _ := os.ReadFile(p)
				snap[rel] = string(b)
			}
			return nil
		})
		return snap
	}
	before := outside()

	call := func(tool, args string) ToolResult { return srv.callTool(admin, tool, json.RawMessage(args)) }
	// concept_write with a valid body: each id either lands under data/ or is
	// refused, never at the KB-root artifact path.
	landed := 0
	for _, id := range []string{"skills/evil/SKILL", "skills/evil", "templates/evil", "paths", "agents/evil", "hooks/evil", "../skills/evil", "x/../../paths"} {
		res := call("concept_write", `{"id":"`+id+`","frontmatter":{"type":"Note","title":"x"},"body":"x"}`)
		if !res.IsError {
			landed++
			if _, err := os.Stat(filepath.Join(k.Root, "data", filepath.FromSlash(id)+".md")); err != nil && !strings.Contains(id, "..") {
				t.Errorf("concept_write %q succeeded but nothing is under data/: %v", id, err)
			}
		}
	}
	if landed == 0 {
		t.Fatal("no concept_write succeeded: the pin would be vacuous")
	}
	// asset_write: an expanded concept and a path walking out of it.
	call("concept_write", `{"id":"runbooks/owner","frontmatter":{"type":"Note","title":"o"},"body":"o"}`)
	call("concept_expand", `{"id":"runbooks/owner"}`)
	for _, path := range []string{"ok.txt", "../../../skills/evil/SKILL.md", "../../../paths.yaml", "/skills/evil.md"} {
		call("asset_write", `{"concept_id":"runbooks/owner","path":"`+path+`","content":"x"}`)
	}
	for _, cid := range []string{"skills/evil", "templates/evil"} {
		call("asset_write", `{"concept_id":"`+cid+`","path":"a.txt","content":"x"}`)
	}
	if after := outside(); !reflect.DeepEqual(before, after) {
		t.Fatalf("the KB root outside data/ changed:\nbefore %v\nafter  %v", keysOf(before), keysOf(after))
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Through the real HTTP handler: a token whose rule carries artifacts: r is
// refused artifact_write with the new code, and keeps its other writes.
func TestArtifactsReadOnlyRule_OverHTTP(t *testing.T) {
	t.Parallel()
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "ro-tok", Principal: "ro", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kbx", Write: true, ReadOnlyArtifacts: true}}}},
		{Token: "rw-tok", Principal: "rw", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kbx", Write: true}}}},
	})
	h := newScopedTestHandler(t, ts)
	text := func(rr *httptest.ResponseRecorder) string {
		var resp Response
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		tr := decodeToolResult(t, resp)
		if len(tr.Content) == 0 {
			return ""
		}
		return tr.Content[0].Text
	}
	// skill_install needs Deps this fixture does not wire; authorizeTool covers it.
	for _, tool := range []string{"artifact_write", "artifact_delete"} {
		rr := doMCP(h, "kbx", "ro-tok", writeToolCallBody(tool))
		assertMCPForbidden(t, rr)
		if got := text(rr); !strings.Contains(got, "artifacts_read_only") {
			t.Errorf("%s: %q lacks artifacts_read_only", tool, got)
		}
		if got := text(doMCP(h, "kbx", "rw-tok", writeToolCallBody(tool))); strings.Contains(got, "forbidden") {
			t.Errorf("%s without the flag: %q", tool, got)
		}
	}
	if got := text(doMCP(h, "kbx", "ro-tok", writeToolCallBody("log_append"))); strings.Contains(got, "forbidden") {
		t.Errorf("log_append with artifacts: r: %q", got)
	}
}

func TestArtifactsReadOnlyRule_UsageRouteAndWhoami(t *testing.T) {
	t.Parallel()
	k := uiFixtureKB(t, "docs")
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "ro", Principal: "ro", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true, ReadOnlyArtifacts: true}}}},
		{Token: "rw", Principal: "rw", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs", Write: true}}}},
	})
	multi := NewMultiKBServer("test")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(nil)
	h := ts.Middleware(multi.Handler())

	row := []map[string]any{usageRow("ops-tool", "skill", "claude", time.Hour, 1)}
	if rr := postUsage(t, h, "?kb=docs", "ro", row); rr.Code != http.StatusNotFound {
		t.Errorf("usage with artifacts: r: %d, want 404", rr.Code)
	}
	if rr := postUsage(t, h, "?kb=docs", "rw", row); rr.Code != http.StatusOK {
		t.Errorf("usage without the flag: %d, want 200", rr.Code)
	}

	for token, want := range map[string]any{"ro": "r", "rw": nil} {
		who := decodeUI(t, getUI(t, h, UIAPIPrefix+"/whoami", token))
		rule := who["permissions"].([]interface{})[0].(map[string]interface{})
		if rule["artifacts"] != want {
			t.Errorf("%s: whoami artifacts = %v, want %v", token, rule["artifacts"], want)
		}
	}
}
