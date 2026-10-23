package repodocs

import (
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The release train (D956) fails silently in three ways, and nothing else reads
// them: a cron in UTC without its second daylight-saving entry (the train runs
// an hour off for half the year), a guard on the clock (a delayed schedule is
// dropped or runs twice), and a merge with the default token (the merge succeeds, but no `push`
// workflow runs, so release-please never cuts the tag).
func TestReleaseTrainWorkflowPinsSchedulesAndMergeToken(t *testing.T) {
	root := repoRoot(t)
	raw := readFile(t, root, ".github/workflows/release-train.yml")

	var wf struct {
		On struct {
			Schedule []struct {
				Cron string `yaml:"cron"`
			} `yaml:"schedule"`
		} `yaml:"on"`
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

	// Saturday 00:30 and Monday 08:30 in Europe/Rome, once per UTC offset.
	want := map[string]bool{"30 22 * * 5": true, "30 23 * * 5": true, "30 6 * * 1": true, "30 7 * * 1": true}
	if len(wf.On.Schedule) != len(want) {
		t.Errorf("release-train.yml: %d schedule entries, want the %d of D956", len(wf.On.Schedule), len(want))
	}
	for _, s := range wf.On.Schedule {
		if !want[s.Cron] {
			t.Errorf("release-train.yml: unexpected cron %q, want one of the four daylight-saving pairs of D956", s.Cron)
		}
		delete(want, s.Cron)
	}
	for c := range want {
		t.Errorf("release-train.yml: missing cron %q", c)
	}

	// The guard matches the entry that fired against today's Europe/Rome offset,
	// each cron paired with its offset, and never reads the clock: a late run is
	// dropped by a minute or hour guard, or passes it twice in one slot.
	if !strings.Contains(raw, "${{ github.event.schedule }}") || !strings.Contains(raw, "TZ=Europe/Rome date +%z") {
		t.Error("release-train.yml: the guard must compare github.event.schedule with `TZ=Europe/Rome date +%z`")
	}
	for _, pair := range []string{`"+0200 30 22 * * 5"`, `"+0100 30 23 * * 5"`, `"+0200 30 6 * * 1"`, `"+0100 30 7 * * 1"`} {
		if !strings.Contains(raw, pair) {
			t.Errorf("release-train.yml: the guard lacks the offset/cron pair %s", pair)
		}
	}
	if regexp.MustCompile(`TZ=Europe/Rome date \+[^\n]*%[HM]`).MatchString(raw) {
		t.Error("release-train.yml: a guard on the hour or minute mishandles a delayed scheduled run; match the cron entry instead")
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
			if st.Uses != "" && !regexp.MustCompile(`^[^@\s]+@[0-9a-f]{40}$`).MatchString(st.Uses) {
				t.Errorf("release-train.yml: `uses: %s` is not pinned to a 40-hex commit SHA", st.Uses)
			}
			if !strings.Contains(st.Run, "gh pr merge") {
				continue
			}
			merges++
			// The merge waits for the security gate's output (D<n>): a gate that is
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
