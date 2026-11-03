//go:build windows

package procattr

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestHideSetsNoWindow(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "exit")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x200} // CREATE_NEW_PROCESS_GROUP stays
	if got := Hide(cmd); got != cmd {
		t.Fatal("Hide must return its argument")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Error("HideWindow not set")
	}
	if cmd.SysProcAttr.CreationFlags != 0x200|createNoWindow {
		t.Errorf("CreationFlags = %#x, want the caller's flag plus CREATE_NO_WINDOW", cmd.SysProcAttr.CreationFlags)
	}
	if err := cmd.Run(); err != nil {
		t.Errorf("a hidden child must still run: %v", err)
	}
}
