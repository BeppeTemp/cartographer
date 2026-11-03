package repodocs

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// scripts/freeze-open.sh is the one place that says whether the freeze is open: a
// release candidate newer than the latest stable release exists (D1384); the
// Dependabot workflows ask it (D1180). TRAP pinned here: the stable release is read
// from the releases listing, never `releases/latest` (a 0.x pre-release flag hides
// it), and a draft is not released yet.
func TestFreezeOpenFromTheCandidate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the script runs on ubuntu only; bash on Windows may be WSL")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	cases := []struct {
		name      string
		releases  *string // "<tag>:<draft> ..."; nil = not injected (unreadable)
		open      bool
		candidate string // oldest candidate of the pending version, when open
	}{
		{"no candidate", ptr("v0.8.0:false"), false, ""},
		{"candidate newer than stable", ptr("v0.9.0-rc.20260101:true v0.8.0:false"), true, "v0.9.0-rc.20260101"},
		{"stable published, candidate still there", ptr("v0.9.0-rc.20260101:false v0.9.0:false v0.8.0:false"), false, ""},
		{"stable still a draft", ptr("v0.9.0-rc.20260101:false v0.9.0:true v0.8.0:false"), true, "v0.9.0-rc.20260101"},
		{"numeric, not lexical", ptr("v0.10.0-rc.20260101:false v0.9.0:false"), true, "v0.10.0-rc.20260101"},
		{"two candidates of one version", ptr("v0.9.0-rc.20260103:false v0.9.0-rc.20260101:false v0.8.0:false"), true, "v0.9.0-rc.20260101"},
		{"candidate of a lower version", ptr("v0.8.0-rc.20260101:false v0.9.0:false"), false, ""},
	}
	for _, c := range cases {
		cmd := exec.Command(bash, "scripts/freeze-open.sh", "--candidate")
		cmd.Dir = repoRoot(t)
		cmd.Env = append(cmd.Environ(), "GH_REPO=example/repo")
		if c.releases != nil {
			cmd.Env = append(cmd.Env, "FREEZE_RELEASES="+*c.releases)
		} else {
			// No injection and a gh that fails: the listing is unreadable and counts as open.
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd.Env = append(cmd.Env, "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		}
		out, err := cmd.Output()
		var ee *exec.ExitError
		switch {
		case err == nil:
			if !c.open {
				t.Errorf("%s: freeze reported open, want closed", c.name)
			}
			if got := strings.TrimSpace(string(out)); got != c.candidate {
				t.Errorf("%s: candidate %q, want %q", c.name, got, c.candidate)
			}
		case errors.As(err, &ee) && ee.ExitCode() == 1:
			if c.open {
				t.Errorf("%s: freeze reported closed, want open", c.name)
			}
		default:
			t.Errorf("%s: freeze-open.sh: %v", c.name, err)
		}
	}
}

// An unreadable listing is open for the gate (exit 0, nothing to wait on) but is
// never "no candidate" for a caller that deletes candidates: --candidate(s) exit 3
// with the message on stderr and nothing on stdout (release-train `discard` fails on it).
func TestFreezeOpenUnreadableListing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the script runs on ubuntu only; bash on Windows may be WSL")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		args []string
		code int
	}{{nil, 0}, {[]string{"--candidate"}, 3}, {[]string{"--candidates"}, 3}} {
		cmd := exec.Command(bash, append([]string{"scripts/freeze-open.sh"}, c.args...)...)
		cmd.Dir = repoRoot(t)
		cmd.Env = append(cmd.Environ(), "GH_REPO=example/repo", "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		code := 0
		var ee *exec.ExitError
		if err := cmd.Run(); errors.As(err, &ee) {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		if code != c.code || stdout.Len() != 0 || !strings.Contains(stderr.String(), "cannot list the releases") {
			t.Errorf("freeze-open.sh %v: exit %d stdout %q stderr %q, want exit %d, no stdout, the message", c.args, code, stdout.String(), stderr.String(), c.code)
		}
	}
}

// A listing that fails must fail the job, not read as "no PR": `for n in $(gh ...)`
// and `< <(gh ...)` hide the exit status from bash -e. The step is run for real with
// a gh that always fails, in both freeze states.
func TestDependabotArmFailsOnUnreadableListing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the script runs on ubuntu only; bash on Windows may be WSL")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	root := repoRoot(t)
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(readFile(t, root, ".github/workflows/dependabot-arm.yml")), &wf); err != nil {
		t.Fatal(err)
	}
	var run string
	for _, st := range wf.Jobs["reconcile"].Steps {
		if st.Run != "" {
			run = st.Run
		}
	}
	if run == "" {
		t.Fatal("dependabot-arm.yml: no run step in reconcile")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, releases := range map[string]string{"freeze open": "v0.9.0-rc.20260101:false v0.8.0:false", "freeze closed": "v0.8.0:false"} {
		cmd := exec.Command(bash, "-e", "-o", "pipefail", "-c", run)
		cmd.Dir = root
		cmd.Env = append(cmd.Environ(), "GH_REPO=example/repo", "FREEZE_RELEASES="+releases,
			"GITHUB_STEP_SUMMARY=/dev/null", "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		if err := cmd.Run(); err == nil {
			t.Errorf("dependabot-arm.yml, %s: the step succeeded with a failing gh listing", name)
		}
	}
}

// No workflow loops (`for ... in $(gh`, `done < <(gh`) over a gh listing: the exit status of the listing is lost.
func TestWorkflowsDoNotLoopOverUncheckedListings(t *testing.T) {
	root := repoRoot(t)
	files, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflows: %v", err)
	}
	re := regexp.MustCompile(`for [a-z_]+ in \$\(\s*gh\b|done\s*<\s*<\(\s*gh\b`)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if m := re.Find(b); m != nil {
			t.Errorf("%s: %q hides the exit status of gh from bash -e: assign the listing first", filepath.Base(f), m)
		}
	}
}

func ptr(s string) *string { return &s }

// The rule a bot follows to arm auto-merge, shared by both workflows (D329, D1020).
func TestDependabotEligible(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the script runs on ubuntu only; bash on Windows may be WSL")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	cases := []struct {
		args []string
		ok   bool
	}{
		{[]string{"go_modules", "version-update:semver-minor"}, true},
		{[]string{"github_actions", "version-update:semver-patch", "version-update:semver-minor"}, true},
		{[]string{"go_modules", "version-update:semver-minor", "version-update:semver-major"}, false},
		{[]string{"docker", "version-update:semver-patch"}, false},
	}
	for _, c := range cases {
		cmd := exec.Command(bash, append([]string{"scripts/dependabot-eligible.sh"}, c.args...)...)
		cmd.Dir = repoRoot(t)
		if got := cmd.Run() == nil; got != c.ok {
			t.Errorf("dependabot-eligible.sh %v: eligible=%v, want %v", c.args, got, c.ok)
		}
	}
}

// A Dependabot bump that arms itself in the freeze moves main off the release
// candidate and the release gate skips (D1180): the arming step asks the script,
// what was armed before the candidate is disarmed, and the re-arm after the release
// fails loudly and applies the same eligibility rule.
func TestDependabotWorkflowsHonourTheFreeze(t *testing.T) {
	root := repoRoot(t)
	auto := readFile(t, root, ".github/workflows/dependabot-automerge.yml")
	for _, want := range []string{"scripts/freeze-open.sh", "steps.freeze.outputs.open != 'true'", "scripts/dependabot-eligible.sh", "ref: main"} {
		if !strings.Contains(auto, want) {
			t.Errorf("dependabot-automerge.yml: missing %q (checkout main, never the PR, before running a script with a write token)", want)
		}
	}
	arm := readFile(t, root, ".github/workflows/dependabot-arm.yml")
	for _, want := range []string{
		"workflow_run", "workflows: [release, preview, release-train]", "workflow_dispatch",
		"scripts/freeze-open.sh", "scripts/dependabot-eligible.sh", "gh pr merge --auto --squash", "gh pr merge --disable-auto",
		"cancel-in-progress: false", "GITHUB_STEP_SUMMARY", "exit \"$failed\"", "ref: main",
	} {
		if !strings.Contains(arm, want) {
			t.Errorf("dependabot-arm.yml: missing %q", want)
		}
	}
	// freeze-open.sh lists the releases: without `contents: read` and a token the
	// call fails and the freeze counts as open, so nothing is ever armed.
	for name, raw := range map[string]string{"dependabot-automerge.yml": auto, "dependabot-arm.yml": arm} {
		var wf struct {
			Permissions map[string]string `yaml:"permissions"`
			Jobs        map[string]struct {
				Permissions map[string]string `yaml:"permissions"`
				Steps       []struct {
					Run string            `yaml:"run"`
					Env map[string]string `yaml:"env"`
				} `yaml:"steps"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal([]byte(raw), &wf); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		calls := 0
		for jobName, job := range wf.Jobs {
			for _, st := range job.Steps {
				if strings.Contains(st.Run, "scripts/freeze-open.sh") {
					calls++
					// Workflow level or job level: zizmor wants write scopes on the job.
					ok := func(m map[string]string) bool { return m["contents"] == "read" || m["contents"] == "write" }
					if !ok(wf.Permissions) && !ok(job.Permissions) {
						t.Errorf("%s: job %s runs freeze-open.sh without `contents: read`", name, jobName)
					}
					if st.Env["GH_TOKEN"] == "" || st.Env["GH_REPO"] == "" {
						t.Errorf("%s: a step runs freeze-open.sh without GH_TOKEN and GH_REPO in its env", name)
					}
				}
			}
		}
		if calls == 0 {
			t.Errorf("%s: no step runs freeze-open.sh", name)
		}
	}
	if strings.Contains(arm, "schedule") || strings.Contains(arm, "Europe/Rome") {
		t.Error("dependabot-arm.yml: no schedule and no time zone: the freeze state decides (D1384)")
	}
	if strings.Contains(arm, "|| echo") {
		t.Error("dependabot-arm.yml: a failed arm must fail the job, not be swallowed")
	}
}
