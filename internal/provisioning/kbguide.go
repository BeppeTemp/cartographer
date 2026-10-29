package provisioning

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// The pieces of the instructions text, shared by two renderings (D1097): the
// managed block a provisioned client writes into its steering file (brief by
// default, D1182: generateKBInstructions per KB, SharedOperationalSection once
// per block) and the operating guide the server sends in `initialize` to a
// connection that has no such block (RenderGuide, the full rendering).

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
	// #1316: the last line of the guide. The managed block does not carry any of
	// it; kb_guide {} stays whole for a host that drops `initialize` (D1100).
	wholeGuideBullet = "- these instructions are the whole guide: `kb_guide` without arguments only repeats them for a host that does not show them;\n"
	readOnlyBullet   = "- this connection is read-only: write tools are not listed and are refused.\n"

	// The bullets only the managed block carries (they make sense with a
	// Cartographer client: `cartographer kb reauthor`, `cartographer update
	// check`, the cartographer-ops skill), kept here beside the shared ones so
	// there is one copy of the block's operational text.
	// D264: an agent's own git in the clone (init, checkout -b, push) is what
	// forks a KB on its remote; the server owns that clone.
	gitBullet = "- never run git commands in the KB's clone: report replication problems from `sync_status` to the operator instead; a push refused because of the commits' author is reported with the command `cartographer kb reauthor <kb>`, for the operator to run.\n"
	// D320: the fallback update channel for every client whose SessionStart
	// hook does not fire (Kiro default chat, timer-only clients): kb_status
	// carries latest_version (D254).
	updateBullet = "- when `kb_status` reports a `latest_version` that differs from `server_version`, tell the user once: the installed version, the available version and the upgrade command for this channel (`cartographer update check` prints it); offer to run it, run it only on explicit consent, then follow the cartographer-ops skill (§Upgrade).\n"
	// D1182: a procedure answered from a search excerpt is the failure the
	// 35-task measurement found; the skill names in the brief sections only
	// work together with this rule.
	readWholeBullet = "- procedures, runbooks and incidents are read whole with `concept_read`, never answered from a search excerpt or a snippet;\n"
	// D1182: where everything the brief sections leave out lives.
	guideBullet = "- `kb_guide` returns the full operating guide, each KB's own directives and its skills with descriptions, on demand: call it when a task touches a KB's own rules or when a skill name in a `Skills:` line looks relevant (on a routed connection it takes the `kb` argument like every tool).\n"

	// preambleNoneComment is what a brief section of a KB that opted out of the
	// generated operational text (`preamble: none`) keeps as its first line, so
	// the client can tell without the curated text (D1182).
	preambleNoneComment = "<!-- cartographer: preamble: none -->"
	// operationalHeading opens the operational section; a section that already
	// carries it (a server that still sends the full sections) makes the client
	// skip its own copy.
	operationalHeading = "Operational instructions:"
)

// SharedOperationalSection is the operational text of the managed block,
// written once per block after the last KB's section (D1182) instead of once
// per KB. Client-owned: no artifact hash covers it, so applyInstructionsGroup
// folds it into the hash the lock records for the client-side tail of the block.
func SharedOperationalSection() string {
	var sb strings.Builder
	sb.WriteString(operationalHeading + "\n")
	sb.WriteString(orientationBullet)
	sb.WriteString(writeBullet)
	sb.WriteString(refusedWriteBullet)
	sb.WriteString(statusBullet)
	sb.WriteString(commitBullet)
	sb.WriteString(gitBullet)
	sb.WriteString(updateBullet)
	sb.WriteString(readWholeBullet)
	sb.WriteString(guideBullet)
	return sb.String()
}

// sharedSectionWanted reports whether the block should carry the shared
// operational section for these (expanded) KB sections: not when every one of
// them opted out with the preamble comment (the RenderGuide rule), and not when
// one already carries the heading, which is what a server that still sends the
// full sections produces (mixed versions degrade, never duplicate).
func sharedSectionWanted(sections []string) bool {
	allOptOut := true
	for _, c := range sections {
		lines := strings.Split(c, "\n")
		if len(lines) == 0 || strings.TrimSpace(lines[0]) != preambleNoneComment {
			allOptOut = false
		}
		// Only the generated head counts: a `block: full` KB's curated body,
		// which follows the scope sentence, may hold a line of the same text.
		head := c
		if i := strings.Index(c, scopeSentencePrefix); i >= 0 {
			head = c[:i]
		}
		for _, l := range strings.Split(head, "\n") {
			if l == operationalHeading {
				return false
			}
		}
	}
	return !allOptOut
}

// scopeSentence precedes a KB's curated body (D182 WP1): the body is one KB's
// own prose, not a session-wide law, and it is what lets a model apply the
// ordinary "more specific source wins" rule when two KBs' curated directives
// (or a repository's own instruction file) disagree — D171 cannot catch that
// class of conflict (unique by construction), so provenance and precedence are
// declared instead of accidental. Emitted only when there IS curated content to
// scope; the opt-out marker only suppresses the operational bullets.
func scopeSentence(kbName string) string {
	return fmt.Sprintf(scopeSentencePrefix+"%q KB's own and govern work in its perimeter; where they conflict with another KB's directives or with a repository's own instruction file, the more specific source wins.\n\n", kbName)
}

// scopeSentencePrefix opens the scope sentence, which ends the generated head of
// a region (sharedSectionWanted looks no further).
const scopeSentencePrefix = "The following directives are the "

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
}

// ProcedureLine is one served procedure in the guide's list (#1316).
type ProcedureLine struct{ Short, UseWhen string }

// coldStartSection is what an empty KB's guide adds after its routing
// paragraph (#1316): how to create the first map and what is reserved. Guide
// only, never the managed block (paid on every model call, D1182).
func coldStartSection() string {
	return fmt.Sprintf("This KB is empty. Create its first map with `map_create` (`name` kebab-case, `title`; `kind: journal` for a dated log of events or sources), then write pages with `concept_write`. Reserved map name: %s. Documented services are pages under `services/<id>`, not a map.\n\n",
		strings.Join(kb.ReservedMapNames, ", "))
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
		sb.WriteString(GuideOperational(opts.ReadOnly, opts.Procedures))
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
func GuideOperational(readOnly bool, procs []ProcedureLine) string {
	var sb strings.Builder
	sb.WriteString("Operational instructions:\n")
	sb.WriteString(orientationBullet)
	if readOnly {
		sb.WriteString(placeholderBullet)
		sb.WriteString(procedureBullets(procs))
		sb.WriteString(wholeGuideBullet)
		sb.WriteString(readOnlyBullet)
		return sb.String()
	}
	sb.WriteString(writeBullet)
	sb.WriteString(refusedWriteBullet)
	sb.WriteString(statusBullet)
	sb.WriteString(commitBullet)
	sb.WriteString(findingsBullet)
	sb.WriteString(placeholderBullet)
	sb.WriteString(procedureBullets(procs))
	sb.WriteString(wholeGuideBullet)
	sb.WriteString(conflictBullet)
	return sb.String()
}
