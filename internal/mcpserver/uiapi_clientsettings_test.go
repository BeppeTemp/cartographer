package mcpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/config"
)

// A client's settings report comes back on GET /settings to the principal
// that sent it, and to nobody else (D818).
func TestClientSettingsAreShownToTheirOwnPrincipalOnly(t *testing.T) {
	t.Parallel()
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "tok-a", Principal: "user-a", Policy: auth.Policy{Admin: true}},
		{Token: "tok-b", Principal: "user-b", Policy: auth.Policy{Admin: true}},
	})
	handler := settingsHandler(t, config.Default(), ts, "docs")

	post := func(token, body string) int {
		req := httptest.NewRequest(http.MethodPost, ClientSettingsPath, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr.Code
	}
	if code := post("tok-a", `{"machine":"laptop","version":"v1","entries":[{"key":"trust","value":true,"origin":"default"}],"yaml":"trust: true\n"}`); code != http.StatusOK {
		t.Fatalf("report status %d", code)
	}
	if code := post("tok-a", `{"version":"v1"}`); code != http.StatusBadRequest {
		t.Errorf("a report without a machine: status %d, want 400", code)
	}

	clients := func(token string) []interface{} {
		rr := getUI(t, handler, UIAPIPrefix+"/settings", token)
		if rr.Code != http.StatusOK {
			t.Fatalf("settings status %d: %s", rr.Code, rr.Body)
		}
		return decodeUI(t, rr)["clients"].([]interface{})
	}
	mine := clients("tok-a")
	if len(mine) != 1 || mine[0].(map[string]interface{})["machine"] != "laptop" {
		t.Fatalf("user-a clients = %v", mine)
	}
	if theirs := clients("tok-b"); len(theirs) != 0 {
		t.Errorf("user-b sees another principal's client: %v", theirs)
	}
}

func TestClientReportsKeepTheNewestMachinesPerPrincipal(t *testing.T) {
	t.Parallel()
	var c clientReports
	for i := 0; i < maxClientReportsPerPrincipal+2; i++ {
		c.put("p", clientReport{Machine: string(rune('a' + i)), ReceivedAt: time.Date(2026, 1, 1+i, 0, 0, 0, 0, time.UTC)})
	}
	got := c.of("p")
	if len(got) != maxClientReportsPerPrincipal || got[0].Machine != "c" {
		t.Fatalf("kept %d, first %q", len(got), got[0].Machine)
	}
}

func postClientSettings(t *testing.T, handler http.Handler, token string, report map[string]any) {
	t.Helper()
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > maxClientSettingsBody {
		t.Fatalf("test report is %d bytes, over the %d the server accepts", len(body), maxClientSettingsBody)
	}
	req := httptest.NewRequest(http.MethodPost, ClientSettingsPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("report status %d: %s", rr.Code, rr.Body)
	}
}

func onlyClient(t *testing.T, handler http.Handler, token string) (map[string]any, *httptest.ResponseRecorder) {
	t.Helper()
	rr := getUI(t, handler, UIAPIPrefix+"/settings", token)
	if rr.Code != http.StatusOK {
		t.Fatalf("settings status %d: %s", rr.Code, rr.Body)
	}
	clients := decodeUI(t, rr)["clients"].([]any)
	if len(clients) != 1 {
		t.Fatalf("clients = %v", clients)
	}
	return clients[0].(map[string]any), rr
}

// A file that repeats a key renders to many times its size (one commented
// block per occurrence), and any token may post one: it is rendered once, on
// receipt, and kept as it is when the rendering outgrows it, so a read costs
// what was posted, not what a file can be made to render to.
func TestClientSettingsRepeatedKeyIsStoredRawAndBoundsTheRead(t *testing.T) {
	t.Parallel()
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{{Token: "tok-a", Principal: "user-a", Policy: auth.Policy{Admin: true}}})
	handler := settingsHandler(t, config.Default(), ts, "docs")

	posted := strings.Repeat("server_url: a\n", 8000)
	postClientSettings(t, handler, "tok-a", map[string]any{"machine": "laptop", "yaml": posted})

	// Without the bound this renders to megabytes.
	if rendered, err := clientconfig.Schema.Format([]byte(posted)); err != nil || len(rendered) < 4*len(posted) {
		t.Fatalf("the fixture does not blow up: %d bytes for %d (%v)", len(rendered), len(posted), err)
	}
	client, rr := onlyClient(t, handler, "tok-a")
	if got := client["yaml"].(string); got != posted {
		t.Errorf("yaml is %d bytes, want the posted %d kept as they are", len(got), len(posted))
	}
	if limit := 2*len(posted) + 64<<10; rr.Body.Len() > limit {
		t.Errorf("GET /settings is %d bytes for a %d-byte report, want at most %d", rr.Body.Len(), len(posted), limit)
	}
}

// An ordinary file is rendered once, on receipt, as the page showed it before:
// the keys it sets documented, the others listed commented out.
func TestClientSettingsAreRenderedOnceOnReceipt(t *testing.T) {
	t.Parallel()
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{{Token: "tok-a", Principal: "user-a", Policy: auth.Policy{Admin: true}}})
	handler := settingsHandler(t, config.Default(), ts, "docs")
	for _, posted := range []string{"", "update_check: false\n"} {
		postClientSettings(t, handler, "tok-a", map[string]any{"machine": "laptop", "yaml": posted})
		client, _ := onlyClient(t, handler, "tok-a")
		got := client["yaml"].(string)
		if !strings.Contains(got, "# --- available settings: uncomment a key to set it ---") || !strings.Contains(got, strings.TrimSpace(posted)) {
			t.Errorf("posted %q, stored %q", posted, got)
		}
	}
}

// docs/control-plane.md promises a secret's value is never returned. A client
// that predates the masking sends the token in the entries and in the file;
// the server masks both before it keeps them, whichever way the file writes it.
func TestClientSettingsNeverReturnASecretValue(t *testing.T) {
	t.Parallel()
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{{Token: "tok-a", Principal: "user-a", Policy: auth.Policy{Admin: true}}})
	handler := settingsHandler(t, config.Default(), ts, "docs")

	files := map[string]string{
		"plain":      "token: s3cret\nserver_url: http://example.com/mcp\n",
		"quoted":     "token: \"s3cret\"\n",
		"flow":       "{token: s3cret, auth: true}\n",
		"block":      "token: |\n  s3cret\n",
		"anchored":   "token: &t s3cret\nserver_name: *t\n",
		"alias":      "other: &t s3cret\ntoken: *t\n",
		"duplicated": "token: first\ntoken: s3cret\n",
		"reference":  "token: ${S3CRET_VAR}\n",
		"commented":  "# the bearer token\ntoken: s3cret # s3cret\n",
		"old token":  "# token: s3cret-old\ntoken: ${CARTOGRAPHER_TOKENS}\nauth: true # token: s3cret\n",
		"not yaml":   "token: s3cret\n  - : [\n",
	}
	for name, file := range files {
		postClientSettings(t, handler, "tok-a", map[string]any{
			"machine": "laptop",
			"entries": []map[string]any{
				{"key": "token", "value": "s3cret", "origin": "file", "default": "s3cret"},
				{"key": "auth", "value": true, "origin": "file"},
			},
			"yaml": file,
		})
		client, rr := onlyClient(t, handler, "tok-a")
		body := rr.Body.String()
		for _, secret := range []string{"s3cret", "S3CRET"} {
			if strings.Contains(body, secret) {
				t.Errorf("%s: %q is in the response", name, secret)
			}
		}
		entries := client["entries"].([]any)
		token := entries[0].(map[string]any)
		if token["value"] != "(set)" || token["secret"] != true || token["default"] == "s3cret" {
			t.Errorf("%s: token entry = %v", name, token)
		}
		if other := entries[1].(map[string]any); other["value"] != true {
			t.Errorf("%s: a key that is no secret was changed: %v", name, other)
		}
		if y := client["yaml"].(string); name != "not yaml" && !strings.Contains(y, "<redacted>") {
			t.Errorf("%s: yaml = %q", name, y)
		}
	}

	// A token the file does not set is "(unset)", and an empty one shows no
	// "<redacted>" that would say otherwise.
	postClientSettings(t, handler, "tok-a", map[string]any{
		"machine": "laptop",
		"entries": []map[string]any{{"key": "token", "value": "${CARTOGRAPHER_TOKENS}", "origin": "default"}},
		"yaml":    "token: \"\"\n",
	})
	client, _ := onlyClient(t, handler, "tok-a")
	if v := client["entries"].([]any)[0].(map[string]any)["value"]; v != "(unset)" {
		t.Errorf("default token entry = %v", v)
	}
	if y := client["yaml"].(string); strings.Contains(y, "<redacted>") {
		t.Errorf("an empty token is shown redacted: %q", y)
	}
}
