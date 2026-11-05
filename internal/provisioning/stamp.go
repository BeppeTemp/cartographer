package provisioning

// Provenance stamping (D138): a skill or agent materialized on a client says
// what it is, where it came from, and how to change it. Without it a
// materialized SKILL.md is indistinguishable from a hand-written one — an
// agent that improves it has no address to send the improvement to, and no
// warning that the next sync replaces its edit.
//
// Client-side only: the KB's own copy is never stamped, for the same reason
// placeholders are not expanded server-side (the content hash and if_match
// must keep describing the source, docs/sync.md §Path portability placeholders).

import (
	"fmt"
	"strings"
)

// provenanceBlockBeginPrefix is what RECOGNIZES an existing begin marker: the
// tail after the em dash is display text, not marker identity — matching the
// full constant would miss a block written by another version and duplicate
// it on the next sync (the same rule as instructionsBlockBeginPrefix).
const (
	provenanceBlockBeginPrefix = "<!-- cartographer:provenance:begin"
	provenanceBlockBegin       = provenanceBlockBeginPrefix + " — managed by Cartographer, do not edit by hand -->"
	provenanceBlockEnd         = "<!-- cartographer:provenance:end -->"
)

// stampedKinds are the kinds that carry a provenance block: the two an agent
// actually reads. Hooks are executable scripts and hook.json (a comment would
// change program semantics), "mcp" is JSON (no comment syntax), and
// "instructions" already announces itself with its own managed block.
var stampedKinds = map[string]bool{"skill": true, "agent": true}

// stampArtifact returns content with this artifact's provenance block, or
// content unchanged for a kind that is not stamped. A block that already ENDS the
// content, as appendBlock writes it, is replaced rather than nested, so stamping
// stamped bytes is a fixed point. Only the trailing block is touched: a skill
// that documents D138 and quotes the block in a code fence keeps everything
// between the quoted markers (#1502; the block was cut from the first begin
// marker found anywhere to the first end after it).
func stampArtifact(content []byte, a Artifact) []byte {
	if !stampedKinds[a.Kind] {
		return content
	}
	return []byte(appendBlock(stripTrailingProvenance(string(content)), provenanceBlock(a)))
}

// stripTrailingProvenance removes the provenance block that ends s, if any: the
// last begin line, when everything after it up to the end of s is that block.
func stripTrailingProvenance(s string) string {
	trimmed := strings.TrimRight(s, "\n")
	if !strings.HasSuffix(trimmed, provenanceBlockEnd) {
		return s
	}
	i := strings.LastIndex(trimmed, "\n"+provenanceBlockBeginPrefix)
	switch {
	case i >= 0:
		i++ // keep the newline that ends the previous line
	case strings.HasPrefix(trimmed, provenanceBlockBeginPrefix):
		i = 0
	default:
		return s
	}
	// Anything between the begin and the end must be only the block itself: a
	// second end marker would mean this "last begin" is not the block's own.
	if strings.Count(trimmed[i:], provenanceBlockEnd) != 1 {
		return s
	}
	return trimmed[:i]
}

// artifactSourcePath is where this artifact lives inside its KB — the path an
// artifact_write call needs.
func artifactSourcePath(a Artifact) string {
	if a.Kind == "agent" {
		return "agents/" + a.Name + ".md"
	}
	return "skills/" + a.Name + "/SKILL.md"
}

// provenanceBlock renders the block for a. It carries no timestamp and no
// manifest revision on purpose: both would change on every sync (the revision
// on any *other* artifact's change), rewriting every file constantly and
// defeating hash comparison. The artifact's own content hash is stable until
// the artifact itself changes.
func provenanceBlock(a Artifact) string {
	// A skill derived from an agent says where the agent lives: that is the
	// address a fix goes to, and its hash is the one the KB knows.
	origin, derived := a.Origin()
	a = origin
	var sb strings.Builder
	sb.WriteString(provenanceBlockBegin)
	sb.WriteString("\n")

	if kb, ok := kbSourceName(a); ok {
		fmt.Fprintf(&sb, "Source: KB %q, path %s\n", kb, artifactSourcePath(a))
		fmt.Fprintf(&sb, "Artifact content hash: %s\n", a.ContentHash)
		fmt.Fprintf(&sb, "Local edits to this file are replaced on the next `cartographer sync`. To change it for good, call artifact_write on the %q KB with path %s.\n",
			kb, artifactSourcePath(a))
	} else {
		// Bundled artifacts ship inside the Cartographer binary: there is no
		// KB to write back to, and no artifact_write instruction to give.
		sb.WriteString("Source: bundled with the Cartographer server.\n")
		fmt.Fprintf(&sb, "Artifact content hash: %s\n", a.ContentHash)
		sb.WriteString("Local edits to this file are replaced on the next `cartographer sync`; changing it for good means changing the Cartographer release that ships it.\n")
	}

	if derived {
		sb.WriteString("Derived by Cartographer as a skill on this client (no subagents); local edits are replaced on the next sync.\n")
	}
	sb.WriteString(provenanceBlockEnd)
	return sb.String()
}

// kbSourceName returns the KB an artifact came from, or ok=false for a bundled
// one (Source "bundle", BuiltIn).
func kbSourceName(a Artifact) (string, bool) {
	if name := strings.TrimPrefix(a.Source, "kb:"); name != a.Source && name != "" {
		return name, true
	}
	return "", false
}
