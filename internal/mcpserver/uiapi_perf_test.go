package mcpserver

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

func perfHandler(t *testing.T) (http.Handler, *kb.KB) {
	t.Helper()
	k := uiFixtureKB(t, "docs")
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "whole", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs"}}}},
		{Token: "narrow", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"visible"}}}}},
	})
	multi := NewMultiKBServer("test")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(nil)
	return ts.Middleware(multi.Handler()), k
}

// The overview counts from the cached graph view: a warm page load, KB switch
// or live refresh reads no concept file (#1962).
func TestUIAPI_WarmOverviewReadsNoConceptFile(t *testing.T) {
	t.Parallel()
	handler, k := perfHandler(t)
	backdateTree(t, k.DataRoot())
	url := UIAPIPrefix + "/kbs/docs/overview"
	for _, token := range []string{"whole", "narrow"} {
		if rr := getUI(t, handler, url, token); rr.Code != http.StatusOK {
			t.Fatalf("%s overview: %d %s", token, rr.Code, rr.Body.String())
		}
	}
	reads := k.GraphFileReads()
	for _, token := range []string{"whole", "narrow"} {
		getUI(t, handler, url, token)
	}
	if n := k.GraphFileReads(); n != reads {
		t.Errorf("a warm overview read %d concept files", n-reads)
	}
}

// The counts the overview reports are those of the permission-filtered walk it
// replaced: what a narrowed token sees, never the whole KB's.
func TestUIAPI_OverviewCountsMatchTheFilteredWalk(t *testing.T) {
	t.Parallel()
	handler, k := perfHandler(t)
	for _, token := range []string{"whole", "narrow"} {
		body := decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs/docs/overview", token))
		concepts := body["concepts"].(map[string]interface{})
		visible := func(id string) bool {
			return token == "whole" || filepath.Dir(id) == "visible" || id == "visible/owner"
		}
		res, err := queryConcepts(k, ConceptQuery{Include: visible})
		if err != nil {
			t.Fatal(err)
		}
		byType, byStatus := map[string]interface{}{}, map[string]interface{}{}
		for _, e := range res.Entries {
			if e.Type != "" {
				byType[e.Type] = float64(len(filterEntries(res.Entries, func(o ConceptEntry) bool { return o.Type == e.Type })))
			}
			if e.Status != "" {
				byStatus[e.Status] = float64(len(filterEntries(res.Entries, func(o ConceptEntry) bool { return o.Status == e.Status })))
			}
		}
		if concepts["total"].(float64) != float64(len(res.Entries)) ||
			!reflect.DeepEqual(concepts["by_type"], byType) || !reflect.DeepEqual(concepts["by_status"], byStatus) {
			t.Errorf("%s: overview %v, walk total %d types %v statuses %v", token, concepts, len(res.Entries), byType, byStatus)
		}
	}
}

func filterEntries(in []ConceptEntry, keep func(ConceptEntry) bool) []ConceptEntry {
	var out []ConceptEntry
	for _, e := range in {
		if keep(e) {
			out = append(out, e)
		}
	}
	return out
}

// Polling tabs share one lstat walk: inside the window a revision poll does
// not validate the graph cache again, and a validation any other reader pays
// for moves the revision at once.
func TestUIAPI_RevisionPollsShareOneValidation(t *testing.T) {
	// Not parallel: it sets the poll's trust window, a package variable.
	setRevisionMaxAge(t, time.Hour)
	handler, k := perfHandler(t)
	revision := func() string {
		t.Helper()
		return decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs/docs/revision", "whole"))["revision"].(string)
	}
	before := revision()
	validations := k.GraphValidations()
	for i := 0; i < 10; i++ {
		if got := revision(); got != before {
			t.Fatalf("revision moved with nothing changed: %q → %q", before, got)
		}
	}
	if n := k.GraphValidations(); n != validations {
		t.Fatalf("ten polls validated the graph %d times", n-validations)
	}
	page := "---\ntype: Topic\ntitle: Pulled\n---\nArrived with a git pull.\n"
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "visible", "pulled.md"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := revision(); got != before {
		t.Fatalf("a poll inside the window validated: %q → %q", before, got)
	}
	// The Atlas reloads on a revision change; any strict read moves it.
	getUI(t, handler, UIAPIPrefix+"/kbs/docs/overview", "whole")
	if got := revision(); got == before {
		t.Fatalf("the revision did not follow a validation another reader paid for")
	}
}

// backdateTree makes every file under root old enough for the graph cache to
// trust its signature: a file modified within the racy window (D241) is read
// again at each validation, which is right and not what these tests measure.
func backdateTree(t *testing.T, root string) {
	t.Helper()
	old := time.Now().Add(-time.Hour)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			err = os.Chtimes(p, old, old)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
