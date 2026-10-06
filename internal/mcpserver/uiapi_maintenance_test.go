package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// maintenanceHandler mounts a git KB with three concepts to repair, one of
// them repaired by the heartbeat, and an open_question concept.
func maintenanceHandler(t *testing.T, ts *auth.TokenStore) http.Handler {
	t.Helper()
	k, s := autoRepairKB(t, "nonstandard_field")
	k.AutoRepairDefault = true
	k.AuthName = "docs"
	s.runAutoRepairQuota(t.Context())
	fm := newFM()
	fm.Set("type", "Contradiction")
	fm.Set("title", "Which port does the proxy listen on?")
	fm.Set("contradiction_kind", "open_question")
	fm.Set("resolution_status", "open")
	if _, err := k.WriteConcept(okf.ConceptID("ops/q1"), fm, "# Q\n", ""); err != nil {
		t.Fatal(err)
	}
	done := newFM()
	done.Set("type", "Contradiction")
	done.Set("title", "Answered already")
	done.Set("contradiction_kind", "open_question")
	done.Set("resolution_status", "resolved")
	if _, err := k.WriteConcept(okf.ConceptID("ops/q2"), done, "# Q\n", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := k.CommitOp("test: questions"); err != nil {
		t.Fatal(err)
	}
	multi := NewMultiKBServer("test")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(nil)
	return ts.Middleware(multi.Handler())
}

func TestUIAPI_MaintenanceSummary(t *testing.T) {
	handler := maintenanceHandler(t, auth.NewTokenStore(nil))
	rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/maintenance/summary", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	body := decodeUI(t, rr)
	ar := body["auto_repair"].(map[string]any)
	if ar["enabled"] != true || ar["default"] != true || ar["interval_days"].(float64) != 1 {
		t.Fatalf("auto_repair = %v", ar)
	}
	last := body["last_auto_repair"].(map[string]any)
	if len(last["checks"].([]any)) != 1 {
		t.Fatalf("last_auto_repair = %v", last)
	}
	repairs := body["repairs"].([]any)
	if len(repairs) != 1 {
		t.Fatalf("repairs = %v", repairs)
	}
	r0 := repairs[0].(map[string]any)
	if r0["background"] != true || r0["files"].(float64) != 3 {
		t.Fatalf("repair = %v", r0)
	}
	sha := r0["sha"].(string)
	if want := "cartographer kb repair docs --revert " + sha[:7]; r0["revert"] != want {
		t.Fatalf("revert = %v, want %q", r0["revert"], want)
	}
	// The panel shows the repair as a repair, never the seed or the questions.
	if strings.Contains(rr.Body.String(), "test: seed") || strings.Contains(rr.Body.String(), "test: questions") {
		t.Fatalf("a non-repair commit is listed: %s", rr.Body.String())
	}
}

func TestUIAPI_MaintenanceQuestionsListsOpenOnes(t *testing.T) {
	handler := maintenanceHandler(t, auth.NewTokenStore(nil))
	rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/maintenance/questions", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	qs := decodeUI(t, rr)["questions"].([]any)
	if len(qs) != 1 || qs[0].(map[string]any)["id"] != "ops/q1" {
		t.Fatalf("questions = %v", qs)
	}
}

func TestUIAPI_MaintenanceUnknownRouteAndMethods(t *testing.T) {
	handler := maintenanceHandler(t, auth.NewTokenStore(nil))
	for _, path := range []string{
		UIAPIPrefix + "/kbs/docs/maintenance",
		UIAPIPrefix + "/kbs/docs/maintenance/revert",
		UIAPIPrefix + "/kbs/docs/maintenance/summary/extra",
		UIAPIPrefix + "/kbs/docs/overview/summary",
	} {
		if rr := getUI(t, handler, path, ""); rr.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, rr.Code)
		}
	}
	// No route of the UI API writes, the maintenance ones included.
	for _, path := range []string{"/kbs", "/kbs/docs/maintenance/summary", "/kbs/docs/maintenance/questions", "/kbs/docs/maintenance/revert", "/kbs/docs/work", "/kbs/docs/overview"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, httptest.NewRequest(method, UIAPIPrefix+path, nil))
			if rr.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s: status %d, want 405", method, path, rr.Code)
			}
		}
	}
}

// The summary describes the whole KB, so a token that sees only part of it is
// told the route does not exist, like /status.
func TestUIAPI_MaintenanceSummaryNeedsAWholeKBView(t *testing.T) {
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "whole", Scopes: []auth.KBScope{{KB: "docs", Write: false}}},
		{Token: "part", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"ops"}}}}},
	})
	handler := maintenanceHandler(t, ts)
	if rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/maintenance/summary", "whole"); rr.Code != http.StatusOK {
		t.Errorf("whole-KB token: %d %s", rr.Code, rr.Body.String())
	}
	if rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/maintenance/summary", "part"); rr.Code != http.StatusNotFound {
		t.Errorf("partial token: %d, want 404", rr.Code)
	}
	// The questions are concepts, filtered one by one like contradiction_report.
	rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/maintenance/questions", "part")
	if qs := decodeUI(t, rr)["questions"].([]any); rr.Code != http.StatusOK || len(qs) != 1 {
		t.Errorf("partial token questions: %d %v", rr.Code, qs)
	}
}
