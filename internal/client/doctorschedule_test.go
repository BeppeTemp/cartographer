package client

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDoctorScheduleDeclareAndWithdraw(t *testing.T) {
	var method, kbq, auth, route string
	var body map[string]string
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, kbq, auth, route = r.Method, r.URL.Query().Get("kb"), r.Header.Get("Authorization"), r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		body = nil
		_ = json.Unmarshal(raw, &body)
		w.WriteHeader(status)
	}))
	defer srv.Close()
	c := New(srv.URL+"/mcp", "tok")

	next := time.Date(2026, 10, 11, 6, 0, 0, 0, time.UTC)
	if err := c.DeclareDoctorSchedule("kb-a", "claude", next, time.Second); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPost || route != "/api/doctor-schedule" || kbq != "kb-a" || auth != "Bearer tok" ||
		body["client"] != "claude" || body["next_run"] != "2026-10-11T06:00:00Z" {
		t.Errorf("declare sent %s %s kb=%s auth=%s %v", method, route, kbq, auth, body)
	}
	if err := c.WithdrawDoctorSchedule("kb-a", time.Second); err != nil || method != http.MethodDelete || body != nil {
		t.Errorf("withdraw: %v, sent %s %v", err, method, body)
	}
	status = http.StatusNotFound
	if err := c.DeclareDoctorSchedule("kb-a", "claude", next, time.Second); err == nil {
		t.Error("a 404 from an older server must surface as an error the caller can ignore")
	}
}
