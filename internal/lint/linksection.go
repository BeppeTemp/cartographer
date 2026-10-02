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
// Both are info: advice, never a gate.

var linksSectionHeading = regexp.MustCompile(`(?im)^(#{2,6})\s+(collegamenti|links|related|see also|vedi anche)\s*#*\s*$`)

var (
	wikiLinkRe = regexp.MustCompile(`\[\[[^\]]*\]\]`)
	mdLinkRe   = regexp.MustCompile(`\[[^\]]*\]\([^)]*\)`)
	listMarkRe = regexp.MustCompile(`(?m)^\s*(?:[-*+]|\d+[.)])\s*`)
)

// linksSection splits body into the links section and the rest. ok is false
// when the body has no such section.
func linksSection(body string) (heading, section, rest string, ok bool) {
	loc := linksSectionHeading.FindStringSubmatchIndex(body)
	if loc == nil {
		return "", "", "", false
	}
	level := loc[3] - loc[2]
	heading = body[loc[4]:loc[5]]
	after := body[loc[1]:]
	end := len(after)
	next := regexp.MustCompile(`(?m)^#{1,` + string(rune('0'+level)) + `}\s`)
	if m := next.FindStringIndex(after); m != nil {
		end = m[0]
	}
	return heading, after[:end], body[:loc[0]] + after[end:], true
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
// target links back to self anywhere in its body to that item's line (D301):
// the reverse edge stays navigable through backlinks once the item is gone.
// Targets in skip (already duplicate_link) are left to that check.
func reciprocalLinkItems(body, linkBase string, self okf.ConceptID, out map[okf.ConceptID]map[okf.ConceptID]struct{}, skip []okf.ConceptID, assetExists func(string) bool) map[okf.ConceptID]string {
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
		if _, back := out[target][self]; back {
			found[target] = line
		}
	}
	return found
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

// DropLinkItem removes the exact line from the body's links section (D295
// WP4) and, when the section is left with no list item, its heading too. Every
// other byte of the body is kept in place. A line no longer present is a no-op.
func DropLinkItem(body, line string) string { return ReplaceLinkItem(body, line, "") }

// ReplaceLinkItem replaces the exact line in the body's links section with
// newLine, or removes it when newLine is "" — and then, when the section is
// left with no list item, its heading too. A line no longer present is a no-op.
func ReplaceLinkItem(body, line, newLine string) string {
	loc := linksSectionHeading.FindStringSubmatchIndex(body)
	if loc == nil {
		return body
	}
	level := loc[3] - loc[2]
	after := body[loc[1]:]
	end := len(after)
	next := regexp.MustCompile(`(?m)^#{1,` + string(rune('0'+level)) + `}\s`)
	if m := next.FindStringIndex(after); m != nil {
		end = m[0]
	}
	section := after[:end]
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
	if !hasItem && strings.TrimSpace(newSection) == "" {
		// Heading and empty section go; keep the following content.
		return strings.TrimRight(body[:loc[0]], "\n") + "\n" + strings.TrimLeft(after[end:], "\n")
	}
	return body[:loc[1]] + newSection + after[end:]
}
