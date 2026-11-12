package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
	"github.com/BeppeTemp/cartographer/internal/service"
)

func TestHealthPushLine(t *testing.T) {
	cases := []struct {
		p    client.HealthPush
		want string
	}{
		{client.HealthPush{State: "failed", UnpushedCommits: 3, Remedy: "unprotect the branch"}, "3 commits not pushed, unprotect the branch"},
		{client.HealthPush{State: "pending", UnpushedCommits: 1}, "1 commit not pushed"},
		{client.HealthPush{State: "failed", Remedy: "bad\x1b[2Jremote"}, "push failed, bad[2Jremote"},
	}
	for _, c := range cases {
		if got := c.p.Line(); got != c.want {
			t.Errorf("Line() = %q, want %q", got, c.want)
		}
	}
}

// A KB whose commits did not reach the remote is a line of `cartographer
// status` and a warning of `doctor`; a KB in sync is neither.
func TestStatusAndDoctor_ReportPushLag(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := clientconfig.Save(home, &clientconfig.Config{ServerURL: "http://127.0.0.1:39273/mcp", Agents: []string{"claude"}}); err != nil {
		t.Fatal(err)
	}
	kbs := []client.HealthKB{
		{Name: "kb-a", Push: &client.HealthPush{State: "failed", UnpushedCommits: 3, Remedy: "the remote refused the push"}},
		{Name: "kb-b"},
	}
	oldVersion, oldHealth, oldManifest, oldService := version, statusHealthFn, statusManifestsFn, statusServiceFn
	version = "v1.0.0"
	statusHealthFn = func(*clientconfig.Config) (*client.Health, error) {
		return &client.Health{Version: "v1.0.0", KBs: &kbs}, nil
	}
	statusManifestsFn = func(*clientconfig.Config, []syncProjection) (map[string]contract.Manifest, error) {
		return nil, errors.New("not needed")
	}
	statusServiceFn = func() (service.Status, error) { return service.Status{}, nil }
	t.Cleanup(func() {
		version, statusHealthFn, statusManifestsFn, statusServiceFn = oldVersion, oldHealth, oldManifest, oldService
	})

	out := withStdout(t, func() { withStderr(t, func() { cmdStatus(nil) }) })
	if !strings.Contains(out, "KB kb-a: 3 commits not pushed, the remote refused the push\n") {
		t.Errorf("status output lacks the push line: %q", out)
	}
	if strings.Contains(out, "kb-b:") {
		t.Errorf("status reports a KB in sync: %q", out)
	}
	js := withStdout(t, func() { withStderr(t, func() { cmdStatus([]string{"--output", "json"}) }) })
	if !strings.Contains(js, `"push_lag":["KB kb-a: 3 commits not pushed, the remote refused the push"]`) {
		t.Errorf("status JSON lacks push_lag: %s", js)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","version":"v1","kbs":[{"name":"kb-a","push":{"push_state":"failed","unpushed_commits":3,"remedy":"fix the remote"}},{"name":"kb-b"}]}`))
	}))
	defer srv.Close()
	facts, err := enumerateKBs(srv.URL, false, "", true)
	findings := checkPushLag(t.TempDir(), facts, err)
	if len(findings) != 1 || findings[0].Severity != doctorWarning || findings[0].Check != "push-lag" ||
		!strings.Contains(findings[0].Message, `"kb-a": 3 commits not pushed, fix the remote`) {
		t.Fatalf("findings = %+v, want one push-lag warning for kb-a", findings)
	}
	if got := checkPushLag(t.TempDir(), facts, errors.New("unreachable")); len(got) != 0 {
		t.Errorf("an unreachable server yields findings: %+v", got)
	}
}
