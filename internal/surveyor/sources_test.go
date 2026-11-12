package surveyor

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/surveyor/connector"
	"github.com/BeppeTemp/cartographer/internal/surveyor/recipe"
	"github.com/BeppeTemp/cartographer/internal/surveyorconfig"
)

const sourcesDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// sourcesConfig has one of each: an operator exec recipe, a plugin Starlark
// recipe, a plugin connector, and fields a report must never carry.
func sourcesConfig() *Config {
	return &Config{
		Checks: NewCheckLog(),
		ExecRecipes: []recipe.ExecConfig{{
			Name: "wiki-lister", Command: "/opt/tools/lister", Args: []string{"--token=sk-live-secret-arg", "https://user:pw-in-url@example.com/x?key=query-secret"},
			Env: map[string]string{"WIKI_TOKEN": "SURVEYOR_SECRET_VAR"}, Instructions: "instructions-secret-text",
			Settings: map[string]any{"password": "settings-secret"},
		}},
		StarRecipes: []recipe.StarConfig{{
			ExecConfig: recipe.ExecConfig{Name: "acme/pages", Shape: recipe.ShapeSystem, Env: map[string]string{"ACME_TOKEN": "SURVEYOR_ACME_VAR"}},
			Plugin:     "acme", Local: "pages",
		}},
		Connectors: []connector.Config{{
			Name: "acme/wiki-mcp", Transport: connector.TransportHTTP, URL: "https://example.com/mcp?token=url-secret",
			TokenEnv: "SURVEYOR_ACME_VAR", Headers: map[string]string{"X-Api-Key": "SURVEYOR_HEADER_VAR"}, Env: map[string]string{"A": "SURVEYOR_SECRET_VAR"},
		}},
		PluginPins: map[string]PluginPin{"acme": {Digest: sourcesDigest, Enabled: true}},
	}
}

func TestSourcesReportGolden(t *testing.T) {
	c := sourcesConfig()
	now := time.Date(2031, 2, 3, 12, 0, 0, 0, time.UTC)
	c.Checks.Record("wiki-lister", errors.New("boom"), now.Add(-90*time.Second))
	c.Checks.Record("acme/pages", nil, now.Add(-5*time.Second))
	raw, err := json.Marshal(c.Sources(now))
	if err != nil {
		t.Fatal(err)
	}
	const d = sourcesDigest
	want := `[` +
		`{"name":"acme/pages","origin":"plugin","plugin":"acme","digest":"` + d + `","kind":"starlark","shape":"system","check":{"outcome":"ok","age_seconds":5}},` +
		`{"name":"acme/wiki-mcp","origin":"plugin","plugin":"acme","digest":"` + d + `","kind":"mcp","check":{"outcome":"unsupported"}},` +
		`{"name":"git-docs","origin":"core","kind":"builtin","shape":"feed","check":{"outcome":"unsupported"}},` +
		`{"name":"http-pages","origin":"core","kind":"builtin","shape":"feed","check":{"outcome":"unsupported"}},` +
		`{"name":"wiki-lister","origin":"operator","kind":"exec","shape":"feed","check":{"outcome":"failed","code":"check_failed","age_seconds":90}}]`
	if string(raw) != want {
		t.Errorf("sources =\n%s\nwant\n%s", raw, want)
	}
	// TRAP pinned: nothing of a source but names, origins and outcomes is sent.
	for _, secret := range []string{"sk-live-secret-arg", "pw-in-url", "query-secret", "url-secret", "SURVEYOR_SECRET_VAR", "SURVEYOR_ACME_VAR", "SURVEYOR_HEADER_VAR",
		"WIKI_TOKEN", "X-Api-Key", "/opt/tools", "instructions-secret", "settings-secret", "boom"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("the report carries %q: %s", secret, raw)
		}
	}
	// What the sources hold survives the server's scrub whole.
	var back []surveyorconfig.Source
	_ = json.Unmarshal(raw, &back)
	if got := surveyorconfig.ScrubSources(back, now); len(got) != 5 {
		t.Errorf("the server would keep %d of 5 sources", len(got))
	}
}

// A recipe and a connector of one name (a plugin's recipes/wiki and
// connectors/wiki.yaml) are both reported, the recipe first, in the same order
// whatever order the config lists them in, and the server keeps both.
func TestSourcesKeepsARecipeAndAConnectorOfTheSameName(t *testing.T) {
	c := &Config{
		Checks:      NewCheckLog(),
		ExecRecipes: []recipe.ExecConfig{{Name: "acme/wiki"}},
		Connectors:  []connector.Config{{Name: "acme/wiki", Transport: connector.TransportHTTP, URL: "https://example.com/mcp"}},
		PluginPins:  map[string]PluginPin{"acme": {Digest: sourcesDigest, Enabled: true}},
	}
	now := time.Now()
	for i := 0; i < 20; i++ { // the sort is stable, not luck
		got := c.Sources(now)
		var wiki []string
		for _, s := range got {
			if s.Name == "acme/wiki" {
				wiki = append(wiki, s.Kind)
			}
		}
		if len(wiki) != 2 || wiki[0] != surveyorconfig.KindExec || wiki[1] != surveyorconfig.KindMCP {
			t.Fatalf("acme/wiki kinds = %v, want [exec mcp]", wiki)
		}
		if kept := surveyorconfig.ScrubSources(got, now); len(kept) != len(got) {
			t.Fatalf("the server keeps %d of %d", len(kept), len(got))
		}
	}
}

func TestSourcesNeverCheckedAndNilLog(t *testing.T) {
	c := sourcesConfig()
	c.Checks = nil // a Config built without a log reports never
	for _, s := range c.Sources(time.Now()) {
		if s.Kind != surveyorconfig.KindExec && s.Kind != surveyorconfig.KindStarlark {
			continue
		}
		if s.Check.Outcome != surveyorconfig.CheckNever {
			t.Errorf("%s: outcome %q, want never", s.Name, s.Check.Outcome)
		}
	}
	var l *CheckLog
	l.Record("x", nil, time.Now()) // nil-safe
	if l.Generation() != 0 {
		t.Error("a nil log has a generation")
	}
}

func TestCheckLogRecordsEachOutcome(t *testing.T) {
	now := time.Date(2031, 2, 3, 12, 0, 0, 0, time.UTC)
	l := NewCheckLog()
	l.Record("a", nil, now)
	l.Record("b", recipe.ErrUnsupported, now)
	l.Record("c", &recipe.Error{Code: recipe.CodeIdentityNotReadOnly, Detail: "the detail stays out"}, now)
	l.Record("d", errors.New("deadline hit"), now)
	for name, want := range map[string]surveyorconfig.SourceCheck{
		"a": {Outcome: "ok"}, "b": {Outcome: "unsupported"}, "c": {Outcome: "failed", Code: "identity_not_read_only"},
		"d": {Outcome: "failed", Code: "check_failed"}, "e": {Outcome: "never"},
	} {
		if got := l.last(name, now.Add(10*time.Second)); got.Outcome != want.Outcome || got.Code != want.Code {
			t.Errorf("%s: %+v, want %+v", name, got, want)
		}
	}
	if g := l.last("a", now.Add(10*time.Second)); g.AgeSeconds != 10 {
		t.Errorf("age = %d, want 10", g.AgeSeconds)
	}
	if l.Generation() != 4 {
		t.Errorf("generation = %d, want 4 (one per recorded check)", l.Generation())
	}
}

// The system poll and the dry run record where they already call Check.
func TestSystemPollRecordsItsCheck(t *testing.T) {
	s := newSysEnv(t)
	cfg := s.cfg()
	cfg.Checks = NewCheckLog()
	s.in = s.instance(cfg)
	s.list("namespace/a", "x")
	s.mustPoll()
	if got := cfg.Checks.last("k8s", s.clock); got.Outcome != surveyorconfig.CheckOK {
		t.Errorf("after a clean check: %+v, want ok", got)
	}
	s.check(`{"check":{"ok":true,"read_only":false,"identity":"dev-admin","detail":"can write"}}`)
	s.poll()
	got := cfg.Checks.last("k8s", s.clock)
	if got.Outcome != surveyorconfig.CheckFailed || got.Code != recipe.CodeIdentityNotReadOnly {
		t.Errorf("after a check that is not read-only: %+v, want failed identity_not_read_only", got)
	}
}

func TestRecordSystemIgnoresTheContextEnding(t *testing.T) {
	l := NewCheckLog()
	_, pf, refused, err := observeSystem(context.Background(), failingCheck{}, recipe.Request{ID: "s"}, noState{}, "")
	l.recordSystem("s", pf, refused, err, time.Now())
	if got := l.last("s", time.Now()); got.Outcome != "failed" || got.Code != recipe.CodeCheckFailed {
		t.Errorf("a failed check: %+v", got)
	}
	l.recordSystem("t", nil, false, context.Canceled, time.Now())
	if got := l.last("t", time.Now()); got.Outcome != "never" {
		t.Errorf("a cancelled poll recorded %+v: it says nothing about the recipe", got)
	}
	// A failure after a clean check (the listing, the state) is not a check failure.
	l.recordSystem("u", &pollFailure{codeProtocol, "bad line"}, false, nil, time.Now())
	if got := l.last("u", time.Now()); got.Outcome != "ok" {
		t.Errorf("a list failure after a clean check: %+v, want ok", got)
	}
}
