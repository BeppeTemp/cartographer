package settingschema

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Removed is a key that a release took out of a settings file (D903). Each
// file has a table of them next to its schema, so a file written for an older
// version is recognised, ignored with a warning that names the replacement, and
// dropped when the file is realigned.
type Removed struct {
	// Path is the key as in Key.Path: dotted, a "[]" segment is a list of
	// mappings ("kbs[].git_profile").
	Path string
	// Replacement is the key that took over, or "" when the behaviour it
	// switched is now always on (or a constant).
	Replacement string
	// Note is what to do instead when there is no single replacement key, and
	// the clause after the replacement when there is one.
	Note string
	// Plan is the plan issue that removed it (a decision number, D672).
	Plan int
	// Env and Flag are the environment variable and the command-line flag that
	// used to feed the key, "" if none.
	Env  string
	Flag string
	// FlagValue marks Flag as taking a value in a separate argument
	// (--flag value), which is dropped with it.
	FlagValue bool
	// Refuse is a value of the key that cannot be honoured at all: a file that
	// holds it is refused, not ignored (the server git profile, D903). The
	// refusal is Note.
	Refuse string
	// RefuseAny refuses the key whatever non-empty value it holds, because
	// ignoring it would change where writes go (kbs[].git_branch, D903): the
	// value has to reach Replacement first. The refusal names Replacement.
	RefuseAny bool
}

// Refuses reports whether value, an environment variable's, is one the key
// cannot be ignored with. A key in a file is judged from its YAML node instead
// (Hit.Refused), where a null is told apart from the string "null".
func (r Removed) Refuses(value string) bool {
	v := strings.TrimSpace(value)
	if r.RefuseAny {
		return v != ""
	}
	return r.Refuse != "" && strings.EqualFold(v, r.Refuse)
}

// Off is a value that switched a feature off and that a realignment resets to
// the default, so every feature is on (D903). The key itself stays a setting.
type Off struct {
	// Path is the key, in the same form as Removed.Path.
	Path string
	// Value is the YAML literal that switched the feature off ("false", "0").
	Value string
	// Plan is the plan issue that decided the feature is always on.
	Plan int
}

// Registry is the retired part of one settings file: the keys it no longer has
// and the values it no longer accepts.
type Registry struct {
	Removed []Removed
	Off     []Off
}

// Kinds of Change.
const (
	ChangeRemoved    = "removed"    // a removed key was dropped
	ChangeReset      = "reset"      // an off value was reset to the default
	ChangeDocumented = "documented" // the file was rewritten into the commented form
	ChangeMoved      = "moved"      // a key took another name or home (Prepare), its value kept
)

// Change is one thing a realignment did to a file.
type Change struct {
	Kind string
	// Path is where it happened, with the list index when the key sits in a
	// list entry ("kbs[1].git_profile"); empty for ChangeDocumented.
	Path string
	// Detail says what became of it: the replacement, or what to do instead.
	Detail string
}

// String is the line a report prints.
func (c Change) String() string {
	switch c.Kind {
	case ChangeRemoved:
		return fmt.Sprintf("removed %s: %s", c.Path, c.Detail)
	case ChangeReset:
		return fmt.Sprintf("reset %s: %s", c.Path, c.Detail)
	case ChangeMoved:
		return fmt.Sprintf("moved %s: %s", c.Path, c.Detail)
	}
	return c.Detail
}

// Report is what a realignment changed. The zero value says nothing changed.
type Report struct {
	Changes []Change
}

// Empty reports whether the realignment left the file as it was.
func (r Report) Empty() bool { return len(r.Changes) == 0 }

// Substantive reports whether the report holds a change of meaning: a removed
// key or a reset value, not only the rewrite into the commented form.
func (r Report) Substantive() bool {
	for _, c := range r.Changes {
		if c.Kind != ChangeDocumented {
			return true
		}
	}
	return false
}

// Lines is the report, one line per change.
func (r Report) Lines() []string {
	out := make([]string, 0, len(r.Changes))
	for _, c := range r.Changes {
		out = append(out, c.String())
	}
	return out
}

// Hit is a retired key found in a document.
type Hit struct {
	// Path is the concrete location: "kbs[1].git_profile".
	Path string
	// Removed is set for a removed key, Off for an off value.
	Removed *Removed
	Off     *Off
	// Value is the value the key holds, as the YAML literal ("server"), with an
	// alias resolved; "" for a list or a mapping.
	Value string

	value  *yaml.Node // the value node, an alias resolved
	parent *yaml.Node // the mapping that holds the key
	key    string
	// shared is set for a pair reached through an alias or a merge key (<<):
	// it lives in an anchor that other places use too, so a realignment does
	// not edit it, and the key stays ignored with its warning.
	shared bool
}

// Message is the warning a Load gives for a retired key that was met again: it
// names what to use instead. The key is ignored, never an error.
func (h Hit) Message() string {
	if h.Removed != nil {
		return h.Path + ": " + h.Removed.Advice()
	}
	return fmt.Sprintf("%s: %s switches a feature off, which is no longer a choice; it is read as the default", h.Path, h.Value)
}

// Refused reports whether the key holds a value that cannot be honoured, and
// the message that says what to do.
func (h Hit) Refused() (string, bool) {
	if h.Removed == nil {
		return "", false
	}
	if h.Removed.RefuseAny {
		if !holdsValue(h.value) {
			return "", false
		}
		shown := strings.TrimSpace(h.Value)
		if h.value.Kind != yaml.ScalarNode {
			shown = "a value that is not a branch name"
		}
		use := h.Removed.Replacement
		if h.Removed.Note != "" {
			use += " (" + h.Removed.Note + ")"
		}
		return fmt.Sprintf("%s: %s is still set, and ignoring it would change where writes go; set %s to that value, then remove this key", h.Path, shown, use), true
	}
	if !h.Removed.Refuses(h.Value) {
		return "", false
	}
	return fmt.Sprintf("%s: %s", h.Path, h.Removed.Note), true
}

// holdsValue reports whether n, the value of a key, holds anything: a null (a
// bare key, ~, null) or an empty string does not; any other scalar, the string
// "null" included, a list or a mapping does.
func holdsValue(n *yaml.Node) bool {
	if n == nil {
		return false
	}
	if n.Kind == yaml.ScalarNode {
		return n.ShortTag() != "!!null" && strings.TrimSpace(n.Value) != ""
	}
	return true
}

// Advice says what became of the key and what to use: the text after the key in
// a warning or a finding ("git.profile: removed setting, ignored; use ...").
func (r Removed) Advice() string {
	switch {
	case r.Replacement != "" && r.Note != "":
		return fmt.Sprintf("removed setting, ignored; use %s (%s)", r.Replacement, r.Note)
	case r.Replacement != "":
		return fmt.Sprintf("removed setting, ignored; use %s", r.Replacement)
	case r.Note != "":
		return fmt.Sprintf("removed setting, ignored: %s", r.Note)
	}
	return "removed setting, ignored: the behaviour is now always on"
}

// detail is the report line of a dropped key.
func (r Removed) detail() string {
	switch {
	case r.Replacement != "" && r.Note != "":
		return fmt.Sprintf("replaced by %s (%s)", r.Replacement, r.Note)
	case r.Replacement != "":
		return "replaced by " + r.Replacement
	case r.Note != "":
		return r.Note
	}
	return "none, the behaviour is now always on"
}

// Find lists the retired keys a document holds, in file order. It touches
// nothing.
func (r Registry) Find(doc *yaml.Node) []Hit {
	root := rootMapping(doc)
	if root == nil {
		return nil
	}
	var hits []Hit
	for i := range r.Removed {
		e := &r.Removed[i]
		walkPath(root, strings.Split(e.Path, "."), "", false, map[walkStep]bool{}, func(parent *yaml.Node, idx int, concrete string, shared bool) {
			v, _ := deref(parent.Content[idx+1])
			hits = append(hits, Hit{Path: concrete, Removed: e, Value: literal(v), value: v, parent: parent, key: parent.Content[idx].Value, shared: shared})
		})
	}
	for i := range r.Off {
		e := &r.Off[i]
		walkPath(root, strings.Split(e.Path, "."), "", false, map[walkStep]bool{}, func(parent *yaml.Node, idx int, concrete string, shared bool) {
			if v := parent.Content[idx+1]; v.Kind == yaml.ScalarNode && sameLiteral(v, e.Value) {
				hits = append(hits, Hit{Path: concrete, Off: e, Value: v.Value, value: v, parent: parent, key: parent.Content[idx].Value, shared: shared})
			}
		})
	}
	return hits
}

// FindBytes is Find on raw file contents; unparseable data holds nothing.
func (r Registry) FindBytes(data []byte) []Hit {
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil {
		return nil
	}
	return r.Find(&doc)
}

func rootMapping(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return nil
	}
	return doc
}

// walkStep is a mapping met at a concrete path: walkPath enters each once.
type walkStep struct {
	m      *yaml.Node
	prefix string
}

// walkPath calls visit for every pair a registry path reaches in m. It follows
// aliases and merge keys (<<) as the decoder does, so a key cannot hide behind
// an anchor; a pair reached through either is shared with the anchor's other
// uses, and visit is told so. seen enters each mapping once per path, so a file
// of aliases nested on aliases costs its size, not their expansion.
func walkPath(m *yaml.Node, segs []string, prefix string, shared bool, seen map[walkStep]bool, visit func(parent *yaml.Node, idx int, concrete string, shared bool)) {
	m, viaAlias := deref(m)
	shared = shared || viaAlias
	if m == nil || m.Kind != yaml.MappingNode || len(segs) == 0 || seen[walkStep{m, prefix}] {
		return
	}
	seen[walkStep{m, prefix}] = true
	name := strings.TrimSuffix(segs[0], "[]")
	list := strings.HasSuffix(segs[0], "[]")
	for i := 0; i+1 < len(m.Content); i += 2 {
		if k := m.Content[i]; k.Kind == yaml.ScalarNode && k.ShortTag() == "!!merge" {
			src, _ := deref(m.Content[i+1])
			from := []*yaml.Node{src}
			if src != nil && src.Kind == yaml.SequenceNode {
				from = src.Content
			}
			for _, f := range from {
				walkPath(f, segs, prefix, true, seen, visit)
			}
			continue
		}
		if m.Content[i].Value != name {
			continue
		}
		v, viaAlias := deref(m.Content[i+1])
		switch {
		case len(segs) == 1 && !list:
			visit(m, i, prefix+name, shared)
		case list && v.Kind == yaml.SequenceNode && len(segs) > 1:
			for j, item := range v.Content {
				walkPath(item, segs[1:], fmt.Sprintf("%s%s[%d].", prefix, name, j), shared || viaAlias, seen, visit)
			}
		case !list && len(segs) > 1:
			walkPath(v, segs[1:], prefix+name+".", shared || viaAlias, seen, visit)
		}
	}
}

// deref follows n through its aliases to the node they name, and reports
// whether there was one.
func deref(n *yaml.Node) (*yaml.Node, bool) {
	via := false
	for n != nil && n.Kind == yaml.AliasNode && n.Alias != nil {
		n, via = n.Alias, true
	}
	return n, via
}

// literal is a scalar's text, and "" for anything else.
func literal(n *yaml.Node) string {
	if n.Kind == yaml.ScalarNode {
		return n.Value
	}
	return ""
}

// sameLiteral reports whether the scalar n holds the same value as the YAML
// literal want: "false" matches false however it is spelled.
func sameLiteral(n *yaml.Node, want string) bool {
	var a, b any
	if n.Decode(&a) != nil || yaml.Unmarshal([]byte(want), &b) != nil {
		return false
	}
	return reflect.DeepEqual(a, b)
}

// Realign brings the settings file data up to date with the schema (D903):
//
//   - a key of the registry that was removed is dropped, with the comment the
//     schema wrote above it; a comment a person wrote above it moves on;
//   - an off value is reset to the default, which is the key absent;
//   - a mapping left empty by either is dropped with them;
//   - what is left is documented by the schema: the comments above every key
//     and the block of the settings that are not set.
//
// Every other value stays as written, and so do the comments and the order of
// the keys: a realignment edits the document, it does not generate a new one.
// It is idempotent: realigning its own output returns the same bytes and an
// empty report. out is data itself when there is nothing to do.
//
// An error means data is not a YAML mapping; nothing is returned to write.
func (s Schema) Realign(data []byte, reg Registry) (out []byte, rep Report, err error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return data, Report{}, fmt.Errorf("not valid YAML: %w", err)
	}
	if doc.Kind != 0 && docMapping(&doc) == nil {
		// A scalar or a list at the top: nothing to realign, nothing to guess.
		return data, Report{}, fmt.Errorf("the top level must be a mapping of setting names to values")
	}

	// The blocks go home first: a removal below, and the filter of the retired
	// keys, read them where the mapping they list holds them.
	s.homeBlocks(&doc)

	hits := reg.Find(&doc)
	var touched []*yaml.Node
	for _, h := range hits {
		if h.shared {
			continue
		}
		idx := pairIdx(h.parent, h.key)
		if idx < 0 {
			continue
		}
		s.removePair(&doc, h.parent, idx)
		touched = append(touched, h.parent)
		switch {
		case h.Removed != nil:
			rep.Changes = append(rep.Changes, Change{Kind: ChangeRemoved, Path: h.Path, Detail: h.Removed.detail()})
		default:
			rep.Changes = append(rep.Changes, Change{Kind: ChangeReset, Path: h.Path, Detail: fmt.Sprintf("%s switched a feature off; the default is back", h.Value)})
		}
	}
	if len(hits) > 0 {
		s.pruneEmpty(&doc, touched)
	}
	// The block of unset settings may list a removed key, with the description
	// it had: Render keeps a line it does not know, so it is taken out first.
	dropRetiredFromBlocks(&doc, reg.retiredPaths())

	s.Render(&doc)
	enc, err := Encode(&doc)
	if err != nil {
		return data, Report{}, err
	}
	if bytes.Equal(enc, data) {
		return data, Report{}, nil
	}
	if rep.Empty() {
		rep.Changes = append(rep.Changes, Change{Kind: ChangeDocumented, Detail: "rewrote the file with the comments of the settings schema"})
	}
	return enc, rep, nil
}

// removePair deletes the pair at idx of m. The comment the schema generated for
// the key goes with it; one a person wrote moves to the next key, or to the end
// of the file, so a realignment never silently loses a note.
func (s Schema) removePair(doc, m *yaml.Node, idx int) {
	k := m.Content[idx]
	carry := k.HeadComment
	// A key is not looked up by its name alone (a nested key may share one with
	// another): a head comment is dropped only when it is byte-for-byte a
	// generated one, anywhere in the table.
	if carry != "" && s.isGeneratedComment(carry) {
		carry = ""
	}
	m.Content = append(m.Content[:idx], m.Content[idx+2:]...)
	if carry == "" {
		return
	}
	if idx < len(m.Content) {
		next := m.Content[idx]
		next.HeadComment = strings.TrimRight(carry+"\n"+next.HeadComment, "\n")
		return
	}
	if doc.Kind == yaml.DocumentNode {
		doc.FootComment = strings.TrimRight(doc.FootComment+"\n"+carry, "\n")
	}
}

func (s Schema) isGeneratedComment(c string) bool {
	for _, k := range s.Keys {
		if isGenerated(c, k) {
			return true
		}
	}
	return false
}

// pruneEmpty drops the mappings a realignment emptied, from the one that lost a
// key up to the root, and the list entries and lists that went with them. A
// mapping that was empty before is not touched, and neither is the root.
func (s Schema) pruneEmpty(doc *yaml.Node, touched []*yaml.Node) {
	root := docMapping(doc)
	parent := map[*yaml.Node]*yaml.Node{}
	var index func(n *yaml.Node)
	index = func(n *yaml.Node) {
		for _, c := range n.Content {
			parent[c] = n
			index(c)
		}
	}
	index(root)
	for _, n := range touched {
		for n != nil && n != root && len(n.Content) == 0 {
			p := parent[n]
			if p == nil {
				break
			}
			for i, c := range p.Content {
				if c != n {
					continue
				}
				if p.Kind == yaml.MappingNode {
					s.removePair(doc, p, i-1) // i is the value; the key is before it
				} else {
					p.Content = append(p.Content[:i], p.Content[i+1:]...)
				}
				break
			}
			n = p
		}
	}
}

// blockEntryRe matches an entry line of the available-settings block: "# " and
// two spaces per level of nesting, then the key.
var blockEntryRe = regexp.MustCompile(`^# ( *)([A-Za-z_][A-Za-z0-9_]*):`)

// dropRetiredFromBlocks takes the removed keys out of the available-settings
// blocks of the document, each with the description lines above it. Rendering
// rebuilds the block from the schema, but it keeps the lines of a key the schema
// no longer has, because it cannot tell them from a person's own notes. Only
// entries whose full path is in retired are dropped: a note that is not a
// retired key, and an entry a person edited, stay.
func dropRetiredFromBlocks(doc *yaml.Node, retired map[string]bool) {
	root := rootMapping(doc)
	if root == nil {
		return
	}
	filter := func(n *yaml.Node, prefix []string) {
		user, block, ok := splitBlock(n.FootComment)
		if !ok {
			return
		}
		kept := filterBlock(block, prefix, retired)
		if len(kept) == 0 {
			n.FootComment = user
			return
		}
		n.FootComment = joinComments(user, Marker+"\n"+strings.Join(kept, "\n"))
	}
	if doc.Kind == yaml.DocumentNode {
		filter(doc, nil)
	}
	var walk func(m *yaml.Node, prefix []string)
	walk = func(m *yaml.Node, prefix []string) {
		for i := 0; i+1 < len(m.Content); i += 2 {
			filter(m.Content[i], prefix)
			if v := m.Content[i+1]; v.Kind == yaml.MappingNode {
				walk(v, append(append([]string(nil), prefix...), m.Content[i].Value))
			}
		}
	}
	walk(root, nil)
}

// filterBlock drops, from the lines of a block, the entries whose path is
// retired, and the description lines directly above each. prefix is the path of
// the mapping the block belongs to.
func filterBlock(lines, prefix []string, retired map[string]bool) []string {
	var out, pending []string
	stack := append([]string(nil), prefix...)
	for _, l := range lines {
		m := blockEntryRe.FindStringSubmatch(l)
		if m == nil {
			pending = append(pending, l)
			continue
		}
		depth := len(prefix) + len(m[1])/2
		if depth > len(stack) {
			depth = len(stack)
		}
		stack = append(stack[:depth], m[2])
		if retired[strings.Join(stack, ".")] {
			pending = nil
			continue
		}
		out = append(append(out, pending...), l)
		pending = nil
	}
	return append(out, pending...)
}

// retiredPaths is the set of paths of the removed keys that can show up in a
// block: the ones that do not run through a list of mappings, which a block never
// lists.
func (r Registry) retiredPaths() map[string]bool {
	out := map[string]bool{}
	for _, e := range r.Removed {
		if !strings.Contains(e.Path, "[]") {
			out[e.Path] = true
		}
	}
	return out
}

// maxBackups bounds the search for a free backup name: past it something other
// than earlier realignments is filling the directory, and the realignment stops.
const maxBackups = 1000

// writeBackup keeps data, the original of a realigned file, beside it as
// <file>.bak-<version>. A backup of that name already there (an earlier
// realignment under the same version) is never overwritten, the oldest one is
// the file as it was before the upgrade: the next free <file>.bak-<version>.<n>
// is used. Each name is created exclusively, so two processes realigning at once
// cannot both take it, and a symlink planted at the name is not followed.
func writeBackup(path, version string, data []byte, perm fs.FileMode) (string, error) {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_', r == '+':
			return r
		}
		return '-'
	}, version)
	if clean == "" {
		clean = "unknown"
	}
	base := path + ".bak-" + clean
	for n := 0; n < maxBackups; n++ {
		p := base
		if n > 0 {
			p = fmt.Sprintf("%s.%d", base, n)
		}
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, err = f.Write(data)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(p)
			return "", err
		}
		return p, nil
	}
	return "", fmt.Errorf("no free backup name: %s and its .1 to .%d all exist", base, maxBackups-1)
}

// RealignFile realigns the settings file at path in place (Realign), keeping the
// original beside it first (writeBackup). A file that is absent, or already in
// canonical form, is left alone: no backup is written and the report is empty.
// The new contents replace the old atomically, with the old file's permissions;
// when the backup or the replacement cannot be written (a file mounted read-only
// is the usual cause) the original is untouched and still works, and the error
// says why. The report is returned with that error, so a caller can tell a file
// that holds removed keys it could not drop from one that only lacks comments.
func (s Schema) RealignFile(path string, reg Registry, version string) (rep Report, backup string, err error) {
	return s.RealignFileWith(path, reg, version, nil)
}

// Prepare rewrites a file's bytes before the registry is applied, for a key that
// did not disappear but changed shape or home (token_env became token, D906): it
// returns the new bytes and the changes it made, which lead the report. It must
// be idempotent and leave bytes it has nothing to do with as they are.
type Prepare func(data []byte) (out []byte, changes []Change, err error)

// RealignFileWith is RealignFile with a Prepare step run first. The backup
// holds the file as it was before the Prepare step, so it is the original.
func (s Schema) RealignFileWith(path string, reg Registry, version string, prepare Prepare) (rep Report, backup string, err error) {
	// A symlinked file (one a dotfiles tool manages) is realigned where it
	// lives: renaming over the link would replace it with a regular file.
	if fi, lerr := os.Lstat(path); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
		if real, rerr := filepath.EvalSymlinks(path); rerr == nil {
			path = real
		}
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Report{}, "", nil
	}
	if err != nil {
		return Report{}, "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return Report{}, "", err
	}
	prepared, pre := data, []Change(nil)
	if prepare != nil {
		if prepared, pre, err = prepare(data); err != nil {
			return Report{}, "", fmt.Errorf("%s: %w", path, err)
		}
	}
	out, rep, err := s.Realign(prepared, reg)
	if err != nil {
		return Report{}, "", fmt.Errorf("%s: %w", path, err)
	}
	if len(pre) > 0 {
		// A prepared file is written even when the registry changes nothing more;
		// the comments-only change would be redundant next to the real ones.
		var rest []Change
		for _, c := range rep.Changes {
			if c.Kind != ChangeDocumented {
				rest = append(rest, c)
			}
		}
		rep.Changes = append(pre, rest...)
	}
	if rep.Empty() {
		return Report{}, "", nil
	}
	backup, err = writeBackup(path, version, data, info.Mode().Perm())
	if err != nil {
		return rep, "", fmt.Errorf("keeping a backup of %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".realign-*")
	if err != nil {
		_ = os.Remove(backup)
		return rep, "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		_ = os.Remove(backup)
		return rep, "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(backup)
		return rep, "", err
	}
	if err := os.Chmod(tmp.Name(), info.Mode().Perm()); err != nil {
		_ = os.Remove(backup)
		return rep, "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(backup)
		return rep, "", err
	}
	return rep, backup, nil
}
