package recipes_test

import (
	"context"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/plugin/starcore"
	"github.com/BeppeTemp/cartographer/internal/plugin/starcore/recipes"
)

// replayList lists through a recipe that GETs url, with the recordings given
// and no seam: a call that left the process would fail to resolve the name.
func replayList(t *testing.T, url string, net []string, ex ...starcore.Exchange) (string, error) {
	t.Helper()
	p := load(t, `
def lst(r):
    resp = http.request("GET", "`+url+`")
    return {"entries": [{"key": "k", "title": "t", "revision": "1", "change": "added", "content": str(resp["status"]) + ":" + resp["body"]}], "next_cursor": "c"}
recipe("wiki", lst)
`)
	got, err := p.Call(context.Background(), recipes.Request{Recipe: "wiki", Verb: "list", Body: []byte(`{}`), Plugin: "acme",
		Permissions: &core.Permissions{Net: net}, Replay: &starcore.Replay{Exchanges: ex}})
	return string(got), err
}

// Replay answers from the recording and checks everything a live call checks.
func TestReplayAnswersFromTheRecordingOnly(t *testing.T) {
	page := starcore.Exchange{Method: "GET", URL: "https://wiki.example.com/pages", Query: map[string]string{"page": "2"}, Status: 201, Body: "hello"}
	got, err := replayList(t, "https://wiki.example.com/pages?page=2", []string{"wiki.example.com"}, page)
	if err != nil || !strings.Contains(got, `"content":"201:hello"`) {
		t.Fatalf("%q %v", got, err)
	}
	for name, tc := range map[string]struct{ url, want string }{
		"no recording":    {"https://wiki.example.com/other", "no recorded response for GET https://wiki.example.com/other"},
		"another query":   {"https://wiki.example.com/pages?page=3", "no recorded response for GET https://wiki.example.com/pages?page=3"},
		"host not in net": {"https://evil.example.org/pages", "permissions.net"},
		"plain http":      {"http://wiki.example.com/pages", "plain http"},
		"credentials":     {"https://user:pw@wiki.example.com/pages", "credentials"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := replayList(t, tc.url, []string{"wiki.example.com"}, page); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%v, want %q", err, tc.want)
			}
		})
	}
}

// A recorded redirect is followed under the redirect rules: within the net
// allow-list it works, outside it is refused.
func TestReplayRedirectsKeepTheirRules(t *testing.T) {
	hop := func(to string) starcore.Exchange {
		return starcore.Exchange{Method: "GET", URL: "https://wiki.example.com/a", Status: 302, Headers: map[string]string{"Location": to}}
	}
	end := starcore.Exchange{Method: "GET", URL: "https://wiki.example.com/b", Body: "end"}
	if got, err := replayList(t, "https://wiki.example.com/a", []string{"wiki.example.com"}, hop("https://wiki.example.com/b"), end); err != nil || !strings.Contains(got, ":end") {
		t.Fatalf("%q %v", got, err)
	}
	other := starcore.Exchange{Method: "GET", URL: "https://evil.example.org/b", Body: "end"}
	if _, err := replayList(t, "https://wiki.example.com/a", []string{"wiki.example.com"}, hop("https://evil.example.org/b"), other); err == nil || !strings.Contains(err.Error(), "not following a redirect") {
		t.Errorf("a redirect out of the allow-list: %v", err)
	}
	if _, err := replayList(t, "https://wiki.example.com/a", []string{"wiki.example.com"}, hop("http://wiki.example.com/b"), end); err == nil || !strings.Contains(err.Error(), "https to http") {
		t.Errorf("a downgrade: %v", err)
	}
}
