package mcpserver

import (
	"strings"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// historyGuard answers, for a caller that does not see the whole KB, whether a
// path at a given commit lay inside the caller's perimeter then. The policy
// gate of a read tool checks only the concept's current ID; history follows
// renames and commits touch several files, so everything a tool reads out of
// git (revisions, past content, commit subjects, reasons) goes through this
// guard. A nil guard, and one for a whole-KB caller, filters nothing.
type historyGuard struct {
	k       *kb.KB
	policy  auth.Policy
	name    string
	typed   bool // some rule of the caller for this KB narrows by type
	files   map[string][]gitx.FileChange
	content map[string]string // "sha:path" → type read there ("" when none)
}

// newHistoryGuard returns nil when the caller sees the whole KB.
func newHistoryGuard(ctx requestContext, k *kb.KB) *historyGuard {
	if WholeVisible(ctx, k, false) {
		return nil
	}
	p := auth.PrincipalFromContext(ctx)
	g := &historyGuard{
		k: k, policy: p.Policy, name: kbName(k),
		files: map[string][]gitx.FileChange{}, content: map[string]string{},
	}
	for _, rule := range p.Policy.Permissions {
		if rule.KB == g.name && len(rule.Types) > 0 {
			g.typed = true
		}
	}
	return g
}

// pathVisible reports whether the file path, as committed in sha, was readable
// by the caller. The type of a type-restricted rule is the one the file held
// in that commit (in its parent for a deletion). A path outside data/ is not a
// concept and carries no perimeter.
func (g *historyGuard) pathVisible(sha, path, status string) bool {
	if g == nil {
		return true
	}
	rel, ok := strings.CutPrefix(path, "data/")
	if !ok {
		return true
	}
	mapName, journalName := "", ""
	if seg, _, nested := strings.Cut(rel, "/"); nested {
		mapName = seg
		if meta, err := g.k.ReadArchiveMeta(seg); err == nil {
			if kind, _ := meta.Get("kind"); kind == "journal" {
				journalName, mapName = seg, ""
			}
		}
	}
	typ := ""
	if g.typed && strings.HasSuffix(rel, ".md") {
		typ = g.typeAt(sha, path, status)
	}
	return g.policy.Allows(g.name, mapName, journalName, typ, false)
}

func (g *historyGuard) typeAt(sha, path, status string) string {
	ref := sha
	if status == "D" {
		ref = sha + "^"
	}
	key := ref + ":" + path
	if t, ok := g.content[key]; ok {
		return t
	}
	typ := ""
	if text, err := gitx.ShowFile(g.k.Root, ref, path); err == nil {
		if fm, _, _ := okf.SplitFrontmatter(text); fm != "" {
			if parsed, perr := okf.ParseFrontmatter(fm); perr == nil {
				if v, ok := parsed.Get("type"); ok {
					typ, _ = v.(string)
				}
			}
		}
	}
	g.content[key] = typ
	return typ
}

func (g *historyGuard) commitFiles(sha string) []gitx.FileChange {
	if f, ok := g.files[sha]; ok {
		return f
	}
	f, err := gitx.CommitFiles(g.k.Root, sha)
	if err != nil {
		f = nil
	}
	g.files[sha] = f
	return f
}

// commitVisible reports whether every path commit sha touched (a rename's
// source included) lay inside the perimeter. When the commit cannot be read it
// is not visible.
func (g *historyGuard) commitVisible(sha string) bool {
	if g == nil {
		return true
	}
	files := g.commitFiles(sha)
	if files == nil {
		return false
	}
	for _, f := range files {
		if !g.pathVisible(sha, f.Path, f.Status) {
			return false
		}
		// A retyping commit changes a page's type in place: the type it had
		// before the commit counts as well.
		if g.typed && f.Status == "M" && !g.pathVisible(sha+"^", f.Path, "M") {
			return false
		}
		if f.OldPath != "" && !g.pathVisible(sha+"^", f.OldPath, "M") {
			return false
		}
	}
	return true
}

// renamedFromHidden reports whether commit sha renamed some file to path from
// a place outside the perimeter.
func (g *historyGuard) renamedFromHidden(sha, path string) bool {
	for _, f := range g.commitFiles(sha) {
		if f.OldPath != "" && f.Path == path && !g.pathVisible(sha+"^", f.OldPath, "M") {
			return true
		}
	}
	return false
}

// revisions keeps the leading run of revs (newest first) that lay inside the
// perimeter and stops at the first revision whose file was outside it, or
// that moved the file in from outside: nothing behind that point is the
// caller's to see. The moving-in revision itself is kept (its subject is
// redacted by redact when the commit also touched outside paths).
func (g *historyGuard) revisions(revs []gitx.FileRevision) []gitx.FileRevision {
	if g == nil {
		return revs
	}
	out := make([]gitx.FileRevision, 0, len(revs))
	for _, r := range revs {
		if !g.pathVisible(r.SHA, r.Path, r.Status) {
			break
		}
		out = append(out, r)
		if g.renamedFromHidden(r.SHA, r.Path) {
			break
		}
	}
	return out
}

// redact gives the commit text the caller may read: the subject and reason
// unchanged when the commit stayed inside the perimeter, otherwise the tool
// name alone with no reason (both are free text naming ids and paths).
func (g *historyGuard) redact(sha, subject, reason string) (string, string, bool) {
	if g.commitVisible(sha) {
		return subject, reason, false
	}
	return toolOfSubject(subject), "", true
}

// toolOfSubject is the tool name a commit subject starts with ("concept_move:
// a -> b" → "concept_move"); the server's own subjects without an argument
// are kept.
func toolOfSubject(subject string) string {
	if subject == externalChangesSubject {
		return subject
	}
	if i := strings.IndexAny(subject, ": ("); i > 0 {
		return subject[:i]
	}
	return subject
}

// reachesKB reports whether the caller holds any read grant on the KB called
// name: the other KB named by a concept_transfer commit is the caller's to
// know only then.
func reachesKB(ctx requestContext, name string) bool {
	if name == "" {
		return true
	}
	return auth.PrincipalFromContext(ctx).Policy.HasKBAccess(name, false)
}

// foreignKBHidden reports whether a commit's Moved-To/Moved-From trailers name
// a KB the caller has no access to.
func foreignKBHidden(ctx requestContext, movedTo, movedFrom string) bool {
	return !reachesKB(ctx, movedTo) || !reachesKB(ctx, movedFrom)
}

// redactForeign cuts a commit's free text to the tool name when it names a KB
// the caller cannot reach.
func redactForeign(ctx requestContext, subject, reason string, movedTo, movedFrom string) (string, string, bool) {
	if foreignKBHidden(ctx, movedTo, movedFrom) {
		return toolOfSubject(subject), "", true
	}
	return subject, reason, false
}

// visibleKBName is name when the caller reaches that KB, otherwise "".
func visibleKBName(ctx requestContext, name string) string {
	if reachesKB(ctx, name) {
		return name
	}
	return ""
}
