package repodocs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A tag is a pointer its owner can move; a commit SHA is not. Every workflow
// here holds a token or publishes something, so an action reference that is not
// a full SHA with its version in a comment is a supply-chain hole (D1020).
// Dependabot keeps the pins current and rewrites the comment with them.
var (
	usesLine    = regexp.MustCompile(`^\s*(?:-\s+)?uses:\s*(\S+)(.*)$`)
	pinnedUses  = regexp.MustCompile(`^[\w.-]+/[\w./-]+@[0-9a-f]{40}$`)
	versionNote = regexp.MustCompile(`^\s*#\s*v\d`)
)

// unpinnedUses returns one message per `uses:` line that is neither a local
// path nor a SHA pin followed by a `# vX` comment.
func unpinnedUses(name, content string) []string {
	var bad []string
	for i, line := range strings.Split(content, "\n") {
		m := usesLine.FindStringSubmatch(line)
		if m == nil || strings.HasPrefix(m[1], "./") {
			continue
		}
		switch {
		case !pinnedUses.MatchString(m[1]):
			bad = append(bad, fmt.Sprintf("%s:%d: %s is not pinned by a full commit SHA", name, i+1, m[1]))
		case !versionNote.MatchString(m[2]):
			bad = append(bad, fmt.Sprintf("%s:%d: %s has no `# vX.Y.Z` comment", name, i+1, m[1]))
		}
	}
	return bad
}

func TestWorkflowActionsArePinnedBySHA(t *testing.T) {
	cases := map[string]struct {
		content string
		want    int
	}{
		"sha with version":  {"      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1", 0},
		"local path":        {"    uses: ./.github/workflows/demo.yml", 0},
		"comment mentions":  {"#   - Every `uses:` is pinned to a full commit SHA", 0},
		"tag":               {"      - uses: actions/checkout@v7", 1},
		"branch":            {"      - uses: actions/checkout@main", 1},
		"short sha":         {"      - uses: actions/checkout@3d3c42e # v7.0.1", 1},
		"sha without note":  {"      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1", 1},
		"sha, wrong note":   {"      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # latest", 1},
		"subpath action":    {"      - uses: github/codeql-action/init@9b3254085494b55a0f7c4c2b3d2d52cc712ce370 # v4.39.1", 0},
		"subpath, tag only": {"      - uses: github/codeql-action/init@v4", 1},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := unpinnedUses("x.yml", c.content); len(got) != c.want {
				t.Errorf("got %v, want %d finding(s)", got, c.want)
			}
		})
	}

	root := repoRoot(t)
	files, err := filepath.Glob(filepath.Join(root, ".github/workflows/*.yml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflows found (%v)", err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, msg := range unpinnedUses(filepath.Base(f), string(raw)) {
			t.Error(msg)
		}
	}
}
