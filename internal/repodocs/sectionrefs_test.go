package repodocs

// Section references and fragment links (D1124, extending D208).
//
// A trap for whoever edits this file: the corpus these gates read includes this
// very file, so an example of a citation written here is a citation. Build the
// page token and the section sign from separate pieces (see `sec` and the cases
// in TestMatchesHeading), or write the page as a placeholder in angle brackets,
// which the page-token class does not match.

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// sec is the section sign. Examples in this file are spelled "page.md " + sec +
// "Heading" so the source never holds a literal citation.
const sec = "§"

// sectionRefAllow lists citations that are not a pointer into the page they
// name. Key: "<citing file>: <normalised cited text>"; value: the reason, which
// is mandatory. An entry whose citation now resolves, or is gone, fails (the
// GapDecisions pattern, D206). Nothing is listed to make a first run green:
// residual drift is fixed in the text.
var sectionRefAllow = map[string]string{}

// sectionRef is one "<page> §<text>" citation found on a line.
type sectionRef struct {
	page string // as written
	text string // as written, cut at the end of the heading name
}

var sectionRefHead = regexp.MustCompile("([A-Za-z0-9_./-]+\\.md)[`)*\\]]*[ \\t]*" + sec)

// extractSectionRefs returns every citation on one line. A heading name ends at
// the first of ; , ( ) ] | → : " or a backtick, at a full stop followed by a blank
// or by the end of the line, at the words "and" and "or", at "+" and "—", or at
// the next section sign; when that sign follows (after such a connector) the next
// name is another citation of the same page. A name quoted in backticks is the
// quoted span.
func extractSectionRefs(line string) []sectionRef {
	var out []sectionRef
	for _, loc := range sectionRefHead.FindAllStringSubmatchIndex(line, -1) {
		page := line[loc[2]:loc[3]]
		rest := line[loc[1]:]
		for {
			var text string
			if strings.HasPrefix(rest, "`") {
				end := strings.IndexByte(rest[1:], '`')
				if end < 0 {
					text, rest = rest[1:], ""
				} else {
					text, rest = rest[1:1+end], rest[end+2:]
				}
			} else {
				cut := headingNameEnd(rest)
				text, rest = rest[:cut], rest[cut:]
			}
			if strings.TrimSpace(text) != "" {
				out = append(out, sectionRef{page: page, text: text})
			}
			next := strings.TrimLeft(rest, " \t,;+")
			next = strings.TrimPrefix(strings.TrimPrefix(next, "and "), "or ")
			next = strings.TrimLeft(next, " \t")
			if !strings.HasPrefix(next, sec) {
				break
			}
			rest = next[len(sec):]
		}
	}
	return out
}

// headingNameEnd returns the byte offset at which a cited heading name stops.
func headingNameEnd(s string) int {
	end := len(s)
	for i, r := range s {
		switch r {
		case ';', ',', '(', ')', ']', '|', '→', ':', '"', '`', '+', '—', '§':
			end = i
		case '.':
			if i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\t' {
				end = i
			}
		}
		if end != len(s) {
			break
		}
	}
	for _, w := range []string{" and ", " or "} {
		if i := strings.Index(s[:end], w); i >= 0 {
			end = i
		}
	}
	return end
}

var (
	mdLinkText = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	blanks     = regexp.MustCompile(`\s+`)
)

// normalizeHeading lower-cases, drops backticks and "*", reduces markdown links
// to their text, collapses blanks and trims trailing punctuation.
func normalizeHeading(s string) string {
	s = mdLinkText.ReplaceAllString(s, "$1")
	s = strings.NewReplacer("`", "", "*", "").Replace(s)
	s = strings.ToLower(blanks.ReplaceAllString(s, " "))
	return strings.TrimRight(strings.TrimSpace(s), " .,;:!?")
}

// matchesHeading reports whether the cited text (normalised) matches a heading
// (normalised): equal, or one is a prefix of the other ending at a word boundary.
func matchesHeading(cited, heading string) bool {
	if cited == "" || heading == "" {
		return false
	}
	if cited == heading {
		return true
	}
	return wordPrefix(cited, heading) || wordPrefix(heading, cited)
}

func wordPrefix(prefix, s string) bool {
	if !strings.HasPrefix(s, prefix) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s[len(prefix):])
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

var (
	atxHeading = regexp.MustCompile(`^ {0,3}#{1,6}[ \t]+(.*?)(?:[ \t]+#+)?[ \t]*$`)
	fenceOpen  = regexp.MustCompile("^ {0,3}(```|~~~)")
)

// pageHeadings returns the raw text of every ATX heading outside fenced code.
func pageHeadings(text string) []string {
	var out []string
	fenced := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if fenceOpen.MatchString(line) {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if m := atxHeading.FindStringSubmatch(line); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}

// githubSlugs and mkdocsSlugs give the anchor of each heading under the rule of
// GitHub and of Python-Markdown's toc extension (what mkdocs uses). They differ
// on runs of "-" and blanks and on duplicates ("-1" against "_1"), and one file
// is read on both, so a fragment may match either.
func githubSlugs(headings []string) []string {
	used := map[string]int{}
	var out []string
	for _, h := range headings {
		s := slugBase(h)
		var b strings.Builder
		for _, r := range strings.ToLower(s) {
			switch {
			case r == ' ':
				b.WriteByte('-')
			case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
				b.WriteRune(r)
			}
		}
		out = append(out, uniqueSlug(b.String(), used, "-"))
	}
	return out
}

func mkdocsSlugs(headings []string) []string {
	used := map[string]int{}
	var out []string
	for _, h := range headings {
		var b strings.Builder
		for _, r := range slugBase(h) {
			if r < 128 && (r == '_' || r == '-' || r == ' ' || r == '\t' ||
				(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
				b.WriteRune(r)
			}
		}
		s := strings.ToLower(strings.TrimSpace(b.String()))
		s = regexp.MustCompile(`[-\s]+`).ReplaceAllString(s, "-")
		out = append(out, uniqueSlug(s, used, "_"))
	}
	return out
}

// slugBase reduces a heading to its rendered text: links to their text, no
// backticks or emphasis marks.
func slugBase(h string) string {
	h = mdLinkText.ReplaceAllString(h, "$1")
	return strings.NewReplacer("`", "", "*", "").Replace(h)
}

func uniqueSlug(s string, used map[string]int, sep string) string {
	if used[s] == 0 {
		used[s] = 1
		return s
	}
	for n := used[s]; ; n++ {
		cand := fmt.Sprintf("%s%s%d", s, sep, n)
		if used[cand] == 0 {
			used[s] = n + 1
			used[cand] = 1
			return cand
		}
	}
}

var htmlAnchor = regexp.MustCompile(`<a\s+(?:id|name)="([^"]+)"`)

// pageAnchors returns every anchor a fragment may name on a page.
func pageAnchors(text string) map[string]bool {
	hs := pageHeadings(text)
	out := map[string]bool{}
	for _, s := range githubSlugs(hs) {
		out[s] = true
	}
	for _, s := range mkdocsSlugs(hs) {
		out[s] = true
	}
	for _, m := range htmlAnchor.FindAllStringSubmatch(text, -1) {
		out[m[1]] = true
	}
	return out
}

func TestMatchesHeading(t *testing.T) {
	cases := []struct {
		name, cited, heading string
		want                 bool
	}{
		{"equal", "Check catalogue", "Check catalogue", true},
		{"cited is a word prefix", "Check catalogue", "Check catalogue (generated)", true},
		{"heading is a word prefix of the prose tail", "Documentation maintenance rules updated in the same session", "Documentation maintenance rules", true},
		{"not at a word boundary", "Hook", "Hooks and agents", false},
		{"unrelated", "Upgrading", "Upgrade", false},
		{"case and backticks", "`Foo` Bar", "foo bar", true},
		{"link reduced to its text", "Foo bar", "[Foo](x.md) bar", true},
	}
	for _, c := range cases {
		if got := matchesHeading(normalizeHeading(c.cited), normalizeHeading(c.heading)); got != c.want {
			t.Errorf("%s: matchesHeading(%q, %q) = %v, want %v", c.name, c.cited, c.heading, got, c.want)
		}
	}
}

func TestExtractSectionRefs(t *testing.T) {
	p := "docs/index.md"
	cases := []struct {
		name, line string
		want       []string
	}{
		{"plain, ends at a comma", "see " + p + " " + sec + "Check catalogue, then", []string{"Check catalogue"}},
		{"backticked", "see `" + p + "` " + sec + "`Real forges` here", []string{"Real forges"}},
		{"parenthesised page", "(" + p + " " + sec + "Foo bar)", []string{"Foo bar"}},
		{"and splits two", p + " " + sec + "Git write coverage and " + sec + "Real forges", []string{"Git write coverage", "Real forges"}},
		{"two citations on a line", p + " " + sec + "One; other.md " + sec + "Two.", []string{"One", "Two"}},
		{"prose after and is not a citation", p + " " + sec + "Hooks and agents", []string{"Hooks"}},
		{"full stop at the end", p + " " + sec + "Last words.", []string{"Last words"}},
		{"no page token", "bare " + sec + "Heading", nil},
		{"placeholder page", "<page>.md " + sec + "Heading", nil},
	}
	for _, c := range cases {
		var got []string
		for _, r := range extractSectionRefs(c.line) {
			got = append(got, r.text)
		}
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s: extractSectionRefs(%q) = %q, want %q", c.name, c.line, got, c.want)
		}
	}
}

func TestSlugs(t *testing.T) {
	hs := []string{"Kind — provider matrix", "Setup", "Setup", "`Code` & more", "Plain `x` heading"}
	gh := githubSlugs(hs)
	md := mkdocsSlugs(hs)
	wantGH := []string{"kind--provider-matrix", "setup", "setup-1", "code--more", "plain-x-heading"}
	wantMD := []string{"kind-provider-matrix", "setup", "setup_1", "code-more", "plain-x-heading"}
	for i := range hs {
		if gh[i] != wantGH[i] {
			t.Errorf("github slug of %q = %q, want %q", hs[i], gh[i], wantGH[i])
		}
		if md[i] != wantMD[i] {
			t.Errorf("mkdocs slug of %q = %q, want %q", hs[i], md[i], wantMD[i])
		}
	}
}

func TestPageHeadingsSkipFences(t *testing.T) {
	text := "# One\n\n```\n# not a heading\n```\n\n## Two ##\n"
	got := pageHeadings(text)
	if len(got) != 2 || got[0] != "One" || got[1] != "Two" {
		t.Errorf("pageHeadings = %q, want [One Two]", got)
	}
}

// Renaming a heading leaves every "<page> §Heading" pointer to it dangling, in
// prose, Go and shell comments and the shipped skills alike, and nothing else
// reports it: the page-path check stops at the page.
func TestEverySectionReferenceResolves(t *testing.T) {
	root := repoRoot(t)

	mds, err := MarkdownFiles(root)
	if err != nil {
		t.Fatalf("collecting the markdown files: %v", err)
	}
	byBase := map[string][]string{}
	isMD := map[string]bool{}
	for _, f := range mds {
		isMD[f] = true
		byBase[path.Base(f)] = append(byBase[path.Base(f)], f)
	}
	headingsOf := map[string][]string{}
	headings := func(rel string) []string {
		h, ok := headingsOf[rel]
		if !ok {
			h = pageHeadings(readFile(t, root, rel))
			headingsOf[rel] = h
		}
		return h
	}

	resolve := func(citing, token string) (string, string) {
		token = strings.TrimPrefix(token, "./")
		for _, c := range []string{token, path.Join(path.Dir(citing), token), "docs/" + token} {
			if isMD[c] {
				return c, ""
			}
		}
		switch cands := byBase[path.Base(token)]; {
		case token == path.Base(token) && len(cands) == 1:
			return cands[0], ""
		case token == path.Base(token) && len(cands) > 1:
			return "", "is ambiguous (" + strings.Join(capped(cands, 3), ", ") + "): write the path from the repository root"
		}
		return "", "cites a page that does not exist"
	}

	files, err := TextFiles(root)
	if err != nil {
		t.Fatalf("collecting the corpus: %v", err)
	}
	checked := 0
	unresolved := map[string]bool{}
	for _, rel := range files {
		if strings.HasPrefix(rel, "docs/decisions/") {
			continue
		}
		for i, line := range strings.Split(readFile(t, root, rel), "\n") {
			for _, ref := range extractSectionRefs(line) {
				checked++
				page, problem := resolve(rel, ref.page)
				if problem != "" {
					t.Errorf("%s:%d %q %s", rel, i+1, ref.page, problem)
					continue
				}
				cited := normalizeHeading(ref.text)
				ok := false
				var sameWord []string
				first, _, _ := strings.Cut(cited, " ")
				for _, h := range headings(page) {
					if matchesHeading(cited, normalizeHeading(h)) {
						ok = true
						break
					}
					if strings.HasPrefix(normalizeHeading(h), first) {
						sameWord = append(sameWord, h)
					}
				}
				if ok {
					continue
				}
				key := rel + ": " + cited
				unresolved[key] = true
				if _, allowed := sectionRefAllow[key]; allowed {
					continue
				}
				t.Errorf("%s:%d cites \"%s %s%s\" but %s has no heading that is, or starts with, %q. "+
					"Headings of %s that start with the same word: %q. Fix the citation, or — if it is not "+
					"a pointer into that page — add %q to sectionRefAllow with the reason.",
					rel, i+1, ref.page, sec, strings.TrimSpace(ref.text), page, cited, page,
					capped(sameWord, 5), key)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no section reference found anywhere: the check is not doing anything")
	}

	var keys []string
	for k := range sectionRefAllow {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch {
		case strings.TrimSpace(sectionRefAllow[k]) == "":
			t.Errorf("sectionRefAllow entry %q has no reason: say why the citation is not a pointer", k)
		case !unresolved[k]:
			t.Errorf("sectionRefAllow entry %q no longer applies (the citation resolves or is gone): "+
				"remove the sectionRefAllow entry", k)
		}
	}
}

// fragmentProblem checks the fragment of one relative link and returns a
// message, or "" when it resolves or is out of scope.
func fragmentProblem(root, citing, target string, texts map[string]string) string {
	file, frag, found := strings.Cut(target, "#")
	if !found || frag == "" {
		return ""
	}
	dest := citing
	if file != "" {
		dest = path.Join(path.Dir(citing), file)
	}
	if path.Ext(dest) != ".md" {
		return ""
	}
	text, ok := texts[dest]
	if !ok {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(dest)))
		if err != nil {
			return "" // a missing file is the file check's to report
		}
		text = string(data)
		texts[dest] = text
	}
	anchors := pageAnchors(text)
	if anchors[frag] {
		return ""
	}
	var near []string
	for a := range anchors {
		if len(frag) >= 3 && strings.HasPrefix(a, frag[:3]) {
			near = append(near, a)
		}
	}
	sort.Strings(near)
	return fmt.Sprintf("%s links to %q: %s has no heading or <a id> that gives that anchor "+
		"(GitHub slug and mkdocs slug both tried). Anchors it does have that start the same way: %q",
		citing, target, dest, capped(near, 5))
}

func TestFragmentChecker(t *testing.T) {
	texts := map[string]string{"a.md": "# Kind — provider matrix\n\n<a id=\"gen\"></a>\n## Setup\n## Setup\n"}
	for _, ok := range []string{"#kind--provider-matrix", "#kind-provider-matrix", "#gen", "#setup", "#setup-1", "#setup_1", "x.png#frag", "#"} {
		if p := fragmentProblem("", "a.md", ok, texts); p != "" {
			t.Errorf("fragment %q should resolve, got %q", ok, p)
		}
	}
	for _, bad := range []string{"#kind-provider", "#setup-2", "#nothing"} {
		if p := fragmentProblem("", "a.md", bad, texts); p == "" {
			t.Errorf("fragment %q should not resolve", bad)
		}
	}
}
