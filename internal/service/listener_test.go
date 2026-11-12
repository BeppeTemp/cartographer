package service

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProbeListener(t *testing.T) {
	serve := func(h http.HandlerFunc) string {
		s := httptest.NewServer(h)
		t.Cleanup(s.Close)
		return strings.TrimPrefix(s.URL, "http://")
	}
	for _, tc := range []struct {
		name string
		addr string
		want ListenerState
	}{
		{"ours", serve(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"status":"ok","version":"v1","started_at":"x"}`))
		}), ListenerCartographer},
		{"ours, bootstrapping", serve(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"status":"ok","version":"v1","bootstrapping":true}`))
		}), ListenerCartographer},
		{"anything that says ok", serve(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"status":"ok"}`)) }), ListenerForeign},
		{"not json", serve(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`hello`)) }), ListenerForeign},
		{"404", serve(http.NotFound), ListenerForeign},
		{"nothing", "127.0.0.1:1", ListenerNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProbeListener(tc.addr, 2*time.Second); got != tc.want {
				t.Errorf("ProbeListener = %v, want %v", got, tc.want)
			}
		})
	}
}

// A 200 from another program on the server address is not a healthy service:
// Status says so, and ForeignListener names the cause (#1998).
func TestStatus_ForeignListenerIsNotHealthy(t *testing.T) {
	serve := func(h http.HandlerFunc) string {
		s := httptest.NewServer(h)
		t.Cleanup(s.Close)
		return strings.TrimPrefix(s.URL, "http://")
	}
	for _, tc := range []struct {
		name        string
		addr        string
		wantHealthy bool
		wantForeign bool
	}{
		{"ours", serve(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"status":"ok","version":"v1"}`))
		}), true, false},
		{"a 200 that is not ours", serve(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"status":"ok"}`)) }), false, true},
		{"an empty 200", serve(func(http.ResponseWriter, *http.Request) {}), false, true},
		{"nothing", "127.0.0.1:1", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := withTestHome(t, "darwin")
			configPath := filepath.Join(home, "server.yaml")
			if err := os.WriteFile(configPath, []byte("http: \""+tc.addr+"\"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			m, _ := newTestManager()
			st, err := m.Status(configPath)
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			if !st.HealthChecked || st.Healthy != tc.wantHealthy || st.ForeignListener != tc.wantForeign {
				t.Errorf("HealthChecked=%v Healthy=%v ForeignListener=%v, want true/%v/%v", st.HealthChecked, st.Healthy, st.ForeignListener, tc.wantHealthy, tc.wantForeign)
			}
			if checkHealth(tc.addr) != tc.wantHealthy {
				t.Errorf("checkHealth(%s) = %v, want %v", tc.addr, !tc.wantHealthy, tc.wantHealthy)
			}
		})
	}
}
