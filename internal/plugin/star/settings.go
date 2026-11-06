package star

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/plugin"
	"go.starlark.net/starlark"
)

// settingsValue is the `settings` name: a read-only mapping and attribute
// access (settings.token, settings["token"]).
//
// A read like settings.x reaches Attr, which has no thread, so unlike the
// functions of the other modules it cannot find the context of the call and
// be late-bound through it; and a function keeps the predeclared names of the
// run that created it, so a later run cannot swap the value either. Two runs
// of plugin.star therefore exist: the first (Load) binds a closed value that
// refuses every read, because what a plugin registers cannot depend on a
// setting (a setting changes without a new approval); a CALL runs plugin.star
// again, privately, binding a value that holds this call's typed settings,
// opens it once the script has loaded, and calls the impl of that run. The
// registered set cannot differ, because the sources are the same and LOAD reads
// no clock, setting or file. Nothing is shared between calls, so they run
// concurrently.
type settingsValue struct {
	vals map[string]starlark.Value
	keys []string // the manifest's keys, sorted
	open bool
}

var (
	_ starlark.HasAttrs = (*settingsValue)(nil)
	_ starlark.Mapping  = (*settingsValue)(nil)
)

func (*settingsValue) String() string        { return "<settings>" }
func (*settingsValue) Type() string          { return "settings" }
func (*settingsValue) Freeze()               {}
func (*settingsValue) Truth() starlark.Bool  { return true }
func (*settingsValue) Hash() (uint32, error) { return 0, errors.New("unhashable type: settings") }
func (s *settingsValue) AttrNames() []string { return slices.Clone(s.keys) }

func (s *settingsValue) Attr(name string) (starlark.Value, error) { return s.get(name) }

func (s *settingsValue) Get(k starlark.Value) (starlark.Value, bool, error) {
	key, ok := starlark.AsString(k)
	if !ok {
		return nil, false, fmt.Errorf("settings keys are strings, got %s", k.Type())
	}
	v, err := s.get(key)
	if err != nil {
		return nil, false, err
	}
	return v, true, nil
}

func (s *settingsValue) get(name string) (starlark.Value, error) {
	if !s.open {
		return nil, fmt.Errorf("settings.%s is not available while plugin.star loads: a setting can change without a new approval, so what a plugin registers cannot read one", name)
	}
	v, ok := s.vals[name]
	if !ok {
		if len(s.keys) == 0 {
			return nil, fmt.Errorf("settings.%s: the manifest declares no settings", name)
		}
		return nil, fmt.Errorf("settings.%s: no such setting, the manifest declares %s", name, strings.Join(s.keys, ", "))
	}
	return v, nil
}

// newSettings types the user's values by the manifest's declarations, applies
// the defaults, and leaves a key with neither as None. A value of the wrong
// type, or for a key the manifest does not declare, is an error.
func newSettings(decl map[string]plugin.Setting, values map[string]any) (*settingsValue, error) {
	s := &settingsValue{vals: map[string]starlark.Value{}}
	for k := range decl {
		s.keys = append(s.keys, k)
	}
	sort.Strings(s.keys)
	for k := range values {
		if _, ok := decl[k]; !ok {
			return nil, fmt.Errorf("setting %q is not declared by the manifest", k)
		}
	}
	for _, k := range s.keys {
		raw, set := values[k]
		if !set {
			raw = decl[k].Default
		}
		if raw == nil {
			s.vals[k] = starlark.None
			continue
		}
		v, err := typed(decl[k].Type, raw)
		if err != nil {
			return nil, fmt.Errorf("setting %q: %w", k, err)
		}
		s.vals[k] = v
	}
	return s, nil
}

func typed(typ string, raw any) (starlark.Value, error) {
	switch typ {
	case "string":
		if v, ok := raw.(string); ok {
			return starlark.String(v), nil
		}
	case "bool":
		if v, ok := raw.(bool); ok {
			return starlark.Bool(v), nil
		}
	case "int":
		switch v := raw.(type) {
		case int:
			return starlark.MakeInt(v), nil
		case int64:
			return starlark.MakeInt64(v), nil
		case uint64:
			return starlark.MakeUint64(v), nil
		case float64:
			if v == math.Trunc(v) && math.Abs(v) < 1<<53 {
				return starlark.MakeInt64(int64(v)), nil
			}
		case json.Number:
			if i, err := v.Int64(); err == nil {
				return starlark.MakeInt64(i), nil
			}
		}
	}
	return nil, fmt.Errorf("%v is not a %s", raw, typ)
}
