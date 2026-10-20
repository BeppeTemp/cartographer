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
	"slices"
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
// Files written by earlier versions open it with "# --- available settings ---":
// isMarker recognises both, and a realignment rewrites the divider to this one.
const Marker = "# --- available settings: uncomment a key to set it ---"

// markerPrefix is what every divider the block ever had starts with.
const markerPrefix = "# --- available settings"

// wrapWidth is the width the comment lines of a key are wrapped at.
const wrapWidth = 76

// isMarker reports whether line is the divider of an available-settings block,
// in the current wording or in an older one.
func isMarker(line string) bool {
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), markerPrefix)
	return ok && (rest == " ---" || strings.HasPrefix(rest, ":") && strings.HasSuffix(rest, " ---"))
}

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
	// Example is a YAML value that shows how the key is written. An unset key is
	// listed with it, commented out and ready to uncomment (a map or a list in
	// block style, indented under the key); without one the line shows Default.
	// A scalar Example is also the value the generated example file sets; a map
	// or a list is only ever shown commented, so that file never configures what
	// would change behaviour (a token, a role).
	Example string
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

// Comment is the head comment of a set key: what it does, then one compact
// line with its values, default and how it can be overridden, as in
//
//	# Size after which the active log segment is rotated.
//	# Values: bytes; 0 keeps the default · Default: 67108864 · needs a restart
//
// The default is left out where it says nothing (an empty map or list). Each
// line starts with "# ".
func Comment(k Key) []string {
	return prefixed(describe(k, false))
}

// blockComment is the comment of a key listed in the available-settings block,
// without the leading "# ". The commented assignment under it shows the value to
// uncomment (assignLines), so the default is stated only when that line shows
// something else.
func blockComment(k Key) []string {
	return describe(k, k.Example == "" || sameValue(k.Example, k.Default))
}

func prefixed(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = "# " + l
	}
	return out
}

// describe is the comment of a key without the leading "# ": the description,
// then the values and the notes on one line. defaultShown says the assignment
// line next to the comment already is the default.
func describe(k Key, defaultShown bool) []string {
	out := wrap(k.Description, wrapWidth)
	var notes []string
	if d := defaultText(k); d != "" && !defaultShown {
		notes = append(notes, "Default: "+d)
	}
	if k.Env != "" {
		notes = append(notes, "Env "+k.Env)
	}
	if k.Flag != "" {
		notes = append(notes, "flag "+k.Flag)
	}
	if k.Restart {
		notes = append(notes, "needs a restart")
	}
	if k.Secret {
		notes = append(notes, "secret: keep it out of version control")
	}
	// A note is never split across lines, and the " ·" that follows it stays with
	// it: \x01 is a space the wrapper does not break at.
	glue := strings.NewReplacer(" ", "\x01")
	var line string
	if k.Values != "" {
		line = "Values: " + k.Values
		if len(notes) > 0 {
			line += "\x01·"
		}
	}
	for i, n := range notes {
		if i == 0 && k.Values == "" {
			n = strings.ToUpper(n[:1]) + n[1:]
		}
		n = glue.Replace(n)
		if i < len(notes)-1 {
			n += "\x01·"
		}
		if line != "" {
			line += " "
		}
		line += n
	}
	if line == "" {
		return out
	}
	for _, l := range wrap(line, wrapWidth) {
		out = append(out, strings.ReplaceAll(l, "\x01", " "))
	}
	return out
}

// defaultText is the default as a person reads it: "none" for an empty string,
// nothing for an empty map or list (a key that is unset is simply absent), the
// YAML literal otherwise.
func defaultText(k Key) string {
	switch d := strings.TrimSpace(k.Default); d {
	case `""`:
		return "none"
	case "{}", "[]":
		return ""
	default:
		return d
	}
}

func sameValue(a, b string) bool {
	return strings.TrimSpace(a) == strings.TrimSpace(b)
}

// legacyComment is the head comment earlier versions generated: the description,
// the values, then one sentence per default, variable, flag and note. It is only
// recognised, never written: a realignment turns it into Comment.
func legacyComment(k Key) []string {
	var out []string
	out = append(out, wrap(k.Description, wrapWidth)...)
	if k.Values != "" {
		out = append(out, wrap("Values: "+k.Values, wrapWidth)...)
	}
	out = append(out, legacyMeta(k)...)
	return prefixed(out)
}

func legacyMeta(k Key) []string {
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
	return wrap(strings.Join(meta, " "), wrapWidth)
}

// isGenerated reports whether c, a head comment as the parser hands it over,
// is the one the schema writes for k, in the current wording or the previous one.
// A leading blank line is not part of it (Render adds one; the parser drops it).
// The old wording is recognised whatever its Values line said then: that text
// is the one the schema edits most, and a comment that differs only there is
// still the schema's.
func isGenerated(c string, k Key) bool {
	c = strings.TrimLeft(c, "\n")
	if c == strings.Join(Comment(k), "\n") || c == strings.Join(legacyComment(k), "\n") {
		return true
	}
	lines := strings.Split(c, "\n")
	desc := prefixed(wrap(k.Description, wrapWidth))
	if len(lines) < len(desc) || !slices.Equal(lines[:len(desc)], desc) {
		return false
	}
	rest := lines[len(desc):]
	if len(rest) == 0 || !strings.HasPrefix(rest[0], "# Values: ") {
		return false
	}
	i := 1
	for i < len(rest) && !strings.HasPrefix(rest[i], "# Default: ") {
		i++
	}
	return slices.Equal(rest[i:], prefixed(legacyMeta(k)))
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
		documentKey(m, i, c, !block)
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
// comment of the user's. A comment the schema wrote in an earlier wording is
// rewritten to the current one. The comment above the first key of a list entry
// belongs to the entry: the parser hangs it on the mapping, above the dash.
func commentKey(m *yaml.Node, i int, c *node, entry bool) {
	k, v := m.Content[i], m.Content[i+1]
	if c.key == nil || k.LineComment != "" || v.LineComment != "" {
		return
	}
	head := &k.HeadComment
	if i == 0 && entry {
		head = &m.HeadComment
	}
	if *head == "" && k.HeadComment == "" || *head != "" && isGenerated(*head, *c.key) {
		*head = strings.Join(Comment(*c.key), "\n")
	}
}

// documentKey documents the i-th key of m and sets it apart from the one before.
func documentKey(m *yaml.Node, i int, c *node, entry bool) {
	commentKey(m, i, c, entry)
	spaceKey(m, i, c)
}

// spaceKey puts a blank line above the i-th key of m, and above its comment when
// it has one: every known key but the first of its mapping is set apart from the
// one before. The blank line is a leading newline in the head comment, which the
// emitter writes as an empty line; the parser drops it, so it is put back on
// every render. A key that only groups others carries no comment, and gets the
// bare newline. The text of a comment a person wrote is not touched.
func spaceKey(m *yaml.Node, i int, c *node) {
	if i == 0 {
		return
	}
	k := m.Content[i]
	switch {
	case k.HeadComment == "":
		if c.key == nil && len(c.kids) > 0 {
			k.HeadComment = "\n"
		}
	case !strings.HasPrefix(k.HeadComment, "\n"):
		k.HeadComment = "\n" + k.HeadComment
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
			documentKey(m, i, c, true)
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
		documentKey(m, i, t, false)
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

// splitBlock cuts a foot comment at the divider, in either wording: what the
// user wrote before it, and the block lines after it.
func splitBlock(foot string) (user string, block []string, found bool) {
	lines := strings.Split(foot, "\n")
	for i, l := range lines {
		if isMarker(l) {
			return strings.TrimRight(strings.Join(lines[:i], "\n"), "\n"), lines[i+1:], true
		}
	}
	return foot, nil, false
}

// group is one key of an available-settings block as a file holds it: the
// description lines above its assignment, and the assignment (the line "# key:"
// and the indented lines that continue it).
type group struct {
	name string
	desc []string
	body []string
}

func applyBlock(doc, m *yaml.Node, t *node) {
	isRoot := doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 && doc.Content[0] == m

	// Take the block out of wherever the parser put it: the last key's foot
	// comment, or, at the root, the document's.
	var old []string
	take := func(n *yaml.Node) {
		if user, block, ok := splitBlock(n.FootComment); ok {
			n.FootComment = strings.TrimLeft(user, "\n")
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

	// Read the block back into groups. A blank line ends a group; an indented
	// comment line, or a bare "#", continues the one before it. What is not a
	// known key stays as it was, after the keys.
	var kept []group
	var pending []string
	cur := -1
	for _, l := range old {
		if strings.TrimSpace(l) == "" {
			cur = -1
			pending = append(pending, "")
			continue
		}
		if mm := entryRe.FindStringSubmatch(l); mm != nil && t.byName[mm[1]] != nil {
			kept = append(kept, group{name: mm[1], desc: trimBlank(pending), body: []string{l}})
			cur, pending = len(kept)-1, nil
			continue
		}
		if cur >= 0 && len(pending) == 0 && (strings.HasPrefix(l, "#  ") || strings.TrimRight(l, " ") == "#") {
			kept[cur].body = append(kept[cur].body, l)
			continue
		}
		pending = append(pending, l)
	}

	// One blank line between keys. A key as the schema would list it now is
	// listed that way (a file written by an earlier version gains the new
	// layout); one a person edited stays as written.
	var lines []string
	add := func(group []string) {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, group...)
	}
	seen := map[string]bool{}
	for _, g := range kept {
		if present[g.name] || seen[g.name] {
			continue
		}
		seen[g.name] = true
		add(settle(t.byName[g.name], g))
	}
	for _, c := range t.kids {
		if present[c.name] || seen[c.name] {
			continue
		}
		add(unsetLines(c, 0))
	}
	if rest := trimBlank(pending); len(rest) > 0 {
		add(rest)
	}
	if len(lines) == 0 {
		return
	}
	block := Marker + "\n\n" + strings.Join(lines, "\n")

	if isRoot {
		doc.FootComment = joinComments(doc.FootComment, block)
		return
	}
	if len(m.Content) == 0 {
		return
	}
	// The foot comment of the last key opens with a blank line, which sets the
	// block apart from the keys above it (the parser drops it, take rebuilds it).
	last := m.Content[len(m.Content)-2]
	last.FootComment = "\n" + joinComments(last.FootComment, block)
}

// trimBlank drops the blank lines at both ends of lines.
func trimBlank(lines []string) []string {
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// settle decides how a key found in a block is listed again. When the person
// left it as a schema generated it, in this version's layout or an earlier one,
// it is listed as the schema generates it now. When the assignment was edited,
// the whole key stays as written.
func settle(c *node, g group) []string {
	user := append(append([]string(nil), g.desc...), g.body...)
	if sameLines(user, unsetLines(c, 0)) {
		return unsetLines(c, 0)
	}
	if leaf(c) {
		if sameLines(g.body, assignLines(c, "")) || sameLines(g.body, legacyAssignLines(c, "")) {
			return unsetLines(c, 0)
		}
		return user
	}
	if sameLines(user, legacyUnsetLines(c, 0)) {
		return unsetLines(c, 0)
	}
	return user
}

// leaf reports whether c is listed as one assignment: a key with nothing under
// it, or a list (whose entries the block never lists).
func leaf(c *node) bool { return len(c.kids) == 0 || c.list }

// sameLines compares lines the way a block is read: without trailing spaces,
// and without the bare "#" that separates the keys of a group.
func sameLines(a, b []string) bool {
	norm := func(ls []string) []string {
		var out []string
		for _, l := range ls {
			if l = strings.TrimRight(l, " "); l != "#" {
				out = append(out, l)
			}
		}
		return out
	}
	a, b = norm(a), norm(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func joinComments(user, block string) string {
	if user == "" {
		return block
	}
	return user + "\n\n" + block
}

// unsetLines is the commented-out form of a key that is not set: its
// description, then the key with the value to uncomment. A key that only groups
// others is shown as a commented mapping, its keys apart by a bare "#".
func unsetLines(c *node, depth int) []string {
	pad := strings.Repeat("  ", depth)
	var out []string
	if c.key != nil {
		for _, l := range blockComment(*c.key) {
			out = append(out, "# "+pad+l)
		}
	}
	if leaf(c) {
		return append(out, assignLines(c, pad)...)
	}
	out = append(out, fmt.Sprintf("# %s%s:", pad, c.name))
	for i, k := range c.kids {
		if i > 0 {
			out = append(out, "#")
		}
		out = append(out, unsetLines(k, depth+1)...)
	}
	return out
}

// assignLines is the commented assignment of a key that is not set: its Example
// when it has one, otherwise its Default. A map or a list in block style goes
// on the lines below the key, indented, so uncommenting the lines is enough.
func assignLines(c *node, pad string) []string {
	val := "[]"
	if c.key != nil {
		val = c.key.Default
		if c.key.Example != "" {
			val = c.key.Example
		}
	}
	return valueLines(c.name, pad, val)
}

func valueLines(name, pad, val string) []string {
	val = strings.TrimRight(val, "\n")
	if !blockValue(val) {
		return []string{fmt.Sprintf("# %s%s: %s", pad, name, val)}
	}
	out := []string{"# " + pad + name + ":"}
	for _, l := range strings.Split(val, "\n") {
		out = append(out, strings.TrimRight("# "+pad+"  "+l, " "))
	}
	return out
}

// blockValue reports whether a YAML value is written over several lines: it
// holds a newline, or it is a map or a list not in flow style.
func blockValue(val string) bool {
	if strings.Contains(val, "\n") {
		return true
	}
	var n yaml.Node
	if yaml.Unmarshal([]byte(val), &n) != nil || len(n.Content) == 0 {
		return false
	}
	v := n.Content[0]
	return (v.Kind == yaml.MappingNode || v.Kind == yaml.SequenceNode) && v.Style&yaml.FlowStyle == 0
}

// legacyUnsetLines is unsetLines as earlier versions wrote it. It is only used
// to recognise a key left as it was generated, and list it in the current form.
func legacyUnsetLines(c *node, depth int) []string {
	pad := strings.Repeat("  ", depth)
	var out []string
	if c.key != nil {
		for _, l := range legacyComment(*c.key) {
			out = append(out, "# "+pad+strings.TrimPrefix(l, "# "))
		}
	}
	if leaf(c) {
		return append(out, legacyAssignLines(c, pad)...)
	}
	out = append(out, fmt.Sprintf("# %s%s:", pad, c.name))
	for _, k := range c.kids {
		out = append(out, legacyUnsetLines(k, depth+1)...)
	}
	return out
}

func legacyAssignLines(c *node, pad string) []string {
	def := "[]"
	if c.key != nil {
		def = c.key.Default
	}
	return []string{fmt.Sprintf("# %s%s: %s", pad, c.name, def)}
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
	// A comment that opens with a blank line gets the indentation of its key on
	// that line. The emitter never writes a line of spaces inside a scalar (a
	// value with one is double-quoted), so these are all ours to clean.
	out := indentOnlyRe.ReplaceAll(b.Bytes(), nil)
	// A mapping with nothing set encodes as "{}": a file with no key set is
	// only its comments, which is valid YAML for an empty file.
	if rest, ok := bytes.CutPrefix(out, []byte("{}\n")); ok {
		return bytes.TrimLeft(rest, "\n"), nil
	}
	return out, nil
}

var indentOnlyRe = regexp.MustCompile(`(?m)^ +$`)

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

// Document builds the document of the keys that carry a scalar Example, in
// schema order. It is the starting point of the generated example file. An
// Example that is a map or a list only shows the shape of an unset key, and is
// left out: the example file must not configure a token, a role or a binding.
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
		if kind := val.Content[0].Kind; kind == yaml.MappingNode || kind == yaml.SequenceNode {
			continue
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
