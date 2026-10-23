package provisioning

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The pieces of the instructions text, shared by two renderings (D1097): the
// managed block a provisioned client writes into its steering file
// (generateKBInstructions, byte-pinned: its ContentHash and signature must not
// change) and the operating guide the server sends in `initialize` to a
// connection that has no such block (RenderGuide).

// routingSentence says that the KB exists and, on a routed connection, which
// value its `kb` argument takes (D187/D288: one endpoint, one copy of the
// tools, bare names; a connection bound to one KB has no `kb` argument, so the
// sentence names the value and says where it applies instead of requiring it).
func routingSentence(kbName string, routed bool) string {
	s := fmt.Sprintf("The %q KB is served via MCP by the \"cartographer\" server.", kbName)
	if routed {
		s += fmt.Sprintf(" Its tools are not prefixed: where a tool takes a `kb` argument, pass `kb: %q` — that connection serves several KBs through one set of tools and never infers which one you mean.", kbName)
	}
	return s
}

// archivesSentence ends the routing paragraph. noneVisible distinguishes a KB
// that has no archive from one whose archives the caller may not see.
func archivesSentence(archives []string, noneVisible bool) string {
	if len(archives) == 0 {
		if noneVisible {
			return " No archives are visible to this connection.\n\n"
		}
		return " No archives detected yet.\n\n"
	}
	names := make([]string, len(archives))
	for i, a := range archives {
		names[i] = a + "/"
	}
	return fmt.Sprintf(" Archives: %s.\n\n", strings.Join(names, ", "))
}

// Operational bullets. orientationBullet and placeholderBullet are the ones a
// read-only connection keeps; the rest instruct a writer.
const (
	// D780: graph_context is named here because agents choose the tools this
	// line names; nothing else in the session says when to use it.
	orientationBullet = "- consult it autonomously when you need historical or architectural context: `search` (keyword) or `atlas_overview` to orient yourself, `concept_read` to read; when the answer spans several linked pages, `graph_context` ranks them in one call instead of reading page by page and following links;\n"
	writeBullet       = "- write or update a page with `concept_write` when you discover something relevant; close relevant sessions with `log_append`;\n"
	// D907: the write gate is always on and nothing accepts a finding.
	refusedWriteBullet = "- a write that would introduce an avoidable finding (broken link, missing index entry, orphan) is refused with each finding's fix and nothing is committed: write the target, the index entry and the link in the same `concept_batch`;\n"
	// D321: one meaning for `active`, said once, true for every KB.
	statusBullet = "- status convention: `active` means the page is valid and current, not that work is open; open work uses `open` (accepted or queued, not started), `in-progress` or `blocked`: when you start executing an `open` item, set `in-progress` in the same `concept_patch` or `concept_batch` that records the first progress; when progress cannot continue, set `blocked` and declare `waiting_on` and `review_after`; a finished task is `done`;\n"
	commitBullet = "- every write is a git commit, revertible.\n"

	// Guide-only bullets (D1097): the block's reader has them from its other
	// directives or its client; a generic connection has only this text.
	findingsBullet    = "- act on the `findings` every write returns before moving on: they name what to fix;\n"
	placeholderBullet = "- `{{repo:<key>}}` and `{{path:<name>}}` in a page or a directive are machine-local path placeholders: keep them verbatim, never resolve or rewrite them;\n"
	conflictBullet    = "- a write blocked by a git conflict is settled with `conflicts_list` and `git_conflict_resolve`.\n"
	// D1100: guide only, the managed block (byte-pinned) does not carry it.
	procedureBullet = "- for step-by-step procedures (ingesting a source, resolving a git conflict, importing a wiki) call `kb_guide` with `procedure`;\n"
	readOnlyBullet  = "- this connection is read-only: write tools are not listed and are refused.\n"
)

// scopeSentence precedes a KB's curated body (D182 WP1): the body is one KB's
// own prose, not a session-wide law, and it is what lets a model apply the
// ordinary "more specific source wins" rule when two KBs' curated directives
// (or a repository's own instruction file) disagree — D171 cannot catch that
// class of conflict (unique by construction), so provenance and precedence are
// declared instead of accidental. Emitted only when there IS curated content to
// scope; the opt-out marker only suppresses the operational bullets.
func scopeSentence(kbName string) string {
	return fmt.Sprintf("The following directives are the %q KB's own and govern work in its perimeter; where they conflict with another KB's directives or with a repository's own instruction file, the more specific source wins.\n\n", kbName)
}

// KBArchiveNames lists the top-level archives of the KB rooted at kbRoot: the
// direct subdirectories of <kbRoot>/data/ (where the concept .md pages live,
// see kb.DataRoot), sorted by name. The KB's infrastructure directories
// (skills/, agents/, hooks/, .cartographer/, .git/) live alongside data/, not
// inside it; their names are still filtered defensively in case they end up as
// subdirectories of data/. A dot-directory is never an archive: it is tool state
// an agent left in the clone (.omc/, #757), not a map. No error if data/ is
// missing or unreadable: nil. Names only, no page count (D65): the text is
// stable imprinting, not state.
func KBArchiveNames(kbRoot string) []string {
	entries, err := os.ReadDir(filepath.Join(kbRoot, "data"))
	if err != nil {
		return nil
	}
	infra := map[string]bool{
		"skills": true, "agents": true, "hooks": true, ".cartographer": true, ".git": true,
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() || infra[e.Name()] || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// GuideKB is one KB of a connection, as RenderGuide needs it.
type GuideKB struct {
	Name string
	// Archives are the archive names the caller may see.
	Archives []string
	// ArchivesHidden is true when the KB has archives the caller may not see
	// (so an empty Archives is not "none yet").
	ArchivesHidden bool
	// Curated is the body of the KB's instructions.md and SkipPreamble its
	// first-line `preamble: none` opt-out (see KBCuratedInstructions).
	Curated      string
	SkipPreamble bool
}

// GuideOptions shapes the guide of one connection.
type GuideOptions struct {
	// Routed is true for the routed endpoint, whose tools take a `kb` argument.
	Routed bool
	// ReadOnly is true for a connection that cannot write.
	ReadOnly bool
}

// RenderGuide renders the operating guide the server sends in `initialize`
// (D1097): per KB its routing paragraph, then the client-neutral operational
// bullets once, then each KB's curated directives behind the scope sentence. It
// leaves out what only makes sense with a Cartographer client (the update
// notice, `cartographer kb reauthor`, the ops skill, the git-in-the-clone rule;
// see generateKBInstructions). Pure: no I/O, no clock; KBs are rendered in the
// order given.
//
// `preamble: none` suppresses the bullets only when every KB opts out; with one
// KB that is exactly the block's rule (D154).
func RenderGuide(kbs []GuideKB, opts GuideOptions) string {
	if len(kbs) == 0 {
		return ""
	}
	var sb strings.Builder
	allOptOut := true
	for _, k := range kbs {
		sb.WriteString(routingSentence(k.Name, opts.Routed))
		sb.WriteString(archivesSentence(k.Archives, k.ArchivesHidden))
		allOptOut = allOptOut && k.SkipPreamble
	}
	if !allOptOut {
		sb.WriteString(GuideOperational(opts.ReadOnly))
	}
	for _, k := range kbs {
		if k.Curated == "" {
			continue
		}
		sb.WriteString("\n")
		sb.WriteString(scopeSentence(k.Name))
		sb.WriteString(k.Curated)
		sb.WriteString("\n")
	}
	return sb.String()
}

// GuideOperational is the guide's "Operational instructions" section alone.
func GuideOperational(readOnly bool) string {
	var sb strings.Builder
	sb.WriteString("Operational instructions:\n")
	sb.WriteString(orientationBullet)
	if readOnly {
		sb.WriteString(placeholderBullet)
		sb.WriteString(readOnlyBullet)
		return sb.String()
	}
	sb.WriteString(writeBullet)
	sb.WriteString(refusedWriteBullet)
	sb.WriteString(statusBullet)
	sb.WriteString(commitBullet)
	sb.WriteString(findingsBullet)
	sb.WriteString(placeholderBullet)
	sb.WriteString(procedureBullet)
	sb.WriteString(conflictBullet)
	return sb.String()
}
