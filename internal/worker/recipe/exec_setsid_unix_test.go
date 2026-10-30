//go:build !windows

package recipe

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func init() {
	// A descendant in a new session: outside the process group the worker kills,
	// and it keeps the script's stdout open.
	platformHelpers["setsid"] = func(args []string) {
		c := exec.Command(os.Args[0], execHelperFlag, "sleep")
		c.Stdout, c.SysProcAttr = os.Stdout, &syscall.SysProcAttr{Setsid: true}
		_ = c.Start()
		_ = os.WriteFile(args[0], []byte(strconv.Itoa(c.Process.Pid)), 0o600)
		time.Sleep(time.Minute)
	}
}

func TestExecDescendantHoldingStdoutOpenDoesNotBlockNorKeepTheSlot(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	r := helperWith(t, func(c *ExecConfig) { c.Timeout = time.Second }, "setsid", pidFile)
	start := time.Now()
	listErr(t, r, CodeListFailed, "timeout")
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("took %s: the read was not interrupted", d)
	}
	select { // the slot is free again
	case execSlot <- struct{}{}:
		<-execSlot
	default:
		t.Error("execSlot still held")
	}
	b, _ := os.ReadFile(pidFile)
	if pid, _ := strconv.Atoi(string(b)); pid > 0 {
		_ = syscall.Kill(pid, syscall.SIGKILL) // it left the group on purpose
	}
}
