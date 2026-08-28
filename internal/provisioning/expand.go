package provisioning

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/repoindex"
)

// placeholderRe matches a {{repo:<key>}} or {{path:<name>}} placeholder
// (D75): a literal "repo" or "path" kind, a colon, then anything but braces.
var placeholderRe = regexp.MustCompile(`\{\{(\\?)(repo|path):([^{}]+)\}\}`)

// isPlaceholderMetasyntax reports whether a key is obviously a documentation
// placeholder rather than a real one: <name> or "...". It silences the WARNING
// only — the text is still left verbatim, which is what a documentation example
// wants (D162). No lookup is needed to be sure: repoindex keys are git remote
// names or path keys, and "<" ">" are legal in neither.
func isPlaceholderMetasyntax(key string) bool {
	return key == "..." || (strings.HasPrefix(key, "<") && strings.HasSuffix(key, ">"))
}

// expansionTracker accumulates state across one whole Apply invocation:
// every warning raised while expanding placeholders (surfaced once, at the
// end, in AppliedResult.Warnings) and every placeholder key resolved along
// the way, key -> local path — the source data for the "Local paths" table
// applyInstructionsGroup appends to the instructions block (D75 WP4).
type expansionTracker struct {
	warnings []string
	resolved map[string]string // e.g. "repo:cartographer" -> "/home/x/repos/cartographer"
}

func newExpansionTracker() *expansionTracker {
	return &expansionTracker{resolved: map[string]string{}}
}

// expandPlaceholders replaces every {{repo:<key>}}/{{path:<name>}}
// occurrence in content with its locally resolved path. A no-op (content
// returned unchanged) unless opts.ExpandPlaceholders is set — see
// ApplyOptions.ExpandPlaceholders: internal/mcpserver never sets it, so the
// MCP server never expands anything, only client-side callers do. An
// unresolved placeholder is left verbatim in the output and reported as a
// warning on tracker — it never blocks materialization (D75 WP3).
func expandPlaceholders(content []byte, opts ApplyOptions, tracker *expansionTracker) []byte {
	if !opts.ExpandPlaceholders || !placeholderRe.Match(content) {
		return content
	}

	return placeholderRe.ReplaceAllFunc(content, func(match []byte) []byte {
		sub := placeholderRe.FindSubmatch(match)
		escaped, kind, key := string(sub[1]) != "", string(sub[2]), string(sub[3])

		// {{\repo:...}} is the authoritative way to write the syntax down: the
		// backslash is removed and nothing is resolved. Chosen over doubling the
		// braces, which is unreadable in a document that is *about* the syntax,
		// and over an HTML-comment wrapper, since skills are also read as plain
		// markdown (D162).
		if escaped {
			return []byte(strings.Replace(string(match), "{{\\", "{{", 1))
		}
		// Documentation metasyntax: verbatim and silent. Before this, describing
		// the generic form inside a skill produced eleven warnings per sync, which
		// trains people to ignore warnings.
		if isPlaceholderMetasyntax(key) {
			return match
		}

		var resolved string
		var err error
		switch kind {
		case "repo":
			var warnings []string
			resolved, warnings, err = repoindex.Resolve(key, opts.Paths, opts.SearchRoots, opts.SearchDepth)
			tracker.warnings = append(tracker.warnings, warnings...)
		case "path":
			if p, ok := opts.Paths[key]; ok {
				resolved = expandHomePath(p)
			} else {
				err = fmt.Errorf("no %q entry under paths: (.cartographer.yaml)", key)
			}
		}
		if err != nil {
			tracker.warnings = append(tracker.warnings, fmt.Sprintf("provisioning: placeholder %s not resolved: %v", match, err))
			return match
		}
		tracker.resolved[kind+":"+key] = resolved
		return []byte(resolved)
	})
}

// expandHomePath expands a leading "~" to the user's home directory —
// mirrors repoindex's unexported expandHome, duplicated here (a few lines)
// rather than exported purely for this one cross-package call.
func expandHomePath(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, p[2:])
}

// buildPathsTable renders the "Local paths" table appended to the
// instructions block whenever at least one {{repo:<key>}}/{{path:<name>}}
// placeholder was resolved while materializing this sync (D75 WP4): every
// agent reading the instructions block gets a live placeholder -> local path
// map, plus a pointer (`cartographer resolve <key>`) for any placeholder it
// meets that isn't listed — e.g. one added to a concept body after this sync
// last ran. Returns "" when resolved is empty (nothing to append).
func buildPathsTable(resolved map[string]string) string {
	if len(resolved) == 0 {
		return ""
	}

	keys := make([]string, 0, len(resolved))
	for k := range resolved {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("### Local paths\n\n")
	b.WriteString("| Placeholder | Local path |\n")
	b.WriteString("|---|---|\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "| `{{%s}}` | `%s` |\n", k, resolved[k])
	}
	b.WriteString("\nFound a `{{repo:...}}`/`{{path:...}}` placeholder in a concept that is missing from this table? Run `cartographer resolve <key>` (e.g. `cartographer resolve repo:name`).")
	return b.String()
}

// hashArtifactFiles uses a versioned, domain-separated length-prefixed
// encoding of path, effective executable mode and raw bytes. Used to record
// ManagedFile.ContentHash on the actually-written (placeholder-expanded)
// bytes: when expansion is a no-op (no placeholder present), files are
// byte-identical to the source, so this returns exactly Artifact.ContentHash
// — zero drift for existing installations (D75 WP3).
func hashArtifactFiles(files []ArtifactFile) string {
	sorted := append([]ArtifactFile(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	h := sha256.New()
	h.Write([]byte("cartographer:artifact-content:v1\x00"))
	for _, f := range sorted {
		writeHashField(h, []byte(f.Path))
		if f.Executable {
			h.Write([]byte{1})
		} else {
			h.Write([]byte{0})
		}
		writeHashField(h, f.Content)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func writeHashField(h interface{ Write([]byte) (int, error) }, data []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(data)))
	h.Write(length[:])
	h.Write(data)
}
