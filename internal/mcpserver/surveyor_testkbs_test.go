package mcpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/componentproto"
	"github.com/BeppeTemp/cartographer/internal/testkbs"
)

// The synthetic Surveyor of `make dev` (internal/testkbs/surveyor.go): its three
// files are accepted by the real route handlers, in the order dev.sh posts
// them, and what they carry is what the Atlas reads back.
func TestSurveyorPayloadsOfTheTestKBsAreAcceptedByTheRoutes(t *testing.T) {
	t.Parallel()
	clock := &testClock{t: time.Now().UTC()}
	dir := t.TempDir()
	if err := testkbs.WriteSurveyor(dir, clock.now()); err != nil {
		t.Fatal(err)
	}
	multi := NewMultiKBServer("test")
	multi.EnableWeb(nil)
	multi.surveyors.now = clock.now
	var perms []auth.Permission
	for _, name := range testkbs.SurveyorKBs() {
		k := uiFixtureKB(t, name)
		multi.MountKB(name, func(s *Server) { RegisterKBTools(s, k, Deps{}) })
		perms = append(perms, auth.Permission{KB: name, Write: true})
	}
	for _, s := range multi.servers {
		s.now = clock.now
	}
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{{Token: "t0k3n-9x", Principal: "dev-principal", Kind: auth.KindSurveyor, Policy: auth.Policy{Permissions: perms}}})
	h := ts.Middleware(multi.Handler())

	post := func(path, file string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer t0k3n-9x")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, rr.Code, rr.Body)
		}
		return rr.Body.String()
	}
	beat := post(componentproto.HeartbeatPath, testkbs.SurveyorHeartbeatFile)
	if !strings.Contains(beat, `"settings_wanted":true`) {
		t.Errorf("heartbeat answer %s", beat)
	}
	post(componentproto.SettingsPath, testkbs.SurveyorSettingsFile)
	runs := post(componentproto.RunsPath, testkbs.SurveyorRunsFile)
	if !strings.Contains(runs, `"received":9`) {
		t.Errorf("runs answer %s: every run of the file must be kept", runs)
	}

	// The settings route kept the sources and the entries, the store the alarm
	// and the candidates.
	rows := multi.surveyors.settingsRows()
	if len(rows) != 1 || len(rows[0].Sources) != 6 || len(rows[0].Entries) == 0 || len(rows[0].KBs) != len(testkbs.SurveyorKBs()) {
		t.Fatalf("settings rows %+v", rows)
	}
	book := multi.surveyors.runs["dev-principal"][testkbs.SurveyorPerimeter]
	if book == nil || len(book.runs) != 9 || len(book.alarms) != 1 || len(book.cands) != 2 {
		t.Fatalf("run book %+v", book)
	}
	var seen = map[string]bool{}
	for _, r := range book.runs {
		seen[r.Outcome] = true
	}
	for _, o := range componentproto.Outcomes() {
		if !seen[o] {
			t.Errorf("no run with outcome %s", o)
		}
	}
	if _, err := json.Marshal(book.runs); err != nil {
		t.Fatal(err)
	}
}
