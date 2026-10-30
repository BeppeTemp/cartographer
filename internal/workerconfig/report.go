package workerconfig

import (
	"math"
	"net/url"
	"regexp"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/settingschema"
	"gopkg.in/yaml.v3"
)

// Redacted is what a report shows in place of a value that must not be shown.
const Redacted = "<redacted>"

// Entry is one worker key with its effective value and origin ("file" or
// "default"), in the JSON shape of the server's settings entries (D22).
type Entry struct {
	Key         string `json:"key"`
	Value       any    `json:"value"`
	Origin      string `json:"origin"`
	Description string `json:"description"`
	Default     string `json:"default"`
	Type        string `json:"type,omitempty"`
	Values      string `json:"values,omitempty"`
}

// Template is the settings of a worker with no config file: every scalar key
// at its default. The Atlas shows it while no worker has reported.
func Template() []Entry {
	var out []Entry
	for _, k := range Schema.Keys {
		if k.Type == "list" {
			continue
		}
		e := Entry{Key: k.Path, Origin: "default", Description: k.Description, Default: k.Default, Type: k.Type, Values: k.Values}
		_ = yaml.Unmarshal([]byte(k.Default), &e.Value)
		out = append(out, e)
	}
	return out
}

// envName is what an `*_env` value may be: a variable name, optionally with (set)/(unset).
var envName = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*( ?\((set|unset)\))?)?$`)

var (
	nameRe   = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
	clockRe  = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	zoneRe   = regexp.MustCompile(`^[A-Za-z0-9_+/-]{1,40}$`)
	modelRe  = regexp.MustCompile(`^[A-Za-z0-9._:/-]{1,100}$`)
	kindRe   = regexp.MustCompile(`^[a-z0-9_-]{1,40}$`)
	enumItem = regexp.MustCompile(`^[a-z0-9_-]+$`)
)

// stringShape is the one place a plain `string` key is allowed on the page: only
// the keys listed here, each with the shape of what it holds. A string key not
// listed (an option a task kind adds) is shown as Redacted, because nothing says
// it is safe to show. models[].model is a model id, shown with a restricted
// character set; the API key it could be confused with is an `*_env` name.
var stringShape = map[string]*regexp.Regexp{
	"name":                     nameRe,
	"models[].name":            nameRe,
	"models[].model":           modelRe,
	"schedule.window.start":    clockRe,
	"schedule.window.end":      clockRe,
	"schedule.window.timezone": zoneRe,
	"tasks[].kind":             kindRe,
	"tasks[].model":            nameRe,
}

// unsafeURL: a URL that carries credentials, a query or a fragment, or that does
// not parse, is not shown. The authority is checked by hand as well, since
// `user:pw@host/x` parses as an opaque URL with no userinfo.
func unsafeURL(v string) bool {
	u, err := url.Parse(v)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(v, "?#") {
		return true
	}
	rest := v
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	return strings.Contains(rest, "@")
}

// enumOf returns the items of a Values text of the form "a | b", or nil.
func enumOf(values string) []string {
	var out []string
	for _, it := range strings.Split(values, " | ") {
		if !enumItem.MatchString(it) {
			return nil
		}
		out = append(out, it)
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

// shown returns the value an entry of the given schema key may carry, and
// whether the entry is kept at all. A value that does not match the key's type
// drops the entry; a string the table does not vouch for is Redacted.
func shown(k settingschema.Key, v any) (any, bool) {
	switch k.Type {
	case "bool":
		b, ok := v.(bool)
		return b, ok
	case "int":
		f, ok := v.(float64)
		return f, ok && f >= 0 && f <= 1<<40 && f == math.Trunc(f)
	case "env", "url", "string":
		s, ok := v.(string)
		switch {
		case !ok:
			return nil, false
		case s == "":
			return s, true
		case k.Type == "env":
			return s, envName.MatchString(s)
		case k.Type == "url":
			if unsafeURL(s) {
				return Redacted, true
			}
			return s, true
		}
		if items := enumOf(k.Values); items != nil {
			for _, it := range items {
				if s == it {
					return s, true
				}
			}
			return Redacted, true
		}
		if re := stringShape[k.Path]; re != nil && re.MatchString(s) {
			return s, true
		}
		return Redacted, true
	}
	return nil, false
}

// Scrub makes a report fit to keep and show, whatever the sender did: nothing
// the sender wrote is kept but a value that matches its key's type. An entry is
// the schema's row (key, description, default, type, values) plus the sender's
// value and origin. An entry is dropped when its key is not a scalar key of
// the table, its origin is not "file" or "default", its value is a list, a map
// or of another type than the key's, or the key is repeated. A string the table
// does not vouch for, and a URL with credentials, a query or a fragment, is
// Redacted. `env` values must be variable names.
func Scrub(entries []Entry) []Entry {
	out := make([]Entry, 0, len(entries))
	seen := map[string]bool{}
	for _, e := range entries {
		k, ok := Schema.Lookup(e.Key)
		if !ok || seen[e.Key] || (e.Origin != "file" && e.Origin != "default") {
			continue
		}
		v, ok := shown(k, e.Value)
		if !ok {
			continue
		}
		seen[e.Key] = true
		out = append(out, Entry{Key: k.Path, Value: v, Origin: e.Origin, Description: k.Description, Default: k.Default, Type: k.Type, Values: k.Values})
	}
	return out
}
