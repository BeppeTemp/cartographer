package repodocs

import (
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The relations between decision records (D2301).
// Superseded: no part of the older record holds. Amended: it holds except where
// the newer one says. Both are declared on both records, in their frontmatter.
var (
	relationKey = regexp.MustCompile(`^(supersedes|amends|superseded_by|amended_by):(.*)$`)
	// nearMissKey is a line that reads as a relation key to a person but not to
	// relationKey: indented, another case, `-` for `_`, a space before the colon.
	// Read as nothing, the same typo on both records would pass in silence.
	nearMissKey = regexp.MustCompile(`(?i)^\s*(supersedes|amends|superseded[-_]by|amended[-_]by)\s*:`)
	relationID  = regexp.MustCompile(`^D(?:[1-9][0-9]*|-new-[a-z0-9-]+)$`)
	// relationRef is a citation of any record, numbered or pending. Unlike
	// decisionRef it reads every length of number: most relations name a
	// four-digit record.
	relationRef = regexp.MustCompile(`\bD(?:[0-9]+|-new-[a-z0-9-]+)\b`)
)

// frontmatterLines returns the lines of the leading `---` block, nil without
// one. A key written in the body is prose, not a relation. A Windows checkout
// may hold CRLF: the `\r` is dropped, or no key would be read there.
func frontmatterLines(text string) []string {
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	if lines[0] != "---" {
		return nil
	}
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			return lines[1:i]
		}
	}
	return nil
}

func parseRelations(d *Decision, text string) {
	seen := map[string]bool{}
	for _, line := range frontmatterLines(text) {
		m := relationKey.FindStringSubmatch(line)
		if m == nil {
			if n := nearMissKey.FindStringSubmatch(line); n != nil {
				want := strings.ReplaceAll(strings.ToLower(n[1]), "-", "_")
				d.relationErrs = append(d.relationErrs, fmt.Sprintf("`%s` looks like a relation key but is not one: write `%s:` at the start of the line, in lower case", line, want))
			}
			continue
		}
		key, raw := m[1], strings.TrimSpace(m[2])
		if seen[key] {
			d.relationErrs = append(d.relationErrs, fmt.Sprintf("`%s:` appears twice: write its ids on one line, comma-separated", key))
			continue
		}
		seen[key] = true
		var ids []string
		dup := map[string]bool{}
		ok := raw != ""
		for _, f := range strings.Split(raw, ",") {
			id := strings.TrimSpace(f)
			if !relationID.MatchString(id) || dup[id] {
				ok = false
				break
			}
			dup[id] = true
			ids = append(ids, id)
		}
		switch {
		case !ok:
			d.relationErrs = append(d.relationErrs, fmt.Sprintf("`%s: %s` does not parse: write comma-separated ids, each `D<n>` or `D-new-<slug>`, none twice", key, raw))
		case key == "superseded_by" && len(ids) != 1:
			d.relationErrs = append(d.relationErrs, "`superseded_by:` names one record, the one that replaces this one whole")
		case key == "superseded_by":
			d.SupersededBy = ids[0]
		case key == "supersedes":
			d.Supersedes = ids
		case key == "amends":
			d.Amends = ids
		default:
			d.AmendedBy = ids
		}
	}
}

// rank orders records in time: the AD file first, then by number, then the
// pending ones, which land after every numbered record.
func (d Decision) rank() int {
	if d.Pending {
		return math.MaxInt
	}
	return d.Num
}

type relation struct{ key, target string }

func (d Decision) relations() []relation {
	var out []relation
	for _, id := range d.Supersedes {
		out = append(out, relation{"supersedes", id})
	}
	for _, id := range d.Amends {
		out = append(out, relation{"amends", id})
	}
	if d.SupersededBy != "" {
		out = append(out, relation{"superseded_by", d.SupersededBy})
	}
	for _, id := range d.AmendedBy {
		out = append(out, relation{"amended_by", id})
	}
	return out
}

func has(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// RelationProblems returns one line per inconsistent relation, naming the file
// and the fix: a value that does not parse, an id with no file, a relation not
// mirrored on the other record (with the line to write), a record naming
// itself, a superseded record that supersedes or amends a later one, a cycle,
// and any relation on the historical AD file.
func RelationProblems(decisions []Decision) []string {
	byID := map[string]Decision{}
	for _, d := range decisions {
		if id := d.ID(); id != "" {
			byID[id] = d
		}
	}
	var out []string
	add := func(d Decision, format string, args ...any) {
		out = append(out, "docs/decisions/"+d.File+": "+fmt.Sprintf(format, args...))
	}
	mirror := func(t Decision, key string, ids []string, id string, why string) {
		add(t, "write `%s: %s` in its frontmatter: %s", key, strings.Join(append(append([]string(nil), ids...), id), ", "), why)
	}
	for _, d := range decisions {
		for _, e := range d.relationErrs {
			add(d, "%s", e)
		}
		id := d.ID()
		if id == "" {
			if len(d.relations()) > 0 {
				add(d, "the historical AD file declares no relation: remove its `supersedes`, `amends`, `superseded_by` and `amended_by` keys")
			}
			continue
		}
		for _, r := range d.relations() {
			if r.target == id {
				add(d, "`%s: %s` names the record itself: remove it", r.key, r.target)
				continue
			}
			t, ok := byID[r.target]
			if !ok {
				add(d, "`%s:` names %s, which has no file in docs/decisions/: fix the id or remove it", r.key, r.target)
				continue
			}
			switch r.key {
			case "supersedes":
				if t.SupersededBy == "" {
					mirror(t, "superseded_by", nil, id, d.File+" supersedes it")
				} else if t.SupersededBy != id {
					add(t, "says `superseded_by: %s`, but %s supersedes it too: one record replaces it whole, so make the other relation an `amends`", t.SupersededBy, d.File)
				}
			case "amends":
				if !has(t.AmendedBy, id) {
					mirror(t, "amended_by", t.AmendedBy, id, d.File+" amends it")
				}
				if has(d.Supersedes, r.target) {
					add(d, "both supersedes and amends %s: superseded means no part of it holds, amended that it holds in part; keep one", r.target)
				}
			case "superseded_by":
				if !has(t.Supersedes, id) {
					mirror(t, "supersedes", t.Supersedes, id, d.File+" says it is superseded by it")
				}
			case "amended_by":
				if !has(t.Amends, id) {
					mirror(t, "amends", t.Amends, id, d.File+" says it is amended by it")
				}
			}
		}
		if d.SupersededBy != "" {
			for _, tid := range append(append([]string(nil), d.Supersedes...), d.Amends...) {
				if t, ok := byID[tid]; ok && t.rank() > d.rank() {
					add(d, "is superseded by %s, so it cannot supersede or amend %s, a later record: declare the relation on %s instead", d.SupersededBy, tid, d.SupersededBy)
				}
			}
		}
	}
	return append(out, relationCycles(decisions, byID)...)
}

// relationCycles reports each cycle of "supersedes or amends" once, whichever
// side declared each edge: a newer record replaces or changes only older ones.
func relationCycles(decisions []Decision, byID map[string]Decision) []string {
	edges := map[string]map[string]bool{}
	edge := func(from, to string) {
		if _, ok := byID[from]; !ok || from == to {
			return
		}
		if _, ok := byID[to]; !ok {
			return
		}
		if edges[from] == nil {
			edges[from] = map[string]bool{}
		}
		edges[from][to] = true
	}
	var ids []string
	for _, d := range decisions {
		id := d.ID()
		if id == "" {
			continue
		}
		ids = append(ids, id)
		for _, t := range append(append([]string(nil), d.Supersedes...), d.Amends...) {
			edge(id, t)
		}
		for _, f := range append([]string{d.SupersededBy}, d.AmendedBy...) {
			edge(f, id)
		}
	}
	sort.Strings(ids)
	state := map[string]int{} // 0 unseen, 1 on the stack, 2 done
	var stack []string
	seen := map[string]bool{}
	var out []string
	var visit func(id string)
	visit = func(id string) {
		state[id] = 1
		stack = append(stack, id)
		var next []string
		for t := range edges[id] {
			next = append(next, t)
		}
		sort.Strings(next)
		for _, t := range next {
			switch state[t] {
			case 0:
				visit(t)
			case 1:
				i := len(stack) - 1
				for stack[i] != t {
					i--
				}
				cycle := append(append([]string(nil), stack[i:]...), t)
				key := strings.Join(sortedCopy(stack[i:]), " ")
				if !seen[key] {
					seen[key] = true
					out = append(out, fmt.Sprintf("docs/decisions/%s: its relations form a cycle, %s: a record supersedes or amends only older ones, so remove one of them",
						byID[t].File, strings.Join(cycle, " → ")))
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = 2
	}
	for _, id := range ids {
		if state[id] == 0 {
			visit(id)
		}
	}
	return out
}

func sortedCopy(s []string) []string {
	c := append([]string(nil), s...)
	sort.Strings(c)
	return c
}

// currentStateFile reports whether a citation in rel is read as current
// behaviour: the docs pages outside docs/decisions/, every AGENTS.md,
// CONTRIBUTING.md and .claude/. A decision record and CHANGELOG.md are history.
func currentStateFile(rel string) bool {
	switch {
	case rel == "CONTRIBUTING.md", path.Base(rel) == "AGENTS.md", strings.HasPrefix(rel, ".claude/"):
		return true
	}
	return strings.HasPrefix(rel, "docs/") && !strings.HasPrefix(rel, "docs/decisions/") && path.Ext(rel) == ".md"
}

// SupersededCitations returns each line of a current-state file that cites a
// superseded record without also citing the record that supersedes it. An
// amended record still holds in part and is not concerned. The generated blocks
// are skipped: the decision index lists superseded records by design, and it is
// regenerated only on the release PR (D328).
func SupersededCitations(root string, decisions []Decision) ([]string, error) {
	succ := map[string]string{}
	for _, d := range decisions {
		if d.SupersededBy != "" && d.ID() != "" {
			succ[d.ID()] = d.SupersededBy
		}
	}
	if len(succ) == 0 {
		return nil, nil
	}
	files, err := TextFiles(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, rel := range files {
		if !currentStateFile(rel) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return nil, err
		}
		for i, line := range strings.Split(blankGeneratedBlocks(string(data), [][2]string{{CodeMapBegin, CodeMapEnd}, {IndexBegin, IndexEnd}}), "\n") {
			cited := map[string]bool{}
			for _, id := range relationRef.FindAllString(line, -1) {
				cited[id] = true
			}
			for _, id := range sortedCopy(keys(cited)) {
				if s, ok := succ[id]; ok && !cited[s] {
					out = append(out, fmt.Sprintf("%s:%d: cites %s, which %s supersedes: cite %s instead, or name it on the same line", rel, i+1, id, s, s))
				}
			}
		}
	}
	return out, nil
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// blankGeneratedBlocks blanks the generated blocks of the given marker pairs and
// keeps the line count, so a reported line number is the line in the file.
// SupersededCitations passes the code map and the decision index only.
func blankGeneratedBlocks(content string, pairs [][2]string) string {
	for _, pair := range pairs {
		for {
			i := strings.Index(content, pair[0])
			j := strings.Index(content, pair[1])
			if i < 0 || j < i {
				break
			}
			j += len(pair[1])
			content = content[:i] + strings.Repeat("\n", strings.Count(content[i:j], "\n")) + content[j:]
		}
	}
	return content
}
