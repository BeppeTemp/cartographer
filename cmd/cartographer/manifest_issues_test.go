package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// issuesPayload is syncPullPayload plus the issues a server names for an
// artifact it left out (#1775).
func issuesPayload(t *testing.T, kb, skill string, issues ...string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(syncPullPayload(t, kb, skill)), &m); err != nil {
		t.Fatal(err)
	}
	if len(issues) > 0 {
		m["issues"] = issues
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const oversizedIssue = "skill/big: file huge.bin is over the 256 KiB artifact cap; the artifact is left out"

func issuesConfig(srvURL string) *clientconfig.Config {
	return &clientconfig.Config{
		ServerURL: srvURL + "/mcp", ServerName: "cartographer",
		Agents: []string{"claude", "codex"}, KnownKBs: []string{"one", "two"},
	}
}

// #1775: sync names what the server left out, once per run (two providers
// read the same KB), prefixed by the KB when more than one is read.
func TestSync_PrintsManifestIssuesOnce(t *testing.T) {
	srv := syncPullServer(t, func(kb string) (string, bool) {
		if kb == "one" {
			return issuesPayload(t, kb, "skill-"+kb, oversizedIssue+"\x1b[31m"), false
		}
		return issuesPayload(t, kb, "skill-"+kb), false
	})
	defer srv.Close()
	dir := t.TempDir()
	cfg := issuesConfig(srv.URL)
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	var runErr error
	stderr := withStderr(t, func() { _, runErr = runSync(dir, cfg, syncOptions{}) })
	if runErr != nil {
		t.Fatal(runErr)
	}
	if n := strings.Count(stderr, "skill/big"); n != 1 {
		t.Fatalf("the issue is named %d times, want once:\n%s", n, stderr)
	}
	if !strings.Contains(stderr, "warning: one: "+oversizedIssue) {
		t.Errorf("want a warning prefixed by the KB:\n%s", stderr)
	}
	if strings.Contains(stderr, "\x1b") {
		t.Errorf("server text reached the terminal unescaped:\n%q", stderr)
	}
}

func TestSync_CleanKBPrintsNoManifestIssue(t *testing.T) {
	srv := syncPullServer(t, func(kb string) (string, bool) { return issuesPayload(t, kb, "skill-"+kb), false })
	defer srv.Close()
	dir := t.TempDir()
	cfg := issuesConfig(srv.URL)
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	stderr := withStderr(t, func() {
		if _, err := runSync(dir, cfg, syncOptions{}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(stderr, "256 KiB") || strings.Contains(stderr, "skill/big") {
		t.Errorf("a clean KB produced an issue:\n%s", stderr)
	}
}

func TestStatus_ShowsManifestIssuesOnce(t *testing.T) {
	srv := syncPullServer(t, func(kb string) (string, bool) {
		if kb == "one" {
			return issuesPayload(t, kb, "skill-"+kb, oversizedIssue), false
		}
		return issuesPayload(t, kb, "skill-"+kb), false
	})
	defer srv.Close()
	home := t.TempDir()
	setHome(t, home)
	if err := clientconfig.Save(home, issuesConfig(srv.URL)); err != nil {
		t.Fatal(err)
	}
	out := withStdout(t, func() { cmdStatus(nil) })
	if n := strings.Count(out, "skill/big"); n != 1 {
		t.Fatalf("status names the issue %d times, want once:\n%s", n, out)
	}
	if !strings.Contains(out, "one: "+oversizedIssue) {
		t.Errorf("status lacks the issue:\n%s", out)
	}
}

// connect materializes without runSync: its issues ride the connect warnings.
func TestConnect_ReportsManifestIssuesOnce(t *testing.T) {
	srv := syncPullServer(t, func(kb string) (string, bool) {
		if kb == "one" {
			return issuesPayload(t, kb, "skill-"+kb, oversizedIssue), false
		}
		return issuesPayload(t, kb, "skill-"+kb), false
	})
	defer srv.Close()
	var res connectResult
	var err error
	withStdout(t, func() {
		res, err = doConnect(connectOptions{Providers: []string{"claude", "codex"}, Dir: t.TempDir(), ServerURL: srv.URL + "/mcp", Name: "cartographer", KBs: []string{"all"}})
	})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, w := range res.Warnings {
		if strings.Contains(w, "skill/big") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("connect warnings name the issue %d times, want once: %q", n, res.Warnings)
	}

	clean := syncPullServer(t, func(kb string) (string, bool) { return issuesPayload(t, kb, "skill-"+kb), false })
	defer clean.Close()
	withStdout(t, func() {
		res, err = doConnect(connectOptions{Providers: []string{"claude"}, Dir: t.TempDir(), ServerURL: clean.URL + "/mcp", Name: "cartographer", KBs: []string{"all"}})
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, "skill/big") {
			t.Errorf("a clean KB produced %q", w)
		}
	}
}

func TestSync_DryRunPrintsManifestIssues(t *testing.T) {
	srv := syncPullServer(t, func(kb string) (string, bool) {
		if kb == "one" {
			return issuesPayload(t, kb, "skill-"+kb, oversizedIssue), false
		}
		return issuesPayload(t, kb, "skill-"+kb), false
	})
	defer srv.Close()
	dir := t.TempDir()
	cfg := issuesConfig(srv.URL)
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	stderr := withStderr(t, func() {
		withStdout(t, func() {
			if _, err := runSync(dir, cfg, syncOptions{DryRun: true}); err != nil {
				t.Fatal(err)
			}
		})
	})
	if !strings.Contains(stderr, "warning: one: "+oversizedIssue) {
		t.Errorf("dry run lacks the warning:\n%s", stderr)
	}
}

// status --json carries the issues raw (like every server text in the
// snapshot: the JSON encoder escapes control characters), and they change
// neither state nor exit code.
func TestStatus_JSONIssuesDoNotChangeStateOrExit(t *testing.T) {
	run := func(withIssue bool) (statusSnapshot, int, string) {
		srv := syncPullServer(t, func(kb string) (string, bool) {
			if kb == "one" && withIssue {
				return issuesPayload(t, kb, "skill-"+kb, oversizedIssue+"\x1b[31m"), false
			}
			return issuesPayload(t, kb, "skill-"+kb), false
		})
		defer srv.Close()
		home := t.TempDir()
		setHome(t, home)
		if err := clientconfig.Save(home, issuesConfig(srv.URL)); err != nil {
			t.Fatal(err)
		}
		code := 0
		out := withStdout(t, func() { code = cmdStatus([]string{"--output", "json"}) })
		var s statusSnapshot
		if err := json.Unmarshal([]byte(out), &s); err != nil {
			t.Fatalf("status json: %v\n%s", err, out)
		}
		table := withStdout(t, func() { cmdStatus(nil) })
		return s, code, table
	}
	with, codeWith, table := run(true)
	without, codeWithout, _ := run(false)
	if len(with.Issues) != 1 || !strings.Contains(with.Issues[0], "skill/big") {
		t.Errorf("json issues = %q", with.Issues)
	}
	if len(without.Issues) != 0 {
		t.Errorf("clean KB json issues = %q", without.Issues)
	}
	if with.State != without.State || codeWith != codeWithout {
		t.Errorf("issues changed state/exit: %s/%d vs %s/%d", with.State, codeWith, without.State, codeWithout)
	}
	if strings.Contains(table, "\x1b") {
		t.Errorf("status table printed server text unescaped: %q", table)
	}
}

func TestIssuesForProjection_Selection(t *testing.T) {
	cs := candidateSet{
		Named:      map[string][]provisioning.Artifact{"a": nil, "b": nil},
		Issues:     map[string][]string{"a": {"ia"}, "b": {"ib"}},
		BareIssues: []string{"bare"},
	}
	cfg := &clientconfig.Config{Clients: map[string]clientconfig.ClientBinding{
		"claude": {KBs: []string{"a"}},
		"codex":  {KBs: []string{"a", "b"}},
		"none":   {KBs: []string{}, KBsSet: true},
	}}
	cases := []struct {
		name string
		cs   candidateSet
		p    syncProjection
		want []string
	}{
		{"one bound KB", cs, syncProjection{Provider: "claude"}, []string{"a|ia"}},
		{"two bound KBs", cs, syncProjection{Provider: "codex"}, []string{"a|ia", "b|ib"}},
		{"workspace reads its own KBs", cs, syncProjection{Provider: "codex", Workspace: "/w", KBs: []string{"b"}}, []string{"b|ib"}},
		{"bundle only", cs, syncProjection{Provider: "claude", BundleOnly: true}, nil},
		{"bound to no KB", cs, syncProjection{Provider: "none"}, nil},
	}
	bare := cs
	bare.HasBare = true
	cases = append(cases,
		struct {
			name string
			cs   candidateSet
			p    syncProjection
			want []string
		}{"bare endpoint", bare, syncProjection{Provider: "claude"}, []string{"|bare"}},
		struct {
			name string
			cs   candidateSet
			p    syncProjection
			want []string
		}{"bare endpoint, workspace", bare, syncProjection{Provider: "claude", Workspace: "/w"}, nil},
		struct {
			name string
			cs   candidateSet
			p    syncProjection
			want []string
		}{"bare endpoint, bound to no KB", bare, syncProjection{Provider: "none"}, nil},
	)
	for _, c := range cases {
		issues, _ := c.cs.issuesForProjection(cfg, c.p)
		var got []string
		for _, is := range issues {
			got = append(got, is.KB+"|"+is.Text)
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// #1799: a global projection over several KBs and a workspace projection bound
// to one of them report the same issue: it is printed once, with the KB's name
// because the run as a whole reads more than one.
func TestManifestIssues_SameIssueAcrossProjectionsIsOneLine(t *testing.T) {
	is := provisioning.KBIssue{KB: "kb-a", Text: "skill \"x\" excluded"}
	got := manifestIssues(map[string]provisioning.Manifest{
		"claude":    {KBIssues: []provisioning.KBIssue{is}, ReadKBs: []string{"kb-a", "kb-b"}},
		"claude@/w": {KBIssues: []provisioning.KBIssue{is}, ReadKBs: []string{"kb-a"}},
	})
	if len(got) != 1 || got[0] != "kb-a: skill \"x\" excluded" {
		t.Errorf("got %q, want one prefixed line", got)
	}
	single := manifestIssues(map[string]provisioning.Manifest{
		"claude@/w": {KBIssues: []provisioning.KBIssue{is}, ReadKBs: []string{"kb-a"}},
	})
	if len(single) != 1 || single[0] != "skill \"x\" excluded" {
		t.Errorf("single-KB run: got %q, want the bare issue", single)
	}
}
