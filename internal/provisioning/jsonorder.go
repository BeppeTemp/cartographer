package provisioning

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
)

// jsonLayout is what an ordinary map round trip of a JSON object loses: the key
// order of every object read, the indent unit and whether the file ended with a
// newline (#1198). Claude Code's settings.json is a file a team may version, so
// patching a hook into it must not reorder the keys around it or drop its final
// newline. Objects are remembered by map identity: the edits (upsertHookEntry,
// stripHookEntries) modify the decoded maps in place, so a surviving object
// keeps its order and a new one is written with sorted keys, as before.
type jsonLayout struct {
	order map[uintptr][]string
	// live holds every decoded object, so no address in order can be reused
	// by a map allocated after the decode.
	live       []map[string]interface{}
	indent     string
	trailingNL bool
}

// loadOrderedJSONObject is loadJSONObject plus the layout saveOrderedJSONObject
// writes back. Numbers are kept as written (json.Number). A missing file is an
// empty object, written with a two-space indent and a final newline.
func loadOrderedJSONObject(path string) (map[string]interface{}, *jsonLayout, error) {
	layout := &jsonLayout{order: map[uintptr][]string{}, indent: "  ", trailingNL: true}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]interface{}{}, layout, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("provisioning: read %s: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeOrdered(dec, layout)
	if err == nil {
		if _, extra := dec.Token(); extra != io.EOF {
			err = errors.New("trailing data after the top-level value")
		}
	}
	if err != nil {
		return nil, nil, fmt.Errorf("provisioning: parse %s: %w", path, err)
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		if v != nil {
			return nil, nil, fmt.Errorf("provisioning: parse %s: not a JSON object", path)
		}
		m = map[string]interface{}{}
	}
	layout.indent = detectIndent(data)
	layout.trailingNL = bytes.HasSuffix(data, []byte("\n"))
	return m, layout, nil
}

// decodeOrdered reads one JSON value from dec, recording the key order of every
// object into layout.
func decodeOrdered(dec *json.Decoder, layout *jsonLayout) (interface{}, error) {
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
				val, err := decodeOrdered(dec, layout)
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
				val, err := decodeOrdered(dec, layout)
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
			return line[:len(line)-len(trimmed)]
		}
	}
	return "  "
}

// saveOrderedJSONObject is saveJSONObject writing m in layout: the keys of an
// object read from the file in their original order (a key added to it goes
// after them, sorted), the file's indent and its final newline. HTML characters
// are not escaped: a hook command's `&&` stays as written.
func saveOrderedJSONObject(baseDir, path string, m map[string]interface{}, layout *jsonLayout) error {
	var buf bytes.Buffer
	if err := encodeOrdered(&buf, m, layout, 0); err != nil {
		return fmt.Errorf("provisioning: serialize %s: %w", path, err)
	}
	if layout.trailingNL {
		buf.WriteByte('\n')
	}
	return writeJSONFile(baseDir, path, buf.Bytes())
}

func encodeOrdered(buf *bytes.Buffer, v interface{}, layout *jsonLayout, depth int) error {
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
func orderedKeys(m map[string]interface{}, layout *jsonLayout) []string {
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
