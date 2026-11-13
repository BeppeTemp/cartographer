//go:build windows

package mcpprobe

import (
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ownTree returns the function that kills the child and every process below it.
// Windows has no process group to signal and exec cannot start the child inside
// a job object, so the tree is read from a snapshot and each descendant is
// terminated, deepest first, before the child itself. The function is also
// cmd.Cancel and runs again when Run returns.
func ownTree(cmd *exec.Cmd) (kill func()) {
	kill = func() {
		if cmd.Process != nil {
			killTree(uint32(cmd.Process.Pid))
		}
	}
	cmd.Cancel = func() error { kill(); return nil }
	return kill
}

func killTree(root uint32) {
	children := map[uint32][]uint32{}
	if snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0); err == nil {
		defer windows.CloseHandle(snap)
		var e windows.ProcessEntry32
		e.Size = uint32(unsafe.Sizeof(e))
		for err := windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
			children[e.ParentProcessID] = append(children[e.ParentProcessID], e.ProcessID)
		}
	}
	var walk func(pid uint32, seen map[uint32]bool)
	walk = func(pid uint32, seen map[uint32]bool) {
		if seen[pid] {
			return
		}
		seen[pid] = true
		for _, c := range children[pid] {
			walk(c, seen)
		}
		if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid); err == nil {
			_ = windows.TerminateProcess(h, 1)
			windows.CloseHandle(h)
		}
	}
	walk(root, map[uint32]bool{})
}
