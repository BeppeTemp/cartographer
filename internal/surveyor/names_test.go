package surveyor

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Surveyor was named "worker" in its packages, routes and wire fields until
// D-new-surveyor-rename. This test fails when the old word creeps back into the
// places that carry the Surveyor, so the next one is caught at the first commit.
// "network" is stripped before the search ("networkError" is not a hit).
func TestNoWorkerNameLeft(t *testing.T) {
	root := filepath.Join("..", "..")
	// Files whose hits are about something else, each with the reason.
	allowed := map[string]string{
		"internal/surveyor/names_test.go":            "this test names the word it forbids",
		"web/src/api/static.ts":                      "the demo's Cloudflare Worker, not the Surveyor",
		"internal/surveyor/budget_test.go":           "TestBudgetIgnoresTheOldWorkerPrefix pins that the pre-rename ledger prefix is not read (D1442)",
		"internal/mcpserver/uiapi_surveyors_test.go": "TestSurveyors_OldRoutesAreGone pins that the old routes answer 404",
	}
	var files []string
	walk := func(dir string) {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				files = append(files, p)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{"internal/surveyor", "internal/surveyorconfig", "web/src/api"} {
		walk(d)
	}
	for _, g := range []string{"cmd/cartographer/surveyor*.go", "internal/mcpserver/uiapi_surveyor*.go", "web/src/components/Surveyors.tsx"} {
		m, err := filepath.Glob(filepath.Join(root, g))
		if err != nil || len(m) == 0 {
			t.Fatalf("%s matches nothing (%v): the guard would pass on nothing", g, err)
		}
		files = append(files, m...)
	}
	for _, f := range files {
		rel := filepath.ToSlash(strings.TrimPrefix(f, root+string(filepath.Separator)))
		if _, ok := allowed[rel]; ok {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			low := strings.ReplaceAll(strings.ToLower(line), "network", "")
			if strings.Contains(low, "worker") {
				t.Errorf("%s:%d names the worker: rename it, or add the file to the allow-list with the reason", rel, i+1)
			}
		}
	}
}
