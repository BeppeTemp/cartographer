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
func linksSectionIssues(body, linkBase string, assetExists func(string) bool) (heading string, duplicates []okf.ConceptID, bare bool, count int) {
	heading, section, rest, ok := linksSection(body)
	if !ok {
		return "", nil, false, 0
	}
	listed := kb.ExtractLinks(section, linkBase, assetExists)
	if len(listed) == 0 {
		return heading, nil, false, 0
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
	words := listMarkRe.ReplaceAllString(mdLinkRe.ReplaceAllString(wikiLinkRe.ReplaceAllString(section, ""), ""), "")
	bare = !strings.ContainsFunc(words, func(r rune) bool {
		return r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r > 127
	})
	return heading, duplicates, bare, len(listed)
}
