package okf

import (
	"regexp"
	"sort"
	"strings"
)

// Path-portability placeholders (D75): a concept or an artifact may cite a
// local path as {{repo:<key>}} or {{path:<name>}}; only the client resolves
// them, against its own filesystem. The syntax lives here, not in
// internal/provisioning, because it is concept syntax: the server lists the
// keys a KB's concepts cite (D262) and internal/kb must not import the
// provisioning package to do it.

// PlaceholderRe matches a {{repo:<key>}} or {{path:<name>}} placeholder: an
// optional escaping backslash (group 1), a literal "repo" or "path" kind
// (group 2), a colon, then anything but braces (group 3).
var PlaceholderRe = regexp.MustCompile(`\{\{(\\?)(repo|path):([^{}]+)\}\}`)

// Placeholder is one parsed occurrence of PlaceholderRe.
type Placeholder struct {
	// Escaped is true for {{\repo:…}}: the authoritative way to write the
	// syntax down in a document about it (D162). Nothing is resolved; the
	// expander only drops the backslash.
	Escaped bool
	Kind    string // "repo" or "path"
	Key     string
}

// ID is the "kind:key" form every list of placeholders uses.
func (p Placeholder) ID() string { return p.Kind + ":" + p.Key }

// ParsePlaceholder parses one PlaceholderRe match. ok is false when match is
// not one.
func ParsePlaceholder(match []byte) (Placeholder, bool) {
	sub := PlaceholderRe.FindSubmatch(match)
	if sub == nil {
		return Placeholder{}, false
	}
	return Placeholder{Escaped: len(sub[1]) != 0, Kind: string(sub[2]), Key: string(sub[3])}, true
}

// IsPlaceholderMetasyntax reports whether a key is obviously a documentation
// placeholder rather than a real one: <name>, "..." or the Unicode ellipsis
// "…" (U+2026, which editors substitute for three dots). It silences the
// warning only — the text is still left verbatim, which is what a
// documentation example wants (D162). No lookup is needed to be sure: repo
// keys are git remote names or path keys, and "<" ">" are legal in neither.
func IsPlaceholderMetasyntax(key string) bool {
	return key == "..." || key == "…" || (strings.HasPrefix(key, "<") && strings.HasSuffix(key, ">"))
}

// Placeholders returns the placeholders text cites as sorted, de-duplicated
// "kind:key" strings, leaving out escaped ones and documentation metasyntax:
// exactly the set a client would try to resolve. nil when there is none.
func Placeholders(text string) []string {
	if !strings.Contains(text, "{{") {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range PlaceholderRe.FindAllStringSubmatch(text, -1) {
		if m[1] != "" || IsPlaceholderMetasyntax(m[3]) {
			continue
		}
		id := m[2] + ":" + m[3]
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// SplitPlaceholderID splits a "kind:key" string into its kind and key. ok is
// false when the kind is neither "repo" nor "path", or the key is empty.
func SplitPlaceholderID(id string) (kind, key string, ok bool) {
	kind, key, found := strings.Cut(id, ":")
	if !found || key == "" || (kind != "repo" && kind != "path") {
		return "", "", false
	}
	return kind, key, true
}
