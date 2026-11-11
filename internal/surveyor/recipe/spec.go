package recipe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"slices"
	"sort"
	"unicode/utf8"
)

// The verbs of protocol v2, as a script declares them in spec.verbs.
const (
	VerbSpec  = "spec"
	VerbCheck = "check"
	VerbList  = "list"
	VerbFetch = "fetch"
)

var (
	verbs        = []string{VerbSpec, VerbCheck, VerbList, VerbFetch}
	settingTypes = []string{"string", "int", "number", "bool"}
	settingRE    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
	envNameRE    = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
)

// Spec is what a script says about itself (the spec verb): a description for
// the operator and the author, never an instruction to the Surveyor, which reads
// its configuration from its own file only.
type Spec struct {
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Shapes      []string               `json:"shapes"`
	Verbs       []string               `json:"verbs"`
	Settings    map[string]SettingSpec `json:"settings"`
	Env         []string               `json:"env"`
}

// SettingSpec is one entry of spec.settings: a typed scalar the operator may set
// under recipes.<name>.settings.
type SettingSpec struct {
	Type        string `json:"type"`
	Default     any    `json:"default"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

// Declares reports whether the script declared the verb.
func (s Spec) Declares(verb string) bool { return slices.Contains(s.Verbs, verb) }

// Spec runs the spec verb. A script that does not implement it answers
// ErrUnsupported; one that does is held to the shape of a spec (spec_invalid).
func (e *execRecipe) Spec(ctx context.Context) (Spec, error) {
	body, err := json.Marshal(e.base(VerbSpec, nil))
	if err != nil {
		return Spec{}, fail(CodeSpecInvalid, "cannot encode the request")
	}
	var sp Spec
	err = e.runScript(ctx, CodeSpecInvalid, body, func(r io.Reader) (bool, error) { return readAnswer(r, "spec", &sp) })
	var re *Error
	switch {
	case ctx.Err() != nil:
		return Spec{}, ctx.Err()
	case errors.Is(err, ErrUnsupported):
		return Spec{}, ErrUnsupported
	case errors.As(err, &re):
		return Spec{}, fail(CodeSpecInvalid, "%s", re.Detail)
	case err != nil:
		return Spec{}, err
	}
	// Everything a script prints is untrusted text on its way to the operator's
	// terminal, the Atlas and a feed_error: an error quotes it, so it goes through
	// the same redaction and control-character strip as a check's detail.
	if err := sp.validate(); err != nil {
		return Spec{}, fail(CodeSpecInvalid, "%s", e.label(err.Error(), maxDetail))
	}
	if !slices.Contains(sp.Shapes, e.Shape()) {
		return Spec{}, fail(CodeSpecInvalid, "the recipe is configured as shape %s but its spec serves %v", e.Shape(), e.label(fmt.Sprint(sp.Shapes), maxDetail))
	}
	sp.Title, sp.Description = e.label(sp.Title, maxSpecTitle), e.label(sp.Description, maxSpecText)
	if sp.Title == "" {
		return Spec{}, fail(CodeSpecInvalid, "title is empty")
	}
	for name, st := range sp.Settings {
		st.Description = e.label(st.Description, maxSpecText)
		if d, ok := st.Default.(string); ok {
			st.Default = e.label(d, maxSpecDefault)
		}
		sp.Settings[name] = st
	}
	return sp, nil
}

// validate holds a spec to its limits; every text is bounded, so a hostile
// script cannot make the Surveyor, the CLI or the Atlas carry more than that.
func (s Spec) validate() error {
	for _, f := range []struct {
		name, v string
		max     int
	}{{"title", s.Title, maxSpecTitle}, {"description", s.Description, maxSpecText}} {
		if n := utf8.RuneCountInString(f.v); f.v == "" && f.name == "title" || n > f.max || !utf8.ValidString(f.v) {
			return fmt.Errorf("%s is empty, over %d characters or not UTF-8", f.name, f.max)
		}
	}
	if len(s.Shapes) == 0 || len(s.Shapes) > 2 {
		return errors.New("shapes lists feed, system or both")
	}
	for _, sh := range s.Shapes {
		if sh != ShapeFeed && sh != ShapeSystem {
			return fmt.Errorf("shape %q is not feed or system", sh)
		}
	}
	if !s.Declares(VerbList) {
		return errors.New("verbs must declare list")
	}
	for _, v := range s.Verbs {
		if !slices.Contains(verbs, v) {
			return fmt.Errorf("verb %q is not one of %v", v, verbs)
		}
	}
	if len(s.Settings) > maxSpecEntries || len(s.Env) > maxSpecEntries {
		return fmt.Errorf("settings and env hold at most %d entries each", maxSpecEntries)
	}
	for name, st := range s.Settings {
		switch {
		case !settingRE.MatchString(name):
			return fmt.Errorf("setting name %q is not [a-z][a-z0-9_] up to 40 characters", name)
		case !slices.Contains(settingTypes, st.Type):
			return fmt.Errorf("setting %s has type %q, want one of %v", name, st.Type, settingTypes)
		case utf8.RuneCountInString(st.Description) > maxSpecText || !utf8.ValidString(st.Description):
			return fmt.Errorf("setting %s: description is over %d characters or not UTF-8", name, maxSpecText)
		case st.Default != nil && !scalarFits(st.Type, st.Default):
			return fmt.Errorf("setting %s: default is not a %s", name, st.Type)
		}
	}
	for _, n := range s.Env {
		if !envNameRE.MatchString(n) {
			return fmt.Errorf("env name %q is not an upper-case variable name", n)
		}
	}
	return nil
}

// CheckSettings holds an operator's recipes.<name>.settings to the spec: no
// setting the script did not declare, every required one present (or defaulted),
// each of its declared type. Only `source test` calls it: a config load never
// runs a script.
func (s Spec) CheckSettings(cfg map[string]any) error {
	var names []string
	for n := range cfg {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		st, ok := s.Settings[n]
		if !ok {
			return fmt.Errorf("setting %q is not one the recipe declares", n)
		}
		if !scalarFits(st.Type, cfg[n]) {
			return fmt.Errorf("setting %q must be a %s", n, st.Type)
		}
	}
	for n, st := range s.Settings {
		if _, set := cfg[n]; !set && st.Required && st.Default == nil {
			return fmt.Errorf("setting %q is required", n)
		}
	}
	return nil
}

// scalarFits reports whether v, decoded from YAML or JSON, is a value of the type.
func scalarFits(typ string, v any) bool {
	switch typ {
	case "string":
		_, ok := v.(string)
		return ok
	case "bool":
		_, ok := v.(bool)
		return ok
	case "int":
		switch x := v.(type) {
		case int, int64:
			return true
		case uint64:
			return x <= math.MaxInt64
		case float64:
			return math.Abs(x) <= 1<<53 && x == math.Trunc(x) // exact in a float, and NaN and Inf fail both tests
		}
	case "number":
		switch x := v.(type) {
		case int, int64:
			return true
		case uint64:
			return x <= math.MaxInt64
		case float64:
			return !math.IsNaN(x) && !math.IsInf(x, 0)
		}
	}
	return false
}
