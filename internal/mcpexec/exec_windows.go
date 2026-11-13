//go:build windows

package mcpexec

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"

	"github.com/BeppeTemp/cartographer/internal/procattr"
)

// Exec runs the descriptor's command as a child that inherits stdin, stdout and
// stderr, in the resolved environment, and returns its exit code. There is no
// exec(2) here, so the launcher stays alive for the child's lifetime and
// forwards an interrupt to it.
func (l Launch) Exec() (int, error) {
	cmd := procattr.Hide(exec.Command(l.Command, l.Args...))
	cmd.Env = l.Env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return 1, err
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	go func() {
		for range sig {
			_ = cmd.Process.Signal(os.Interrupt)
		}
	}()
	err := cmd.Wait()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	return 0, err
}
