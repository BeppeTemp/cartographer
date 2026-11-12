package mcpserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/surveyorconfig"
)

const srcDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type sourcesAnswer struct {
	Surveyors []struct {
		Sources []surveyorconfig.Source `json:"sources"`
	} `json:"surveyors"`
}

func (f *surveyorFixture) sources(t *testing.T, token string) []surveyorconfig.Source {
	t.Helper()
	rr := f.get(token, "/settings")
	var a sourcesAnswer
	if err := json.Unmarshal(rr.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	if len(a.Surveyors) != 1 {
		t.Fatalf("%d rows: %s", len(a.Surveyors), rr.Body.String())
	}
	if a.Surveyors[0].Sources == nil {
		t.Fatalf("sources is not an array: %s", rr.Body.String())
	}
	return a.Surveyors[0].Sources
}

func src(name, origin, kind string, check map[string]any) map[string]any {
	return map[string]any{"name": name, "origin": origin, "kind": kind, "check": check}
}

func TestSurveyorSettings_SourcesAreStoredScrubbedAndDated(t *testing.T) {
	t.Parallel()
	f := newAtlasSurveyorFixture(t)
	f.post(t, "surveyor-a", beat(nil))
	pages := src("acme/pages", "plugin", "starlark", map[string]any{"outcome": "failed", "code": "check_failed", "age_seconds": 90})
	pages["plugin"], pages["digest"], pages["shape"] = "acme", srcDigest, "system"
	rr := f.postSettings("surveyor-a", report(map[string]any{"sources": []map[string]any{
		src("wiki-lister", "operator", "exec", map[string]any{"outcome": "ok", "age_seconds": 5, "at": "2001-01-01T00:00:00Z"}),
		pages,
		src("wiki-mcp", "operator", "mcp", map[string]any{"outcome": "unsupported"}),
	}}))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	got := f.sources(t, "surveyor-a")
	if len(got) != 3 {
		t.Fatalf("kept %+v", got)
	}
	// Ages use the server's clock: the time a Surveyor declared is not read.
	now := f.clock.now()
	if want := now.Add(-5 * time.Second); !got[0].Check.At.Equal(want) {
		t.Errorf("wiki-lister checked at %v, want %v (receipt minus age)", got[0].Check.At, want)
	}
	if want := now.Add(-90 * time.Second); !got[1].Check.At.Equal(want) || got[1].Check.Code != "check_failed" || got[1].Plugin != "acme" || got[1].Shape != "system" {
		t.Errorf("acme/pages = %+v, want checked at %v", got[1], want)
	}
	if !got[2].Check.At.IsZero() {
		t.Errorf("an unsupported check carries a time: %+v", got[2])
	}
}

func TestSurveyorSettings_SourcesAreReplacedWhole(t *testing.T) {
	t.Parallel()
	f := newAtlasSurveyorFixture(t)
	f.post(t, "surveyor-a", beat(nil))
	one := src("wiki-lister", "operator", "exec", map[string]any{"outcome": "never"})
	two := src("wiki-mcp", "operator", "mcp", map[string]any{"outcome": "unsupported"})
	f.postSettings("surveyor-a", report(map[string]any{"sources": []map[string]any{one, two}}))
	f.postSettings("surveyor-a", report(map[string]any{"sources": []map[string]any{two}}))
	if got := f.sources(t, "surveyor-a"); len(got) != 1 || got[0].Name != "wiki-mcp" {
		t.Fatalf("after the second report: %+v, want only wiki-mcp", got)
	}
	// A Surveyor that predates the field sends none: the row says [] and keeps nothing of the old.
	f.postSettings("surveyor-a", report(nil))
	if got := f.sources(t, "surveyor-a"); len(got) != 0 {
		t.Fatalf("a report with no sources left %+v", got)
	}
}

func TestSurveyorSettings_SourcesDropMalformedAndOversizeElements(t *testing.T) {
	t.Parallel()
	f := newAtlasSurveyorFixture(t)
	f.post(t, "surveyor-a", beat(nil))
	good := src("wiki-lister", "operator", "exec", map[string]any{"outcome": "never"})
	good["command"], good["env"], good["url"] = "S3CRET-cmd", map[string]any{"TOKEN": "S3CRET"}, "https://user:S3CRET@example.com"
	list := []any{
		good,
		"not an object",
		map[string]any{"name": 5, "origin": "operator", "kind": "exec", "check": map[string]any{"outcome": "never"}}, // wrong type
		src("bad-kind", "operator", "shell", map[string]any{"outcome": "never"}),
		src("bad-code", "operator", "exec", map[string]any{"outcome": "failed", "code": "token S3CRET refused"}),
		src(strings.Repeat("a", 5000), "operator", "exec", map[string]any{"outcome": "never"}),
		src("wiki-lister", "operator", "exec", map[string]any{"outcome": "ok"}), // a repeated name
	}
	for i := 0; i < 2*surveyorconfig.MaxSources; i++ {
		list = append(list, src("r-"+strings.Repeat("a", i/26)+string(rune('a'+i%26)), "operator", "exec", map[string]any{"outcome": "never"}))
	}
	rr := f.postSettings("surveyor-a", report(map[string]any{"sources": list}))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s: one bad element must not refuse the report", rr.Code, rr.Body.String())
	}
	got := f.sources(t, "surveyor-a")
	if len(got) != surveyorconfig.MaxSources || got[0].Name != "wiki-lister" || got[0].Check.Outcome != "never" {
		t.Fatalf("kept %d, first %+v: want the cap, the first good element once, no malformed one", len(got), got[0])
	}
	for _, s := range got {
		if s.Kind == "shell" || s.Name == "bad-kind" || s.Name == "bad-code" || len(s.Name) > 81 {
			t.Errorf("kept %+v", s)
		}
	}
	if body := f.get("surveyor-a", "/settings").Body.String(); strings.Contains(body, "S3CRET") {
		t.Error("the settings view carries a value a source never holds")
	}
}

func TestSurveyorSettings_SourcesFollowTheRowsVisibility(t *testing.T) {
	t.Parallel()
	f := newAtlasSurveyorFixture(t)
	f.post(t, "surveyor-ab", beat(map[string]any{"kbs": []string{"kb-a", "kb-b"}}))
	f.postSettings("surveyor-ab", report(map[string]any{"kbs": []string{"kb-a", "kb-b"},
		"sources": []map[string]any{src("wiki-lister", "operator", "exec", map[string]any{"outcome": "never"})}}))
	for _, tok := range []string{"reader", "narrow", "other"} {
		if body := f.get(tok, "/settings").Body.String(); strings.Contains(body, "wiki-lister") {
			t.Errorf("%s sees a source of a perimeter it does not see whole", tok)
		}
	}
	if got := f.sources(t, "surveyor-ab"); len(got) != 1 {
		t.Fatalf("the caller that sees both KBs got %+v", got)
	}
}

func TestSurveyorSettings_ReportCarryingTheTokenInASourceIsRefused(t *testing.T) {
	t.Parallel()
	f := newAtlasSurveyorFixture(t)
	f.post(t, "surveyor-a", beat(nil))
	// "surveyor-a" is the token; a JSON escape hides it from the raw bytes.
	name := "surveyor\\u002da"
	raw := `{"component":"surveyor","protocol":1,"perimeter":"team-a","kbs":["kb-a"],"entries":[],"sources":[{"name":"` + name +
		`","origin":"operator","kind":"exec","check":{"outcome":"never"}}]}`
	if rr := f.postSettings("surveyor-a", []byte(raw)); rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s, want 400", rr.Code, rr.Body.String())
	}
}
