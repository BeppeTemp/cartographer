package provisioning

import (
	"strings"

	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// The pieces of the instructions text, shared by two renderings (D1097): the
// managed block a provisioned client writes into its steering file (brief by
// default, D1182: generateKBInstructions per KB, SharedOperationalSection once
// per block) and the operating guide the server sends in `initialize` to a
// connection that has no such block (RenderGuide, the full rendering).

// WithoutSkillsLine returns a copy of a KB's generated `instructions` artifact
// without its skills line (#1408): what a client with `kb_options.<kb>.artifacts:
// false` writes, so the block no longer names skills it does not install. The
// routing and maps paragraph stay: the KB is still connected.
//
// The line is the one right after the first blank line (the end of the
// routing/maps paragraph, archivesSentence ends in "\n\n") when it starts with
// "Skills: ". A "Skills:" line anywhere else is the curated body of a
// `block: full` KB and is never touched. Anything else (no skills line, another
// kind, another source, several files) is returned unchanged.
//
// It runs AFTER VerifiedManifest and rehashes the content: the signature covers
// the original hash, Apply trusts the Signed flag and never re-verifies, so the
// rewrite keeps Signed/Signature as verification left them. Rewriting before
// verification would make the signature fail. TestWithoutSkillsLineMatchesGenerate
// pins it byte for byte against generateKBInstructions.
func WithoutSkillsLine(a contract.Artifact) contract.Artifact {
	if a.Kind != "instructions" || !strings.HasPrefix(a.Source, "kb:") || len(a.Files) != 1 {
		return a
	}
	text := string(a.Files[0].Content)
	head, rest, ok := strings.Cut(text, "\n\n")
	if !ok || !strings.HasPrefix(rest, "Skills: ") {
		return a
	}
	line, tail, ok := strings.Cut(rest, "\n")
	if !ok || line == "" {
		return a
	}
	f := a.Files[0]
	f.Content = []byte(head + "\n\n" + tail)
	files := []contract.ArtifactFile{f}
	a.Files = files
	a.ContentHash = contract.HashArtifactFiles(files)
	return a
}

// Operational bullets. OrientationBullet and placeholderBullet are the ones a
// read-only connection keeps; the rest instruct a writer.
const (

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
	sb.WriteString(contract.OrientationBullet)
	sb.WriteString(contract.WriteBullet)
	sb.WriteString(contract.RefusedWriteBullet)
	sb.WriteString(contract.StatusBullet)
	sb.WriteString(contract.CommitBullet)
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
		if len(lines) == 0 || strings.TrimSpace(lines[0]) != contract.PreambleNoneComment {
			allOptOut = false
		}
		// Only the generated head counts: a `block: full` KB's curated body,
		// which follows the scope sentence, may hold a line of the same text.
		head := c
		if i := strings.Index(c, contract.ScopeSentencePrefix); i >= 0 {
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
