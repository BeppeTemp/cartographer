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
