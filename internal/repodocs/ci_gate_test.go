package repodocs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// gateResult runs scripts/ci-gate.sh with a fake gh and reports its exit status
// and output. statuses[n] is the answer of the n-th statuses call (the last one
// repeats); perms maps a login to the permission the API reports for it.
func gateResult(t *testing.T, env map[string]string, statuses []string, perms map[string]string) (string, bool) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("bash script, run by the Unix legs")
	}
	for _, bin := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not found", bin)
		}
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, s := range statuses {
		if err := os.WriteFile(filepath.Join(dir, "statuses."+strconv.Itoa(i+1)), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for login, perm := range perms {
		if err := os.WriteFile(filepath.Join(dir, "perm."+login), []byte(`{"permission":"`+perm+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fake := `#!/bin/sh
d="$FAKE_DIR"
case "$2" in
  */statuses*)
    n=$(( $(cat "$d/count" 2>/dev/null || echo 0) + 1 )); echo "$n" > "$d/count"
    last=$(ls "$d" | grep -c '^statuses\.')
    [ "$n" -le "$last" ] || n=$last
    cat "$d/statuses.$n" ;;
  */collaborators/*/permission)
    l=${2#*/collaborators/}; l=${l%/permission}
    cat "$d/perm.$l" 2>/dev/null || exit 1 ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join(repoRoot(t), "scripts", "ci-gate.sh"))
	var e []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GATE_") {
			e = append(e, kv)
		}
	}
	e = append(e, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "FAKE_DIR="+dir,
		"GATE_POLL_INTERVAL=0", "GATE_POLL_TRIES=5", "REPO=example/upstream", "HEAD_SHA=abc123")
	for k, v := range env {
		e = append(e, k+"="+v)
	}
	cmd.Env = e
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

func signoffStatus(login, context, state string) string {
	return fmt.Sprintf(`[{"context":%q,"state":%q,"creator":{"login":%q}}]`, context, state, login)
}

// The decision table of the `gate` job (scripts/ci-gate.sh).
func TestCIGateDecisionTable(t *testing.T) {
	maint := map[string]string{"EVENT": "pull_request", "TEST_RESULT": "skipped", "WEB_RESULT": "skipped", "CHANGES_RESULT": "success", "CHANGES_WEB": "false"}
	with := func(base map[string]string, kv ...string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	good := signoffStatus("user", "signoff", "success")
	for _, c := range []struct {
		name     string
		env      map[string]string
		statuses []string
		perms    map[string]string
		want     bool
		msg      string
	}{
		{"maintainer PR with a signoff by a writer", maint, []string{good}, map[string]string{"user": "write"}, true, ""},
		{"signoff by an admin", maint, []string{good}, map[string]string{"user": "admin"}, true, ""},
		{"signoff by a maintain role", maint, []string{good}, map[string]string{"user": "maintain"}, true, ""},
		{"signoff by a creator without write access", maint, []string{good}, map[string]string{"user": "read"}, false, "make signoff"},
		{"signoff by a creator whose permission is unreadable", maint, []string{good}, nil, false, "make signoff"},
		{"signoff in state failure", maint, []string{signoffStatus("user", "signoff", "failure")}, map[string]string{"user": "write"}, false, "make signoff"},
		{"a status of another context", maint, []string{signoffStatus("user", "ci/other", "success")}, map[string]string{"user": "write"}, false, "make signoff"},
		{"no status at all", maint, []string{`[]`}, nil, false, "make signoff"},
		{"an unreadable status list", maint, []string{`not json`}, nil, false, "make signoff"},
		{"signoff appearing on the third poll", maint, []string{`[]`, `[]`, good}, map[string]string{"user": "write"}, true, ""},
		{"signoff appearing after the window", maint, []string{`[]`, `[]`, `[]`, `[]`, `[]`, good}, map[string]string{"user": "write"}, false, "make signoff"},

		{"contributor PR, everything green", with(maint, "TEST_RESULT", "success", "WEB_RESULT", "success"), nil, nil, true, ""},
		{"contributor PR, web not needed", with(maint, "TEST_RESULT", "success"), nil, nil, true, ""},
		{"contributor PR, web needed but skipped", with(maint, "TEST_RESULT", "success", "CHANGES_WEB", "true"), nil, nil, false, "web"},
		{"contributor PR, changes failed so web is not known to be unneeded", with(maint, "TEST_RESULT", "success", "CHANGES_RESULT", "failure", "CHANGES_WEB", ""), nil, nil, false, "web"},
		{"contributor PR, test failed", with(maint, "TEST_RESULT", "failure", "WEB_RESULT", "success"), nil, nil, false, "test"},
		{"contributor PR, a web leg failed", with(maint, "TEST_RESULT", "success", "WEB_RESULT", "failure"), nil, nil, false, "web"},
		{"contributor PR, cancelled", with(maint, "TEST_RESULT", "cancelled", "WEB_RESULT", "success"), nil, nil, false, "test"},
		{"push, green", map[string]string{"EVENT": "push", "TEST_RESULT": "success", "WEB_RESULT": "success", "CHANGES_RESULT": "skipped"}, nil, nil, true, ""},
		{"push, test skipped", map[string]string{"EVENT": "push", "TEST_RESULT": "skipped", "WEB_RESULT": "success", "CHANGES_RESULT": "skipped"}, nil, nil, false, "test"},
		{"push, web skipped", map[string]string{"EVENT": "push", "TEST_RESULT": "success", "WEB_RESULT": "skipped", "CHANGES_RESULT": "skipped"}, nil, nil, false, "web"},
		{"merge_group, web skipped", map[string]string{"EVENT": "merge_group", "TEST_RESULT": "success", "WEB_RESULT": "skipped", "CHANGES_RESULT": "skipped"}, nil, nil, false, "web"},
		{"workflow_dispatch, green", map[string]string{"EVENT": "workflow_dispatch", "TEST_RESULT": "success", "WEB_RESULT": "success", "CHANGES_RESULT": "skipped"}, nil, nil, true, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, ok := gateResult(t, c.env, c.statuses, c.perms)
			if ok != c.want {
				t.Fatalf("want success=%v, got %v\n%s", c.want, ok, out)
			}
			if !c.want && !strings.Contains(out, c.msg) {
				t.Errorf("the failure message should mention %q\n%s", c.msg, out)
			}
		})
	}
}

type ciWorkflow struct {
	On          map[string]any `yaml:"on"`
	Concurrency struct {
		Group  string `yaml:"group"`
		Cancel any    `yaml:"cancel-in-progress"`
	} `yaml:"concurrency"`
	Jobs map[string]struct {
		Name     string `yaml:"name"`
		Needs    any    `yaml:"needs"`
		If       string `yaml:"if"`
		Strategy any    `yaml:"strategy"`
		Steps    []struct {
			Run string `yaml:"run"`
			If  string `yaml:"if"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func loadWorkflow(t *testing.T, name string) (ciWorkflow, string) {
	t.Helper()
	raw := readFile(t, repoRoot(t), ".github/workflows/"+name)
	var wf ciWorkflow
	if err := yaml.Unmarshal([]byte(raw), &wf); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return wf, raw
}

func needsOf(v any) []string {
	switch n := v.(type) {
	case string:
		return []string{n}
	case []any:
		var out []string
		for _, x := range n {
			out = append(out, fmt.Sprint(x))
		}
		return out
	}
	return nil
}

// `gate` is the only required CI check. A job skipped by `if:` reports as passed
// and a skipped required check is a hole (D1592), so its only condition is
// `!cancelled()`; and the maintainer exclusion of `test` and `web-leg` must not
// let the release PR (opened by the maintainer's token) or a bot skip them.
func TestCIGateJobCannotBeSkippedAndMaintainerPRsSkipTheHeavyLegs(t *testing.T) {
	wf, _ := loadWorkflow(t, "ci.yml")
	gate, ok := wf.Jobs["gate"]
	if !ok || gate.Name != "gate" {
		t.Fatalf("ci.yml needs a job `gate` named gate")
	}
	cond := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(gate.If), "${{"), "}}"))
	if cond != "!cancelled()" {
		t.Errorf("gate.if must be exactly !cancelled(), got %q: a skipped required check passes", gate.If)
	}
	if gate.Strategy != nil {
		t.Error("gate must not be a matrix: a matrix renames its legs and the required name is lost")
	}
	needs := strings.Join(needsOf(gate.Needs), " ")
	for _, n := range []string{"test", "web-leg", "changes"} {
		if !strings.Contains(" "+needs+" ", " "+n+" ") {
			t.Errorf("gate must need %s, needs: %s", n, needs)
		}
	}
	for _, name := range []string{"test", "web-leg"} {
		c := wf.Jobs[name].If
		for _, want := range []string{"author_association", "OWNER", "MEMBER", "COLLABORATOR", "Bot", "release-please--"} {
			if !strings.Contains(c, want) {
				t.Errorf("%s.if lacks %q: a PR that is not a maintainer's must still run it", name, want)
			}
		}
	}
	for _, name := range []string{"test-windows", "web"} {
		if !strings.Contains(wf.Jobs[name].If, "github.event_name != 'pull_request'") {
			t.Errorf("%s no longer runs on a pull_request (gate is the required check), got %q", name, wf.Jobs[name].If)
		}
	}
}

// privacy.yml: the push run scans the same commits as a synchronize run (WP3).
func TestPrivacyDoesNotRescanAPRPush(t *testing.T) {
	wf, _ := loadWorkflow(t, "privacy.yml")
	pr, _ := wf.On["pull_request"].(map[string]any)
	var types []string
	for _, x := range pr["types"].([]any) {
		types = append(types, fmt.Sprint(x))
	}
	if strings.Join(types, ",") != "opened,edited,reopened" {
		t.Errorf("privacy pull_request types must be opened, edited, reopened (synchronize is covered by the push run), got %v", types)
	}
}

func TestCodeQLLeavesThePRAndSecurityKeepsTheDependencyScan(t *testing.T) {
	cq, _ := loadWorkflow(t, "codeql.yml")
	if _, ok := cq.On["pull_request"]; ok {
		t.Error("codeql.yml must not run on a pull_request")
	}
	for _, k := range []string{"push", "schedule", "workflow_dispatch"} {
		if _, ok := cq.On[k]; !ok {
			t.Errorf("codeql.yml must keep the %s trigger", k)
		}
	}
	sec, _ := loadWorkflow(t, "security.yml")
	pr, _ := sec.On["pull_request"].(map[string]any)
	paths := fmt.Sprint(pr["paths"])
	for _, p := range []string{"go.mod", "go.sum", "web/package.json", "web/package-lock.json"} {
		if !strings.Contains(" "+strings.Trim(paths, "[]")+" ", " "+p+" ") {
			t.Errorf("security.yml must keep scanning a PR that changes %s", p)
		}
	}
}

// Runs on main coalesce (one running, the newest pending): only a pull_request
// may cancel a run in flight.
func TestRunsOnMainAreNeverCancelled(t *testing.T) {
	for _, name := range []string{"ci.yml", "codeql.yml", "security.yml", "docs.yml"} {
		wf, _ := loadWorkflow(t, name)
		c := strings.Join(strings.Fields(fmt.Sprint(wf.Concurrency.Cancel)), " ")
		if c != "false" && c != "${{ github.event_name == 'pull_request' }}" {
			t.Errorf("%s: cancel-in-progress must be false or true only for a pull_request, got %q", name, c)
		}
		if wf.Concurrency.Group == "" {
			t.Errorf("%s has no concurrency group", name)
		}
		if strings.Contains(wf.Concurrency.Group, "github.sha") && name != "ci.yml" && name != "docs.yml" {
			t.Errorf("%s: a push to main must share one group, got %s", name, wf.Concurrency.Group)
		}
	}
	ci, _ := loadWorkflow(t, "ci.yml")
	if !strings.Contains(ci.Concurrency.Group, "merge_group") {
		t.Error("ci.yml: a merge_group run keeps its own commit group")
	}
	var step string
	for _, s := range ci.Jobs["main-status"].Steps {
		step += s.Run
	}
	if !strings.Contains(step, "gh run list") || !strings.Contains(step, "--status success") || !strings.Contains(step, "compare/") {
		t.Error("main-status must name the range since the last green push run (gh run list --status success, a compare/ URL)")
	}
}

// Every make target the CI web legs run is run by `make web-gate`, so a new web
// step in CI cannot be forgotten in a maintainer's local signoff.
func TestWebGateRunsEveryWebLegTarget(t *testing.T) {
	root := repoRoot(t)
	wf, _ := loadWorkflow(t, "ci.yml")
	m := regexp.MustCompile(`(?m)^web-gate:.*\n((?:\t.*\n)+)`).FindStringSubmatch(readFile(t, root, "Makefile"))
	if m == nil {
		t.Fatal("Makefile has no web-gate recipe")
	}
	var legTargets []string
	for _, s := range wf.Jobs["web-leg"].Steps {
		for _, tm := range regexp.MustCompile(`make ([a-z][a-z0-9-]*)`).FindAllStringSubmatch(s.Run, -1) {
			legTargets = append(legTargets, tm[1])
		}
	}
	if len(legTargets) < 3 {
		t.Fatalf("found %v make targets in the web-leg steps, expected at least web-check, web-test, e2e-web", legTargets)
	}
	for _, target := range legTargets {
		if !regexp.MustCompile(`\b` + regexp.QuoteMeta(target) + `( |$)`).MatchString(strings.ReplaceAll(m[1], "\n", " ")) {
			t.Errorf("web-gate does not run `make %s`, which the CI web legs run", target)
		}
	}
}

// #1800: `make worktree-add` fetches only the base's main, without tags. A bare
// `git fetch <remote>` also fetches tags and refuses to move one a release
// candidate re-cut on the same day moved, which stopped the target.
func TestWorktreeAddFetchesWithoutTags(t *testing.T) {
	m := regexp.MustCompile(`(?m)^worktree-add:.*\n((?:\t.*\n)+)`).FindStringSubmatch(readFile(t, repoRoot(t), "Makefile"))
	if m == nil {
		t.Fatal("Makefile has no worktree-add recipe")
	}
	if !regexp.MustCompile(`git fetch --no-tags \$\$base main\b`).MatchString(m[1]) {
		t.Errorf("worktree-add must run `git fetch --no-tags $$base main` (a bare fetch also fetches tags and stops on a moved release candidate tag):\n%s", m[1])
	}
}
