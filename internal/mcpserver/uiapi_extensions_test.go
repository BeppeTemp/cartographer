package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
)

func extPlugin(name, route string) *core.Plugin {
	return &core.Plugin{
		Manifest: core.Manifest{Name: name, Version: "1.0.0", DisplayName: "Team " + name, Description: "Forks for the team",
			Permissions: &core.Permissions{Net: []string{"secret-host.test"}, Env: []string{"SECRET_VAR"}}},
		Digest: strings.Repeat("c", 64),
		Contributions: map[string][]core.Contribution{
			"views": {{Point: "views", Name: "sources", View: &core.View{Title: "Sources", Icon: "sources", Scope: "kb", Limit: 100,
				Source: core.ViewSource{Route: route}, Renderer: "table", Columns: []core.ViewColumn{{Key: "name", Label: "Name", Format: "text"}}}}},
			"apps": {{Point: "apps", Name: "board", App: &core.App{Title: "Board", Icon: "paths", URL: "https://board.example.com/"}}},
		},
	}
}

func extHandler(t *testing.T, ts *auth.TokenStore, plugins ...*core.Plugin) http.Handler {
	t.Helper()
	multi := NewMultiKBServer("v-test")
	if err := multi.SetAtlasPlugins(plugins); err != nil {
		t.Fatal(err)
	}
	multi.EnableWeb(nil)
	return ts.Middleware(multi.Handler())
}

func TestUIAPI_ExtensionsEmptyAreArrays(t *testing.T) {
	t.Parallel()
	rr := getUI(t, extHandler(t, auth.NewTokenStore(nil)), UIAPIPrefix+"/extensions", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("%d %s", rr.Code, rr.Body)
	}
	for _, k := range []string{"views", "apps", "plugins"} {
		if a, ok := decodeUI(t, rr)[k].([]interface{}); !ok || len(a) != 0 {
			t.Errorf("%s = %v, want []", k, decodeUI(t, rr)[k])
		}
	}
}

func TestUIAPI_ExtensionsListsApprovedAndFiltersPlugins(t *testing.T) {
	t.Parallel()
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "tok-admin", Policy: auth.Policy{Admin: true}},
		{Token: "only-a", Scopes: []auth.KBScope{{KB: "kb-a"}}},
	})
	h := extHandler(t, ts, extPlugin("zeta", "/api/v1/kbs/{kb}/sources"), extPlugin("alpha", "/api/v1/kbs/{kb}/work"))
	admin := decodeUI(t, getUI(t, h, UIAPIPrefix+"/extensions", "tok-admin"))
	views := admin["views"].([]interface{})
	if len(views) != 2 || views[0].(map[string]interface{})["plugin"] != "alpha" {
		t.Fatalf("views = %v", views)
	}
	if src := views[0].(map[string]interface{})["source"].(map[string]interface{}); src["route"] != "/api/v1/kbs/{kb}/work" {
		t.Errorf("the route stays a template: %v", src)
	}
	if apps := admin["apps"].([]interface{}); len(apps) != 2 {
		t.Errorf("apps = %v", apps)
	}
	plugins := admin["plugins"].([]interface{})
	if len(plugins) != 2 {
		t.Fatalf("plugins = %v", plugins)
	}
	comp := plugins[0].(map[string]interface{})["components"].([]interface{})[0].(map[string]interface{})
	if comp["component"] != "meridian" || comp["state"] != "approved" || comp["digest"] != strings.Repeat("c", 64) {
		t.Errorf("component = %v", comp)
	}
	narrow := decodeUI(t, getUI(t, h, UIAPIPrefix+"/extensions", "only-a"))
	if len(narrow["views"].([]interface{})) != 2 || len(narrow["plugins"].([]interface{})) != 0 {
		t.Errorf("a narrowed token sees the views and no plugins: %v", narrow)
	}
}

func TestUIAPI_ExtensionsIsReadOnly(t *testing.T) {
	t.Parallel()
	h := extHandler(t, auth.NewTokenStore(nil), extPlugin("alpha", "/api/v1/kbs/{kb}/work"))
	head := httptest.NewRecorder()
	h.ServeHTTP(head, httptest.NewRequest(http.MethodHead, UIAPIPrefix+"/extensions", nil))
	if head.Code != http.StatusOK {
		t.Errorf("HEAD = %d", head.Code)
	}
	post := httptest.NewRecorder()
	h.ServeHTTP(post, httptest.NewRequest(http.MethodPost, UIAPIPrefix+"/extensions", nil))
	if post.Code != http.StatusMethodNotAllowed || !strings.Contains(post.Body.String(), "method_not_allowed") {
		t.Errorf("POST = %d %s", post.Code, post.Body)
	}
}

// The answer names the permission kinds the manifest declares and carries no
// value of them (host, variable, path).
func TestUIAPI_ExtensionsLeaksNoPermission(t *testing.T) {
	t.Parallel()
	p := extPlugin("alpha", "/api/v1/kbs/{kb}/work")
	p.Manifest.Components = map[core.Side]core.Component{core.SideAtlas: {}}
	body := getUI(t, extHandler(t, auth.NewTokenStore(nil), p), UIAPIPrefix+"/extensions", "").Body.String()
	if !strings.Contains(body, `"permissions":["net","env"]`) {
		t.Errorf("permission names missing: %s", body)
	}
	for _, banned := range []string{"secret-host.test", "SECRET_VAR", `"env":`, `"net":`} {
		if strings.Contains(body, banned) {
			t.Errorf("%q in the answer: %s", banned, body)
		}
	}
}

// A view may only present a stable or preview GET of the public API.
func TestAtlasViewsMustBePublicGET(t *testing.T) {
	t.Parallel()
	for route, ok := range map[string]bool{
		"/api/v1/kbs":                  true,
		"/api/v1/kbs/{kb}/work":        true,
		"/api/v1/kbs/{kb}/sources":     true,
		"/mcp":                         false,
		"/api/ui/v1/kbs":               false,
		"/api/v1/settings":             false, // internal tier
		"/api/v1/kbs/{kb}/components":  false, // internal tier
		"/api/v1/kbs/{kb}/audit":       false,
		"/api/v1/kbs/{kb}/events":      false, // a stream
		"/api/v1/kbs/{kb}/nonexistent": false,
		"/api/v1":                      false,
	} {
		err := ValidateAtlasPlugins([]*core.Plugin{extPlugin("alpha", route)})
		if (err == nil) != ok {
			t.Errorf("%s: err = %v, want accepted = %v", route, err, ok)
		}
		if err != nil && !strings.Contains(err.Error(), "views/sources.yaml: source.route") {
			t.Errorf("%s: the refusal does not name the key: %v", route, err)
		}
	}
	if err := (&MultiKBServer{}).SetAtlasPlugins([]*core.Plugin{extPlugin("alpha", "/mcp")}); err == nil {
		t.Error("SetAtlasPlugins accepted a view over /mcp")
	}
}

// Forms a browser normalises into another route must not pass the segment match.
func TestAtlasViewsRefuseNormalisedRoutes(t *testing.T) {
	t.Parallel()
	for _, route := range []string{
		`/api/v1/kbs/x\..\..\ui\v1\settings?/overview`,
		"/api/v1/kbs/{kb}/overview?x=1",
		"/api/v1/kbs/{kb}/overview#x",
		"/api/v1/kbs/%2e%2e/ui/v1/settings/overview",
		"/api/v1/kbs/{kb}/../overview",
		"/api/v1/kbs/./overview",
		"/api/v1/kbs/{kb}/over\tview",
		"/api/v1/kbs/{kb}/overview/",
		"/api/v1/kbs//overview",
		"/api/v1/kbs/{kb}x/overview",
		"/api/v1/kbs/{id}/overview",
	} {
		if err := ValidateAtlasPlugins([]*core.Plugin{extPlugin("alpha", route)}); err == nil {
			t.Errorf("%s: accepted", route)
		}
		// "{kb}x" becomes the plain segment "kb-ax" once expanded: only Validate's
		// shape check refuses it.
		if (publicRoutes{}).IsPublicGET(strings.ReplaceAll(route, "{kb}", "kb-a")) && !strings.Contains(route, "{kb}x") {
			t.Errorf("%s: IsPublicGET accepted it", route)
		}
	}
}

// The whole body shape, so a renamed field is a deliberate edit.
func TestUIAPI_ExtensionsBodyShape(t *testing.T) {
	t.Parallel()
	h := extHandler(t, auth.NewTokenStore(nil), extPlugin("alpha", "/api/v1/kbs/{kb}/work"))
	body := decodeUI(t, getUI(t, h, UIAPIPrefix+"/extensions", ""))
	keys := func(m map[string]interface{}) string {
		var out []string
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return strings.Join(out, ",")
	}
	if got := keys(body); got != "apps,generated_at,plugins,reported,views" {
		t.Errorf("body keys = %s", got)
	}
	view := body["views"].([]interface{})[0].(map[string]interface{})
	if got := keys(view); got != "columns,description,icon,limit,name,plugin,renderer,scope,source,title" {
		t.Errorf("view keys = %s", got)
	}
	if got := keys(body["apps"].([]interface{})[0].(map[string]interface{})); got != "description,icon,name,plugin,title,url" {
		t.Errorf("app keys = %s", got)
	}
	plugin := body["plugins"].([]interface{})[0].(map[string]interface{})
	if got := keys(plugin); got != "components,description,display_name,name,version" {
		t.Errorf("plugin keys = %s", got)
	}
	if got := keys(plugin["components"].([]interface{})[0].(map[string]interface{})); got != "approved,component,contributions,digest,installed,permissions,state,version" {
		t.Errorf("component keys = %s", got)
	}
}

func compassRows(t *testing.T, body map[string]interface{}) map[string][]map[string]interface{} {
	t.Helper()
	out := map[string][]map[string]interface{}{}
	for _, p := range body["plugins"].([]interface{}) {
		pm := p.(map[string]interface{})
		for _, c := range pm["components"].([]interface{}) {
			cm := c.(map[string]interface{})
			out[pm["name"].(string)] = append(out[pm["name"].(string)], cm)
		}
	}
	return out
}

// A Compass's plugins join the row Meridian pins by name, or get a row of
// their own, each tagged with the machine that reported it.
func TestUIAPI_ExtensionsMergesCompassPlugins(t *testing.T) {
	t.Parallel()
	multi := NewMultiKBServer("v-test")
	if err := multi.SetAtlasPlugins([]*core.Plugin{extPlugin("acme", "/api/v1/kbs/{kb}/work")}); err != nil {
		t.Fatal(err)
	}
	multi.EnableWeb(nil)
	multi.clientReports.put("local-admin", clientReport{Machine: "laptop-a", Plugins: []clientconfig.ReportPlugin{
		{Name: "acme", Version: "1.0.0", State: "active", Approved: "aaaaaaaaaaaa", Installed: "aaaaaaaaaaaa", Description: "Team forks",
			Contributions: map[string]int{"skills": 3, "agents": 2}, Permissions: []string{"net"}},
		{Name: "solo", Version: "0.2.0", DisplayName: "Solo", State: "pending approval", Installed: "bbbbbbbbbbbb"},
	}})
	// A report of another principal and one of an older client add nothing.
	multi.clientReports.put("someone-else", clientReport{Machine: "other", Plugins: []clientconfig.ReportPlugin{{Name: "secret", State: "active"}}})
	multi.clientReports.put("local-admin", clientReport{Machine: "old-client"})
	h := auth.NewTokenStore(nil).Middleware(multi.Handler())

	body := decodeUI(t, getUI(t, h, UIAPIPrefix+"/extensions", ""))
	rows := compassRows(t, body)
	if _, ok := rows["secret"]; ok {
		t.Fatalf("another principal's plugin leaked: %v", rows)
	}
	acme := rows["acme"]
	if len(acme) != 2 || acme[0]["component"] != "meridian" || acme[1]["component"] != "compass" || acme[1]["machine"] != "laptop-a" || acme[1]["state"] != "active" || acme[1]["digest"] != "aaaaaaaaaaaa" {
		t.Errorf("acme components = %v", acme)
	}
	if acme[1]["version"] != "1.0.0" || acme[1]["installed"] != "aaaaaaaaaaaa" || acme[1]["permissions"].([]interface{})[0] != "net" ||
		acme[1]["contributions"].(map[string]interface{})["skills"] != float64(3) {
		t.Errorf("acme compass detail = %v", acme[1])
	}
	if m := acme[0]; m["contributions"].(map[string]interface{})["views"] != float64(1) || m["permissions"].([]interface{})[0] != "net" {
		t.Errorf("acme meridian detail = %v", m)
	}
	solo := rows["solo"]
	if len(solo) != 1 || solo[0]["component"] != "compass" || solo[0]["state"] != "pending approval" || solo[0]["digest"] != "bbbbbbbbbbbb" {
		t.Errorf("solo components = %v", solo)
	}
	var desc string
	for _, p := range body["plugins"].([]interface{}) {
		if pm := p.(map[string]interface{}); pm["name"] == "acme" {
			desc, _ = pm["description"].(string)
		}
	}
	if desc == "" {
		t.Error("the row carries no description")
	}
	if got := body["reported"].([]interface{}); len(got) != 2 || got[1] != "compass" {
		t.Errorf("reported = %v", got)
	}
}

// Without a report that carries the field, only Meridian reported; and a
// narrowed token still gets no plugin, whatever was reported.
func TestUIAPI_ExtensionsReportedNamesWhoAnswered(t *testing.T) {
	t.Parallel()
	multi := NewMultiKBServer("v-test")
	multi.EnableWeb(nil)
	multi.clientReports.put("local-admin", clientReport{Machine: "old-client"})
	h := auth.NewTokenStore(nil).Middleware(multi.Handler())
	body := decodeUI(t, getUI(t, h, UIAPIPrefix+"/extensions", ""))
	if got := body["reported"].([]interface{}); len(got) != 1 || got[0] != "meridian" {
		t.Errorf("reported = %v", got)
	}
	if len(body["plugins"].([]interface{})) != 0 {
		t.Errorf("plugins = %v", body["plugins"])
	}
	multi.clientReports.put("local-admin", clientReport{Machine: "new", Plugins: []clientconfig.ReportPlugin{}})
	body = decodeUI(t, getUI(t, h, UIAPIPrefix+"/extensions", ""))
	if got := body["reported"].([]interface{}); len(got) != 2 {
		t.Errorf("an empty list from a new client still means it reported: %v", got)
	}
}
