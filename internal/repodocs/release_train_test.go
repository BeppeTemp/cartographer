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

	merges := 0
	for name, job := range wf.Jobs {
		for _, st := range job.Steps {
			if st.Uses != "" && !regexp.MustCompile(`^[^@\s]+@[0-9a-f]{40}$`).MatchString(st.Uses) {
				t.Errorf("release-train.yml: `uses: %s` is not pinned to a 40-hex commit SHA", st.Uses)
			}
			if !strings.Contains(st.Run, "gh pr merge") {
				continue
			}
			merges++
			if st.Env["GH_TOKEN"] != "${{ secrets.RELEASE_PLEASE_TOKEN }}" {
				t.Errorf("release-train.yml: job %s step %q merges with GH_TOKEN=%q: a merge by github.token triggers no push workflow, release-please would never tag", name, st.Name, st.Env["GH_TOKEN"])
			}
		}
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

// The README film is rendered inside the release candidate (D1068): a red film
// fails the preview run, so release-train.yml never finds a successful candidate
// and does not merge. Nothing in that path may swallow a failure, and film.yml
// holds only the token scope it pushes with.
func TestPreviewWorkflowRunsFilmWithoutSwallowingFailures(t *testing.T) {
	root := repoRoot(t)
	var preview struct {
		Jobs map[string]struct {
			Uses            string            `yaml:"uses"`
			ContinueOnError interface{}       `yaml:"continue-on-error"`
			Permissions     map[string]string `yaml:"permissions"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(readFile(t, root, ".github/workflows/preview.yml")), &preview); err != nil {
		t.Fatalf("preview.yml: %v", err)
	}
	called := false
	for name, job := range preview.Jobs {
		if job.Uses != "./.github/workflows/film.yml" {
			continue
		}
		called = true
		if job.ContinueOnError != nil {
			t.Errorf("preview.yml: job %s has continue-on-error: a film failure must fail the candidate", name)
		}
		if job.Permissions["contents"] != "write" {
			t.Errorf("preview.yml: job %s needs job-level contents: write to push readme-film", name)
		}
	}
	if !called {
		t.Error("preview.yml: no job calls film.yml")
	}

	raw := readFile(t, root, ".github/workflows/film.yml")
	var film struct {
		Permissions map[string]string `yaml:"permissions"`
		Jobs        map[string]struct {
			ContinueOnError interface{} `yaml:"continue-on-error"`
			Steps           []struct {
				Name            string      `yaml:"name"`
				Run             string      `yaml:"run"`
				ContinueOnError interface{} `yaml:"continue-on-error"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(raw), &film); err != nil {
		t.Fatalf("film.yml: %v", err)
	}
	if len(film.Permissions) != 1 || film.Permissions["contents"] != "write" {
		t.Errorf("film.yml: permissions %v, want only contents: write", film.Permissions)
	}
	for name, job := range film.Jobs {
		if job.ContinueOnError != nil {
			t.Errorf("film.yml: job %s has continue-on-error", name)
		}
		for _, st := range job.Steps {
			if st.ContinueOnError != nil || strings.Contains(st.Run, "|| true") {
				t.Errorf("film.yml: job %s step %q swallows a failure (continue-on-error or `|| true`)", name, st.Name)
			}
		}
	}
}
