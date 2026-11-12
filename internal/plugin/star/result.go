package star

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"go.starlark.net/starlark"
)

const maxJSONDepth = 100

// encodeJSON writes v as compact JSON and stops with LimitExceeded as soon as
// the output passes MaxResultBytes, so shared substructure (x = [x, x] again
// and again) costs a bounded amount of work; it also honours the call's context.
func encodeJSON(ctx context.Context, w *bytes.Buffer, v starlark.Value, depth int) error {
	if w.Len() > MaxResultBytes {
		return &CallError{Kind: LimitExceeded, Msg: fmt.Sprintf("the result is over the %d byte cap", MaxResultBytes)}
	}
	if err := ctx.Err(); err != nil {
		return ctxError(err)
	}
	if depth > maxJSONDepth {
		return &CallError{Kind: Failed, Msg: "the result is nested too deeply or contains itself"}
	}
	bad := func() error {
		return &CallError{Kind: Failed, Msg: "the result is not JSON-able: " + v.Type()}
	}
	switch x := v.(type) {
	case starlark.NoneType:
		w.WriteString("null")
	case starlark.Bool:
		fmt.Fprint(w, bool(x))
	case starlark.Int:
		w.WriteString(x.String())
	case starlark.Float:
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return &CallError{Kind: Failed, Msg: "the result is not JSON-able: a float that is not finite"}
		}
		b, _ := json.Marshal(f)
		w.Write(b)
	case starlark.String:
		writeJSONString(w, string(x))
	case *starlark.List:
		w.WriteByte('[')
		for i := 0; i < x.Len(); i++ {
			if i > 0 {
				w.WriteByte(',')
			}
			if err := encodeJSON(ctx, w, x.Index(i), depth+1); err != nil {
				return err
			}
		}
		w.WriteByte(']')
	case starlark.Tuple:
		w.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				w.WriteByte(',')
			}
			if err := encodeJSON(ctx, w, e, depth+1); err != nil {
				return err
			}
		}
		w.WriteByte(']')
	case *starlark.Dict:
		keys := make([]string, 0, x.Len())
		vals := map[string]starlark.Value{}
		for _, kv := range x.Items() {
			k, ok := starlark.AsString(kv[0])
			if !ok {
				return &CallError{Kind: Failed, Msg: "the result is not JSON-able: a dict key of type " + kv[0].Type()}
			}
			keys = append(keys, k)
			vals[k] = kv[1]
		}
		sort.Strings(keys)
		w.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				w.WriteByte(',')
			}
			writeJSONString(w, k)
			w.WriteByte(':')
			if err := encodeJSON(ctx, w, vals[k], depth+1); err != nil {
				return err
			}
		}
		w.WriteByte('}')
	default:
		return bad()
	}
	return nil
}

func writeJSONString(w *bytes.Buffer, s string) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	w.Truncate(w.Len() - 1) // Encode ends with a newline
}

func capText(s string) (string, error) {
	if len(s) > MaxResultBytes {
		return "", &CallError{Kind: LimitExceeded, Msg: fmt.Sprintf("the result is %d bytes, over the %d cap", len(s), MaxResultBytes)}
	}
	return s, nil
}
