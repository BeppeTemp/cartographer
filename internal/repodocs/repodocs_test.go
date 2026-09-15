package repodocs

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/blocktext"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

var update = flag.Bool("update", false, "rewrite the generated blocks instead of checking them")

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := Root()
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

// checkGeneratedBlock compares one generated block against what the generator
// produces now, or rewrites it when -update is passed. The comparison is on the
// block's exact body, obtained via BlockBody, which insists on exactly one marker
// pair: a substring match would accept a second, stale copy of the block sitting
// elsewhere in the file.
func checkGeneratedBlock(t *testing.T, root, rel, begin, end, body, fixCmd string) {
	t.Helper()

	path := filepath.Join(root, rel)
	current := readFile(t, root, rel)
	want := "\n" + strings.TrimRight(body, "\n") + "\n"

	if *update {
		next, ok := blocktext.ReplaceBetween(current, begin, end,
			begin+want+end+"\n")
		if !ok {
			t.Fatalf("%s does not contain the generated block markers:\n  %s\n  %s", rel, begin, end)
		}
		if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
			t.Fatalf("writing %s: %v", rel, err)
		}
		t.Logf("regenerated the block in %s", rel)
		return
	}

	got, err := BlockBody(current, begin, end)
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	if got != want {
		t.Errorf("the generated block in %s is stale. Run `%s` and commit the result.", rel, fixCmd)
	}
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

	checkGeneratedBlock(t, root, filepath.Join("docs", "decisions.md"),
		IndexBegin, IndexEnd, RenderIndex(decisions), "make decisions-index")
}

func TestNoDecisionNumberIsUsedTwice(t *testing.T) {
	decisions, err := LoadDecisions(repoRoot(t))
	if err != nil {
		t.Fatalf("loading the decisions: %v", err)
	}
	seen := map[int]string{}
	var historical []string
	for _, d := range decisions {
		if d.Num == 0 {
			// The thirteen original ADs are one historical file with no
			// individual bodies. A second one would mean two files claiming the
			// same unnumbered slot, which the numeric check below cannot see.
			historical = append(historical, d.File)
			continue
		}
		if prev, dup := seen[d.Num]; dup {
			t.Errorf("D%d is claimed by two files: %s and %s", d.Num, prev, d.File)
			continue
		}
		seen[d.Num] = d.File
	}
	if len(historical) > 1 {
		t.Errorf("more than one unnumbered AD file: %v — they are a single historical record",
			historical)
	}
}

// A half-written decision still indexes, and `<title>` renders as an HTML tag, so
// the entry appears blank on GitHub and on the site. `make decisions-new` cannot
// prevent it (it deliberately only reserves the file), so this does.
func TestNoDecisionFileKeepsATemplatePlaceholder(t *testing.T) {
	root := repoRoot(t)

	decisions, err := LoadDecisions(root)
	if err != nil {
		t.Fatalf("loading the decisions: %v", err)
	}
	for _, d := range decisions {
		text := readFile(t, root, filepath.Join("docs", "decisions", d.File))
		for _, ph := range TemplatePlaceholders {
			if strings.Contains(text, ph) {
				t.Errorf("docs/decisions/%s still contains the template placeholder %q: "+
					"finish the record (or delete the file) before committing it", d.File, ph)
			}
		}
	}
}

// The budgets no client reports on. See the constants for what enforces each.
//
// Lines are measured on the hand-written text and characters on the whole file,
// on purpose: the character budget is what a client loads, while the line budget
// is a proxy for how much prose a reader will actually follow, and the generated
// code map is neither prose nor something an author trims by moving a section.
func TestContextFileBudgets(t *testing.T) {
	root := repoRoot(t)
	text := readFile(t, root, "AGENTS.md")

	handWritten := StripGeneratedBlocks(text)
	if lines := strings.Count(handWritten, "\n") + 1; lines > MaxRootLines {
		t.Errorf("the hand-written part of AGENTS.md is %d lines, over the %d-line budget: move a "+
			"section to the page that owns it and leave one line of pointer", lines, MaxRootLines)
	}
	if chars := len([]rune(text)); chars > MaxRootChars {
		t.Errorf("AGENTS.md is %d characters, over the %d this repository allows for a file a "+
			"client loads whole (see MaxRootChars)", chars, MaxRootChars)
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

	skills, err := os.ReadDir(filepath.Join(root, CanonicalSkillDir))
	if err != nil {
		t.Fatalf("reading %s: %v", CanonicalSkillDir, err)
	}
	var names []string
	for _, s := range skills {
		if s.IsDir() {
			names = append(names, s.Name())
		}
	}
	if len(names) == 0 {
		t.Fatalf("%s is empty: the parity check is not doing anything", CanonicalSkillDir)
	}

	bridges := SupportedClients()
	if len(bridges) == 0 {
		t.Fatal("no client needs a bridge: the parity check is not doing anything")
	}

	for _, client := range bridges {
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
					"  ln -s ../../%s/%s %s && git add %s",
					want, client.Name, name, CanonicalSkillDir, name, want, want)
			case mode != "120000":
				t.Errorf("%s is recorded in the git index with mode %s instead of 120000 "+
					"(symlink): it is a copy, and it will drift from %s/%s.\n"+
					"  git rm -r --cached %s && rm -rf %s && ln -s ../../%s/%s %s && git add %s",
					want, mode, CanonicalSkillDir, name, want, want, CanonicalSkillDir, name, want, want)
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

// Where a client reads its instructions and its skills is a fact this repository
// already owns, in internal/provisioning's provider matrix. CONTRIBUTING.md's
// per-client table restates it for a human, and a restatement drifts: it did,
// for the whole Antigravity row. This checks the restatement against the matrix.
func TestClientSkillSurfacesMatchTheProviderRegistry(t *testing.T) {
	root := repoRoot(t)
	contributing := readFile(t, root, "CONTRIBUTING.md")

	const probe = "probe-skill"
	for _, c := range ClientSurfaces {
		got := provisioning.ProjectDestination("skill", probe, c.Provider)
		want := ""
		if c.SkillDir != "" {
			want = filepath.Join(c.SkillDir, probe)
		}
		if got != want {
			t.Errorf("%s: ClientSurfaces says a project-local skill goes to %q, but "+
				"provisioning.ProjectDestination says %q.\nOne of the two is wrong — the matrix in "+
				"internal/provisioning/workspacescope.go is the audited source (D193), so change "+
				"ClientSurfaces and CONTRIBUTING.md unless you are re-auditing the client.",
				c.Name, want, got)
		}

		row := tableRow(contributing, "| **"+c.Name+"**")
		switch {
		case row == "":
			t.Errorf("CONTRIBUTING.md §Working with an agent client has no row for %s", c.Name)
		case !strings.Contains(row, c.RowMustSay):
			t.Errorf("CONTRIBUTING.md's %s row does not mention %q:\n  %s\n"+
				"A client with no project-local scope must name its real, global location rather "+
				"than leaving the reader to assume there is a repo-local one.",
				c.Name, c.RowMustSay, strings.TrimSpace(row))
		}

		if c.SkillDir == "" && strings.Contains(row, CanonicalSkillDir) {
			t.Errorf("CONTRIBUTING.md's %s row claims %s, but the provider matrix gives that "+
				"client no project-local skill scope at all:\n  %s",
				c.Name, CanonicalSkillDir, strings.TrimSpace(row))
		}
	}
}

// tableRow returns the first line starting with prefix, or "".
func tableRow(text, prefix string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
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

	checkGeneratedBlock(t, root, "AGENTS.md",
		CodeMapBegin, CodeMapEnd, RenderCodeMap(pkgs), "make codemap")
}

// A `D<n>` in a comment is this repository's reference convention: no path, so it
// survives a reworded title and costs almost nothing to write. The price is that
// nothing about it is checked by the compiler, so this checks it — over every
// tracked text file, not only the Go sources. The reference that motivated the
// gate lived in Go comments *and* in docs/sync.md, and the first version of the
// gate only read the former; the one it missed, D130, was in three decision
// records citing each other.
func TestEveryDecisionCitedResolves(t *testing.T) {
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
		t.Fatalf("scanning the repository: %v", err)
	}
	if len(cited) == 0 {
		t.Fatal("no D<n> reference found: the check is not doing anything")
	}

	for _, num := range sortedKeys(cited) {
		where := cited[num]
		switch {
		case exists[num]:
			continue
		case GapDecisions[num] != "":
			continue // deliberately has no record
		case ReservedDecisions[num] != "":
			continue // an open plan holds the number until it lands
		}
		t.Errorf("D%d is cited in %s but there is no docs/decisions/D%d-*.md.\n"+
			"Write the record (`make decisions-new N=%d …`), fix the reference, or — if the number "+
			"is deliberately empty — declare it in GapDecisions/ReservedDecisions with the reason. "+
			"A number that resolves to nothing is worse than no reference at all.",
			num, strings.Join(capped(where, 4), ", "), num, num)
	}

	// Both directions. A declared gap that acquires a file is how a number
	// silently comes back meaning something else; a reserved number whose plan
	// has landed is a stale entry, and an allow-list nobody prunes is how a gate
	// quietly stops meaning anything (D206).
	for num, reason := range GapDecisions {
		if exists[num] {
			t.Errorf("D%d is declared a gap (%s) but docs/decisions/D%d-*.md now exists: "+
				"the number must not be reused — rename the record, or remove the GapDecisions "+
				"entry if reusing it is a deliberate, recorded choice", num, reason, num)
		}
	}
	for num, plan := range ReservedDecisions {
		if exists[num] {
			t.Errorf("D%d is reserved by %s but its record now exists: remove the "+
				"ReservedDecisions entry", num, plan)
		}
	}
}

// A page rename leaves every pointer to it dangling, and the ones in Go and shell
// comments are reported by nothing: not by the compiler, not by the markdown link
// check, not by mkdocs. Splitting the ten thematic decision registers into one
// file each left nineteen of them behind in nine packages and three e2e scripts.
func TestEveryCitedDocumentationPathExists(t *testing.T) {
	root := repoRoot(t)

	cited, err := CitedDocPaths(root)
	if err != nil {
		t.Fatalf("scanning the repository: %v", err)
	}
	if len(cited) == 0 {
		t.Fatal("no documentation path cited anywhere: the check is not doing anything")
	}

	var paths []string
	for p := range cited {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		if _, err := os.Stat(filepath.Join(root, p)); err == nil {
			continue
		}
		t.Errorf("%q is cited in %s but does not exist.\n"+
			"If the page moved, point at where it went; if the reference is to a decision, the "+
			"convention is the bare `D<n>` with no path, which survives the next move.",
			p, strings.Join(capped(cited[p], 4), ", "))
	}
}

func sortedKeys(m map[int][]string) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func capped(items []string, n int) []string {
	if len(items) <= n {
		return items
	}
	return append(append([]string{}, items[:n]...), fmt.Sprintf("… and %d more", len(items)-n))
}

// Claude Code is the only client that will not read AGENTS.md, at any depth: it
// loads a nested CLAUDE.md when it first reads a file in that directory, so every
// directory with an AGENTS.md needs one (D213). It is a one-line import rather
// than a symlink, because a public repository does not choose the operating
// system of the people who clone it and git does not materialize symlinks on a
// Windows checkout without Developer Mode. An AGENTS.override.md is refused: Codex
// reads it instead of the AGENTS.md beside it, not in addition to it.
func TestEveryAgentsMdHasAClaudeImport(t *testing.T) {
	root := repoRoot(t)
	dirs, err := InstructionDirs(root)
	if err != nil {
		t.Fatalf("listing the instruction directories: %v", err)
	}
	if len(dirs) == 0 {
		t.Fatal("no AGENTS.md found: the check is not doing anything")
	}
	for _, dir := range dirs {
		if _, err := os.Lstat(filepath.Join(root, dir, "AGENTS.override.md")); err == nil {
			t.Errorf("%s: Codex reads AGENTS.override.md instead of AGENTS.md, hiding that "+
				"directory's rules from one client only; put the content in AGENTS.md", filepath.Join(dir, "AGENTS.override.md"))
		}
		rel := filepath.Join(dir, "CLAUDE.md")
		fi, err := os.Lstat(filepath.Join(root, rel))
		switch {
		case err != nil:
			t.Errorf("%s is missing: Claude Code does not read AGENTS.md, so without it the "+
				"rules in %s never reach Claude — add a file containing exactly `@AGENTS.md`",
				rel, filepath.Join(dir, "AGENTS.md"))
			continue
		case fi.Mode()&os.ModeSymlink != 0:
			t.Errorf("%s is a symlink: a Windows checkout without Developer Mode turns it into "+
				"a text file and Claude loads nothing — make it a file containing `@AGENTS.md`", rel)
			continue
		}
		if got := strings.TrimSpace(readFile(t, root, rel)); got != "@AGENTS.md" {
			t.Errorf("%s should contain exactly `@AGENTS.md` and nothing else, got %q.\n"+
				"Instructions live in AGENTS.md, which Codex, Kiro and Antigravity read natively.", rel, got)
		}
	}
}

// Written to Kiro's rules, the strictest of the four, so the same file is valid
// on every client — and parsed with the same YAML parser a client uses, because
// the defect that actually loses a skill is a frontmatter no strict parser
// accepts, and a lenient reader of our own cannot see it.
func TestSkillFrontmatterMatchesTheStrictestClient(t *testing.T) {
	root := repoRoot(t)
	kebab := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

	dirs, err := os.ReadDir(filepath.Join(root, CanonicalSkillDir))
	if err != nil {
		t.Fatalf("reading %s: %v", CanonicalSkillDir, err)
	}

	checked := 0
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		name := d.Name()
		fm, err := ParseSkillFrontmatter(filepath.Join(root, CanonicalSkillDir, name, "SKILL.md"))
		if err != nil {
			t.Errorf("%s/%s: %v", CanonicalSkillDir, name, err)
			continue
		}
		checked++

		switch got := fm.Name; {
		case got == "":
			t.Errorf("%s: `name` is missing; Codex and Kiro both require it", name)
		case got != name:
			t.Errorf("%s: `name` is %q but the directory is %q; Kiro requires them equal", name, got, name)
		case !kebab.MatchString(got):
			t.Errorf("%s: `name` %q must be lowercase alphanumeric with single hyphens", name, got)
		case len(got) > MaxSkillNameChars:
			t.Errorf("%s: `name` is %d characters, over %d", name, len(got), MaxSkillNameChars)
		}

		if fm.Description == "" {
			t.Errorf("%s: `description` is missing; it is what every client matches on", name)
		} else if n := len([]rune(fm.Description)); n > MaxSkillDescriptionChars {
			t.Errorf("%s: `description` is %d characters, over %d", name, n, MaxSkillDescriptionChars)
		}
	}
	if checked == 0 {
		t.Fatalf("no SKILL.md parsed under %s: the check is not doing anything", CanonicalSkillDir)
	}
}

// A moved page does not report its inbound links, so this does. Code — fenced
// blocks and inline spans — is stripped first: the documentation is full of
// markdown link *examples* describing the KB's own link format, and they are
// illustrations, not links (the same distinction the KB linter draws, D150).
//
// The corpus is every tracked markdown file, not a hand-kept list of globs: the
// list had already missed the nested AGENTS.md files and the issue template.
func TestEveryRelativeDocLinkResolves(t *testing.T) {
	root := repoRoot(t)
	link := regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`)

	files, err := MarkdownFiles(root)
	if err != nil {
		t.Fatalf("collecting the markdown files: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no markdown file collected: the link check is not doing anything")
	}

	for _, rel := range files {
		text := stripCode(readFile(t, root, rel))
		for _, m := range link.FindAllStringSubmatch(text, -1) {
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
			if _, err := os.Stat(filepath.Join(root, filepath.Dir(rel), target)); err != nil {
				t.Errorf("%s links to %q, which does not exist", rel, m[1])
			}
		}
	}
}

// stripCode blanks out fenced code blocks and inline code spans, preserving line
// structure so reported positions stay meaningful.
//
// An unpaired backtick leaves the rest of its line intact. The obvious
// implementation — split on the delimiter, keep the even segments — silently
// drops everything after an odd delimiter, which means a broken link can hide
// behind a stray backtick in the same line.
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
		b.WriteString(stripInlineCode(line))
	}
	return b.String()
}

// stripInlineCode removes closed inline code spans from one line, longest
// delimiter first, and leaves an unclosed one — and everything after it — as is.
func stripInlineCode(line string) string {
	for _, delim := range []string{"``", "`"} {
		var b strings.Builder
		rest := line
		for {
			open := strings.Index(rest, delim)
			if open < 0 {
				b.WriteString(rest)
				break
			}
			close := strings.Index(rest[open+len(delim):], delim)
			if close < 0 { // unclosed: keep the remainder verbatim
				b.WriteString(rest)
				break
			}
			b.WriteString(rest[:open])
			rest = rest[open+len(delim)+close+len(delim):]
		}
		line = b.String()
	}
	return line
}

// No link may point at a decision through the old thematic register plus an
// anchor: those registers no longer exist, and an anchor-shaped reference is the
// shape that silently survives a split.
func TestNoLinkUsesTheOldDecisionAnchors(t *testing.T) {
	root := repoRoot(t)
	stale := regexp.MustCompile(`\]\([^)]*#d\d+[^)]*\)`)

	files, err := MarkdownFiles(root)
	if err != nil {
		t.Fatalf("collecting the markdown files: %v", err)
	}

	var offenders []string
	for _, rel := range files {
		if found := stale.FindAllString(readFile(t, root, rel), -1); found != nil {
			offenders = append(offenders, fmt.Sprintf("%s: %s", rel, strings.Join(found, ", ")))
		}
	}
	if len(offenders) > 0 {
		t.Errorf("links still use a `#d<n>` anchor into a thematic register; one decision is one "+
			"file now, so the link is `decisions/D<n>-<slug>.md`:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// The corpus definitions are load-bearing for four gates, and their failure mode
// is to quietly scan nothing. These assertions are cheap and they are what makes
// "the check is not doing anything" impossible to reach unnoticed.
func TestReferenceCorpusIsSane(t *testing.T) {
	root := repoRoot(t)

	files, err := TextFiles(root)
	if err != nil {
		t.Fatalf("collecting the text files: %v", err)
	}

	index := map[string]bool{}
	for _, f := range files {
		index[f] = true
	}
	for _, want := range []string{
		"AGENTS.md",
		"CONTRIBUTING.md",
		filepath.Join("docs", "index.md"),
		filepath.Join("docs", "decisions", "D206-d163-was-a-copied-off-by-one-not-a-missing-record.md"),
		filepath.Join("internal", "mcpserver", "AGENTS.md"),
		filepath.Join("internal", "provisioning", "provisioning.go"),
		filepath.Join(".agents", "skills", "implement-issue", "SKILL.md"),
		filepath.Join(".github", "ISSUE_TEMPLATE", "plan.md"),
		filepath.Join("test", "e2e", "scenarios", "03_config_opencode.sh"),
		"config.example.yaml",
	} {
		if !index[want] {
			t.Errorf("%s is missing from the reference corpus: a gate that does not read it "+
				"cannot report anything about it", want)
		}
	}
	for _, unwanted := range []string{
		"CHANGELOG.md",
		filepath.Join("test", "e2e", "fixtures", "kb-homelab-lite", "AGENTS.md"),
	} {
		if index[unwanted] {
			t.Errorf("%s is in the reference corpus but must not be: see skipFile/skipDir for why",
				unwanted)
		}
	}
}
