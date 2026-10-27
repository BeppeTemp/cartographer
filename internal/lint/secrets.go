package lint

import (
	"fmt"
	"regexp"
	"strings"
)

// potential_secret (D1202): a credential pasted into a concept's body or
// frontmatter lands in git history and in every client. The matcher is
// deliberately narrow: known token formats with their real length and charset,
// plus a PEM private-key header followed by key material. No generic
// "password:" heuristic, no entropy matching.
//
// The match is never echoed: secretMatches returns (kind, line) only, never
// the matched substring, and the finding message names the kind and the
// location. Keep it that way: the refusal JSON, the lint output and Atlas
// Health are all built from Finding.Message.
//
// Gate trap (D907): the write gate keys a finding on {path, check, message}
// and the message carries the line number, so an edit that merely shifts a
// pre-existing secret down reads as a newly introduced finding and the write
// is refused. That is wanted (the page holds a secret; the refusal tells the
// author to remove it). Do not "fix" it by dropping the line from the message.

type secretPattern struct {
	kind  string
	re    *regexp.Regexp
	extra func(token string) bool // optional extra condition on the whole token
}

// tokenBoundary keeps a longer unrelated string from matching on a prefix.
const (
	secLead  = `(?:^|[^A-Za-z0-9_-])`
	secTrail = `(?:[^A-Za-z0-9_-]|$)`
)

func secretRE(body string) *regexp.Regexp {
	return regexp.MustCompile(secLead + `(` + body + `)` + secTrail)
}

func hasDigit(s string) bool { return strings.ContainsAny(s, "0123456789") }

// Formats follow each vendor's published token format: GitHub (ghp_/gho_/ghs_/
// ghu_ + 36 alnum; github_pat_ + 22 + _ + 59), GitLab (glpat- + 20..64),
// Anthropic (sk-ant-<kind><n>- + 80+), OpenAI (sk-proj- + 40+; legacy sk- + 48
// alnum), AWS access key id (AKIA/ASIA + 16 base32), Slack (xox[abpr]- +
// digits), Google API key (AIza + 35), Stripe (sk_live_/rk_live_ + 24+), age
// (AGE-SECRET-KEY-1 + 58 bech32).
var secretPatterns = []secretPattern{
	{kind: "GitHub token", re: secretRE(`gh[pous]_[A-Za-z0-9]{36}`)},
	{kind: "GitHub token", re: secretRE(`github_pat_[A-Za-z0-9]{22}_[A-Za-z0-9]{59}`)},
	{kind: "GitLab token", re: secretRE(`glpat-[A-Za-z0-9_-]{20,64}`)},
	{kind: "Anthropic API key", re: secretRE(`sk-ant-[a-z]+[0-9]+-[A-Za-z0-9_-]{80,}`)},
	{kind: "OpenAI API key", re: secretRE(`sk-proj-[A-Za-z0-9_-]{40,}`)},
	{kind: "OpenAI API key", re: secretRE(`sk-[A-Za-z0-9]{48}`)},
	{kind: "AWS access key id", re: secretRE(`(?:AKIA|ASIA)[A-Z2-7]{16}`)},
	{kind: "Slack token", re: secretRE(`xox[abpr]-[A-Za-z0-9-]{10,}`), extra: func(t string) bool { return hasDigit(t[5:]) }},
	{kind: "Google API key", re: secretRE(`AIza[A-Za-z0-9_-]{35}`)},
	{kind: "Stripe key", re: secretRE(`[sr]k_live_[A-Za-z0-9]{24,}`)},
	{kind: "age secret key", re: secretRE(`AGE-SECRET-KEY-1[QPZRY9X8GF2TVDW0S3JN54KHCE6MUA7L]{58}`)},
}

var (
	pemHeaderRE = regexp.MustCompile(`-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----`)
	pemBodyRE   = regexp.MustCompile(`^[A-Za-z0-9+/]{16,}={0,2}$`)
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

type secretMatch struct {
	kind string
	line int // 1-based
}

// secretMatches scans text line by line and returns each (kind, line) once.
// It never returns the matched text.
func secretMatches(text string) []secretMatch {
	lines := strings.Split(text, "\n")
	var out []secretMatch
	seen := map[secretMatch]bool{}
	add := func(kind string, line int) {
		m := secretMatch{kind, line}
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	for i, line := range lines {
		for _, p := range secretPatterns {
			for _, loc := range p.re.FindAllStringSubmatchIndex(line, -1) {
				tok := line[loc[2]:loc[3]]
				if p.extra != nil && !p.extra(tok) {
					continue
				}
				if placeholderRun(tok[prefixLen(tok):]) {
					continue
				}
				add(p.kind, i+1)
			}
		}
		if pemHeaderRE.MatchString(line) {
			for j := i + 1; j < len(lines); j++ {
				next := strings.TrimSpace(lines[j])
				if next == "" {
					continue
				}
				if pemBodyRE.MatchString(next) && !placeholderRun(next) {
					add("PEM private key", i+1)
				}
				break
			}
		}
	}
	return out
}

// prefixLen is the length of the static prefix of a matched token, which the
// placeholder rule skips.
func prefixLen(tok string) int {
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

const secretAdvice = "; move it to the KB's secrets/ (SOPS-encrypted) and rotate it, because if the page was ever committed the value is already in git history; if it was an example, rewrite it as a placeholder"

// secretFindings scans the body and the frontmatter values of one concept. The
// message names the kind and the location, never the value (D1202).
func secretFindings(in conceptInput) []Finding {
	var out []Finding
	emit := func(kind, where string) {
		out = append(out, newFinding("potential_secret", Finding{
			Path:    in.RelPath,
			Message: fmt.Sprintf("potential secret: %s %s%s", kind, where, secretAdvice),
		}))
	}
	for _, m := range secretMatches(in.Body) {
		emit(m.kind, fmt.Sprintf("on body line %d", m.line))
	}
	if in.Parsed == nil {
		for _, m := range secretMatches(in.FrontmatterRaw) {
			emit(m.kind, `in frontmatter field "frontmatter"`)
		}
		return out
	}
	for _, key := range in.Parsed.Keys() {
		v, _ := in.Parsed.Get(key)
		var vals []string
		switch t := v.(type) {
		case string:
			vals = []string{t}
		case []string:
			vals = t
		}
		kinds := map[string]bool{}
		for _, s := range vals {
			for _, m := range secretMatches(s) {
				if !kinds[m.kind] {
					kinds[m.kind] = true
					emit(m.kind, fmt.Sprintf("in frontmatter field %q", key))
				}
			}
		}
	}
	return out
}
