package workerconfig

import (
	"bytes"
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/settingschema"
	"gopkg.in/yaml.v3"
)

// Redacted is what a report shows in place of a value that is not what the
// schema says it is (the server's own YAML view uses the same word).
const Redacted = "<redacted>"

// Entry is one worker key with its effective value and origin ("file" when the
// config sets it, "default" otherwise), in the JSON shape of the server's own
// settings entries so the Atlas reads both alike (D22). A key that names an
// environment variable holds the name, never the value.
type Entry struct {
	Key         string `json:"key"`
	Value       any    `json:"value"`
	Origin      string `json:"origin"`
	Description string `json:"description"`
	Default     string `json:"default"`
	Type        string `json:"type,omitempty"`
	Values      string `json:"values,omitempty"`
}

// Template is the worker settings with no config file: every key at its
// default, and the file they amount to. The Atlas shows it while no worker has
// reported, so what a worker can set is readable before one connects.
func Template() ([]Entry, string) {
	entries := make([]Entry, 0, len(Schema.Keys))
	for _, k := range Schema.Keys {
		e := Entry{Key: k.Path, Origin: "default", Description: k.Description, Default: k.Default, Type: k.Type, Values: k.Values}
		_ = yaml.Unmarshal([]byte(k.Default), &e.Value)
		entries = append(entries, e)
	}
	out, _ := Schema.Format(nil)
	return entries, string(out)
}

// envName is what an `*_env` value may be: a variable name, optionally with the
// (set)/(unset) a report appends.
var envName = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*( ?\((set|unset)\))?)?$`)

// commentedEnv matches a commented-out assignment of an `*_env` key, as the
// file lists a key it does not set: group 1 stays, group 2 is the value.
var commentedEnv = regexp.MustCompile(`^(\s*#\s*[A-Za-z0-9_]*_env\s*:\s*)(\S.*)$`)

// ErrUnsafe is a report whose YAML the mask cannot vouch for.
var ErrUnsafe = errors.New("workerconfig: settings must be one YAML document with no merge key and no alias")

// unsafeValue reports whether a value under key would put a secret in the
// page: an `*_env` that is not a variable name, or a URL with credentials.
func unsafeValue(key, v string) bool {
	switch {
	case strings.HasSuffix(key, "_env"):
		v = strings.Trim(v, `"'`)
		return !envName.MatchString(v)
	case strings.HasSuffix(key, "url"):
		u, err := url.Parse(v)
		return err == nil && u.User != nil
	}
	return false
}

func scrubValue(key string, v any) any {
	switch x := v.(type) {
	case string:
		if unsafeValue(key, x) {
			return Redacted
		}
	case []any:
		for i := range x {
			x[i] = scrubValue(key, x[i])
		}
	case map[string]any:
		for k, e := range x {
			x[k] = scrubValue(k, e)
		}
	}
	return v
}

// Scrub makes a report fit to keep and show, whatever the sender did. The
// worker rendered the YAML and masked its entries, but the server does not
// trust the shape: no secret may be in memory or on the page. It returns the
// entries and the YAML with every `*_env` value that is not a variable name, and
// every URL carrying credentials, replaced by Redacted, and the KB names the
// file lists. The YAML must be one document with no merge key and no alias,
// where a value escapes the mask: otherwise the error is ErrUnsafe or the
// parser's. It does not render again, so a stored text is never larger than
// what was received.
func Scrub(entries []Entry, text string) ([]Entry, string, []string, error) {
	data := []byte(text)
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		return nil, "", nil, err
	}
	var second yaml.Node
	if dec.Decode(&second) == nil {
		return nil, "", nil, ErrUnsafe
	}
	changed := false
	if !scrubNode(&doc, &changed) {
		return nil, "", nil, ErrUnsafe
	}
	if changed {
		out, err := settingschema.Encode(&doc)
		if err != nil {
			return nil, "", nil, err
		}
		data = out
	}
	out := make([]Entry, len(entries))
	for i, e := range entries {
		e.Value = scrubValue(e.Key, e.Value)
		if unsafeValue(e.Key, e.Default) {
			e.Default = Redacted
		}
		out[i] = e
	}
	var cfg struct {
		KBs []string `yaml:"kbs"`
	}
	_ = doc.Decode(&cfg)
	return out, string(data), cfg.KBs, nil
}

// scrubNode masks the document in place and reports whether it is free of
// aliases and merge keys.
func scrubNode(n *yaml.Node, changed *bool) bool {
	if n.Kind == yaml.AliasNode {
		return false
	}
	for _, c := range []*string{&n.HeadComment, &n.LineComment, &n.FootComment} {
		lines := strings.Split(*c, "\n")
		for i, l := range lines {
			if m := commentedEnv.FindStringSubmatch(l); m != nil && unsafeValue("_env", strings.TrimSpace(m[2])) {
				lines[i], *changed = m[1]+Redacted, true
			}
		}
		*c = strings.Join(lines, "\n")
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Tag == "!!merge" || k.Value == "<<" {
				return false
			}
			if v.Kind == yaml.ScalarNode && unsafeValue(k.Value, v.Value) {
				// A trailing comment is likely to quote the value.
				*v = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: Redacted}
				*changed = true
			}
		}
	}
	for _, c := range n.Content {
		if !scrubNode(c, changed) {
			return false
		}
	}
	return true
}
