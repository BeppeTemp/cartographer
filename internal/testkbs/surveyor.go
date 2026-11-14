package testkbs

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/componentproto"
	"github.com/BeppeTemp/cartographer/internal/procattr"
	"github.com/BeppeTemp/cartographer/internal/surveyorconfig"
)

// The files of the synthetic Surveyor in the output directory of
// GenerateFleet's command, next to client-report.json: the three bodies a
// Surveyor posts to the component route family (componentproto), in the order
// it posts them. `make dev` posts them after every server start so the Atlas
// views fed by a Surveyor (Settings, Components, Health, Activity, Work) have
// something to show.
const (
	SurveyorHeartbeatFile = "surveyor-heartbeat.json" // POST componentproto.HeartbeatPath
	SurveyorSettingsFile  = "surveyor-settings.json"  // POST componentproto.SettingsPath
	SurveyorRunsFile      = "surveyor-runs.json"      // POST componentproto.RunsPath
)

// SurveyorPerimeter and SurveyorID name the imaginary Surveyor.
const (
	SurveyorPerimeter = "field-team"
	SurveyorID        = "surveyor-dev-1"
)

// SurveyorAuthor is the git author of the Surveyor's commits in the fleet,
// shaped as a real one (`surveyor/<perimeter>/<replica>`); surveyorCommitOffsets
// are the positions, counted from the newest commit, that the generator gives
// it: two runs of two commits each.
const SurveyorAuthor = "surveyor/" + SurveyorPerimeter + "/" + SurveyorID

var surveyorCommitOffsets = []int{1, 2, 4, 5}

// SurveyorKBs are the KBs of its perimeter: two shaped KBs of the fleet and the
// bare one, so the perimeter does not depend on what a developer mounts beside
// the fleet.
func SurveyorKBs() []string { return []string{"observatory", "mountain-rescue", "test-30"} }

// The bodies below mirror the wire shapes of the heartbeat, settings and runs
// routes; the closed vocabularies (states, outcomes, alarms, candidate kinds,
// task kinds) come from componentproto, the settings from surveyorconfig, and
// TestSurveyorPayloadsAreAcceptedByTheRoutes (internal/mcpserver) posts the
// files to the real handlers, so a drift fails there.

type beatBody struct {
	Protocol        int          `json:"protocol"`
	Surveyor        string       `json:"surveyor"`
	Perimeter       string       `json:"perimeter"`
	Version         string       `json:"version"`
	KBs             []string     `json:"kbs"`
	State           string       `json:"state"`
	IntervalSeconds int          `json:"interval_seconds"`
	LastRun         *beatLastRun `json:"last_run,omitempty"`
	Budgets         []beatBudget `json:"budgets,omitempty"`
	Tasks           []beatTask   `json:"tasks,omitempty"`
}

type beatLastRun struct {
	At          time.Time `json:"at"`
	UnitsDone   int       `json:"units_done"`
	TokensSpent int       `json:"tokens_spent"`
	Commits     int       `json:"commits"`
}

type beatBudget struct {
	Profile string `json:"profile"`
	Tier    string `json:"tier,omitempty"`
	Spent   int    `json:"spent"`
	Limit   int    `json:"limit"`
}

type beatTask struct {
	Kind            string `json:"kind"`
	IntervalSeconds int    `json:"interval_seconds"`
}

type settingsBody struct {
	Component string `json:"component"`
	Protocol  int    `json:"protocol"`
	*surveyorconfig.Report
}

type runsBody struct {
	Protocol   int         `json:"protocol"`
	ID         string      `json:"id"`
	Perimeter  string      `json:"perimeter"`
	Runs       []runsRun   `json:"runs"`
	Alarms     []runsAlarm `json:"alarms"`
	Candidates []runsCand  `json:"report_candidates"`
}

type runsRun struct {
	RunID       string    `json:"run_id"`
	Task        string    `json:"task"`
	KB          string    `json:"kb"`
	StartedAt   time.Time `json:"started_at"`
	DurationMS  int64     `json:"duration_ms"`
	Outcome     string    `json:"outcome"`
	Error       string    `json:"error,omitempty"`
	TokensSpent int       `json:"tokens_spent"`
}

type runsAlarm struct {
	Code   string    `json:"code"`
	KB     string    `json:"kb,omitempty"`
	Source string    `json:"source,omitempty"`
	Since  time.Time `json:"since"`
	Detail string    `json:"detail,omitempty"`
}

type runsCand struct {
	Kind            string `json:"kind"`
	Tool            string `json:"tool"`
	Code            string `json:"code"`
	KB              string `json:"kb"`
	Count           int    `json:"count"`
	FirstAgeSeconds int    `json:"first_age_seconds"`
	LastAgeSeconds  int    `json:"last_age_seconds"`
}

// SurveyorPayloads is what the imaginary Surveyor posts, relative to now.
type SurveyorPayloads struct {
	Heartbeat any
	Settings  any
	Runs      any
}

// Surveyor builds the three bodies. The heartbeat interval is the server's
// largest (an hour), so a declaration stays live three hours without a beat:
// dev.sh repeats the posts well inside that.
func Surveyor(now time.Time) (*SurveyorPayloads, error) { return surveyorWith(now, nil) }

// surveyorWith is Surveyor with the times of the commits the Surveyor made in
// each KB, oldest first: the runs of a KB that has them are anchored on those
// (a run starts shortly before its first commit and ends after its last), so
// the Atlas can join a run to what it changed. Without them the runs are
// relative to now and touch nothing.
func surveyorWith(now time.Time, commits map[string][]time.Time) (*SurveyorPayloads, error) {
	kbs := SurveyorKBs()
	now = now.UTC()
	ago := func(d time.Duration) time.Time { return now.Add(-d) }

	beat := beatBody{
		Protocol: componentproto.Protocol, Surveyor: SurveyorID, Perimeter: SurveyorPerimeter, Version: "dev",
		KBs: kbs, State: "idle", IntervalSeconds: 3600,
		LastRun: &beatLastRun{At: ago(25 * time.Minute), UnitsDone: 6, TokensSpent: 18400, Commits: 3},
		Budgets: []beatBudget{{Profile: "small-model", Tier: "small", Spent: 61200, Limit: 200000}, {Profile: "large-model", Tier: "large", Spent: 9800, Limit: 40000}},
		Tasks: []beatTask{{Kind: "poll", IntervalSeconds: 3600}, {Kind: "ingest", IntervalSeconds: 6 * 3600},
			{Kind: "synthesis", IntervalSeconds: 24 * 3600}, {Kind: "doctor", IntervalSeconds: 7 * 24 * 3600}},
	}
	for _, t := range beat.Tasks {
		if !contains(componentproto.TaskKinds(), t.Kind) {
			return nil, fmt.Errorf("testkbs: surveyor task %q is not a componentproto task kind", t.Kind)
		}
	}

	// The settings entries are built by the Surveyor's own builder from an
	// empty config, so every key is at its default and the schema is the real one.
	rep, err := surveyorconfig.Build(nil, SurveyorPerimeter, SurveyorID, "dev", kbs, func(string) string { return "" })
	if err != nil {
		return nil, fmt.Errorf("testkbs: surveyor settings: %w", err)
	}
	digest := strings.Repeat("0a1b2c3d", 8)
	rep.Sources = []surveyorconfig.Source{
		{Name: "git-docs", Origin: surveyorconfig.OriginCore, Kind: surveyorconfig.KindBuiltin, Shape: "feed",
			Check: surveyorconfig.SourceCheck{Outcome: surveyorconfig.CheckOK, AgeSeconds: 600}},
		{Name: "http-pages", Origin: surveyorconfig.OriginCore, Kind: surveyorconfig.KindBuiltin, Shape: "feed",
			Check: surveyorconfig.SourceCheck{Outcome: surveyorconfig.CheckOK, AgeSeconds: 600}},
		{Name: "field-notes/weather-digest", Origin: surveyorconfig.OriginPlugin, Plugin: "field-notes", Digest: digest,
			Kind: surveyorconfig.KindStarlark, Shape: "feed",
			Check: surveyorconfig.SourceCheck{Outcome: surveyorconfig.CheckOK, AgeSeconds: 1800}},
		{Name: "tide-tables", Origin: surveyorconfig.OriginOperator, Kind: surveyorconfig.KindExec, Shape: "feed",
			Check: surveyorconfig.SourceCheck{Outcome: surveyorconfig.CheckFailed, Code: "check_failed", AgeSeconds: 900}},
		{Name: "ferry-status", Origin: surveyorconfig.OriginOperator, Kind: surveyorconfig.KindExec, Shape: "system",
			Check: surveyorconfig.SourceCheck{Outcome: surveyorconfig.CheckUnsupported}},
		{Name: "survey-archive", Origin: surveyorconfig.OriginOperator, Kind: surveyorconfig.KindMCP,
			Check: surveyorconfig.SourceCheck{Outcome: surveyorconfig.CheckNever}},
	}
	settings := settingsBody{Component: componentproto.KindSurveyor, Protocol: componentproto.Protocol, Report: rep}

	run := func(i int, task, kb, outcome, errText string, tokens int, age time.Duration) runsRun {
		return runsRun{RunID: fmt.Sprintf("dev-run-%03d", i), Task: task, KB: kb, StartedAt: ago(age),
			DurationMS: int64(4000 + 900*i), Outcome: outcome, Error: errText, TokensSpent: tokens}
	}
	// anchored rewrites the start and duration of run i to cover commits
	// [from, to) of kb, when the fleet has them.
	anchored := func(r runsRun, kb string, from, to int) runsRun {
		c := commits[kb]
		if to > len(c) {
			return r
		}
		r.StartedAt = c[from].Add(-30 * time.Second)
		r.DurationMS = c[to-1].Sub(c[from]).Milliseconds() + 60000
		return r
	}
	runs := runsBody{
		Protocol: componentproto.Protocol, ID: SurveyorID, Perimeter: SurveyorPerimeter,
		Runs: []runsRun{
			anchored(run(1, "ingest", kbs[0], componentproto.OutcomeOK, "", 7400, 25*time.Minute), kbs[0], 2, 4),
			anchored(run(2, "synthesis", kbs[0], componentproto.OutcomeOK, "", 5200, 70*time.Minute), kbs[0], 0, 2),
			run(3, "poll", kbs[1], componentproto.OutcomeFailed, "the feed answered 503 three times", 0, 2*time.Hour),
			anchored(run(8, "ingest", kbs[1], componentproto.OutcomeOK, "", 6100, 3*time.Hour), kbs[1], 2, 4),
			anchored(run(9, "poll", kbs[1], componentproto.OutcomeOK, "", 900, 4*time.Hour), kbs[1], 0, 2),
			run(4, "synthesis", kbs[1], componentproto.OutcomeBudgetExhausted, "the daily token budget of the small profile is spent", 11800, 5*time.Hour),
			run(5, "doctor", kbs[2], componentproto.OutcomeSkipped, "", 0, 9*time.Hour),
			run(6, "poll", kbs[2], componentproto.OutcomeOK, "", 1800, 26*time.Hour),
			run(7, "ingest", kbs[1], componentproto.OutcomeFailed, "the model answered with a tool call the server refused", 3300, 30*time.Hour),
		},
		Alarms: []runsAlarm{{Code: componentproto.AlarmFeedFailing, KB: kbs[1], Source: "tide-tables", Since: ago(2 * time.Hour),
			Detail: "the last three checks of the feed failed"}},
		Candidates: []runsCand{
			{Kind: componentproto.CandidateToolError, Tool: "concept_write", Code: "invalid_request", KB: kbs[1], Count: 3, FirstAgeSeconds: 30 * 3600, LastAgeSeconds: 2 * 3600},
			{Kind: componentproto.CandidateContractMismatch, Tool: "source_register", Code: "unexpected_field", KB: kbs[0], Count: 1, FirstAgeSeconds: 4000, LastAgeSeconds: 4000},
		},
	}
	for _, o := range runs.Runs {
		if !contains(componentproto.Outcomes(), o.Outcome) {
			return nil, fmt.Errorf("testkbs: surveyor run outcome %q is not a componentproto outcome", o.Outcome)
		}
	}
	return &SurveyorPayloads{Heartbeat: beat, Settings: settings, Runs: runs}, nil
}

func contains(set []string, s string) bool {
	for _, v := range set {
		if v == s {
			return true
		}
	}
	return false
}

// surveyorCommitTimes reads, from the KB repositories under dir, when the
// Surveyor's commits landed (oldest first). A missing or bare KB has none.
func surveyorCommitTimes(dir string) map[string][]time.Time {
	out := map[string][]time.Time{}
	for _, kb := range SurveyorKBs() {
		cmd := procattr.Hide(exec.Command("git", "-C", filepath.Join(dir, kb), "log", "--author="+SurveyorAuthor, "--format=%ct", "--reverse"))
		b, err := cmd.Output()
		if err != nil {
			continue
		}
		for _, f := range strings.Fields(string(b)) {
			var sec int64
			if _, err := fmt.Sscan(f, &sec); err == nil {
				out[kb] = append(out[kb], time.Unix(sec, 0).UTC())
			}
		}
	}
	return out
}

// WriteSurveyor writes the three bodies as JSON files in dir. The fleet in dir,
// when it is there, says which commits the Surveyor made.
func WriteSurveyor(dir string, now time.Time) error {
	p, err := surveyorWith(now, surveyorCommitTimes(dir))
	if err != nil {
		return err
	}
	for name, v := range map[string]any{SurveyorHeartbeatFile: p.Heartbeat, SurveyorSettingsFile: p.Settings, SurveyorRunsFile: p.Runs} {
		data, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), append(data, '\n'), 0o644); err != nil {
			return err
		}
	}
	return nil
}
