//go:build !windows

package procattr

import "os/exec"

func hide(*exec.Cmd) {}
