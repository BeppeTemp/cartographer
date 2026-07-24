package mcpserver

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"

	"github.com/BeppeTemp/cartographer/internal/embed"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/search"
	"github.com/BeppeTemp/cartographer/internal/sqlindex"
)

// Deps holds the optional dependencies for tool registration.
type Deps struct {
	Embedder embed.Embedder  // nil → keyword-only search
	VecStore *embed.Store    // required if Embedder is set
	SQLIndex *sqlindex.Index // nil → in-memory index
	BundleFS fs.FS           // nil → no bundled skills
}

// RegisterKBTools registers all KB tools on the server, including search, navigation,
// bundled skills and semantic search, according to the provided Deps.
//
//   - search / index_rebuild: if deps.SQLIndex is set, keyword search runs against
//     SQLite FTS5 (falling back to the in-memory index on failure) and semantic
//     search uses the SQLite embedding cache; otherwise, if deps.Embedder and
//     deps.VecStore are both set, hybrid in-memory search is used; otherwise
//     search falls back to the keyword-only in-memory index.
//   - concept_write shares the same in-memory keyword index (via a single
//     liveIndex) with search/index_rebuild, so a successful write is
//     immediately reflected by keyword search. When deps.SQLIndex is set,
//     concept_write also best-effort-upserts the write into SQLite FTS5.
//   - skill_list: if deps.BundleFS is set, uses the bundle-aware variant and also
//     registers skill_install, sync_check, sync_apply and sync_pull; otherwise uses
//     the installed-only variant.
//
// Builds the keyword index at registration time.
func RegisterKBTools(s *Server, k *kb.KB, deps Deps) {
	register := func(t Tool) {
		if t.ReadOnly {
			t = readSyncWrap(k, t)
		}
		s.RegisterTool(t)
	}

	// D76/WP4: route conflicts detected by the async push worker (see
	// pushworker.go) through the same conflict-registry/degraded handling
	// used for synchronous pushes in gitWrap. No-op when SyncOutDebounce==0
	// (the worker is never started in that case).
	k.OnPushConflict = func(rce *gitx.RebaseConflictError) {
		n := handleConflictError(k, rce)
		fmt.Fprintf(os.Stderr,
			"cartographer: git conflict during async push: registered %d concept(s) as degraded\n", n)
	}

	idx, meta := buildIndex(k)
	live := newLiveIndex(idx, meta)
	if deps.SQLIndex != nil {
		k.OnSyncIn = func() {
			stats, err := ReconcileIndex(k, live, deps.SQLIndex)
			if err != nil {
				fmt.Fprintf(os.Stderr, "cartographer: reconcile after git sync: %v\n", err)
				return
			}
			if stats.Indexed > 0 || stats.Updated > 0 || stats.Removed > 0 {
				fmt.Fprintf(os.Stderr, "cartographer: reconciled index after git sync: indexed=%d updated=%d removed=%d\n", stats.Indexed, stats.Updated, stats.Removed)
			}
		}
	}

	register(toolAtlasOverview(k))
	register(toolIndexGet(k))
	register(toolConceptRead(k))
	register(toolLogTail(k))
	register(toolChangesSince(k))
	register(gitWrap(k, toolConceptWrite(k, live, deps.SQLIndex)))
	register(gitWrap(k, toolConceptPatch(k, live, deps.SQLIndex)))
	register(gitWrap(k, toolMapCreate(k)))
	register(gitWrap(k, toolMapDelete(k)))
	register(gitWrap(k, toolConceptExpand(k)))
	register(gitWrap(k, toolLogAppend(k)))
	register(gitWrap(k, toolSnapshot(k)))
	register(toolValidate(k))
	register(toolMapList(k))
	register(toolConceptList(k))
	register(toolGraphNeighbors(k))
	register(toolSearch(k, live, deps))
	register(toolIndexRebuild(k, live, deps))
	register(toolReindex(k, live, deps.SQLIndex))
	register(toolLint(k))
	register(toolCommitGate(k))
	register(toolGateCheck(k))
	register(gitWrap(k, toolSupersede(k)))
	register(gitWrap(k, toolConceptMove(k, live, deps.SQLIndex)))
	register(gitWrap(k, toolConceptDelete(k, live, deps.SQLIndex)))
	register(gitWrap(k, toolConflictResolve(k)))
	register(toolKBStatus(k))
	register(toolContradictionReport(k))
	register(toolConflictsList(k))
	register(toolGitConflictResolve(k))
	register(toolServiceGet(k))
	register(toolServiceList(k))
	register(toolArtifactRead(k))
	register(toolArtifactList(k))
	if k.AllowArtifactWrite {
		register(artifactNotifyWrap(s, gitWrap(k, toolArtifactWrite(k))))
		register(artifactNotifyWrap(s, gitWrap(k, toolArtifactDelete(k))))
	}

	if deps.BundleFS != nil {
		register(toolSkillListWithBundle(k, deps.BundleFS))
		register(notifyWrap(s, gitWrap(k, toolSkillInstall(k, deps.BundleFS)), "notifications/skills/list_changed"))
		register(toolSyncCheck(k, deps.BundleFS))
		register(toolSyncApply(k, deps.BundleFS))
		register(toolSyncPull(k, deps.BundleFS))
	} else {
		register(toolSkillList(k))
	}
}

// notifyWrap fires an MCP notification after the wrapped tool completes successfully
// (no Go error and res.IsError==false).
func notifyWrap(s *Server, t Tool, method string) Tool {
	orig := t
	t.Handler = func(args json.RawMessage) (ToolResult, error) {
		res, err := orig.Handler(args)
		if err == nil && !res.IsError {
			_ = s.Notify(method, map[string]any{})
		}
		return res, err
	}
	return t
}

// buildIndex builds the in-memory keyword index and its parallel per-concept
// metadata (title, body — used by search to fill title/snippet without a
// ReadConcept call, D70).
func buildIndex(k *kb.KB) (*search.Index, map[string]conceptMeta) {
	idx := search.New()
	meta := make(map[string]conceptMeta)
	k.WalkConcepts(func(id okf.ConceptID, content string) error {
		idx.Add(string(id), content)
		meta[string(id)] = parseConceptMeta(content)
		return nil
	})
	return idx, meta
}
