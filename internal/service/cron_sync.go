package service

// The sync timer on cron (D1682): the second managed block of the crontab,
// independent of the server block because `connect` installs the timer on
// machines that may run no server at all.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	// Cron runs a job with its own default PATH (/usr/bin:/bin): the entry
	// carries the PATH of the units (syncServicePATH) as a command prefix.
	cmd := fmt.Sprintf("PATH=%s %s sync --log-file %s", shQuote(syncServicePATH(bin)), shQuote(bin), shQuote(filepath.Join(stateDir, "sync.log")))
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

// syncBackend picks the backend an install of the sync timer uses: the same
// fresh probe as the server's Install, so a host that gained or lost systemd
// migrates the timer too. The definitions on disk decide only status and
// uninstall. ErrNoServiceManager where neither backend exists.
func (m *Manager) syncBackend() (Backend, error) {
	b := m.Backend()
	if goos == "linux" {
		b = m.DetectBackend()
	}
	if b == BackendNone {
		return BackendNone, ErrNoServiceManager
	}
	return b, nil
}

// removeSyncCronLeftover removes our sync cron block and definition before a
// systemd install, so the two never both fire. A sandboxed home touches no
// scheduler; a failure is a note, the install goes on.
func (m *Manager) removeSyncCronLeftover() {
	if p, err := CronSyncDefinitionPath(); err != nil || !fileExists(p) || homeSandboxed() {
		return
	}
	if err := m.uninstallSyncCron(); err != nil {
		fmt.Fprintf(os.Stderr, "note: the old cron sync entry could not be removed (%v); remove it with `crontab -e`\n", err)
	}
}

// Markers of the units RenderSyncSystemdService/Timer write: a unit file that
// lacks its marker is somebody else's and is never disabled or deleted.
const (
	syncServiceMarker = "Description=Cartographer client sync\n"
	syncTimerMarker   = "Description=Cartographer client sync timer\n"
)

func fileHolds(path, marker string) bool {
	data, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(data), marker)
}

// removeSyncSystemdLeftover disables and removes our systemd sync pair before
// a cron install. A pair that is not ours is left alone, with a note.
func (m *Manager) removeSyncSystemdLeftover() {
	timer, err1 := SyncSystemdTimerPath()
	svc, err2 := SyncSystemdServicePath()
	if err1 != nil || err2 != nil || (!fileExists(timer) && !fileExists(svc)) {
		return
	}
	if (fileExists(timer) && !fileHolds(timer, syncTimerMarker)) || (fileExists(svc) && !fileHolds(svc, syncServiceMarker)) {
		fmt.Fprintf(os.Stderr, "note: %s and %s are not ours (no Cartographer header): left in place; remove them yourself if they should not run next to the cron entry\n", filepath.Base(timer), filepath.Base(svc))
		return
	}
	if _, err := lookPath("systemctl"); err == nil {
		m.run("systemctl", "--user", "disable", "--now", syncSystemdTimer)
	}
	removeSafe(timer)
	removeSafe(svc)
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
		if err := mkdirSafe(d); err != nil {
			return fmt.Errorf("service: create %s: %w", d, err)
		}
	}
	block := renderCronSyncBlock(binPath, stateDir, interval)
	prev, prevErr := os.ReadFile(defPath)
	if err := writeSafe(defPath, []byte("# bin="+binPath+"\n"+block)); err != nil {
		return fmt.Errorf("service: write sync cron definition: %w", err)
	}
	if err := m.editCrontab(cronSyncBegin, cronSyncEnd, block); err != nil {
		if prevErr == nil {
			writeSafe(defPath, prev)
		} else {
			removeSafe(defPath)
		}
		return err
	}
	m.removeSyncSystemdLeftover()
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
	if err := removeSafe(defPath); err != nil && !os.IsNotExist(err) {
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
