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
// content unchanged for a kind that is not stamped. Any existing block is
// replaced, never nested: the block is always rebuilt from the source content,
// so re-materializing an already-stamped file is a fixed point.
func stampArtifact(content []byte, a Artifact) []byte {
	if !stampedKinds[a.Kind] {
		return content
	}
	block := provenanceBlock(a)
	body, replaced := replaceBlock(string(content), provenanceBlockBeginPrefix, provenanceBlockEnd, "")
	if !replaced {
		body = string(content)
	}
	return []byte(appendBlock(body, block))
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
