// Package jsonorder edits a JSON object without reformatting the rest: the key
// order of every object read, the indent unit (tabs included), CRLF, the final
// newline and the exact digits of a number (json.Number) survive a
// read-modify-write (#1198, #1502), and HTML characters are not
// escaped (`&&` in another tool's command stays as written). It is for files
// shared with the user or with other tools (a client's settings, MCP config,
// hooks.json); a file Cartographer owns whole may use plain encoding/json.
//
// Objects are remembered by map identity: edits modify the decoded maps in
// place, so a surviving object keeps its order and a key added to it is written
// after them, sorted.
package jsonorder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Layout is what an ordinary map round trip of a JSON object loses.
type Layout struct {
	order map[uintptr][]string
	// live holds every decoded object, so no address in order can be reused
	// by a map allocated after the decode.
	live       []map[string]interface{}
	indent     string
	trailingNL bool
	crlf       bool
}

// NewLayout is the layout of a file that does not exist yet: two-space indent,
// final newline, LF.
func NewLayout() *Layout {
	return &Layout{order: map[uintptr][]string{}, indent: "  ", trailingNL: true}
}

// maxJSONDepth bounds the nesting a file may have to be rewritten. A client
// settings file is a few levels deep; the encoder indents every level, so output
// grows with the square of the depth, and a hostile file in a repository (18 KB
// of nested arrays became 162 MB) must be refused, never rewritten. Parse fails,
// so the caller leaves the file untouched.
const maxJSONDepth = 64

// maxIndent bounds the indent unit taken from a file: a longer one, or one that is
// not spaces and tabs, falls back to two spaces.
const maxIndent = 16

// Parse decodes data, a JSON object, into maps (numbers kept as json.Number)
// and records its layout. An empty-null document is an empty object.
func Parse(data []byte) (map[string]interface{}, *Layout, error) {
	layout := NewLayout()
	// json.Valid first: it is iterative and refuses what json.Unmarshal would
	// (trailing data, nesting past its depth limit) before decodeOrdered recurses.
	if !json.Valid(data) {
		return nil, nil, errors.New("not valid JSON")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeOrdered(dec, layout, 0)
	m, ok := v.(map[string]interface{})
	if err == nil && !ok && v != nil {
		err = errors.New("not a JSON object")
	}
	if err != nil {
		return nil, nil, err
	}
	if m == nil {
		m = map[string]interface{}{}
	}
	layout.indent = detectIndent(data)
	layout.trailingNL = bytes.HasSuffix(data, []byte("\n"))
	layout.crlf = bytes.Contains(data, []byte("\r\n"))
	return m, layout, nil
}

// Marshal serializes m in layout.
func Marshal(m map[string]interface{}, layout *Layout) ([]byte, error) {
	var buf bytes.Buffer
	if err := encodeOrdered(&buf, m, layout, 0); err != nil {
		return nil, err
	}
	if layout.trailingNL {
		buf.WriteByte('\n')
	}
	data := buf.Bytes()
	if layout.crlf {
		data = bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n"))
	}
	return data, nil
}

// decodeOrdered reads one JSON value from dec, recording the key order of every
// object into layout.
func decodeOrdered(dec *json.Decoder, layout *Layout, depth int) (interface{}, error) {
	if depth > maxJSONDepth {
		return nil, fmt.Errorf("nested more than %d levels, not rewritten", maxJSONDepth)
	}
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			m := map[string]interface{}{}
			var keys []string
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := kt.(string)
				if !ok {
					return nil, fmt.Errorf("object key %v is not a string", kt)
				}
				val, err := decodeOrdered(dec, layout, depth+1)
				if err != nil {
					return nil, err
				}
				if _, dup := m[key]; !dup {
					keys = append(keys, key)
				}
				m[key] = val
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			layout.order[reflect.ValueOf(m).Pointer()] = keys
			layout.live = append(layout.live, m)
			return m, nil
		case '[':
			arr := []interface{}{}
			for dec.More() {
				val, err := decodeOrdered(dec, layout, depth+1)
				if err != nil {
					return nil, err
				}
				arr = append(arr, val)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return arr, nil
		}
		return nil, fmt.Errorf("unexpected delimiter %v", t)
	default:
		return t, nil
	}
}

// detectIndent returns the leading whitespace of the first indented line of
// data, the file's own indent unit; two spaces when it has none.
func detectIndent(data []byte) string {
	for _, line := range strings.Split(string(data), "\n")[1:] {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed != "" && len(trimmed) < len(line) {
			indent := line[:len(line)-len(trimmed)]
			if len(indent) > maxIndent {
				return "  "
			}
			return indent
		}
	}
	return "  "
}

func encodeOrdered(buf *bytes.Buffer, v interface{}, layout *Layout, depth int) error {
	if depth > maxJSONDepth {
		return fmt.Errorf("nested more than %d levels, not written", maxJSONDepth)
	}
	newline := func(d int) {
		buf.WriteByte('\n')
		buf.WriteString(strings.Repeat(layout.indent, d))
	}
	switch t := v.(type) {
	case map[string]interface{}:
		if len(t) == 0 {
			buf.WriteString("{}")
			return nil
		}
		buf.WriteByte('{')
		for i, k := range orderedKeys(t, layout) {
			if i > 0 {
				buf.WriteByte(',')
			}
			newline(depth + 1)
			if err := encodeScalar(buf, k); err != nil {
				return err
			}
			buf.WriteString(": ")
			if err := encodeOrdered(buf, t[k], layout, depth+1); err != nil {
				return err
			}
		}
		newline(depth)
		buf.WriteByte('}')
	case []interface{}:
		if len(t) == 0 {
			buf.WriteString("[]")
			return nil
		}
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			newline(depth + 1)
			if err := encodeOrdered(buf, e, layout, depth+1); err != nil {
				return err
			}
		}
		newline(depth)
		buf.WriteByte(']')
	default:
		return encodeScalar(buf, t)
	}
	return nil
}

// orderedKeys lists m's keys: the recorded order of an object read from the
// file, minus the keys removed since, then every other key sorted.
func orderedKeys(m map[string]interface{}, layout *Layout) []string {
	recorded := layout.order[reflect.ValueOf(m).Pointer()]
	keys := make([]string, 0, len(m))
	seen := make(map[string]bool, len(m))
	for _, k := range recorded {
		if _, ok := m[k]; ok && !seen[k] {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	var rest []string
	for k := range m {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(keys, rest...)
}

func encodeScalar(buf *bytes.Buffer, v interface{}) error {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	buf.Write(bytes.TrimSuffix(b.Bytes(), []byte("\n")))
	return nil
}
