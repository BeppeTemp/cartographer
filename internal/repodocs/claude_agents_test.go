package repodocs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The role bodies of the repository's subagents live only in `.claude/agents/`
// (D-new-two-dev-clients); the second client's definitions are generated from them
// (kiroagents.go). What still matters in a body's frontmatter is that each one pins a
// Claude model, so a coordinator never hands a mandate to whatever the session
// default is, and that the reviewers, the readers and the auditors cannot edit.
var claudeModelID = regexp.MustCompile(`^claude-[a-z]+-\d+-\d+$`)

// readOnlyAgents are the agents that must not change the working copy.
var readOnlyAgents = map[string]bool{
	"cartographer-contract-reviewer": true,
	"cartographer-docs-auditor":      true,
	"cartographer-investigator":      true,
	"cartographer-plan-writer":       true,
	"cartographer-security-reviewer": true,
	"cartographer-source-reader":     true,
}

func TestClaudeSubagentsPinAModelAndReadOnlyOnesCannotEdit(t *testing.T) {
	root := repoRoot(t)
	mds, err := filepath.Glob(filepath.Join(root, ".claude", "agents", "*.md"))
	if err != nil || len(mds) == 0 {
		t.Fatalf("no .claude/agents/*.md: the check is not doing anything (%v)", err)
	}
	seen := map[string]bool{}
	for _, md := range mds {
		name := strings.TrimSuffix(filepath.Base(md), ".md")
		seen[name] = true
		raw, err := os.ReadFile(md)
		if err != nil {
			t.Error(err)
			continue
		}
		parts := strings.SplitN(string(raw), "---\n", 3)
		if len(parts) != 3 || parts[0] != "" {
			t.Errorf(".claude/agents/%s.md: no frontmatter", name)
			continue
		}
		var fm struct {
			Name  string `yaml:"name"`
			Model string `yaml:"model"`
			Tools string `yaml:"tools"`
		}
		if err := yaml.Unmarshal([]byte(parts[1]), &fm); err != nil {
			t.Errorf(".claude/agents/%s.md: the frontmatter is not valid YAML: %v", name, err)
			continue
		}
		if fm.Name != name {
			t.Errorf(".claude/agents/%s.md: `name` is %q, want the file name", name, fm.Name)
		}
		if !claudeModelID.MatchString(fm.Model) {
			t.Errorf(".claude/agents/%s.md: `model` is %q, want a pinned `claude-<family>-<major>-<minor>` id", name, fm.Model)
		}
		if readOnlyAgents[name] && fm.Tools != "Read, Grep, Glob, Bash" {
			t.Errorf(".claude/agents/%s.md: `tools` is %q, want `Read, Grep, Glob, Bash` (no Edit, no Write)", name, fm.Tools)
		}
	}
	for name := range readOnlyAgents {
		if !seen[name] {
			t.Errorf(".claude/agents/%s.md is missing", name)
		}
	}
}

// AC-P7-2: an implementer never runs the hosted check; the coordinator starts
// one on the tip beside the review (D-new-fast-review-loop).
func TestImplementerAgentsNeverRunCheckHosted(t *testing.T) {
	root := repoRoot(t)
	for _, name := range []string{"cartographer-plan-implementer", "cartographer-fix-implementer", "cartographer-live-implementer"} {
		raw, err := os.ReadFile(filepath.Join(root, ".claude", "agents", name+".md"))
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if !strings.Contains(strings.ToLower(text), "never `make check-hosted`") {
			t.Errorf(".claude/agents/%s.md: it must say the implementer never runs `make check-hosted`", name)
		}
		// Whatever is left once the "never" sentence and the description of what
		// the Windows leg runs are removed must not name the hosted check at all.
		rest := regexp.MustCompile("(?i)never `make check-hosted`").ReplaceAllString(text, "")
		rest = strings.ReplaceAll(rest, "the Windows leg of `make check-hosted` runs the test", "")
		if strings.Contains(rest, "check-hosted") {
			t.Errorf(".claude/agents/%s.md: `check-hosted` appears outside the \"never\" sentence: an implementer is not told to run it", name)
		}
	}
}

// AC-P6-4, AC-P6-5, AC-P6-6: the second client's definitions are generated from
// the Claude ones, one file per role, and nothing else matches the prefix.
func TestKiroAgentsAreGeneratedFromTheClaudeOnes(t *testing.T) {
	root := repoRoot(t)
	mds, _ := filepath.Glob(filepath.Join(root, ".claude", "agents", "cartographer-*.md"))
	if len(mds) == 0 {
		t.Fatal("no .claude/agents/cartographer-*.md: the check is not doing anything")
	}
	fix := "make kiro-agents"
	want := map[string]bool{}
	for _, md := range mds {
		name := strings.TrimSuffix(filepath.Base(md), ".md")
		raw, err := os.ReadFile(md)
		if err != nil {
			t.Fatal(err)
		}
		gen, err := GenerateKiroAgent(name, raw)
		if err != nil {
			t.Error(err)
			continue
		}
		rel := filepath.Join(".kiro", "agents", name+".json")
		want[rel] = true
		for _, line := range strings.Split(strings.SplitN(string(raw), "---\n", 3)[2], "\n") {
			if len(line) >= 30 && strings.Contains(string(gen), line) {
				t.Errorf("%s holds a line of the role's body: a definition carries a pointer, never a copy", rel)
				break
			}
		}
		if *update {
			// Never write through a symlink: it could point outside the tree.
			for _, p := range []string{filepath.Join(root, ".kiro"), filepath.Join(root, ".kiro", "agents"), filepath.Join(root, rel)} {
				if fi, err := os.Lstat(p); err == nil && fi.Mode()&os.ModeSymlink != 0 {
					t.Fatalf("%s is a symlink: make kiro-agents refuses to write through it", p)
				}
			}
			if err := os.MkdirAll(filepath.Join(root, ".kiro", "agents"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, rel), gen, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil || string(got) != string(gen) {
			t.Errorf("%s is missing or differs from its generator. Run `%s` and commit the result.", rel, fix)
		}
		var def kiroAgent
		if err := json.Unmarshal(gen, &def); err != nil {
			t.Fatal(err)
		}
		if readOnlyAgents[name] && (contains(def.Tools, "write") || contains(def.Tools, "code")) {
			t.Errorf("%s: a read-only role has the write or the code tool (code can rewrite)", rel)
		}
		for _, r := range def.Permissions.Rules {
			if r.Effect != "allow" || (r.Tool != "fs_read" && r.Tool != "fs_write") || (r.Tool == "fs_write" && !contains(def.Tools, "write")) {
				t.Errorf("%s: the rule %+v is a pre-approval the .claude source does not have", rel, r)
			}
		}
	}
	existing, _ := filepath.Glob(filepath.Join(root, ".kiro", "agents", "cartographer-*.json"))
	for _, f := range existing {
		rel, _ := filepath.Rel(root, f)
		if !want[rel] {
			if *update {
				_ = os.Remove(f)
				continue
			}
			t.Errorf("%s has no .claude/agents source. Run `%s` or delete it.", rel, fix)
		}
	}
}

func TestGenerateKiroAgent(t *testing.T) {
	md := func(model, tools string) []byte {
		fm := "---\nname: cartographer-x\ndescription: d\nmodel: " + model + "\n"
		if tools != "" {
			fm += "tools: " + tools + "\n"
		}
		return []byte(fm + "---\nbody\n")
	}
	cases := []struct {
		name, model, tools, wantErr string
		wantContains                []string
		wantAbsent                  []string
	}{
		{name: "model spelling and tool table", model: "claude-sonnet-5-5", tools: "Read, Grep, Glob, Bash",
			wantContains: []string{`"claude-sonnet-5.5"`, `"shell"`}, wantAbsent: []string{"fs_write", `"write"`, `"code"`, "CONTRIBUTING.md"}},
		{name: "write and edit are one tool", model: "claude-haiku-5-5", tools: "Read, Edit, Write",
			wantContains: []string{`"claude-haiku-5.5"`, "fs_write", `"code"`, "CONTRIBUTING.md"}},
		{name: "no tools", model: "claude-sonnet-5-5",
			wantContains: []string{`"write"`, `"glob"`, "fs_write"}},
		{name: "unknown tool", model: "claude-sonnet-5-5", tools: "Read, WebFetch", wantErr: "WebFetch"},
		{name: "bad model", model: "sonnet", wantErr: "model"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := GenerateKiroAgent("cartographer-x", md(c.model, c.tools))
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want one naming %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range c.wantContains {
				if !strings.Contains(string(out), s) {
					t.Errorf("output lacks %s:\n%s", s, out)
				}
			}
			for _, s := range c.wantAbsent {
				if strings.Contains(string(out), s) {
					t.Errorf("output has %s:\n%s", s, out)
				}
			}
			if strings.Contains(string(out), "allowedTools") || strings.Contains(string(out), "toolsSettings") {
				t.Error("a V2-only key was written")
			}
		})
	}
}

// AC-P6-9: the table of CONTRIBUTING.md names exactly the declared development
// clients, and each has a test of its definitions.
func TestContributingTableNamesTheDeclaredClients(t *testing.T) {
	root := repoRoot(t)
	var header []string
	for _, line := range strings.Split(readFile(t, root, "CONTRIBUTING.md"), "\n") {
		if strings.HasPrefix(line, "| Difference |") {
			for _, c := range strings.Split(strings.Trim(line, "| "), "|") {
				header = append(header, strings.TrimSpace(c))
			}
			break
		}
	}
	var want []string
	src := readFile(t, root, "internal/repodocs/claude_agents_test.go")
	for _, c := range DevelopmentClients {
		want = append(want, c.Name)
		if !strings.Contains(src, "func "+c.DefinitionsTest+"(") {
			t.Errorf("%s is a declared development client but %s does not exist in claude_agents_test.go", c.Name, c.DefinitionsTest)
		}
	}
	if len(header) != len(want)+1 || strings.Join(header[1:], ",") != strings.Join(want, ",") {
		t.Errorf("the table in CONTRIBUTING.md §Working with an agent client has the columns %q, want Difference then %q", header, want)
	}
}

// Anyone can comment on a public issue, and anyone can write its body: an
// agent definition or skill that reads an issue's or a PR's text through `gh`
// must keep only the maintainer associations, or a stranger's text reaches the
// agent as the maintainer's decision. A read of non-text fields only (state,
// labels, the author association itself) is free.
func TestGhIssueCommentReadsFilterByAuthorAssociation(t *testing.T) {
	root := repoRoot(t)
	var files []string
	for _, pat := range []string{
		filepath.Join(".claude", "agents", "*.md"),
		filepath.Join(".claude", "skills", "*", "SKILL.md"),
		filepath.Join(".claude", "skills", "*", "references", "*.md"),
	} {
		m, _ := filepath.Glob(filepath.Join(root, pat))
		files = append(files, m...)
	}
	if len(files) == 0 {
		t.Fatal("no agent definition or skill found")
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range unguardedGhReads(string(raw)) {
			t.Errorf("%s: `%s` reads issue or PR text without a select on the OWNER, MEMBER and COLLABORATOR association (IN(...)) that guards .body", f, m)
		}
	}
}

// ghReadCommands returns every `gh issue view`, `gh pr view` and `gh api`
// command of a Markdown file as one line: the inline code spans (wrapped over
// lines or not) and the commands of fenced blocks (backslash continuations
// joined).
func ghReadCommands(md string) []string {
	start := regexp.MustCompile(`gh (?:issue view|pr view|api)\b`)
	var out []string
	var prose []string
	flush := func() {
		text := strings.Join(prose, " ")
		prose = nil
		for _, sp := range regexp.MustCompile("`([^`]*)`").FindAllStringSubmatch(text, -1) {
			if loc := start.FindStringIndex(sp[1]); loc != nil {
				out = append(out, strings.Join(strings.Fields(sp[1][loc[0]:]), " "))
			}
		}
	}
	lines := strings.Split(md, "\n")
	fence := ""
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if t := strings.TrimSpace(l); strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			if fence == "" {
				fence = t[:3]
			} else if strings.HasPrefix(t, fence) {
				fence = ""
			}
			continue
		}
		if fence != "" {
			for i+1 < len(lines) && (strings.HasSuffix(l, "\\") || strings.HasSuffix(strings.TrimSpace(l), "|")) {
				i++
				l = strings.TrimSuffix(l, "\\") + " " + lines[i]
			}
			if loc := start.FindStringIndex(l); loc != nil {
				out = append(out, strings.Join(strings.Fields(l[loc[0]:]), " "))
			}
			continue
		}
		if strings.TrimSpace(l) == "" {
			flush()
			continue
		}
		prose = append(prose, l)
	}
	flush()
	return out
}

func TestGhReadCommandsFindsWrappedAndFenced(t *testing.T) {
	md := "text `gh issue view 1\n--json comments` more\n\n```sh\ngh api graphql \\\n  -f q=x\n```\n"
	got := ghReadCommands(md)
	if len(got) != 2 || got[0] != "gh issue view 1 --json comments" || got[1] != "gh api graphql -f q=x" {
		t.Fatalf("got %q", got)
	}
}

// The workflows open their `bug` and `ci-red` issues as github-actions[bot],
// which GitHub reports as a Bot with the association CONTRIBUTOR (checked on a
// real one), so the association filter alone would drop the body of every such
// issue: an agent that reads an issue's body keeps the bot's by its login.
func TestIssueBodyReadKeepsTheWorkflowBot(t *testing.T) {
	root := repoRoot(t)
	for _, name := range []string{"cartographer-fix-implementer.md", "cartographer-plan-writer.md"} {
		raw, err := os.ReadFile(filepath.Join(root, ".claude", "agents", name))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, m := range ghReadCommands(string(raw)) {
			if strings.HasPrefix(m, "gh api") && strings.Contains(m, "author_association") && strings.Contains(m, ".body") {
				found = true
				if !strings.Contains(m, `.user.login == "github-actions[bot]"`) {
					t.Errorf("%s: the issue body read %q does not keep the body of github-actions[bot]", name, m)
				}
			}
		}
		if !found {
			t.Errorf("%s: no issue body read through gh api found", name)
		}
	}
}

func TestGhReadGuardRejectsWeakForms(t *testing.T) {
	const sel = `select(.author_association | IN("OWNER","MEMBER","COLLABORATOR")) | .body`
	bad := []string{
		"`gh api -H X repos/o/r/issues/1`",
		"`gh api --paginate repos/o/r/issues/1/comments`",
		"`gh api repos/o/r/issues/1 --jq 'select(true or (.author_association | IN(\"OWNER\",\"MEMBER\",\"COLLABORATOR\"))) | .body'`",
		"~~~sh\ngh issue view 1 --json body\n~~~\n",
		"```sh\ngh issue view 1 --json comments --jq '.comments[]' |\n  jq .body\n```\n",
		"```sh\ngh pr view 1 \\\n --json reviews\n```\n",
	}
	for _, md := range bad {
		if len(unguardedGhReads(md)) == 0 {
			t.Errorf("not rejected: %q", md)
		}
	}
	good := []string{
		"`gh api -H X repos/o/r/issues/1 --jq '.comments[] | " + sel + "'`",
		"~~~sh\ngh issue view 1 --json comments --jq '.comments[] |\n " + sel + "'\n~~~\n",
		"`gh api repos/o/r/issues/1 --jq '.title, (select(.user.login == \"github-actions[bot]\" or (.author_association | IN(\"OWNER\",\"MEMBER\",\"COLLABORATOR\"))) | .body)'`",
	}
	for _, md := range good {
		if g := unguardedGhReads(md); len(g) != 0 {
			t.Errorf("rejected %q: %q", md, g)
		}
	}
}

var apiText = regexp.MustCompile(`^gh api\b.*(?:\bgraphql\b|/(?:issues|pulls)/)`)

// A select on the maintainer associations that is followed by `.body`: the
// `IN(` form, the three associations and the body behind the guard (the bot
// the workflows run as, github-actions[bot], is the one allowed addition).
const inList = `IN\(\s*"OWNER"\s*,\s*"MEMBER"\s*,\s*"COLLABORATOR"\s*\)`
const field = `\.(?:authorAssociation|author_association)\s*\|\s*`

var assoc = regexp.MustCompile(`select\(\s*` + field + inList + `\s*\)\s*\|\s*\.body\b|` +
	`select\(\s*\.user\.login\s*==\s*"github-actions\[bot\]"\s*or\s*\(\s*` + field + inList + `\s*\)\s*\)\s*\|\s*\.body\b`)
var readsText = func(m string) bool {
	if strings.HasPrefix(m, "gh api") {
		if !apiText.MatchString(m) {
			return false
		}
		if strings.HasPrefix(m, "gh api graphql") {
			return true
		}
		// `--jq .author_association`: a non-text field, no select needed.
		if strings.Contains(m, "--jq") && !strings.Contains(m, "body") && !strings.Contains(m, "/comments") {
			return false
		}
		return true
	}
	i := strings.Index(m, "--json")
	if i < 0 {
		return true // a bare `gh issue view` prints the body
	}
	fields := m[i:]
	for _, w := range []string{"body", "comments", "reviews", "latestReviews"} {
		if strings.Contains(fields, w) {
			return true
		}
	}
	return false
}

// unguardedGhReads returns the gh commands of a Markdown file that read issue
// or PR text without the association guard.
func unguardedGhReads(md string) []string {
	var out []string
	for _, m := range ghReadCommands(md) {
		if readsText(m) && !assoc.MatchString(m) {
			out = append(out, m)
		}
	}
	return out
}
