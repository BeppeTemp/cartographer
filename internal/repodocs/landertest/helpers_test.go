package landertest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/BeppeTemp/cartographer/internal/repodocs"
)

// The small helpers internal/repodocs keeps in its own tests, which a test package cannot import.

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := repodocs.Root()
	if err != nil {
		t.Fatalf("locating the repository root: %v", err)
	}
	return root
}

func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(data)
}

func readFileAbs(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// needBash skips where the script cannot run: Windows (bash there may be WSL) and an image
// without bash, such as the alpine one the docker job tests in.
func needBash(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("bash script, run by the Unix legs")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
}

func bashOrSkip(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the script runs on ubuntu and macOS only; bash on Windows may be WSL")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not on PATH")
	}
	return bash
}

type ciWorkflow struct {
	Jobs map[string]struct {
		Needs any `yaml:"needs"`
	} `yaml:"jobs"`
}

func loadWorkflow(t *testing.T, name string) (ciWorkflow, string) {
	t.Helper()
	raw := readFile(t, repoRoot(t), ".github/workflows/"+name)
	var wf ciWorkflow
	if err := yaml.Unmarshal([]byte(raw), &wf); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return wf, raw
}

func needsOf(v any) []string {
	switch n := v.(type) {
	case string:
		return []string{n}
	case []any:
		var out []string
		for _, x := range n {
			out = append(out, fmt.Sprint(x))
		}
		return out
	}
	return nil
}
