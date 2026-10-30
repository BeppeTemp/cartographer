package system

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/worker/recipe"
)

// The verdict lister of examples/system-recipes/prometheus-verdicts, run through
// the real exec runner against a stand-in for the Prometheus HTTP API: the script
// has no test-only path, so its URLs, headers and redirects are the real ones.
// Skipped where no Python 3 is found.

var verdictDir = filepath.Join("..", "..", "..", "examples", "system-recipes", "prometheus-verdicts")

const verdictToken = "tok-canary"

const cleanFlags = `{"web.enable-admin-api":"false","web.enable-lifecycle":"false","web.enable-remote-write-receiver":"false"}`

const verdictRules = `{"groups":[{"name":"g","rules":[
	{"type":"alerting","name":"HighErrorRate","state":"inactive","health":"ok","labels":{"service":"api","severity":"warning"}},
	{"type":"alerting","name":"HighErrorRate","state":"firing","health":"ok","labels":{"service":"api","severity":"critical"}},
	{"type":"alerting","name":"ApiDown","state":"pending","health":"ok","labels":{"service":"api"}},
	{"type":"alerting","name":"RuleBroken","state":"inactive","health":"err","labels":{"service":"api"}},
	{"type":"alerting","name":"OtherDown","state":"firing","health":"ok","labels":{"service":"other"}},
	{"type":"recording","name":"job:rate","health":"ok","labels":{"service":"api"}}]}]}`

// promStub answers the three endpoints the lister calls, each field being the
// data of the answer. status replaces every answer; redirect sends the queries away.
type promStub struct {
	*httptest.Server
	mu               sync.Mutex
	flags, rules     string
	ratio, days      string // the result arrays of the two queries of checks.json
	status, redirect string
	auth             []string
}

func vector(value string) string {
	return fmt.Sprintf(`[{"metric":{},"value":[1900000000.25,%q]}]`, value)
}

func newPromStub(t *testing.T) *promStub {
	t.Helper()
	s := &promStub{flags: cleanFlags, rules: verdictRules, ratio: vector("2.25"), days: vector("43.75")}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.auth = append(s.auth, r.Header.Get("Authorization"))
		if s.redirect != "" && r.URL.Path == "/api/v1/query" {
			http.Redirect(w, r, s.redirect, http.StatusFound)
			return
		}
		if s.status != "" {
			var code int
			fmt.Sscanf(s.status, "%d", &code)
			w.WriteHeader(code)
			return
		}
		var data string
		switch r.URL.Path {
		case "/api/v1/status/flags":
			data = s.flags
		case "/api/v1/rules":
			data = s.rules
		case "/api/v1/query":
			series := s.ratio
			if strings.Contains(r.URL.Query().Get("query"), "node_filesystem_avail_bytes") {
				series = s.days
			}
			data = `{"resultType":"vector","result":` + series + `}`
		default:
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"status":"success","data":%s}`, data)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *promStub) set(f func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f()
}

func verdictLister(t *testing.T, s *promStub, checks string) recipe.SystemRecipe {
	t.Helper()
	t.Setenv("METRICS_URL", s.URL)
	t.Setenv("METRICS_TOKEN", verdictToken)
	script, err := filepath.Abs(filepath.Join(verdictDir, "lister.py")) // the child runs in a temp directory
	if err != nil {
		t.Fatal(err)
	}
	if checks == "" {
		checks, _ = filepath.Abs(filepath.Join(verdictDir, "checks.json"))
	}
	env := map[string]string{"METRICS_URL": "METRICS_URL", "METRICS_TOKEN": "METRICS_TOKEN"}
	return recipe.NewExec(recipe.ExecConfig{Name: "metrics-verdicts", Shape: recipe.ShapeSystem, Command: python(t), Args: []string{script, checks},
		Env: env, Timeout: time.Minute, MaxOutput: 1 << 20, MaxContent: 8 << 10, Instructions: "x"}).(recipe.SystemRecipe)
}

func TestExampleVerdictsPreflight(t *testing.T) {
	s := newPromStub(t)
	v, err := verdictLister(t, s, "").Preflight(context.Background(), recipe.Request{ID: "s/m"})
	if err != nil || v.Identity() != "metrics-viewer" || !strings.HasPrefix(v.Detail(), "checked: ") {
		t.Fatalf("clean endpoint: %+v %v", v, err)
	}
	for name, tc := range map[string]struct {
		change func(*promStub)
		want   string
	}{
		"admin api": {func(p *promStub) { p.flags = `{"web.enable-admin-api":"true"}` }, "takes writes: web.enable-admin-api"},
		"lifecycle": {func(p *promStub) { p.flags = `{"web.enable-admin-api":"false","web.enable-lifecycle":"true"}` }, "web.enable-lifecycle"},
		"write receiver": {func(p *promStub) {
			p.flags = `{"web.enable-admin-api":"false","web.enable-remote-write-receiver":"true"}`
		}, "web.enable-remote-write-receiver"},
		"no flags to read": {func(p *promStub) { p.flags = `{}` }, "does not report its flags"},
		"not authorized":   {func(p *promStub) { p.status = "403" }, "answered HTTP 403"},
	} {
		p := newPromStub(t)
		p.set(func() { tc.change(p) })
		_, err := verdictLister(t, p, "").Preflight(context.Background(), recipe.Request{ID: "s/m"})
		e, ok := err.(*recipe.Error)
		if !ok || e.Code != recipe.CodeIdentityNotReadOnly || !strings.Contains(e.Detail, tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}
}

func TestExampleVerdictsListingHoldsNoValue(t *testing.T) {
	s := newPromStub(t)
	r := verdictLister(t, s, "")
	l, err := list(t, r, recipe.Request{ID: "s/m"})
	if err != nil || len(l.Entries) != 2 {
		t.Fatalf("listing = %+v %v", l, err)
	}
	const api = "alert ApiDown: ok\nalert HighErrorRate: bad\nalert RuleBroken: no data\nerror-ratio: watch\n"
	if l.Entries[0].Key != "service/api" || l.Entries[0].Content != api {
		t.Errorf("service/api = %q", l.Entries[0].Content)
	}
	if l.Entries[1].Key != "storage/data" || l.Entries[1].Content != "days-to-full: watch\n" {
		t.Errorf("storage/data = %q", l.Entries[1].Content)
	}
	digit := regexp.MustCompile(`[0-9]`)
	for _, e := range l.Entries {
		if digit.MatchString(e.Content) {
			t.Errorf("%s holds a number: %q", e.Key, e.Content)
		}
	}
	for _, h := range s.auth {
		if h != "Bearer "+verdictToken {
			t.Fatalf("a request went with Authorization %q", h)
		}
	}
	before, _ := ValidateListing(l.Entries, 8<<10)

	// A value that moves inside its band costs nothing: same revisions, same bytes.
	s.set(func() { s.ratio, s.days = vector("1.75"), vector("20.5") })
	l2, err := list(t, r, recipe.Request{ID: "s/m"})
	if err != nil {
		t.Fatal(err)
	}
	inside, _ := ValidateListing(l2.Entries, 8<<10)
	if !reflect.DeepEqual(before, inside) {
		t.Errorf("a move inside the band changed the listing: %+v", inside)
	}

	// Crossing an edge changes that group only, and bands read upwards or downwards.
	s.set(func() { s.ratio = vector("3") })
	l3, err := list(t, r, recipe.Request{ID: "s/m"})
	if err != nil {
		t.Fatal(err)
	}
	crossed, _ := ValidateListing(l3.Entries, 8<<10)
	if crossed[0].Revision == before[0].Revision || crossed[1].Revision != before[1].Revision {
		t.Errorf("revisions %+v", crossed)
	}
	for value, want := range map[string]string{"13.9": "bad", "14": "watch", "59.9": "watch", "60": "ok", "+Inf": "ok", "NaN": "no data"} {
		s.set(func() { s.days = vector(value) })
		got, err := list(t, r, recipe.Request{ID: "s/m", Include: []string{"storage/*"}})
		if err != nil || len(got.Entries) != 1 || got.Entries[0].Content != "days-to-full: "+want+"\n" {
			t.Errorf("days %s: %+v %v, want %s", value, got.Entries, err, want)
		}
	}
	s.set(func() { s.days = `[]` })
	got, err := list(t, r, recipe.Request{ID: "s/m", Include: []string{"storage/*"}})
	if err != nil || got.Entries[0].Content != "days-to-full: no data\n" {
		t.Errorf("an empty answer: %+v %v", got.Entries, err)
	}
}

// A listing that left a line out would read as a change, so every failure fails
// the whole listing and names what failed; the token never appears.
func TestExampleVerdictsFailuresNameTheSignal(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(*promStub)
		want   string
	}{
		"two series": {func(p *promStub) {
			p.ratio = `[{"metric":{"a":"b"},"value":[1,"1"]},{"metric":{"a":"c"},"value":[1,"2"]}]`
		}, "service/api/error-ratio answered 2 series"},
		"not a number": {func(p *promStub) { p.days = `[{"metric":{},"value":[1,"high"]}]` }, "storage/data/days-to-full answered a sample that is not a number"},
		"server error": {func(p *promStub) { p.status = "500" }, "answered HTTP 500"},
	} {
		p := newPromStub(t)
		r := verdictLister(t, p, "")
		v, err := r.Preflight(context.Background(), recipe.Request{ID: "s/m"})
		if err != nil {
			t.Fatal(err)
		}
		p.set(func() { tc.change(p) })
		_, err = r.ListSystem(context.Background(), v, recipe.Request{ID: "s/m"})
		e, ok := err.(*recipe.Error)
		if !ok || e.Code != recipe.CodeListFailed || !strings.Contains(e.Detail, tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
		if err != nil && strings.Contains(err.Error(), verdictToken) {
			t.Errorf("%s: the token is in the error", name)
		}
	}
}

// urllib repeats the Authorization header on a redirect, to any host.
func TestExampleVerdictsFollowsNoRedirect(t *testing.T) {
	var hits int
	var mu sync.Mutex
	away := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		hits++
	}))
	defer away.Close()
	s := newPromStub(t)
	r := verdictLister(t, s, "")
	v, err := r.Preflight(context.Background(), recipe.Request{ID: "s/m"})
	if err != nil {
		t.Fatal(err)
	}
	s.set(func() { s.redirect = away.URL })
	_, err = r.ListSystem(context.Background(), v, recipe.Request{ID: "s/m"})
	mu.Lock()
	defer mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "answered HTTP 302") || hits != 0 {
		t.Errorf("err = %v, the other host was hit %d times", err, hits)
	}
}

func TestExampleVerdictsNeverUsesTheLocator(t *testing.T) {
	s := newPromStub(t)
	r := verdictLister(t, s, "")
	base, err := list(t, r, recipe.Request{ID: "s/m", Locator: "metrics://production"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := list(t, r, recipe.Request{ID: "s/m", Locator: "http://127.0.0.1:1/"})
	if err != nil || !reflect.DeepEqual(base.Entries, other.Entries) {
		t.Errorf("the locator changed the listing: %+v %v", other, err)
	}
}

func TestExampleVerdictsChecksFile(t *testing.T) {
	s := newPromStub(t)
	for name, tc := range map[string]struct{ checks, want string }{
		"no groups":   {`{}`, "cannot read the checks file"},
		"not json":    {`groups`, "cannot read the checks file"},
		"empty group": {`{"groups":[{"key":"a"}]}`, "group a has neither"},
		"no query":    {`{"groups":[{"key":"a","signals":[{"name":"s"}]}]}`, "group a: a signal needs"},
		"free word": {`{"groups":[{"key":"a","signals":[{"name":"s","query":"q","bands":[{"verdict":"fine"}]}]}]}`,
			"signal a/s: every band needs a verdict"},
		"edges down": {`{"groups":[{"key":"a","signals":[{"name":"s","query":"q","bands":[{"below":2,"verdict":"ok"},{"below":1,"verdict":"bad"},{"verdict":"bad"}]}]}]}`,
			"signal a/s: bands need ascending"},
		"open last band": {`{"groups":[{"key":"a","signals":[{"name":"s","query":"q","bands":[{"below":2,"verdict":"ok"}]}]}]}`,
			"signal a/s: bands need ascending"},
	} {
		path := filepath.Join(t.TempDir(), "checks.json")
		if err := os.WriteFile(path, []byte(tc.checks), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := verdictLister(t, s, path).Preflight(context.Background(), recipe.Request{ID: "s/m"})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}
	r := verdictLister(t, s, "")
	v, err := r.Preflight(context.Background(), recipe.Request{ID: "s/m"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.ListSystem(context.Background(), v, recipe.Request{ID: "s/m", MaxContentBytes: 10})
	if err == nil || !strings.Contains(err.Error(), "group service/api is over max_content_bytes") {
		t.Errorf("cap: %v", err)
	}
}
