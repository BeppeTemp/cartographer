package kb

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// The scheduled doctor declaration (D369): a client that installed a daily
// headless kb-doctor session tells the server when the next one is due, so the
// Atlas can say "next doctor session: tomorrow 06:00" instead of "starts when an
// agent next connects". It lives in <root>/.cartographer/doctor-schedule.json,
// local-only like every file there: the schedule belongs to one operator's
// machine, so it is neither committed nor replicated between servers.

const doctorScheduleFilename = "doctor-schedule.json"

// doctorScheduleStale is how long past its next run a declaration is believed:
// a client re-declares after every successful run, so a declaration this far
// behind means the job stopped (machine gone, client failing, timer removed
// without telling the server).
const doctorScheduleStale = 24 * time.Hour

var doctorScheduleMu sync.Mutex

// DoctorSchedule is the declaration. The JSON tags are the wire format of
// POST /api/doctor-schedule.
type DoctorSchedule struct {
	// Client is the agent client the job runs, e.g. "claude".
	Client string `json:"client"`
	// NextRun is when the next session is due.
	NextRun time.Time `json:"next_run"`
	// DeclaredAt is when the server received the declaration.
	DeclaredAt time.Time `json:"declared_at"`
}

func (k *KB) doctorSchedulePath() string {
	return filepath.Join(k.cartographerDirPath(), doctorScheduleFilename)
}

// LoadDoctorSchedule reads the declaration. A KB nobody scheduled has none: nil.
func (k *KB) LoadDoctorSchedule() (*DoctorSchedule, error) {
	data, err := os.ReadFile(k.doctorSchedulePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("LoadDoctorSchedule: %w", err)
	}
	var s DoctorSchedule
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("LoadDoctorSchedule: unmarshal: %w", err)
	}
	return &s, nil
}

// LiveDoctorSchedule is the declaration if it is still believable at now, nil
// otherwise (none declared, unreadable, or more than a day past its next run).
func (k *KB) LiveDoctorSchedule(now time.Time) *DoctorSchedule {
	s, err := k.LoadDoctorSchedule()
	if err != nil || s == nil || now.After(s.NextRun.Add(doctorScheduleStale)) {
		return nil
	}
	return s
}

// DeclareDoctorSchedule stores the declaration, replacing any previous one: a
// KB has at most one schedule, the latest client to declare wins.
func (k *KB) DeclareDoctorSchedule(s DoctorSchedule) error {
	doctorScheduleMu.Lock()
	defer doctorScheduleMu.Unlock()
	if err := k.ensureCartographerDir(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("DeclareDoctorSchedule: marshal: %w", err)
	}
	return writeFileAtomic(k.doctorSchedulePath(), data)
}

// ClearDoctorSchedule removes the declaration; clearing none is a success.
func (k *KB) ClearDoctorSchedule() error {
	doctorScheduleMu.Lock()
	defer doctorScheduleMu.Unlock()
	if err := os.Remove(k.doctorSchedulePath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("ClearDoctorSchedule: %w", err)
	}
	return nil
}
