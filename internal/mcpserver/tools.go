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

	s.RegisterTool(toolAtlasOverview(k))
	s.RegisterTool(toolIndexGet(k))
	s.RegisterTool(toolConceptRead(k))
	s.RegisterTool(toolLogTail(k))
	s.RegisterTool(gitWrap(k, toolConceptWrite(k, live, deps.SQLIndex)))
	s.RegisterTool(gitWrap(k, toolConceptPatch(k, live, deps.SQLIndex)))
	s.RegisterTool(gitWrap(k, toolMapCreate(k)))
	s.RegisterTool(gitWrap(k, toolMapDelete(k)))
	s.RegisterTool(gitWrap(k, toolConceptExpand(k)))
	s.RegisterTool(gitWrap(k, toolLogAppend(k)))
	s.RegisterTool(gitWrap(k, toolSnapshot(k)))
	s.RegisterTool(toolValidate(k))
	s.RegisterTool(toolMapList(k))
	s.RegisterTool(toolConceptList(k))
	s.RegisterTool(toolGraphNeighbors(k))
	s.RegisterTool(toolSearch(k, live, deps))
	s.RegisterTool(toolIndexRebuild(k, live, deps))
	s.RegisterTool(toolLint(k))
	s.RegisterTool(toolCommitGate(k))
	s.RegisterTool(toolGateCheck(k))
	s.RegisterTool(gitWrap(k, toolSupersede(k)))
	s.RegisterTool(gitWrap(k, toolConceptMove(k, live, deps.SQLIndex)))
	s.RegisterTool(gitWrap(k, toolConceptDelete(k, live, deps.SQLIndex)))
	s.RegisterTool(gitWrap(k, toolConflictResolve(k)))
	s.RegisterTool(toolKBStatus(k))
	s.RegisterTool(toolContradictionReport(k))
	s.RegisterTool(toolConflictsList(k))
	s.RegisterTool(toolGitConflictResolve(k))
	s.RegisterTool(toolServiceGet(k))
	s.RegisterTool(toolServiceList(k))
	s.RegisterTool(toolArtifactRead(k))
	s.RegisterTool(toolArtifactList(k))
	if k.AllowArtifactWrite {
		s.RegisterTool(artifactNotifyWrap(s, gitWrap(k, toolArtifactWrite(k))))
		s.RegisterTool(artifactNotifyWrap(s, gitWrap(k, toolArtifactDelete(k))))
	}

	if deps.BundleFS != nil {
		s.RegisterTool(toolSkillListWithBundle(k, deps.BundleFS))
		s.RegisterTool(notifyWrap(s, gitWrap(k, toolSkillInstall(k, deps.BundleFS)), "notifications/skills/list_changed"))
		s.RegisterTool(toolSyncCheck(k, deps.BundleFS))
		s.RegisterTool(toolSyncApply(k, deps.BundleFS))
		s.RegisterTool(toolSyncPull(k, deps.BundleFS))
	} else {
		s.RegisterTool(toolSkillList(k))
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
