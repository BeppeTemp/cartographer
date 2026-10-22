package repodocs

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	filmInputsBlock = regexp.MustCompile(`(?s)FILM_INPUTS=\((.*?)\n\)`)
	// An import specifier: `import "x"`, `import ... from "x"` or `export ... from "x"`.
	tsImport = regexp.MustCompile(`(?m)^\s*(?:import|export)\b[^"'\n;]*?["']([^"']+)["']`)
)

// The film's hash covers an explicit list of paths (film/inputs.sh, D1068). A new
// import in the film that reaches outside the list would let a change to it
// alter the render with no new film: follow every relative import from the film's
// entry points and fail on the first file the list does not cover.
func TestFilmInputsCoverTheFilmsImports(t *testing.T) {
	root := repoRoot(t)
	block := filmInputsBlock.FindStringSubmatch(readFile(t, root, "film/inputs.sh"))
	if block == nil {
		t.Fatal("film/inputs.sh: no FILM_INPUTS=( ... ) list")
	}
	var inputs []string
	for _, line := range strings.Split(block[1], "\n") {
		if p := strings.TrimSpace(line); p != "" && !strings.HasPrefix(p, "#") {
			if _, err := os.Stat(filepath.Join(root, p)); err != nil {
				t.Errorf("film/inputs.sh: input %s does not exist", p)
			}
			inputs = append(inputs, p)
		}
	}
	covered := func(p string) bool {
		for _, in := range inputs {
			if p == in || strings.HasPrefix(p, in+"/") {
				return true
			}
		}
		return false
	}

	seen := map[string]bool{}
	queue := []string{"film/src/main.ts", "film/src/data.ts"}
	for len(queue) > 0 {
		file := queue[0]
		queue = queue[1:]
		if seen[file] {
			continue
		}
		seen[file] = true
		if !covered(file) {
			t.Errorf("%s is imported by the film but is outside film/inputs.sh: a change to it would not re-render the film", file)
			continue
		}
		if !strings.HasSuffix(file, ".ts") {
			continue // css, svg: no imports followed
		}
		for _, m := range tsImport.FindAllStringSubmatch(readFile(t, root, file), -1) {
			spec := m[1]
			if !strings.HasPrefix(spec, ".") {
				continue // a package: web/package-lock.json is in the set
			}
			target := path.Join(path.Dir(file), spec)
			for _, ext := range []string{"", ".ts", ".tsx", "/index.ts"} {
				if fi, err := os.Stat(filepath.Join(root, target+ext)); err == nil && !fi.IsDir() {
					target += ext
					break
				}
			}
			queue = append(queue, target)
		}
	}
	if len(seen) < 5 {
		t.Errorf("only %d files reached from the film's entry points: the import pattern no longer matches", len(seen))
	}
}
