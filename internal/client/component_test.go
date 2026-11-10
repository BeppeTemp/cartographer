package client

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/componentproto"
)

// The component calls go to the family's paths, carry the protocol, and a 426
// hands the caller the server's numbers.
func TestComponentCalls(t *testing.T) {
	var got []string
	var settings map[string]any
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.Path+"?"+r.URL.RawQuery)
		if r.URL.Path == componentproto.SettingsPath {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &settings)
		}
		if status == http.StatusUpgradeRequired {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"code":"protocol_unsupported","protocol":4,"min_protocol":3}}`))
			return
		}
		_, _ = w.Write([]byte(`{"protocol":1,"min_protocol":1}`))
	}))
	defer srv.Close()
	c := New(srv.URL+"/mcp", "tok").WithInsecureHTTP(true)

	if err := c.ReportUsage("kb-a", []int{}, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := c.ReportClientSettings(map[string]any{"machine": "m"}, time.Second); err != nil {
		t.Fatal(err)
	}
	if settings["component"] != "client" || settings["protocol"] != float64(componentproto.Protocol) || settings["machine"] != "m" {
		t.Fatalf("settings body = %v", settings)
	}
	a, err := c.Heartbeat(map[string]any{"protocol": 1}, time.Second)
	if err != nil || a.Protocol != 1 || a.MinProtocol != 1 {
		t.Fatalf("answer %+v err %v", a, err)
	}
	want := []string{"/api/component/v1/usage?kb=kb-a&protocol=1", "/api/component/v1/settings?protocol=1", "/api/component/v1/heartbeat?protocol=1"}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("call %d went to %q, want %q", i, got[i], w)
		}
	}

	status = http.StatusUpgradeRequired
	_, err = c.Heartbeat(map[string]any{"protocol": 1}, time.Second)
	var re *RemoteError
	if !errors.As(err, &re) || re.HTTPStatus != 426 || re.ServerProtocol != 4 || re.ServerMinProtocol != 3 {
		t.Fatalf("426 error = %#v", err)
	}
}

// Against a 0.28 server, which serves only the released routes, a report
// falls back once to the 0.28 route with the 0.28 body, and the client then
// goes straight there.
func TestReportsFallBackToThe028Routes(t *testing.T) {
	var got []string
	var settings map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.Path)
		switch r.URL.Path {
		case componentproto.LegacyUsagePath:
		case componentproto.LegacyClientSettingsPath:
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &settings)
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := New(srv.URL+"/mcp", "tok").WithInsecureHTTP(true)
	if err := c.ReportUsage("kb-a", []int{}, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := c.ReportClientSettings(map[string]any{"machine": "m"}, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, has := settings["component"]; has || settings["machine"] != "m" {
		t.Fatalf("legacy body = %v, want the 0.28 shape", settings)
	}
	want := []string{"/api/component/v1/usage", "/api/usage", "/api/client-settings"}
	if len(got) != len(want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("calls = %v, want %v", got, want)
		}
	}
	// A server that has no route at all fails the same way, quietly.
	srv2 := httptest.NewServer(http.NotFoundHandler())
	defer srv2.Close()
	if err := New(srv2.URL+"/mcp", "").WithInsecureHTTP(true).ReportUsage("kb-a", []int{}, time.Second); err == nil {
		t.Fatal("a 404 on both routes must still be an error")
	}
}
