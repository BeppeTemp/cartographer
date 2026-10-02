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
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// doctorTestKB serves a KB with one nonstandard_field finding (`updated` for
// `timestamp`) through an in-process server, and returns a caller for it.
func doctorTestKB(t *testing.T, autoRepair []string) (toolCaller, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "kb-a")
	k, err := kb.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	k.DoctorAutoRepair = autoRepair
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
	concept := filepath.Join(root, "ops", "first.md")
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

func runDoctorJSON(t *testing.T, c toolCaller, apply bool) (kbDoctorReport, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runKBDoctor(c, apply, true, &out, &errOut)
	var rep kbDoctorReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("report: %v (stdout %q, stderr %q)", err, out.String(), errOut.String())
	}
	return rep, code
}

func checkLine(rep kbDoctorReport, check string) kbDoctorCheck {
	for _, dc := range rep.Checks {
		if dc.Check == check {
			return dc
		}
	}
	return kbDoctorCheck{}
}

func TestKBDoctorDryRunWritesNothing(t *testing.T) {
	c, concept := doctorTestKB(t, []string{"nonstandard_field"})
	before, _ := os.ReadFile(concept)
	rep, code := runDoctorJSON(t, c, false)
	if code != kbDoctorExitJudgement {
		t.Fatalf("exit = %d, want %d (debt remains)", code, kbDoctorExitJudgement)
	}
	if dc := checkLine(rep, "nonstandard_field"); dc.Planned != 1 || dc.Applied != 0 || !dc.Auto {
		t.Fatalf("nonstandard_field = %+v", dc)
	}
	if after, _ := os.ReadFile(concept); !bytes.Equal(before, after) {
		t.Fatal("a dry run changed the concept")
	}
}

func TestKBDoctorApplyRefusesChecksOutsideTheCapability(t *testing.T) {
	c, concept := doctorTestKB(t, nil)
	before, _ := os.ReadFile(concept)
	var out, errOut bytes.Buffer
	code := runKBDoctor(c, true, false, &out, &errOut)
	if code != kbDoctorExitJudgement {
		t.Fatalf("exit = %d, want %d; stderr %s", code, kbDoctorExitJudgement, errOut.String())
	}
	if after, _ := os.ReadFile(concept); !bytes.Equal(before, after) {
		t.Fatal("--apply with an empty doctor_auto_repair changed the concept")
	}
	if !strings.Contains(out.String(), "doctor_auto_repair is empty") {
		t.Fatalf("report does not say why nothing was applied:\n%s", out.String())
	}
}

func TestKBDoctorApplyRunsOnlyTheListedChecks(t *testing.T) {
	c, concept := doctorTestKB(t, []string{"nonstandard_field"})
	rep, code := runDoctorJSON(t, c, true)
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
	if code != kbDoctorExitClean || rep.Remaining != 0 {
		t.Fatalf("exit = %d, remaining = %d, want a clean KB", code, rep.Remaining)
	}
	// A mechanical pass is not a doctor session (D290): it must not move
	// last_doctor, or it would silence the doctor proposal for an interval.
	log, _ := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(concept)), "log.md"))
	if strings.Contains(string(log), "kb-doctor") {
		t.Fatalf("kb doctor wrote the kb-doctor marker:\n%s", log)
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

func (f fakeCaller) Call(string, any) (json.RawMessage, error) { return json.RawMessage(f), nil }
