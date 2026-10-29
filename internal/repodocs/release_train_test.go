package repodocs

import (
	"regexp"
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
	smoke, push := -1, -1
	for _, job := range wf.Jobs {
		for i, s := range job.Steps {
			if strings.Contains(s.Run, "test/smoke/image.sh") && smoke < 0 {
				smoke = i
			}
			if p, ok := s.With["push"]; ok && p == true && push < 0 {
				push = i
			}
		}
	}
	if smoke < 0 || push < 0 {
		t.Fatalf("preview.yml: smoke step at %d, push step at %d: both must exist", smoke, push)
	}
	if smoke > push {
		t.Errorf("preview.yml: the smoke (step %d) runs after the push (step %d): a candidate whose container does not start would be published", smoke, push)
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
	for _, banned := range []string{"HOMEBREW_TAP_TOKEN", "RELEASE_PLEASE_TOKEN", "mcp-publisher", "cartographer:latest"} {
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
