package repodocs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestVetCoversEveryReleaseTarget pins that the train gate compiles what the
// release builds (#1858): main's test-macos job runs a GoReleaser snapshot of
// every target, so a target the `vet` recipe of the Makefile skips reaches main
// red. The rule: every GOOS of .goreleaser.yaml (after its ignore list) has a
// vet leg (linux is the plain `go vet ./...`), and so has every GOARCH that is
// not amd64 or arm64 (the 32-bit ones, where int is narrower); arm64 and amd64
// of one OS share a word size, so one leg per OS covers both.
func TestVetCoversEveryReleaseTarget(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, ".goreleaser.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Builds []struct {
			Goos   []string `yaml:"goos"`
			Goarch []string `yaml:"goarch"`
			Ignore []struct {
				Goos   string `yaml:"goos"`
				Goarch string `yaml:"goarch"`
			} `yaml:"ignore"`
		} `yaml:"builds"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	mk, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	// The legs of the vet recipe: (GOOS, GOARCH) as written, "" for unset.
	type leg struct{ goos, goarch string }
	var legs []leg
	in := false
	for _, l := range strings.Split(string(mk), "\n") {
		if strings.HasPrefix(l, "vet:") {
			in = true
			continue
		}
		if !in {
			continue
		}
		if !strings.HasPrefix(l, "\t") {
			break
		}
		if !strings.Contains(l, "go vet ./...") {
			continue
		}
		var g leg
		if m := regexp.MustCompile(`GOOS=(\w+)`).FindStringSubmatch(l); m != nil {
			g.goos = m[1]
		}
		if m := regexp.MustCompile(`GOARCH=(\w+)`).FindStringSubmatch(l); m != nil {
			g.goarch = m[1]
		}
		legs = append(legs, g)
	}
	if len(legs) == 0 {
		t.Fatal("the vet target of the Makefile has no `go vet ./...` line")
	}
	hasOS := func(goos string) bool {
		for _, g := range legs {
			if g.goos == goos || (g.goos == "" && goos == "linux") {
				return true
			}
		}
		return false
	}
	hasArch := func(goarch string) bool {
		for _, g := range legs {
			if g.goarch == goarch {
				return true
			}
		}
		return false
	}
	if len(cfg.Builds) == 0 {
		t.Fatal(".goreleaser.yaml lists no build")
	}
	for _, b := range cfg.Builds {
		for _, goos := range b.Goos {
			for _, goarch := range b.Goarch {
				ignored := false
				for _, ig := range b.Ignore {
					if ig.Goos == goos && ig.Goarch == goarch {
						ignored = true
					}
				}
				if ignored {
					continue
				}
				if !hasOS(goos) {
					t.Errorf("release target %s/%s: the Makefile vet target has no GOOS=%s leg", goos, goarch, goos)
				}
				if goarch != "amd64" && goarch != "arm64" && !hasArch(goarch) {
					t.Errorf("release target %s/%s: the Makefile vet target has no GOARCH=%s leg", goos, goarch, goarch)
				}
			}
		}
	}
}

// The four vets run in parallel (a serial run was minutes of the Linux leg), and the recipe still
// fails when any of them does: each is started in the background and waited for, its status kept.
func TestVetRunsInParallelAndFailsIfAnyFails(t *testing.T) {
	mk := readFile(t, repoRoot(t), "Makefile")
	i := strings.Index(mk, "\nvet:")
	if i < 0 {
		t.Fatal("no vet target in the Makefile")
	}
	recipe := mk[i+1:]
	if j := strings.Index(recipe, "\n\n"); j >= 0 {
		recipe = recipe[:j]
	}
	if n := strings.Count(recipe, "go vet ./... >"); n < 4 {
		t.Errorf("the vet recipe starts %d background vets, want one per platform (4)", n)
	}
	for _, want := range []string{"wait $$p || {", "rc=1", "vet $$n failed:", "exit $$rc"} {
		if !strings.Contains(recipe, want) {
			t.Errorf("the vet recipe lacks %q: a failing vet must fail the target\n%s", want, recipe)
		}
	}
}
