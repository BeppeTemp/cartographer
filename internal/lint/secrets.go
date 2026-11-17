package lint

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/secretscan"
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

type secretMatch struct {
	kind string
	line int // 1-based
}

// secretMatches returns each (kind, line) once, from secretscan.Find. It never
// returns the matched text.
func secretMatches(text string) []secretMatch {
	var out []secretMatch
	seen := map[secretMatch]bool{}
	for _, m := range secretscan.Find(text) {
		k := secretMatch{m.Kind, m.Line}
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

const secretAdvice = "; move it to the KB's secrets/ (SOPS-encrypted); if it was an example, rewrite it as a placeholder"

// SecretHistoryClause ends the message of a potential_secret on a page that has
// git history: the value is already there, so it needs rotating. A page with no
// history gets no such claim (#2186: a refused write is never committed). The
// write gate compares messages before and after a write and strips this clause
// first, so a page that moved keeps the identity of its finding.
const SecretHistoryClause = "; the page is in git history, so the value is already there: rotate it"

// pageHistory says whether the page at id has a commit in the KB's git history.
func pageHistory(k *kb.KB, id okf.ConceptID) func() bool {
	return func() bool {
		loc, err := k.LocateConcept(id)
		if err != nil {
			return false
		}
		rel, err := filepath.Rel(k.Root, loc.File)
		if err != nil {
			return false
		}
		revs, err := gitx.FileHistory(k.Root, filepath.ToSlash(rel), 1)
		return err == nil && len(revs) == 1
	}
}

// secretFindings scans the body and every line of the frontmatter of one concept. The
// message names the kind and the location, never the value (D1202).
func secretFindings(in conceptInput) []Finding {
	var out []Finding
	advice := secretAdvice
	emit := func(kind, where string) {
		if in.History != nil && advice == secretAdvice && in.History() {
			advice += SecretHistoryClause
		}
		out = append(out, newFinding("potential_secret", Finding{
			Path:    in.RelPath,
			Message: fmt.Sprintf("potential secret: %s %s%s", kind, where, advice),
		}))
	}
	for _, m := range secretMatches(in.Body) {
		emit(m.kind, fmt.Sprintf("on body line %d", m.line))
	}
	reported := map[string]bool{} // key + "\x00" + kind, found through a parsed value
	if in.Parsed != nil {
		for _, key := range in.Parsed.Keys() {
			v, _ := in.Parsed.Get(key)
			var vals []string
			switch t := v.(type) {
			case string:
				vals = []string{t}
			case []string:
				vals = t
			case okf.Block: // a nested block (`credentials:` + indented keys)
				vals = []string{string(t)}
			}
			for _, s := range vals {
				for _, m := range secretMatches(s) {
					if !reported[key+"\x00"+m.kind] {
						reported[key+"\x00"+m.kind] = true
						emit(m.kind, fmt.Sprintf("in frontmatter field %q", key))
					}
				}
			}
		}
	}
	// The raw block is always scanned too: the parser drops lines (a scalar
	// followed by list items, comments), and a dropped line still reaches git.
	// A match under a top-level key already reported for that kind is the same
	// finding; any other is reported by its line within the frontmatter.
	rawKeys := rawLineKeys(in.FrontmatterRaw)
	for _, m := range secretMatches(in.FrontmatterRaw) {
		if k := rawKeys[m.line-1]; k != "" && reported[k+"\x00"+m.kind] {
			continue
		}
		emit(m.kind, fmt.Sprintf("on frontmatter line %d", m.line))
	}
	return out
}

// rawLineKeys maps each line of a raw frontmatter block to the top-level key
// it sits under ("" before the first key or under a comment).
func rawLineKeys(raw string) []string {
	lines := strings.Split(raw, "\n")
	keys := make([]string, len(lines))
	cur := ""
	for i, l := range lines {
		if l != "" && l[0] != ' ' && l[0] != '\t' && l[0] != '#' && l[0] != '-' {
			if c := strings.Index(l, ":"); c > 0 {
				cur = strings.Trim(strings.TrimSpace(l[:c]), `"'`)
			}
		}
		if strings.HasPrefix(l, "#") {
			continue // a top-level comment belongs to no key
		}
		keys[i] = cur
	}
	return keys
}
