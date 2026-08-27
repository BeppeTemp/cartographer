package provisioning

// Hermes delivery (D141). Hermes owns its own skills: a curator under
// HERMES_HOME/skills/ archives, rewrites and pins them from its own learning
// loop. Materializing there would put Cartographer and the curator in a
// permanent fight, so a KB skill is DELIVERED to skill-inbox/<name>/cartographer/
// as a proposal, with a generated SOURCE.md saying where it came from and what
// adopting it means. The agent decides; Cartographer never installs.

import (
	"fmt"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// generatedArtifactFiles returns the files Cartographer adds to an artifact's
// materialized directory beyond the KB's own — today only hermes' SOURCE.md.
//
// A KB skill that already ships a file with a generated name is a collision:
// overwriting it would silently destroy KB content, and delivering both is
// impossible. That one artifact fails with a warning naming it; the rest of
// the sync completes, and the collision persists (nothing is recorded in the
// lockfile) until the KB is fixed.
func generatedArtifactFiles(a Artifact, provider configurator.Provider, files []ArtifactFile) (generated []ArtifactFile, warning string, ok bool) {
	if provider != configurator.ProviderHermes || a.Kind != "skill" {
		return nil, "", true
	}
	for _, f := range files {
		if strings.EqualFold(strings.TrimPrefix(f.Path, "./"), hermesSourceFile) {
			return nil, fmt.Sprintf(
				"skill %q ships its own %s: not delivered to the hermes inbox, which generates that file (rename it in the KB)",
				a.Name, hermesSourceFile), false
		}
	}
	return []ArtifactFile{{Path: hermesSourceFile, Content: hermesSourceDoc(a)}}, "", true
}

// hermesSourceDoc renders the delivery note. Deterministic — no timestamp, no
// manifest revision — so an unchanged skill re-delivers byte-identically and
// the sync stays idempotent; the artifact's content hash is what distinguishes
// a new proposal from a re-delivery.
func hermesSourceDoc(a Artifact) []byte {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Cartographer delivery: %s\n\n", a.Name)
	sb.WriteString("This directory was delivered by Cartographer. It is a **proposal**, not an installed skill:\n")
	sb.WriteString("nothing under `$HERMES_HOME/skills/` was written, and nothing here is active.\n\n")

	kb, fromKB := kbSourceName(a)
	if fromKB {
		fmt.Fprintf(&sb, "- Source: KB %q, path `%s`\n", kb, artifactSourcePath(a))
	} else {
		sb.WriteString("- Source: bundled with the Cartographer server\n")
	}
	fmt.Fprintf(&sb, "- Artifact content hash: `%s`\n\n", a.ContentHash)

	sb.WriteString("Adopting it is `skill_manage`'s job: compare it with the skill you already have, take what is\n")
	sb.WriteString("worth taking, then reload and verify. Cartographer re-delivers this directory in place on every\n")
	sb.WriteString("sync — the content hash above is what tells an unchanged re-delivery from a new proposal.\n\n")

	if fromKB {
		fmt.Fprintf(&sb, "To change this skill for every client, change it in the KB: `artifact_write` on KB %q, path\n`%s`. Edits made here reach nothing else and are replaced on the next sync.\n",
			kb, artifactSourcePath(a))
	} else {
		sb.WriteString("This skill ships inside the Cartographer binary: changing it for every client means changing\n")
		sb.WriteString("the release. Edits made here reach nothing else and are replaced on the next sync.\n")
	}
	return []byte(sb.String())
}
