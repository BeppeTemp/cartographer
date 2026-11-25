package mcpserver

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

func dashboardUIHandler(t *testing.T, k *kb.KB) http.Handler {
	t.Helper()
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "whole", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs"}}}},
		{Token: "narrow", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"visible"}}}}},
		{Token: "other", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "elsewhere"}}}},
	})
	multi := NewMultiKBServer("test")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(nil)
	return ts.Middleware(multi.Handler())
}

// AC-kbdash-15: both routes answer the tool's result for the caller's
// principal and 404 for a KB the caller cannot see.
func TestDashboardUIRoutes(t *testing.T) {
	t.Parallel()
	_, k := dashboardServer(t)
	k.AuthName = "docs"
	h := dashboardUIHandler(t, k)

	if rr := getUI(t, h, UIAPIPrefix+"/kbs/docs/dashboards", "other"); rr.Code != http.StatusNotFound {
		t.Fatalf("list of an unseen KB: %d, want 404", rr.Code)
	}
	if rr := getUI(t, h, UIAPIPrefix+"/kbs/docs/dashboards/week", "other"); rr.Code != http.StatusNotFound {
		t.Fatalf("get of an unseen KB: %d, want 404", rr.Code)
	}

	list := decodeUI(t, getUI(t, h, UIAPIPrefix+"/kbs/docs/dashboards", "whole"))
	entries := list["dashboards"].([]interface{})
	if len(entries) != 1 || entries[0].(map[string]interface{})["name"] != "week" || entries[0].(map[string]interface{})["title"] != "Week" {
		t.Fatalf("whole list = %v", list)
	}
	// Names only for a caller that does not see the whole KB.
	rr := getUI(t, h, UIAPIPrefix+"/kbs/docs/dashboards", "narrow")
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "Week") || !strings.Contains(rr.Body.String(), `"week"`) {
		t.Fatalf("narrow list: %d %s", rr.Code, rr.Body.String())
	}

	got := decodeUI(t, getUI(t, h, UIAPIPrefix+"/kbs/docs/dashboards/week", "whole"))
	if got["name"] != "week" || got["hash"] == "" || got["hash"] == nil {
		t.Fatalf("whole get = %v", got)
	}
	if narrow := getUI(t, h, UIAPIPrefix+"/kbs/docs/dashboards/week", "narrow"); narrow.Code != http.StatusOK || strings.Contains(narrow.Body.String(), "private words") {
		t.Fatalf("narrow get leaked the note: %d %s", narrow.Code, narrow.Body.String())
	}
	if miss := decodeUI(t, getUI(t, h, UIAPIPrefix+"/kbs/docs/dashboards/nothing", "whole")); miss["hash"] != "" {
		t.Fatalf("unknown name = %v, want an empty dashboard", miss)
	}
	if bad := getUI(t, h, UIAPIPrefix+"/kbs/docs/dashboards/Bad_Name", "whole"); bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid name: %d, want 400", bad.Code)
	}
}

// AC-kbdash-14: the revision token changes after a dashboard-only edit and
// does not after a read.
func TestRevisionMovesOnDashboardEdit(t *testing.T) {
	// Not parallel: it narrows the poll's trust window, a package variable.
	setRevisionMaxAge(t, 0)
	_, k := workServer(t)
	k.AuthName = "docs"
	h := dashboardUIHandler(t, k)
	revision := func() string {
		t.Helper()
		return decodeUI(t, getUI(t, h, UIAPIPrefix+"/kbs/docs/revision", "whole"))["revision"].(string)
	}
	empty := revision()
	writeDashboardFile(t, k, "week", toolTestDashboard)
	created := revision()
	if created == empty {
		t.Fatalf("revision did not move on a new dashboard: %q", created)
	}
	getUI(t, h, UIAPIPrefix+"/kbs/docs/dashboards/week", "whole")
	getUI(t, h, UIAPIPrefix+"/kbs/docs/dashboards", "whole")
	if again := revision(); again != created {
		t.Fatalf("revision moved on a read: %q -> %q", created, again)
	}
	writeDashboardFile(t, k, "week", toolTestDashboard+"  - id: more\n    type: list\n")
	path := filepath.Join(k.Root, "dashboards", "week.yaml")
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	edited := revision()
	if edited == created {
		t.Fatalf("revision did not move on a dashboard edit: %q", edited)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if removed := revision(); removed == edited {
		t.Fatalf("revision did not move on a dashboard removal: %q", removed)
	}
}

// The demo KB's dashboard must stay valid: the public demo exports it (D745).
func TestDemoDashboardIsValid(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "..", "demo", "astronomy", "dashboards", "observing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := kb.ValidateDashboard(data); err != nil {
		t.Fatalf("demo dashboard: %v", err)
	}
}
