package configurator

// Adoption of orphaned tables in Codex's config.toml (D99).
//
// Cartographer never parses/re-serializes config.toml (D58): it owns
// marker-delimited slices of it and merges them textually (internal/blocktext).
// Codex CLI, however, rewrites the whole file whenever it persists its own
// settings (trusted hook hashes, [tui], [projects.*], ...): tables are re-emitted
// in canonical form and every comment is dropped — our markers included — while
// the tables themselves survive. Without the step below, the next connect/sync
// would find no markers, append the managed block again and leave the file with
// the same table declared twice: a duplicate key, which stops Codex from
// starting.
//
// AdoptCodexOrphanTables restores idempotency: before a managed block is
// rewritten, the copies of the tables that block owns are removed from the rest
// of the file. Still purely textual — only the spans we own are touched, every
// other byte (comments, ordering, unrelated tables) survives.

import (
	"errors"
	"os"
	"regexp"
	"strings"
)

// codexTOMLHeader matches a TOML table ([name]) or array-of-tables ([[name]])
// header line, capturing the key as written.
var codexTOMLHeader = regexp.MustCompile(`^\[\[?([^\[\]]+)\]\]?$`)

// codexManagedMarker matches a Cartographer block marker line in config.toml
// ("# cartographer:<id>:begin — <display text>" / "# cartographer:<id>:end"),
// capturing the block id and the begin/end side. The display text after the
// begin marker is not part of the marker identity (see blocktext.ReplaceBetween).
var codexManagedMarker = regexp.MustCompile(`^#\s*cartographer:(\S+):(begin|end)\b`)

// AdoptCodexOrphanTables removes from the config.toml at path every top-level
// table (with its sub-tables) that owned recognizes as belonging to a
// Cartographer-managed block but that sits outside every such block — i.e. the
// marker-less copies Codex leaves behind when it rewrites the file. owned
// receives the table's key, split into unquoted segments, and the table's full
// text. Returns the keys removed (dotted, as segments joined) so the caller can
// tell the user why the file changed; no-op, with no error, if the file does
// not exist yet.
func AdoptCodexOrphanTables(path string, owned func(key []string, body string) bool) ([]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	stripped, removed := stripCodexOrphanTables(string(data), owned)
	if len(removed) == 0 {
		return nil, nil
	}
	if err := os.WriteFile(path, []byte(stripped), 0o644); err != nil {
		return nil, err
	}
	return removed, nil
}

// CodexMCPTableOwner returns an owned-predicate (see AdoptCodexOrphanTables)
// matching the [mcp_servers.<name>] tables declared in blockBody, the body of a
// managed block about to be written: those, and only those, are the tables that
// block is the single source of truth for.
func CodexMCPTableOwner(blockBody string) func(key []string, body string) bool {
	var names []string
	for _, line := range strings.Split(blockBody, "\n") {
		key, ok := codexTableHeaderKey(line)
		if !ok || len(key) != 2 || key[0] != "mcp_servers" {
			continue
		}
		names = append(names, key[1])
	}
	return func(key []string, _ string) bool {
		if len(key) < 2 || key[0] != "mcp_servers" {
			return false
		}
		for _, name := range names {
			if key[1] == name {
				return true
			}
		}
		return false
	}
}

// codexTableGroup is the byte range [start, end) of a top-level table header
// plus its body and its sub-tables, keyed by the header's segments.
type codexTableGroup struct {
	key        []string
	start, end int
}

// stripCodexOrphanTables returns content without the table groups owned
// recognizes outside every Cartographer-managed block, plus their keys.
func stripCodexOrphanTables(content string, owned func(key []string, body string) bool) (string, []string) {
	managed := codexManagedSpans(content)

	var removed []string
	kept := make([]string, 0, 2)
	last := 0
	for _, g := range codexTableGroups(content) {
		if overlapsAny(g, managed) || !owned(g.key, content[g.start:g.end]) {
			continue
		}
		kept = append(kept, content[last:g.start])
		last = g.end
		removed = append(removed, strings.Join(g.key, "."))
	}
	if len(removed) == 0 {
		return content, nil
	}
	kept = append(kept, content[last:])

	out := kept[0]
	for _, chunk := range kept[1:] {
		out = joinSeam(out, chunk)
	}
	return out, removed
}

// joinSeam concatenates the two sides of a removed span, keeping at most one
// blank line between them: the span may have taken its own trailing blank line
// with it while the previous one is still there, and the result must not
// accumulate blank lines at every removal.
func joinSeam(before, after string) string {
	head := strings.TrimRight(before, "\n")
	tail := strings.TrimLeft(after, "\n")
	if head == "" {
		return tail
	}
	if tail == "" {
		return head + "\n"
	}
	separators := len(before) - len(head) + len(after) - len(tail)
	if separators > 2 {
		separators = 2
	}
	return head + strings.Repeat("\n", separators) + tail
}

// codexLine is one line of config.toml: its byte range, the table key it
// declares (nil if it is not a table header) and whether it is a comment.
type codexLine struct {
	start, end int
	key        []string
	comment    bool
}

// codexLines splits content into lines, classifying each one.
func codexLines(content string) []codexLine {
	var lines []codexLine
	for off := 0; off < len(content); {
		end := len(content)
		if i := strings.IndexByte(content[off:], '\n'); i >= 0 {
			end = off + i + 1
		}
		l := codexLine{start: off, end: end}
		text := strings.TrimSpace(content[off:end])
		if strings.HasPrefix(text, "#") {
			l.comment = true
		} else {
			l.key, _ = codexTableHeaderKey(text)
		}
		lines = append(lines, l)
		off = end
	}
	return lines
}

// codexTableHeaderKey returns the key segments declared by line if it is a
// table or array-of-tables header, unquoted so a bare and a quoted spelling of
// the same name compare equal.
func codexTableHeaderKey(line string) ([]string, bool) {
	m := codexTOMLHeader.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return nil, false
	}
	return splitTOMLKey(m[1])
}

// splitTOMLKey splits a dotted TOML key into its segments, unquoting the quoted
// ones — which may contain dots themselves (hooks.state."…/config.toml:…"), so
// a plain split on "." would not do. Returns false for anything that is not a
// well-formed dotted key, so a line that merely looks like a header (a
// multi-line array's "[...]" continuation) is not mistaken for one.
func splitTOMLKey(key string) ([]string, bool) {
	var segments []string
	rest := strings.TrimSpace(key)
	for {
		var segment string
		switch {
		case strings.HasPrefix(rest, `"`), strings.HasPrefix(rest, `'`):
			quote := rest[0]
			end := -1
			for i := 1; i < len(rest); i++ {
				if rest[i] == '\\' && quote == '"' {
					i++
					continue
				}
				if rest[i] == quote {
					end = i
					break
				}
			}
			if end < 0 {
				return nil, false
			}
			segment = unquoteTOMLSegment(rest[1:end], quote)
			rest = rest[end+1:]
		default:
			end := strings.IndexFunc(rest, func(r rune) bool { return !isBareKeyRune(r) })
			if end == 0 {
				return nil, false
			}
			if end < 0 {
				end = len(rest)
			}
			segment = rest[:end]
			rest = rest[end:]
		}
		segments = append(segments, segment)

		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			return segments, true
		}
		if rest[0] != '.' {
			return nil, false
		}
		rest = strings.TrimLeft(rest[1:], " \t")
	}
}

// unquoteTOMLSegment undoes the escaping QuoteTOMLString applies, for basic
// (double-quoted) strings; literal (single-quoted) strings have no escapes.
func unquoteTOMLSegment(s string, quote byte) string {
	if quote != '"' || !strings.Contains(s, `\`) {
		return s
	}
	r := strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\n`, "\n", `\r`, "\r", `\t`, "\t")
	return r.Replace(s)
}

func isBareKeyRune(r rune) bool {
	return r == '-' || r == '_' ||
		(r >= '0' && r <= '9') ||
		(r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z')
}

// codexTableGroups returns the top-level table groups of content, in order. A
// group is a table header plus everything up to the next header that is not one
// of its sub-tables — so [mcp_servers.x] carries [mcp_servers.x.env] with it,
// and [[hooks.E]] carries its [[hooks.E.hooks]]. Comment lines directly above
// the next header introduce it and are left out of the group.
func codexTableGroups(content string) []codexTableGroup {
	lines := codexLines(content)
	var groups []codexTableGroup
	for i := 0; i < len(lines); i++ {
		if lines[i].key == nil {
			continue
		}
		g := codexTableGroup{key: lines[i].key, start: lines[i].start, end: len(content)}
		next := i + 1
		for ; next < len(lines); next++ {
			if lines[next].key != nil && !isSubKey(lines[next].key, g.key) {
				break
			}
		}
		if next < len(lines) {
			g.end = lines[next].start
			for k := next - 1; k > i && lines[k].comment; k-- {
				g.end = lines[k].start
			}
		}
		groups = append(groups, g)
		i = next - 1
	}
	return groups
}

// isSubKey reports whether key is a sub-table of root.
func isSubKey(key, root []string) bool {
	if len(key) <= len(root) {
		return false
	}
	for i, segment := range root {
		if key[i] != segment {
			return false
		}
	}
	return true
}

// span is a byte range [start, end) of a config.toml, covered by the
// Cartographer-managed block identified by id — the block id parsed from its
// marker ("# cartographer:<id>:begin" / ":end"). bodyStart/bodyEnd are the
// range strictly between the two marker lines (the marker lines themselves
// excluded): table-group discovery must stay inside it, or the last group in
// the block would absorb the end marker's own comment line.
type span struct {
	start, end         int
	bodyStart, bodyEnd int
	id                 string
}

// codexManagedSpans returns the byte ranges covered by the Cartographer-managed
// blocks of content, marker lines included — every block, not just the one
// being written: an orphan is by definition a table no managed block delimits,
// so a table another block owns (another hook, another MCP server) must never
// be adopted.
func codexManagedSpans(content string) []span {
	open := map[string]int{}
	openBody := map[string]int{}
	var spans []span
	for _, l := range codexLines(content) {
		if !l.comment {
			continue
		}
		m := codexManagedMarker.FindStringSubmatch(strings.TrimSpace(content[l.start:l.end]))
		if m == nil {
			continue
		}
		if m[2] == "begin" {
			open[m[1]] = l.start
			openBody[m[1]] = l.end
			continue
		}
		if start, ok := open[m[1]]; ok {
			spans = append(spans, span{start: start, end: l.end, bodyStart: openBody[m[1]], bodyEnd: l.start, id: m[1]})
			delete(open, m[1])
			delete(openBody, m[1])
		}
	}
	return spans
}

// codexManagedSpanByID returns content's Cartographer-managed span whose
// block id matches id, and whether one was found — false if the block is
// absent, or its begin marker has no matching end (codexManagedSpans only
// ever emits closed spans).
func codexManagedSpanByID(content, id string) (span, bool) {
	for _, s := range codexManagedSpans(content) {
		if s.id == id {
			return s, true
		}
	}
	return span{}, false
}

// codexMarkerBlockID extracts the block id a Cartographer marker line
// encodes ("# cartographer:<id>:begin" / ":end" — the display text after the
// begin marker's " — " is not part of it, same as ReplaceBetween's "stable"
// prefix), and whether marker is a well-formed one. Lets a caller holding
// just a begin/end marker string, not the whole file, find its own span among
// codexManagedSpans's.
func codexMarkerBlockID(marker string) (string, bool) {
	m := codexManagedMarker.FindStringSubmatch(strings.TrimSpace(marker))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// overlapsAny reports whether g overlaps any of the spans.
func overlapsAny(g codexTableGroup, spans []span) bool {
	for _, s := range spans {
		if g.start < s.end && s.start < g.end {
			return true
		}
	}
	return false
}

// codexTableGroupsInRange is codexTableGroups restricted to r: groups are
// computed against r's own text, so a group can never spill past r.end into
// content outside it (the same rule codexTableGroups applies at end-of-file
// applies here at end-of-span instead) — offsets are then translated back
// into content's coordinates.
func codexTableGroupsInRange(content string, r span) []codexTableGroup {
	groups := codexTableGroups(content[r.start:r.end])
	for i := range groups {
		groups[i].start += r.start
		groups[i].end += r.start
	}
	return groups
}

// codexBodyHeaderKeys returns the key of every table header declared in
// body, in the order they appear — the tables a managed block's next
// contents (about to replace the span) declares.
func codexBodyHeaderKeys(body string) [][]string {
	var keys [][]string
	for _, line := range strings.Split(body, "\n") {
		if key, ok := codexTableHeaderKey(line); ok {
			keys = append(keys, key)
		}
	}
	return keys
}

// keyDeclaredIn reports whether key exactly matches one of keys, segment by
// segment — segments are already unquoted by splitTOMLKey, so a bare and a
// quoted spelling of the same key compare equal.
func keyDeclaredIn(key []string, keys [][]string) bool {
	for _, k := range keys {
		if keysEqual(key, k) {
			return true
		}
	}
	return false
}

func keysEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// EvictForeignTablesFromBlock moves out of the Cartographer-managed block
// delimited by begin/end, in the config.toml at path, every top-level table
// group that newBody — the block's next contents, about to replace the span
// via blocktext.Write — does not declare. Codex records its own bookkeeping
// (e.g. [hooks.state."…"] trusted-hash tables) positionally after the last
// table it finds in the file, which in a Cartographer-managed config.toml can
// land inside our span; blocktext.Write would otherwise destroy it along with
// the rest of the block on the next rewrite, silently losing state that is
// entirely Codex's own (D99's codexHookTableOwner deliberately leaves it
// alone for the same reason).
//
// Foreign groups are cut from the span, verbatim, and re-inserted, in file
// order, immediately before the begin marker line, separated from their
// neighbours by at most one blank line — never merged or reformatted (D58:
// config.toml is never parsed/re-serialized). A key declared both inside the
// span and in newBody is ours: it is left in the block for blocktext.Write to
// overwrite, never relocated.
//
// Returns the relocated keys (dotted, as segments joined) in file order, so
// the caller can warn; no-op with no error if the file or the block is
// absent, or nothing in the span is foreign.
func EvictForeignTablesFromBlock(path, begin, end, newBody string) ([]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	content := string(data)

	beginID, ok := codexMarkerBlockID(begin)
	if !ok {
		return nil, nil
	}
	if endID, ok := codexMarkerBlockID(end); !ok || endID != beginID {
		return nil, nil
	}
	target, ok := codexManagedSpanByID(content, beginID)
	if !ok {
		return nil, nil
	}

	ownKeys := codexBodyHeaderKeys(newBody)
	var foreign []codexTableGroup
	for _, g := range codexTableGroupsInRange(content, span{start: target.bodyStart, end: target.bodyEnd}) {
		if !keyDeclaredIn(g.key, ownKeys) {
			foreign = append(foreign, g)
		}
	}
	if len(foreign) == 0 {
		return nil, nil
	}

	if err := os.WriteFile(path, []byte(evictGroupsFromSpan(content, target, foreign)), 0o644); err != nil {
		return nil, err
	}

	relocated := make([]string, len(foreign))
	for i, g := range foreign {
		relocated[i] = strings.Join(g.key, ".")
	}
	return relocated, nil
}

// evictGroupsFromSpan cuts foreign (in file order, all within target) out of
// content and re-inserts their verbatim text, stitched together and onto
// their neighbours via joinSeam's blank-line accounting, immediately before
// target's start (the begin marker line).
func evictGroupsFromSpan(content string, target span, foreign []codexTableGroup) string {
	spanPieces := make([]string, 0, len(foreign)+1)
	removed := make([]string, 0, len(foreign))
	last := target.start
	for _, g := range foreign {
		spanPieces = append(spanPieces, content[last:g.start])
		removed = append(removed, content[g.start:g.end])
		last = g.end
	}
	spanPieces = append(spanPieces, content[last:target.end])

	strippedSpan := spanPieces[0]
	for _, p := range spanPieces[1:] {
		strippedSpan = joinSeam(strippedSpan, p)
	}

	relocated := removed[0]
	for _, r := range removed[1:] {
		relocated = joinSeam(relocated, r)
	}

	before := joinSeam(joinSeam(content[:target.start], relocated), strippedSpan)
	return before + content[target.end:]
}
