package repodocs

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/blocktext"
)

var update = flag.Bool("update", false, "rewrite the generated decision index instead of checking it")

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := Root()
	if err != nil {
		t.Fatalf("locating the repository root: %v", err)
	}
	return root
}

// The index is generated. This test is both the check and, with -update, the
// generator: `make decisions-index` is exactly this test with that flag, so the
// tool that writes the file and the tool that verifies it can never disagree.
func TestDecisionIndexIsUpToDate(t *testing.T) {
	root := repoRoot(t)

	decisions, err := LoadDecisions(root)
	if err != nil {
		t.Fatalf("loading the decisions: %v", err)
	}
	if unknown := UnknownTopics(decisions); len(unknown) > 0 {
		t.Fatalf("topics used by a decision but missing from TopicOrder: %v\n"+
			"add them to TopicOrder (with the reading position you want) — they are not dropped silently",
			unknown)
	}

	path := filepath.Join(root, "docs", "decisions.md")
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	body := RenderIndex(decisions)
	want := IndexBegin + "\n" + strings.TrimRight(body, "\n") + "\n" + IndexEnd + "\n"

	if *update {
		next, ok := blocktext.ReplaceBetween(string(current), IndexBegin, IndexEnd, want)
		if !ok {
			t.Fatalf("%s does not contain the generated block markers:\n  %s\n  %s",
				path, IndexBegin, IndexEnd)
		}
		if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
		t.Logf("regenerated the index in %s with %d records", path, len(decisions))
		return
	}

	if !strings.Contains(string(current), want) {
		t.Errorf("the decision index in docs/decisions.md is stale.\n"+
			"Run `make decisions-index` and commit the result.\n"+
			"(%d decision files on disk)", len(decisions))
	}
}

func TestNoDecisionNumberIsUsedTwice(t *testing.T) {
	decisions, err := LoadDecisions(repoRoot(t))
	if err != nil {
		t.Fatalf("loading the decisions: %v", err)
	}
	seen := map[int]string{}
	for _, d := range decisions {
		if d.Num == 0 {
			continue // the historical AD block
		}
		if prev, dup := seen[d.Num]; dup {
			t.Errorf("D%d is claimed by two files: %s and %s", d.Num, prev, d.File)
			continue
		}
		seen[d.Num] = d.File
	}
}

// The budgets no client reports on. See the constants for what enforces each.
func TestContextFileBudgets(t *testing.T) {
	root := repoRoot(t)

	data, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatalf("reading AGENTS.md: %v", err)
	}
	text := string(data)

	if lines := strings.Count(text, "\n") + 1; lines > MaxRootLines {
		t.Errorf("AGENTS.md is %d lines, over the %d-line budget: move a section to the "+
			"page that owns it and leave one line of pointer", lines, MaxRootLines)
	}
	if chars := len([]rune(text)); chars > MaxRootChars {
		t.Errorf("AGENTS.md is %d characters, over the %d Antigravity allows per rules file",
			chars, MaxRootChars)
	}

	chains, err := ChainBytes(root)
	if err != nil {
		t.Fatalf("measuring the AGENTS.md chains: %v", err)
	}
	if len(chains) == 0 {
		t.Fatal("no AGENTS.md found: the chain measurement is not doing anything")
	}
	for dir, size := range chains {
		if size > MaxChainBytes {
			t.Errorf("the Codex instruction chain for %s is %d bytes, over %d: launched there, "+
				"Codex would silently drop the deepest files — the guidance closest to the code",
				dir, size, MaxChainBytes)
		}
	}
}

// One skill, one copy. Every other location is a symlink into .agents/skills,
// because the way this degrades is that someone replaces a link with a copy and
// two clients start reading different procedures without any warning.
//
// The check is on the **git index**, not on the working tree, and that is the
// point: a Windows checkout without Developer Mode gets core.symlinks=false and
// materialises every link as a text file containing its target, so a working-tree
// check would either fail for that contributor or have to skip and prove nothing.
// The index records mode 120000 regardless of the platform that checked it out,
// so this runs identically everywhere. Verified empirically (Kiro CLI 2.21.4, 45
// runs with negative controls): a client does follow the symlink, and given the
// materialised-text-file variant it loads nothing at all, silently — which is
// exactly why the mode has to be asserted rather than assumed.
func TestSkillBridgesAreSymlinksInTheIndex(t *testing.T) {
	root := repoRoot(t)

	skills, err := os.ReadDir(filepath.Join(root, ".agents", "skills"))
	if err != nil {
		t.Fatalf("reading .agents/skills: %v", err)
	}
	var names []string
	for _, s := range skills {
		if s.IsDir() {
			names = append(names, s.Name())
		}
	}
	if len(names) == 0 {
		t.Fatal(".agents/skills is empty: the parity check is not doing anything")
	}

	for _, client := range SupportedClients {
		out, err := gitIndex(root, client.SkillDir)
		if err != nil {
			t.Fatalf("git ls-files %s: %v", client.SkillDir, err)
		}

		for _, name := range names {
			want := client.SkillDir + "/" + name
			mode, tracked := out[want]
			switch {
			case !tracked:
				t.Errorf("%s is not in the git index, so %s will not see the %q skill:\n"+
					"  ln -s ../../.agents/skills/%s %s && git add %s",
					want, client.Name, name, name, want, want)
			case mode != "120000":
				t.Errorf("%s is recorded in the git index with mode %s instead of 120000 "+
					"(symlink): it is a copy, and it will drift from .agents/skills/%s.\n"+
					"  git rm -r --cached %s && rm -rf %s && ln -s ../../.agents/skills/%s %s && git add %s",
					want, mode, name, want, want, name, want, want)
			}
		}
	}
}

// gitIndex maps every path tracked under dir to its index mode.
func gitIndex(root, dir string) (map[string]string, error) {
	cmd := exec.Command("git", "ls-files", "-s", "--", dir)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	modes := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		// <mode> <sha> <stage>\t<path>
		meta, path, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) == 0 {
			continue
		}
		modes[path] = fields[0]
	}
	return modes, nil
}

// The code map in AGENTS.md is generated from the package doc comments, so it
// cannot describe a layout the code no longer has. With -update this is
// `make codemap`.
func TestCodeMapIsUpToDate(t *testing.T) {
	root := repoRoot(t)

	pkgs, err := Packages(root)
	if err != nil {
		t.Fatalf("listing the packages: %v", err)
	}
	if len(pkgs) == 0 {
		t.Fatal("no package found: the code map is not doing anything")
	}

	var undocumented []string
	for _, p := range pkgs {
		if p.Synopsis == "" {
			undocumented = append(undocumented, p.Path)
		}
	}
	if len(undocumented) > 0 {
		t.Errorf("these packages have no doc comment, so the generated code map has nothing to "+
			"say about them — add one whose first sentence states what the package owns:\n  %s",
			strings.Join(undocumented, "\n  "))
	}

	path := filepath.Join(root, "AGENTS.md")
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading AGENTS.md: %v", err)
	}

	body := RenderCodeMap(pkgs)
	want := CodeMapBegin + "\n" + strings.TrimRight(body, "\n") + "\n" + CodeMapEnd + "\n"

	if *update {
		next, ok := blocktext.ReplaceBetween(string(current), CodeMapBegin, CodeMapEnd, want)
		if !ok {
			t.Fatalf("AGENTS.md does not contain the generated block markers:\n  %s\n  %s",
				CodeMapBegin, CodeMapEnd)
		}
		if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
			t.Fatalf("writing AGENTS.md: %v", err)
		}
		t.Logf("regenerated the code map with %d packages", len(pkgs))
		return
	}

	if !strings.Contains(string(current), want) {
		t.Errorf("the code map in AGENTS.md is stale. Run `make codemap` and commit the result.\n"+
			"(%d packages)", len(pkgs))
	}
}

// A `D<n>` in a comment is this repository's reference convention: no path, so it
// survives a reworded title and costs almost nothing to write. The price is that
// nothing about it is checked by the compiler, so this checks it.
func TestEveryDecisionCitedInCodeResolves(t *testing.T) {
	root := repoRoot(t)

	decisions, err := LoadDecisions(root)
	if err != nil {
		t.Fatalf("loading the decisions: %v", err)
	}
	exists := map[int]bool{}
	for _, d := range decisions {
		if d.Num > 0 {
			exists[d.Num] = true
		}
	}

	cited, err := CitedDecisions(root)
	if err != nil {
		t.Fatalf("scanning the Go sources: %v", err)
	}
	if len(cited) == 0 {
		t.Fatal("no D<n> reference found in the Go sources: the check is not doing anything")
	}

	for num, where := range cited {
		if exists[num] {
			continue
		}
		if reason, known := KnownDanglingDecisions[num]; known {
			t.Logf("D%d is a known dangling reference (%s), first seen in %s", num, reason, where)
			continue
		}
		t.Errorf("D%d is cited in %s but there is no docs/decisions/D%d-*.md.\n"+
			"Either write the record (`make decisions-new N=%d …`) or fix the reference — "+
			"a number that resolves to nothing is worse than no reference at all.",
			num, where, num, num)
	}

	// A stale allow-list entry is how a gate quietly stops meaning anything.
	for num := range KnownDanglingDecisions {
		if exists[num] {
			t.Errorf("D%d is in KnownDanglingDecisions but the record now exists: remove the entry", num)
		}
	}
}

// Claude Code is the only client that will not read AGENTS.md. It gets a
// one-line import rather than a symlink, because a public repository does not
// choose the operating system of the people who clone it and git does not
// materialize symlinks on a Windows checkout without Developer Mode.
func TestClaudeMdOnlyImportsAgentsMd(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("reading CLAUDE.md: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "@AGENTS.md" {
		t.Errorf("CLAUDE.md should contain exactly `@AGENTS.md` and nothing else, got %q.\n"+
			"Instructions live in AGENTS.md, which Codex, Kiro and Antigravity read natively.", got)
	}
}

// Written to Kiro's rules, the strictest of the four, so the same file is valid
// on every client.
func TestSkillFrontmatterMatchesTheStrictestClient(t *testing.T) {
	root := repoRoot(t)
	kebab := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

	dirs, err := os.ReadDir(filepath.Join(root, ".agents", "skills"))
	if err != nil {
		t.Fatalf("reading .agents/skills: %v", err)
	}

	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		name := d.Name()
		data, err := os.ReadFile(filepath.Join(root, ".agents", "skills", name, "SKILL.md"))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}

		fm, err := frontmatter(string(data))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}

		switch got := fm["name"]; {
		case got == "":
			t.Errorf("%s: `name` is missing; Codex and Kiro both require it", name)
		case got != name:
			t.Errorf("%s: `name` is %q but the directory is %q; Kiro requires them equal", name, got, name)
		case !kebab.MatchString(got):
			t.Errorf("%s: `name` %q must be lowercase alphanumeric with single hyphens", name, got)
		case len(got) > MaxSkillNameChars:
			t.Errorf("%s: `name` is %d characters, over %d", name, len(got), MaxSkillNameChars)
		}

		desc := fm["description"]
		if desc == "" {
			t.Errorf("%s: `description` is missing; it is what every client matches on", name)
		} else if n := len([]rune(desc)); n > MaxSkillDescriptionChars {
			t.Errorf("%s: `description` is %d characters, over %d", name, n, MaxSkillDescriptionChars)
		}
	}
}

// A moved page does not report its inbound links, so this does. Code — fenced
// blocks and inline spans — is stripped first: the documentation is full of
// markdown link *examples* describing the KB's own link format, and they are
// illustrations, not links (the same distinction the KB linter draws, D150).
func TestEveryRelativeDocLinkResolves(t *testing.T) {
	root := repoRoot(t)
	link := regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`)

	var files []string
	for _, pattern := range []string{"*.md", "docs/*.md", "docs/decisions/*.md", ".agents/skills/*/SKILL.md"} {
		matched, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			t.Fatalf("globbing %s: %v", pattern, err)
		}
		files = append(files, matched...)
	}
	if len(files) == 0 {
		t.Fatal("no markdown file collected: the link check is not doing anything")
	}

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Errorf("reading %s: %v", file, err)
			continue
		}
		rel, _ := filepath.Rel(root, file)

		for _, m := range link.FindAllStringSubmatch(stripCode(string(data)), -1) {
			target := m[1]
			switch {
			case strings.HasPrefix(target, "http://"), strings.HasPrefix(target, "https://"),
				strings.HasPrefix(target, "mailto:"), strings.HasPrefix(target, "#"):
				continue
			}
			if i := strings.IndexByte(target, '#'); i >= 0 {
				target = target[:i]
			}
			if target == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(file), target)); err != nil {
				t.Errorf("%s links to %q, which does not exist", rel, m[1])
			}
		}
	}
}

// stripCode blanks out fenced code blocks and inline code spans, preserving line
// structure so reported positions stay meaningful.
func stripCode(text string) string {
	var b strings.Builder
	b.Grow(len(text))

	inFence := false
	for i, line := range strings.Split(text, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "```") ||
			strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		// drop inline code spans, including double-backtick spans
		out := line
		for _, delim := range []string{"``", "`"} {
			var parts []string
			for j, seg := range strings.Split(out, delim) {
				if j%2 == 0 {
					parts = append(parts, seg)
				}
			}
			out = strings.Join(parts, "")
		}
		b.WriteString(out)
	}
	return b.String()
}

// No link may point at a decision through the old thematic register plus an
// anchor: those registers no longer exist, and an anchor-shaped reference is the
// shape that silently survives a split.
func TestNoLinkUsesTheOldDecisionAnchors(t *testing.T) {
	root := repoRoot(t)
	stale := regexp.MustCompile(`\]\([^)]*#d\d+[^)]*\)`)

	var offenders []string
	for _, pattern := range []string{"*.md", "docs/*.md", "docs/decisions/*.md", ".agents/skills/*/SKILL.md"} {
		matched, _ := filepath.Glob(filepath.Join(root, pattern))
		for _, file := range matched {
			if strings.HasSuffix(file, "CHANGELOG.md") {
				continue // a released changelog is a historical record, not documentation
			}
			data, err := os.ReadFile(file)
			if err != nil {
				continue
			}
			if found := stale.FindAllString(string(data), -1); found != nil {
				rel, _ := filepath.Rel(root, file)
				offenders = append(offenders, fmt.Sprintf("%s: %s", rel, strings.Join(found, ", ")))
			}
		}
	}
	if len(offenders) > 0 {
		t.Errorf("links still use a `#d<n>` anchor into a thematic register; one decision is one "+
			"file now, so the link is `decisions/D<n>-<slug>.md`:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

func frontmatter(text string) (map[string]string, error) {
	if !strings.HasPrefix(text, "---\n") {
		return nil, fmt.Errorf("no YAML frontmatter")
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return nil, fmt.Errorf("frontmatter is not closed")
	}
	out := map[string]string{}
	key := ""
	for _, line := range strings.Split(text[4:4+end], "\n") {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			if key != "" { // folded scalar continuation
				out[key] = strings.TrimSpace(out[key] + " " + strings.TrimSpace(line))
			}
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if v == ">-" || v == ">" || v == "|" || v == "|-" {
			v = ""
		}
		out[key] = v
	}
	return out, nil
}
