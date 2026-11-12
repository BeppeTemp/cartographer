package feedback

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/BeppeTemp/cartographer/internal/secretscan"
)

// Known is what a caller knows about the installation: every value that must
// not reach a public issue. The redactor fails closed: it looks for token
// shapes it was not told about, and for each of these in any ASCII case except
// the concept IDs, which are case-sensitive paths and match only as written.
type Known struct {
	KBNames     []string
	ServerHosts []string // hosts and host:port of the server URL and of any KB remote
	User        string
	Home        string
	Workspaces  []string
	ConceptIDs  []string
}

// Summary counts what was replaced per class, never the values.
type Summary struct{ Secrets, Hosts, Paths, Users, ConceptIDs, KBNames int }

// Total is the number of replacements.
func (s Summary) Total() int {
	return s.Secrets + s.Hosts + s.Paths + s.Users + s.ConceptIDs + s.KBNames
}

const removed = "[removed]"

var (
	jwtRE      = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]*`)
	bearerRE   = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[A-Za-z0-9._~+/=-]{8,}`)
	longRunRE  = regexp.MustCompile(`[A-Za-z0-9_-]{32,}`)
	userinfoRE = regexp.MustCompile(`[A-Za-z0-9._~%-]+:[^\s:@/\[\]]+@`)
	// A credential-named key followed by : or = and a value: quoted up to the
	// closing quote, else one word (an optional scheme word, "token abc", is part
	// of it); [ is excluded for idempotence. After password, passwd or secret an
	// unquoted value runs to the end of the line: a passphrase has several words.
	keyValueRE = regexp.MustCompile(`(?i)\b([A-Za-z0-9_]*(?:token|api[_-]?key|authorization)["']?\s*[:=]\s*)("[^"\n]*"|'[^'\n]*'|(?:(?:Bearer|Basic|token)\s+)?[^\s"',;&\[\]]+)`)
	phraseRE   = regexp.MustCompile(`(?i)\b([A-Za-z0-9_]*(?:password|passwd|secret)["']?\s*[:=]\s*)("[^"\n]*"|'[^'\n]*'|[^\s\[][^\n]*)`)
	// scp-style remotes (git@host:path), e-mail addresses and bare host names.
	scpRE   = regexp.MustCompile(`\b[A-Za-z0-9._-]+@([A-Za-z0-9][A-Za-z0-9.-]*):[A-Za-z0-9._~/-]+`)
	emailRE = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+`)
	// Fail closed: a dotted name of two or more labels is a host unless its last
	// label is not alphabetic or is a file extension (see fileExts).
	fqdnRE = regexp.MustCompile(`\b[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)+\b`)
	tldRE  = regexp.MustCompile(`^[A-Za-z]{2,24}$`)
	// The value of a secret-looking query or assignment key; [ is excluded so
	// "[removed]" is not matched again (idempotence).
	queryRE = regexp.MustCompile(`(?i)\b([A-Za-z0-9_]*(?:token|key|secret|password|sig|signature|auth|code))=([^&\s#"')>\[\]]+)`)
	urlRE   = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*)://((?:\[[0-9A-Za-z:.%]*\]|[^\s/?#"'<>()\[\]])*)[^\s"'<>()\[\]]*`)
	ipv4RE  = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)
	// The placeholder hosts; a host that is one is kept, so a second pass is a no-op.
	placeholderIDRE   = regexp.MustCompile(`^area/page-\d+$`)
	placeholderHostRE = regexp.MustCompile(`^example(?:-\d+)?\.(?:com|org|net)$`)
)

type redactor struct {
	k     Known
	sum   Summary
	hosts map[string]string
}

// Redact removes secrets and every value of k from text, in a fixed order
// (secrets, hosts, paths, user, concept IDs, KB names). It is deterministic
// and idempotent: Redact(Redact(x)) == Redact(x).
func Redact(text string, k Known) (string, Summary) {
	r := &redactor{k: k, hosts: map[string]string{}}
	text = r.urls(text) // first: its authority may hold userinfo or an IPv6 literal
	text = r.secrets(text)
	text = r.scpAndEmail(text)
	text = r.bareHosts(text)
	text = r.fqdns(text)
	text = r.paths(text)
	text = r.user(text)
	text = r.conceptIDs(text)
	text = r.kbNames(text)
	return text, r.sum
}

func (r *redactor) secrets(s string) string {
	// Find orders by line then pattern, not by offset: sort the spans, merge
	// the overlapping ones, then replace right to left (earlier offsets stay valid).
	ms := secretscan.Find(s)
	sort.SliceStable(ms, func(a, b int) bool { return ms[a].Start < ms[b].Start })
	var spans [][2]int
	for _, m := range ms {
		if n := len(spans); n > 0 && m.Start <= spans[n-1][1] {
			spans[n-1][1] = max(spans[n-1][1], m.End)
		} else {
			spans = append(spans, [2]int{m.Start, m.End})
		}
	}
	for i := len(spans) - 1; i >= 0; i-- {
		s = s[:spans[i][0]] + removed + s[spans[i][1]:]
		r.sum.Secrets++
	}
	count := func(re *regexp.Regexp, repl func(string) string) {
		s = re.ReplaceAllStringFunc(s, func(m string) string {
			if x := repl(m); x != m {
				r.sum.Secrets++
				return x
			}
			return m
		})
	}
	count(jwtRE, func(string) string { return removed })
	count(phraseRE, func(m string) string { return keepQuote(phraseRE, m) })
	count(keyValueRE, func(m string) string { return keepQuote(keyValueRE, m) })
	count(bearerRE, func(m string) string { return strings.Fields(m)[0] + " " + removed })
	count(userinfoRE, func(string) string { return removed + "@" })
	count(queryRE, func(m string) string { k, _, _ := strings.Cut(m, "="); return k + "=" + removed })
	count(longRunRE, func(m string) string { // heuristic: a long run with letters and digits
		if strings.IndexFunc(m, unicode.IsLetter) >= 0 && strings.IndexFunc(m, unicode.IsDigit) >= 0 {
			return removed
		}
		return m
	})
	return s
}

// keepQuote replaces the value of a key=value match, keeping its quotes.
func keepQuote(re *regexp.Regexp, m string) string {
	sub := re.FindStringSubmatch(m)
	v := sub[2]
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return sub[1] + v[:1] + removed + v[len(v)-1:]
	}
	return sub[1] + removed
}

// host maps a real host to a stable placeholder: example.com, example.org,
// example.net, then example-4.com...; two hosts stay distinguishable.
func (r *redactor) host(h string) string {
	h = strings.ToLower(h)
	if p, ok := r.hosts[h]; ok {
		return p
	}
	n := len(r.hosts)
	p := fmt.Sprintf("example-%d.com", n+1)
	if n < 3 {
		p = "example." + []string{"com", "org", "net"}[n]
	}
	r.hosts[h] = p
	return p
}

// hostOnly drops userinfo and port: "[removed]@host:8080" -> "host".
func hostOnly(authority string) string {
	if i := strings.LastIndex(authority, "@"); i >= 0 {
		authority = authority[i+1:]
	}
	if i := strings.LastIndex(authority, ":"); i >= 0 && !strings.HasSuffix(authority, "]") {
		authority = authority[:i]
	}
	return strings.ToLower(authority)
}

// urls keeps this project's own URLs and reduces every other to scheme://example.com.
func (r *redactor) urls(s string) string {
	repo := strings.ToLower(Repo)
	return urlRE.ReplaceAllStringFunc(s, func(m string) string {
		sub := urlRE.FindStringSubmatch(m)
		h := hostOnly(sub[2])
		rest := strings.ToLower(m[len(sub[1])+3+len(sub[2]):])
		switch {
		case placeholderHostRE.MatchString(h) && rest == "":
			return m
		case sub[2] == "" || strings.ToLower(sub[2]) != h: // userinfo or a port: never ours
		case h == "github.com" && underPath(rest, "/"+repo), h == "beppetemp.github.io" && underPath(rest, "/cartographer"):
			return m
		}
		if !placeholderHostRE.MatchString(h) {
			h = r.host(h)
		}
		r.sum.Hosts++
		return sub[1] + "://" + h
	})
}

// underPath reports whether rest is prefix itself or something below it, so
// "/cartographer-evil" is not under "/cartographer".
func underPath(rest, prefix string) bool {
	prefix = strings.ToLower(prefix)
	return strings.HasPrefix(rest, prefix) && (len(rest) == len(prefix) || strings.ContainsRune("/?#", rune(rest[len(prefix)])))
}

// fileExts are the last labels that name a file, not a host: the explicit
// exceptions of the fail-closed host rule.
var fileExts = map[string]bool{}

func init() {
	for _, e := range strings.Fields(`go md yaml yml json sh py js ts tsx jsx mjs cjs txt toml mod sum star html css lock log env
		rs c h cc cpp java rb xml sql ini cfg conf csv tsv png jpg jpeg gif svg pdf zip gz tgz tar bak tmp out diff patch pid sock
		key pem crt cer exe dll so dylib bin vue map`) {
		fileExts[e] = true
	}
}

// allowedHosts are the public hosts a report may name without redaction.
var allowedHosts = map[string]bool{"github.com": true, "beppetemp.github.io": true}

// scpAndEmail turns "git@host:path" into "user@<placeholder host>:path" and an
// e-mail address into user@example.com.
func (r *redactor) scpAndEmail(s string) string {
	s = scpRE.ReplaceAllStringFunc(s, func(m string) string {
		h := strings.ToLower(scpRE.FindStringSubmatch(m)[1])
		if !placeholderHostRE.MatchString(h) {
			h = r.host(h)
		}
		if x := "user@" + h + ":path"; x != m {
			r.sum.Hosts++
			return x
		}
		return m
	})
	return emailRE.ReplaceAllStringFunc(s, func(m string) string {
		if m == "user@example.com" {
			return m
		}
		r.sum.Hosts++
		return "user@example.com"
	})
}

// fqdns replaces every remaining dotted name that looks like a host and is not
// a placeholder or an allow-listed public host.
func (r *redactor) fqdns(s string) string {
	return fqdnRE.ReplaceAllStringFunc(s, func(m string) string {
		h := strings.ToLower(m)
		last := h[strings.LastIndex(h, ".")+1:]
		if allowedHosts[h] || placeholderHostRE.MatchString(h) || !tldRE.MatchString(last) || fileExts[last] {
			return m
		}
		r.sum.Hosts++
		return r.host(m)
	})
}

// bareHosts replaces the server hosts the caller knows, and any IPv4 address.
func (r *redactor) bareHosts(s string) string {
	var needles []string
	for _, h := range r.k.ServerHosts {
		if h = strings.TrimSpace(h); h != "" {
			needles = append(needles, h)
			if o := hostOnly(h); o != strings.ToLower(h) && o != "" {
				needles = append(needles, o)
			}
		}
	}
	for _, n := range longestFirst(needles) {
		s = r.token(s, n, true, "", func(string) string { r.sum.Hosts++; return r.host(hostOnly(n)) })
	}
	return ipv4RE.ReplaceAllStringFunc(s, func(m string) string { r.sum.Hosts++; return r.host(m) })
}

// paths turns each workspace into $HOME/project[-N], then the home into $HOME.
func (r *redactor) paths(s string) string {
	i := 0
	for _, w := range longestFirst(r.k.Workspaces) {
		if w = strings.TrimRight(strings.TrimSpace(w), `/\`); len(w) < 2 {
			continue
		}
		i++
		to := "$HOME/project"
		if i > 1 {
			to += fmt.Sprintf("-%d", i)
		}
		s = r.pathForms(s, w, to)
	}
	if h := strings.TrimRight(strings.TrimSpace(r.k.Home), `/\`); len(h) >= 2 {
		s = r.pathForms(s, h, "$HOME")
	}
	return s
}

func (r *redactor) pathForms(s, p, to string) string {
	fold := len(p) > 1 && (p[1] == ':' || strings.HasPrefix(p, `\\`)) // a Windows path
	for _, form := range []string{p, strings.ReplaceAll(p, `\`, "/"), strings.ReplaceAll(p, "/", `\`)} {
		s = r.token(s, form, fold, "", func(string) string { r.sum.Paths++; return to })
	}
	return s
}

// user replaces the account name: whole-token from 3 characters, otherwise only
// in "name@" and in the home-directory forms of the three platforms.
func (r *redactor) user(s string) string {
	u := strings.TrimSpace(r.k.User)
	if u == "" || u == "user" {
		return s
	}
	if len(u) >= 3 {
		return r.token(s, u, true, "", func(string) string { r.sum.Users++; return "user" })
	}
	for _, f := range []string{u + "@", "/Users/" + u, "/home/" + u, `\Users\` + u} {
		keep := len(f) - len(u)
		if f[len(f)-1] == '@' {
			keep = 0
		}
		s = r.token(s, f, true, "", func(string) string {
			r.sum.Users++
			if f[len(f)-1] == '@' {
				return "user@"
			}
			return f[:keep] + "user"
		})
	}
	return s
}

func (r *redactor) conceptIDs(s string) string {
	var ids []string
	for _, id := range r.k.ConceptIDs {
		if id = strings.TrimSpace(id); strings.Contains(id, "/") && !placeholderIDRE.MatchString(id) {
			ids = append(ids, id)
		}
	}
	// Numbered in order of first appearance, so find the positions first.
	ids = longestFirst(ids)
	type hit struct{ pos, id int }
	var hits []hit
	for i, id := range ids {
		for from := 0; ; {
			j := strings.Index(s[from:], id)
			if j < 0 {
				break
			}
			hits = append(hits, hit{from + j, i})
			from += j + len(id)
		}
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].pos < hits[b].pos })
	n := map[int]int{}
	for _, h := range hits {
		if _, ok := n[h.id]; !ok {
			n[h.id] = len(n) + 1
		}
	}
	for i, id := range ids {
		if num, ok := n[i]; ok {
			s = r.token(s, id, false, ".md", func(string) string { r.sum.ConceptIDs++; return fmt.Sprintf("area/page-%d", num) })
		}
	}
	return s
}

func (r *redactor) kbNames(s string) string {
	for i, name := range r.k.KBNames {
		to := fmt.Sprintf("kb-%d", i+1)
		if i < 26 {
			to = "kb-" + string(rune('a'+i))
		}
		// Names match in any ASCII case, so the placeholder guard compares folded.
		low := asciiLower(name)
		if len(name) < 3 || low == to || strings.HasPrefix(low, "kb-") && len(name) == 4 {
			continue
		}
		s = r.token(s, name, true, "", func(string) string { r.sum.KBNames++; return to })
	}
	return s
}

func longestFirst(in []string) []string {
	out := append([]string(nil), in...)
	sort.SliceStable(out, func(a, b int) bool { return len(out[a]) > len(out[b]) })
	return out
}

// token replaces whole-token occurrences of needle (ASCII case-insensitive when
// fold): not touching an alphanumeric, _ or - on either side, "." and every
// other byte counting as a boundary (fail closed: "alice.example" is redacted).
// A needle that starts or ends in a non-word byte ("ab@", "/home/ab") needs no
// boundary on that side. A suffix (".md") right after a match is consumed.
func (r *redactor) token(s, needle string, fold bool, suffix string, repl func(string) string) string {
	if needle == "" {
		return s
	}
	hay, nd := s, needle
	if fold {
		hay, nd = asciiLower(s), asciiLower(needle)
	}
	var b strings.Builder
	last := 0
	for from := 0; ; {
		j := strings.Index(hay[from:], nd)
		if j < 0 {
			break
		}
		i, e := from+j, from+j+len(nd)
		from = i + 1
		m := e
		if suffix != "" && strings.HasPrefix(s[e:], suffix) {
			m += len(suffix)
		}
		if i < last || isWord(nd[0]) && i > 0 && isWord(s[i-1]) || isWord(nd[len(nd)-1]) && m < len(s) && isWord(s[m]) {
			continue
		}
		b.WriteString(s[last:i] + repl(s[i:e]))
		last, from = m, m
	}
	b.WriteString(s[last:])
	return b.String()
}

// isWord reports a byte that continues a name: ASCII alphanumeric, _ or -.
func isWord(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == '-'
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
