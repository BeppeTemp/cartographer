package service

// The sync timer on cron (D1682): the second managed block of the crontab,
// independent of the server block because `connect` installs the timer on
// machines that may run no server at all.

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BeppeTemp/cartographer/internal/blocktext"
)

const (
	cronSyncBegin = "# BEGIN cartographer sync — managed by cartographer, do not edit"
	cronSyncEnd   = "# END cartographer sync"
)

// cronSchedule turns an interval into a five-field schedule. Cron has no
// "every 45 minutes after the last run": the interval is rounded up to whole
// minutes (minimum 1); under an hour it is */n (fires at :00, :n, ... of each
// hour), from an hour up it is rounded up to whole hours, capped at daily.
func cronSchedule(interval time.Duration) string {
	mins := int((interval + time.Minute - 1) / time.Minute)
	if mins < 1 {
		mins = 1
	}
	if mins < 60 {
		return fmt.Sprintf("*/%d * * * *", mins)
	}
	hours := (mins + 59) / 60
	if hours >= 24 {
		return "0 0 * * *"
	}
	return fmt.Sprintf("0 */%d * * *", hours)
}

func renderCronSyncBlock(bin, stateDir string, interval time.Duration) string {
	cmd := fmt.Sprintf("%s sync --log-file %s", shQuote(bin), shQuote(filepath.Join(stateDir, "sync.log")))
	// No @reboot line: a missed run is made up by the next slot.
	return cronSyncBegin + "\n# interval=" + interval.String() + "\n" + cronSchedule(interval) + " " + cmd + "\n" + cronSyncEnd + "\n"
}

func intervalFromCronDefinition(data []byte) time.Duration {
	d, err := time.ParseDuration(cronDefinitionField(data, "interval"))
	if err != nil || d < 0 {
		return 0
	}
	return d
}

// syncBackend picks the backend of the sync timer: its own definition decides
// when present, else the server's rule. The error is ErrNoServiceManager where
// neither exists.
func (m *Manager) syncBackend() (Backend, error) {
	if p, err := CronSyncDefinitionPath(); err == nil && fileExists(p) {
		return BackendCron, nil
	}
	if p, err := SyncSystemdTimerPath(); err == nil && fileExists(p) {
		return BackendSystemd, nil
	}
	if b := m.Backend(); b != BackendNone {
		return b, nil
	}
	return BackendNone, ErrNoServiceManager
}

func (m *Manager) installSyncCron(binPath string, interval time.Duration) error {
	if err := cronGuard(); err != nil {
		return err
	}
	stateDir, err := StateDir()
	if err != nil {
		return fmt.Errorf("service: resolve state dir: %w", err)
	}
	for what, p := range map[string]string{"binary": binPath, "state": stateDir} {
		if err := checkCronPath(what, p); err != nil {
			return err
		}
	}
	defPath, err := CronSyncDefinitionPath()
	if err != nil {
		return fmt.Errorf("service: resolve cron definition path: %w", err)
	}
	for _, d := range []string{stateDir, filepath.Dir(defPath)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("service: create %s: %w", d, err)
		}
	}
	block := renderCronSyncBlock(binPath, stateDir, interval)
	prev, prevErr := os.ReadFile(defPath)
	if err := os.WriteFile(defPath, []byte("# bin="+binPath+"\n"+block), 0o644); err != nil {
		return fmt.Errorf("service: write sync cron definition: %w", err)
	}
	if err := m.editCrontab(cronSyncBegin, cronSyncEnd, block); err != nil {
		if prevErr == nil {
			os.WriteFile(defPath, prev, 0o644)
		} else {
			os.Remove(defPath)
		}
		return err
	}
	// A unit pair of ours would fire next to the cron entry.
	if timer, err := SyncSystemdTimerPath(); err == nil && fileExists(timer) {
		if _, err := lookPath("systemctl"); err == nil {
			m.run("systemctl", "--user", "disable", "--now", syncSystemdTimer)
		}
		os.Remove(timer)
		if svc, err := SyncSystemdServicePath(); err == nil {
			os.Remove(svc)
		}
	}
	return nil
}

// uninstallSyncCron follows the verified-removal rule: the definition goes only
// once the block is really out of the crontab (#1584).
func (m *Manager) uninstallSyncCron() error {
	if err := cronGuard(); err != nil {
		return err
	}
	defPath, err := CronSyncDefinitionPath()
	if err != nil {
		return fmt.Errorf("service: resolve cron definition path: %w", err)
	}
	if !fileExists(defPath) {
		return nil
	}
	if err := m.removeCronBlock(cronSyncBegin, cronSyncEnd); err != nil {
		return err
	}
	if err := os.Remove(defPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("service: remove sync cron definition: %w", err)
	}
	return nil
}

func (m *Manager) syncTimerStatusCron() (SyncTimerStatus, error) {
	var st SyncTimerStatus
	defPath, err := CronSyncDefinitionPath()
	if err != nil {
		return st, fmt.Errorf("service: resolve cron definition path: %w", err)
	}
	st.Path = defPath
	if data, err := os.ReadFile(defPath); err == nil {
		st.Installed = true
		st.Interval = intervalFromCronDefinition(data)
	}
	// An unreadable crontab leaves Active false, never an error; a sandboxed
	// home must not read the real one.
	if !homeSandboxed() {
		if cur, err := m.readCrontab(); err == nil {
			_, o := blocktext.ReplaceBlock(cur, cronSyncBegin, cronSyncEnd, "")
			st.Active = o == blocktext.Found
		}
	}
	return st, nil
}
