package contract

import (
	"os"
	"runtime"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx/gitxtest"
)

func TestMain(m *testing.M) {
	gitxtest.QuietGit() // a detached git gc would break t.TempDir cleanup
	os.Exit(m.Run())
}

// skipWithoutSymlinks skips a test whose subject is a symlink on a platform
// whose link semantics differ (the same guard as the apply package's tests).
func skipWithoutSymlinks(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
}
