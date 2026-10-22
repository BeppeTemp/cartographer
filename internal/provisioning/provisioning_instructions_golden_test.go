package provisioning_test

// Golden pins of the managed instructions block (D56/D182), written before the
// block and the server's initialize guide (D1097) came to share one generator:
// the block, its ContentHash and so its signature must not change, or every
// provisioned client re-syncs its steering file.

import (
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

const goldenRouting = "The \"kb-a\" KB is served via MCP by the \"cartographer\" server. Its tools are not prefixed: where a tool takes a `kb` argument, pass `kb: \"kb-a\"` — that connection serves several KBs through one set of tools and never infers which one you mean."

const goldenBullets = "Operational instructions:\n" +
	"- consult it autonomously when you need historical or architectural context: `search` (keyword) or `atlas_overview` to orient yourself, `concept_read` to read; when the answer spans several linked pages, `graph_context` ranks them in one call instead of reading page by page and following links;\n" +
	"- write or update a page with `concept_write` when you discover something relevant; close relevant sessions with `log_append`;\n" +
	"- a write that would introduce an avoidable finding (broken link, missing index entry, orphan) is refused with each finding's fix and nothing is committed: write the target, the index entry and the link in the same `concept_batch`;\n" +
	"- status convention: `active` means the page is valid and current, not that work is open; open work uses `open` (accepted or queued, not started), `in-progress` or `blocked`: when you start executing an `open` item, set `in-progress` in the same `concept_patch` or `concept_batch` that records the first progress; when progress cannot continue, set `blocked` and declare `waiting_on` and `review_after`; a finished task is `done`;\n" +
	"- every write is a git commit, revertible.\n" +
	"- never run git commands in the KB's clone: report replication problems from `sync_status` to the operator instead; a push refused because of the commits' author is reported with the command `cartographer kb reauthor <kb>`, for the operator to run.\n" +
	"- when `kb_status` reports a `latest_version` that differs from `server_version`, tell the user once: the installed version, the available version and the upgrade command for this channel (`cartographer update check` prints it); offer to run it, run it only on explicit consent, then follow the cartographer-ops skill (§Upgrade).\n"

const goldenScope = "The following directives are the \"kb-a\" KB's own and govern work in its perimeter; where they conflict with another KB's directives or with a repository's own instruction file, the more specific source wins.\n\n"

func goldenBlock(t *testing.T, setup func(root string)) provisioning.Artifact {
	t.Helper()
	root := makeKBWithArchives(t, nil)
	setup(root)
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb-a": root}, provisioning.BuildOptions{RoutedMount: true})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	return findInstructionsArtifact(t, m, "kb-a")
}

func TestGolden_Block_RoutedNoArchive(t *testing.T) {
	a := goldenBlock(t, func(string) {})
	want := goldenRouting + " No archives detected yet.\n\n" + goldenBullets
	if got := string(a.Files[0].Content); got != want {
		t.Errorf("block changed:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestGolden_Block_RoutedArchivesAndCurated(t *testing.T) {
	a := goldenBlock(t, func(root string) {
		writeFile(t, filepath.Join(root, "data", "journal", "a.md"), "# a\n")
		writeFile(t, filepath.Join(root, "data", "entities", "b.md"), "# b\n")
		writeFile(t, filepath.Join(root, "instructions.md"), "Read the index first.\n")
	})
	want := goldenRouting + " Archives: entities/, journal/.\n\n" + goldenBullets +
		"\n" + goldenScope + "Read the index first.\n"
	if got := string(a.Files[0].Content); got != want {
		t.Errorf("block changed:\ngot:\n%s\nwant:\n%s", got, want)
	}
	const wantHash = "3be5ac4df889bfe25ab525c3a545e776c51e87fa30c12b83c121dae2dc3f0008"
	if a.ContentHash != wantHash {
		t.Errorf("ContentHash = %s, want %s", a.ContentHash, wantHash)
	}
}

func TestGolden_Block_PreambleNone(t *testing.T) {
	a := goldenBlock(t, func(root string) {
		writeFile(t, filepath.Join(root, "data", "entities", "b.md"), "# b\n")
		writeFile(t, filepath.Join(root, "instructions.md"), "<!-- cartographer: preamble: none -->\nOnly this.\n")
	})
	want := goldenRouting + " Archives: entities/.\n\n" + "\n" + goldenScope + "Only this.\n"
	if got := string(a.Files[0].Content); got != want {
		t.Errorf("block changed:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
