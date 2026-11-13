//go:build windows

package mcpexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/BeppeTemp/cartographer/internal/procattr"
)

// Exec runs the descriptor's command as a child that inherits stdin, stdout and
// stderr, in the resolved environment, and returns its exit code. There is no
// exec(2) here, so the launcher stays alive for the child's lifetime. An
// interrupt kills the child (Windows has no way to send one to a process), and
// the child is bound to a job object that kills it when the launcher dies, so
// it is never orphaned holding the secrets.
func (l Launch) Exec() (int, error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return l.run(ctx)
}

func (l Launch) run(ctx context.Context) (int, error) {
	cmd := procattr.Hide(exec.Command(l.Command, l.Args...))
	cmd.Env = l.Env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	job, err := newKillOnCloseJob()
	if err != nil {
		return 1, err
	}
	defer windows.CloseHandle(job)
	if err := cmd.Start(); err != nil {
		return 1, err
	}
	if err := assignToJob(job, cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return 1, err
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
		case <-done:
		}
	}()
	err = cmd.Wait()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	return 0, err
}

func newKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func assignToJob(job windows.Handle, pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.AssignProcessToJobObject(job, h)
}
