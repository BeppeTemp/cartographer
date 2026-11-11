package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/componentproto"
	"github.com/BeppeTemp/cartographer/internal/config"
)

// kindFixture adds tokens of every kind to the Atlas surveyor fixture.
func kindFixture(t *testing.T, specs ...config.ComponentSpec) *surveyorFixture {
	t.Helper()
	f := newSurveyorFixture(t)
	f.multi.EnableWeb(nil)
	f.multi.components.now = f.clock.now
	f.multi.SetComponents(specs)
	w := []auth.Permission{{KB: "kb-a", Write: true}}
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "surv", Principal: "surv", Kind: auth.KindSurveyor, Policy: auth.Policy{Permissions: w}},
		{Token: "cli", Principal: "cli", Kind: auth.KindClient, Policy: auth.Policy{Permissions: w}},
		{Token: "hum", Principal: "hum", Kind: auth.KindHuman, Policy: auth.Policy{Permissions: w}},
		{Token: "unset", Principal: "unset", Policy: auth.Policy{Permissions: w}},
		{Token: "reader", Principal: "reader", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-a"}}}},
		{Token: "other", Principal: "other", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-b"}}}},
	})
	f.h = ts.Middleware(f.multi.Handler())
	return f
}

type compAnswer struct {
	Protocol    int `json:"protocol"`
	MinProtocol int `json:"min_protocol"`
	Server      struct {
		Version   string `json:"version"`
		StartedAt string `json:"started_at"`
	} `json:"server"`
	Components []struct {
		Kind         string   `json:"kind"`
		Name         string   `json:"name"`
		State        string   `json:"state"`
		Declared     bool     `json:"declared"`
		KindDeclared bool     `json:"kind_declared"`
		MinReplicas  int      `json:"min_replicas"`
		KBs          []string `json:"kbs"`
		LastSeenAt   *string  `json:"last_seen_at"`
		Replicas     []struct {
			ID           string `json:"id"`
			Protocol     int    `json:"protocol"`
			ProtocolSkew string `json:"protocol_skew"`
			KindDeclared bool   `json:"kind_declared"`
		} `json:"replicas"`
	} `json:"components"`
}

func (f *surveyorFixture) components(t *testing.T, token string) (compAnswer, string) {
	t.Helper()
	rr := f.get(token, "/kbs/kb-a/components")
	if rr.Code != http.StatusOK {
		t.Fatalf("components: %d %s", rr.Code, rr.Body)
	}
	var a compAnswer
	if err := json.Unmarshal(rr.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	return a, rr.Body.String()
}

func TestUIComponents_ShapeWithAndWithoutRegistry(t *testing.T) {
	t.Parallel()
	// No registry, nothing live: an empty list, still the envelope.
	f := kindFixture(t)
	a, raw := f.components(t, "reader")
	if len(a.Components) != 0 || !strings.Contains(raw, `"components":[]`) || a.Protocol != componentproto.Protocol || a.MinProtocol != componentproto.MinProtocol || a.Server.StartedAt == "" {
		t.Fatalf("empty = %s", raw)
	}
	// No registry, a live Surveyor: shown as undeclared.
	f.beatOK(t, "surv", beat(nil))
	a, raw = f.components(t, "reader")
	if len(a.Components) != 1 || a.Components[0].State != componentproto.StateUndeclared || a.Components[0].Declared ||
		len(a.Components[0].Replicas) != 1 || a.Components[0].Replicas[0].ID != "w-1" || !a.Components[0].Replicas[0].KindDeclared || !a.Components[0].KindDeclared {
		t.Fatalf("undeclared = %s", raw)
	}
	// The principal and the token never leave the server.
	if strings.Contains(raw, `"surv"`) || strings.Contains(raw, "principal") {
		t.Fatalf("principal leaked: %s", raw)
	}

	// A registry: a silent expected component is present with no replicas.
	f = kindFixture(t, spec("team-a", 1, "kb-a", "kb-b"))
	f.clock.advance(componentGrace + time.Second)
	a, raw = f.components(t, "reader")
	c := a.Components[0]
	if len(a.Components) != 1 || c.State != componentproto.StateSilent || !c.Declared || c.MinReplicas != 1 || c.LastSeenAt != nil || c.Replicas == nil || len(c.Replicas) != 0 {
		t.Fatalf("silent = %s", raw)
	}
	// kb-b is not visible to this reader: its name is not revealed.
	if strings.Join(c.KBs, ",") != "kb-a" {
		t.Fatalf("kbs = %v", c.KBs)
	}
	f.beatOK(t, "unset", beat(map[string]any{"protocol": 2}))
	a, raw = f.components(t, "reader")
	c = a.Components[0]
	if c.State != componentproto.StateUp || len(c.Replicas) != 1 || c.Replicas[0].ProtocolSkew != componentproto.SkewAhead || c.Replicas[0].KindDeclared || c.KindDeclared {
		t.Fatalf("up, unset kind = %s", raw)
	}
}

func TestUIComponents_Visibility(t *testing.T) {
	t.Parallel()
	f := kindFixture(t)
	for _, c := range []struct{ token, path string }{
		{"other", "/kbs/kb-a/components"},
		{"reader", "/kbs/nope/components"},
		{"reader", "/kbs/kb-a/surveyors"},
	} {
		if rr := f.get(c.token, c.path); rr.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", c.token, c.path, rr.Code)
		}
	}
}

func (f *surveyorFixture) repairOf(t *testing.T) (state, owner, by string, hasSince bool) {
	t.Helper()
	rr := f.get("reader", "/kbs/kb-a/maintenance/summary")
	if rr.Code != http.StatusOK {
		t.Fatalf("summary: %d %s", rr.Code, rr.Body)
	}
	var s struct {
		Maintenance struct {
			Repair struct {
				Owner string  `json:"owner"`
				By    string  `json:"by"`
				State string  `json:"state"`
				Since *string `json:"since"`
			} `json:"repair"`
		} `json:"maintenance"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	r := s.Maintenance.Repair
	return r.State, r.Owner, r.By, r.Since != nil
}

func TestMaintenanceSummary_NamesWhoRepairs(t *testing.T) {
	t.Parallel()
	f := kindFixture(t, spec("team-a", 1, "kb-a"))
	if st, owner, by, since := f.repairOf(t); st != "server" || owner != "server" || by != "" || since {
		t.Fatalf("no surveyor: %s %s %q %v", st, owner, by, since)
	}
	// Handed over: a live Surveyor declaring repair, seen repairing.
	f.beatOK(t, "surv", repairBeat("w-1", nil))
	f.multi.servers["kb-a"].noteSurveyorRepair("surv")
	if st, owner, by, since := f.repairOf(t); st != "surveyor" || owner != "surveyor" || by != "team-a" || !since {
		t.Fatalf("handed over: %s %s %q %v", st, owner, by, since)
	}
	// It goes silent: the server repairs again, and says whose silence it is.
	f.clock.advance(componentGrace + time.Minute)
	if st, owner, by, since := f.repairOf(t); st != "fallback" || owner != "server" || by != "team-a" || !since {
		t.Fatalf("fallback: %s %s %q %v", st, owner, by, since)
	}
	// An expected Surveyor that never declared repair is no fallback.
	g := kindFixture(t, spec("team-a", 1, "kb-a"))
	g.beatOK(t, "surv", beat(nil))
	g.clock.advance(componentGrace + time.Minute)
	if st, _, _, _ := g.repairOf(t); st != "server" {
		t.Fatalf("no repair declared: %s", st)
	}
}

func TestComponentRoutes_KindMatrix(t *testing.T) {
	t.Parallel()
	f := kindFixture(t)
	settings := func(component string) map[string]any {
		if component == "surveyor" {
			return map[string]any{"component": "surveyor", "protocol": 1, "perimeter": "team-a", "kbs": []string{"kb-a"}, "entries": []any{}}
		}
		return map[string]any{"component": "client", "protocol": 1, "machine": "m-1"}
	}
	// 403 is the only status that matters here: whatever else the body does
	// (a 404, a 400) means the kind check let the token in.
	cases := []struct {
		route string
		do    func(token string) int
		deny  []string
	}{
		{"heartbeat", func(tok string) int { return f.post(t, tok, beat(nil)).Code }, []string{"cli"}},
		{"settings/surveyor", func(tok string) int {
			return rawJSONPost(f.h, componentproto.SettingsPath, tok, settings("surveyor")).Code
		}, []string{"cli"}},
		{"settings/client", func(tok string) int {
			return rawJSONPost(f.h, componentproto.SettingsPath, tok, settings("client")).Code
		}, []string{"surv"}},
		{"usage", func(tok string) int {
			return rawJSONPost(f.h, componentproto.UsagePath+"?kb=kb-a&protocol=1", tok, []any{}).Code
		}, []string{"surv"}},
	}
	for _, c := range cases {
		for _, tok := range []string{"surv", "cli", "hum", "unset"} {
			want := true
			for _, d := range c.deny {
				want = want && tok != d
			}
			got := c.do(tok)
			if (got != http.StatusForbidden) != want {
				t.Errorf("%s as %s = %d, allowed want %v", c.route, tok, got, want)
			}
		}
	}
	// The error code is the contract's.
	rr := f.post(t, "cli", beat(nil))
	if !strings.Contains(rr.Body.String(), componentproto.CodeWrongTokenKind) {
		t.Fatalf("body = %s", rr.Body)
	}
	// The auth-off local principal and the 0.28 aliases are not subject to it.
	req := httptest.NewRequest(http.MethodPost, componentproto.HeartbeatPath, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.ContextWithPrincipal(req.Context(), auth.LocalAdminPrincipal()))
	rec := httptest.NewRecorder()
	f.multi.handleComponent(rec, req)
	if rec.Code == http.StatusForbidden {
		t.Fatalf("local principal refused: %s", rec.Body)
	}
	if got := rawJSONPost(f.h, componentproto.LegacyUsagePath+"?kb=kb-a", "surv", []any{}).Code; got == http.StatusForbidden {
		t.Fatalf("legacy usage enforced a kind")
	}
}

func TestTouchActivity_ByKind(t *testing.T) {
	t.Parallel()
	f := kindFixture(t)
	s := f.multi.servers["kb-a"]
	// A declared surveyor is no activity even with no live declaration.
	s.touchActivity("surv", true, "kb_status")
	if _, ok := s.lastActivity(); ok {
		t.Fatal("a surveyor-kind call counted as activity")
	}
	// A token with no kind counts unless it has a live declaration.
	s.touchActivity("unset", false, "kb_status")
	if _, ok := s.lastActivity(); !ok {
		t.Fatal("a person's call did not count")
	}
}
