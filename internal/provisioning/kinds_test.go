package provisioning_test

// Tests for provisioning.Kinds (D864): the kind list the Atlas Artifacts page
// shows even for a KB that has none. The list is a contract with the scanners
// and the docs, so each test pins one side of it.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/repodocs"
)

var kindPlaceholder = regexp.MustCompile(`<[a-z]+>`)

// A KBPath is the layout BuildManifest scans: a file written there, with the
// placeholder replaced by a name, comes back as an artifact of that kind. The
// KB-only kinds are checked against their own scanners in internal/mcpserver
// (TestKinds_KBOnlyPathsAreWhatTheScannersFind).
func TestKinds_PathsAreWhatTheScannerFinds(t *testing.T) {
	const name = "sample"
	for _, k := range provisioning.Kinds() {
		if k.KBOnly {
			continue
		}
		t.Run(k.Kind, func(t *testing.T) {
			kbRoot := t.TempDir()
			path := filepath.Join(kbRoot, filepath.FromSlash(kindPlaceholder.ReplaceAllString(k.KBPath, name)))
			opts := provisioning.BuildOptions{}
			switch k.Kind {
			case "skill":
				writeFile(t, path, "---\nname: "+name+"\ndescription: A skill.\n---\nBody.\n")
			case "agent":
				writeFile(t, path, "---\nname: "+name+"\n---\nBody.\n")
			case "hook":
				writeFile(t, path, `{"event":"PostToolUse","matcher":"concept_write","command":"./run.sh"}`)
				writeFile(t, filepath.Join(filepath.Dir(path), "run.sh"), "#!/bin/sh\necho ok\n")
			case "mcp":
				writeFile(t, path, `{"type":"http","url":"https://tools.example.com/mcp"}`)
			case "instructions":
				// The artifact is generated for every KB, named after it; the
				// file at KBPath is what the generated text carries.
				writeFile(t, path, "Directive written at "+k.KBPath+".\n")
			default:
				t.Fatalf("kind %q is in the matrix and has no fixture here: add one", k.Kind)
			}
			m, err := provisioning.BuildManifest(nil, map[string]string{name: kbRoot}, opts)
			if err != nil {
				t.Fatalf("BuildManifest: %v", err)
			}
			var found []provisioning.Artifact
			for _, a := range m.Artifacts {
				if a.Kind == k.Kind && a.Source == "kb:"+name {
					found = append(found, a)
				}
			}
			if len(found) != 1 || found[0].Name != name {
				t.Fatalf("a file at %s should yield exactly one %s artifact named %q, got %+v", k.KBPath, k.Kind, name, found)
			}
			if k.Kind == "instructions" && !strings.Contains(string(found[0].Files[0].Content), "Directive written at "+k.KBPath) {
				t.Errorf("the file at %s did not reach the generated instructions:\n%s", k.KBPath, found[0].Files[0].Content)
			}
		})
	}
}

// A Doc is "<repository path>#<anchor>", resolved by the UI against the blob
// URL of main: the page must exist and carry a heading GitHub gives that anchor.
func TestKinds_DocsExistAndHaveTheHeading(t *testing.T) {
	root, err := repodocs.Root()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range provisioning.Kinds() {
		file, anchor, ok := strings.Cut(k.Doc, "#")
		if !ok || anchor == "" || strings.HasPrefix(file, "/") || strings.Contains(file, "..") {
			t.Errorf("kind %q: Doc %q is not a repository path with an anchor", k.Kind, k.Doc)
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			t.Errorf("kind %q: %v", k.Kind, err)
			continue
		}
		var slugs []string
		fenced := false
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "```") {
				fenced = !fenced
				continue
			}
			if fenced || !strings.HasPrefix(line, "#") {
				continue
			}
			slugs = append(slugs, githubSlug(strings.TrimLeft(line, "#")))
		}
		found := false
		for _, s := range slugs {
			found = found || s == anchor
		}
		if !found {
			t.Errorf("kind %q: %s has no heading with the anchor #%s (its anchors: %v)", k.Kind, file, anchor, slugs)
		}
	}
}

// githubSlug is the anchor GitHub gives a heading: inline code marks dropped,
// lowercase, spaces to hyphens, any other punctuation but hyphen and
// underscore removed.
func githubSlug(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(strings.ReplaceAll(heading, "`", ""))) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}
