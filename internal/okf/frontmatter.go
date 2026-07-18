// Package okf — parser/serializer for the YAML subset used in OKF frontmatter.
// Parsing and serialization are implemented using Go's standard library only.
package okf

import (
	"fmt"
	"sort"
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
}

// Frontmatter is an ordered map representing an OKF frontmatter block.
// Preserves key order and the position of original comments.
// Supported values: string, []string, nil.
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
			} else {
				value = nil
				i++
			}

		case strings.HasPrefix(valueRaw, "["):
			// Flow list: [a, b, c]
			closing := strings.LastIndex(valueRaw, "]")
			if closing < 0 {
				return nil, fmt.Errorf("frontmatter: unclosed flow list at key %q", key)
			}
			inner := valueRaw[1:closing]
			value = parseFlowList(inner)
			i++

		default:
			// scalar
			value = unquoteScalar(valueRaw)
			i++
		}

		// Duplicate key: overwrite the existing value (standard YAML behavior).
		if idx, exists := fm.index[key]; exists {
			fm.entries[idx].value = value
		} else {
			fm.index[key] = len(fm.entries)
			fm.entries = append(fm.entries, entry{kind: entryKeyKind, key: key, value: value})
		}
	}

	return fm, nil
}

// parseFlowList parses the inner part of a YAML flow list (without the square brackets).
func parseFlowList(inner string) []string {
	if strings.TrimSpace(inner) == "" {
		return []string{}
	}
	parts := strings.Split(inner, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		result = append(result, unquoteScalar(strings.TrimSpace(p)))
	}
	return result
}

// unquoteScalar removes outer quotes if present ("..." or '...').
func unquoteScalar(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') ||
			(s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// Serialize generates the YAML text of the frontmatter in insertion order,
// preserving original comments. Does not include the --- delimiters.
// []string values are always serialized as flow lists [a, b, c].
func (fm *Frontmatter) Serialize() string {
	var sb strings.Builder
	for _, e := range fm.entries {
		switch e.kind {
		case entryCommentKind:
			sb.WriteString(e.raw)
			sb.WriteByte('\n')
		case entryKeyKind:
			sb.WriteString(serializeEntry(e.key, e.value))
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// CanonicalString generates YAML text with keys sorted alphabetically
// and without comments. Used for deterministic content-hash computation.
func (fm *Frontmatter) CanonicalString() string {
	keys := fm.Keys()
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		idx := fm.index[k]
		sb.WriteString(serializeEntry(k, fm.entries[idx].value))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// serializeEntry formats a key-value pair as a YAML line.
func serializeEntry(key string, value interface{}) string {
	switch v := value.(type) {
	case nil:
		return key + ":\n"
	case string:
		return key + ": " + v + "\n"
	case []string:
		return key + ": [" + strings.Join(v, ", ") + "]\n"
	default:
		return key + ": " + fmt.Sprintf("%v", v) + "\n"
	}
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
func (fm *Frontmatter) Set(key string, value interface{}) {
	if idx, exists := fm.index[key]; exists {
		fm.entries[idx].value = value
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
	fm.entries = append(fm.entries[:idx], fm.entries[idx+1:]...)
	delete(fm.index, key)
	// Update indices of keys that were shifted.
	for k, i := range fm.index {
		if i > idx {
			fm.index[k] = i - 1
		}
	}
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
