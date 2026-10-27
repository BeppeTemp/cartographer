package lint

import (
	"regexp"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// externalOrigin reports whether the page is marked as a verbatim external
// document (frontmatter `origin: external`, #1195 / D1195). The content checks
// that judge the KB's own writing (open_marker, machine_path, title_quality)
// skip such a page: its words are not the KB's to edit. Structural checks
// still apply. Any other value of origin means nothing.
func externalOrigin(fm *okf.Frontmatter) bool {
	if fm == nil {
		return false
	}
	s, _ := frontmatterValue(fm, "origin").(string)
	return strings.EqualFold(strings.TrimSpace(s), "external")
}

// sshHostRe: the word ssh followed by a non-option token (a host), possibly
// inside backticks or parentheses. A heuristic by design (D1195): "ssh to"
// in prose also matches, which only ever hides a finding, never invents one.
var sshHostRe = regexp.MustCompile("(?:^|[\\s`(])ssh\\s+[^\\s\\-`]")

// hostPromptRe: a shell prompt that carries a hostname, `user@host:…$ `,
// `user@host$ `, `[user@host dir]$ `, `root@host:…# `. A bare `$ ` is not one.
var hostPromptRe = regexp.MustCompile(`^(?:\[[\w.-]+@[\w.-]+[^\]]*\]|[\w.-]+@[\w.-]+(?::\S*?)?)[$#](?:\s|$)`)

func namesSSHHost(line string) bool {
	return strings.Contains(line, "ssh") && sshHostRe.MatchString(line)
}

// remoteBlockStart reports whether a fenced block whose first non-blank line
// is first runs on a remote host.
func remoteBlockStart(first string) bool {
	first = strings.TrimSpace(first)
	if strings.HasPrefix(first, "$ ") || strings.HasPrefix(first, "# ") {
		first = strings.TrimSpace(first[2:])
	}
	if strings.HasPrefix(first, "ssh ") && namesSSHHost(first) {
		return true
	}
	return strings.Contains(first, "@") && hostPromptRe.MatchString(first)
}

// remoteFenceSpans returns the [start,end) byte spans of the fenced code
// blocks that are remote: the nearest non-blank line before the opening fence
// names `ssh <host>`, or the block opens with an `ssh <host>` line or a
// prompt carrying a hostname (D1195).
func remoteFenceSpans(body string) [][]int {
	if !strings.Contains(body, "```") && !strings.Contains(body, "~~~") {
		return nil
	}
	var spans [][]int
	var (
		open     bool
		fenceCh  byte
		fenceLen int
		start    int
		remote   bool
		sawFirst bool
		last     string // last non-blank line outside a fence
	)
	closeAt := func(end int) {
		if remote {
			spans = append(spans, []int{start, end})
		}
		open = false
	}
	pos := 0
	for pos <= len(body) {
		nl := strings.IndexByte(body[pos:], '\n')
		end := len(body)
		if nl >= 0 {
			end = pos + nl
		}
		line := body[pos:end]
		trim := strings.TrimLeft(line, " ")
		indentOK := len(line)-len(trim) <= 3
		ch, n := fenceRun(trim)
		switch {
		case open:
			if indentOK && ch == fenceCh && n >= fenceLen && strings.TrimSpace(trim[n:]) == "" {
				closeAt(pos)
			} else if !sawFirst && strings.TrimSpace(line) != "" {
				sawFirst = true
				remote = remote || remoteBlockStart(line)
			}
		case indentOK && n >= 3 && !(ch == '`' && strings.Contains(trim[n:], "`")):
			open, fenceCh, fenceLen = true, ch, n
			start, sawFirst = end, false
			remote = namesSSHHost(last)
			last = ""
		default:
			if strings.TrimSpace(line) != "" {
				last = line
			}
		}
		if nl < 0 {
			break
		}
		pos = end + 1
	}
	if open {
		closeAt(len(body))
	}
	return spans
}

// fenceRun is the fence character and run length at the start of s, 0 if none.
func fenceRun(s string) (byte, int) {
	if s == "" || (s[0] != '`' && s[0] != '~') {
		return 0, 0
	}
	n := 0
	for n < len(s) && s[n] == s[0] {
		n++
	}
	return s[0], n
}
