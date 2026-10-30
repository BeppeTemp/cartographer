package mcpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/workerconfig"
)

func (f *workerFixture) postSettings(token string, body any) *httptest.ResponseRecorder {
	raw, ok := body.([]byte)
	if !ok {
		raw, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(http.MethodPost, WorkerSettingsPath, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	f.h.ServeHTTP(rr, req)
	return rr
}

func report(over map[string]any) map[string]any {
	r := map[string]any{"perimeter": "team-a", "worker": "w-1", "version": "1.0", "kbs": []string{"kb-a"},
		"yaml":    "ignored: on the wire, never stored",
		"entries": []map[string]any{{"key": "token_env", "value": "WORKER_TOKEN", "origin": "file"}}}
	for k, v := range over {
		r[k] = v
	}
	return r
}

type settingsAnswer struct {
	Workers []struct {
		Perimeter string `json:"perimeter"`
		Replicas  int    `json:"replicas"`
		KBs       []string
		Entries   []struct {
			Key   string `json:"key"`
			Value any    `json:"value"`
		} `json:"entries"`
	} `json:"workers"`
	Template struct {
		Entries []workerconfig.Entry `json:"entries"`
	} `json:"worker_template"`
}

func (f *workerFixture) settings(t *testing.T, token string) settingsAnswer {
	t.Helper()
	rr := f.get(token, "/settings")
	if rr.Code != http.StatusOK {
		t.Fatalf("settings status %d: %s", rr.Code, rr.Body.String())
	}
	var a settingsAnswer
	if err := json.Unmarshal(rr.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	return a
}

func wanted(t *testing.T, rr *httptest.ResponseRecorder) bool {
	t.Helper()
	var a struct {
		Wanted bool `json:"settings_wanted"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	return a.Wanted
}

func TestWorkerSettings_ShownOnlyWithALiveDeclaration(t *testing.T) {
	t.Parallel()
	f := newAtlasWorkerFixture(t)
	if rr := f.postSettings("worker-a", report(nil)); rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	// A report with no declaration behind it is not shown.
	if n := len(f.settings(t, "worker-a").Workers); n != 0 {
		t.Fatalf("%d rows without a declaration", n)
	}
	// Re-posting after the first beat: the report survives while it is live.
	if !wanted(t, f.post(t, "worker-a", beat(nil))) {
		t.Fatal("settings_wanted must be true while no report is held")
	}
	f.postSettings("worker-a", report(nil))
	if wanted(t, f.post(t, "worker-a", beat(nil))) {
		t.Fatal("settings_wanted must be false once the report is held")
	}
	a := f.settings(t, "worker-a")
	if len(a.Workers) != 1 || a.Workers[0].Perimeter != "team-a" || a.Workers[0].Replicas != 1 {
		t.Fatalf("rows = %+v", a.Workers)
	}
	f.clock.advance(time.Hour)
	if n := len(f.settings(t, "worker-a").Workers); n != 0 {
		t.Fatalf("%d rows with a stale declaration", n)
	}
	if !wanted(t, f.post(t, "worker-a", beat(nil))) {
		t.Fatal("settings_wanted must flip back after the declaration expired")
	}
}

func TestWorkerSettings_ReplicasShareARow(t *testing.T) {
	t.Parallel()
	f := newAtlasWorkerFixture(t)
	f.post(t, "worker-a", beat(map[string]any{"worker": "w-1"}))
	f.post(t, "worker-a", beat(map[string]any{"worker": "w-2"}))
	f.postSettings("worker-a", report(nil))
	f.postSettings("worker-a", report(map[string]any{"version": "1.1"}))
	a := f.settings(t, "worker-a")
	if len(a.Workers) != 1 || a.Workers[0].Replicas != 2 {
		t.Fatalf("rows = %+v", a.Workers)
	}
}

func TestWorkerSettings_PerimeterNeedsEveryKBWhole(t *testing.T) {
	t.Parallel()
	f := newAtlasWorkerFixture(t)
	f.post(t, "worker-ab", beat(map[string]any{"kbs": []string{"kb-a", "kb-b"}}))
	f.postSettings("worker-ab", report(map[string]any{"kbs": []string{"kb-a", "kb-b"}}))
	if n := len(f.settings(t, "worker-ab").Workers); n != 1 {
		t.Fatalf("a caller seeing both KBs got %d rows", n)
	}
	if n := len(f.settings(t, "reader").Workers); n != 0 {
		t.Fatalf("a caller seeing kb-a only got %d rows (kb-b names must not leak)", n)
	}
	if n := len(f.settings(t, "narrow").Workers); n != 0 {
		t.Fatalf("a narrowed caller got %d rows", n)
	}
}

func TestWorkerSettings_ReturnsNoTraceOfASecret(t *testing.T) {
	t.Parallel()
	f := newAtlasWorkerFixture(t)
	f.post(t, "worker-a", beat(nil))
	ent := func(k string, v any) map[string]any {
		return map[string]any{"key": k, "value": v, "origin": "file", "description": "S3CRET", "default": "S3CRET"}
	}
	rr := f.postSettings("worker-a", report(map[string]any{
		"yaml": "token: S3CRET\nYAMLMARK: 1\n", // accepted and ignored: never stored, never returned
		"entries": []map[string]any{
			ent("token_env", "sk-S3CRET"), ent("token", "S3CRET"), ent("S3CRET", "x"), ent("name", float64(1)),
			ent("models", []any{map[string]any{"S3CRET": "x"}}), ent("kbs[]", "S3CRET"), ent("schedule.poll_seconds", "S3CRET"),
			ent("server_url", "https://user:S3CRET@example.com/mcp"), ent("models[].provider", "S3CRET"),
			ent("models", map[string]any{"S3CRET": "x"}), ent("models.api_key", "S3CRET"),
		},
	}))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	body := f.get("worker-a", "/settings").Body.String()
	if strings.Contains(body, "S3CRET") || strings.Contains(body, "YAMLMARK") {
		t.Error("settings view leaks a secret or the ignored yaml")
	}
	if !strings.Contains(body, "redacted") {
		t.Error("the URL was not redacted")
	}
	if a := f.settings(t, "worker-a"); len(a.Workers) != 1 || len(a.Workers[0].Entries) != 2 {
		t.Errorf("kept entries = %+v", a.Workers)
	}
}

func TestWorkerSettings_Rejections(t *testing.T) {
	t.Parallel()
	f := newAtlasWorkerFixture(t)
	cases := []struct {
		name string
		body any
		code int
	}{
		{"token escaped in body", []byte(`{"perimeter":"team-a","kbs":["kb-a"],"version":"\u0077orker-a"}`), 400},
		{"token in body", report(map[string]any{"version": "worker-a"}), 400},
		{"bad perimeter", report(map[string]any{"perimeter": "Team A"}), 400},
		{"not an object", []byte(`[1]`), 400},
		{"oversize", append([]byte(`{"perimeter":"team-a","yaml":"`), bytes.Repeat([]byte("a"), maxWorkerSettingsBody)...), 413},
		{"no kbs", report(map[string]any{"kbs": []string{}}), 400},
		{"kb not writable", report(map[string]any{"kbs": []string{"kb-b"}}), 404},
	}
	for _, c := range cases {
		if rr := f.postSettings("worker-a", c.body); rr.Code != c.code {
			t.Errorf("%s: status %d, want %d (%s)", c.name, rr.Code, c.code, rr.Body.String())
		}
	}
	if rr := f.postSettings("reader", report(nil)); rr.Code != http.StatusNotFound {
		t.Errorf("read-only token: status %d, want 404", rr.Code)
	}
	req := httptest.NewRequest(http.MethodGet, WorkerSettingsPath, nil)
	req.Header.Set("Authorization", "Bearer worker-a")
	rr := httptest.NewRecorder()
	f.h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed || rr.Header().Get("Allow") != "POST" {
		t.Errorf("GET: status %d allow %q", rr.Code, rr.Header().Get("Allow"))
	}
	req = httptest.NewRequest(http.MethodPost, WorkerSettingsPath, strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer worker-a")
	req.Header.Set("Content-Type", "text/plain")
	rr = httptest.NewRecorder()
	f.h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Errorf("wrong type: status %d", rr.Code)
	}
	req = httptest.NewRequest(http.MethodPost, WorkerSettingsPath, strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer worker-a")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example.com")
	rr = httptest.NewRecorder()
	OriginGuard(f.h).ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("cross-origin: status %d", rr.Code)
	}
}

func TestWorkerSettings_EvictionBounds(t *testing.T) {
	t.Parallel()
	var s workerStore
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	for i := 0; i < 70; i++ {
		p := string(rune('a'+i%7)) + "-p"
		per := "per-" + string(rune('a'+i/7)) + string(rune('a'+i%7))
		now = now.Add(time.Second)
		s.put(workerDecl{Principal: p, Worker: per, Perimeter: per, IntervalSeconds: 3600, ReceivedAt: now})
		s.putSettings(p, per, workerSettings{ReceivedAt: now})
	}
	total := 0
	for p, m := range s.settings {
		if len(m) > maxWorkerSettingsPerPrincipal {
			t.Errorf("%s holds %d", p, len(m))
		}
		total += len(m)
	}
	if total > maxWorkerSettingsTotal {
		t.Errorf("total %d", total)
	}
}

func TestWorkerSettings_TemplateAlwaysPresent(t *testing.T) {
	t.Parallel()
	f := newAtlasWorkerFixture(t)
	a := f.settings(t, "worker-a")
	if len(a.Template.Entries) != len(workerconfig.Template()) || len(a.Workers) != 0 {
		t.Fatalf("template = %d entries, workers %d", len(a.Template.Entries), len(a.Workers))
	}
}

func TestWorkerSettings_KeepsOnlyKBsThePrincipalMayWrite(t *testing.T) {
	t.Parallel()
	f := newAtlasWorkerFixture(t)
	f.post(t, "worker-a", beat(nil))
	f.postSettings("worker-a", report(map[string]any{"kbs": []string{"kb-a", "kb-b", "nope"}}))
	rows := f.multi.workers.settingsRows()
	if len(rows) != 1 || len(rows[0].KBs) != 1 || rows[0].KBs[0] != "kb-a" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestWorkerSettings_VersionIsAShortTokenOrDropped(t *testing.T) {
	t.Parallel()
	f := newAtlasWorkerFixture(t)
	f.post(t, "worker-a", beat(nil))
	for version, want := range map[string]string{"v1.2.3-rc+4": "v1.2.3-rc+4", "S3CRET sk=1": "", "<b>x</b>": "", strings.Repeat("a", 33): ""} {
		f.postSettings("worker-a", report(map[string]any{"version": version}))
		rows := f.multi.workers.settingsRows()
		if len(rows) != 1 || rows[0].Version != want {
			t.Errorf("version %q -> %+v, want %q", version, rows, want)
		}
	}
}
