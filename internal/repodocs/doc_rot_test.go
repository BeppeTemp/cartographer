package repodocs

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Current-state pages state what is true now (docs/index.md §Documentation
// maintenance rules, D1128). This file is the mechanical half of that rule: it
// refuses the narrow set of phrasings that are reliably history or a hand-kept
// count. It is a net, not a proof: "now", "no longer", "since D<n>" and "before
// D<n>" are deliberately not policed, because most of their uses state something
// a reader can still hit and policing them would put dozens of sentences on the
// allow-list.

type rotRule struct {
	name   string
	re     *regexp.Regexp
	except *regexp.Regexp // a hit is dropped when an except match ends where the hit ends
	group  int            // the capture group reported as the match (0 = whole)
	hint   string
}

const rotCountNouns = `providers|clients|tools|checks|kinds|subagents|agents|runners|skills|modes|tiers|workflows|jobs|errors`
const rotNumbers = `three|four|five|six|seven|eight|nine|ten|eleven|twelve|thirteen|fourteen|fifteen|sixteen|seventeen|eighteen|nineteen|twenty|thirty|forty|fifty|[3-9]|[0-9]{2,}`

var rotRules = []rotRule{
	{
		name:   "used to",
		re:     regexp.MustCompile(`(?i)\bused to\b`),
		except: regexp.MustCompile(`(?i)\b(?:be|is|are|was|were|been|being|get|gets|can|cannot|also|only|not) used to\b`),
		hint:   "keep the present rule and drop \"used to\"",
	},
	{
		name: "previously",
		re:   regexp.MustCompile(`(?i)\bpreviously\b`),
		hint: "state what is true today, not what was before",
	},
	{
		name: "removed by D<n>",
		re:   regexp.MustCompile(`(?i)\b(?:removed|dropped|retired) (?:by|in) D[0-9]+\b`),
		hint: "say what the code does today; the decision file keeps the removal",
	},
	{
		name: "as of D<n>",
		re:   regexp.MustCompile(`(?i)\bas of D[0-9]+\b`),
		hint: "a current-state page is always \"as of now\"; drop the date marker",
	},
	{
		name: "became / gained",
		re:   regexp.MustCompile(`(?i)\b(?:became|gained)\b`),
		hint: "describe the present behaviour, not the change",
	},
	{
		name:  "hand-kept count",
		re:    regexp.MustCompile(`(?i)(?:^|[^\w.-])((?:` + rotNumbers + `)(?:\s+[A-Za-z-]+)?\s+(?:` + rotCountNouns + `)\b)`),
		group: 1,
		hint:  "name the members or link the source of truth instead of numbering them",
	},
}

// rotAllow is one sentence that matches a rule and is still true today: a
// retired key, tool or flag a reader can still meet. The fragment is matched
// whitespace-insensitively against the raw paragraph.
type rotAllow struct{ File, Fragment, Reason string }

var rotAllowed = []rotAllow{
	{"docs/configurator.md", "A provider that was not previously connected", "states the result of a probe a user meets on every upgrade, not a past state of the docs"},
	{"docs/control-plane.md", "pages that became or stopped being orphans", "describes a field of the changes_since digest, which reports a transition"},
	{"docs/deployment.md", "deprecated by D288 and **removed by D325**", "documents retired keys and flags that still load or fail on a leftover config"},
	{"docs/deployment.md", "Removed in D906, each warned about and ignored", "documents retired variables and flags that a launcher can still pass"},
	{"docs/deployment.md", `"Cartographer found 25 kinds`, "an example title of the UI, not a count the page keeps"},
	{"docs/transport-auth.md", "were deprecated by D288 and removed by D325", "documents retired keys a leftover YAML can still carry"},
	{"docs/transport-auth.md", "were removed by D325 the same way", "documents retired keys a leftover YAML can still carry"},
}

type rotFinding struct {
	file       string
	line       int
	rule       string
	hint       string
	match      string
	paragraph  string // raw text, newlines kept
	start, end int    // match offsets in paragraph
}

type rotParagraph struct {
	raw, masked string
	line        int
}

// maskLine blanks the closed inline code spans of one line with spaces of the
// same length: double backtick first, then single; an unclosed backtick leaves
// the rest of the line readable.
func maskLine(s string) string {
	b := []byte(s)
	for _, tick := range []string{"``", "`"} {
		pos := 0
		for {
			i := strings.Index(string(b[pos:]), tick)
			if i < 0 {
				break
			}
			i += pos
			j := strings.Index(string(b[i+len(tick):]), tick)
			if j < 0 {
				break
			}
			j += i + len(tick)
			for k := i; k < j+len(tick); k++ {
				b[k] = ' '
			}
			pos = j + len(tick)
		}
	}
	return string(b)
}

// rotParagraphs masks fenced blocks and inline code with same-length spaces (so
// offsets index the raw text) and groups the lines into paragraphs.
func rotParagraphs(text string) []rotParagraph {
	var out []rotParagraph
	var raws, masks []string
	first := 0
	flush := func() {
		if len(raws) == 0 {
			return
		}
		out = append(out, rotParagraph{raw: strings.Join(raws, "\n"), masked: strings.Join(masks, " "), line: first})
		raws, masks = nil, nil
	}
	inFence := false
	for i, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		masked := line
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			masked = strings.Repeat(" ", len(line))
		} else if inFence {
			masked = strings.Repeat(" ", len(line))
		} else {
			masked = maskLine(line)
		}
		if trimmed == "" {
			flush()
			continue
		}
		if len(raws) == 0 {
			first = i + 1
		}
		raws = append(raws, line)
		masks = append(masks, masked)
	}
	flush()
	return out
}

// findRot returns the findings of one file's text.
func findRot(file, text string) []rotFinding {
	var out []rotFinding
	for _, p := range rotParagraphs(text) {
		for _, r := range rotRules {
			for _, loc := range r.re.FindAllStringSubmatchIndex(p.masked, -1) {
				s, e := loc[2*r.group], loc[2*r.group+1]
				if r.except != nil && excepted(r.except, p.masked, e) {
					continue
				}
				out = append(out, rotFinding{
					file: file, rule: r.name, hint: r.hint,
					line:      p.line + strings.Count(p.raw[:s], "\n"),
					match:     strings.Join(strings.Fields(p.masked[s:e]), " "),
					paragraph: p.raw, start: s, end: e,
				})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].file != out[j].file {
			return out[i].file < out[j].file
		}
		return out[i].line < out[j].line
	})
	return out
}

func excepted(re *regexp.Regexp, s string, end int) bool {
	for _, l := range re.FindAllStringIndex(s, -1) {
		if l[1] == end {
			return true
		}
	}
	return false
}

func fragmentRe(fragment string) *regexp.Regexp {
	words := strings.Fields(fragment)
	for i, w := range words {
		words[i] = regexp.QuoteMeta(w)
	}
	return regexp.MustCompile(strings.Join(words, `\s+`))
}

// applyAllow returns the uncovered findings, the stale entries, the duplicate
// (File, Fragment) pairs and the entries without a fragment or a reason.
func applyAllow(findings []rotFinding, allowed []rotAllow) (open []rotFinding, stale, dup, noReason []rotAllow) {
	used := make([]bool, len(allowed))
	seen := map[[2]string]bool{}
	res := make([]*regexp.Regexp, len(allowed))
	for i, a := range allowed {
		key := [2]string{a.File, a.Fragment}
		if seen[key] {
			dup = append(dup, a)
		}
		seen[key] = true
		if strings.TrimSpace(a.Reason) == "" || strings.TrimSpace(a.Fragment) == "" {
			noReason = append(noReason, a)
			continue
		}
		res[i] = fragmentRe(a.Fragment)
	}
	for _, f := range findings {
		covered := false
		for i, a := range allowed {
			if res[i] == nil || a.File != f.file {
				continue
			}
			for _, l := range res[i].FindAllStringIndex(f.paragraph, -1) {
				if l[0] < f.end && f.start < l[1] {
					covered, used[i] = true, true
				}
			}
		}
		if !covered {
			open = append(open, f)
		}
	}
	for i, a := range allowed {
		if res[i] != nil && !used[i] {
			stale = append(stale, a)
		}
	}
	return
}

// inRotScope reports whether a slash path is a current-state page: decision
// files (history is their job) and CHANGELOG.md (a released record) are not.
func inRotScope(rel string) bool {
	if path.Ext(rel) != ".md" {
		return false
	}
	switch {
	case rel == "docs/decisions.md", strings.HasPrefix(rel, "docs/decisions/"):
		return false
	case strings.HasPrefix(rel, "docs/"):
		return true
	case rel == "README.md", rel == "CONTRIBUTING.md", rel == "AGENTS.md":
		return true
	case strings.HasPrefix(rel, "internal/skillbundle/bundled/"):
		return true
	case strings.HasPrefix(rel, "internal/") && path.Base(rel) == "AGENTS.md":
		return true
	case strings.HasPrefix(rel, ".claude/skills/"):
		return true
	case strings.HasPrefix(rel, ".claude/agents/") && path.Dir(rel) == ".claude/agents":
		return true
	}
	return false
}

func TestInRotScope(t *testing.T) {
	in := []string{"docs/data-plane.md", "docs/brand/index.md", "internal/mcpserver/AGENTS.md",
		"internal/skillbundle/bundled/cartographer-ops/SKILL.md", ".claude/agents/cartographer-contract-reviewer.md",
		"README.md", "CONTRIBUTING.md", "AGENTS.md", ".claude/skills/cartographer-implement-plan/SKILL.md"}
	out := []string{"docs/decisions/D206-d163-was-a-copied-off-by-one-not-a-missing-record.md", "docs/decisions.md",
		"CHANGELOG.md", "deploy/demo/README.md", "test/e2e/README.md"}
	for _, p := range in {
		if !inRotScope(p) {
			t.Errorf("%s should be in scope", p)
		}
	}
	for _, p := range out {
		if inRotScope(p) {
			t.Errorf("%s should be out of scope", p)
		}
	}
}

func rotRules1(text string) []string {
	var out []string
	for _, f := range findRot("x.md", text) {
		out = append(out, fmt.Sprintf("%s@%d", f.rule, f.line))
	}
	return out
}

func TestRotDetector(t *testing.T) {
	fires := map[string]string{
		"used to":         "The two used to be inverses of each other.",
		"previously":      "The server previously accepted this key.",
		"removed by D<n>": "The flag was removed by D12 and is gone.",
		"as of D<n>":      "The list is complete as of D160.",
		"became / gained": "The field became mandatory.",
		"hand-kept count": "There are thirteen review kinds in total.",
	}
	for rule, sentence := range fires {
		for _, text := range []string{sentence, strings.Replace(sentence, " the ", "\nthe ", 1), wrapMiddle(sentence)} {
			got := rotRules1(text)
			if len(got) == 0 || !strings.HasPrefix(got[0], rule+"@") {
				t.Errorf("rule %q should fire on %q, got %v", rule, text, got)
			}
		}
	}
	quiet := []string{
		"A probe can be used to probe it.", "Two clients are two sessions.", "At most 200 findings.",
		"The v0.26 tools are listed.", "A 3-way clients merge.", "It is used to sign.",
		"```\nused to be\n```", "Run `used to` now.", "Run ``a ` used to`` now.",
	}
	for _, text := range quiet {
		if got := rotRules1(text); len(got) != 0 {
			t.Errorf("%q should not fire, got %v", text, got)
		}
	}
	if got := rotRules1("An `unclosed backtick then it used to work."); len(got) != 1 {
		t.Errorf("a hit after an unclosed backtick must fire, got %v", got)
	}
	if got := rotRules1("first line\r\n\r\nsecond\r\nit previously worked\r\n"); len(got) != 1 || got[0] != "previously@4" {
		t.Errorf("line of a hit on the second line of a paragraph (CRLF): %v", got)
	}
}

func wrapMiddle(s string) string {
	i := strings.LastIndex(s[:len(s)/2+3], " ")
	return s[:i] + "\n" + s[i+1:]
}

func TestRotAllowList(t *testing.T) {
	text := "It used to be a key.\n\nThe flag was removed in D5,\nstill rejected.\n\nAn unrelated previously.\n"
	findings := findRot("a.md", text)
	if len(findings) != 3 {
		t.Fatalf("want 3 findings, got %d", len(findings))
	}
	allowed := []rotAllow{
		{"a.md", "removed in D5, still   rejected", "retired flag"},
		{"a.md", "gone from nowhere", "stale"},
		{"a.md", "It used", "x"},
		{"a.md", "It used", "x"},
		{"a.md", "an", " "},
	}
	open, stale, dup, noReason := applyAllow(findings, allowed)
	if len(open) != 1 || open[0].rule != "previously" {
		t.Errorf("open: %+v", open)
	}
	// "It used" covers the first paragraph's finding; "gone from nowhere" covers nothing.
	if len(stale) != 1 || stale[0].Fragment != "gone from nowhere" {
		t.Errorf("stale: %+v", stale)
	}
	if len(dup) != 1 || len(noReason) != 1 {
		t.Errorf("dup %v noReason %v", dup, noReason)
	}
	// Same paragraph, but the fragment sits away from the match: not covered.
	f := findRot("a.md", "It previously worked. Elsewhere the word key stands.")
	open, stale, _, _ = applyAllow(f, []rotAllow{{"a.md", "word key stands", "r"}})
	if len(open) != 1 || len(stale) != 1 {
		t.Errorf("distant fragment must not cover: open %v stale %v", open, stale)
	}
}

func TestCurrentStateDocsCarryNoHistoryNorCounts(t *testing.T) {
	root := repoRoot(t)
	all, err := MarkdownFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, f := range all {
		if inRotScope(f) {
			files = append(files, f)
		}
	}
	have := map[string]bool{}
	for _, f := range files {
		have[f] = true
	}
	for _, must := range []string{"README.md", "CONTRIBUTING.md", "AGENTS.md", "docs/data-plane.md",
		"internal/mcpserver/AGENTS.md", "internal/skillbundle/bundled/cartographer-ops/SKILL.md",
		".claude/skills/cartographer-implement-plan/SKILL.md", ".claude/agents/cartographer-contract-reviewer.md"} {
		if !have[must] {
			t.Errorf("the current-state corpus does not contain %s: the gate would scan nothing it exists to read", must)
		}
	}
	var findings []rotFinding
	for _, f := range files {
		if strings.HasPrefix(f, "docs/decisions/") {
			t.Errorf("a decision file is in the corpus: %s", f)
		}
		findings = append(findings, findRot(f, readFile(t, root, f))...)
	}
	open, stale, dup, noReason := applyAllow(findings, rotAllowed)
	var msgs []string
	for _, f := range open {
		msgs = append(msgs, fmt.Sprintf("%s:%d: %q (%s): %s", f.file, f.line, f.match, f.rule, f.hint))
	}
	if len(open) > 0 {
		msgs = append(msgs, "A current-state page states what is true now. Rewrite each sentence above in the present tense and, if the history matters, move it to docs/decisions/D<n>-<slug>.md (make decisions-new N=<issue or PR> SLUG=… TOPIC=…); for a count, name the members or link the source of truth instead of numbering them. If the sentence documents a retired key, tool or flag a reader can still meet, add it to rotAllowed in internal/repodocs/doc_rot_test.go with the exact fragment and the reason.")
	}
	for _, a := range stale {
		msgs = append(msgs, fmt.Sprintf("rotAllowed entry for %s (%q) covers no finding: the text was reworded or the page fixed — remove the entry", a.File, a.Fragment))
	}
	for _, a := range dup {
		msgs = append(msgs, fmt.Sprintf("rotAllowed has a duplicate entry for %s (%q): merge them", a.File, a.Fragment))
	}
	for _, a := range noReason {
		msgs = append(msgs, fmt.Sprintf("rotAllowed entry for %s (%q) has no fragment or no reason: say why the sentence is still true today", a.File, a.Fragment))
	}
	for _, m := range capped(msgs, 60) {
		t.Error(m)
	}
}
