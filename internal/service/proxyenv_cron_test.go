//go:build !windows

package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCronServerReadsTheEnvFileNotTheCronEnvironment(t *testing.T) {
	e := newCronEnv(t)
	environ = func() []string { return []string{"HTTPS_PROXY=http://user:pw@proxy.example.com"} }
	e.install(t)
	args := strings.Join(e.procs.starts[0], " ")
	envPath := filepath.Join(filepath.Dir(e.cfg), "service.env")
	if strings.Contains(args, "env-file") || strings.Contains(args, "pw@") {
		t.Errorf("args %s", args)
	}
	if b, err := os.ReadFile(envPath); err != nil || !strings.Contains(string(b), "HTTPS_PROXY=http://user:pw@proxy.example.com") {
		t.Errorf("env file: %q, %v", b, err)
	}
}
