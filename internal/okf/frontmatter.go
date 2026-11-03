// Package okf — parser/serializer for the YAML subset used in OKF frontmatter.
// Parsing and serialization are implemented using Go's standard library only.
package okf

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// entryKind distinguishes elements in the internal ordered list.
type entryKind int

const (
	entryKeyKind     entryKind = iota // key-value pair
	entryCommentKind                  // comment line (# ...) or blank line
)

// entry is an element of the Frontmatter ordered structure.
type entry struct {
	kind  entryKind
	key   string      // valid only for entryKeyKind
	value interface{} // string, []string or nil; valid only for entryKeyKind
	raw   string      // original line; valid only for entryCommentKind

	// comment is the trailing " # ..." of the key's line (D1558), written back
	// verbatim while the value is unchanged. Valid only for entryKeyKind.
	comment string
	// block is the verbatim text of a block scalar ("|", ">" and variants:
	// the header after "key:" and the indented lines), valid only for
	// entryKeyKind. value holds the logical string; Serialize writes block
	// back while value is still blockValue, so an unrelated write leaves the
	// page byte-identical.
	block      string
	blockValue string
	// parsed marks an entry read from a file: Validate does not hold its key to
	// CheckKey, so a page that already carries a key such as "a#b" stays
	// writable (imported pages, D162); only keys a caller sets are checked.
	parsed bool
}

// Block is the raw text of a nested block under a key whose value is empty:
//
//	providers:
//	  codex: { sandbox_mode: read-only }
//
// The parser does not interpret it (its YAML subset has no nested maps); it
// keeps the indented lines verbatim, so the block is neither flattened into
// sibling keys nor lost on Serialize, and a consumer that needs the structure
// parses the text itself. Lines keep their original indentation.
type Block string

// Frontmatter is an ordered map representing an OKF frontmatter block.
// Preserves key order and the position of original comments.
// Supported values: string, []string, Block, nil.
type Frontmatter struct {
	entries []entry
	index   map[string]int // key → index in entries (entryKeyKind only)
}

// ParseFrontmatter parses the raw frontmatter text (without --- delimiters)
// and returns a structured *Frontmatter.
func ParseFrontmatter(raw string) (*Frontmatter, error) {
	fm := &Frontmatter{
		index: make(map[string]int),
	}

	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(raw, "\n")

	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Blank line or comment: preserved in order.
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			fm.entries = append(fm.entries, entry{kind: entryCommentKind, raw: line})
			i++
			continue
		}

		// An indented line outside an open block (a continuation of a multi-line
		// scalar, a stray item) is never a key: it is kept verbatim like a
		// comment, and pruned with its key (pruneContinuations). Without this a
		// "  Text with: colon" line became a top-level key and the next write
		// rewrote it at column 0.
		if line[0] == ' ' || line[0] == '\t' {
			fm.entries = append(fm.entries, entry{kind: entryCommentKind, raw: line})
			i++
			continue
		}

		colonIdx := strings.Index(line, ":")
		if colonIdx < 0 {
			// Unrecognizable line: treated as a comment for tolerance.
			fm.entries = append(fm.entries, entry{kind: entryCommentKind, raw: line})
			i++
			continue
		}

		key := strings.TrimSpace(line[:colonIdx])
		valueRaw := strings.TrimSpace(line[colonIdx+1:])

		var value interface{}
		var comment, blockText, blockValue string
		if !strings.HasPrefix(valueRaw, "[") {
			valueRaw, comment = splitInlineComment(valueRaw)
		}

		switch {
		case valueRaw == "":
			// Empty value: check for block list in subsequent lines.
			var blockItems []string
			j := i + 1
			for j < len(lines) {
				nextTrimmed := strings.TrimSpace(lines[j])
				if strings.HasPrefix(nextTrimmed, "- ") {
					item := unquoteScalar(strings.TrimSpace(nextTrimmed[2:]))
					blockItems = append(blockItems, item)
					j++
				} else {
					break
				}
			}
			if len(blockItems) > 0 {
				value = blockItems
				i = j
			} else if end := nestedBlockEnd(lines, i+1); end > i+1 {
				value = Block(strings.Join(lines[i+1:end], "\n"))
				i = end
			} else {
				value = nil
				i++
			}

		case isBlockScalarHeader(valueRaw):
			end := nestedBlockEnd(lines, i+1)
			blockLines := lines[i+1 : end]
			value = decodeBlockScalar(valueRaw, blockLines)
			blockValue = value.(string)
			blockText = strings.Join(append([]string{valueRaw}, blockLines...), "\n")
			i = end

		case strings.HasPrefix(valueRaw, "["):
			// Flow list, on one line or several (D162): the multi-line form is
			// valid YAML and is what any editor produces for a long list, but the
			// parser only ever looked at the current line — 63 files in one corpus
			// used it and one of them aborted an import. The block-list branch
			// above already had the lookahead; this branch simply did not use it.
			inner, consumed, tail, closed := collectFlowList(lines[i:], colonIdx+1)
			if !closed {
				return nil, fmt.Errorf("frontmatter: unclosed flow list at key %q (line %d)", key, i+1)
			}
			value = parseFlowList(inner)
			if t := strings.TrimSpace(tail); strings.HasPrefix(t, "#") {
				comment = " " + t
			}
			i += consumed

		default:
			// scalar
			value = unquoteScalar(valueRaw)
			i++
		}

		// Duplicate key: overwrite the existing value (standard YAML behavior).
		if comment != "" && !strings.HasPrefix(comment, " ") {
			comment = " " + comment
		}
		e := entry{kind: entryKeyKind, key: key, value: value, comment: comment, block: blockText, blockValue: blockValue, parsed: true}
		if idx, exists := fm.index[key]; exists {
			e.kind = entryKeyKind
			fm.entries[idx] = e
		} else {
			fm.index[key] = len(fm.entries)
			fm.entries = append(fm.entries, e)
		}
	}

	return fm, nil
}

// splitInlineComment splits a scalar's text (already trimmed) from a trailing
// YAML comment (D1558). A quoted scalar ends at its closing quote; an unquoted
// one at the first "#" that starts the text or follows a space or tab. When
// the text after a closing quote is not a comment the whole text is the value,
// as before. The returned comment keeps its "#" and has no leading space.
func splitInlineComment(v string) (value, comment string) {
	if v == "" {
		return v, ""
	}
	if v[0] == '"' || v[0] == '\'' {
		q := v[0]
		for i := 1; i < len(v); i++ {
			switch {
			case q == '"' && v[i] == '\\':
				i++
			case v[i] == q:
				if q == '\'' && i+1 < len(v) && v[i+1] == '\'' {
					i++
					continue
				}
				rest := strings.TrimSpace(v[i+1:])
				if strings.HasPrefix(rest, "#") && (v[i+1] == ' ' || v[i+1] == '\t') {
					return v[:i+1], rest
				}
				return v, ""
			}
		}
		return v, ""
	}
	if v[0] == '#' {
		return "", v
	}
	for i := 1; i < len(v); i++ {
		if v[i] == '#' && (v[i-1] == ' ' || v[i-1] == '\t') {
			return strings.TrimSpace(v[:i]), v[i:]
		}
	}
	return v, ""
}

// isBlockScalarHeader reports whether v is a YAML block scalar header: "|" or
// ">" with an optional chomping sign and indentation digit, in either order.
func isBlockScalarHeader(v string) bool {
	if v == "" || (v[0] != '|' && v[0] != '>') {
		return false
	}
	var sign, digit int
	for _, c := range v[1:] {
		switch {
		case c == '+' || c == '-':
			sign++
		case c >= '1' && c <= '9':
			digit++
		default:
			return false
		}
	}
	return sign <= 1 && digit <= 1
}

// decodeBlockScalar returns the logical string of a block scalar: literal
// ("|") keeps the line breaks, folded (">") joins consecutive lines with a
// space and keeps the breaks of blank and more-indented lines; the chomping
// sign decides the trailing newlines (clip, "-" strip, "+" keep).
func decodeBlockScalar(header string, lines []string) string {
	indent := -1
	if d := strings.Trim(header[1:], "+-"); d != "" {
		indent = int(d[0] - '0')
		// An explicit indicator is relative to the parent (column 0).
	}
	if indent < 0 {
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				indent = len(l) - len(strings.TrimLeft(l, " \t"))
				break
			}
		}
	}
	if indent < 0 {
		indent = 0
	}
	text := make([]string, len(lines))
	for n, l := range lines {
		switch {
		case strings.TrimSpace(l) == "" && len(l) <= indent:
			text[n] = ""
		case len(l) >= indent:
			text[n] = l[indent:]
		default:
			text[n] = strings.TrimLeft(l, " \t")
		}
	}
	// Trailing blank lines are the chomped part.
	trail := 0
	for len(text) > 0 && text[len(text)-1] == "" {
		text = text[:len(text)-1]
		trail++
	}
	var body string
	if header[0] == '|' {
		body = strings.Join(text, "\n")
	} else {
		var sb strings.Builder
		for n, l := range text {
			if n > 0 {
				prev := text[n-1]
				moreIndented := func(x string) bool { return x != "" && (x[0] == ' ' || x[0] == '\t') }
				switch {
				case l == "" || prev == "":
					// a blank line is one break; the line before it adds none
					if l == "" {
						sb.WriteByte('\n')
					}
				case moreIndented(l) || moreIndented(prev):
					sb.WriteByte('\n')
				default:
					sb.WriteByte(' ')
				}
			}
			sb.WriteString(l)
		}
		body = sb.String()
	}
	if len(text) == 0 {
		return ""
	}
	switch {
	case strings.Contains(header, "-"):
		return body
	case strings.Contains(header, "+"):
		return body + strings.Repeat("\n", trail+1)
	default:
		return body + "\n"
	}
}

// nestedBlockEnd returns the index just past the indented block that starts at
// lines[start]: every following line that is indented (or blank/comment inside
// the block, provided an indented line follows). It returns start when
// lines[start] is not indented, i.e. there is no block.
func nestedBlockEnd(lines []string, start int) int {
	isIndented := func(l string) bool {
		return l != "" && (l[0] == ' ' || l[0] == '\t') && strings.TrimSpace(l) != ""
	}
	end := start
	for j := start; j < len(lines); j++ {
		switch {
		case isIndented(lines[j]):
			end = j + 1
		case strings.TrimSpace(lines[j]) == "":
			// blank: belongs to the block only if an indented line follows
		default:
			return end
		}
	}
	return end
}

// collectFlowList accumulates the inner text of a flow list starting on
// lines[0] at byte offset start, across as many lines as needed, and reports how
// many lines it consumed and whether the closing bracket was found.
//
// Depth is tracked over [ and ], and bracket characters inside quoted scalars are
// skipped, so a "]" in a quoted element is not a terminator. Scanning stops at the
// end of the frontmatter block: a "[" with no "]" before it is unclosed, not a
// licence to consume the body. tail is what follows the closing bracket on its
// line (a trailing comment, D1558).
func collectFlowList(lines []string, start int) (inner string, consumed int, tail string, closed bool) {
	var sb strings.Builder
	depth := 0
	var quote byte
	escaped := false
	for n, line := range lines {
		segment := line
		if n == 0 {
			segment = line[start:]
		} else {
			sb.WriteByte(' ')
		}
		for i := 0; i < len(segment); i++ {
			c := segment[i]
			if quote != 0 {
				// A \" inside a double-quoted item (strconv.Quote's escape,
				// which serializeScalar emits) does not close it — the same
				// rule splitFlowList applies (D309).
				switch {
				case escaped:
					escaped = false
				case c == '\\' && quote == '"':
					escaped = true
				case c == quote:
					quote = 0
				}
				sb.WriteByte(c)
				continue
			}
			switch c {
			case '\'', '"':
				quote = c
			case '[':
				depth++
				if depth == 1 {
					// The opening bracket itself is not part of the inner text.
					continue
				}
			case ']':
				depth--
				if depth == 0 {
					return sb.String(), n + 1, segment[i+1:], true
				}
			}
			sb.WriteByte(c)
		}
	}
	return sb.String(), len(lines), "", false
}

// parseFlowList parses the inner part of a YAML flow list (without the square brackets).
func parseFlowList(inner string) []string {
	if strings.TrimSpace(inner) == "" {
		return []string{}
	}
	parts := splitFlowList(inner)
	result := make([]string, 0, len(parts))
	for n, p := range parts {
		// A trailing comma before "]" is valid YAML and is what every editor
		// emits when it wraps a list, so the empty final segment it leaves is
		// dropped rather than becoming an empty element (D162).
		if n == len(parts)-1 && strings.TrimSpace(p) == "" {
			continue
		}
		result = append(result, unquoteScalar(strings.TrimSpace(p)))
	}
	return result
}

// splitFlowList separates a small YAML flow list while respecting quoted
// scalars emitted by serializeEntry. It intentionally supports only the OKF
// string subset, not general YAML.
func splitFlowList(inner string) []string {
	var parts []string
	start := 0
	quoted := byte(0)
	escaped := false
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		if quoted != 0 {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' && quoted == '"' {
				escaped = true
				continue
			}
			if c == quoted {
				quoted = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quoted = c
			continue
		}
		if c == ',' {
			parts = append(parts, inner[start:i])
			start = i + 1
		}
	}
	parts = append(parts, inner[start:])
	return parts
}

// unquoteScalar removes outer quotes if present ("..." or '...').
func unquoteScalar(s string) string {
	if len(s) >= 2 {
		if s[0] == '"' && s[len(s)-1] == '"' {
			if unquoted, err := strconv.Unquote(s); err == nil {
				return unquoted
			}
			return s[1 : len(s)-1]
		}
		if s[0] == '\'' && s[len(s)-1] == '\'' {
			return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
		}
	}
	return s
}

// Serialize generates the YAML text of the frontmatter in insertion order,
// preserving original comments. Does not include the --- delimiters.
// []string values are always serialized as flow lists [a, b, c].
// Blank lines before the first key are dropped (D309): a written file never
// starts with "---\n\n". A real comment there is kept.
func (fm *Frontmatter) Serialize() string {
	var sb strings.Builder
	leading := true
	for _, e := range fm.entries {
		if leading && e.kind == entryCommentKind && strings.TrimSpace(e.raw) == "" {
			continue
		}
		leading = false
		switch e.kind {
		case entryCommentKind:
			sb.WriteString(e.raw)
			sb.WriteByte('\n')
		case entryKeyKind:
			if v, ok := e.value.(string); ok && e.block != "" && v == e.blockValue {
				block := e.block
				if e.comment != "" {
					if nl := strings.IndexByte(block, '\n'); nl >= 0 {
						block = block[:nl] + e.comment + block[nl:]
					} else {
						block += e.comment
					}
				}
				sb.WriteString(e.key + ": " + block + "\n")
				continue
			}
			line := mustSerializeEntry(e.key, e.value)
			if e.comment != "" {
				if nl := strings.IndexByte(line, '\n'); nl >= 0 {
					line = line[:nl] + e.comment + line[nl:]
				}
			}
			sb.WriteString(line)
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// mustSerializeEntry panics on a value serializeEntry refuses: Serialize has
// no error to return and silently dropping or mis-rendering a key loses data.
// An unsupported type is a programming error (the write tools refuse nested
// values and WriteConcept calls Validate, but other paths serialize without
// it), so tests catch it (D1313).
func mustSerializeEntry(key string, value interface{}) string {
	line, err := serializeEntry(key, value)
	if err != nil {
		panic(fmt.Sprintf("okf: frontmatter key %q holds unsupported type %T", key, value))
	}
	return line
}

// ErrNestedFrontmatter marks a frontmatter value the flat OKF subset cannot
// hold: a map, or a list holding a map or a list (D1313).
var ErrNestedFrontmatter = errors.New("invalid_frontmatter")

// CheckValue reports whether value is one serializeEntry can write. Scalars
// and lists of scalars pass; anything nested is refused naming the key.
func CheckValue(key string, value interface{}) error {
	_, err := serializeEntry(key, value)
	return err
}

// CheckKey reports whether key is one serializeEntry writes and ParseFrontmatter
// reads back unchanged (D1558): serializeEntry emits "key: ", so a key holding
// ':', '#', a line break, edge whitespace or a "- " prefix reads back as another
// key, a comment or a continuation, or writes keys the caller never passed.
func CheckKey(key string) error {
	switch {
	case key == "":
		return fmt.Errorf("%w: empty frontmatter key", ErrNestedFrontmatter)
	case strings.ContainsAny(key, ":#\n\r"):
		return fmt.Errorf("%w: key %q: a frontmatter key cannot contain ':', '#' or a line break", ErrNestedFrontmatter, key)
	case strings.TrimSpace(key) != key:
		return fmt.Errorf("%w: key %q: a frontmatter key cannot start or end with whitespace", ErrNestedFrontmatter, key)
	case strings.HasPrefix(key, "- ") || key == "-":
		return fmt.Errorf("%w: key %q: a frontmatter key cannot start with \"- \"", ErrNestedFrontmatter, key)
	}
	return nil
}

// Validate runs CheckKey and CheckValue over every key; the write path calls it
// before serializing (D1313).
func (fm *Frontmatter) Validate() error {
	for _, e := range fm.entries {
		if e.kind != entryKeyKind {
			continue
		}
		if !e.parsed {
			if err := CheckKey(e.key); err != nil {
				return err
			}
		}
		if err := CheckValue(e.key, e.value); err != nil {
			return err
		}
	}
	return nil
}

// VerifyRoundTrip reports whether serialized frontmatter (the output of
// Serialize) parses back. The write path calls it before a concept reaches
// disk (D309), so a serialisation regression rejects the write instead of
// leaving a file no tool can read.
func VerifyRoundTrip(serialized string) error {
	if _, err := ParseFrontmatter(serialized); err != nil {
		return fmt.Errorf("frontmatter round-trip check failed: %w", err)
	}
	return nil
}

// CanonicalString generates YAML text with keys sorted alphabetically
// and without comments. Used for deterministic content-hash computation.
func (fm *Frontmatter) CanonicalString() string {
	keys := fm.Keys()
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		idx := fm.index[k]
		sb.WriteString(mustSerializeEntry(k, fm.entries[idx].value))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// serializeEntry formats a key-value pair as a YAML line. A type it does not
// know is an error, never a "%v" rendering (D1313): a map printed that way
// reads back as a string and the data is silently lost.
func serializeEntry(key string, value interface{}) (string, error) {
	switch v := value.(type) {
	case nil:
		return key + ":\n", nil
	case string:
		return key + ": " + serializeScalar(v) + "\n", nil
	case Block:
		return key + ":\n" + string(v) + "\n", nil
	case []string:
		items := make([]string, len(v))
		for i, item := range v {
			items[i] = serializeScalar(item)
		}
		return key + ": [" + strings.Join(items, ", ") + "]\n", nil
	case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return key + ": " + fmt.Sprintf("%v", v) + "\n", nil
	default:
		return "", fmt.Errorf("%w: key %q: nested values (a map, or a list holding a map or a list) are not supported; flatten the keys or put the structure in the body", ErrNestedFrontmatter, key)
	}
}

// serializeScalar keeps ordinary frontmatter readable while quoting values
// that could otherwise change YAML structure. strconv.Quote also turns literal
// newlines into \n escapes, which ParseFrontmatter decodes through unquoteScalar.
//
// The character set must stay in sync with the quote handling of
// splitFlowList and collectFlowList:
// every byte they treat as a delimiter or a quote (',', '"', '\”)
// is quoted here, or a flow-list item does not re-parse (D309: an unquoted
// "dall'operatore" opened a single-quoted string that never closed).
func serializeScalar(value string) string {
	if value == "" || strings.ContainsAny(value, ":,[]{}#\n\r\t\\\"'") || strings.HasPrefix(value, "-") || strings.TrimSpace(value) != value {
		return strconv.Quote(value)
	}
	return value
}

// Get returns the value associated with the key, and true if the key exists.
func (fm *Frontmatter) Get(key string) (interface{}, bool) {
	idx, ok := fm.index[key]
	if !ok {
		return nil, false
	}
	return fm.entries[idx].value, true
}

// Set sets the value for a key. If the key already exists, updates the value
// in place preserving the original position; otherwise appends the key at the end.
//
// Overwriting a key also drops the orphaned continuation lines of its old
// value (D309, see pruneContinuations): the new value replaces them.
func (fm *Frontmatter) Set(key string, value interface{}) {
	if idx, exists := fm.index[key]; exists {
		fm.entries[idx].value = value
		fm.entries[idx].comment = ""
		fm.pruneContinuations(idx)
		return
	}
	fm.index[key] = len(fm.entries)
	fm.entries = append(fm.entries, entry{kind: entryKeyKind, key: key, value: value})
}

// Delete removes the key from the frontmatter. It is a no-op if the key does not exist.
func (fm *Frontmatter) Delete(key string) {
	idx, ok := fm.index[key]
	if !ok {
		return
	}
	fm.pruneContinuations(idx)
	fm.entries = append(fm.entries[:idx], fm.entries[idx+1:]...)
	delete(fm.index, key)
	// Update indices of keys that were shifted.
	for k, i := range fm.index {
		if i > idx {
			fm.index[k] = i - 1
		}
	}
}

// pruneContinuations removes the comment entries right after the key at idx
// that were lines of its old value: "provenance: text" followed by
// "  - item" lines parses the scalar and keeps the items as unrecognizable
// lines (comments), which Serialize would print after the new value as
// malformed YAML (D309). The run removed is the indented lines (and the blank
// lines among them) up to the last indented one; it stops at the next key or
// at a comment starting at column 0, so a real "# comment" is never removed
// and blank lines that only separate keys stay.
func (fm *Frontmatter) pruneContinuations(idx int) {
	end := idx + 1 // one past the last indented continuation
	for j := idx + 1; j < len(fm.entries); j++ {
		e := fm.entries[j]
		if e.kind != entryCommentKind {
			break
		}
		if strings.TrimSpace(e.raw) == "" {
			continue
		}
		if !strings.HasPrefix(e.raw, " ") && !strings.HasPrefix(e.raw, "\t") {
			break
		}
		end = j + 1
	}
	n := end - (idx + 1)
	if n == 0 {
		return
	}
	fm.entries = append(fm.entries[:idx+1], fm.entries[end:]...)
	for k, i := range fm.index {
		if i > idx {
			fm.index[k] = i - n
		}
	}
}

// Rename moves the value of oldKey to newKey keeping its position. It reports
// false, changing nothing, when oldKey is absent or newKey already exists.
func (fm *Frontmatter) Rename(oldKey, newKey string) bool {
	idx, ok := fm.index[oldKey]
	if !ok {
		return false
	}
	if _, taken := fm.index[newKey]; taken {
		return false
	}
	fm.entries[idx].key = newKey
	fm.entries[idx].parsed = false
	delete(fm.index, oldKey)
	fm.index[newKey] = idx
	return true
}

// Keys returns the keys in insertion order (excludes comments).
func (fm *Frontmatter) Keys() []string {
	var keys []string
	for _, e := range fm.entries {
		if e.kind == entryKeyKind {
			keys = append(keys, e.key)
		}
	}
	return keys
}

// Type is a shortcut to retrieve the "type" field as a string.
// Returns "" if the field is absent or the value is not a string.
func (fm *Frontmatter) Type() string {
	v, ok := fm.Get("type")
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}
