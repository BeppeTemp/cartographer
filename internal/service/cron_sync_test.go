//go:build !windows

package service

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCronSchedule(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{
		{time.Minute, "*/1 * * * *"},
		{30 * time.Second, "*/1 * * * *"},
		{30 * time.Minute, "*/30 * * * *"},
		{45 * time.Minute, "*/45 * * * *"},
		{time.Hour, "0 */1 * * *"},
		{90 * time.Minute, "0 */2 * * *"},
		{2 * time.Hour, "0 */2 * * *"},
		{23 * time.Hour, "0 */23 * * *"},
		{48 * time.Hour, "0 0 * * *"},
	} {
		if got := cronSchedule(tc.in); got != tc.want {
			t.Errorf("cronSchedule(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRenderCronSyncBlock(t *testing.T) {
	want := cronSyncBegin + "\n# interval=30m0s\n*/30 * * * * '/opt/bin/cartographer' sync --log-file '/s/sync.log'\n" + cronSyncEnd + "\n"
	if got := renderCronSyncBlock("/opt/bin/cartographer", "/s", 30*time.Minute); got != want {
		t.Errorf("block:\n%s\nwant:\n%s", got, want)
	}
}

func TestCronSyncTimer_InstallStatusUninstall(t *testing.T) {
	e := newCronEnv(t)
	const foreign = "MAILTO=user\n0 3 * * * /usr/bin/backup\n"
	e.ct.text, e.ct.has = foreign, true

	if err := e.m.InstallSyncTimer(45 * time.Minute); err != nil {
		t.Fatalf("InstallSyncTimer: %v", err)
	}
	if !strings.HasPrefix(e.ct.text, foreign) || strings.Count(e.ct.text, "BEGIN cartographer sync") != 1 || !strings.Contains(e.ct.text, "*/45 * * * * ") {
		t.Fatalf("crontab:\n%s", e.ct.text)
	}
	if len(e.ct.other) != 0 {
		t.Errorf("non-crontab commands ran: %v", e.ct.other)
	}
	st, err := e.m.SyncTimerStatus()
	if err != nil || !st.Installed || !st.Active || st.Interval != 45*time.Minute {
		t.Errorf("status = %+v, %v", st, err)
	}

	// The server block is independent: both coexist, each removable alone.
	e.install(t)
	if strings.Count(e.ct.text, "BEGIN cartographer") != 2 {
		t.Fatalf("both blocks expected:\n%s", e.ct.text)
	}
	if err := e.m.UninstallSyncTimer(); err != nil {
		t.Fatalf("UninstallSyncTimer: %v", err)
	}
	if strings.Contains(e.ct.text, "cartographer sync") || !strings.Contains(e.ct.text, "BEGIN cartographer server") {
		t.Errorf("sync removal touched the server block:\n%s", e.ct.text)
	}
	if st, _ := e.m.SyncTimerStatus(); st.Installed || st.Active {
		t.Errorf("after uninstall: %+v", st)
	}
	if err := e.m.UninstallSyncTimer(); err != nil {
		t.Errorf("second uninstall: %v", err)
	}
	if err := e.m.Uninstall(); err != nil || e.ct.text != foreign {
		t.Errorf("server uninstall: %v, crontab %q", err, e.ct.text)
	}
}

func TestCronSyncTimer_NoServerNeeded(t *testing.T) {
	e := newCronEnv(t)
	if err := e.m.InstallSyncTimer(0); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.m.SyncTimerStatus(); st.Interval != DefaultSyncInterval {
		t.Errorf("interval = %v, want the default", st.Interval)
	}
	if p, _ := CronServerDefinitionPath(); fileExists(p) {
		t.Error("a sync timer installed a server definition")
	}
}

func TestCronSyncTimer_UninstallVerifiesRemoval(t *testing.T) {
	e := newCronEnv(t)
	if err := e.m.InstallSyncTimer(time.Hour); err != nil {
		t.Fatal(err)
	}
	def, _ := CronSyncDefinitionPath()
	e.ct.failWrite = true
	if err := e.m.UninstallSyncTimer(); err == nil {
		t.Fatal("uninstall succeeded with the block still in the crontab")
	}
	if !fileExists(def) {
		t.Error("definition deleted although the block is still registered")
	}
	e.ct.text, e.ct.has = "", false
	if err := e.m.UninstallSyncTimer(); err != nil || fileExists(def) {
		t.Errorf("block gone: err=%v, definition present=%v", err, fileExists(def))
	}
}

func TestCronSyncTimer_FailedWriteLeavesNoDefinition(t *testing.T) {
	e := newCronEnv(t)
	e.ct.failWrite = true
	if err := e.m.InstallSyncTimer(time.Hour); err == nil {
		t.Fatal("expected the crontab write to fail")
	}
	if def, _ := CronSyncDefinitionPath(); fileExists(def) {
		t.Error("definition left after a failed install")
	}
}

func TestSyncTimer_NoManagerIsAnError(t *testing.T) {
	e := newCronEnv(t)
	lookPath = func(string) (string, error) { return "", errors.New("absent") }
	err := e.m.InstallSyncTimer(time.Hour)
	if !errors.Is(err, ErrNoServiceManager) {
		t.Errorf("err = %v, want ErrNoServiceManager", err)
	}
	// Nothing installed on such a host is not a failure to uninstall.
	if err := e.m.UninstallSyncTimer(); err != nil {
		t.Errorf("uninstall with nothing installed: %v", err)
	}
}
