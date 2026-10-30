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

// Template is the settings of a worker with no config file: every key at its default, and the file they amount to.
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

// envName is what an `*_env` value may be: a variable name, optionally with (set)/(unset).
var envName = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*( ?\((set|unset)\))?)?$`)

// commentedAssign matches a commented-out assignment (`# key: value`): group 1
// stays, group 2 is the key, group 3 the value.
var commentedAssign = regexp.MustCompile(`^(\s*#\s*([a-z][a-z0-9_]*)\s*:\s*)(\S.*)$`)

// ErrUnsafe is a report whose YAML the mask cannot vouch for.
var ErrUnsafe = errors.New("workerconfig: settings must be one YAML document with no merge key and no alias")

// known reports whether path is a schema key or holds some.
func known(path string) bool {
	for _, k := range Schema.Keys {
		if k.Path == path || strings.HasPrefix(k.Path, path+".") || strings.HasPrefix(k.Path, path+"[]") {
			return true
		}
	}
	return false
}

// leafNames are the last path segments of the schema keys.
var leafNames = func() map[string]bool {
	m := map[string]bool{}
	for _, k := range Schema.Keys {
		m[k.Path[strings.LastIndexAny(k.Path, ".]")+1:]] = true
	}
	return m
}()

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

// unsafeByName applies the rules of a key by its name, case-insensitively.
func unsafeByName(name, v string) bool {
	name = strings.ToLower(name)
	switch {
	case strings.HasSuffix(name, "_env"):
		return !envName.MatchString(strings.Trim(v, `"'`))
	case strings.HasSuffix(name, "url"):
		return unsafeURL(v)
	}
	return false
}

// unsafeScalar reports whether a scalar at a schema path (a list item is the
// list's path plus "[]") would put a secret in the page. It fails closed: a
// value under a key the table does not know is unsafe, whatever the key is
// called (`token`, `password`, `api_key`).
func unsafeScalar(path, v string) bool {
	if v == "" {
		return false
	}
	path = strings.TrimSuffix(path, "[]")
	k, ok := Schema.Lookup(path)
	if !ok {
		return true
	}
	if k.Type == "env" && !envName.MatchString(strings.Trim(v, `"'`)) || k.Type == "url" && unsafeURL(v) {
		return true
	}
	return unsafeByName(path[strings.LastIndexAny(path, ".]")+1:], v)
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// scrubValue masks a decoded value (an entry's value) at a schema path.
func scrubValue(path string, v any) any {
	switch x := v.(type) {
	case string:
		if unsafeScalar(path, x) {
			return Redacted
		}
	case []any:
		for i := range x {
			x[i] = scrubValue(path+"[]", x[i])
		}
	case map[string]any:
		for k, e := range x {
			x[k] = scrubValue(joinPath(path, k), e)
		}
	case nil, bool, int, int64, uint64, float64:
	default:
		return Redacted
	}
	return v
}

// Scrub makes a report fit to keep and show, whatever the sender did: the
// worker masked it, but no secret may be in memory or on the page. It returns
// entries and YAML in which every value under a key the table does not know,
// every `*_env` value that is not a variable name and every URL with
// credentials, a query or a fragment is replaced by Redacted, and the file's KB
// names. The YAML must be one document with no merge key and no alias (where a
// value escapes the mask). It does not render again, so nothing grows.
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
	if !scrubNode(&doc, "", &changed) {
		return nil, "", nil, ErrUnsafe
	}
	if changed {
		out, err := settingschema.Encode(&doc)
		if err != nil {
			return nil, "", nil, err
		}
		data = out
	}
	// An entry is the schema's row plus the sender's value and origin: every
	// other field is overwritten, and a key or an origin it does not know drops
	// the entry, since each of them is shown on the page.
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		k, ok := Schema.Lookup(e.Key)
		if !ok || (e.Origin != "file" && e.Origin != "default") {
			continue
		}
		out = append(out, Entry{Key: k.Path, Value: scrubValue(k.Path, e.Value), Origin: e.Origin,
			Description: k.Description, Default: k.Default, Type: k.Type, Values: k.Values})
	}
	var cfg struct {
		KBs []string `yaml:"kbs"`
	}
	_ = doc.Decode(&cfg)
	return out, string(data), cfg.KBs, nil
}

// opaque reports whether n holds an alias or a merge key, which a dropped
// subtree must not hide.
func opaque(n *yaml.Node) bool {
	if n.Kind == yaml.AliasNode || n.Tag == "!!merge" || (n.Kind == yaml.ScalarNode && n.Value == "<<") {
		return true
	}
	for _, c := range n.Content {
		if opaque(c) {
			return true
		}
	}
	return false
}

// scrubComments masks the value of a commented-out assignment in n's comments.
func scrubComments(n *yaml.Node, changed *bool) {
	for _, c := range []*string{&n.HeadComment, &n.LineComment, &n.FootComment} {
		lines := strings.Split(*c, "\n")
		for i, l := range lines {
			if m := commentedAssign.FindStringSubmatch(l); m != nil && (!leafNames[m[2]] || unsafeByName(m[2], strings.TrimSpace(m[3]))) {
				lines[i], *changed = m[1]+Redacted, true
			}
		}
		*c = strings.Join(lines, "\n")
	}
}

// scrubNode masks the document in place and reports whether it is free of
// aliases and merge keys. path is the schema path of n.
func scrubNode(n *yaml.Node, path string, changed *bool) bool {
	if n.Kind == yaml.AliasNode {
		return false
	}
	scrubComments(n, changed)
	mask := func(v *yaml.Node, p string) bool {
		if v.Kind != yaml.ScalarNode || !unsafeScalar(p, v.Value) {
			return false
		}
		// A trailing comment is likely to quote the value.
		*v = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: Redacted}
		*changed = true
		return true
	}
	switch n.Kind {
	case yaml.MappingNode:
		kept := make([]*yaml.Node, 0, len(n.Content))
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Kind != yaml.ScalarNode || k.Tag == "!!merge" || k.Value == "<<" {
				return false
			}
			// A key the table does not know is dropped with its value, whatever
			// the value is: the key itself may be the secret.
			p := joinPath(path, k.Value)
			if !known(p) {
				if opaque(k) || opaque(v) {
					return false
				}
				*changed = true
				continue
			}
			scrubComments(k, changed)
			if !mask(v, p) && !scrubNode(v, p, changed) {
				return false
			}
			kept = append(kept, k, v)
		}
		n.Content = kept
	case yaml.SequenceNode:
		for _, c := range n.Content {
			if !mask(c, path+"[]") && !scrubNode(c, path+"[]", changed) {
				return false
			}
		}
	default:
		for _, c := range n.Content {
			if !mask(c, path) && !scrubNode(c, path, changed) {
				return false
			}
		}
	}
	return true
}
