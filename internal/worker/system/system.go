// Package system diffs the state of a live system.
//
// A lister prints the current state of a system grouped (one entry per
// namespace, per service); this package hashes each group, keeps the last
// state per group, and says which groups were added, modified or removed. It
// does no I/O and imports no internal/kb (the worker is a client of the public
// contract); the writes that surround it are the poll's (D1372, design D20).
package system

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/worker/recipe"
)

// Instructions is the supplement of the system prompt of a system unit, embedded
// in the binary: instructions the worker follows never come from a KB.
//
//go:embed instructions.md
var Instructions string

// Caps of a listing and of the state (decision 15).
const (
	MaxGroups     = recipe.SystemMaxGroups
	MaxStateBytes = 8 << 20
	maxLabel      = 300
)

// Group is one validated entry of a listing: the content has LF line endings and
// the revision is computed here, never taken from the lister.
type Group struct {
	Key, Title, Locator, Revision, Content string
}

// Revision is the first 16 hex characters of sha256 over the content with CRLF
// folded to LF: the fold keeps a lister run on Windows from changing every hash.
func Revision(content string) string {
	sum := sha256.Sum256([]byte(fold(content)))
	return hex.EncodeToString(sum[:8])
}

func fold(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

var (
	keyRE      = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._:/@-]{0,199}$`)
	revisionRE = regexp.MustCompile(`^[0-9a-f]{16}$`)
	// yamlWords are read back by a YAML parser as another type (a key lands in
	// frontmatter and in concept_list where filters).
	yamlWords = map[string]bool{"true": true, "false": true, "null": true, "yes": true, "no": true, "on": true, "off": true}
)

// ValidKey reports whether s is a group key (decision 3).
func ValidKey(s string) bool { return keyRE.MatchString(s) && !yamlWords[strings.ToLower(s)] }

func protocol(format string, a ...any) error {
	return &recipe.Error{Code: recipe.CodeProtocolError, Detail: fmt.Sprintf(format, a...)}
}

// ValidateListing checks a listing and returns its groups sorted by key. Any
// violation is a protocol_error naming the key or the count, and nothing is
// handled. maxContent is the recipe's per-group cap.
func ValidateListing(entries []recipe.Entry, maxContent int) ([]Group, error) {
	if len(entries) > MaxGroups {
		return nil, protocol("%d groups, over the %d a system may list", len(entries), MaxGroups)
	}
	seen := map[string]bool{}
	out := make([]Group, 0, len(entries))
	for _, e := range entries {
		switch {
		case !ValidKey(e.Key):
			return nil, protocol("group key %q is not a valid key (a letter first, then letters, digits and . _ : / @ -, at most 200, not a YAML word)", clip(e.Key, 60))
		case seen[e.Key]:
			return nil, protocol("group key %q is listed twice", e.Key)
		case e.Content == "" || !utf8.ValidString(e.Content) || strings.ContainsRune(e.Content, 0):
			return nil, protocol("group %q has no content, or content that is not UTF-8 text", e.Key)
		case len(e.Content) > maxContent:
			return nil, protocol("group %q has %d bytes of content, over the %d allowed (group coarser or filter with include)", e.Key, len(e.Content), maxContent)
		}
		seen[e.Key] = true
		title := label(e.Title)
		if title == "" {
			title = e.Key
		}
		c := fold(e.Content)
		out = append(out, Group{Key: e.Key, Title: title, Locator: label(e.Locator), Revision: Revision(c), Content: c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// label is untrusted one-line text: control characters stripped, 300 runes.
func label(s string) string {
	s = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)), " ")
	if r := []rune(s); len(r) > maxLabel {
		s = string(r[:maxLabel])
	}
	return s
}

func clip(s string, n int) string {
	if len(s) > n {
		s = s[:n]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}
