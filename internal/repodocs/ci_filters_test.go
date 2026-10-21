package repodocs

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// runFilters feeds paths to scripts/ci-filters.sh and returns its outputs.
func runFilters(t *testing.T, headRef string, paths ...string) map[string]string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	cmd := exec.Command(bash, "scripts/ci-filters.sh", headRef)
	cmd.Dir = repoRoot(t)
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\n") + "\n")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("ci-filters.sh: %v\n%s", err, errb.String())
	}
	got := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		k, v, _ := strings.Cut(l, "=")
		got[k] = v
	}
	return got
}

// The path filters decide which CI jobs a PR runs; a filter that matches
// `internal/` wholesale ran Playwright on two runners for every Go change
// (D994). The table pins the intended meaning.
func TestCIFiltersTable(t *testing.T) {
	cases := []struct {
		name, head string
		paths      []string
		web, win   bool
		demo, tf   bool
	}{
		{"docs-only repodocs change", "feat/x", []string{"internal/repodocs/repodocs.go"}, false, false, false, false},
		{"kb source", "feat/x", []string{"internal/kb/x.go"}, true, true, false, false},
		{"kb test", "feat/x", []string{"internal/kb/x_test.go"}, false, true, false, false},
		{"non-Go embedded file", "feat/x", []string{"internal/skillbundle/bundled/a/SKILL.md"}, true, false, false, false},
		{"binary entry point", "feat/x", []string{"cmd/cartographer/main.go"}, true, true, false, false},
		{"frontend", "feat/x", []string{"web/src/a.ts"}, true, false, false, false},
		{"rebuilt bundle only", "feat/x", []string{"internal/webui/dist/index.html"}, false, false, false, false},
		{"windows-only file", "feat/x", []string{"internal/lint/a_windows.go"}, true, true, false, false},
		{"docs", "feat/x", []string{"docs/testing.md"}, false, false, false, false},
		{"demo worker", "feat/x", []string{"deploy/demo/worker.js"}, false, false, true, false},
		{"demo terraform", "feat/x", []string{"deploy/demo/main.tf"}, false, false, true, true},
		{"release PR", "release-please--branches--main", []string{"CHANGELOG.md"}, true, true, true, true},
	}
	for _, c := range cases {
		got := runFilters(t, c.head, c.paths...)
		for k, want := range map[string]bool{"web": c.web, "windows": c.win, "demo": c.demo, "tf": c.tf} {
			if got[k] != map[bool]string{true: "true", false: "false"}[want] {
				t.Errorf("%s: %s=%s, want %v", c.name, k, got[k], want)
			}
		}
	}
}

// The `web` filter skips the packages the binary cannot import. Keep that list
// equal to what `go list` says, in both directions.
func TestCIFiltersWebExclusionsMatchTheBinary(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not on PATH")
	}
	root := repoRoot(t)
	list := func(args ...string) []string {
		cmd := exec.Command(goBin, args...)
		cmd.Dir = root
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("go %v: %v", args, err)
		}
		return strings.Fields(string(out))
	}
	deps := map[string]bool{}
	for _, p := range list("list", "-deps", "./cmd/cartographer") {
		deps[p] = true
	}
	mod := strings.TrimSpace(strings.Join(list("list", "-m"), " "))
	excluded := map[string]bool{}
	for _, d := range []string{"changelog", "changelog/gen", "repodocs", "testkbs", "testkbs/gen", "webui/gen"} {
		excluded[mod+"/internal/"+d] = true
	}
	for p := range excluded {
		if deps[p] {
			t.Errorf("%s is imported by cmd/cartographer: remove it from not_in_binary in scripts/ci-filters.sh and from this list, or drop the import", p)
		}
	}
	var missing []string
	for _, p := range list("list", "./internal/...") {
		if !deps[p] && !excluded[p] {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		t.Errorf("packages the binary does not import and ci-filters.sh does not exclude (add them to not_in_binary and to this test): %v", missing)
	}
}

// A PR branch is scanned on pull_request (BASE..HEAD); a push trigger on every
// branch scanned it twice (D994).
func TestPrivacyPushIsMainOnly(t *testing.T) {
	raw := readFile(t, repoRoot(t), ".github/workflows/privacy.yml")
	if !regexp.MustCompile(`(?m)^  push:\n    branches: \[main\]\n`).MatchString(raw) {
		t.Error("privacy.yml: `push:` must be limited to `branches: [main]`; the PR is scanned on pull_request")
	}
}

// Every Makefile target is run by a workflow or declared local on purpose: a
// target nobody runs rots (`smoke` did, unnoticed, D994).
func TestEveryMakeTargetIsRunByCIOrDeclaredLocal(t *testing.T) {
	root := repoRoot(t)
	local := map[string]bool{
		"help": true, "build": true, "run": true, "run-http": true, "dev": true, "clean": true,
		"fmt": true, "hero": true, "web": true, "worktree-add": true, "worktree-rm": true,
		"decisions-index": true, "decisions-new": true, "changelog-areas": true, "codemap": true,
		"settings-example": true, "demo-kb": true, "demo-image": true,
		// Until the release-candidate plan (#995) wires them into a workflow.
		"docker": true, "demo-smoke": true,
	}
	var wf strings.Builder
	files, _ := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	for _, f := range files {
		wf.WriteString(readFile(t, root, filepath.ToSlash(strings.TrimPrefix(f, root+string(filepath.Separator)))))
	}
	// Prerequisites count: `make gate` runs test, vet, race and fmt-check.
	mk := readFile(t, root, "Makefile")
	deps := map[string][]string{}
	var targets []string
	for _, m := range regexp.MustCompile(`(?m)^([a-z][a-z0-9-]*):([^#\n=]*)`).FindAllStringSubmatch(mk, -1) {
		targets = append(targets, m[1])
		deps[m[1]] = strings.Fields(m[2])
	}
	run := map[string]bool{}
	var mark func(string)
	mark = func(n string) {
		if run[n] {
			return
		}
		run[n] = true
		for _, d := range deps[n] {
			mark(d)
		}
	}
	for _, n := range targets {
		if regexp.MustCompile(`make [^\n]*\b` + regexp.QuoteMeta(n) + `\b`).MatchString(wf.String()) {
			mark(n)
		}
	}
	sort.Strings(targets)
	for _, n := range targets {
		if !run[n] && !local[n] {
			t.Errorf("Makefile target %q is run by no workflow and is not in the local-only list: wire it into .github/workflows/ or add it to this test's list", n)
		}
	}
}
