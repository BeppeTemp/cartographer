package mcpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

func sendDoctorSchedule(t *testing.T, h http.Handler, method, query, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, DoctorSchedulePath+query, bytes.NewReader(raw))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func scheduleBody(client string, in time.Duration) map[string]string {
	return map[string]string{"client": client, "next_run": time.Now().Add(in).UTC().Format(time.RFC3339)}
}

func TestDoctorScheduleEndpoint_DeclareAndClear(t *testing.T) {
	h, k := usageHandler(t)
	rr := sendDoctorSchedule(t, h, http.MethodPost, "?kb=docs", "whole", scheduleBody("claude", 10*time.Hour))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	got, err := k.LoadDoctorSchedule()
	if err != nil || got == nil || got.Client != "claude" {
		t.Fatalf("store = %+v, %v", got, err)
	}
	if rr := sendDoctorSchedule(t, h, http.MethodDelete, "?kb=docs", "whole", nil); rr.Code != http.StatusOK {
		t.Fatalf("delete: status %d", rr.Code)
	}
	if got, _ := k.LoadDoctorSchedule(); got != nil {
		t.Fatalf("declaration survived a DELETE: %+v", got)
	}
	// Clearing what is not declared is not an error.
	if rr := sendDoctorSchedule(t, h, http.MethodDelete, "?kb=docs", "whole", nil); rr.Code != http.StatusOK {
		t.Fatalf("second delete: status %d", rr.Code)
	}
}

func TestDoctorScheduleEndpoint_Rejects(t *testing.T) {
	h, k := usageHandler(t)
	for name, body := range map[string]any{
		"bad client":    map[string]string{"client": "../x", "next_run": time.Now().Format(time.RFC3339)},
		"bad time":      map[string]string{"client": "claude", "next_run": "tomorrow"},
		"far in future": scheduleBody("claude", 30*24*time.Hour),
	} {
		if rr := sendDoctorSchedule(t, h, http.MethodPost, "?kb=docs", "whole", body); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, rr.Code)
		}
	}
	if got, _ := k.LoadDoctorSchedule(); got != nil {
		t.Errorf("a rejected body was stored: %+v", got)
	}
	// Scope: unknown KB and tokens that cannot write the whole KB get a 404.
	if rr := sendDoctorSchedule(t, h, http.MethodPost, "?kb=nope", "whole", scheduleBody("claude", time.Hour)); rr.Code != http.StatusNotFound {
		t.Errorf("unknown kb: status %d, want 404", rr.Code)
	}
	for _, tok := range []string{"reader", "narrow"} {
		if rr := sendDoctorSchedule(t, h, http.MethodPost, "?kb=docs", tok, scheduleBody("claude", time.Hour)); rr.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", tok, rr.Code)
		}
	}
	if rr := sendDoctorSchedule(t, h, http.MethodPost, "?kb=docs", "", scheduleBody("claude", time.Hour)); rr.Code != http.StatusUnauthorized {
		t.Errorf("no token: status %d, want 401", rr.Code)
	}
	if rr := sendDoctorSchedule(t, h, http.MethodGet, "?kb=docs", "whole", nil); rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: status %d, want 405", rr.Code)
	}
}

// Health reads the declaration through the maintenance summary, and a
// declaration more than a day past its next run is not believed.
func TestMaintenanceSummary_DoctorSchedule(t *testing.T) {
	ts := auth.NewTokenStore(nil)
	h := maintenanceHandler(t, ts)
	summary := func() map[string]any {
		rr := getUI(t, h, UIAPIPrefix+"/kbs/docs/maintenance/summary", "")
		if rr.Code != http.StatusOK {
			t.Fatalf("summary: status %d", rr.Code)
		}
		var out map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if _, ok := summary()["doctor_schedule"]; ok {
		t.Fatal("doctor_schedule present with nothing declared")
	}
	if rr := sendDoctorSchedule(t, h, http.MethodPost, "?kb=docs", "", scheduleBody("claude", 5*time.Hour)); rr.Code != http.StatusOK {
		t.Fatalf("declare: status %d: %s", rr.Code, rr.Body.String())
	}
	ds, ok := summary()["doctor_schedule"].(map[string]any)
	if !ok || ds["client"] != "claude" || ds["next_run"] == "" {
		t.Fatalf("doctor_schedule = %v", summary()["doctor_schedule"])
	}
}

func TestLiveDoctorSchedule_Staleness(t *testing.T) {
	_, k := usageHandler(t)
	now := time.Now()
	declare := func(next time.Time) {
		if err := k.DeclareDoctorSchedule(kb.DoctorSchedule{Client: "claude", NextRun: next, DeclaredAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	declare(now.Add(-23 * time.Hour))
	if k.LiveDoctorSchedule(now) == nil {
		t.Error("a run 23h late is still within the grace day")
	}
	declare(now.Add(-25 * time.Hour))
	if k.LiveDoctorSchedule(now) != nil {
		t.Error("a run 25h late must fall back to the old text")
	}
}
