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
		Casks []struct {
			SkipUpload bool `yaml:"skip_upload"`
		} `yaml:"homebrew_casks"`
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

	// The cask is pushed to the tap by a job after `publish`, not by GoReleaser
	// while the release is a draft (its URL would answer 404).
	if len(gr.Casks) == 0 {
		t.Error(".goreleaser.yaml: no homebrew_casks entry")
	}
	for _, c := range gr.Casks {
		if !c.SkipUpload {
			t.Error(".goreleaser.yaml: homebrew_casks must set skip_upload: true (D1350): the cask would reach the tap while the release is a draft")
		}
	}

	var wf struct {
		Jobs map[string]struct {
			Needs yaml.Node `yaml:"needs"`
			Steps []struct {
				Run  string            `yaml:"run"`
				Name string            `yaml:"name"`
				Env  map[string]string `yaml:"env"`
				With struct {
					Tags      string `yaml:"tags"`
					Name      string `yaml:"name"`
					Overwrite bool   `yaml:"overwrite"`
				} `yaml:"with"`
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
	// A draft lost its tag name between GoReleaser and publish (#1447): both jobs that
	// write the draft find it by its name too, and put the tag name back.
	workflow := readFile(t, root, ".github/workflows/release.yml")
	if n := strings.Count(workflow, `.draft and .name == env.TAG`); n != 2 {
		t.Errorf("release.yml: %d lookups fall back on the draft's name, want 2 (prerelease-flag and publish)", n)
	}
	if n := strings.Count(workflow, `-f tag_name="$TAG"`); n != 2 {
		t.Errorf("release.yml: %d writes set the tag name, want 2 (prerelease-flag and publish)", n)
	}
	// The cask on the tap and :latest are announced channels too (D1350 amended).
	for _, j := range []string{"cask", "latest"} {
		if _, ok := wf.Jobs[j]; !ok {
			t.Errorf("release.yml: no %s job", j)
		} else if !slices.Contains(needs(j), "publish") {
			t.Errorf("release.yml: %s must need publish", j)
		}
	}
	// A re-run of the whole workflow uploads the cask artifact again: without
	// overwrite the second upload fails with a 409.
	for _, s := range wf.Jobs["goreleaser"].Steps {
		if s.With.Name == "cask" && !s.With.Overwrite {
			t.Error("release.yml: the cask upload-artifact step needs overwrite: true, a re-run would fail with a 409")
		}
	}
	for _, s := range wf.Jobs["docker"].Steps {
		if strings.Contains(s.With.Tags, ":latest") {
			t.Error("release.yml: the docker job must not push :latest, the latest job retags it after publish")
		}
	}
	if !slices.Contains(needs("mcp-registry"), "publish") {
		t.Error("release.yml: mcp-registry announces the version and must need publish")
	}

	// The published notes are the CHANGELOG.md section of the tag (#1819), set in the
	// PATCH that un-drafts; the step that runs the tag's code holds no secret.
	for _, s := range pub.Steps {
		if strings.Contains(s.Run, "go run") {
			if !strings.Contains(s.Run, "-section") {
				t.Error("release.yml: the publish step that runs go run must extract the notes with gen -section")
			}
			for k, v := range s.Env {
				if strings.Contains(v, "secrets.") || strings.Contains(k, "TOKEN") {
					t.Errorf("release.yml: the step running go run has %s in its environment: it runs the tag's code (D1020)", k)
				}
			}
		}
		if strings.Contains(s.Run, "draft=false") {
			if !strings.Contains(s.Run, "-F body=@") || strings.Contains(s.Run, "go run") {
				t.Error("release.yml: publish must set the body (-F body=@file) in the PATCH that sets draft=false, in a step that runs gh only")
			}
		}
	}
}

// The docker job publishes two images, each attested on its own digest (D1440).
// Attesting the Surveyor with the server's digest (a copy-paste of the step) is
// the trap: the signature would vouch for the wrong image.
func TestReleaseDockerJobPublishesAndAttestsBothImages(t *testing.T) {
	var wf struct {
		Jobs map[string]struct {
			Steps []struct {
				ID   string         `yaml:"id"`
				Uses string         `yaml:"uses"`
				Run  string         `yaml:"run"`
				With map[string]any `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(readFile(t, repoRoot(t), ".github/workflows/release.yml")), &wf); err != nil {
		t.Fatalf("release.yml: %v", err)
	}
	str := func(v any) string { s, _ := v.(string); return s }
	pushID := map[string]string{} // image -> id of the step that pushed it
	attested := map[string]string{}
	for _, s := range wf.Jobs["docker"].Steps {
		switch {
		case strings.HasPrefix(s.Uses, "docker/build-push-action@"):
			if s.With["push"] != true {
				continue
			}
			if !strings.HasPrefix(str(s.With["provenance"]), "mode=max") || s.With["sbom"] != true {
				t.Errorf("release.yml docker: push step %q lacks provenance: mode=max or sbom: true", s.ID)
			}
			tags := str(s.With["tags"])
			image, target := "ghcr.io/beppetemp/cartographer", str(s.With["target"])
			switch target {
			case "":
			case "surveyor":
				image += "-surveyor"
			default:
				t.Errorf("release.yml docker: unexpected target %q", target)
			}
			// :latest is not pushed here: the latest job retags it after publish.
			if tag := image + ":${{ github.ref_name }}"; !strings.Contains(tags, tag+"\n") && !strings.HasSuffix(strings.TrimSpace(tags), tag) {
				t.Errorf("release.yml docker: step %q (target %q) lacks tag %s", s.ID, target, tag)
			}
			for _, line := range strings.Split(strings.TrimSpace(tags), "\n") {
				line = strings.TrimSpace(line)
				if !strings.HasPrefix(line, image+":") {
					t.Errorf("release.yml docker: step %q (target %q) pushes %s, outside %s", s.ID, target, line, image)
				}
			}
			if s.ID == "" {
				t.Errorf("release.yml docker: push step for %s has no id to attest by", image)
			}
			pushID[image] = s.ID
		case strings.HasPrefix(s.Uses, "actions/attest-build-provenance@"):
			attested[str(s.With["subject-name"])] = str(s.With["subject-digest"])
		}
	}
	for _, image := range []string{"ghcr.io/beppetemp/cartographer", "ghcr.io/beppetemp/cartographer-surveyor"} {
		id, ok := pushID[image]
		if !ok {
			t.Errorf("release.yml docker: no push step for %s", image)
			continue
		}
		want := "${{ steps." + id + ".outputs.digest }}"
		if got := attested[image]; got != want {
			t.Errorf("release.yml docker: %s is attested with digest %q, want %q (the step that pushed it)", image, got, want)
		}
	}
	if len(pushID) != 2 || len(attested) != 2 {
		t.Errorf("release.yml docker: %d push steps and %d attestations, want 2 and 2", len(pushID), len(attested))
	}
	var retag string
	for _, s := range wf.Jobs["latest"].Steps {
		retag += s.Run
	}
	for _, image := range []string{"ghcr.io/beppetemp/cartographer", "ghcr.io/beppetemp/cartographer-surveyor"} {
		if !strings.Contains(retag, "-t "+image+":latest ") {
			t.Errorf("release.yml latest: %s:latest is not retagged after publish", image)
		}
	}
}
