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

const settingsYAML = "name: team-a\nkbs:\n  - kb-a\ntoken_env: WORKER_TOKEN\nmodels:\n  - name: cloud\n    api_key_env: CLOUD_API_KEY\n"

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
	r := map[string]any{"perimeter": "team-a", "worker": "w-1", "version": "1.0", "yaml": settingsYAML,
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
		YAML      string `json:"yaml"`
		KBs       []string
		Entries   []struct {
			Key   string `json:"key"`
			Value any    `json:"value"`
		} `json:"entries"`
	} `json:"workers"`
	Template struct {
		Entries []workerconfig.Entry `json:"entries"`
		YAML    string               `json:"yaml"`
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
	if len(a.Workers) != 1 || a.Workers[0].Perimeter != "team-a" || a.Workers[0].Replicas != 1 || a.Workers[0].YAML != settingsYAML {
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
	yaml := "name: team-a\nkbs:\n  - kb-a\n  - kb-b\n"
	f.post(t, "worker-ab", beat(map[string]any{"kbs": []string{"kb-a", "kb-b"}}))
	f.postSettings("worker-ab", report(map[string]any{"yaml": yaml}))
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

func TestWorkerSettings_ScrubsSecrets(t *testing.T) {
	t.Parallel()
	f := newAtlasWorkerFixture(t)
	f.post(t, "worker-a", beat(nil))
	yaml := "kbs:\n  - kb-a\ntoken_env: sk-secret-value-123 # sk-secret-value-123\nserver_url: https://user:pw@example.com/mcp\nmodels:\n  - name: cloud\n    api_key_env: sk-other-secret\n# api_key_env: sk-commented\n"
	rr := f.postSettings("worker-a", report(map[string]any{"yaml": yaml, "entries": []map[string]any{
		{"key": "token_env", "value": "sk-secret-value-123", "default": "sk-def", "origin": "file"},
		{"key": "models", "value": []any{map[string]any{"api_key_env": "sk-other-secret", "name": "cloud"}}, "origin": "file"},
		{"key": "server_url", "value": "https://user:pw@example.com/mcp", "origin": "file"},
	}}))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	body := f.get("worker-a", "/settings").Body.String()
	for _, leak := range []string{"sk-secret-value-123", "sk-other-secret", "sk-commented", "sk-def", "user:pw"} {
		if strings.Contains(body, leak) {
			t.Errorf("settings view leaks %q", leak)
		}
	}
	if !strings.Contains(body, "redacted") {
		t.Error("nothing was redacted")
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
		{"token escaped in body", []byte(`{"perimeter":"team-a","yaml":"kbs: [kb-a]\\n","version":"\\u0077orker-a"}`), 400},
		{"token in body", report(map[string]any{"version": "worker-a"}), 400},
		{"bad yaml", report(map[string]any{"yaml": "a: [b"}), 400},
		{"empty yaml", report(map[string]any{"yaml": ""}), 400},
		{"merge key", report(map[string]any{"yaml": "x: &a {k: v}\ny:\n  <<: *a\nkbs: [kb-a]\n"}), 400},
		{"alias", report(map[string]any{"yaml": "x: &a v\nkbs: [kb-a]\ny: *a\n"}), 400},
		{"two documents", report(map[string]any{"yaml": "kbs: [kb-a]\n---\nkbs: [kb-b]\n"}), 400},
		{"bad perimeter", report(map[string]any{"perimeter": "Team A"}), 400},
		{"not an object", []byte(`[1]`), 400},
		{"oversize", append([]byte(`{"perimeter":"team-a","yaml":"`), bytes.Repeat([]byte("a"), maxWorkerSettingsBody)...), 413},
		{"kb not writable", report(map[string]any{"yaml": "kbs: [kb-b]\n"}), 404},
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
	want, _ := workerconfig.Schema.Format(nil)
	if a.Template.YAML != string(want) || len(a.Template.Entries) != len(workerconfig.Schema.Keys) || len(a.Workers) != 0 {
		t.Fatalf("template = %d entries, %d bytes, workers %d", len(a.Template.Entries), len(a.Template.YAML), len(a.Workers))
	}
}

func TestWorkerSettings_KeepsOnlyKBsThePrincipalMayWrite(t *testing.T) {
	t.Parallel()
	f := newAtlasWorkerFixture(t)
	f.post(t, "worker-a", beat(nil))
	f.postSettings("worker-a", report(map[string]any{"yaml": "kbs: [kb-a, kb-b, nope]\n"}))
	rows := f.multi.workers.settingsRows()
	if len(rows) != 1 || len(rows[0].KBs) != 1 || rows[0].KBs[0] != "kb-a" {
		t.Fatalf("rows = %+v", rows)
	}
}
