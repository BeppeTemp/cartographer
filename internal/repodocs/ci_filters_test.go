package repodocs

import (
	"bytes"
	"os"
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
	return runFiltersEnv(t, []string{"HEAD_IN_BASE_REPO=true"}, headRef, paths...)
}

// runFiltersEnv is runFilters with env added to the environment, and no HEAD_IN_BASE_REPO but env's.
func runFiltersEnv(t *testing.T, env []string, headRef string, paths ...string) map[string]string {
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
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HEAD_IN_BASE_REPO=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, env...)
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
		macos      bool
		helm       bool
	}{
		{"docs-only testkbs change", "feat/x", []string{"internal/testkbs/kbs.go"}, false, false, false, false, false, false, false, false},
		{"kb source", "feat/x", []string{"internal/kb/x.go"}, true, true, false, false, false, false, false, false},
		{"kb test", "feat/x", []string{"internal/kb/x_test.go"}, false, true, false, false, false, false, false, false},
		{"non-Go embedded file", "feat/x", []string{"internal/skillbundle/bundled/a/SKILL.md"}, true, false, false, false, false, false, false, false},
		{"binary entry point", "feat/x", []string{"cmd/cartographer/main.go"}, true, true, false, false, false, false, false, false},
		{"frontend", "feat/x", []string{"web/src/a.ts"}, true, false, false, false, false, false, false, false},
		{"rebuilt bundle only", "feat/x", []string{"internal/webui/dist/index.html"}, false, false, false, false, false, false, false, false},
		{"windows-only file", "feat/x", []string{"internal/lint/a_windows.go"}, true, true, false, false, false, false, false, false},
		{"docs", "feat/x", []string{"docs/testing.md"}, false, false, false, false, false, false, false, false},
		{"demo worker", "feat/x", []string{"deploy/demo/worker.js"}, false, false, true, false, true, false, false, false},
		{"demo terraform", "feat/x", []string{"deploy/demo/main.tf"}, false, false, true, true, true, false, false, false},
		{"ci.yml only", "feat/x", []string{".github/workflows/ci.yml"}, true, true, true, false, true, true, true, true},
		{"filter script only", "feat/x", []string{"scripts/ci-filters.sh"}, true, true, true, false, true, true, true, true},
		{"dockerfile", "feat/x", []string{"Dockerfile"}, false, false, false, false, true, false, false, false},
		{"go.mod", "feat/x", []string{"go.mod"}, true, true, false, false, true, false, false, false},
		{"image smoke script", "feat/x", []string{"test/smoke/image.sh"}, false, false, false, false, true, false, false, false},
		{"surveyor image smoke script", "feat/x", []string{"test/smoke/surveyor-image.sh"}, false, false, false, false, true, false, false, false},
		{"forge hook", "feat/x", []string{"test/forge/pre-receive.sh"}, false, true, false, false, false, true, false, false},
		{"forge runner", "feat/x", []string{"test/forge/run.sh"}, false, false, false, false, false, true, false, false},
		{"service package", "feat/x", []string{"internal/service/manager.go"}, true, true, false, false, false, false, true, false},
		{"tray package", "feat/x", []string{"internal/tray/model.go"}, true, true, false, false, false, false, true, false},
		{"service flags", "feat/x", []string{"cmd/cartographer/service.go"}, true, true, false, false, false, false, true, false},
		{"upgrade-repair command", "feat/x", []string{"cmd/cartographer/upgraderepair.go"}, true, true, false, false, false, false, true, false},
		{"another command", "feat/x", []string{"cmd/cartographer/serve.go"}, true, true, false, false, false, false, false, false},
		{"install.sh", "feat/x", []string{"install.sh"}, false, true, false, false, false, false, true, false},
		{"goreleaser config", "feat/x", []string{".goreleaser.yaml"}, false, false, false, false, false, false, true, false},
		{"install tests", "feat/x", []string{"test/install/run.sh"}, false, true, false, false, false, false, true, false},
		{"macos tests", "feat/x", []string{"test/macos/cask.sh"}, false, false, false, false, false, false, true, false},
		{"chart template", "feat/x", []string{"deploy/helm/cartographer/templates/meridian-deployment.yaml"}, false, false, false, false, false, false, false, true},
		{"chart test script", "feat/x", []string{"test/helm/run.sh"}, false, false, false, false, false, false, false, true},
		{"helm install script", "feat/x", []string{"scripts/install-helm.sh"}, false, false, false, false, false, false, false, true},
		{"Makefile", "feat/x", []string{"Makefile"}, true, true, false, false, false, false, false, true},
		{"an unrelated Go file for helm", "feat/x", []string{"internal/kb/y.go"}, true, true, false, false, false, false, false, false},
		// The release PR runs everything, but while its diff is only what the release workflow writes
		// `web` and `helm` follow its paths and skip; `forge` stays, the release PR being the only
		// place the real-forge suite runs unasked (#2441).
		{"release PR, generated files only", "release-please--branches--main", []string{"CHANGELOG.md", ".release-please-manifest.json", "docs/decisions.md"}, false, true, true, true, true, true, true, false},
		{"release PR, the changelog alone", "release-please--branches--main", []string{"CHANGELOG.md"}, false, true, true, true, true, true, true, false},
		{"release PR of a maintenance branch", "release-please--branches--release-0.1", []string{"CHANGELOG.md", ".release-please-manifest.json"}, false, true, true, true, true, true, true, false},
		{"release PR that carries anything else", "release-please--branches--main", []string{"CHANGELOG.md", "docs/testing.md"}, true, true, true, true, true, true, true, true},
		{"release PR, a file that only looks generated", "release-please--branches--main", []string{"deploy/CHANGELOG.md"}, true, true, true, true, true, true, true, true},
		{"release PR whose files could not be listed", "release-please--branches--main", nil, true, true, true, true, true, true, true, true},
		{"release PR, generated files and a chart", "release-please--branches--main", []string{"CHANGELOG.md", "deploy/helm/Chart.yaml"}, true, true, true, true, true, true, true, true},
		{"generated files on an ordinary PR", "feat/x", []string{"CHANGELOG.md"}, false, false, false, false, false, false, false, false},
	}
	for _, c := range cases {
		got := runFilters(t, c.head, c.paths...)
		for k, want := range map[string]bool{"web": c.web, "windows": c.win, "demo": c.demo, "tf": c.tf, "docker": c.docker, "forge": c.forge, "macos": c.macos, "helm": c.helm} {
			if got[k] != map[bool]string{true: "true", false: "false"}[want] {
				t.Errorf("%s: %s=%s, want %v", c.name, k, got[k], want)
			}
		}
	}
}

// The release PR's skip is for a branch of this repository: ci.yml says so in HEAD_IN_BASE_REPO, and
// without a plain `true` there (a fork's branch named release-please--*, a caller that does not say,
// such as scripts/signoff.sh) the release PR runs everything (#2441).
func TestCIFiltersReleaseSkipNeedsTheBaseRepository(t *testing.T) {
	generated := []string{"CHANGELOG.md", ".release-please-manifest.json", "docs/decisions.md"}
	for _, env := range [][]string{nil, {"HEAD_IN_BASE_REPO=false"}, {"HEAD_IN_BASE_REPO="}, {"HEAD_IN_BASE_REPO=TRUE"}, {"HEAD_IN_BASE_REPO=1"}} {
		got := runFiltersEnv(t, env, "release-please--branches--main", generated...)
		for _, k := range []string{"web", "helm", "forge", "windows", "docker", "macos", "demo", "tf"} {
			if got[k] != "true" {
				t.Errorf("env %v: %s=%s on the release PR, want true (no skip without HEAD_IN_BASE_REPO=true)", env, k, got[k])
			}
		}
	}
	if got := runFiltersEnv(t, []string{"HEAD_IN_BASE_REPO=true"}, "release-please--branches--main", generated...); got["web"] != "false" || got["helm"] != "false" || got["forge"] != "true" {
		t.Errorf("a release PR of this repository skips web and helm and keeps forge, got %v", got)
	}
	wf := readFile(t, repoRoot(t), ".github/workflows/ci.yml")
	if !strings.Contains(wf, "          HEAD_IN_BASE_REPO: ${{ github.event.pull_request.head.repo.full_name == github.repository }}\n") {
		t.Error("ci.yml's changes job does not tell ci-filters.sh whether the PR's head is a branch of this repository")
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
		"fmt": true, "hero": true, "web": true, "worktree-add": true, "worktree-rm": true, "worktree-clean": true,
		"decisions-index": true, "decisions-new": true, "changelog-areas": true, "codemap": true, "kiro-agents": true, "runs-where": true,
		"settings-example": true, "openapi": true, "demo-kb": true, "demo-image": true,
		"signoff": true, "signoff-platform": true, "web-gate": true,
		// The signoff's targeted retry of the Go tests a red run named (scripts/signoff.sh).
		"test-rerun": true,
		// ci.yml's test legs run gate in its parts, the tests through scripts/test-shard.sh
		// (D-new-ci-shards; TestTheTestLegsRunTheGateInParts pins that they cover it).
		"gate": true, "test": true,
		// The fast tier: what an implementer runs while iterating; make gate is CI's.
		"check": true, "check-run": true, "check-hosted": true,
		// The usage test: a headless agent in a maintainer's own session, never a workflow
		// (TestNoWorkflowCallsTheUsageTest).
		"usage-test": true,
		// The lander queues and lands from a maintainer's machine (scripts/land.sh).
		"ready": true, "land": true, "land-status": true, "requeue": true, "status": true, "unlanded": true,
		// The issue queue and a maintainer's accepted bug (scripts/issue-queue.sh, issue-new.sh).
		"issues": true, "issue": true,
		// The docker job builds with buildx (layer cache in the GitHub cache) and
		// runs test/smoke/image.sh itself; these two are the same checks by hand.
		// docker-surveyor(-smoke) are the Surveyor image's, built and smoked by the
		// same job.
		"docker": true, "docker-smoke": true, "docker-surveyor": true, "docker-surveyor-smoke": true,
	}
	var wf strings.Builder
	files, _ := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	for _, f := range files {
		wf.WriteString(readFile(t, root, filepath.ToSlash(strings.TrimPrefix(f, root+string(filepath.Separator)))))
	}
	// Prerequisites count: `make gate` runs test, vet, race and fmt-check.
	mk := readFile(t, root, "Makefile")
	targets, deps := makefileTargets(mk)
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

// makefileTargets returns the targets a Makefile defines, in file order, and
// their prerequisites. Shared with the documentation structure gate, which checks
// that every `make <target>` the text cites is one of them.
func makefileTargets(mk string) ([]string, map[string][]string) {
	deps := map[string][]string{}
	var targets []string
	for _, m := range regexp.MustCompile(`(?m)^([a-z][a-z0-9-]*):([^#\n=]*)`).FindAllStringSubmatch(mk, -1) {
		targets = append(targets, m[1])
		deps[m[1]] = strings.Fields(m[2])
	}
	return targets, deps
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
