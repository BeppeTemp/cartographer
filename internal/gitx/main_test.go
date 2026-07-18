package gitx

import (
	"os"
	"testing"
)

// TestMain disables git's auto-gc/maintenance for every git spawned by the
// tests and by the code under test (runGitEnv starts from os.Environ()): a
// rebase/pull can trigger a detached `git gc --auto` that keeps writing into
// .git/objects while t.TempDir() removes the dir → flaky cleanup
// "directory not empty" (seen in CI on golang:1.26-alpine).
func TestMain(m *testing.M) {
	os.Setenv("GIT_CONFIG_COUNT", "3")
	os.Setenv("GIT_CONFIG_KEY_0", "gc.auto")
	os.Setenv("GIT_CONFIG_VALUE_0", "0")
	os.Setenv("GIT_CONFIG_KEY_1", "gc.autoDetach")
	os.Setenv("GIT_CONFIG_VALUE_1", "false")
	os.Setenv("GIT_CONFIG_KEY_2", "maintenance.auto")
	os.Setenv("GIT_CONFIG_VALUE_2", "false")
	os.Exit(m.Run())
}
