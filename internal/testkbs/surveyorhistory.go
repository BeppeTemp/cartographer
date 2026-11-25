package testkbs

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/BeppeTemp/cartographer/internal/componentproto"
)

// SurveyorHistoryDir and SurveyorHistoryName are where the generated fleet puts
// the seed of the persistent Surveyor run counters, relative to the output
// directory: the server keeps the real file at <data>/.cartographer/ (a folder
// of the data dir that is never a KB), so `make dev` copies this one there.
const (
	SurveyorHistoryDir  = ".cartographer"
	SurveyorHistoryName = "surveyor-history.json"
	// SurveyorHistorySeedDays is how many days before today the seed covers.
	SurveyorHistorySeedDays = 20
)

// The shape of the file is the server's (internal/mcpserver/surveyorhistory.go,
// format version 1); TestSeededHistoryLoads loads these bytes through the real
// loader, so a change of the format fails there and not in the dev instance.
type historySeedRow struct {
	KB         string `json:"kb"`
	Perimeter  string `json:"perimeter"`
	Task       string `json:"task"`
	Outcome    string `json:"outcome"`
	Runs       int64  `json:"runs"`
	Tokens     int64  `json:"tokens"`
	DurationMS int64  `json:"duration_ms"`
}

type historySeedFile struct {
	Version int                         `json:"version"`
	Days    map[string][]historySeedRow `json:"days"`
	Seen    map[string]struct{}         `json:"seen"`
}

// SurveyorHistoryFile returns the bytes of a version-1 history file with rows
// for each KB of SurveyorKBs, one set per day for the 20 days before today
// (today's counts come from the runs `make dev` posts), a mix of tasks and
// outcomes, and an empty seen-set.
func SurveyorHistoryFile(now time.Time) ([]byte, error) {
	tasks := componentproto.TaskKinds()
	outcomes := []string{componentproto.OutcomeOK, componentproto.OutcomeFailed, componentproto.OutcomeSkipped, componentproto.OutcomeBudgetExhausted}
	f := historySeedFile{Version: 1, Days: map[string][]historySeedRow{}, Seen: map[string]struct{}{}}
	today := now.UTC().Truncate(24 * time.Hour)
	for d := 1; d <= SurveyorHistorySeedDays; d++ {
		day := today.AddDate(0, 0, -d).Format("2006-01-02")
		for ki, kb := range SurveyorKBs() {
			// Two or three distinct (task, outcome) pairs per KB and day; most
			// are ok, a failure or a skip now and then.
			for j := 0; j < 2+(d+ki)%2; j++ {
				task := tasks[(d+ki+j)%len(tasks)]
				outcome := outcomes[0]
				if j == 1 {
					outcome = outcomes[(d+ki)%len(outcomes)]
				}
				runs := int64(1 + (d*3+ki+j)%5)
				var tokens int64
				if outcome != componentproto.OutcomeSkipped {
					tokens = runs * int64(900+((d+ki)%7)*350)
				}
				f.Days[day] = append(f.Days[day], historySeedRow{KB: kb, Perimeter: SurveyorPerimeter, Task: task,
					Outcome: outcome, Runs: runs, Tokens: tokens, DurationMS: runs * int64(3000+(d%4)*1500)})
			}
		}
	}
	return json.Marshal(f)
}

// WriteSurveyorHistory writes the seed to <dir>/.cartographer/surveyor-history.json
// (mode 0600), only when the file is absent: an existing one is never touched.
func WriteSurveyorHistory(dir string, now time.Time) error {
	b, err := SurveyorHistoryFile(now)
	if err != nil {
		return err
	}
	d := filepath.Join(dir, SurveyorHistoryDir)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	// O_EXCL: a file already there, whatever it is, stays as it is.
	f, err := os.OpenFile(filepath.Join(d, SurveyorHistoryName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
