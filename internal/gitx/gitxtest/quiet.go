package gitxtest

import "os"

// QuietGit switches off git's automatic gc and maintenance for every git the
// test binary spawns, the code under test included (gitx starts each command
// from os.Environ()). A commit or rebase can otherwise start a detached
// `git gc --auto` that keeps writing into .git while t.TempDir() removes it,
// and the cleanup fails with "directory not empty". Call it first in TestMain
// of every package whose tests create a git repository: a repodocs test
// fails when one does not.
func QuietGit() {
	os.Setenv("GIT_CONFIG_COUNT", "3")
	os.Setenv("GIT_CONFIG_KEY_0", "gc.auto")
	os.Setenv("GIT_CONFIG_VALUE_0", "0")
	os.Setenv("GIT_CONFIG_KEY_1", "gc.autoDetach")
	os.Setenv("GIT_CONFIG_VALUE_1", "false")
	os.Setenv("GIT_CONFIG_KEY_2", "maintenance.auto")
	os.Setenv("GIT_CONFIG_VALUE_2", "false")
}
