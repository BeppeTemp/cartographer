package worker

import (
	"fmt"
	"os"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx/gitxtest"
)

// execHelperFlag makes the test binary act as an operator's exec recipe (no
// interpreter needed, so it runs on every OS): one added entry, then the cursor.
const execHelperFlag = "--exec-helper"

// TestMain: see gitxtest.QuietGit (allow_test.go builds a KB).
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == execHelperFlag {
		fmt.Println(`{"entry":{"key":"a.md","title":"A","revision":"r1","change":"added","locator":"https://wiki.example.com/a"}}`)
		fmt.Println(`{"next_cursor":"c1"}`)
		os.Exit(0)
	}
	gitxtest.QuietGit()
	os.Exit(m.Run())
}
