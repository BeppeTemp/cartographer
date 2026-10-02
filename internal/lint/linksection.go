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
// fixable is the subset of duplicates whose list item is link-only (no word):
// mechanical removal is safe.
func linksSectionIssues(body, linkBase string, assetExists func(string) bool) (heading string, duplicates []okf.ConceptID, fixable map[okf.ConceptID]string, bare bool, count int) {
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
	// Per-item link-only test: check each list item in the section.
	itemLinkOnly := linkOnlyItems(section, linkBase, assetExists)
	fixable = map[okf.ConceptID]string{}
	for _, id := range listed {
		if inText[id] {
			duplicates = append(duplicates, id)
			if line, ok := itemLinkOnly[id]; ok {
				fixable[id] = line
			}
		}
	}
	words := listMarkRe.ReplaceAllString(mdLinkRe.ReplaceAllString(wikiLinkRe.ReplaceAllString(section, ""), ""), "")
	bare = !strings.ContainsFunc(words, func(r rune) bool {
		return r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r > 127
	})
	return heading, duplicates, fixable, bare, len(listed)
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
func DropLinkItem(body, line string) string {
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
