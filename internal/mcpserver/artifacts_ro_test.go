package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
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
// root) and concepts/assets (under data/): concept_write must not be a way to
// land a file under skills/, templates/ or paths.yaml.
func TestArtifactsReadOnlyRule_ConceptWriteCannotReachArtifactPaths(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	k.AuthName = "docs"
	srv := New("test")
	srv.SetPolicyKB("docs")
	RegisterKBTools(srv, k, Deps{})
	admin := auth.ContextWithPrincipal(context.Background(), auth.LocalAdminPrincipal())
	for _, id := range []string{"skills/evil/SKILL", "skills/evil", "templates/evil", "paths", "agents/evil", "hooks/evil"} {
		srv.callTool(admin, "concept_write", json.RawMessage(`{"id":"`+id+`","body":"x"}`))
	}
	for _, rel := range []string{"skills/evil", "skills/evil.md", "agents/evil.md", "hooks/evil.md", "paths.yaml", "templates/evil.md"} {
		if _, err := os.Stat(filepath.Join(k.Root, rel)); err == nil {
			t.Errorf("concept_write created %s outside data/", rel)
		}
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
