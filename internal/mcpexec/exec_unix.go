//go:build !windows

package mcpexec

import (
	"os/exec"
	"syscall"
)

// Exec replaces this process with the descriptor's command, in the resolved
// environment: the launcher is gone, so a secret is in the child's environment
// only. A bare command is looked up on the PATH the launcher inherited, the
// semantics the client would have given it. It returns only on failure.
func (l Launch) Exec() (int, error) {
	path, err := exec.LookPath(l.Command)
	if err != nil {
		return 1, err
	}
	err = syscall.Exec(path, append([]string{l.Command}, l.Args...), l.Env)
	return 1, err
}
