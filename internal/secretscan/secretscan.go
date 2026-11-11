// Package secretscan finds potential credentials in text: known token formats
// with their real length and charset, plus a PEM private-key header followed by
// key material (D1202). It is a leaf, importable by a client package (which may
// not import lint): the lint check reports a kind and a line from it, and a
// caller that must remove a secret has its byte span.
//
// Trap: a new token format is a row in secretPatterns, never in the lint
// package; the lint test rows and the Find span test then cover it.
package secretscan

import (
	"regexp"
	"strings"
)

type secretPattern struct {
	kind  string
	re    *regexp.Regexp
	extra func(token string) bool // optional extra condition on the whole token
}

// secretRE anchors a token so a longer unrelated string does not match on a
// prefix: it must not be preceded by an alphanumeric (`_`, `-` and markdown
// emphasis may touch it) nor followed by a character of its own body charset
// (so the fixed length holds while `ghp_…-prod` still matches).
func secretRE(charset, body string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|[^A-Za-z0-9])(` + body + `)(?:[^` + charset + `]|$)`)
}

func hasDigit(s string) bool { return strings.ContainsAny(s, "0123456789") }

// Formats follow each vendor's published token format: GitHub (ghp_/gho_/ghs_/
// ghu_ + 36 alnum; github_pat_ + 22 + _ + 59), GitLab (glpat- + 20..64),
// Anthropic (sk-ant-<kind><n>- + 80+), OpenAI (sk-proj- + 40+; legacy sk- + 48
// alnum), AWS access key id (AKIA/ASIA + 16 base32), Slack (xox[abpr]- +
// digits), Google API key (AIza + 35), Stripe (sk_live_/rk_live_ + 24+), age
// (AGE-SECRET-KEY-1 + 58 bech32).
var secretPatterns = []secretPattern{
	{kind: "GitHub token", re: secretRE(`A-Za-z0-9`, `gh[pous]_[A-Za-z0-9]{36}`)},
	{kind: "GitHub token", re: secretRE(`A-Za-z0-9_`, `github_pat_[A-Za-z0-9]{22}_[A-Za-z0-9]{59}`)},
	{kind: "GitLab token", re: secretRE(`A-Za-z0-9_-`, `glpat-[A-Za-z0-9_-]{20,64}`)},
	{kind: "Anthropic API key", re: secretRE(`A-Za-z0-9_-`, `sk-ant-[a-z]+[0-9]+-[A-Za-z0-9_-]{80,}`)},
	{kind: "OpenAI API key", re: secretRE(`A-Za-z0-9_-`, `sk-proj-[A-Za-z0-9_-]{40,}`)},
	{kind: "OpenAI API key", re: secretRE(`A-Za-z0-9`, `sk-[A-Za-z0-9]{48}`)},
	{kind: "AWS access key id", re: secretRE(`A-Za-z0-9`, `(?:AKIA|ASIA)[A-Z2-7]{16}`)},
	{kind: "Slack token", re: secretRE(`A-Za-z0-9-`, `xox[abpr]-[A-Za-z0-9-]{10,}`), extra: func(t string) bool { return hasDigit(t[5:]) }},
	{kind: "Google API key", re: secretRE(`A-Za-z0-9_-`, `AIza[A-Za-z0-9_-]{35}`)},
	{kind: "Stripe key", re: secretRE(`A-Za-z0-9`, `[sr]k_live_[A-Za-z0-9]{24,}`)},
	{kind: "age secret key", re: secretRE(`A-Za-z0-9`, `AGE-SECRET-KEY-1[QPZRY9X8GF2TVDW0S3JN54KHCE6MUA7L]{58}`)},
}

var (
	pemHeaderRE = regexp.MustCompile(`-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----`)
	pemBodyRE   = regexp.MustCompile(`^[A-Za-z0-9+/]{16,}={0,2}$`)
	pemHdrLine  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*:`) // RFC 1421 header: Proc-Type:, DEK-Info:
	xRunRE      = regexp.MustCompile(`x{4,}|X{4,}|\*{4,}`)
)

// placeholderRun reports whether s looks like filler, not key material: one
// repeated character, a run of 4+ x/X/*, or an ellipsis or angle bracket.
func placeholderRun(s string) bool {
	if s == "" {
		return true
	}
	if core := strings.NewReplacer("_", "", "-", "").Replace(s); core == "" || strings.Count(core, core[:1]) == len(core) {
		return true
	}
	return xRunRE.MatchString(s) || strings.ContainsAny(s, "<>…") || strings.Contains(s, "...")
}

// Match is one potential secret. Start and End are byte offsets in the text
// given to Find; text[Start:End] is the token, or for a PEM key the header up
// to the end of the last key-material line (or of the END line when present).
// Kind and Line (1-based) are all a lint finding may show: never echo the span.
type Match struct {
	Kind       string
	Line       int
	Start, End int
}

// Find scans text line by line and returns every match by line, patterns in table order within a line (the order the lint output has always had).
func Find(text string) []Match {
	lines := strings.SplitAfter(text, "\n")
	offs := make([]int, len(lines)+1)
	for i, l := range lines {
		offs[i+1] = offs[i] + len(l)
	}
	var out []Match
	for i, raw := range lines {
		line := strings.TrimSuffix(raw, "\n")
		for _, p := range secretPatterns {
			for _, loc := range p.re.FindAllStringSubmatchIndex(line, -1) {
				tok := line[loc[2]:loc[3]]
				if p.extra != nil && !p.extra(tok) {
					continue
				}
				if placeholderRun(tok[PrefixLen(tok):]) {
					continue
				}
				out = append(out, Match{p.kind, i + 1, offs[i] + loc[2], offs[i] + loc[3]})
			}
		}
		if loc := pemHeaderRE.FindStringIndex(line); loc != nil {
			if end, ok := pemSpan(lines, offs, i); ok {
				out = append(out, Match{"PEM private key", i + 1, offs[i] + loc[0], end})
			}
		}
	}
	return out
}

// pemSpan reports whether the header on lines[h] is followed by key material
// and, if so, where the key ends. Quote and list prefixes (> - *) are stripped;
// RFC 1421 header lines of an encrypted key are skipped.
func pemSpan(lines []string, offs []int, h int) (int, bool) {
	body := func(i int) string {
		return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(lines[i]), ">-* \t"))
	}
	j := h + 1
	for ; j < len(lines); j++ {
		if next := body(j); next != "" && !pemHdrLine.MatchString(next) {
			break
		}
	}
	if j >= len(lines) || !pemBodyRE.MatchString(body(j)) || placeholderRun(body(j)) {
		return 0, false
	}
	end := offs[j] + len(strings.TrimRight(lines[j], "\r\n"))
	for k := j + 1; k < len(lines); k++ {
		switch next := body(k); {
		case pemBodyRE.MatchString(next):
			end = offs[k] + len(strings.TrimRight(lines[k], "\r\n"))
		case strings.Contains(lines[k], "-----END "):
			return offs[k] + len(strings.TrimRight(lines[k], "\r\n")), true
		default:
			return end, true
		}
	}
	return end, true
}

// PrefixLen is the length of the static prefix of a matched token, which the
// placeholder rule skips.
func PrefixLen(tok string) int {
	switch {
	case strings.HasPrefix(tok, "github_pat_"):
		return len("github_pat_")
	case strings.HasPrefix(tok, "glpat-"):
		return len("glpat-")
	case strings.HasPrefix(tok, "sk-ant-"):
		// sk-ant-<letters><digits>-
		if i := strings.Index(tok[len("sk-ant-"):], "-"); i >= 0 {
			return len("sk-ant-") + i + 1
		}
		return len("sk-ant-")
	case strings.HasPrefix(tok, "sk-proj-"):
		return len("sk-proj-")
	case strings.HasPrefix(tok, "sk_live_"), strings.HasPrefix(tok, "rk_live_"):
		return len("sk_live_")
	case strings.HasPrefix(tok, "sk-"):
		return len("sk-")
	case strings.HasPrefix(tok, "AGE-SECRET-KEY-1"):
		return len("AGE-SECRET-KEY-1")
	case strings.HasPrefix(tok, "xox"):
		return len("xoxb-")
	case strings.HasPrefix(tok, "AKIA"), strings.HasPrefix(tok, "ASIA"), strings.HasPrefix(tok, "AIza"):
		return 4
	case strings.HasPrefix(tok, "gh"):
		return 4
	}
	return 0
}
