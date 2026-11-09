package repodocs

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The release on demand (D1384) fails silently in three ways, and nothing else
// reads them: a schedule that brings the calendar back, a freeze check measured
// from the wrong place, and a merge with the default token (the merge succeeds,
// but no `push` workflow runs, so release-please never cuts the tag).
func TestReleaseTrainWorkflowIsOnDemandAndPinsMergeToken(t *testing.T) {
	root := repoRoot(t)
	raw := readFile(t, root, ".github/workflows/release-train.yml")

	var wf struct {
		On   map[string]interface{} `yaml:"on"`
		Jobs map[string]struct {
			Steps []struct {
				Name string            `yaml:"name"`
				ID   string            `yaml:"id"`
				If   string            `yaml:"if"`
				Uses string            `yaml:"uses"`
				Run  string            `yaml:"run"`
				Env  map[string]string `yaml:"env"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(raw), &wf); err != nil {
		t.Fatalf("release-train.yml: %v", err)
	}

	if _, ok := wf.On["schedule"]; ok {
		t.Error("release-train.yml: a `schedule:` trigger brings the weekly train back; a release is dispatched by hand (D1384)")
	}
	var dispatch struct {
		Inputs struct {
			Action struct {
				Options []string `yaml:"options"`
			} `yaml:"action"`
		} `yaml:"inputs"`
	}
	d, _ := yaml.Marshal(wf.On["workflow_dispatch"])
	if err := yaml.Unmarshal(d, &dispatch); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(dispatch.Inputs.Action.Options, ","); got != "rc,release,discard" {
		t.Errorf("release-train.yml: action options %q, want rc,release,discard", got)
	}
	// The freeze check is measured from the commit of the oldest candidate, never a
	// weekday: the newest candidate is always the head of main (the gate requires it).
	if !strings.Contains(raw, "compare/$cand_sha...main") || !strings.Contains(raw, "freeze-open.sh --candidate") {
		t.Error("release-train.yml: the freeze check must list `compare/<oldest candidate commit>...main` (freeze-open.sh --candidate)")
	}
	if strings.Contains(raw, "last wednesday") || strings.Contains(raw, "Europe/Rome") {
		t.Error("release-train.yml: the calendar is gone (D1384): no `last wednesday`, no Europe/Rome")
	}

	merges, gates := 0, 0
	for name, job := range wf.Jobs {
		for _, st := range job.Steps {
			if st.ID == "security" {
				gates++
				if st.Env["SEC_TOKEN"] != "${{ secrets.SECURITY_GATE_TOKEN }}" {
					t.Errorf("release-train.yml: the security gate reads with %q: github.token cannot read Dependabot alerts or draft advisories", st.Env["SEC_TOKEN"])
				}
			}
			if name == "train" && st.Uses != "" {
				t.Errorf("release-train.yml: `uses: %s` in the job that holds the PAT: no third-party code runs with it", st.Uses)
			}
			if st.Uses != "" && !regexp.MustCompile(`^[^@\s]+@[0-9a-f]{40}$`).MatchString(st.Uses) {
				t.Errorf("release-train.yml: `uses: %s` is not pinned to a 40-hex commit SHA", st.Uses)
			}
			if !strings.Contains(st.Run, "gh pr merge") {
				continue
			}
			merges++
			// The merge waits for the security gate's output (D1123): a gate that is
			// skipped or removed must leave the release PR unmerged.
			if st.If != "steps.security.outputs.clear == 'true'" {
				t.Errorf("release-train.yml: job %s step %q merges with if=%q, want it gated on steps.security.outputs.clear", name, st.Name, st.If)
			}
			if st.Env["GH_TOKEN"] != "${{ secrets.RELEASE_PLEASE_TOKEN }}" {
				t.Errorf("release-train.yml: job %s step %q merges with GH_TOKEN=%q: a merge by github.token triggers no push workflow, release-please would never tag", name, st.Name, st.Env["GH_TOKEN"])
			}
		}
	}
	if gates != 1 {
		t.Errorf("release-train.yml: %d steps with id security, want exactly one", gates)
	}
	if merges != 1 {
		t.Errorf("release-train.yml: %d steps run `gh pr merge`, want exactly one", merges)
	}
}

// The release candidate is started before it is published (D995): a smoke step
// that came after the push would publish an image whose container does not
// start, and release-train.yml matches the candidate by the tag at the end of
// run-name.
func TestPreviewWorkflowSmokesBeforePush(t *testing.T) {
	raw := readFile(t, repoRoot(t), ".github/workflows/preview.yml")
	var wf struct {
		RunName string `yaml:"run-name"`
		Jobs    map[string]struct {
			Steps []struct {
				Uses string                 `yaml:"uses"`
				Run  string                 `yaml:"run"`
				With map[string]interface{} `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(raw), &wf); err != nil {
		t.Fatalf("preview.yml: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(wf.RunName), "${{ inputs.tag }}") {
		t.Errorf("preview.yml: run-name %q must end with the tag input: release-train.yml finds the candidate by it", wf.RunName)
	}
	// Every image smoke runs before the first push, and both images are smoked
	// and pushed (D1440): a Surveyor smoke after the push would publish an image
	// that does not start.
	lastSmoke, firstPush, smokes, surveyorPush := -1, -1, 0, false
	for _, job := range wf.Jobs {
		for i, s := range job.Steps {
			if strings.Contains(s.Run, "test/smoke/image.sh") || strings.Contains(s.Run, "test/smoke/surveyor-image.sh") {
				smokes++
				if i > lastSmoke {
					lastSmoke = i
				}
			}
			if p, ok := s.With["push"]; ok && p == true {
				if firstPush < 0 {
					firstPush = i
				}
				if s.With["target"] == "surveyor" {
					surveyorPush = true
				}
			}
		}
	}
	if smokes != 2 || firstPush < 0 {
		t.Fatalf("preview.yml: %d image smoke steps, first push at %d: want both smokes and a push", smokes, firstPush)
	}
	if lastSmoke > firstPush {
		t.Errorf("preview.yml: a smoke (step %d) runs after the first push (step %d): a candidate whose container does not start would be published", lastSmoke, firstPush)
	}
	if !surveyorPush {
		t.Error("preview.yml: no push step for target: surveyor")
	}
}

// A film that cannot render blocks the release candidate and so the train (D1068): the film job of preview.yml
// must not tolerate failure, film.yml must not swallow one anywhere in its path, the job that runs the checked-out code is read-only and
// the one that writes (only on main) runs none of it.
// Every action it uses is pinned to a full commit SHA (it holds a write token).
func TestPreviewCallsTheFilmWithoutToleratingFailure(t *testing.T) {
	root := repoRoot(t)
	type step struct {
		Uses            string            `yaml:"uses"`
		Run             string            `yaml:"run"`
		If              string            `yaml:"if"`
		With            map[string]string `yaml:"with"`
		ContinueOnError any               `yaml:"continue-on-error"`
	}
	var preview struct {
		Jobs map[string]struct {
			Uses            string            `yaml:"uses"`
			Permissions     map[string]string `yaml:"permissions"`
			ContinueOnError any               `yaml:"continue-on-error"`
			Needs           any               `yaml:"needs"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(readFile(t, root, ".github/workflows/preview.yml")), &preview); err != nil {
		t.Fatalf("preview.yml: %v", err)
	}
	calls := 0
	for name, job := range preview.Jobs {
		if job.Uses == "./.github/workflows/film.yml" {
			calls++
			if job.ContinueOnError != nil {
				t.Errorf("preview.yml: job %s calls film.yml with continue-on-error: a film that fails must fail the candidate", name)
			}
			if job.Permissions["contents"] != "write" {
				t.Errorf("preview.yml: job %s needs `permissions: contents: write` to call film.yml", name)
			}
		} else if strings.Contains(toString(job.Needs), "film") {
			t.Errorf("preview.yml: job %s needs the film job: the film and the image run in parallel", name)
		}
		if job.Uses != "" && job.Needs != nil {
			t.Errorf("preview.yml: job %s has needs: the film and the image run in parallel", name)
		}
	}
	if calls != 1 {
		t.Errorf("preview.yml: %d jobs call film.yml, want exactly one", calls)
	}

	raw := readFile(t, root, ".github/workflows/film.yml")
	var film struct {
		Permissions map[string]string `yaml:"permissions"`
		Jobs        map[string]struct {
			ContinueOnError any               `yaml:"continue-on-error"`
			Permissions     map[string]string `yaml:"permissions"`
			Needs           any               `yaml:"needs"`
			If              string            `yaml:"if"`
			Steps           []step            `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(raw), &film); err != nil {
		t.Fatalf("film.yml: %v", err)
	}
	if len(film.Permissions) != 1 || film.Permissions["contents"] != "read" {
		t.Errorf("film.yml: permissions %v, want exactly contents: read", film.Permissions)
	}
	// The job that runs the checked-out code never holds the write token, and does not leave one in .git/config.
	render, publish := film.Jobs["render"], film.Jobs["publish"]
	if len(render.Permissions) != 1 || render.Permissions["contents"] != "read" {
		t.Errorf("film.yml: job render has permissions %v, want exactly contents: read", render.Permissions)
	}
	checkouts := 0
	for _, st := range render.Steps {
		switch {
		case strings.HasPrefix(st.Uses, "actions/checkout@"):
			checkouts++
			if st.With["persist-credentials"] != "false" {
				t.Errorf("film.yml: job render checks out with persist-credentials %q, want false", st.With["persist-credentials"])
			}
		// A manual run of another ref renders unreviewed code: it must neither read nor write the caches main uses.
		case strings.HasPrefix(st.Uses, "actions/cache@"):
			if !strings.Contains(st.If, "steps.needed.outputs.trusted == 'true'") {
				t.Errorf("film.yml: render's cache step is not gated on trusted, if: %q", st.If)
			}
		case strings.HasPrefix(st.Uses, "actions/setup-node@"):
			if c := st.With["cache"]; c != "" && !strings.Contains(c, "steps.needed.outputs.trusted == 'true'") {
				t.Errorf("film.yml: render's setup-node cache %q is not gated on trusted", c)
			}
		}
	}
	if checkouts != 1 {
		t.Errorf("film.yml: job render has %d checkouts, want 1", checkouts)
	}
	if len(publish.Permissions) != 1 || publish.Permissions["contents"] != "write" || !strings.Contains(toString(publish.Needs), "render") {
		t.Errorf("film.yml: job publish must need render and hold exactly contents: write, got %v needs %v", publish.Permissions, publish.Needs)
	}
	if !strings.Contains(publish.If, "refs/heads/main") {
		t.Errorf("film.yml: job publish must be gated on refs/heads/main, if: %q", publish.If)
	}
	for _, st := range publish.Steps {
		if strings.HasPrefix(st.Uses, "actions/checkout@") && st.With["ref"] != "${{ needs.render.outputs.sha }}" {
			t.Errorf("film.yml: job publish checks out ref %q, want the rendered sha", st.With["ref"])
		}
		if strings.HasPrefix(st.Uses, "actions/download-artifact@") && st.With["path"] != "film/out" {
			t.Errorf("film.yml: job publish downloads to %q, want film/out only", st.With["path"])
		}
		if strings.Contains(st.Run, "npm ") || strings.Contains(st.Run, "npx ") || strings.Contains(st.Run, "render.sh") {
			t.Errorf("film.yml: job publish holds the write token and must not run the film's code: %q", st.Run)
		}
	}
	pinned := regexp.MustCompile(`^[^@\s]+@[0-9a-f]{40}$`)
	for name, job := range film.Jobs {
		if job.ContinueOnError != nil {
			t.Errorf("film.yml: job %s has continue-on-error", name)
		}
		for i, st := range job.Steps {
			if st.ContinueOnError != nil {
				t.Errorf("film.yml: job %s step %d has continue-on-error", name, i)
			}
			if strings.Contains(st.Run, "|| true") {
				t.Errorf("film.yml: job %s step %d swallows a failure with `|| true`", name, i)
			}
			if st.Uses != "" && !pinned.MatchString(st.Uses) {
				t.Errorf("film.yml: `uses: %s` is not pinned to a 40-hex commit SHA", st.Uses)
			}
		}
	}
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		parts := make([]string, 0, len(x))
		for _, e := range x {
			parts = append(parts, toString(e))
		}
		return strings.Join(parts, " ")
	}
	return ""
}

// The release candidate's binaries (D1192) are a GitHub pre-release, published
// only after the image smoke passed and the image was pushed, and nothing of
// them is stable: a missing --latest=false moves GitHub's "latest release", a
// goreleaser that publishes pushes the Homebrew cask, and a stable secret in
// this workflow is one step from publishing with it.
func TestPreviewPublishesCandidateBinariesAfterTheImage(t *testing.T) {
	raw := readFile(t, repoRoot(t), ".github/workflows/preview.yml")
	var wf struct {
		Permissions map[string]string `yaml:"permissions"`
		Jobs        map[string]struct {
			Needs       any               `yaml:"needs"`
			Permissions map[string]string `yaml:"permissions"`
			Steps       []struct {
				Uses string            `yaml:"uses"`
				Run  string            `yaml:"run"`
				With map[string]any    `yaml:"with"`
				Env  map[string]string `yaml:"env"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(raw), &wf); err != nil {
		t.Fatalf("preview.yml: %v", err)
	}
	if len(wf.Permissions) != 1 || wf.Permissions["contents"] != "read" {
		t.Errorf("preview.yml: top-level permissions %v, want exactly contents: read (write only on the jobs that need it)", wf.Permissions)
	}
	// The job that smokes and pushes the image.
	imageJob := ""
	for name, job := range wf.Jobs {
		for _, st := range job.Steps {
			if strings.Contains(st.Run, "test/smoke/image.sh") {
				imageJob = name
			}
		}
	}
	bin, ok := wf.Jobs["binaries"]
	if !ok || imageJob == "" {
		t.Fatalf("preview.yml: want a `binaries` job and the image job (found image job %q)", imageJob)
	}
	if toString(bin.Needs) != imageJob {
		t.Errorf("preview.yml: binaries needs %v, want exactly %q: nothing of a candidate is published before its image smoke", bin.Needs, imageJob)
	}
	if bin.Permissions["contents"] != "write" {
		t.Errorf("preview.yml: binaries has permissions %v, want contents: write for gh release", bin.Permissions)
	}
	var goreleaser, create string
	for _, st := range bin.Steps {
		if strings.HasPrefix(st.Uses, "goreleaser/goreleaser-action@") {
			goreleaser, _ = st.With["args"].(string)
		}
		if strings.Contains(st.Run, "gh release create") {
			create = st.Run
			if st.Env["GH_TOKEN"] != "${{ github.token }}" {
				t.Errorf("preview.yml: gh release create runs with GH_TOKEN=%q, want github.token: a PAT's tag would start release.yml", st.Env["GH_TOKEN"])
			}
		}
	}
	skip := regexp.MustCompile(`--skip=(\S+)`).FindStringSubmatch(goreleaser)
	if skip == nil || !strings.Contains(","+skip[1]+",", ",publish,") || !strings.Contains(","+skip[1]+",", ",homebrew,") {
		t.Errorf("preview.yml: goreleaser args %q must skip publish and homebrew (the cask renders its tap token before publishing)", goreleaser)
	}
	for _, want := range []string{"--prerelease", "--latest=false"} {
		if !strings.Contains(create, want) {
			t.Errorf("preview.yml: gh release create lacks %s", want)
		}
	}
	for _, banned := range []string{"HOMEBREW_TAP_TOKEN", "RELEASE_PLEASE_TOKEN", "mcp-publisher", "cartographer:latest", "cartographer-surveyor:latest"} {
		if strings.Contains(raw, banned) {
			t.Errorf("preview.yml: references %s, which belongs to a stable release", banned)
		}
	}
}

// A candidate's tag never starts a release (D1192), and the release deletes
// only its own version's candidates.
func TestReleaseWorkflowIgnoresAndCleansUpCandidates(t *testing.T) {
	raw := readFile(t, repoRoot(t), ".github/workflows/release.yml")
	var wf struct {
		On struct {
			Push struct {
				Tags []string `yaml:"tags"`
			} `yaml:"push"`
		} `yaml:"on"`
		Jobs map[string]struct {
			Needs       any               `yaml:"needs"`
			Permissions map[string]string `yaml:"permissions"`
			Steps       []struct {
				Run string            `yaml:"run"`
				Env map[string]string `yaml:"env"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(raw), &wf); err != nil {
		t.Fatalf("release.yml: %v", err)
	}
	// Any hyphen, not only -rc.: preview.yml accepts every vX.Y.Z-<suffix>.
	if strings.Join(wf.On.Push.Tags, " ") != "v* !v*-*" {
		t.Errorf("release.yml: on.push.tags %v, want [v* !v*-*]: a candidate tag pushed by a PAT would publish the cask, latest and the registry entry", wf.On.Push.Tags)
	}
	job, ok := wf.Jobs["candidate-cleanup"]
	if !ok {
		t.Fatal("release.yml: no candidate-cleanup job")
	}
	// After publish, which un-drafts the release last: deleting the candidates closes
	// the freeze (D1384), and it must last until the release is installable.
	if toString(job.Needs) != "publish" {
		t.Errorf("release.yml: candidate-cleanup needs %v, want publish (the job that un-drafts the release)", job.Needs)
	}
	run := ""
	for _, st := range job.Steps {
		run += st.Run
	}
	for _, want := range []string{"gh release delete", "--cleanup-tag", `-rc\\.[0-9]{8}$`, "${version//./\\\\.}"} {
		if !strings.Contains(run, want) {
			t.Errorf("release.yml: candidate-cleanup lacks %q: it deletes the candidates of the shipped version only", want)
		}
	}
}

// The security gate must tell a read that failed from an open alert (#1811) without
// printing what the read returned (the repository is public, D1123). The step's script
// runs for real with a fake gh: its exit and stderr are chosen per read by path.
func TestSecurityGateNamesTheReadThatFailed(t *testing.T) {
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
				ID  string `yaml:"id"`
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(readFile(t, root, ".github/workflows/release-train.yml")), &wf); err != nil {
		t.Fatal(err)
	}
	var run string
	for _, job := range wf.Jobs {
		for _, st := range job.Steps {
			if st.ID == "security" {
				run = st.Run
			}
		}
	}
	if run == "" {
		t.Fatal("release-train.yml: no security step")
	}
	// gh api <path> ...: FAIL_<read> makes read <read> fail with that stderr, OUT_<read> is its result.
	fake := `#!/bin/sh
case "$1" in
api)
  case "$3" in
    *dependabot*) r=1 ;; *code-scanning*) r=2 ;; *security-advisories*) r=3 ;; *pulls*) r=4 ;;
  esac
  eval "fail=\$FAIL_$r out=\$OUT_$r"
  if [ -n "$fail" ]; then echo "$fail" >&2; exit 1; fi
  [ -z "$out" ] || echo "$out"
  ;;
pr) echo "comment" >> "$GH_LOG" ;;
esac
`
	cases := []struct {
		name     string
		env      []string
		clear    bool
		want     string
		forbid   []string
		wantSkip bool
	}{
		{"read 3 fails", []string{"FAIL_3=gh: Not Found (HTTP 404) hunter2"}, false, "security gate: read 3 failed (HTTP 404)", []string{"hunter2"}, true},
		{"fork read fails without a status, numbered as read 3", []string{"OUT_3=owner/fork", "FAIL_4=dial tcp: timeout hunter2"}, false, "security gate: read 3 failed (no status)", []string{"hunter2"}, true},
		{"all reads empty", nil, true, "", []string{"read ", "failed", "token is empty"}, false},
		{"read 1 not empty", []string{"OUT_1=7"}, false, "", []string{"read ", "failed", "alert", "7"}, true},
		{"token empty", []string{"SEC_TOKEN="}, false, "security gate: token is empty", nil, true},
	}
	for _, c := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(fake), 0o755); err != nil {
			t.Fatal(err)
		}
		output := filepath.Join(dir, "output")
		ghLog := filepath.Join(dir, "gh.log")
		cmd := exec.Command(bash, "-e", "-o", "pipefail", "-c", run)
		cmd.Dir = root
		cmd.Env = append(append(cmd.Environ(), "GH_REPO=example/repo", "SEC_TOKEN=tok", "PR=1",
			"GITHUB_OUTPUT="+output, "GH_LOG="+ghLog, "RUNNER_TEMP="+dir,
			"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH")), c.env...)
		var buf strings.Builder
		cmd.Stdout, cmd.Stderr = &buf, &buf
		if err := cmd.Run(); err != nil {
			t.Errorf("%s: the step must exit 0, got %v\n%s", c.name, err, buf.String())
		}
		out := buf.String()
		if c.want != "" && !strings.Contains(out, c.want) {
			t.Errorf("%s: log %q lacks %q", c.name, out, c.want)
		}
		for _, f := range c.forbid {
			if strings.Contains(out, f) {
				t.Errorf("%s: log %q must not contain %q", c.name, out, f)
			}
		}
		got, _ := os.ReadFile(output)
		if cleared := strings.Contains(string(got), "clear=true"); cleared != c.clear {
			t.Errorf("%s: clear=true written: %v, want %v", c.name, cleared, c.clear)
		}
		if _, err := os.Stat(ghLog); (err == nil) != c.wantSkip {
			t.Errorf("%s: skip comment posted: %v, want %v", c.name, err == nil, c.wantSkip)
		}
	}
}

// The Helm chart is published with every release and every release candidate
// (D1441). The traps: a chart pushed before the images it points to; a push whose
// digest is not read back (the attestation would name nothing); a version that is
// not stamped from the tag (the placeholder would ship); a candidate chart that
// claims an attestation nobody verifies (D1022); and a chart pushed without the
// checks that guard its invariants.
func TestChartJobsPublishTheStampedChart(t *testing.T) {
	root := repoRoot(t)
	type step struct {
		Name string         `yaml:"name"`
		Uses string         `yaml:"uses"`
		Run  string         `yaml:"run"`
		With map[string]any `yaml:"with"`
	}
	type job struct {
		Needs       any               `yaml:"needs"`
		Permissions map[string]string `yaml:"permissions"`
		Steps       []step            `yaml:"steps"`
	}
	load := func(file string) (map[string]job, map[string]string) {
		var wf struct {
			Permissions map[string]string `yaml:"permissions"`
			Jobs        map[string]job    `yaml:"jobs"`
		}
		if err := yaml.Unmarshal([]byte(readFile(t, root, ".github/workflows/"+file)), &wf); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		return wf.Jobs, wf.Permissions
	}
	index := func(file string, steps []step, pred func(step) bool) int {
		for i, s := range steps {
			if pred(s) {
				return i
			}
		}
		t.Errorf("%s: chart job lacks a step", file)
		return -1
	}
	runs := func(sub string) func(step) bool { return func(s step) bool { return strings.Contains(s.Run, sub) } }

	for _, file := range []string{"release.yml", "preview.yml"} {
		jobs, _ := load(file)
		chart, ok := jobs["chart"]
		if !ok {
			t.Errorf("%s: no chart job", file)
			continue
		}
		if toString(chart.Needs) != "docker" {
			t.Errorf("%s: chart needs %v, want docker: the chart never exists before the images it points to", file, chart.Needs)
		}
		if chart.Permissions["packages"] != "write" || chart.Permissions["contents"] != "read" {
			t.Errorf("%s: chart permissions %v, want contents: read and packages: write", file, chart.Permissions)
		}
		check := index(file, chart.Steps, runs("make helm-check"))
		pkg := index(file, chart.Steps, runs("helm package"))
		push := index(file, chart.Steps, runs("helm push"))
		if !(check < pkg && pkg < push) {
			t.Errorf("%s: chart steps must run helm-check, then package, then push (got %d, %d, %d)", file, check, pkg, push)
		}
		if pkg >= 0 {
			run := chart.Steps[pkg].Run
			if !strings.Contains(run, `--version "${TAG#v}"`) || !strings.Contains(run, `--app-version "$TAG"`) {
				t.Errorf("%s: the chart is not stamped with both --version and --app-version from the tag", file)
			}
		}
		if push >= 0 {
			run := chart.Steps[push].Run
			if !strings.Contains(run, "oci://ghcr.io/beppetemp/charts") || !strings.Contains(run, "--password-stdin") ||
				!strings.Contains(run, "Digest: ") || !strings.Contains(run, "GITHUB_OUTPUT") {
				t.Errorf("%s: the chart push must log in with --password-stdin, push to oci://ghcr.io/beppetemp/charts and read the digest back", file)
			}
			if strings.Contains(run, ":latest") {
				t.Errorf("%s: the chart is never pushed as latest", file)
			}
		}
		attest := false
		for _, s := range chart.Steps {
			if strings.HasPrefix(s.Uses, "actions/attest-build-provenance@") {
				attest = true
				if s.With["push-to-registry"] != true || s.With["subject-name"] != "ghcr.io/beppetemp/charts/cartographer" ||
					!strings.Contains(toString(s.With["subject-digest"]), "steps.push.outputs.digest") {
					t.Errorf("%s: the chart attestation must name the chart and the digest read back, with push-to-registry: true", file)
				}
			}
		}
		if want := file == "release.yml"; attest != want {
			t.Errorf("%s: chart attestation = %v, want %v (the release chart is attested, the candidate's is not: D1022)", file, attest, want)
		}
	}
	rel, _ := load("release.yml")
	if p := rel["chart"].Permissions; p["id-token"] != "write" || p["attestations"] != "write" {
		t.Errorf("release.yml: chart permissions %v, want id-token and attestations: write", p)
	}
	prev, top := load("preview.yml")
	if len(top) != 1 || top["contents"] != "read" {
		t.Errorf("preview.yml: top-level permissions %v, want exactly contents: read", top)
	}
	if toString(prev["binaries"].Needs) != "docker" {
		t.Errorf("preview.yml: binaries needs %v, want exactly docker: the chart job runs beside it, not before it", prev["binaries"].Needs)
	}

	// Chart.yaml holds the two placeholders the workflows stamp: a hand edit would
	// ship a chart that disagrees with its image tag.
	chartYAML := readFile(t, root, "deploy/helm/cartographer/Chart.yaml")
	for _, want := range []string{"\nversion: 0.0.0-dev\n", "\nappVersion: \"v0.0.0-dev\"\n"} {
		if !strings.Contains(chartYAML, want) {
			t.Errorf("Chart.yaml lacks the placeholder %q", strings.TrimSpace(want))
		}
	}
}
