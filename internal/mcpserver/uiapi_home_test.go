package mcpserver

import (
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// homeUIHandler mounts the UI fixture with a "whole" and a "narrow" token and
// one skill that references concepts in both Maps.
func homeUIHandler(t *testing.T) http.Handler {
	t.Helper()
	k := uiFixtureKB(t, "docs")
	skill := "---\nname: probe\ndescription: Reads the fixture\n---\nStart from [[visible/beta]], then visible/alpha.md and hidden/secret.\nNot a concept: visible/nope.\n"
	full := filepath.Join(k.Root, "skills/probe/SKILL.md")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(skill), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "whole", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs"}}}},
		{Token: "narrow", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"visible"}}}}},
	})
	multi := NewMultiKBServer("test")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{MCPAllowlist: []provisioning.MCPAllowlistEntry{}}) })
	multi.EnableWeb(nil)
	return ts.Middleware(multi.Handler())
}

func TestUIAPI_SearchIsFilteredAndNeverRecordsAMiss(t *testing.T) {
	handler := homeUIHandler(t)

	if rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/search", "narrow"); rr.Code != http.StatusBadRequest {
		t.Fatalf("search without q: status %d, want 400", rr.Code)
	}
	body := decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs/docs/search?q=links", "narrow"))
	var ids []string
	for _, raw := range body["results"].([]interface{}) {
		ids = append(ids, raw.(map[string]interface{})["id"].(string))
	}
	for _, id := range ids {
		if strings.HasPrefix(id, "hidden/") {
			t.Fatalf("narrowed search returned a hidden concept: %v", ids)
		}
	}
	if len(ids) == 0 {
		t.Fatal("narrowed search found nothing in its own Map")
	}

	// The reader types; what matches nothing is not a knowledge gap.
	getUI(t, handler, UIAPIPrefix+"/kbs/docs/search?q=zzzunmatched", "whole")
	status := decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs/docs/status", "whole"))
	if misses, ok := status["search_misses"]; ok {
		t.Fatalf("a UI search was recorded as a miss: %v", misses)
	}
}

func TestUIAPI_StatusIsAWholeKBResource(t *testing.T) {
	handler := homeUIHandler(t)
	if rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/status", "narrow"); rr.Code != http.StatusNotFound {
		t.Fatalf("narrow status: %d, want 404", rr.Code)
	}
	if rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/status", "whole"); rr.Code != http.StatusOK {
		t.Fatalf("whole status: %d: %s", rr.Code, rr.Body.String())
	}
}

func TestUIAPI_ChangesIsServedAndRejectsABadWindow(t *testing.T) {
	handler := homeUIHandler(t)
	rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/changes?since=7d", "narrow")
	if rr.Code == http.StatusNotFound || rr.Code >= 500 {
		t.Fatalf("changes: status %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Code == http.StatusOK && strings.Contains(rr.Body.String(), "hidden/secret") {
		t.Fatal("narrowed changes disclosed a hidden concept")
	}
	if rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/changes?since=yesterday-ish", "narrow"); rr.Code != http.StatusBadRequest {
		t.Fatalf("bad since: status %d, want 400", rr.Code)
	}
}

func TestUIAPI_ArtifactsCarryTheConceptsTheyReference(t *testing.T) {
	handler := homeUIHandler(t)
	detail := decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs/docs/artifact?kind=skill&name=probe", "whole"))
	var got []string
	for _, raw := range detail["concepts"].([]interface{}) {
		got = append(got, raw.(string))
	}
	want := []string{"hidden/secret", "visible/alpha", "visible/beta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("concepts = %v, want %v (a missing id is never a reference)", got, want)
	}

	concept := decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs/docs/concept?id=visible/alpha", "whole"))
	usedBy, _ := concept["used_by"].([]interface{})
	if len(usedBy) != 1 || usedBy[0].(map[string]interface{})["name"] != "probe" {
		t.Fatalf("used_by = %v, want the probe skill", concept["used_by"])
	}
	// Artifacts are whole-KB resources: a narrowed principal learns nothing.
	narrow := decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs/docs/concept?id=visible/alpha", "narrow"))
	if v := narrow["used_by"]; v != nil {
		t.Fatalf("narrowed used_by = %v, want absent", v)
	}
}

func TestArtifactConceptRefs(t *testing.T) {
	exists := map[okf.ConceptID]struct{}{"a/b": {}, "c/d": {}, "e/f": {}}
	a := kbArtifact{Files: []provisioning.ArtifactFile{
		{Path: "SKILL.md", Content: []byte("See [[a/b|B]], c/d/index.md and e/f. Also x/y and /a/b.\n")},
		{Path: "logo.bin", Content: []byte{0xff, 0xfe}},
	}}
	if got, want := artifactConceptRefs(a, exists), []string{"a/b", "c/d", "e/f"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("refs = %v, want %v", got, want)
	}
}

// TestUIAPI_WorkFiltersLikeTheTool (D302): the route is work_list for the
// request's principal, and a KB the caller cannot see is a 404.
func TestUIAPI_WorkFiltersLikeTheTool(t *testing.T) {
	k := uiFixtureKB(t, "docs")
	writeKBFile(t, k, "visible/task.md", "---\ntype: Task\ntitle: Task\nstatus: open\npriority: p1\n---\n# T\n\n- [ ] visible item\n")
	writeKBFile(t, k, "hidden/task.md", "---\ntype: Task\ntitle: Hidden\nstatus: open\n---\n# H\n\n- [ ] hidden item\n")
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "whole", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs"}}}},
		{Token: "narrow", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"visible"}}}}},
		{Token: "other", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "elsewhere"}}}},
	})
	multi := NewMultiKBServer("test")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(nil)
	handler := ts.Middleware(multi.Handler())

	if rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/work", "other"); rr.Code != http.StatusNotFound {
		t.Fatalf("unseen KB: status %d, want 404", rr.Code)
	}
	rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/work?include=items&fields=priority", "narrow")
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "hidden") || !strings.Contains(rr.Body.String(), "visible item") || !strings.Contains(rr.Body.String(), `"priority":"p1"`) {
		t.Fatalf("narrow work: %d %s", rr.Code, rr.Body.String())
	}
	if body := decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs/docs/work?where=priority%3Dp1", "whole")); body["total"].(float64) != 1 {
		t.Fatalf("where: %v", body)
	}
	if rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/work?include=bogus", "whole"); rr.Code != http.StatusBadRequest {
		t.Fatalf("bad include: %d", rr.Code)
	}
}

func TestUIAPI_RevisionMovesOnAnOutOfBandEdit(t *testing.T) {
	k := uiFixtureKB(t, "docs")
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "whole", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs"}}}},
		{Token: "narrow", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"visible"}}}}},
	})
	multi := NewMultiKBServer("test")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{MCPAllowlist: []provisioning.MCPAllowlistEntry{}}) })
	multi.EnableWeb(nil)
	handler := ts.Middleware(multi.Handler())

	// A narrowed reader would learn when a hidden Map moves.
	if rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/revision", "narrow"); rr.Code != http.StatusNotFound {
		t.Fatalf("narrow revision: %d, want 404", rr.Code)
	}
	revision := func() string {
		t.Helper()
		return decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs/docs/revision", "whole"))["revision"].(string)
	}
	before := revision()
	if again := revision(); again != before {
		t.Fatalf("revision moved with nothing changed: %q → %q", before, again)
	}
	// A pull or an editor writes behind the server's back.
	page := "---\ntype: Topic\ntitle: Pulled\n---\nArrived with a git pull.\n"
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "visible", "pulled.md"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	if after := revision(); after == before {
		t.Fatalf("revision did not move after an out-of-band edit: %q", after)
	}
}
