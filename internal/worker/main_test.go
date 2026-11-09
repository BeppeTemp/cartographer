package worker

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx/gitxtest"
)

// execHelperFlag makes the test binary act as an operator's exec recipe (no
// interpreter needed, so it runs on every OS): one added entry, then the cursor.
const execHelperFlag = "--exec-helper"

// TestMain: see gitxtest.QuietGit (allow_test.go builds a KB).
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == execHelperFlag && os.Args[2] == "system" {
		systemHelper()
	}
	if len(os.Args) > 1 && os.Args[1] == execHelperFlag {
		if len(os.Args) > 2 && os.Args[2] == "content" {
			fmt.Println(`{"entry":{"key":"a.md","title":"A","revision":"r1","change":"added","content":"Inline page text."}}`)
			fmt.Println(`{"next_cursor":"c1"}`)
			os.Exit(0)
		}
		if len(os.Args) > 2 && os.Args[2] == "fail" {
			fmt.Fprintln(os.Stderr, "lister broke")
			os.Exit(3)
		}
		fmt.Println(`{"entry":{"key":"a.md","title":"A","revision":"r1","change":"added","locator":"https://wiki.example.com/a"}}`)
		fmt.Println(`{"next_cursor":"c1"}`)
		os.Exit(0)
	}
	gitxtest.QuietGit()
	os.Exit(m.Run())
}

// systemHelper is a system lister driven by files in $SYS_FIXTURE_DIR: the
// request's mode is appended to modes, preflight.json is the preflight answer
// and listing.jsonl the listing. A missing file is a failure (exit 3).
func systemHelper() {
	dir := os.Getenv("SYS_FIXTURE_DIR")
	b, _ := io.ReadAll(os.Stdin)
	var req struct{ Mode string }
	_ = json.Unmarshal(b, &req)
	if f, err := os.OpenFile(filepath.Join(dir, "modes"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		fmt.Fprintln(f, req.Mode)
		f.Close()
	}
	name := map[string]string{"preflight": "preflight.json", "list": "listing.jsonl"}[req.Mode]
	out, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		fmt.Fprintln(os.Stderr, "no fixture for", req.Mode)
		os.Exit(3)
	}
	os.Stdout.Write(out)
	os.Exit(0)
}
