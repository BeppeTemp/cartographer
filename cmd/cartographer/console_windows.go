//go:build windows

package main

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// detachOwnConsole closes the console window Windows created for this process,
// when the process is the only one attached to it (D1479).
//
// cartographer.exe is a console-subsystem binary, and a Scheduled Task with an
// InteractiveToken principal starts it in the user's desktop session: Windows
// then gives it a new, visible console window (on Windows 11 a Windows Terminal
// tab, which looks like a PowerShell). Closing that window sends the process
// CTRL_CLOSE_EVENT and kills it — the service went offline the moment the user
// closed what looked like a stray terminal. FreeConsole detaches the process;
// with no process left attached, the window goes away and nothing can close it.
//
// "Only one attached" is what tells the task's fresh console apart from the
// terminal someone typed the command in: there the shell is attached too, and
// the process keeps its console. A process started with no console at all (an
// MCP client spawning stdio `serve` with CREATE_NO_WINDOW) makes
// GetConsoleProcessList fail, and nothing happens either.
//
// The callers only call it with --log-file set, the shape the task definitions
// use: the output that went to the console already goes to the file.
func detachOwnConsole() {
	if ownsConsoleAlone(consoleProcessCount()) && procFreeConsole.Find() == nil {
		syscall.SyscallN(procFreeConsole.Addr())
	}
}

var (
	kernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	procGetConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
	procFreeConsole           = kernel32.NewProc("FreeConsole")
)

// consoleProcessCount returns how many processes are attached to this
// process's console, 0 when it has none or the call fails.
func consoleProcessCount() uint32 {
	if procGetConsoleProcessList.Find() != nil {
		return 0
	}
	var pids [2]uint32
	// SyscallN, not LazyProc.Call: TestNoUnqualifiedProductionToolCalls reads
	// any `.Call(` in this package as an MCP tool call bypassing callTool.
	n, _, _ := syscall.SyscallN(procGetConsoleProcessList.Addr(), uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	return uint32(n)
}
