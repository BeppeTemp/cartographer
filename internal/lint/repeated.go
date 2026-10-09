package lint

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// repeated_link (info, D357): the same target linked more than once in one
// paragraph. duplicate_link only covers a link the text already carries and
// the "See also" section repeats; two links to one target inside a paragraph
// are noise a reader meets as a wall of blue, and nothing reported them.

// inlineLinkRe matches an inline markdown link or image: group 1 is "!" for an
// image, 2 the label, 3 the href.
var inlineLinkRe = regexp.MustCompile(`(!?)\[([^\[\]]*)\]\(([^()\s]+)(?:\s+"[^"]*")?\)`)

var listItemRe = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s`)

// linkOcc is one inline link of a paragraph: its byte span in the body, label
// and href.
type linkOcc struct {
	start, end  int
	label, href string
}

// paragraphLinks returns, per paragraph (a run of lines with no blank line;
// every list item and heading is its own), the inline links outside code. A
// fenced block and a code span hold none.
func paragraphLinks(body string) [][]linkOcc {
	masked := kb.MaskCodeSpans(body)
	var out [][]linkOcc
	var cur []linkOcc
	flush := func() {
		if len(cur) > 0 {
			out = append(out, cur)
		}
		cur = nil
	}
	fence := ""
	off := 0
	for _, line := range strings.SplitAfter(body, "\n") {
		start := off
		off += len(line)
		t := strings.TrimSpace(line)
		if fence == "" {
			if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
				flush()
				fence = t[:3]
				continue
			}
		} else {
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			continue
		}
		if t == "" || strings.HasPrefix(t, "#") {
			flush()
			continue
		}
		if listItemRe.MatchString(line) {
			flush()
		}
		for _, m := range inlineLinkRe.FindAllStringSubmatchIndex(masked[start:start+len(line)], -1) {
			if masked[start+m[2]:start+m[3]] == "!" {
				continue // an image is not a link to read past
			}
			label := body[start+m[4] : start+m[5]]
			if strings.TrimSpace(label) == "" {
				continue // nothing to leave in its place
			}
			cur = append(cur, linkOcc{start: start + m[0], end: start + m[1], label: label, href: body[start+m[6] : start+m[7]]})
		}
	}
	flush()
	return out
}

// repeatedLinkFindings reports, once per target, a link repeated inside one
// paragraph. The fix unlinks the later occurrences wherever they are.
func repeatedLinkFindings(relPath, body string) []Finding {
	if !strings.Contains(body, "](") {
		return nil
	}
	var order []string
	extra := map[string]int{}
	for _, para := range paragraphLinks(body) {
		seen := map[string]bool{}
		for _, o := range para {
			if seen[o.href] {
				if extra[o.href] == 0 {
					order = append(order, o.href)
				}
				extra[o.href]++
			}
			seen[o.href] = true
		}
	}
	var out []Finding
	for _, href := range order {
		out = append(out, newFinding("repeated_link", Finding{
			Path:    relPath,
			Message: fmt.Sprintf("%s is linked %d more time(s) in a paragraph that already links it: leave the first link, write the others as plain text", href, extra[href]),
			Fix:     &Fix{Kind: FixUnlinkRepeat, Field: href},
		}))
	}
	return out
}

// UnlinkRepeats rewrites, in every paragraph, the occurrences of href after
// the first as their label text (unlink_repeat). It reports whether anything
// changed, so a second run is a no-op.
func UnlinkRepeats(body, href string) (string, bool) {
	type cut struct {
		start, end int
		label      string
	}
	var cuts []cut
	for _, para := range paragraphLinks(body) {
		first := true
		for _, o := range para {
			if o.href != href {
				continue
			}
			if first {
				first = false
				continue
			}
			cuts = append(cuts, cut{o.start, o.end, o.label})
		}
	}
	if len(cuts) == 0 {
		return body, false
	}
	var sb strings.Builder
	last := 0
	for _, c := range cuts {
		sb.WriteString(body[last:c.start])
		sb.WriteString(c.label)
		last = c.end
	}
	sb.WriteString(body[last:])
	return sb.String(), true
}
