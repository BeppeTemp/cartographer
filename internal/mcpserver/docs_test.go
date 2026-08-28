package mcpserver

// Guards the docs against tool-name drift: twice in a row, a rename left dead
// tool names behind in the markdown (D66 kb_overview→atlas_overview, D77
// archive_*/dossier_*→map_*), and both times it was found by hand, months
// later — the README was still advertising three tools that no longer existed.
// A rename that forgets a page now fails CI instead.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

// toolShaped matches a backticked snake_case identifier — the way the docs cite
// a tool (`concept_read`) as well as, unavoidably, lint rules, frontmatter
// fields and Go identifiers. toolPrefixes narrows it to the domains the MCP
// surface actually uses, and docsNonTools exempts the known homonyms.
var toolShaped = regexp.MustCompile(`\x60([a-z][a-z0-9_]*_[a-z0-9_]+)\x60`)

// toolPrefixes are the domain prefixes of the MCP tool names. A citation
// starting with one of these is expected to name a real tool.
var toolPrefixes = []string{
	"atlas_", "map_", "concept_", "index_", "log_", "graph_", "search_",
	"skill_", "service_", "secret_", "artifact_", "template_", "asset_", "sync_", "git_", "kb_", "archive_",
	"dossier_", "conflict_", "conflicts_", "commit_", "gate_", "changes_",
	"contradiction_",
}

// docsNonTools are identifiers that match toolShaped and a tool prefix but are
// not tools: lint rule names, frontmatter fields, and one deliberate mention of
// an operation that does not exist (documented as such).
var docsNonTools = map[string]bool{
	"artifact_signing_seed": true,
	// audit configuration keys (D119), not tools
	"archive_dir":       true,
	"retention_days":    true,
	"max_segment_bytes": true,
	// lint rules (internal/lint)
	"concept_oversize": true, "map_oversize": true, "machine_path": true,
	"expanded_as_category": true, "expanded_ambiguous": true,
	"expanded_missing_index": true, "legacy_archive_descriptor": true,
	"imported_draft": true, "broken_link": true, "stale_claim": true,
	"orphan_asset":     true,
	"index_incomplete": true,
	// frontmatter fields and schema keys
	"concept_id": true, "concept_types": true, "archive_type": true,
	// tool argument names, cited where the docs explain which argument
	// identifies the resource a write touched (commit subjects)
	"contradiction_id": true, "source_id": true,
	"contradiction_kind": true, "service_ref": true, "secret_refs": true,
	"secrets_source": true, "review_after": true, "asserted_by": true,
	"last_indexed_commit": true, "content_hash": true, "applied_revision": true,
	"allow_artifact_write": true, "sops_age_key_file": true, "token_env": true,
	"search_roots": true, "resolution_strategy": true, "resolution_body": true,
	// kb_status / sync_status output fields (D145), not tools
	"git_workflow": true, "git_profile": true, "git_sync": true,
	// a tool of ANOTHER agent, cited where Cartographer explains what it
	// deliberately leaves to it (D141: Hermes adopts a delivered skill with
	// its own skill_manage)
	"skill_manage": true,
	// intentionally absent, documented as not implemented (D77, YAGNI)
	"concept_collapse": true,
}

// docsFiles are the markdown files whose tool citations must resolve. The
// decision registers are excluded because they intentionally retain historical
// tool names. Walk recursively so a new nested current-state page is covered.
func docsFiles(t *testing.T) []string {
	t.Helper()
	root := filepath.Join("..", "..")
	var files []string
	for _, p := range []string{"README.md", "AGENTS.md", "CONTRIBUTING.md"} {
		files = append(files, filepath.Join(root, p))
	}
	docsRoot := filepath.Join(root, "docs")
	err := filepath.WalkDir(docsRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path == filepath.Join(docsRoot, "decisions") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".md") && entry.Name() != "decisions.md" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk docs/: %v", err)
	}
	return files
}

func TestDocsToolNamesExist(t *testing.T) {
	// Full surface: artifact writes and the bundle-backed skill/sync tools are
	// registered conditionally, and the docs describe them all.
	k := setupTestKB(t)
	k.AllowArtifactWrite = true
	s := New("test")
	RegisterKBTools(s, k, Deps{BundleFS: fstest.MapFS{}})
	registered := make(map[string]bool, len(s.tools))
	for name := range s.tools {
		registered[name] = true
	}

	for _, path := range docsFiles(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			for _, m := range toolShaped.FindAllStringSubmatch(line, -1) {
				name := m[1]
				if registered[name] || docsNonTools[name] {
					continue
				}
				hasPrefix := false
				for _, p := range toolPrefixes {
					if strings.HasPrefix(name, p) {
						hasPrefix = true
						break
					}
				}
				if !hasPrefix {
					continue
				}
				t.Errorf("%s:%d cites %q, which is not a registered MCP tool — "+
					"rename it, or add it to docsNonTools if it is not a tool",
					filepath.Base(path), i+1, name)
			}
		}
	}
}
