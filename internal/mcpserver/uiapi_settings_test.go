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
	cfg.Git.Sync = false
	cfg.Origins = map[string]config.Origin{"git.sync": config.OriginEnv}
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
		if row["key"] == "git.sync" {
			found = true
			if row["value"] != false || row["origin"] != "env" || row["description"] == "" {
				t.Errorf("git.sync row = %v", row)
			}
		}
	}
	if !found {
		t.Error("git.sync missing from the server section")
	}
	kbs := body["kbs"].([]interface{})
	if len(kbs) != 1 || kbs[0].(map[string]interface{})["name"] != "docs" {
		t.Fatalf("kbs = %v", kbs)
	}
	if _, ok := kbs[0].(map[string]interface{})["git"]; !ok {
		t.Error("per-KB git facts missing")
	}
}

func TestUIAPI_SettingsRedactsSecrets(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Auth.Tokens = []config.TokenSpec{{Token: "tok-secret-1"}}
	cfg.Audit.KeySeed = "audit-secret-2"
	cfg.KBs = []config.KBSpec{{Path: "/x/docs", ArtifactSigningSeed: "seed-secret-3"}}
	handler := settingsHandler(t, cfg, auth.NewTokenStore(nil), "docs")
	rr := getUI(t, handler, UIAPIPrefix+"/settings", "")
	for _, secret := range []string{"tok-secret-1", "audit-secret-2", "seed-secret-3"} {
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
// one must be a key of the schema, so the two cannot drift (D743).
func TestKBCapabilitySettingsExistInSchema(t *testing.T) {
	t.Parallel()
	k := uiFixtureKB(t, "docs")
	for name, c := range kbCapabilities(k) {
		for _, setting := range strings.Split(c.Setting, " or ") {
			setting = strings.TrimSuffix(setting, " (ignored)")
			if setting == "kbs[]" { // the list itself is the key "kbs"
				setting = "kbs"
			}
			if _, ok := config.Schema.Lookup(setting); !ok {
				t.Errorf("capability %s names %q, which is not in config.Schema", name, setting)
			}
		}
	}
}
