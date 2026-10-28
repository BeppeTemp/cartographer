package repodocs

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A release is announced by the update check the moment it is published, so it
// must stay a draft until every channel can install it (D1350). Three things
// break that silently: release-please publishing the release itself (no
// "draft"), a draft without its tag (release.yml never starts), and a `publish`
// job that does not wait for the binaries, the pre-release flag and the image.
func TestReleaseIsPublishedLast(t *testing.T) {
	root := repoRoot(t)

	var cfg map[string]any
	if err := json.Unmarshal([]byte(readFile(t, root, "release-please-config.json")), &cfg); err != nil {
		t.Fatalf("release-please-config.json: %v", err)
	}
	for _, k := range []string{"draft", "force-tag-creation"} {
		if v, _ := cfg[k].(bool); !v {
			t.Errorf("release-please-config.json: %q must be true (D1350): release-please would publish the release before its channels exist", k)
		}
	}

	// GoReleaser edits the release it finds with its own draft value (default
	// false) and cannot find a draft by tag unless told to look for one.
	var gr struct {
		Release struct {
			Draft            bool `yaml:"draft"`
			UseExistingDraft bool `yaml:"use_existing_draft"`
		} `yaml:"release"`
	}
	if err := yaml.Unmarshal([]byte(readFile(t, root, ".goreleaser.yaml")), &gr); err != nil {
		t.Fatalf(".goreleaser.yaml: %v", err)
	}
	if !gr.Release.Draft || !gr.Release.UseExistingDraft {
		t.Error(".goreleaser.yaml: release.draft and release.use_existing_draft must be true (D1350): GoReleaser would publish the release, or create a second one")
	}

	var wf struct {
		Jobs map[string]struct {
			Needs yaml.Node `yaml:"needs"`
			Steps []struct {
				Run string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(readFile(t, root, ".github/workflows/release.yml")), &wf); err != nil {
		t.Fatalf("release.yml: %v", err)
	}
	needs := func(job string) []string {
		var out []string
		n := wf.Jobs[job].Needs
		if n.Kind == yaml.ScalarNode {
			return []string{n.Value}
		}
		_ = n.Decode(&out)
		return out
	}

	pub, ok := wf.Jobs["publish"]
	if !ok {
		t.Fatal("release.yml: no publish job (D1350)")
	}
	for _, j := range []string{"goreleaser", "prerelease-flag", "docker"} {
		if !slices.Contains(needs("publish"), j) {
			t.Errorf("release.yml: publish must need %s", j)
		}
	}
	var run string
	for _, s := range pub.Steps {
		run += s.Run
	}
	if !strings.Contains(run, "draft=false") || !strings.Contains(run, ".draft") || !strings.Contains(run, ".prerelease") {
		t.Error("release.yml: publish must un-draft the release and read draft and prerelease back")
	}
	if !slices.Contains(needs("mcp-registry"), "publish") {
		t.Error("release.yml: mcp-registry announces the version and must need publish")
	}
}
