package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
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
