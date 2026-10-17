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

// retiredHandler mounts a KB where every kind of retirement happened: an
// archive with a reason (through the real tool, so the log entry is the one
// production writes), a supersede, a status set by hand, a concept archived out
// of a Map the narrow token cannot see, a supersede onto a hidden successor,
// and a journal with one closed old entry and one archived entry.
func retiredHandler(t *testing.T) (http.Handler, *kb.KB) {
	t.Helper()
	k := setupTestKB(t)
	k.AuthName = "docs"
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	for _, m := range []struct{ name, kind string }{{"notes", "map"}, {"archive", "map"}, {"hidden", "map"}, {"runs", "journal"}} {
		if err := k.CreateMap(m.name, strings.ToUpper(m.name[:1])+m.name[1:], m.kind, nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	for rel, content := range map[string]string{
		"notes/old-plan.md":     "---\ntype: Note\ntitle: Old plan\nstatus: active\n---\nBody.\n",
		"notes/rota.md":         "---\ntype: Note\ntitle: Rota\nstatus: active\n---\nBody.\n",
		"notes/rota-v2.md":      "---\ntype: Note\ntitle: Rota v2\nstatus: active\n---\nBody.\n",
		"notes/handmade.md":     "---\ntype: Note\ntitle: Handmade\nstatus: deprecated\ntimestamp: 2026-01-05\n---\nBody.\n",
		"notes/doomed.md":       "---\ntype: Note\ntitle: Doomed\nstatus: active\n---\nBody.\n",
		"notes/live-a.md":       "---\ntype: Note\ntitle: Live A\nstatus: active\n---\nBody.\n",
		"notes/live-b.md":       "---\ntype: Note\ntitle: Live B\nstatus: active\n---\nBody.\n",
		"notes/live-c.md":       "---\ntype: Note\ntitle: Live C\nstatus: active\n---\nBody.\n",
		"notes/live-d.md":       "---\ntype: Note\ntitle: Live D\nstatus: active\n---\nBody.\n",
		"notes/live-e.md":       "---\ntype: Note\ntitle: Live E\nstatus: active\n---\nBody.\n",
		"hidden/secret.md":      "---\ntype: Note\ntitle: Secret\nstatus: active\n---\nBody.\n",
		"hidden/dead.md":        "---\ntype: Note\ntitle: Dead\nstatus: deprecated\n---\nBody.\n",
		"hidden/successor.md":   "---\ntype: Note\ntitle: Successor\nstatus: active\n---\nBody.\n",
		"runs/2019-closed.md":   "---\ntype: Note\ntitle: Closed run\nstatus: closed\ntimestamp: 2019-01-01\n---\nBody.\n",
		"runs/2019-archived.md": "---\ntype: Note\ntitle: Archived run\nstatus: archived\ntimestamp: 2019-01-02\n---\nBody.\n",
	} {
		abs := filepath.Join(k.DataRoot(), rel)
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	callOK(t, s, "concept_archive", `{"id":"notes/old-plan","if_match":"`+hashOf(t, k, "notes/old-plan")+`","reason":"replaced by the new plan, see D9"}`)
	// The reason is free text and names where the page came from: a token that
	// cannot see that Map must not read it.
	callOK(t, s, "concept_archive", `{"id":"hidden/secret","if_match":"`+hashOf(t, k, "hidden/secret")+`","reason":"moved out of hidden/payroll"}`)
	callOK(t, s, "supersede", `{"source_id":"notes/rota","target_id":"notes/rota-v2","reason":"new rota"}`)
	callOK(t, s, "supersede", `{"source_id":"notes/doomed","target_id":"hidden/successor"}`)

	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "whole", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs"}}}},
		{Token: "narrow", Policy: auth.Policy{Permissions: []auth.Permission{
			{KB: "docs", Maps: []string{"notes", "archive"}},
			{KB: "docs", Journals: []string{"runs"}},
		}}},
	})
	multi := NewMultiKBServer("test")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(nil)
	return ts.Middleware(multi.Handler()), k
}

func retiredRows(t *testing.T, body map[string]interface{}) map[string]map[string]interface{} {
	t.Helper()
	rows := map[string]map[string]interface{}{}
	for _, raw := range body["concepts"].([]interface{}) {
		row := raw.(map[string]interface{})
		rows[row["id"].(string)] = row
	}
	return rows
}

func TestUIAPI_RetiredTellsEachStory(t *testing.T) {
	t.Parallel()
	handler, _ := retiredHandler(t)
	rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/retired", "whole")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	body := decodeUI(t, rr)
	rows := retiredRows(t, body)

	archived := rows["archive/old-plan"]
	if archived == nil || archived["status"] != "deprecated" || archived["from"] != "notes/old-plan" ||
		archived["reason"] != "replaced by the new plan, see D9" || archived["retired_at"] == nil {
		t.Errorf("archived row = %v", archived)
	}
	if secret := rows["archive/secret"]; secret == nil || secret["from"] != "hidden/secret" || secret["reason"] != "moved out of hidden/payroll" {
		t.Errorf("a caller who sees the source must read from and reason: %v", secret)
	}
	superseded := rows["notes/rota"]
	if superseded == nil || superseded["status"] != "superseded" || superseded["superseded_by"] != "notes/rota-v2" ||
		superseded["superseded_by_title"] != "Rota v2" || superseded["supersede_reason"] != "new rota" ||
		superseded["retired_at"] == nil || superseded["from"] != nil {
		t.Errorf("superseded row = %v", superseded)
	}
	// A status set by hand has no log entry: its status and timestamp only.
	hand := rows["notes/handmade"]
	if hand == nil || hand["status"] != "deprecated" || hand["timestamp"] != "2026-01-05" ||
		hand["retired_at"] != nil || hand["from"] != nil || hand["reason"] != nil {
		t.Errorf("hand-made row = %v", hand)
	}
	if rows["notes/live-a"] != nil || rows["notes/rota-v2"] != nil {
		t.Errorf("a live concept is listed as retired: %v", rows)
	}

	// Recorded retirements come first, newest first; the rest follow by id.
	var order []string
	for _, raw := range body["concepts"].([]interface{}) {
		order = append(order, raw.(map[string]interface{})["id"].(string))
	}
	for i := 1; i < len(order); i++ {
		a, b := rows[order[i-1]], rows[order[i]]
		if a["retired_at"] == nil && b["retired_at"] != nil {
			t.Errorf("a row with no date sorts before one with a date: %v", order)
		}
	}
	if _, ok := rows["runs/2019-closed"]; ok {
		t.Error("a closed (not retired) journal entry is listed")
	}

	// Maps: the archive map carries the flag and the top-level counts.
	maps := map[string]map[string]interface{}{}
	for _, raw := range body["maps"].([]interface{}) {
		m := raw.(map[string]interface{})
		maps[m["name"].(string)] = m
	}
	if maps["archive"]["archive"] != true || maps["archive"]["retired"].(float64) != 2 || maps["archive"]["total"].(float64) != 2 {
		t.Errorf("archive map = %v", maps["archive"])
	}
	if maps["notes"]["archive"] != false || maps["runs"]["archive"] != false {
		t.Errorf("a live map or a journal is flagged archive: %v %v", maps["notes"], maps["runs"])
	}

	// Journals: the closed old entry waits, the archived one is counted.
	journals := body["journals"].([]interface{})
	if len(journals) != 1 {
		t.Fatalf("journals = %v", journals)
	}
	j := journals[0].(map[string]interface{})
	if j["name"] != "runs" || j["waiting"].(float64) != 1 || j["archived"].(float64) != 1 || j["harvest_after"].(float64) != 45 || j["harvest_off"] != nil {
		t.Errorf("journal = %v", j)
	}
}

func TestUIAPI_RetiredNarrowedTokenLearnsNothingHidden(t *testing.T) {
	t.Parallel()
	handler, _ := retiredHandler(t)
	rr := getUI(t, handler, UIAPIPrefix+"/kbs/docs/retired", "narrow")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	body := decodeUI(t, rr)
	// The archived page the token sees came from a Map it cannot see, the
	// superseded one points at a successor it cannot see: neither id appears,
	// nor the hidden Map's own retired page or its counts.
	if strings.Contains(rr.Body.String(), "hidden") || strings.Contains(rr.Body.String(), "Successor") {
		t.Fatalf("hidden id or title leaked: %s", rr.Body.String())
	}
	rows := retiredRows(t, body)
	if rows["archive/secret"] == nil || rows["archive/secret"]["from"] != nil {
		t.Errorf("archive/secret = %v, want listed without its from", rows["archive/secret"])
	}
	if rows["notes/doomed"] == nil || rows["notes/doomed"]["superseded_by"] != nil || rows["notes/doomed"]["superseded_by_title"] != nil {
		t.Errorf("notes/doomed = %v, want listed without its successor", rows["notes/doomed"])
	}
	if rows["archive/secret"]["reason"] != nil {
		t.Errorf("archive/secret reveals its reason although its source is hidden: %v", rows["archive/secret"])
	}
	if rows["archive/old-plan"]["from"] != "notes/old-plan" || rows["archive/old-plan"]["reason"] != "replaced by the new plan, see D9" {
		t.Errorf("a visible from or its reason was dropped: %v", rows["archive/old-plan"])
	}
	for _, raw := range body["maps"].([]interface{}) {
		if raw.(map[string]interface{})["name"] == "hidden" {
			t.Fatal("hidden map listed")
		}
	}
}

func TestUIAPI_OverviewMarksArchiveMaps(t *testing.T) {
	t.Parallel()
	handler, _ := retiredHandler(t)
	overview := decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs/docs/overview", "whole"))
	flags := map[string]interface{}{}
	for _, raw := range overview["collections"].([]interface{}) {
		c := raw.(map[string]interface{})
		flags[c["name"].(string)] = c["archive"]
	}
	if flags["archive"] != true || flags["notes"] != false || flags["runs"] != false {
		t.Errorf("archive flags = %v", flags)
	}
}

// The reader asks for archived pages explicitly (D820): the default query
// still hides them, as it does for an agent.
func TestUIAPI_SearchIncludeArchived(t *testing.T) {
	t.Parallel()
	handler, k := retiredHandler(t)
	fm := "---\ntype: Note\ntitle: Quokka rota\nstatus: archived\n---\nquokka\n"
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "archive", "quokka.md"), []byte(fm), 0o644); err != nil {
		t.Fatal(err)
	}
	live := "---\ntype: Note\ntitle: Quokka live\nstatus: active\n---\nquokka\n"
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "notes", "quokka-live.md"), []byte(live), 0o644); err != nil {
		t.Fatal(err)
	}
	ids := func(path string) map[string]bool {
		out := map[string]bool{}
		for _, raw := range decodeUI(t, getUI(t, handler, path, "whole"))["results"].([]interface{}) {
			out[raw.(map[string]interface{})["id"].(string)] = true
		}
		return out
	}
	if got := ids(UIAPIPrefix + "/kbs/docs/search?q=quokka"); !got["notes/quokka-live"] || got["archive/quokka"] {
		t.Errorf("default search = %v, want the live page only", got)
	}
	if got := ids(UIAPIPrefix + "/kbs/docs/search?q=quokka&include_archived=1"); !got["notes/quokka-live"] || !got["archive/quokka"] {
		t.Errorf("include_archived search = %v, want both", got)
	}
	var archivedFlag bool
	for _, raw := range decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs/docs/search?q=quokka&include_archived=1", "whole"))["results"].([]interface{}) {
		if h := raw.(map[string]interface{}); h["id"] == "archive/quokka" {
			archivedFlag, _ = h["archived"].(bool)
		}
	}
	if !archivedFlag {
		t.Error("the archived hit does not say it is archived")
	}
}

// retiredServer is a small KB (a live Map and an archive Map) behind the UI
// handler, with the tool registry kept so a test can retire things through the
// real tools and read back what the log entry holds.
func retiredServer(t *testing.T) (http.Handler, *Server, *kb.KB) {
	t.Helper()
	k := setupTestKB(t)
	k.AuthName = "docs"
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	for _, name := range []string{"notes", "archive"} {
		if err := k.CreateMap(name, strings.ToUpper(name[:1])+name[1:], "map", nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"plan", "rota", "rota-v2", "memo"} {
		content := "---\ntype: Note\ntitle: " + id + "\nstatus: active\n---\nBody.\n"
		if err := os.WriteFile(filepath.Join(k.DataRoot(), "notes", id+".md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	multi := NewMultiKBServer("test")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(nil)
	return auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "whole", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs"}}}},
	}).Middleware(multi.Handler()), s, k
}

// A reason with line breaks used to be written as is: the log entry then ended
// early (or a line starting "## " opened a second one) and the retirement read
// back as "no retirement recorded".
func TestUIAPI_RetiredReasonWithLineBreaks(t *testing.T) {
	t.Parallel()
	handler, s, k := retiredServer(t)
	callOK(t, s, "concept_archive", `{"id":"notes/memo","if_match":"`+hashOf(t, k, "notes/memo")+`","reason":"first line\r\nsecond line\n\n## third"}`)

	row := retiredRows(t, decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs/docs/retired", "whole")))["archive/memo"]
	if row == nil || row["from"] != "notes/memo" || row["reason"] != "first line second line ## third" || row["retired_at"] == nil {
		t.Errorf("archive/memo = %v, want its story with the reason on one line", row)
	}
	log, err := k.ReadRaw("log.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(log, "concept_archive: notes/memo -> archive/memo (deprecated, first line second line ## third):\n") != 1 {
		t.Errorf("the reason is not on the entry's title line:\n%s", log)
	}
}

// An archived page that is superseded later has two entries; the supersede does
// not erase where the page came from and why it was archived, and the row is
// dated by the entry that set its current status.
func TestUIAPI_RetiredArchivedThenSuperseded(t *testing.T) {
	t.Parallel()
	handler, s, k := retiredServer(t)
	callOK(t, s, "concept_archive", `{"id":"notes/rota","if_match":"`+hashOf(t, k, "notes/rota")+`","reason":"folded into the new rota"}`)
	callOK(t, s, "supersede", `{"source_id":"archive/rota","target_id":"notes/rota-v2","reason":"v2 is current"}`)
	callOK(t, s, "concept_archive", `{"id":"notes/plan","if_match":"`+hashOf(t, k, "notes/plan")+`"}`)

	// The tools stamp their log entries within the same second, which would let
	// either entry's date pass for the other's: rewrite the log with the same
	// entries a day or more apart. archive/plan gets a supersede entry in the
	// log only, as if it had been superseded and then deprecated again by hand,
	// so its status is not the supersede's.
	if err := os.Remove(filepath.Join(k.DataRoot(), "log.md")); err != nil {
		t.Fatal(err)
	}
	day := func(d int) time.Time { return time.Date(2026, 3, d, 9, 0, 0, 0, time.UTC) }
	for _, e := range []struct {
		text string
		at   time.Time
	}{
		{"concept_archive: notes/rota -> archive/rota (deprecated, folded into the new rota):\n- notes/rota → archive/rota", day(1)},
		{"concept_archive: notes/plan -> archive/plan (deprecated):\n- notes/plan → archive/plan", day(2)},
		{"supersede: archive/rota → notes/rota-v2", day(5)},
		{"supersede: archive/plan → notes/rota-v2", day(6)},
	} {
		if err := k.AppendLog(e.text, e.at); err != nil {
			t.Fatal(err)
		}
	}

	rows := retiredRows(t, decodeUI(t, getUI(t, handler, UIAPIPrefix+"/kbs/docs/retired", "whole")))
	row := rows["archive/rota"]
	if row == nil || row["status"] != "superseded" || row["superseded_by"] != "notes/rota-v2" || row["supersede_reason"] != "v2 is current" {
		t.Fatalf("archive/rota = %v, want the supersede", row)
	}
	if row["from"] != "notes/rota" || row["reason"] != "folded into the new rota" {
		t.Errorf("archive/rota = %v, want the archive's from and reason kept", row)
	}
	if got, want := row["retired_at"], day(5).Format(time.RFC3339); got != want {
		t.Errorf("superseded row: retired_at = %v, want the supersede's %v", got, want)
	}
	// Deprecated, not superseded: the archive entry's date, although a later
	// supersede entry exists.
	plan := rows["archive/plan"]
	if plan == nil || plan["status"] != "deprecated" {
		t.Fatalf("archive/plan = %v, want deprecated", plan)
	}
	if got, want := plan["retired_at"], day(2).Format(time.RFC3339); got != want {
		t.Errorf("deprecated row: retired_at = %v, want the archive's %v", got, want)
	}
}
