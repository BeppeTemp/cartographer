// Package settingschema describes the keys of a settings file once, in code, and
// renders that description as comments inside the YAML file (D747).
//
// A Schema is a registry table, not struct tags: the server config has raw and
// public struct pairs plus env and flag overrides, so tags would have to be
// duplicated. The packages that own a file declare its table (internal/config,
// internal/clientconfig) and a test there keeps the table and the structs in
// sync. Everything that shows a key to a human (the comments in the file, the
// generated config.example.yaml, later the config view) reads the same table.
package settingschema

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Scope says where a key lives. KB marks the per-KB keys that belong to the KB
// rather than to one server's machine.
type Scope string

const (
	ScopeServer Scope = "server"
	ScopeKB     Scope = "kb"
	ScopeClient Scope = "client"
)

// Marker opens the block of commented-out, unset keys at the end of a mapping.
const Marker = "# --- available settings ---"

// Key describes one YAML key. Path is dotted ("git.autocommit"); a segment
// ending in "[]" is a list of mappings ("kbs[].remote"). A list of mappings has
// one Key of its own, with the plain path ("kbs"), for the list itself.
type Key struct {
	Path        string
	Scope       Scope
	Type        string // "string", "bool", "int", "duration", "list", "map", ...
	Values      string // accepted values, free text; empty when any value of the type goes
	Default     string // YAML literal of the default: `""`, `true`, `30s`, `[]`
	Description string // one sentence
	Env         string // environment variable that overrides it
	Flag        string // command-line flag that overrides it
	Restart     bool   // a running process only picks it up after a restart
	Secret      bool   // the value must be redacted on display
	Example     string // YAML value written in the generated example file; empty = left out
}

// Schema is the ordered key table of one settings file. The order is the order
// keys are rendered in.
type Schema struct {
	Keys []Key
}

// Lookup returns the key with the given path.
func (s Schema) Lookup(path string) (Key, bool) {
	for _, k := range s.Keys {
		if k.Path == path {
			return k, true
		}
	}
	return Key{}, false
}

// Comment is the head comment of a set key: what it does, its values, its
// default, and how it can be overridden. Each line starts with "# ".
func Comment(k Key) []string {
	var out []string
	out = append(out, wrap(k.Description, 76)...)
	if k.Values != "" {
		out = append(out, wrap("Values: "+k.Values, 76)...)
	}
	meta := []string{"Default: " + k.Default + "."}
	if k.Env != "" {
		meta = append(meta, "Env: "+k.Env+".")
	}
	if k.Flag != "" {
		meta = append(meta, "Flag: "+k.Flag+".")
	}
	if k.Restart {
		meta = append(meta, "Needs a restart.")
	}
	if k.Secret {
		meta = append(meta, "Secret: keep it out of version control.")
	}
	out = append(out, wrap(strings.Join(meta, " "), 76)...)
	for i := range out {
		out[i] = "# " + out[i]
	}
	return out
}

func wrap(text string, width int) []string {
	var lines []string
	cur := ""
	for _, w := range strings.Fields(text) {
		if cur != "" && len(cur)+1+len(w) > width {
			lines = append(lines, cur)
			cur = w
			continue
		}
		if cur != "" {
			cur += " "
		}
		cur += w
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// node is the key table as a tree, to walk a YAML mapping against it.
type node struct {
	name   string
	key    *Key
	list   bool
	kids   []*node
	byName map[string]*node
}

func (n *node) child(name string) *node {
	if c, ok := n.byName[name]; ok {
		return c
	}
	c := &node{name: name, byName: map[string]*node{}}
	n.kids = append(n.kids, c)
	n.byName[name] = c
	return c
}

func (s Schema) tree() *node {
	root := &node{byName: map[string]*node{}}
	for i := range s.Keys {
		n := root
		for _, seg := range strings.Split(s.Keys[i].Path, ".") {
			list := strings.HasSuffix(seg, "[]")
			n = n.child(strings.TrimSuffix(seg, "[]"))
			n.list = n.list || list
		}
		n.key = &s.Keys[i]
	}
	return root
}

// Render completes the YAML document doc against the schema, in place:
//
//   - a known key that carries no comment yet gets its Comment as head comment;
//     a comment the user wrote is never touched;
//   - at the end of every mapping outside a list the keys that are not set are
//     listed, commented out and documented, under Marker. The block is rebuilt
//     from what it holds: lines the user edited stay as they are, a key that is
//     now set leaves the block, a new schema key joins it.
//
// Unknown keys are left alone. Rendering twice yields the same document.
func (s Schema) Render(doc *yaml.Node) {
	root := docMapping(doc)
	if root == nil {
		return
	}
	s.walk(doc, root, s.tree(), true)
}

func docMapping(doc *yaml.Node) *yaml.Node {
	if doc.Kind == 0 {
		doc.Kind = yaml.DocumentNode
	}
	if doc.Kind != yaml.DocumentNode {
		return nil
	}
	if len(doc.Content) == 0 {
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	if doc.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	return doc.Content[0]
}

func (s Schema) walk(doc, m *yaml.Node, t *node, block bool) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		c := t.byName[k.Value]
		if c == nil {
			continue
		}
		commentKey(m, i, c, !block)
		switch {
		case v.Kind == yaml.MappingNode && len(c.kids) > 0 && !c.list:
			s.walk(doc, v, c, true)
		case v.Kind == yaml.SequenceNode && c.list:
			for _, e := range v.Content {
				if e.Kind == yaml.MappingNode {
					s.walk(doc, e, c, false)
				}
			}
		}
	}
	if block {
		applyBlock(doc, m, t)
	}
}

// commentKey gives the i-th key of m its Comment, unless it already carries a
// comment of the user's. The comment above the first key of a list entry belongs
// to the entry: the parser hangs it on the mapping, above the dash.
func commentKey(m *yaml.Node, i int, c *node, entry bool) {
	k, v := m.Content[i], m.Content[i+1]
	if c.key == nil || k.LineComment != "" || v.LineComment != "" {
		return
	}
	head := &k.HeadComment
	if i == 0 && entry {
		head = &m.HeadComment
	}
	if *head == "" && k.HeadComment == "" {
		*head = strings.Join(Comment(*c.key), "\n")
	}
}

// CommentEntry documents the keys of a list entry mapping m (an entry of the
// list at listPath, for example "kbs") that carry no comment yet. It is what a
// writer that appends one entry calls, without rendering the whole file.
func (s Schema) CommentEntry(m *yaml.Node, listPath string) {
	t := s.tree()
	for _, seg := range strings.Split(listPath, ".") {
		if t = t.byName[seg]; t == nil {
			return
		}
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if c := t.byName[m.Content[i].Value]; c != nil {
			commentKey(m, i, c, true)
		}
	}
}

// CommentKey documents the one key of mapping m named key, whose schema path is
// path ("kbs[].name"), when it carries no comment yet.
func (s Schema) CommentKey(m *yaml.Node, key, path string) {
	t := s.tree()
	for _, seg := range strings.Split(path, ".") {
		if t = t.byName[strings.TrimSuffix(seg, "[]")]; t == nil {
			return
		}
	}
	if i := pairIdx(m, key); i >= 0 {
		commentKey(m, i, t, false)
	}
}

func pairIdx(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return i
		}
	}
	return -1
}

var entryRe = regexp.MustCompile(`^# ([A-Za-z_][A-Za-z0-9_]*):`)

// splitBlock cuts a foot comment at Marker: what the user wrote before it, and
// the block lines after it.
func splitBlock(foot string) (user string, block []string, found bool) {
	lines := strings.Split(foot, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) == Marker {
			return strings.TrimRight(strings.Join(lines[:i], "\n"), "\n"), lines[i+1:], true
		}
	}
	return foot, nil, false
}

type group struct {
	name  string
	lines []string
}

func applyBlock(doc, m *yaml.Node, t *node) {
	isRoot := doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 && doc.Content[0] == m

	// Take the block out of wherever the parser put it: the last key's foot
	// comment, or, at the root, the document's.
	var old []string
	take := func(n *yaml.Node) {
		if user, block, ok := splitBlock(n.FootComment); ok {
			n.FootComment = user
			old = append(old, block...)
		}
	}
	for i := 0; i < len(m.Content); i += 2 {
		take(m.Content[i])
	}
	if isRoot {
		take(doc)
	}

	present := map[string]bool{}
	for i := 0; i < len(m.Content); i += 2 {
		present[m.Content[i].Value] = true
	}

	var kept []group
	var pending []string
	seen := map[string]bool{}
	for _, l := range old {
		if mm := entryRe.FindStringSubmatch(l); mm != nil && t.byName[mm[1]] != nil {
			kept = append(kept, group{name: mm[1], lines: append(pending, l)})
			pending = nil
			continue
		}
		if strings.HasPrefix(l, "#  ") && len(kept) > 0 && len(pending) == 0 {
			kept[len(kept)-1].lines = append(kept[len(kept)-1].lines, l)
			continue
		}
		pending = append(pending, l)
	}
	var lines []string
	for _, g := range kept {
		if present[g.name] || seen[g.name] {
			continue
		}
		seen[g.name] = true
		lines = append(lines, g.lines...)
	}
	for _, c := range t.kids {
		if present[c.name] || seen[c.name] {
			continue
		}
		lines = append(lines, unsetLines(c, 0)...)
	}
	lines = append(lines, pending...)
	if len(lines) == 0 {
		return
	}
	block := Marker + "\n" + strings.Join(lines, "\n")

	if isRoot {
		doc.FootComment = joinComments(doc.FootComment, block)
		return
	}
	if len(m.Content) == 0 {
		return
	}
	last := m.Content[len(m.Content)-2]
	last.FootComment = joinComments(last.FootComment, block)
}

func joinComments(user, block string) string {
	if user == "" {
		return block
	}
	return user + "\n" + block
}

// unsetLines is the commented-out form of a key that is not set: its
// description, then the key with its default. A key that only groups others is
// shown as a commented mapping.
func unsetLines(c *node, depth int) []string {
	pad := strings.Repeat("  ", depth)
	var out []string
	if c.key != nil {
		for _, l := range Comment(*c.key) {
			out = append(out, "# "+pad+strings.TrimPrefix(l, "# "))
		}
	}
	if len(c.kids) == 0 || c.list {
		def := "[]"
		if c.key != nil {
			def = c.key.Default
		}
		return append(out, fmt.Sprintf("# %s%s: %s", pad, c.name, def))
	}
	out = append(out, fmt.Sprintf("# %s%s:", pad, c.name))
	for _, k := range c.kids {
		out = append(out, unsetLines(k, depth+1)...)
	}
	return out
}

// Encode serializes doc with the two-space indent the repository's YAML files use.
func Encode(doc *yaml.Node) ([]byte, error) {
	var b bytes.Buffer
	e := yaml.NewEncoder(&b)
	e.SetIndent(2)
	if err := e.Encode(doc); err != nil {
		return nil, err
	}
	if err := e.Close(); err != nil {
		return nil, err
	}
	// A mapping with nothing set encodes as "{}": a file with no key set is
	// only its comments, which is valid YAML for an empty file.
	if rest, ok := bytes.CutPrefix(b.Bytes(), []byte("{}\n")); ok {
		return bytes.TrimLeft(rest, "\n"), nil
	}
	return b.Bytes(), nil
}

// Format parses data, renders it against the schema and serializes it again.
// Empty data yields a file with only the available-settings block.
func (s Schema) Format(data []byte) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	s.Render(&doc)
	return Encode(&doc)
}

// Document builds the document of the keys that carry an Example, in schema
// order. It is the starting point of the generated example file.
func (s Schema) Document() (*yaml.Node, error) {
	doc := &yaml.Node{Kind: yaml.DocumentNode}
	root := docMapping(doc)
	for _, k := range s.Keys {
		if k.Example == "" {
			continue
		}
		var val yaml.Node
		if err := yaml.Unmarshal([]byte(k.Example), &val); err != nil || len(val.Content) == 0 {
			return nil, fmt.Errorf("settingschema: example of %s: %q is not YAML", k.Path, k.Example)
		}
		setPath(root, strings.Split(k.Path, "."), val.Content[0])
	}
	return doc, nil
}

// Set puts val at the dotted path of doc, creating the mappings on the way (a
// "[]" segment is a one-entry list, as in Document). A key already there is
// left as it is. It builds the document of a set of effective values, which
// Render then documents.
func Set(doc *yaml.Node, path string, val *yaml.Node) {
	if m := docMapping(doc); m != nil {
		setPath(m, strings.Split(path, "."), val)
	}
}

func setPath(m *yaml.Node, segs []string, val *yaml.Node) {
	seg := strings.TrimSuffix(segs[0], "[]")
	list := strings.HasSuffix(segs[0], "[]")
	var v *yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == seg {
			v = m.Content[i+1]
		}
	}
	if len(segs) == 1 {
		if v == nil {
			m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: seg}, val)
		}
		return
	}
	if v == nil {
		v = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		if list {
			v = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
		}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: seg}, v)
	}
	if list {
		v = v.Content[0]
	}
	setPath(v, segs[1:], val)
}

// Example is the generated example file: the keys with an Example set, the
// others documented and commented out. header is prepended as comment lines.
func (s Schema) Example(header string) ([]byte, error) {
	doc, err := s.Document()
	if err != nil {
		return nil, err
	}
	s.Render(doc)
	body, err := Encode(doc)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	for _, l := range strings.Split(strings.TrimRight(header, "\n"), "\n") {
		b.WriteString(strings.TrimRight("# "+l, " ") + "\n")
	}
	b.WriteString("\n")
	b.Write(body)
	return []byte(b.String()), nil
}
