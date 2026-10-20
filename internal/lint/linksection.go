package lint

import (
	"regexp"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// A trailing links section ("## Collegamenti", "## Links", "## See also") is
// how many KB templates carry a page's links, and for most pages it is the
// only place those links exist: it is not an antipattern in itself, and
// removing it would empty the graph. Two things about it are (D287):
//   - duplicate_link: a link written both in the text and in the section —
//     one of the two is redundant and they drift apart;
//   - bare_link_list: a section of links with not one word on why each one
//     matters — the reader, human or agent, cannot tell a dependency from a
//     loose association.
// Both are problems like any other finding (D947).

var linksSectionHeading = regexp.MustCompile(`(?im)^(#{2,6})\s+(collegamenti|links|related|see also|vedi anche)\s*#*\s*$`)

var (
	wikiLinkRe = regexp.MustCompile(`\[\[[^\]]*\]\]`)
	mdLinkRe   = regexp.MustCompile(`\[(?:[^\[\]]|\[[^\[\]]*\])*\]\([^)]*\)`)
	listMarkRe = regexp.MustCompile(`(?m)^\s*(?:[-*+]|\d+[.)])\s*`)
)

// anyHeading matches the start of an ATX heading line of any level.
var anyHeading = regexp.MustCompile(`(?m)^(#{1,6})\s`)

// linksExtent is where a body's links section lies: byte offsets into the
// body, so a reader slices and a repair splices the same bytes. The section is
// body[headEnd:end]: it ends at the first heading of ANY level after the
// links heading. A `###` under `## Links` is, in practice, a heading demoted
// from `##` to get out of template_extra_section — prose, not part of the
// links — so its links are text and its words are not the section's.
type linksExtent struct {
	headStart, headEnd, end int
	level                   int    // of the links heading
	heading                 string // its text as written, without the #s
	nextLevel               int    // of the heading that ends the section; 0 at the end of the body
}

// locateLinksSection finds the first links heading of body and where its
// section ends. linksSection (the checks) and ReplaceLinkItem (the repair) both
// read it: they share one boundary, so what a finding names is what the repair
// edits.
func locateLinksSection(body string) (linksExtent, bool) {
	loc := linksSectionHeading.FindStringSubmatchIndex(body)
	if loc == nil {
		return linksExtent{}, false
	}
	x := linksExtent{headStart: loc[0], headEnd: loc[1], end: len(body), level: loc[3] - loc[2], heading: body[loc[4]:loc[5]]}
	if m := anyHeading.FindStringSubmatchIndex(body[x.headEnd:]); m != nil {
		x.end = x.headEnd + m[0]
		x.nextLevel = m[3] - m[2]
	}
	return x, true
}

// linksSection splits body into the links section and the rest. ok is false
// when the body has no such section.
func linksSection(body string) (heading, section, rest string, ok bool) {
	x, ok := locateLinksSection(body)
	if !ok {
		return "", "", "", false
	}
	return x.heading, body[x.headEnd:x.end], body[:x.headStart] + body[x.end:], true
}

// linksSectionIssues reports, for one concept body, the links repeated in
// the text and whether the section is a bare list of links.
// fixable maps each duplicate to the fix that takes it out of its item
// (duplicateItemFixes).
func linksSectionIssues(body, linkBase string, assetExists func(string) bool) (heading string, duplicates []okf.ConceptID, fixable map[okf.ConceptID]*Fix, bare bool, count int) {
	heading, section, rest, ok := linksSection(body)
	if !ok {
		return "", nil, nil, false, 0
	}
	listed := kb.ExtractLinks(section, linkBase, assetExists)
	if len(listed) == 0 {
		return heading, nil, nil, false, 0
	}
	inText := map[okf.ConceptID]bool{}
	for _, id := range kb.ExtractLinks(rest, linkBase, assetExists) {
		inText[id] = true
	}
	for _, id := range listed {
		if inText[id] {
			duplicates = append(duplicates, id)
		}
	}
	fixable = duplicateItemFixes(section, linkBase, assetExists, inText)
	words := listMarkRe.ReplaceAllString(mdLinkRe.ReplaceAllString(wikiLinkRe.ReplaceAllString(section, ""), ""), "")
	bare = !strings.ContainsFunc(words, func(r rune) bool {
		return r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r > 127
	})
	return heading, duplicates, fixable, bare, len(listed)
}

// itemSeparator splits a list item into the groups a reader sees as one
// entry each: "[[a]] (successor) · [[b]] · [[c]]".
var itemSeparator = regexp.MustCompile(`\s*[·;|]\s*|,\s+`)

// duplicateItemFixes maps each link the text already carries to the fix that
// takes it out of its list item (D307). The item is split into groups at its
// separators; a group without a link belongs to the link before it (its
// reason). A group whose links are all in the text goes with its words —
// the text says why already. An item left with no link is dropped; one with
// other links keeps them. A group mixing a duplicate with another link is
// left alone: no cut keeps one without the other.
func duplicateItemFixes(section, linkBase string, assetExists func(string) bool, inText map[okf.ConceptID]bool) map[okf.ConceptID]*Fix {
	out := map[okf.ConceptID]*Fix{}
	for _, line := range strings.Split(section, "\n") {
		if !listMarkRe.MatchString(line) {
			continue
		}
		marker := listMarkRe.FindString(line)
		content := line[len(marker):]
		// Separators inside a link ("[[x|Foo, bar]]") are not separators.
		masked := content
		for _, re := range []*regexp.Regexp{wikiLinkRe, mdLinkRe} {
			masked = re.ReplaceAllStringFunc(masked, func(m string) string { return strings.Repeat("x", len(m)) })
		}
		seps := itemSeparator.FindAllStringIndex(masked, -1)
		type group struct {
			text  string
			sep   string // the separator before it
			links []okf.ConceptID
		}
		var groups []group
		start, sep := 0, ""
		for i := 0; i <= len(seps); i++ {
			end := len(content)
			if i < len(seps) {
				end = seps[i][0]
			}
			part := content[start:end]
			links := kb.ExtractLinks(part, linkBase, assetExists)
			if len(links) == 0 && len(groups) > 0 {
				groups[len(groups)-1].text += sep + part // a reason, with the link before it
			} else {
				groups = append(groups, group{text: part, sep: sep, links: links})
			}
			if i < len(seps) {
				sep = content[seps[i][0]:seps[i][1]]
				start = seps[i][1]
			}
		}
		var kept []group
		var dropped []okf.ConceptID
		for _, g := range groups {
			dup := len(g.links) > 0
			for _, id := range g.links {
				dup = dup && inText[id]
			}
			if dup {
				dropped = append(dropped, g.links...)
				continue
			}
			kept = append(kept, g)
		}
		if len(dropped) == 0 {
			continue
		}
		var fix *Fix
		hasLink := false
		for _, g := range kept {
			hasLink = hasLink || len(g.links) > 0
		}
		if !hasLink {
			fix = &Fix{Kind: FixDropLinkItem, Field: line}
		} else {
			var b strings.Builder
			b.WriteString(marker)
			for i, g := range kept {
				if i > 0 {
					s := g.sep
					if s == "" {
						s = " · "
					}
					b.WriteString(s)
				}
				b.WriteString(strings.TrimSpace(g.text))
			}
			fix = &Fix{Kind: FixRewriteLinkItem, Field: line, To: b.String()}
		}
		for _, id := range dropped {
			if _, seen := out[id]; !seen {
				out[id] = fix
			}
		}
	}
	return out
}

// reciprocalLinkItems maps each link-only item of the links section whose
// target links back to self from its text to that item's line (D301): the
// reverse edge stays navigable through backlinks once the item is gone.
// Targets in skip (already duplicate_link) are left to that check.
//
// A back-link that sits only in the target's own links section does not
// count (D309): A and B listing each other only there would both be flagged,
// and repairing both findings drops the edge from the graph. out is the first
// filter — a target that does not link to self at all needs no body read —
// and readBody returns the target's body and link base for the second.
func reciprocalLinkItems(body, linkBase string, self okf.ConceptID, out map[okf.ConceptID]map[okf.ConceptID]struct{}, skip []okf.ConceptID, assetExists func(string) bool, readBody func(okf.ConceptID) (body, linkBase string)) map[okf.ConceptID]string {
	_, section, _, ok := linksSection(body)
	if !ok {
		return nil
	}
	skipped := map[okf.ConceptID]bool{}
	for _, id := range skip {
		skipped[id] = true
	}
	found := map[okf.ConceptID]string{}
	for target, line := range linkOnlyItems(section, linkBase, assetExists) {
		if target == self || skipped[target] {
			continue
		}
		if _, back := out[target][self]; !back {
			continue
		}
		tBody, tBase := readBody(target)
		if bodyLinksOutsideLinkSection(tBody, tBase, assetExists)[self] {
			found[target] = line
		}
	}
	return found
}

// bodyLinksOutsideLinkSection returns the concept IDs body links to outside
// its links section ("## Collegamenti" / "## Links" / "## Related" …).
func bodyLinksOutsideLinkSection(body, linkBase string, assetExists func(string) bool) map[okf.ConceptID]bool {
	if _, _, rest, ok := linksSection(body); ok {
		body = rest
	}
	set := map[okf.ConceptID]bool{}
	for _, id := range kb.ExtractLinks(body, linkBase, assetExists) {
		set[id] = true
	}
	return set
}

// linkOnlyItems maps each concept ID whose list item in the section is a
// single link and nothing else to that item's exact line. A line with two
// links, or with any word beside the link, is never fixable: the words may be
// the reason the link is there.
func linkOnlyItems(section, linkBase string, assetExists func(string) bool) map[okf.ConceptID]string {
	out := map[okf.ConceptID]string{}
	for _, line := range strings.Split(section, "\n") {
		if !listMarkRe.MatchString(line) {
			continue
		}
		stripped := strings.TrimSpace(listMarkRe.ReplaceAllString(line, ""))
		if stripped == "" {
			continue
		}
		noLinks := mdLinkRe.ReplaceAllString(wikiLinkRe.ReplaceAllString(stripped, ""), "")
		if strings.TrimSpace(noLinks) != "" {
			continue
		}
		ids := kb.ExtractLinks(stripped, linkBase, assetExists)
		if len(ids) != 1 || len(wikiLinkRe.FindAllString(stripped, -1))+len(mdLinkRe.FindAllString(stripped, -1)) != 1 {
			continue
		}
		if _, seen := out[ids[0]]; !seen {
			out[ids[0]] = line
		}
	}
	return out
}

// HeadingRequired says whether the page being repaired must keep a heading
// (its level and text as written): the page's template requires that section
// (TemplateRequiresHeading). A nil HeadingRequired requires none.
type HeadingRequired func(level int, heading string) bool

// DropLinkItem removes the exact line from the body's links section (D295
// WP4) and, when the section is left with no list item, its heading too —
// unless required says the heading must stay. Every other byte of the body is
// kept in place. A line no longer present is a no-op.
func DropLinkItem(body, line string, required HeadingRequired) string {
	return ReplaceLinkItem(body, line, "", required)
}

// ReplaceLinkItem replaces the exact line in the body's links section with
// newLine, or removes it when newLine is "" — and then, when the section is
// left with no list item, its heading too. The heading stays when a deeper
// heading follows (the section still has children, and removing their parent
// would re-parent them under the previous heading) and when required asks for
// it: a template that requires the section would report it missing the next
// lint. required is asked only when the heading would otherwise go. A line no
// longer present is a no-op.
func ReplaceLinkItem(body, line, newLine string, required HeadingRequired) string {
	x, ok := locateLinksSection(body)
	if !ok {
		return body
	}
	section := body[x.headEnd:x.end]
	lines := strings.Split(section, "\n")
	kept := lines[:0]
	removed := false
	for _, l := range lines {
		if !removed && l == line {
			removed = true
			if newLine != "" {
				kept = append(kept, newLine)
			}
			continue
		}
		kept = append(kept, l)
	}
	if !removed {
		return body
	}
	newSection := strings.Join(kept, "\n")
	hasItem := false
	for _, l := range kept {
		if listMarkRe.MatchString(l) && strings.TrimSpace(listMarkRe.ReplaceAllString(l, "")) != "" {
			hasItem = true
			break
		}
	}
	if hasItem || strings.TrimSpace(newSection) != "" {
		return body[:x.headEnd] + newSection + body[x.end:]
	}
	if x.nextLevel > x.level || required != nil && required(x.level, x.heading) {
		// Nothing of its own is left, but the heading stays: one blank line
		// before whatever follows it.
		head := strings.TrimRight(body[:x.headEnd], " \t\r\n") + "\n"
		if x.end < len(body) {
			head += "\n"
		}
		return head + body[x.end:]
	}
	// Heading and empty section go; keep the following content.
	return strings.TrimRight(body[:x.headStart], "\n") + "\n" + strings.TrimLeft(body[x.end:], "\n")
}
