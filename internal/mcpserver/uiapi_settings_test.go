package mcpserver

import (
	"net/http"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/config"
)

func settingsHandler(t *testing.T, cfg *config.Config, ts *auth.TokenStore, names ...string) http.Handler {
	t.Helper()
	multi := NewMultiKBServer("v-test")
	multi.SetLatestVersionSource(func() string { return "v-next" })
	multi.SetSettings(cfg, "homebrew")
	for _, name := range names {
		k := uiFixtureKB(t, name)
		multi.MountKB(name, func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	}
	multi.EnableWeb(nil)
	return ts.Middleware(multi.Handler())
}

func TestUIAPI_SettingsListsValuesOriginsAndFacts(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Web.Enabled = false
	cfg.Origins = map[string]config.Origin{"web.enabled": config.OriginEnv}
	handler := settingsHandler(t, cfg, auth.NewTokenStore(nil), "docs")
	rr := getUI(t, handler, UIAPIPrefix+"/settings", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	body := decodeUI(t, rr)
	if body["version"] != "v-test" || body["latest_version"] != "v-next" || body["update_channel"] != "homebrew" {
		t.Errorf("facts = %v", body)
	}
	var found bool
	for _, e := range body["server"].([]interface{}) {
		row := e.(map[string]interface{})
		if row["key"] == "web.enabled" {
			found = true
			if row["value"] != false || row["origin"] != "env" || row["description"] == "" {
				t.Errorf("web.enabled row = %v", row)
			}
		}
	}
	if !found {
		t.Error("web.enabled missing from the server section")
	}
	kbs := body["kbs"].([]interface{})
	if len(kbs) != 1 || kbs[0].(map[string]interface{})["name"] != "docs" {
		t.Fatalf("kbs = %v", kbs)
	}
	if _, ok := kbs[0].(map[string]interface{})["git"]; !ok {
		t.Error("per-KB git facts missing")
	}
	// The YAML views (D818): the server file with the env-set key and its
	// origin, and the KB's settings.yaml, both documented by the schema.
	if y, _ := body["server_yaml"].(string); !strings.Contains(y, "enabled: false # from CARTOGRAPHER_WEB_ENABLED") ||
		!strings.Contains(y, "# --- available settings: uncomment a key to set it ---") {
		t.Errorf("server_yaml = %q", y)
	}
	if y, _ := kbs[0].(map[string]interface{})["yaml"].(string); !strings.Contains(y, "# --- available settings: uncomment a key to set it ---") {
		t.Errorf("kb yaml = %q", y)
	}
}

// A viewer none of whose clients has reported still sees what a client can set
// (D948): client_template carries every key of the client schema at its default,
// the secrets masked, and the settings file they amount to, commented by the
// same schema that writes it.
func TestUIAPI_SettingsClientTemplate(t *testing.T) {
	t.Parallel()
	handler := settingsHandler(t, config.Default(), auth.NewTokenStore(nil), "docs")
	rr := getUI(t, handler, UIAPIPrefix+"/settings", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	body := decodeUI(t, rr)
	if clients, _ := body["clients"].([]interface{}); len(clients) != 0 {
		t.Fatalf("clients = %v, want none reported", body["clients"])
	}
	tmpl, ok := body["client_template"].(map[string]interface{})
	if !ok {
		t.Fatalf("client_template missing: %v", body)
	}
	entries, _ := tmpl["entries"].([]interface{})
	if len(entries) == 0 {
		t.Fatal("client_template has no entries")
	}
	byKey := map[string]map[string]interface{}{}
	for _, e := range entries {
		row := e.(map[string]interface{})
		if row["origin"] != "default" || row["description"] == "" {
			t.Errorf("entry %v: every key is at its default and documented", row)
		}
		byKey[row["key"].(string)] = row
	}
	if row := byKey["server_url"]; row == nil || row["value"] != row["default"] {
		t.Errorf("server_url = %v, want its default as value", row)
	}
	if row := byKey["token"]; row == nil || row["value"] != "(unset)" {
		t.Errorf("token = %v, want the masked marker", row)
	}
	if y, _ := tmpl["yaml"].(string); !strings.Contains(y, "# --- available settings: uncomment a key to set it ---") || !strings.Contains(y, "server_url") {
		t.Errorf("client_template yaml = %q", y)
	}
}

func TestUIAPI_SettingsRedactsSecrets(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Auth.Tokens = []config.TokenSpec{{Token: "tok-secret-1"}}
	cfg.KBs = []config.KBSpec{{Path: "/x/docs"}}
	handler := settingsHandler(t, cfg, auth.NewTokenStore(nil), "docs")
	rr := getUI(t, handler, UIAPIPrefix+"/settings", "")
	for _, secret := range []string{"tok-secret-1"} {
		if strings.Contains(rr.Body.String(), secret) {
			t.Errorf("secret %q is in the response", secret)
		}
	}
	if !strings.Contains(rr.Body.String(), `"(set)"`) {
		t.Errorf("redacted marker missing: %s", rr.Body)
	}
}

func TestUIAPI_SettingsOmitsInvisibleKB(t *testing.T) {
	t.Parallel()
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "only-x", Scopes: []auth.KBScope{{KB: "kbx", Write: false}}},
	})
	cfg := config.Default()
	cfg.KBs = []config.KBSpec{{Name: "kbx", Path: "/x/kbx"}, {Name: "kby", Path: "/x/kby"}}
	handler := settingsHandler(t, cfg, ts, "kbx", "kby")
	if rr := getUI(t, handler, UIAPIPrefix+"/settings", ""); rr.Code != http.StatusUnauthorized {
		t.Errorf("no token: status %d, want 401", rr.Code)
	}
	body := decodeUI(t, getUI(t, handler, UIAPIPrefix+"/settings", "only-x"))
	kbs := body["kbs"].([]interface{})
	if len(kbs) != 1 || kbs[0].(map[string]interface{})["name"] != "kbx" {
		t.Fatalf("kbs = %v", kbs)
	}
	// Nor the server YAML's kbs[] entries.
	if y, _ := body["server_yaml"].(string); strings.Contains(y, "kby") {
		t.Errorf("server_yaml names the invisible KB:\n%s", y)
	}
	// The server section's kbs list must not name the invisible KB either.
	for _, e := range body["server"].([]interface{}) {
		row := e.(map[string]interface{})
		if row["key"] == "kbs" {
			if got, _ := row["value"].([]interface{}); len(got) != 1 || got[0] != "kbx" {
				t.Errorf("server kbs = %v, want [kbx]", row["value"])
			}
		}
	}
}

// The capabilities kb_status reports name the setting that controls each: every
// one must be a key of the schema (the server's, or the KB's own), so the two cannot drift (D743).
func TestKBCapabilitySettingsExistInSchema(t *testing.T) {
	t.Parallel()
	k := uiFixtureKB(t, "docs")
	for name, c := range kbCapabilities(k) {
		for _, setting := range strings.Split(c.Setting, " or ") {
			setting = strings.TrimSuffix(setting, " (ignored)")
			if setting == "kbs[]" { // the list itself is the key "kbs"
				setting = "kbs"
			}
			if key, ok := strings.CutPrefix(setting, "settings.yaml "); ok {
				found := false
				for _, k := range config.KBKeys {
					found = found || k.Path == key
				}
				if !found {
					t.Errorf("capability %s names %q, which is not a key of config.KBKeys", name, setting)
				}
				continue
			}
			if _, ok := config.Schema.Lookup(setting); !ok {
				t.Errorf("capability %s names %q, which is not in config.Schema", name, setting)
			}
		}
	}
}
