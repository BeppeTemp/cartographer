package service

import (
	"net/http"
	"net/http/httptest"
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
