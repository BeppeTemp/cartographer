package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// repairTestKB serves a KB with one nonstandard_field finding (`updated` for
// `timestamp`) through an in-process server, and returns a caller for it.
func repairTestKB(t *testing.T, autoRepair []string) (toolCaller, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "kb-a")
	k, err := kb.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	k.AutoRepair = autoRepair
	k.DoctorIntervalDays = 14
	if err := k.CreateMapWithContract("ops", "Ops", "map", nil, "", kb.MapContract{}); err != nil {
		t.Fatal(err)
	}
	fm, _ := okf.ParseFrontmatter("")
	fm.Set("type", "Note")
	fm.Set("title", "First")
	fm.Set("updated", "2026-01-02")
	if _, err := k.WriteConcept("ops/first", fm, "# First\n", ""); err != nil {
		t.Fatal(err)
	}
	concept := filepath.Join(k.DataRoot(), "ops", "first.md")
	s := mcpserver.New("test")
	mcpserver.RegisterKBTools(s, k, mcpserver.Deps{})
	h := s.HTTPHandler()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.ContextWithPrincipal(r.Context(), auth.Principal{ID: "test", Policy: auth.Policy{Admin: true}})
		h.ServeHTTP(w, r.WithContext(ctx))
	}))
	t.Cleanup(srv.Close)
	return targetCaller{c: client.New(srv.URL+"/mcp", "")}, concept
}

func runRepairJSON(t *testing.T, c toolCaller, apply bool) (kbRepairReport, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runKBRepair(c, apply, true, &out, &errOut)
	var rep kbRepairReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("report: %v (stdout %q, stderr %q)", err, out.String(), errOut.String())
	}
	return rep, code
}

func checkLine(rep kbRepairReport, check string) kbRepairCheck {
	for _, dc := range rep.Checks {
		if dc.Check == check {
			return dc
		}
	}
	return kbRepairCheck{}
}

func TestKBRepairDryRunWritesNothing(t *testing.T) {
	c, concept := repairTestKB(t, []string{"nonstandard_field"})
	before, err := os.ReadFile(concept)
	if err != nil || !bytes.Contains(before, []byte("updated:")) {
		t.Fatalf("fixture: %v %q", err, before)
	}
	rep, code := runRepairJSON(t, c, false)
	if code != kbRepairExitJudgement {
		t.Fatalf("exit = %d, want %d (debt remains)", code, kbRepairExitJudgement)
	}
	if dc := checkLine(rep, "nonstandard_field"); dc.Planned != 1 || dc.Applied != 0 || !dc.Auto {
		t.Fatalf("nonstandard_field = %+v", dc)
	}
	if after, _ := os.ReadFile(concept); !bytes.Equal(before, after) {
		t.Fatal("a dry run changed the concept")
	}
}

func TestKBRepairApplyRefusesChecksOutsideTheCapability(t *testing.T) {
	// At the kb.KB level an empty list is "none": the default is resolved
	// from the config before the KB is built (config.KBSpec.AutoRepairChecks).
	c, concept := repairTestKB(t, []string{})
	before, _ := os.ReadFile(concept)
	var out, errOut bytes.Buffer
	code := runKBRepair(c, true, false, &out, &errOut)
	if code != kbRepairExitJudgement {
		t.Fatalf("exit = %d, want %d; stderr %s", code, kbRepairExitJudgement, errOut.String())
	}
	if after, _ := os.ReadFile(concept); !bytes.Equal(before, after) {
		t.Fatal("--apply with an empty auto_repair changed the concept")
	}
	if !strings.Contains(out.String(), "auto_repair is explicitly empty") {
		t.Fatalf("report does not say why nothing was applied:\n%s", out.String())
	}
}

func TestKBRepairApplyRunsOnlyTheListedChecks(t *testing.T) {
	c, concept := repairTestKB(t, []string{"nonstandard_field"})
	rep, code := runRepairJSON(t, c, true)
	if dc := checkLine(rep, "nonstandard_field"); dc.Applied != 1 {
		t.Fatalf("nonstandard_field = %+v, want 1 applied", dc)
	}
	for _, dc := range rep.Checks {
		if dc.Check != "nonstandard_field" && dc.Applied != 0 {
			t.Fatalf("%s applied outside the capability: %+v", dc.Check, dc)
		}
	}
	after, _ := os.ReadFile(concept)
	if !strings.Contains(string(after), "timestamp:") || strings.Contains(string(after), "updated:") {
		t.Fatalf("concept not repaired:\n%s", after)
	}
	if code != kbRepairExitClean || rep.Remaining != 0 {
		t.Fatalf("exit = %d, remaining = %d, want a clean KB", code, rep.Remaining)
	}
	// A mechanical pass is not a doctor session (D290): it must not move
	// last_doctor, or it would silence the doctor proposal for an interval.
	log, _ := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(concept)), "log.md"))
	if strings.Contains(string(log), "kb-doctor") {
		t.Fatalf("kb doctor wrote the kb-doctor marker:\n%s", log)
	}
}

// The server nudges a writer once a day toward a kb-doctor session (D299),
// but never the CLI: client.Call would hand every command a JSON array.
func TestKBRepairCLIIsNeverNudged(t *testing.T) {
	c, _ := repairTestKB(t, nil)
	for i := 0; i < 2; i++ {
		raw, err := c.Invoke("search", map[string]any{"query": "First"})
		if err != nil {
			t.Fatal(err)
		}
		var blocks []string
		if json.Unmarshal(raw, &blocks) == nil {
			t.Fatalf("the CLI received %d blocks: %q", len(blocks), blocks)
		}
	}
}

func TestCallIntoTakesTheFirstBlock(t *testing.T) {
	var v struct{ A int }
	two := fakeCaller(`["{\"A\":1}","cartographer: notice"]`)
	if err := callInto(two, "x", nil, &v); err != nil || v.A != 1 {
		t.Fatalf("v = %+v, err = %v", v, err)
	}
}

type fakeCaller string

func (f fakeCaller) Invoke(string, any) (json.RawMessage, error) { return json.RawMessage(f), nil }

// The product default (D323) is what --apply runs when the operator never
// wrote an auto_repair: every safe check (D355), and the report says it is the
// default so an upgrading operator is not surprised.
func TestKBRepairApplyRunsDefaultChecks(t *testing.T) {
	c, concept := repairTestKB(t, config.DefaultAutoRepair)
	rep, code := runRepairJSON(t, c, true)
	if dc := checkLine(rep, "nonstandard_field"); dc.Applied != 1 {
		t.Fatalf("nonstandard_field = %+v, want 1 applied by the default list", dc)
	}
	if code != kbRepairExitClean {
		t.Fatalf("exit = %d, want a clean KB", code)
	}
	for _, check := range config.DefaultAutoRepair {
		if !checkLine(rep, check).Auto {
			t.Errorf("%s is in the default list but not marked auto", check)
		}
	}
	for _, check := range []string{"broken_link", "reciprocal_link_item"} {
		if checkLine(rep, check).Auto {
			t.Errorf("%s drops or rewrites links and must not be a default", check)
		}
	}
	if after, _ := os.ReadFile(concept); strings.Contains(string(after), "updated:") {
		t.Fatalf("concept not repaired:\n%s", after)
	}
}

func TestKBRepairReportsTheDefaultList(t *testing.T) {
	var out bytes.Buffer
	printKBRepairReport(&out, kbRepairReport{KB: "kb-a", AutoRepair: config.DefaultAutoRepair, AutoRepairDefault: true})
	if !strings.Contains(out.String(), "auto_repair uses the default (14 checks") {
		t.Fatalf("report:\n%s", out.String())
	}
}
