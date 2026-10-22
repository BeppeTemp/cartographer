package repodocs

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The README film is re-rendered only when the hash of film/inputs.sh changes (D1068). An import of
// film/src that leaves that list would let a change to the imported file break the render with no render
// triggered; a listed path that no longer exists makes film-needed.sh fail on every run.
func TestFilmImportsStayInsideTheHashedInputs(t *testing.T) {
	root := repoRoot(t)
	list := regexp.MustCompile(`(?s)FILM_INPUTS=\((.*?)\)`).FindStringSubmatch(readFile(t, root, "film/inputs.sh"))
	if list == nil {
		t.Fatal("film/inputs.sh: no FILM_INPUTS array")
	}
	inputs := strings.Fields(list[1])
	for _, in := range inputs {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(in))); err != nil {
			t.Errorf("film/inputs.sh lists %s, which does not exist: %v", in, err)
		}
	}

	imp := regexp.MustCompile(`(?m)^\s*(?:import|export)\b[^"'\n]*?(?:from\s+)?["'](\.[^"']*)["']`)
	for _, file := range []string{"film/src/main.ts", "film/src/data.ts"} {
		for _, m := range imp.FindAllStringSubmatch(readFile(t, root, file), -1) {
			target := path.Join(path.Dir(file), m[1])
			if !inFilmInputs(inputs, target) {
				t.Errorf("%s imports %s (%s), outside film/inputs.sh: add it there, or a change to it will not re-render the film", file, m[1], target)
			}
		}
	}
}

func TestInFilmInputs(t *testing.T) {
	inputs := []string{"film", "web/src/lib/graph3d", "web/src/lib/palette.ts", "docs/brand/coordinate-pine.svg"}
	for target, want := range map[string]bool{
		"film/src/data":                   true,
		"web/src/lib/graph3d/scene":       true,
		"web/src/lib/palette":             true,
		"docs/brand/coordinate-pine.svg":  true,
		"web/src/lib/graph3d-extra/other": false,
		"web/src/lib/api":                 false,
		"web/src/lib/palette-other":       false,
		"docs/brand/cartographer.css":     false,
		"film-other/main":                 false,
	} {
		if got := inFilmInputs(inputs, target); got != want {
			t.Errorf("inFilmInputs(%q) = %v, want %v", target, got, want)
		}
	}
}

// inFilmInputs reports whether an import target, as written (no extension), is one of the inputs or under one.
func inFilmInputs(inputs []string, target string) bool {
	for _, in := range inputs {
		if target == in || strings.HasPrefix(target, in+"/") || strings.HasPrefix(in, target+".") {
			return true
		}
	}
	return false
}
