package docskb

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// The link rules of the generated KB. Links are rewritten the way the data
// plane reads them (docs/data-plane.md §Cross-links and the graph): the same
// pattern as the graph's, code spans and fenced blocks left alone, a title after
// the destination dropped, `|` and `]` kept out of a label.
//
//	a page of the KB            -> [[id|label]], an anchor resolved to the unit holding the heading
//	anything else in the repo   -> the pinned GitHub URL (blob, tree for a trailing slash)
//	an external URL             -> unchanged
//	D<n> in prose               -> one appended "Decisions cited:" line per unit

var (
	mdLinkRe    = regexp.MustCompile(`\[((?:[^\[\]]|\[[^\[\]]*\])*)\]\(([^)]+)\)`)
	wikiLinkRe  = regexp.MustCompile(`\[\[[^\[\]]*\]\]`)
	listItemRe  = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s`)
	decisionNum = regexp.MustCompile(`\bD(\d+)\b`)
	releaseRe   = regexp.MustCompile(`^v\d+\.\d+\.\d+(-rc\.\d+)?$`)
	schemeRe    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
)

const (
	citedMax     = 12
	citedPrefix  = "Decisions cited: "
	registryNote = "cited in the documentation as an example of the placeholder syntax"
)

// Ref is the git ref the pinned URLs point at: the version tag when the build's
// version is exactly a release or a release candidate, else main (a git describe
// string and "dev" are no tag anyone can open).
func Ref(version string) string {
	if releaseRe.MatchString(version) {
		return version
	}
	return "main"
}

// target is a source file that became a concept: its id and the concept that
// holds each of its heading anchors.
type target struct {
	id      string
	anchors map[string]string
}

// rewrite applies the link rules to the lines of a unit cut from src. text is
// the whole source, to give an error its line. It returns the new lines and the
// decision concepts the unit now links, which its citation line leaves out.
func (g *gen) rewrite(src, text string, lines []string) ([]string, map[string]bool) {
	body := join(lines)
	masked := strings.Split(kb.MaskCodeSpans(body), "\n")
	kinds := kb.FenceKinds(body)
	linked := map[string]bool{}
	seen := map[string]bool{} // targets already linked in this paragraph
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = line
		t := strings.TrimSpace(line)
		switch {
		case kinds[i] == kb.LineFenceOpen:
			seen = map[string]bool{}
		case kinds[i] != kb.LineText:
			continue
		case t == "" || strings.HasPrefix(t, "#") || listItemRe.MatchString(line):
			seen = map[string]bool{}
		}
		if kinds[i] != kb.LineText {
			continue
		}
		// Wiki-link syntax in the source prose is documentation of it, not a
		// link of this KB: showing it in a code span keeps the graph from
		// reading it as one.
		if w := wikiLinkRe.FindAllStringIndex(masked[i], -1); w != nil {
			var wb strings.Builder
			prev := 0
			for _, x := range w {
				wb.WriteString(line[prev:x[0]] + "`" + line[x[0]:x[1]] + "`")
				prev = x[1]
			}
			line = wb.String() + line[prev:]
			out[i], masked[i] = line, kb.MaskCodeSpans(line)
		}
		if !strings.Contains(masked[i], "](") {
			continue
		}
		var b strings.Builder
		last := 0
		for _, m := range mdLinkRe.FindAllStringSubmatchIndex(masked[i], -1) {
			whole, lab, dest := line[m[0]:m[1]], line[m[2]:m[3]], line[m[4]:m[5]]
			image := m[0] > 0 && line[m[0]-1] == '!'
			repl, key, dec, ok := g.linkTo(src, text, whole, lab, dest, image)
			if !ok {
				continue
			}
			if key != "" && !image {
				if seen[key] {
					repl = lab
				}
				seen[key] = true
			}
			if dec != "" {
				linked[dec] = true
			}
			b.WriteString(line[last:m[0]])
			b.WriteString(repl)
			last = m[1]
		}
		b.WriteString(line[last:])
		out[i] = b.String()
	}
	return out, linked
}

// linkTo is the replacement of one markdown link. ok is false for a link left
// as written (an external URL). key identifies the target for the repeated-link
// rule; dec is the decision concept the link points at, if it does.
func (g *gen) linkTo(src, text, whole, lab, dest string, image bool) (repl, key, dec string, ok bool) {
	d := strings.TrimSpace(dest)
	if strings.HasPrefix(d, "<") {
		if j := strings.Index(d, ">"); j > 0 {
			d = d[1:j]
		}
	} else if f := strings.Fields(d); len(f) > 0 {
		d = f[0] // a title after the destination is dropped
	}
	if d == "" || strings.HasPrefix(d, "//") || schemeRe.MatchString(d) {
		return "", "", "", false
	}
	file, frag, _ := strings.Cut(d, "#")
	file, _, _ = strings.Cut(file, "?")
	p := src
	if file != "" {
		p = path.Join(path.Dir(src), file)
		if strings.HasPrefix(file, "/") {
			p = strings.TrimLeft(file, "/")
		}
	}
	p = path.Clean(p)
	if p == ".." || strings.HasPrefix(p, "../") {
		g.fail(src, text, whole, "leaves the repository")
		return "", "", "", false
	}
	if tg, found := g.targets[p]; found && !image {
		id := tg.id
		if frag != "" {
			if id, found = tg.anchors[frag]; !found {
				g.fail(src, text, whole, "no heading #"+frag+" in "+p)
				return "", "", "", false
			}
		}
		if strings.HasPrefix(id, decisionMap+"/") {
			dec = id
		}
		return link(id, lab), id, dec, true
	}
	kind := "blob/"
	if strings.HasSuffix(file, "/") || p == "." {
		kind = "tree/"
	}
	url := strings.TrimSuffix(repoURL, "blob/") + kind + g.ref
	if p != "." {
		url += "/" + p
	}
	if frag != "" {
		url += "#" + frag
	}
	repl = "[" + lab + "](" + url + ")"
	if image {
		return repl, "", "", true
	}
	return repl, url, "", true
}

func (g *gen) fail(src, text, whole, why string) {
	line := 1
	if i := strings.Index(text, whole); i >= 0 {
		line += strings.Count(text[:i], "\n")
	}
	g.errs = append(g.errs, fmt.Errorf("%s:%d: link %s %s", src, line, whole, why))
}

// cited is the "Decisions cited:" line of a unit whose text, after the rewrite,
// is lines: every D<n> of its prose that has a decision file, ascending, without
// the unit's own decision and the ones it already links. "" when there is none.
func (g *gen) cited(lines []string, self string, linked map[string]bool) string {
	text := kb.MaskCodeSpans(join(lines))
	text = wikiLinkRe.ReplaceAllString(text, " ")
	text = mdLinkRe.ReplaceAllString(text, " ")
	var nums []int
	have := map[int]bool{}
	for _, m := range decisionNum.FindAllStringSubmatch(text, -1) {
		n, _ := strconv.Atoi(m[1])
		id, ok := g.byNum[n]
		if !ok || id == self || linked[id] || have[n] {
			continue
		}
		have[n] = true
		nums = append(nums, n)
	}
	if len(nums) == 0 {
		return ""
	}
	sort.Ints(nums)
	if len(nums) > citedMax {
		nums = nums[:citedMax]
	}
	var parts []string
	for _, n := range nums {
		parts = append(parts, link(g.byNum[n], "D"+strconv.Itoa(n)))
	}
	return citedPrefix + strings.Join(parts, ", ")
}

// registry is paths.yaml: the documentation shows placeholders of the path
// syntax as examples, and a KB that cites any declares them. Every key the
// generated concepts cite is declared, so a page added to the docs needs no edit
// here.
func (g *gen) registry() {
	ids := map[string]bool{}
	for name, b := range g.out {
		if !strings.HasPrefix(name, "data/") {
			continue
		}
		_, body, _ := okf.SplitFrontmatter(string(b))
		for _, id := range okf.Placeholders(body) {
			ids[id] = true
		}
	}
	if len(ids) == 0 {
		return
	}
	sorted := make([]string, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	var paths, repos []string
	for _, id := range sorted {
		kind, key, _ := okf.SplitPlaceholderID(id)
		line := fmt.Sprintf("  %s: {description: %s}", key, registryNote)
		if kind == "repo" {
			repos = append(repos, line)
		} else {
			paths = append(paths, line)
		}
	}
	var b strings.Builder
	for _, s := range []struct {
		name  string
		lines []string
	}{{"paths", paths}, {"repos", repos}} {
		if len(s.lines) > 0 {
			b.WriteString(s.name + ":\n" + strings.Join(s.lines, "\n") + "\n")
		}
	}
	g.out[kb.PathRegistryFile] = []byte(b.String())
}
