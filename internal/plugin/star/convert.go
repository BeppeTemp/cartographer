package star

import (
	"encoding/json"
	"fmt"
	"sort"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

// toStarlark converts decoded JSON (numbers as json.Number or float64) and the
// plain Go values a host passes (int, int64, string, bool, slices and maps).
func toStarlark(v any) (starlark.Value, error) {
	switch x := v.(type) {
	case nil:
		return starlark.None, nil
	case string:
		return starlark.String(x), nil
	case bool:
		return starlark.Bool(x), nil
	case int:
		return starlark.MakeInt(x), nil
	case int64:
		return starlark.MakeInt64(x), nil
	case float64:
		return starlark.Float(x), nil
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return starlark.MakeInt64(i), nil
		}
		f, err := x.Float64()
		if err != nil {
			return nil, fmt.Errorf("number %s: %w", x, err)
		}
		return starlark.Float(f), nil
	case []string:
		l := make([]starlark.Value, len(x))
		for i, s := range x {
			l[i] = starlark.String(s)
		}
		return starlark.NewList(l), nil
	case []any:
		l := make([]starlark.Value, len(x))
		for i, e := range x {
			c, err := toStarlark(e)
			if err != nil {
				return nil, err
			}
			l[i] = c
		}
		return starlark.NewList(l), nil
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		d := starlark.NewDict(len(x))
		for _, k := range keys {
			c, err := toStarlark(x[k])
			if err != nil {
				return nil, err
			}
			if err := d.SetKey(starlark.String(k), c); err != nil {
				return nil, err
			}
		}
		return d, nil
	}
	return nil, fmt.Errorf("cannot pass a %T to a script", v)
}

// ctxStruct is the argument of an event handler: a struct of the event's fields.
func ctxStruct(fields map[string]any) (starlark.Value, error) {
	d := starlark.StringDict{}
	for k, v := range fields {
		c, err := toStarlark(v)
		if err != nil {
			return nil, fmt.Errorf("event field %q: %w", k, err)
		}
		d[k] = c
	}
	return starlarkstruct.FromStringDict(starlark.String("ctx"), d), nil
}
