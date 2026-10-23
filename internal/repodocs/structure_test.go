package repodocs

// Cross-lists that must agree and are kept by hand (D1124): the page index and
// the site nav, the `make` targets and repository paths the text cites, against
// what exists. The `cartographer <command>` half lives in cmd/cartographer, next
// to the table it compares.
//
// As in sectionrefs_test.go, the corpus includes this file: examples of a
// citation are built from pieces or written with a placeholder.

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// repoPathAllow lists backtick-quoted repository paths that are not meant to
// exist. Key: "<file>: <path>"; value: the reason, mandatory. Checked both ways
// like sectionRefAllow. Starts empty.
var repoPathAllow = map[string]string{}

func TestDocumentationStructure(t *testing.T) {
	root := repoRoot(t)

	t.Run("pages", func(t *testing.T) { checkPages(t, root) })
	t.Run("make-targets", func(t *testing.T) { checkMakeTargets(t, root) })
	t.Run("paths", func(t *testing.T) { checkRepoPaths(t, root) })
}

var indexLink = regexp.MustCompile(`\]\(([^)\s#]+)(?:#[^)\s]*)?\)`)

// navPages returns every markdown page the mkdocs.yml nav lists, relative to docs/.
func navPages(t *testing.T, root string) map[string]bool {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(readFile(t, root, "mkdocs.yml")), &doc); err != nil {
		t.Fatalf("parsing mkdocs.yml: %v", err)
	}
	out := map[string]bool{}
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Kind == yaml.ScalarNode {
			if strings.HasSuffix(n.Value, ".md") && !strings.Contains(n.Value, "://") {
				out[n.Value] = true
			}
			return
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		t.Fatal("mkdocs.yml is not a mapping")
	}
	top := doc.Content[0].Content
	for i := 0; i+1 < len(top); i += 2 {
		if top[i].Value == "nav" {
			walk(top[i+1])
		}
	}
	if len(out) == 0 {
		t.Fatal("no page found in the mkdocs.yml nav: the check is not doing anything")
	}
	return out
}

func checkPages(t *testing.T, root string) {
	var pages []string
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(filepath.Join(root, "docs"), p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "decisions" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(rel, ".md") && rel != "index.md" {
			pages = append(pages, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking docs/: %v", err)
	}
	sort.Strings(pages)
	if len(pages) == 0 {
		t.Fatal("no page found under docs/: the check is not doing anything")
	}

	linked := map[string]bool{}
	for _, m := range indexLink.FindAllStringSubmatch(readFile(t, root, "docs/index.md"), -1) {
		linked[path.Clean(m[1])] = true
	}
	nav := navPages(t, root)

	for _, p := range pages {
		if !linked[p] {
			t.Errorf("docs/%s is not linked from docs/index.md: add its row to the table that groups it (mkdocs.yml mirrors that grouping)", p)
		}
		if !nav[p] {
			t.Errorf("docs/%s is not in the mkdocs.yml nav: add it under the group docs/index.md puts it in (the site build only reports this as INFO)", p)
		}
	}
	var listed []string
	for p := range nav {
		listed = append(listed, p)
	}
	sort.Strings(listed)
	for _, p := range listed {
		if _, err := os.Stat(filepath.Join(root, "docs", filepath.FromSlash(p))); err != nil {
			t.Errorf("the mkdocs.yml nav lists %s, which does not exist under docs/", p)
		}
	}
}

var (
	codeSpan    = regexp.MustCompile("`([^`\n]+)`")
	makeInSpan  = regexp.MustCompile(`(?:^|[\s;&(])make\s+([^;&|]*)`)
	makeInFence = regexp.MustCompile(`^\s*(?:\$ )?(?:cd \S+ && )?make\s+(.*)$`)
	makeTarget  = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

// citedMakeTarget reads the arguments after `make` and returns the target: the
// first word that is not an option (-C consumes its argument) nor NAME=value.
func citedMakeTarget(args string) (string, bool) {
	words := strings.Fields(args)
	for i := 0; i < len(words); i++ {
		w := words[i]
		switch {
		case w == "-C":
			i++
		case strings.HasPrefix(w, "-"), strings.Contains(w, "="):
		default:
			w = strings.TrimRight(w, ".,:)")
			return w, makeTarget.MatchString(w)
		}
	}
	return "", false
}

// isFenceLine reports whether a markdown line opens or closes a fenced block.
func isFenceLine(line string) bool {
	return fenceOpen.MatchString(line)
}

func checkMakeTargets(t *testing.T, root string) {
	targets, _ := makefileTargets(readFile(t, root, "Makefile"))
	known := map[string]bool{}
	for _, n := range targets {
		known[n] = true
	}
	sort.Strings(targets)

	files, err := TextFiles(root)
	if err != nil {
		t.Fatalf("collecting the corpus: %v", err)
	}
	cited := 0
	report := func(rel string, line int, args string) {
		name, ok := citedMakeTarget(args)
		if !ok {
			return
		}
		cited++
		if !known[name] {
			t.Errorf("%s:%d runs \"make %s\" but the Makefile has no target %s. Targets: %s. Fix the text, or add the target.",
				rel, line, name, name, strings.Join(targets, " "))
		}
	}
	for _, rel := range files {
		if strings.HasPrefix(rel, "docs/decisions/") { // history cites targets since removed
			continue
		}
		isMD := path.Ext(rel) == ".md"
		fenced := false
		for i, line := range strings.Split(readFile(t, root, rel), "\n") {
			if isMD && isFenceLine(line) {
				fenced = !fenced
				continue
			}
			if fenced {
				if m := makeInFence.FindStringSubmatch(line); m != nil && !strings.ContainsAny(m[1], "<>") && !strings.Contains(m[1], "...") {
					report(rel, i+1, m[1])
				}
				continue
			}
			// Outside markdown only comments are read: a backtick in code is a
			// raw string or a command substitution, not a quotation.
			if !isMD {
				trim := strings.TrimSpace(line)
				if !strings.HasPrefix(trim, "//") && !strings.HasPrefix(trim, "#") {
					continue
				}
			}
			for _, sp := range codeSpan.FindAllStringSubmatch(line, -1) {
				if strings.ContainsAny(sp[1], "<>$") || strings.Contains(sp[1], "...") {
					continue
				}
				if m := makeInSpan.FindStringSubmatch(sp[1]); m != nil {
					report(rel, i+1, m[1])
				}
			}
		}
	}
	if cited == 0 {
		t.Fatal("no make target cited anywhere: the check is not doing anything")
	}
}

var (
	repoPathPrefix = regexp.MustCompile(`^(?:internal|cmd|scripts|deploy|test)/`)
	pathExt        = regexp.MustCompile(`\.[a-z0-9]{1,5}$`)
	lineSuffix     = regexp.MustCompile(`(?::\d+(?:-\d+)?|#L\d+(?:-L?\d+)?)$`)
)

// citedRepoPath normalises one backtick span to a path whose existence is
// required, or "" when the span is not a path reference.
func citedRepoPath(span string) string {
	if !repoPathPrefix.MatchString(span) || strings.ContainsAny(span, "*<>{}$… ") || strings.Contains(span, "...") {
		return ""
	}
	p := lineSuffix.ReplaceAllString(span, "")
	p = strings.TrimRight(p, "/.,")
	if p == "" {
		return ""
	}
	// `internal/gitx.runGitEnv`: a last segment with no file extension is
	// <dir>.<Symbol>; only the directory is required.
	if base := path.Base(p); !pathExt.MatchString(base) {
		if i := strings.LastIndexByte(p, '.'); i > strings.LastIndexByte(p, '/') {
			p = p[:i]
		}
	}
	return p
}

func checkRepoPaths(t *testing.T, root string) {
	files, err := MarkdownFiles(root)
	if err != nil {
		t.Fatalf("collecting the markdown files: %v", err)
	}
	cited := 0
	unresolved := map[string]bool{}
	for _, rel := range files {
		if strings.HasPrefix(rel, "docs/decisions/") {
			continue
		}
		for i, line := range strings.Split(readFile(t, root, rel), "\n") {
			for _, sp := range codeSpan.FindAllStringSubmatch(line, -1) {
				p := citedRepoPath(sp[1])
				if p == "" {
					continue
				}
				cited++
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err == nil {
					continue
				}
				key := rel + ": " + p
				unresolved[key] = true
				if _, ok := repoPathAllow[key]; ok {
					continue
				}
				t.Errorf("%s:%d cites %q which does not exist. If the file moved, point at where it went; "+
					"if it is not meant to exist, add %q to repoPathAllow with the reason.", rel, i+1, p, key)
			}
		}
	}
	if cited == 0 {
		t.Fatal("no repository path cited anywhere: the check is not doing anything")
	}
	var keys []string
	for k := range repoPathAllow {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch {
		case strings.TrimSpace(repoPathAllow[k]) == "":
			t.Errorf("repoPathAllow entry %q has no reason: say why the path is not meant to exist", k)
		case !unresolved[k]:
			t.Errorf("repoPathAllow entry %q no longer applies (the path exists or is no longer cited): "+
				"remove the repoPathAllow entry", k)
		}
	}
}

func TestCitedRepoPath(t *testing.T) {
	cases := map[string]string{
		"internal/gitx/gitx.go:120":            "internal/gitx/gitx.go",
		"internal/mcpserver/":                  "internal/mcpserver",
		"internal/gitx.runGitEnv":              "internal/gitx",
		"internal/changelog.CanonicalScopes()": "internal/changelog",
		"internal/kb/*.go":                     "",
		"cmd/<name>/main.go":                   "",
		".claude/skills/x/SKILL.md":            "",
		"scripts/ci-filters.sh#L12":            "scripts/ci-filters.sh",
		"internal/foo bar":                     "",
	}
	for in, want := range cases {
		if got := citedRepoPath(in); got != want {
			t.Errorf("citedRepoPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCitedMakeTarget(t *testing.T) {
	cases := []struct {
		args, want string
		ok         bool
	}{
		{"gate", "gate", true},
		{"-C dir gate", "gate", true},
		{"SLUG=x worktree-add", "worktree-add", true},
		{"decisions-new N=1 SLUG=x", "decisions-new", true},
		{"... target", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := citedMakeTarget(c.args)
		if got != c.want || ok != c.ok {
			t.Errorf("citedMakeTarget(%q) = %q, %v; want %q, %v", c.args, got, ok, c.want, c.ok)
		}
	}
}
