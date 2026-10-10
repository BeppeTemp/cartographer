package mcpserver

import (
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// The Atlas's Health panel, its upkeep half (D323, D338): what the server repaired by itself,
// what the last doctor session left, and the questions the doctor deferred to
// a person. Read-only like the rest of the UI API: the panel shows the exact
// command that undoes a repair, it never runs it.

// maintenanceRepairWindow is how far back the panel lists repair commits.
const maintenanceRepairWindow = 30 * 24 * time.Hour

// maintenanceRepairCap bounds the commits listed, newest first.
const maintenanceRepairCap = 50

type maintenanceAutoRepair struct {
	Enabled      bool     `json:"enabled"`
	Default      bool     `json:"default"`
	Checks       []string `json:"checks"`
	IntervalDays int      `json:"interval_days"`
}

type maintenanceRepair struct {
	SHA     string `json:"sha"`
	At      string `json:"at"`
	Subject string `json:"subject"`
	Reason  string `json:"reason,omitempty"`
	Files   int    `json:"files"`
	// Background is true for a commit the server's heartbeat made, false for
	// a kb_repair someone ran.
	Background bool `json:"background"`
	// Revert is the command that undoes it: shown, never run, by the Atlas.
	Revert string `json:"revert"`
}

type maintenanceDoctorSchedule struct {
	Client  string `json:"client"`
	NextRun string `json:"next_run"`
}

type maintenanceSummary struct {
	AutoRepair     maintenanceAutoRepair `json:"auto_repair"`
	LastAutoRepair *autoRepairRun        `json:"last_auto_repair"`
	LastDoctor     string                `json:"last_doctor,omitempty"`
	NextDoctor     string                `json:"next_doctor,omitempty"`
	DoctorInterval int                   `json:"doctor_interval_days"`
	// DoctorSchedule is the client-declared scheduled headless doctor session
	// (D369), present only while the declaration is not stale.
	DoctorSchedule *maintenanceDoctorSchedule `json:"doctor_schedule,omitempty"`
	// DoctorMode is who runs the doctor sessions (D358): "unattended" or "assisted".
	DoctorMode string `json:"doctor_mode"`
	// Runs are the background repair's runs of the last 30 days, newest first
	// (D365): what each fixed, by check, with the commit that undoes it.
	Runs    []autoRepairRun     `json:"runs"`
	Repairs []maintenanceRepair `json:"repairs"`
}

// GET /kbs/{kb}/maintenance/{summary,questions}.
func uiMaintenance(w http.ResponseWriter, r *http.Request, srv *Server, what string) {
	k := srv.kbRef
	switch what {
	case "questions":
		// The deferred doctor questions: open_question Contradiction concepts,
		// through the same walk as contradiction_report, so what the caller
		// cannot see in the tool it cannot see here.
		entries, err := listContradictions(r.Context(), k, "", "open", "open_question")
		if err != nil {
			writeUIInternal(w, "maintenance questions", err)
			return
		}
		if entries == nil {
			entries = []contradictionEntry{}
		}
		writeUIJSON(w, http.StatusOK, map[string]any{"questions": entries})
	case "summary":
		// It describes the whole KB's history and settings, like status.
		if !WholeVisible(r.Context(), k, false) {
			writeUINotFound(w)
			return
		}
		out := maintenanceSummary{
			AutoRepair: maintenanceAutoRepair{
				Enabled:      len(k.AutoRepair) > 0 && k.DoctorAutoIntervalDays > 0,
				Default:      k.AutoRepairDefault,
				Checks:       append([]string{}, k.AutoRepair...),
				IntervalDays: k.DoctorAutoIntervalDays,
			},
			DoctorInterval: k.DoctorIntervalDays,
			DoctorMode:     "unattended",
			Repairs:        []maintenanceRepair{},
			Runs:           []autoRepairRun{},
		}
		if k.DoctorAssisted() {
			out.DoctorMode = "assisted"
		}
		if run, ok := lastAutoRepairRun(k); ok {
			out.LastAutoRepair = &run
		}
		out.Runs = autoRepairRunsSince(k, srv.now().Add(-maintenanceRepairWindow), maintenanceRepairCap)
		out.LastDoctor = lastDoctorDate(k)
		if srv.conformance != nil {
			out.LastDoctor = srv.conformance.cachedDoctorDate(k)
		}
		if t, err := time.Parse("2006-01-02", out.LastDoctor); err == nil && k.DoctorIntervalDays > 0 {
			out.NextDoctor = t.AddDate(0, 0, k.DoctorIntervalDays).Format("2006-01-02")
		}
		if s := k.LiveDoctorSchedule(srv.now()); s != nil {
			out.DoctorSchedule = &maintenanceDoctorSchedule{Client: s.Client, NextRun: s.NextRun.Format(time.RFC3339)}
		}
		commits, err := gitx.LogNameStatus(k.Root, srv.now().Add(-maintenanceRepairWindow))
		if err != nil {
			writeUIInternal(w, "maintenance repairs", err)
			return
		}
		sort.SliceStable(commits, func(i, j int) bool { return commits[i].At.After(commits[j].At) })
		for _, c := range commits {
			if !isRepairCommit(gitx.CommitSummary{Subject: c.Subject, Reason: c.Reason}) {
				continue
			}
			files := 0
			for _, f := range c.Files {
				// The log entry, the rewritten map contract and the regenerated
				// index ride in the same commit: they are not repaired concepts.
				switch path.Base(f.Path) {
				case "log.md", "_map.md", "index.md":
				default:
					files++
				}
			}
			out.Repairs = append(out.Repairs, maintenanceRepair{
				SHA: c.SHA, At: c.At.UTC().Format(time.RFC3339), Subject: c.Subject, Reason: c.Reason,
				Files: files, Background: strings.HasPrefix(c.Reason, repairCommitReasonPrefix),
				Revert: fmt.Sprintf("cartographer kb repair %s --revert %s", kbName(k), shortSHA(c.SHA)),
			})
			if len(out.Repairs) == maintenanceRepairCap {
				break
			}
		}
		writeUIJSON(w, http.StatusOK, out)
	default:
		writeUINotFound(w)
	}
}
