package testkbs

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
)

var day = time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)

// A generated KB opens, has a history, and plants the findings the Health
// view is judged on: if a lint change stops one of them firing, the test KBs
// no longer show that state and this test says which. It runs on a KB of the
// scale path and on a profiled one with conflicts.
func TestGeneratedKBShowsEveryPlantedState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Run("scale path", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "test-300")
		if err := Generate(root, Spec{Name: "test-300", Concepts: 300, Seed: 7, Conflicts: true}, day); err != nil {
			t.Fatal(err)
		}
		plantedStates(t, root, []string{"concept_batch:", "concept_patch:", "concept_move:", "concept_delete:", "supersede:", "auto-repair (background):"})
	})
	t.Run("profiled with conflicts", func(t *testing.T) {
		root := generated(t, "ferry-line")
		plantedStates(t, root, []string{"concept_batch:", "concept_patch:", "concept_move:", "concept_delete:", "auto-repair (background):"})
		// The profile's commit count and date span (WP3).
		p := profiles["ferry-line"]
		commits, first, recent := history(t, root)
		if n := len(commits); n < p.Commits*9/10 || n > p.Commits*11/10 {
			t.Errorf("%d commits, the profile has %d", n, p.Commits)
		}
		if d := day.Sub(first).Hours() / 24; d < float64(p.SpanDays)-1 || d > float64(p.SpanDays)+1 {
			t.Errorf("the history spans %.1f days, the profile %d", d, p.SpanDays)
		}
		if recent < p.Last30*95/100 || recent > p.Last30*105/100 {
			t.Errorf("%d commits in the last 30 days, the profile has %d", recent, p.Last30)
		}
	})
}

func plantedStates(t *testing.T, root string, subjects []string) {
	t.Helper()
	k, err := kb.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", root, "log", "--format=%s").Output()
	if err != nil {
		t.Fatal(err)
	}
	log := string(out)
	for _, subject := range subjects {
		if !strings.Contains(log, subject) {
			t.Errorf("no %q commit in the history", subject)
		}
	}
	findings, err := lint.Run(k, "", false)
	if err != nil {
		t.Fatal(err)
	}
	fired := map[string]bool{}
	for _, f := range findings {
		fired[f.Check] = true
	}
	// An island is an observation (D947): kb_review lists it, lint does not.
	items, err := lint.Review(k, findings)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Kind == lint.ReviewObservation {
			fired[it.Check] = true
		}
	}
	for _, check := range []string{"broken_link", "island", "invalid_field_value", "missing_required_field", "forbidden_term",
		"closed_with_open_items", "contract_malformed", "unmapped_folder", "hook_invalid", "link_to_retired"} {
		if !fired[check] {
			t.Errorf("planted %s does not fire", check)
		}
	}
	for _, name := range []string{"lint-history.jsonl", "search-misses.jsonl", "usage.json", "conflicts.json"} {
		if _, err := os.Stat(filepath.Join(root, ".cartographer", name)); err != nil {
			t.Errorf("local state %s: %v", name, err)
		}
	}
}

// history reads the commit subjects, the first commit's time and the number of
// commits in the 30 days before the test day.
func history(t *testing.T, root string) (subjects []string, first time.Time, recent int) {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "log", "--format=%at %s").Output()
	if err != nil {
		t.Fatal(err)
	}
	cut := day.AddDate(0, 0, -30).Unix()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		at, subject, _ := strings.Cut(line, " ")
		sec, err := strconv.ParseInt(at, 10, 64)
		if err != nil {
			t.Fatalf("git log line %q", line)
		}
		subjects = append(subjects, subject)
		if tm := time.Unix(sec, 0).UTC(); first.IsZero() || tm.Before(first) {
			first = tm
		}
		if sec >= cut {
			recent++
		}
	}
	return subjects, first, recent
}

// The bare KB has no git and no artifacts: the empty states.
func TestBareKBHasNoHistoryNorArtifacts(t *testing.T) {
	root := filepath.Join(t.TempDir(), "test-30")
	if err := Generate(root, Spec{Name: "test-30", Concepts: 30, Seed: 1, Bare: true}, day); err != nil {
		t.Fatal(err)
	}
	if _, err := kb.Open(root); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{".git", "skills", "services", "paths.yaml"} {
		if _, err := os.Stat(filepath.Join(root, p)); err == nil {
			t.Errorf("%s exists in the bare KB", p)
		}
	}
}

// The fleet is eleven KBs: the scale KB of 5,000 concepts (#861), the nine
// profiled ones and the bare one (D1032).
func TestFleetScale(t *testing.T) {
	f := Fleet()
	if len(f) != 11 || f[0].Concepts != 5000 || f[0].Profile != nil {
		t.Fatalf("fleet = %+v", f)
	}
	if last := f[len(f)-1]; !last.Bare || last.Concepts != 30 {
		t.Errorf("the last KB is not the bare one: %+v", last)
	}
	seen := map[string]bool{}
	conflicts := 0
	for _, s := range f[1 : len(f)-1] {
		if s.Profile == nil || s.Theme != s.Name || themes[s.Name] == nil || seen[s.Name] {
			t.Errorf("profiled spec %+v", s)
		}
		seen[s.Name] = true
		if s.Concepts != s.Profile.Concepts() {
			t.Errorf("%s: %d concepts, the profile sums to %d", s.Name, s.Concepts, s.Profile.Concepts())
		}
		if s.Conflicts {
			conflicts++
		}
	}
	if conflicts != 1 || !f[2].Conflicts || f[2].Name != "ferry-line" {
		t.Errorf("conflicts are planted on ferry-line only: %d", conflicts)
	}
}

// The client report is what a real client sends with `sync`: an entry for every
// key of the client schema, the file the client wrote, providers bound to KBs of
// the fleet, and no secret in clear (D818).
func TestClientReport(t *testing.T) {
	r, err := ClientReport()
	if err != nil {
		t.Fatal(err)
	}
	if r.Machine != "dev-laptop" || r.Version != "dev" {
		t.Errorf("machine %q, version %q", r.Machine, r.Version)
	}
	got := map[string]clientconfig.ReportEntry{}
	for _, e := range r.Entries {
		got[e.Key] = e
	}
	for _, k := range clientconfig.Schema.Keys {
		if _, ok := got[k.Path]; !ok {
			t.Errorf("no entry for the client key %s", k.Path)
		}
	}
	if strings.TrimSpace(r.YAML) == "" || got["server_name"].Origin != "file" {
		t.Errorf("the report is not built from a written file: origin %q, yaml %d bytes", got["server_name"].Origin, len(r.YAML))
	}
	if r.ServerURL == "" {
		t.Error("no server_url")
	}

	fleet := map[string]bool{}
	for _, s := range Fleet() {
		fleet[s.Name] = true
	}
	bound := 0
	for _, p := range r.Providers {
		for _, w := range p.Workspaces {
			for _, name := range w.KBs {
				if !fleet[name] {
					t.Errorf("workspace %s of %s is bound to %q, not a KB of the fleet", w.Path, p.Name, name)
				}
				bound++
			}
			if !strings.HasPrefix(w.Path, "$HOME/") {
				t.Errorf("workspace path %q is not under $HOME", w.Path)
			}
		}
	}
	if bound == 0 {
		t.Error("no provider has a workspace bound to a KB")
	}
	var resolved, unresolved, ignored int
	for _, ph := range r.Placeholders {
		switch {
		case ph.Ignored:
			ignored++
		case ph.Path != "":
			resolved++
		default:
			unresolved++
		}
	}
	if resolved == 0 || unresolved == 0 || ignored == 0 {
		t.Errorf("placeholders resolved %d, unresolved %d, ignored %d: every state is wanted", resolved, unresolved, ignored)
	}

	// No secret in clear: a secret key shows only (set)/(unset), its line in the
	// file only the redaction; the dev server takes no token, so none is set.
	for _, k := range clientconfig.Schema.Keys {
		if !k.Secret {
			continue
		}
		if v := got[k.Path].Value; v != clientconfig.SecretSet && v != clientconfig.SecretUnset {
			t.Errorf("secret key %s shows %v", k.Path, v)
		}
		leaf := k.Path[strings.LastIndex(k.Path, ".")+1:]
		for _, m := range regexp.MustCompile(`(?m)^\s*`+regexp.QuoteMeta(leaf)+`:[ \t]*(\S.*)$`).FindAllStringSubmatch(r.YAML, -1) {
			if m[1] != clientconfig.SecretRedacted {
				t.Errorf("the file shows %s: %s", leaf, m[1])
			}
		}
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"BEGIN ", "Bearer ", "ghp_", "password"} {
		if strings.Contains(string(data), s) {
			t.Errorf("the report contains %q", s)
		}
	}
}

// WriteClientReport leaves the report as JSON next to the KBs, readable back.
func TestWriteClientReport(t *testing.T) {
	dir := t.TempDir()
	if err := WriteClientReport(dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ClientReportFile))
	if err != nil {
		t.Fatal(err)
	}
	var r clientconfig.SettingsReport
	if err := json.Unmarshal(data, &r); err != nil || r.Machine != "dev-laptop" || len(r.Entries) == 0 {
		t.Errorf("unreadable report: %v, machine %q, %d entries", err, r.Machine, len(r.Entries))
	}
}
