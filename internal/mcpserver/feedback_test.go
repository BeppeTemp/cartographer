package mcpserver

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/BeppeTemp/cartographer/internal/feedback"
)

func mustDest(t *testing.T, s string) feedback.Destination {
	t.Helper()
	d, err := feedback.ParseDestination(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func errTool(name string) Tool {
	return Tool{
		Name:        name,
		Description: "test-only tool whose handler returns a Go error",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		ReadOnly:    true,
		Handler: func(requestContext, json.RawMessage) (ToolResult, error) {
			return ToolResult{}, errors.New("disk exploded")
		},
	}
}

// The hint rides the two internal-error sites (recovered panic, handler Go
// error) in the SAME text block, and `off` removes it.
func TestInternalErrorCarriesTheReportHint(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		dest     string
		wantHint bool
	}{{"", true}, {"github", true}, {"https://tracker.example.com/new?t={title}", true}, {"off", false}} {
		s := New("test")
		s.SetFeedback(mustDest(t, tc.dest))
		s.RegisterTool(panicTool("boom_tool"))
		s.RegisterTool(errTool("err_tool"))
		for _, name := range []string{"boom_tool", "err_tool"} {
			res := callTool(t, s, name, `{}`)
			assertInternalError(t, res)
			if len(res.Content) != 1 {
				t.Fatalf("%s/%s: the hint must stay in the single text block, got %d blocks", tc.dest, name, len(res.Content))
			}
			if got := strings.HasSuffix(res.Content[0].Text, internalErrorHint); got != tc.wantHint {
				t.Errorf("%s/%s: hint present = %v, want %v: %q", tc.dest, name, got, tc.wantHint, res.Content[0].Text)
			}
			if strings.Contains(res.Content[0].Text, "disk exploded") && !strings.HasPrefix(res.Content[0].Text, "internal error: disk exploded") {
				t.Errorf("%s/%s: the error text moved: %q", tc.dest, name, res.Content[0].Text)
			}
		}
	}
}

// A user-level refusal is not a defect: none of them carries the hint.
func TestUserErrorsCarryNoReportHint(t *testing.T) {
	t.Parallel()
	s := newEraServer(t)
	for name, args := range map[string]string{
		"no_such_tool": `{}`,
		"concept_read": `{"id":"does/not-exist"}`,
	} {
		res := callTool(t, s, name, args)
		if !res.IsError {
			t.Fatalf("%s: expected an error result, got %+v", name, res)
		}
		for _, c := range res.Content {
			if strings.Contains(c.Text, "cartographer-feedback") {
				t.Errorf("%s: a user error carries the report hint: %q", name, c.Text)
			}
		}
	}
	ro := newEraServer(t)
	ro.readOnlySession = true
	if res := callTool(t, ro, "concept_write", `{"id":"a/b","body":"x"}`); !res.IsError || strings.Contains(res.Content[0].Text, "cartographer-feedback") {
		t.Errorf("a read-only refusal must carry no hint: %+v", res)
	}
}

func TestKBStatusReportsTheFeedbackDestination(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ setting, mode, tmpl string }{
		{"", "github", ""},
		{"off", "off", ""},
		{"https://tracker.example.com/new?t={title}", "custom", "https://tracker.example.com/new?t={title}"},
	} {
		s := newEraServer(t)
		s.SetFeedback(mustDest(t, tc.setting))
		var m struct {
			Feedback struct {
				Destination string `json:"destination"`
				URLTemplate string `json:"url_template"`
			} `json:"feedback"`
		}
		if err := json.Unmarshal([]byte(callOK(t, s, "kb_status", `{}`)), &m); err != nil {
			t.Fatal(err)
		}
		if m.Feedback.Destination != tc.mode || m.Feedback.URLTemplate != tc.tmpl {
			t.Errorf("%q: feedback = %+v, want %s %q", tc.setting, m.Feedback, tc.mode, tc.tmpl)
		}
	}
}

// The mode reaches the managed block through sync_pull; github is the absent
// default, so older clients and servers agree.
func TestSyncPullCarriesTheFeedbackModeUnlessGitHub(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ setting, want string }{{"", ""}, {"off", "off"}, {"https://tracker.example.com/new?t={title}", "custom"}} {
		k := setupTestKB(t)
		s := New("test")
		RegisterKBTools(s, k, Deps{BundleFS: fstest.MapFS{}})
		s.SetFeedback(mustDest(t, tc.setting))
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(callOK(t, s, "sync_pull", `{}`)), &m); err != nil {
			t.Fatal(err)
		}
		raw, present := m["feedback"]
		if tc.want == "" {
			if present {
				t.Errorf("%q: github must leave the field out, got %s", tc.setting, raw)
			}
			continue
		}
		var f struct{ Destination string }
		if err := json.Unmarshal(raw, &f); err != nil || f.Destination != tc.want {
			t.Errorf("%q: feedback = %s, want %s", tc.setting, raw, tc.want)
		}
		if strings.Contains(string(raw), "tracker.example.com") {
			t.Errorf("sync_pull must not carry the template: %s", raw)
		}
	}
}

// The guide of a generic connection names the offer unless the operator
// switched it off.
func TestGuideNamesTheFeedbackOfferUnlessOff(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		setting string
		want    bool
	}{{"", true}, {"off", false}} {
		k := setupTestKB(t)
		s := New("test")
		RegisterKBTools(s, k, Deps{})
		s.SetFeedback(mustDest(t, tc.setting))
		got := callOK(t, s, "kb_guide", `{}`)
		if has := strings.Contains(got, "cartographer-feedback"); has != tc.want {
			t.Errorf("%q: guide names the feedback skill = %v, want %v", tc.setting, has, tc.want)
		}
	}
}
