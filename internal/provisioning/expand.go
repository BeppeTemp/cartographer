package provisioning

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/repoindex"
)

// The placeholder syntax — the regex, the escape rule and the metasyntax
// exemption — lives in internal/okf (D262): it is concept syntax, and the
// server lists the keys a KB's concepts cite from the same parser the client
// expands with, so the two can never disagree about what counts as a key.

// expansionTracker accumulates state across one whole Apply invocation:
// every warning raised while expanding placeholders (surfaced once, at the
// end, in AppliedResult.Warnings), every placeholder key resolved along the
// way, key -> local path — the source data for the "Local paths" table
// applyInstructionsGroup appends to the instructions block (D75 WP4) — and
// every key that could not be resolved, key -> reason (D262).
//
// It holds one repoindex.Resolver for the whole Apply, so however many
// {{repo:…}} keys miss, the search roots are walked at most once (D262).
type expansionTracker struct {
	warnings   []string
	resolved   map[string]string // e.g. "repo:cartographer" -> "/home/x/repos/cartographer"
	unresolved map[string]string // e.g. "path:assets" -> "no \"assets\" entry under paths: …"
	resolver   *repoindex.Resolver
}

func newExpansionTracker() *expansionTracker {
	return &expansionTracker{resolved: map[string]string{}, unresolved: map[string]string{}}
}

// resolve answers one placeholder, memoized for the whole Apply: a key seen
// twice is looked up once, and an unresolved key is recorded once — the
// failure is reported as one aggregated line per sync, not one per
// occurrence (D262).
func (t *expansionTracker) resolve(kind, key string, opts ApplyOptions) (string, bool) {
	id := kind + ":" + key
	if p, ok := t.resolved[id]; ok {
		return p, true
	}
	if _, ok := t.unresolved[id]; ok {
		return "", false
	}
	var resolved string
	var err error
	switch kind {
	case "repo":
		if t.resolver == nil {
			t.resolver = repoindex.NewResolver(opts.Paths, opts.SearchRoots, opts.SearchDepth)
		}
		var warnings []string
		resolved, warnings, err = t.resolver.Resolve(key)
		t.warnings = append(t.warnings, warnings...)
	case "path":
		if p, ok := opts.Paths[key]; ok {
			resolved = expandHomePath(p)
		} else {
			err = fmt.Errorf("no %q entry under paths: (.cartographer.yaml)", key)
		}
	default:
		err = fmt.Errorf("unknown placeholder kind %q", kind)
	}
	if err != nil {
		t.unresolved[id] = err.Error()
		return "", false
	}
	t.resolved[id] = resolved
	return resolved, true
}

// preResolve resolves every listed "kind:key" before any artifact is
// expanded (D262), so the "Local paths" table covers every key the KB cites —
// a concept-only key included — not just the ones inside the artifacts this
// particular Apply happened to rewrite. Metasyntax and malformed ids are
// skipped, exactly as the expander skips them.
func (t *expansionTracker) preResolve(ids []string, opts ApplyOptions) {
	for _, id := range ids {
		kind, key, ok := okf.SplitPlaceholderID(id)
		if !ok || okf.IsPlaceholderMetasyntax(key) {
			continue
		}
		t.resolve(kind, key, opts)
	}
}

// placeholderSources returns every placeholder this Apply must resolve, as
// "kind:key" -> the KBs citing it: the keys the server listed for the bound
// KBs (ApplyOptions.Placeholders, which covers concept bodies) plus the ones
// found in the authorized artifacts of m itself. The second half is what
// makes the table independent of which artifacts this Apply rewrites — and
// what keeps it complete against an older server that lists nothing. A
// bundled artifact contributes the key with no KB attached.
func placeholderSources(m Manifest, opts ApplyOptions) map[string][]string {
	out := make(map[string][]string, len(opts.Placeholders))
	add := func(id, kb string) {
		kbs, seen := out[id]
		if !seen {
			out[id] = nil
		}
		if kb == "" {
			return
		}
		for _, k := range kbs {
			if k == kb {
				return
			}
		}
		out[id] = append(kbs, kb)
	}
	for id, kbs := range opts.Placeholders {
		add(id, "")
		for _, kb := range kbs {
			add(id, kb)
		}
	}
	for _, a := range m.Artifacts {
		if !artifactAuthorized(a, opts) {
			continue
		}
		kb := ""
		if strings.HasPrefix(a.Source, "kb:") {
			kb = strings.TrimPrefix(a.Source, "kb:")
		}
		for _, f := range a.Files {
			for _, id := range okf.Placeholders(string(f.Content)) {
				add(id, kb)
			}
		}
	}
	for id := range out {
		sort.Strings(out[id])
	}
	return out
}

// expandPlaceholders replaces every {{repo:<key>}}/{{path:<name>}}
// occurrence in content with its locally resolved path. A no-op (content
// returned unchanged) unless opts.ExpandPlaceholders is set — see
// ApplyOptions.ExpandPlaceholders: internal/mcpserver never sets it, so the
// MCP server never expands anything, only client-side callers do. An
// unresolved placeholder is left verbatim in the output and recorded on
// tracker — it never blocks materialization (D75 WP3).
func expandPlaceholders(content []byte, opts ApplyOptions, tracker *expansionTracker) []byte {
	if !opts.ExpandPlaceholders || !okf.PlaceholderRe.Match(content) {
		return content
	}

	return okf.PlaceholderRe.ReplaceAllFunc(content, func(match []byte) []byte {
		p, _ := okf.ParsePlaceholder(match)

		// {{\repo:...}} is the authoritative way to write the syntax down: the
		// backslash is removed and nothing is resolved. Chosen over doubling the
		// braces, which is unreadable in a document that is *about* the syntax,
		// and over an HTML-comment wrapper, since skills are also read as plain
		// markdown (D162).
		if p.Escaped {
			return []byte(strings.Replace(string(match), "{{\\", "{{", 1))
		}
		// Documentation metasyntax: verbatim and silent. Before this, describing
		// the generic form inside a skill produced eleven warnings per sync, which
		// trains people to ignore warnings.
		if okf.IsPlaceholderMetasyntax(p.Key) {
			return match
		}

		resolved, ok := tracker.resolve(p.Kind, p.Key, opts)
		if !ok {
			return match
		}
		return []byte(resolved)
	})
}

// expandHomePath expands a leading "~" (alone, or followed by either separator)
// to the user's home directory. It is repoindex.ExpandHome, not a copy of it:
// the two were near-duplicates that had already drifted — this one accepted only
// "~/" — and a `paths:` entry must mean the same thing whether it is read here or
// when a {{repo:…}} placeholder is resolved. This package already imports
// repoindex, so sharing costs no new dependency edge.
func expandHomePath(p string) string { return repoindex.ExpandHome(p) }

// placeholderParagraph is appended to the instructions block by every
// client-side sync, whether or not anything was resolved (D262): before it,
// the explanation only appeared beside a non-empty table, so on a machine
// where nothing had ever resolved the agent was never told the mechanism
// existed and guessed paths instead. It is appended AFTER expansion and must
// stay that way: the placeholders it spells out are metasyntax anyway, but
// the paragraph is Cartographer's text, not the KB's, and nothing in it is
// meant to be resolved.
const placeholderParagraph = "### Local paths\n\n" +
	"Concepts and artifacts may cite local paths as `{{repo:<key>}}` or `{{path:<name>}}` placeholders. " +
	"The server never expands them: only this machine knows where things are. " +
	"The ones resolved here are listed in the table below, when there are any. " +
	"For any other, run `cartographer resolve <kind>:<key>` (e.g. `cartographer resolve repo:name`); " +
	"if that fails, ask the user for the path instead of guessing it, and record the answer with " +
	"`cartographer paths set <kind>:<key> <path>`."

// buildPathsSection renders the placeholder part of the instructions block:
// the fixed paragraph, then the "Local paths" table when anything resolved.
func buildPathsSection(resolved map[string]string) string {
	if table := buildPathsTable(resolved); table != "" {
		return placeholderParagraph + "\n\n" + table
	}
	return placeholderParagraph
}

// pathsSectionHash is what the lock records of the rendered section (D262),
// so a table that changed with no artifact change — a concept started citing
// a key, a `paths:` entry was added — still rewrites the block.
func pathsSectionHash(section string) string {
	sum := sha256.Sum256([]byte("cartographer:paths-section:v1\x00" + section))
	return fmt.Sprintf("%x", sum[:])
}

// buildPathsTable renders the "Local paths" table rows for every resolved
// {{repo:<key>}}/{{path:<name>}} placeholder (D75 WP4): every agent reading
// the instructions block gets a live placeholder -> local path map. The
// pointer to `cartographer resolve` lives in placeholderParagraph, which is
// written even when this is empty. Returns "" when resolved is empty.
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
	b.WriteString("| Placeholder | Local path |\n")
	b.WriteString("|---|---|\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "| `{{%s}}` | `%s` |\n", k, resolved[k])
	}
	return strings.TrimRight(b.String(), "\n")
}

// UnresolvedPlaceholdersWarning renders the one aggregated line a sync prints
// for every placeholder it could not resolve (D262), with the command that
// fixes each. "" when there is none. Callers aggregate across providers
// first: the same key missing for three providers is one problem.
func UnresolvedPlaceholdersWarning(unresolved map[string]string) string {
	if len(unresolved) == 0 {
		return ""
	}
	ids := make([]string, 0, len(unresolved))
	for id := range unresolved {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	fmt.Fprintf(&b, "%d placeholder(s) not resolved on this machine (left verbatim):", len(ids))
	for _, id := range ids {
		fmt.Fprintf(&b, "\n  %s — %s\n    fix: cartographer paths set %s <path>", id, unresolved[id], id)
	}
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
