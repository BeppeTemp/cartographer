package contract

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// routingSentence says that the KB exists and, on a routed connection, which
// value its `kb` argument takes (D187/D288: one endpoint, one copy of the
// tools, bare names; a connection bound to one KB has no `kb` argument, so the
// sentence names the value and says where it applies instead of requiring it;
// #1246/D1246 cut the clause to 105 bytes per KB, it is paid once per KB).
func routingSentence(kbName string, routed bool) string {
	s := fmt.Sprintf("The %q KB is served via MCP by the \"cartographer\" server.", kbName)
	if routed {
		s += fmt.Sprintf(" Its tools are not prefixed: where a tool takes a `kb` argument, pass `kb: %q`; it is never inferred.", kbName)
	}
	return s
}

// archivesSentence ends the routing paragraph. noneVisible distinguishes a KB
// that has no map from one whose maps the caller may not see. The word is
// "Maps", the one the tools use (map_create); the Go names keep "archive"
// (#1316). The block shares this text, so its hash moved once with the word.
func archivesSentence(archives []string, noneVisible bool) string {
	if len(archives) == 0 {
		if noneVisible {
			return " No maps are visible to this connection.\n\n"
		}
		return " No maps yet.\n\n"
	}
	names := make([]string, len(archives))
	for i, a := range archives {
		names[i] = a + "/"
	}
	return fmt.Sprintf(" Maps: %s.\n\n", strings.Join(names, ", "))
}

// maxSkillNames caps the skill catalog of one KB's brief section (D1182):
// without a cap one skill-rich KB defeats the budget of the block.
const maxSkillNames = 40

// skillsSentence is the catalog line of a KB's brief section: the names of its
// skills, sorted, and nothing else (descriptions come from kb_guide). Empty
// when the KB has no skill. Shared with kb_guide's tests, stable imprinting:
// names only, no counts but the overflow (D65).
func skillsSentence(names []string) string {
	if len(names) == 0 {
		return ""
	}
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	more := 0
	if len(sorted) > maxSkillNames {
		more = len(sorted) - maxSkillNames
		sorted = sorted[:maxSkillNames]
	}
	line := "Skills: " + strings.Join(sorted, ", ")
	if more > 0 {
		line += fmt.Sprintf(", +%d more (kb_guide)", more)
	}
	return line + ".\n"
}

const // D780: graph_context is named here because agents choose the tools this
// line names; nothing else in the session says when to use it.
OrientationBullet = "- consult it autonomously when you need historical or architectural context: `search` (keyword) or `atlas_overview` to orient yourself, `concept_read` to read; when the answer spans several linked pages, `graph_context` ranks them in one call instead of reading page by page and following links;\n"

const WriteBullet = "- write or update a page with `concept_write` when you discover something relevant; close relevant sessions with `log_append`;\n"

const // D907: the write gate is always on and nothing accepts a finding.
RefusedWriteBullet = "- a write that would introduce an avoidable finding (broken link, missing index entry, orphan) is refused with each finding's fix and nothing is committed: write the target, the index entry and the link in the same `concept_batch`;\n"

const // D321: one meaning for `active`, said once, true for every KB.
StatusBullet = "- status convention: `active` means the page is valid and current, not that work is open; open work uses `open` (accepted or queued, not started), `in-progress` or `blocked`: when you start executing an `open` item, set `in-progress` in the same `concept_patch` or `concept_batch` that records the first progress; when progress cannot continue, set `blocked` and declare `waiting_on` and `review_after`; a finished task is `done`;\n"

const CommitBullet = "- every write is a git commit, revertible.\n"

const // Guide-only bullets (D1097): the block's reader has them from its other
// directives or its client; a generic connection has only this text.
findingsBullet = "- act on the `findings` every write returns before moving on: they name what to fix;\n"

const placeholderBullet = "- `{{repo:<key>}}` and `{{path:<name>}}` in a page or a directive are machine-local path placeholders: keep them verbatim, never resolve or rewrite them;\n"

// Feedback modes as the served instructions know them (feedback.destination;
// this package cannot import internal/feedback). A missing or unknown mode is
// github.
const (
	FeedbackGitHub = "github"
	FeedbackCustom = "custom"
	FeedbackOff    = "off"
)

const feedbackBullet = "- on a likely defect in Cartographer (an internal error, behaviour that contradicts the docs, a limit you must work around, a user complaint), offer once per problem and per session, if the user agrees, a report via the `cartographer-feedback` skill; never file or offer silently;\n"

const feedbackBulletCustom = "- on a likely defect in Cartographer (an internal error, behaviour that contradicts the docs, a limit you must work around, a user complaint), offer once per problem and per session, if the user agrees, a report via the `cartographer-feedback` skill; never file or offer silently; it goes to the operator's tracker: use the link `cartographer feedback` prints (no shell: no link);\n"

// FeedbackBullet is the operational bullet that says when to offer a report
// (D1925). It is one line, client-neutral, and empty when the
// operator switched feedback off: the offer lines and the internal-error hint
// go together.
func FeedbackBullet(mode string) string {
	switch mode {
	case FeedbackOff:
		return ""
	case FeedbackCustom:
		return feedbackBulletCustom
	}
	return feedbackBullet
}

const conflictBullet = "- a write blocked by a git conflict is settled with `conflicts_list` and `git_conflict_resolve`.\n"

const // #1316: the last line of the guide. The managed block does not carry any of
// it; kb_guide {} stays whole for a host that drops `initialize` (D1100).
wholeGuideBullet = "- these instructions are the whole guide: `kb_guide` without arguments only repeats them for a host that does not show them;\n"

const readOnlyBullet = "- this connection is read-only: write tools are not listed and are refused.\n"

const // preambleNoneComment is what a brief section of a KB that opted out of the
// generated operational text (`preamble: none`) keeps as its first line, so
// the client can tell without the curated text (D1182).
PreambleNoneComment = "<!-- cartographer: preamble: none -->"

// scopeSentence precedes a KB's curated body (D182 WP1): the body is one KB's
// own prose, not a session-wide law, and it is what lets a model apply the
// ordinary "more specific source wins" rule when two KBs' curated directives
// (or a repository's own instruction file) disagree — D171 cannot catch that
// class of conflict (unique by construction), so provenance and precedence are
// declared instead of accidental. Emitted only when there IS curated content to
// scope; the opt-out marker only suppresses the operational bullets.
func scopeSentence(kbName string) string {
	return fmt.Sprintf(ScopeSentencePrefix+"%q KB's own and govern work in its perimeter; where they conflict with another KB's directives or with a repository's own instruction file, the more specific source wins.\n\n", kbName)
}

// ScopeSentencePrefix opens the scope sentence, which ends the generated head of
// a region (sharedSectionWanted looks no further).
const ScopeSentencePrefix = "The following directives are the "

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
	// Procedures are the procedures this server serves with their body, as
	// the caller builds them (pure here: no bundle I/O). Empty: no list.
	Procedures []ProcedureLine
	// Feedback is the feedback.destination mode ("github", "custom", "off"; ""
	// is github): it selects the bullet that says when to offer a report.
	Feedback string
}

// ProcedureLine is one served procedure in the guide's list (#1316).
type ProcedureLine struct{ Short, UseWhen string }

// coldStartSection is what an empty KB's guide adds after its routing
// paragraph (#1316): how to create the first map and what is reserved. Guide
// only, never the managed block (paid on every model call, D1182).
func coldStartSection() string {
	return fmt.Sprintf("This KB is empty. Create its first map with `map_create` (`name` kebab-case, `title`; `kind: journal` for a dated log of events or sources), then write pages with `concept_write`. Reserved map name: %s. Documented services are pages under `services/<id>`, not a map.\n\n",
		strings.Join(okf.ReservedMapNames, ", "))
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
		if len(k.Archives) == 0 && !k.ArchivesHidden && !opts.ReadOnly && !k.SkipPreamble {
			sb.WriteString(coldStartSection())
		}
		allOptOut = allOptOut && k.SkipPreamble
	}
	if !allOptOut {
		sb.WriteString(GuideOperational(opts.ReadOnly, opts.Procedures, opts.Feedback))
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

// procedureBullets renders the procedures list: a heading line, then one line
// per procedure. Nothing when none is served (never a dangling reference).
func procedureBullets(procs []ProcedureLine) string {
	if len(procs) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("- procedures, each opened with `kb_guide` `procedure: <name>`:\n")
	for _, p := range procs {
		fmt.Fprintf(&sb, "  - `%s`: %s;\n", p.Short, p.UseWhen)
	}
	return sb.String()
}

// GuideOperational is the guide's "Operational instructions" section alone.
func GuideOperational(readOnly bool, procs []ProcedureLine, feedbackMode string) string {
	var sb strings.Builder
	sb.WriteString("Operational instructions:\n")
	sb.WriteString(OrientationBullet)
	if readOnly {
		sb.WriteString(placeholderBullet)
		sb.WriteString(procedureBullets(procs))
		sb.WriteString(FeedbackBullet(feedbackMode))
		sb.WriteString(wholeGuideBullet)
		sb.WriteString(readOnlyBullet)
		return sb.String()
	}
	sb.WriteString(WriteBullet)
	sb.WriteString(RefusedWriteBullet)
	sb.WriteString(StatusBullet)
	sb.WriteString(CommitBullet)
	sb.WriteString(findingsBullet)
	sb.WriteString(placeholderBullet)
	sb.WriteString(procedureBullets(procs))
	sb.WriteString(FeedbackBullet(feedbackMode))
	sb.WriteString(wholeGuideBullet)
	sb.WriteString(conflictBullet)
	return sb.String()
}
