package repodocs

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// runFilters feeds paths to scripts/ci-filters.sh and returns its outputs.
func runFilters(t *testing.T, headRef string, paths ...string) map[string]string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the script runs on ubuntu only; bash on Windows may be WSL")
	}
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
		docker     bool
		forge      bool
	}{
		{"docs-only repodocs change", "feat/x", []string{"internal/repodocs/repodocs.go"}, false, false, false, false, false, false},
		{"kb source", "feat/x", []string{"internal/kb/x.go"}, true, true, false, false, false, false},
		{"kb test", "feat/x", []string{"internal/kb/x_test.go"}, false, true, false, false, false, false},
		{"non-Go embedded file", "feat/x", []string{"internal/skillbundle/bundled/a/SKILL.md"}, true, false, false, false, false, false},
		{"binary entry point", "feat/x", []string{"cmd/cartographer/main.go"}, true, true, false, false, false, false},
		{"frontend", "feat/x", []string{"web/src/a.ts"}, true, false, false, false, false, false},
		{"rebuilt bundle only", "feat/x", []string{"internal/webui/dist/index.html"}, false, false, false, false, false, false},
		{"windows-only file", "feat/x", []string{"internal/lint/a_windows.go"}, true, true, false, false, false, false},
		{"docs", "feat/x", []string{"docs/testing.md"}, false, false, false, false, false, false},
		{"demo worker", "feat/x", []string{"deploy/demo/worker.js"}, false, false, true, false, true, false},
		{"demo terraform", "feat/x", []string{"deploy/demo/main.tf"}, false, false, true, true, true, false},
		{"ci.yml only", "feat/x", []string{".github/workflows/ci.yml"}, true, true, true, false, true, true},
		{"filter script only", "feat/x", []string{"scripts/ci-filters.sh"}, true, true, true, false, true, true},
		{"dockerfile", "feat/x", []string{"Dockerfile"}, false, false, false, false, true, false},
		{"go.mod", "feat/x", []string{"go.mod"}, true, true, false, false, true, false},
		{"demo guestbook seed", "feat/x", []string{"demo/guestbook/index.md"}, false, false, false, false, true, false},
		{"image smoke script", "feat/x", []string{"test/smoke/image.sh"}, false, false, false, false, true, false},
		{"forge hook", "feat/x", []string{"test/forge/pre-receive.sh"}, false, true, false, false, false, true},
		{"forge runner", "feat/x", []string{"test/forge/run.sh"}, false, false, false, false, false, true},
		{"release PR", "release-please--branches--main", []string{"CHANGELOG.md"}, true, true, true, true, true, true},
	}
	for _, c := range cases {
		got := runFilters(t, c.head, c.paths...)
		for k, want := range map[string]bool{"web": c.web, "windows": c.win, "demo": c.demo, "tf": c.tf, "docker": c.docker, "forge": c.forge} {
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
	excluded := map[string]bool{}
	// The list is parsed out of the script, so the two cannot drift. Each entry
	// is a directory: its subpackages (changelog/gen, testkbs/gen) go with it.
	m := regexp.MustCompile(`(?m)^not_in_binary='\^internal/\(([^)]*)\)/'`).FindStringSubmatch(readFile(t, root, "scripts/ci-filters.sh"))
	if m == nil {
		t.Fatal("scripts/ci-filters.sh: cannot find the not_in_binary line")
	}
	for _, d := range strings.Split(m[1], "|") {
		for _, p := range list("list", "./internal/"+d+"/...") {
			excluded[p] = true
		}
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

// Every Makefile target is run by a workflow or declared local on purpose: a
// target nobody runs rots (`smoke` did, unnoticed, D994).
func TestEveryMakeTargetIsRunByCIOrDeclaredLocal(t *testing.T) {
	root := repoRoot(t)
	local := map[string]bool{
		"help": true, "build": true, "run": true, "run-http": true, "dev": true, "clean": true,
		"fmt": true, "hero": true, "web": true, "worktree-add": true, "worktree-rm": true,
		"decisions-index": true, "decisions-new": true, "changelog-areas": true, "codemap": true,
		"settings-example": true, "demo-kb": true, "demo-image": true,
		// The docker job builds with buildx (layer cache in the GitHub cache) and
		// runs test/smoke/image.sh itself; these two are the same checks by hand.
		"docker": true, "docker-smoke": true,
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
		if makeInvokes(wf.String(), n) {
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

// makeInvokes reports whether some line runs `make ... <target>` with target as
// a whole word: `\b` would match before a hyphen, and `make smoke-http` would
// then count as running `smoke`.
func makeInvokes(text, target string) bool {
	return regexp.MustCompile(`(?m)(^|[ \t])make[ \t]+([^\s]+[ \t]+)*` + regexp.QuoteMeta(target) + `([ \t]|$)`).MatchString(text)
}

func TestMakeInvokesIsATokenMatch(t *testing.T) {
	if makeInvokes("- run: make smoke-http\n- run: make e2e\n", "smoke") {
		t.Error("make smoke-http must not count as running smoke")
	}
	if !makeInvokes("- run: make smoke-http\n- run: make smoke\n", "smoke") {
		t.Error("make smoke must count as running smoke")
	}
	if !makeInvokes("run: make e2e-web E2E_WEB_ARGS=x\n", "e2e-web") || makeInvokes("run: make e2e-web\n", "e2e") {
		t.Error("e2e-web and e2e are different targets")
	}
}
