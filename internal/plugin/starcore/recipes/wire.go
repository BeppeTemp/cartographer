package recipes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"go.starlark.net/starlark"

	"github.com/BeppeTemp/cartographer/internal/plugin/starcore"
)

func failed(format string, a ...any) error {
	return &starcore.CallError{Kind: starcore.Failed, Msg: fmt.Sprintf(format, a...)}
}

// wire writes the dict a verb returned as the lines an exec recipe prints for
// it; the recipe package's parser judges them, so a Starlark result is held to
// the caps, key rules and codes of the exec protocol. This function only
// changes the shape: it checks which keys exist and their types, nothing of
// what the values may be.
func wire(ctx context.Context, verb string, v starlark.Value) ([]byte, error) {
	var raw bytes.Buffer
	if err := starcore.EncodeJSON(ctx, &raw, v, 0); err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw.Bytes(), &m); err != nil || m == nil {
		return nil, failed("%s returns a dict", verb)
	}
	take := func(key string, into any) error {
		b, ok := m[key]
		if !ok {
			return nil
		}
		delete(m, key)
		if err := json.Unmarshal(b, into); err != nil {
			return failed("%s: %s has the wrong type", verb, key)
		}
		return nil
	}
	var out bytes.Buffer
	line := func(key string, val any) {
		b, _ := json.Marshal(map[string]any{key: val})
		out.Write(b)
		out.WriteByte('\n')
	}
	switch verb {
	case "spec", "check":
		// The dict is the object itself: check() returns {"ok": ..., ...}.
		line(verb, m)
		m = nil
	case "list":
		var entries []json.RawMessage
		var deletes []string
		var cursor string
		if err := firstErr(take("entries", &entries), take("deletes", &deletes), take("next_cursor", &cursor)); err != nil {
			return nil, err
		}
		for _, e := range entries {
			line("entry", e)
		}
		for _, k := range deletes {
			line("delete", map[string]string{"key": k})
		}
		line("next_cursor", cursor)
	case "fetch":
		var contents map[string]string
		if err := take("contents", &contents); err != nil {
			return nil, err
		}
		keys := make([]string, 0, len(contents))
		for k := range contents {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			line("content", map[string]string{"key": k, "content": contents[k]})
		}
		line("done", true)
	default:
		return nil, &starcore.CallError{Kind: starcore.Internal, Msg: "unknown verb " + verb}
	}
	if len(m) > 0 {
		for k := range m {
			return nil, failed("%s: unknown key %q in the result", verb, k)
		}
	}
	return out.Bytes(), nil
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
