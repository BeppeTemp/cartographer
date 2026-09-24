package mcpserver

import (
	"crypto/ed25519"
	"fmt"
	"io/fs"
	"os"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/sqlindex"
)

// Deps holds the optional dependencies for tool registration.
type Deps struct {
	SQLIndex       *sqlindex.Index    // nil → in-memory index
	BundleFS       fs.FS              // nil → no bundled skills
	ArtifactSigner ed25519.PrivateKey // nil → KB artifacts remain unsigned
	MCPAllowlist   []provisioning.MCPAllowlistEntry
	// RoutedMount says this server's KBs are reachable through the single
	// routed endpoint (D187). It reaches the generated instructions block, the
	// part the model actually reads, so the imprinting names the bare tools and
	// the `kb` value to pass — a wrong name there is worse than the duplication
	// routing removes.
	RoutedMount bool
}

// RegisterKBTools registers all KB tools on the server, including search,
// navigation and bundled skills, according to the provided Deps.
//
//   - search / reindex: if deps.SQLIndex is set, keyword search runs
//     against SQLite FTS5, falling back to the in-memory index on failure;
//     otherwise it runs against the in-memory keyword index. Keyword search is
//     the only mode (D135).
//   - no write tool touches an index: every search reconciles both indexes
//     with the files first (D245), so a write — or any other change to a
//     concept file — is reflected by the next search.
//   - skill_list: if deps.BundleFS is set, uses the bundle-aware variant and also
//     registers skill_install, sync_check, sync_apply and sync_pull; otherwise uses
//     the installed-only variant.
//
// Builds the keyword index at registration time.
func RegisterKBTools(s *Server, k *kb.KB, deps Deps) {
	if s.PolicyKB() != "" {
		k.AuthName = s.PolicyKB()
	}
	s.kbRef = k
	s.kbArtifacts = artifactSource{allowlist: deps.MCPAllowlist, signer: deps.ArtifactSigner}
	installPolicy(s, k)
	register := func(t Tool) {
		t.ResourceClass = resourceClassForTool(t.Name)
		if t.ResourceClass == "" {
			panic("mcpserver: tool without resource class: " + t.Name)
		}
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

	// The search indexes follow the files by validation (D245): the first
	// reconciliation fills the live index (and brings SQLite in line), and
	// every search, pull and reindex after it applies only the delta.
	rec := newSearchReconciler(k, deps.SQLIndex)
	misses := newSearchMissLog(k)
	if _, err := rec.reconcile(); err != nil {
		fmt.Fprintf(os.Stderr, "cartographer: build search index: %v\n", err)
	}
	k.OnSyncIn = func() {
		stats, err := rec.reconcile()
		if err != nil {
			fmt.Fprintf(os.Stderr, "cartographer: reconcile after git sync: %v\n", err)
			return
		}
		if stats.Indexed > 0 || stats.Updated > 0 || stats.Removed > 0 {
			fmt.Fprintf(os.Stderr, "cartographer: reconciled index after git sync: indexed=%d updated=%d removed=%d\n", stats.Indexed, stats.Updated, stats.Removed)
		}
	}

	register(toolAtlasOverview(k))
	register(toolIndexGet(k))
	register(toolConceptRead(k))
	register(toolLogTail(k))
	register(toolChangesSince(k))
	register(gitWrap(k, toolConceptWrite(k)))
	register(gitWrap(k, toolConceptPatch(k)))
	register(gitWrap(k, toolIndexPatch(k)))
	register(gitWrap(k, toolMapCreate(k)))
	register(gitWrap(k, toolMapUpdate(k)))
	register(gitWrap(k, toolMapDelete(k)))
	register(gitWrap(k, toolConceptExpand(k)))
	register(gitWrap(k, toolLogAppend(k)))
	register(gitWrap(k, toolSnapshot(k)))
	register(toolValidate(k))
	register(toolMapList(k))
	register(toolConceptList(k))
	register(toolGraphNeighbors(k))
	register(toolGraphContext(k, rec, deps))
	register(toolLinkSuggest(k))
	register(toolGraphPath(k))
	register(toolSearch(k, rec, misses, deps))
	register(toolReindex(k, rec, deps))
	register(toolLint(k))
	register(toolCommitGate(k))
	register(toolGateCheck(k))
	register(gitWrap(k, toolSupersede(k)))
	register(gitWrap(k, toolConceptMove(k)))
	register(gitWrap(k, toolConceptBatch(k)))
	register(gitWrap(k, toolConceptDelete(k)))
	register(gitWrap(k, toolConflictResolve(k)))
	register(toolKBStatus(k, misses, s.version, s.knownLatestVersion))
	register(toolContradictionReport(k))
	register(toolConflictsList(k))
	register(toolSyncStatus(k))
	register(toolGitConflictResolve(k))
	register(toolPRStatus(k))
	register(toolPRFinalize(k))
	register(toolServiceGet(k))
	register(toolServiceList(k))
	// Secret resolution requires rw scope but is still a read-side operation;
	// wrap explicitly so a synchronised KB is fresh before decryption.
	register(readSyncWrap(k, toolSecretResolve(k)))
	register(gitWrap(k, toolSecretSet(k)))
	register(toolArtifactRead(k, deps.MCPAllowlist))
	register(toolArtifactList(k, deps.MCPAllowlist))
	register(toolTemplateList(k))
	register(gitWrap(k, toolConceptNew(k)))
	register(gitWrap(k, toolConceptMerge(k)))
	register(gitWrap(k, toolConceptCollapse(k)))
	register(toolAssetRead(k))
	register(toolAssetList(k))
	register(gitWrap(k, toolAssetWrite(k)))
	register(gitWrap(k, toolAssetDelete(k)))
	if k.AllowArtifactWrite {
		register(gitWrap(k, toolArtifactWrite(k)))
		register(gitWrap(k, toolArtifactDelete(k)))
	}

	if deps.BundleFS != nil {
		register(toolSkillListWithBundle(k, deps.BundleFS))
		register(gitWrap(k, toolSkillInstall(k, deps.BundleFS)))
		toolPrefix := s.ToolNamePrefix()
		register(toolSyncCheck(k, deps.BundleFS, toolPrefix, deps.RoutedMount, deps.ArtifactSigner, deps.MCPAllowlist))
		register(toolSyncApply(k, deps.BundleFS, toolPrefix, deps.RoutedMount, deps.ArtifactSigner, deps.MCPAllowlist))
		register(toolSyncPull(k, deps.BundleFS, toolPrefix, deps.RoutedMount, deps.ArtifactSigner, deps.MCPAllowlist))
	} else {
		register(toolSkillList(k))
	}
}
