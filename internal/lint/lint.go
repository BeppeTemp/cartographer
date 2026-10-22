// Package lint implements deterministic lint checks on a KB scope.
package lint

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// machinePathRe matches a literal home-anchored path — the kind of thing a
// concept body should express as {{repo:<key>}}/{{path:<nome>}} instead
// (D75), since it's only valid on the machine that wrote it. Deliberately
// narrow to the four home-anchored forms in the spec (macOS/Linux user
// homes, tilde shorthand, Windows Users dir): container/cluster absolute
// paths (/etc/..., /var/...) are legitimate and identical across machines,
// so they are intentionally not flagged.
//
// A candidate match here is not automatically a finding (D124): it can still
// be inside a URL (urlRe below) or under a built-in tool path (D313), both of
// which make it an operational/target path rather than a client-local one. No
// map adds its own prefixes (D907).
var machinePathRe = regexp.MustCompile(`(?:/Users/|/home/|~/|C:\\Users\\)[^\s` + "`" + `'"()]*`)

// urlRe matches a URL with an explicit scheme (e.g. "https://", "s3://"). A
// machine_path candidate found inside one of these spans is a URL host/path
// component, not a filesystem path on the machine that wrote it (D124).
var urlRe = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s` + "`" + `'"()]*`)

// categoryRank is the position of a check's category in Categories, the order
// findings are listed in. A name the registry does not know comes last.
func categoryRank(check string) int {
	s, ok := Spec(check)
	if !ok {
		return len(Categories)
	}
	for i, c := range Categories {
		if c == s.Category {
			return i
		}
	}
	return len(Categories)
}

// Order returns the findings sorted into the order every reader lists them in
// (D324): the registry's category order, then check, then path, then message.
// lint.Run walks a map, so without this two calls on the same KB disagree and
// offset pagination is meaningless. The sort lives here, not in Run, so the Run
// cache (D294) stays untouched. The order is where a reader starts, never a
// judgement of gravity: every finding is a problem (D947).
func Order(findings []Finding) []Finding {
	out := append([]Finding(nil), findings...)
	rank := map[string]int{}
	for _, f := range out {
		if _, ok := rank[f.Check]; !ok {
			rank[f.Check] = categoryRank(f.Check)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ra, rb := rank[a.Check], rank[b.Check]; ra != rb {
			return ra < rb
		}
		if a.Check != b.Check {
			return a.Check < b.Check
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Message < b.Message
	})
	return out
}

// CountsByCheck counts the findings per check. Callers compute it on the full
// set before narrowing a view, so a response never hides the shape of what it is
// not showing.
func CountsByCheck(findings []Finding) map[string]int {
	out := map[string]int{}
	for _, f := range findings {
		out[f.Check]++
	}
	return out
}

// Thresholds for the D77 WP4 structural guardrails. Deterministic by design
// (lint never calls an LLM): they defend the hierarchy's semantics — an
// expanded concept is one concept grown into a directory, not a taxonomy
// bucket; categories belong to curated indexes and search, not to the
// filesystem.
const (
	// expandedAsCategoryMinChildren is the child count above which an
	// expanded concept whose children are mostly unlinked from its index
	// is flagged as a category in disguise.
	expandedAsCategoryMinChildren = 8
	// mapOversizeThreshold is the concept count above which a map should
	// probably be split thematically (into a new map, not a subfolder).
	mapOversizeThreshold = 50
	// conceptOversizeThreshold is the body size (bytes) above which a concept is
	// flagged for splitting. Derived from the read guard rather than an
	// independent number (D159): lint advises a split at half the size at which
	// a plain concept_read degrades to an outline, so an author gets warning
	// before reads change shape. The two used to be unrelated constants, and the
	// claim that this one "mirrors" the guard was simply false — 30000 does not
	// mirror 60000.
	conceptOversizeThreshold = okf.ConceptReadSizeGuard / 2
)

// Finding represents a single lint finding.
type Finding struct {
	Path    string // concept path relative to KB (e.g. "arch/runbook.md")
	Check   string // check name (e.g. "broken_link", "stale_claim")
	Message string
	Fix     *Fix // optional mechanical remedy (D289)
	// Proposal is the structured vocabulary a missing_value_contract
	// finding suggests (D296).
	Proposal *Proposal
	// Count is a check-specific tally (open_marker: markers found, D297).
	Count int
	// members are an island's concepts, for its acceptance (D306).
	members []okf.ConceptID
	// Artifact marks a finding whose Path is a KB-root artifact file
	// (skills/…, agents/…, instructions.md), never a concept, even when it
	// ends in .md (D316): a caller mapping a path back to a concept must not
	// invent one from it.
	Artifact bool
}

// Now is used for date comparison in stale_claim checks. Override in tests.
var Now = func() time.Time { return time.Now() }

// pastDay reports whether day, a YYYY-MM-DD frontmatter date parsed to midnight
// UTC, is before the current UTC day. A date is judged by day, never by the
// instant: comparing it with Now() directly makes today count as past from
// 00:00 UTC. stale_claim and reviewSuspended both go through here, so they
// agree on the review day.
func pastDay(day time.Time) bool {
	return day.Before(Now().UTC().Truncate(24 * time.Hour))
}

// Run executes all deterministic lint checks on the given scope.
// If scope is empty, lints the entire KB.
// When scopeNeighbors is true, also lint the graph neighbors of concepts in scope.
//
// The sibling KB roots for cross_kb_path come from k.SiblingRoots (D316), so
// every caller — the lint tool, the cached kb_status/Atlas findings, the gate
// — reports the same findings for the same KB.
func Run(k *kb.KB, scope string, scopeNeighbors bool) ([]Finding, error) {
	return RunWithOptions(k, scope, scopeNeighbors, runOptions(k))
}

// runOptions is the injected, non-KB input of a lint run, read the same way for
// every caller.
func runOptions(k *kb.KB) Options {
	return Options{CrossKBRoots: k.SiblingRoots}
}

// ArtifactFindings runs the artifact checks of an unscoped Run (D316: skills,
// agents, instructions.md) and nothing else: it neither walks the concepts nor
// builds the graph, so its cost follows the artifacts and not the size of the KB.
// A caller that holds a lock while it lints uses it instead of Run. The
// findings are the ones Run reports for the same artifacts, plus junk_file for
// the files given (KB-root-relative), which Run finds by listing the whole git
// tree. missing_instructions is never reported: it needs the concept count.
func ArtifactFindings(k *kb.KB, files []string) []Finding {
	findings := checkArtifacts(k, runOptions(k), readInstructions(k), 0, secretsDirExists(k))
	return append(findings, junkFindings(files, nil)...)
}

func secretsDirExists(k *kb.KB) bool {
	fi, err := os.Stat(filepath.Join(k.Root, "secrets"))
	return err == nil && fi.IsDir()
}

// RunWithOptions is Run with the injected, non-KB inputs explicit.
func RunWithOptions(k *kb.KB, scope string, scopeNeighbors bool, opts Options) ([]Finding, error) {
	return runChecks(k, scope, scopeNeighbors, opts)
}

// owningMap is the map a finding path or concept ID belongs to: its first path
// segment, "" for a root file.
func owningMap(path string) string {
	if m, _, ok := strings.Cut(path, "/"); ok {
		return m
	}
	return ""
}

// runChecks is the whole of Run: no setting, no frontmatter field and no map
// key silences a finding (D907).
func runChecks(k *kb.KB, scope string, scopeNeighbors bool, opts Options) ([]Finding, error) {
	// Collect all non-reserved concepts and their physical paths.
	// relPathOf mirrors resolveConceptRelPath on the read path: for a plain
	// concept it holds "<id>.md", for an expanded one "<id>/index.md".  When
	// both forms exist the walk emits two files with the same id; the direct
	// form comes first in walk order and wins (matching resolveConceptRelPath's
	// "direct wins" rule on the read path).
	allConcepts := map[okf.ConceptID]string{} // id → full content
	relPathOf := map[okf.ConceptID]string{}   // id → physical rel path
	if err := k.WalkConceptPaths(func(id okf.ConceptID, physicalPath, content string) error {
		allConcepts[id] = content
		if _, exists := relPathOf[id]; !exists {
			relPathOf[id] = physicalPath // direct form wins
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("lint.Run: walk: %w", err)
	}
	// exists answers "is target a concept" from the walk, falling back to the
	// filesystem only on a miss: a link may name an expanded concept by its
	// index file ("<id>/index") or a map's own index.md, neither of which is a
	// walked ID, and ReadConcept resolves both (D294 regression, D295).
	exists := func(target okf.ConceptID) bool {
		if _, ok := relPathOf[target]; ok {
			return true
		}
		_, err := k.ReadConcept(target)
		return err == nil || !errors.Is(err, okf.ErrNotFound)
	}

	// Normalise scope to forward-slash form (OKF paths use /). Empty means
	// "no scope restriction" for both the concept and directory-based checks.
	scopeNorm := ""
	if scope != "" {
		scopeNorm = strings.ReplaceAll(scope, "\\", "/")
		scopeNorm = strings.TrimSuffix(scopeNorm, "/")
	}

	// Determine which concepts fall within the requested scope.
	toCheck := map[okf.ConceptID]string{}
	if scopeNorm == "" {
		for id, c := range allConcepts {
			toCheck[id] = c
		}
	} else {
		for id, c := range allConcepts {
			idStr := string(id)
			// Match either an exact concept or any concept under a directory.
			if idStr == scopeNorm || strings.HasPrefix(idStr, scopeNorm+"/") {
				toCheck[id] = c
			}
		}
	}

	// One graph read serves the scope expansion, the orphan check (D241) and
	// the structural checks (D243): a GraphNeighbors call per concept
	// validated the whole KB each time.
	lg, err := k.LinkGraph(nil)
	if err != nil {
		return nil, fmt.Errorf("lint.Run: link graph: %w", err)
	}
	graph := lg.Links
	isRetired := retiredOf(lg)

	// Expand scope with 1-hop graph neighbours when requested: out-links
	// only, the concept itself excluded — GraphNeighbors(id, 1)'s semantics.
	if scopeNeighbors && len(toCheck) > 0 {
		extra := map[okf.ConceptID]bool{}
		for id := range toCheck {
			for cid := range graph.Out[id] {
				if cid == id {
					continue
				}
				if _, already := toCheck[cid]; !already {
					extra[cid] = true
				}
			}
		}
		for id := range extra {
			if c, ok := allConcepts[id]; ok {
				toCheck[id] = c
			}
		}
	}

	// The reverse-link graph for the orphan check. It retains self-links,
	// preserving the pre-D108 orphan behaviour.
	incomingLinks := graph.In
	// Source citations (D278), over the whole KB whatever the scope.
	sourceCited := kb.SourceCitations(allConcepts)

	// Archive name set for orphan skip rule.
	archives, err := k.ListArchives()
	if err != nil {
		return nil, fmt.Errorf("lint.Run: list archives: %w", err)
	}
	archiveSet := make(map[string]bool, len(archives))
	for _, a := range archives {
		archiveSet[a] = true
	}

	// D356: the type each map's pages agree on, and whether a slug target is
	// free, for the invalid-page checks' fixes.
	types := newMapTypes(allConcepts)
	slugTargetFree := func(target okf.ConceptID) bool {
		if _, ok := allConcepts[target]; ok {
			return false
		}
		return !exists(target)
	}

	var findings []Finding
	// Contracts are descriptor-level data. Cache them once per map for this
	// run, so a map with many concepts does not repeatedly parse _map.md.
	contracts := make(map[string]kb.MapContract, len(archives))
	var catalog kb.TemplateCatalog // the KB's templates, read once per run and only if a map asks (D352)
	var catalogOnce sync.Once
	for _, archive := range archives {
		contract, contractErr := k.ReadMapContract(archive)
		if contractErr != nil {
			continue // A non-map directory has no descriptor/contract.
		}
		contracts[archive] = contract
		if scopeMatchesDir(scopeNorm, archive) {
			for _, malformed := range contract.Malformed {
				findings = append(findings, newFinding("contract_malformed", Finding{
					Path:    malformed.Descriptor,
					Message: fmt.Sprintf("malformed lint contract key %q", malformed.Key),
				}))
			}
		}
	}

	// D357: the type palette and the status spellings, KB-wide.
	drift := newDriftData(k, allConcepts, contracts)

	// The KB's path placeholder registry (D263). Its own findings — a
	// malformed entry, a declared key nothing cites — belong to paths.yaml,
	// a KB-level file, so only an unscoped lint reports them.
	registry, registryFindings := loadRegistryLint(k)
	if scopeNorm == "" {
		findings = append(findings, registryFindings...)
		findings = append(findings, settingsFindings(k)...)
		cited := map[string]bool{}
		for _, f := range lg.Facets {
			for _, id := range f.Placeholders {
				cited[id] = true
			}
		}
		artifactIDs, artErr := k.ArtifactPlaceholders()
		if artErr != nil {
			return nil, fmt.Errorf("lint.Run: artifact placeholders: %w", artErr)
		}
		for _, id := range artifactIDs {
			cited[id] = true
		}
		// A KB that cites placeholders but never wrote paths.yaml gets one
		// nudge (D314): the D263 checks are opt-in by presence of the file, so
		// without it nothing says the vocabulary is undeclared. A file that
		// exists but is unreadable is contract_malformed, not this.
		if !registry.state.Present && len(registryFindings) == 0 && len(cited) > 0 {
			findings = append(findings, newFinding("missing_registry", Finding{
				Path: kb.PathRegistryFile,
				Message: fmt.Sprintf("%d placeholder(s) cited but %s does not exist — create it to get unknown_placeholder and unused_placeholder checks (D263)",
					len(cited), kb.PathRegistryFile),
			}))
		}
		for _, id := range registry.unused(cited) {
			findings = append(findings, newFinding("unused_placeholder", Finding{
				Path:    kb.PathRegistryFile,
				Message: fmt.Sprintf("{{%s}} is declared but no concept or artifact cites it — drop it, or merge it into the key that is used", id),
			}))
		}
	}

	// The KB's glossary (D276): its malformed entries belong to glossary.yaml,
	// so only an unscoped lint reports them; forbidden_term is per concept.
	glossary, glossaryFindings := loadGlossaryLint(k)
	if scopeNorm == "" {
		findings = append(findings, glossaryFindings...)
	}

	// Hooks are KB-level artifacts, not concepts: only an unscoped lint
	// reports a broken hook.json (D284).
	if scopeNorm == "" {
		findings = append(findings, checkHooks(k)...)
	}

	// The other KB-root artifacts (D316): skills, agents, instructions.md.
	// KB-level like the hooks, so only an unscoped lint reports them; the
	// instructions.md frontmatter (perimeter, legacy_paths) is read once here
	// and also drives the per-concept legacy_path check below.
	instr := readInstructions(k)
	hasSecretsDir := secretsDirExists(k)
	if scopeNorm == "" {
		findings = append(findings, checkArtifacts(k, opts, instr, len(allConcepts), hasSecretsDir)...)
		// Not in ArtifactFindings: it reads the usage store and git history,
		// and its callers hold a lock (D1043).
		skills, _ := loadSkills(k)
		findings = append(findings, checkArtifactsUnused(k, skills)...)
	}
	// junkAssets are the junk files the expanded-concept pass reported as
	// junk_asset, so the whole-tree junk_file pass does not report them twice.
	junkAssets := map[string]bool{}

	// The graph-shape observations (cut_concept, island, map_misfit) are not
	// findings (D947): only kb_review computes them, so the cheap structure
	// (collection kinds and the successor resolver) is all a lint needs.
	st := newStructure(k, lg, archives, contracts)

	// readTargetBody serves reciprocal_link_item the body of a link target
	// (D309): the target's text, not its links section, must link back. The
	// split is cached across the run — a hub is the target of many pages.
	targetBodies := map[okf.ConceptID]string{}
	var targetMu sync.Mutex
	readTargetBody := func(target okf.ConceptID) (string, string) {
		targetMu.Lock()
		defer targetMu.Unlock()
		b, ok := targetBodies[target]
		if !ok {
			_, b, _ = okf.SplitFrontmatter(allConcepts[target])
			targetBodies[target] = b
		}
		return b, relPathOf[target]
	}

	// Each concept's checks read the run's shared data and write only their
	// own findings, so they run on every core: on a KB of a thousand pages
	// this is most of a whole-KB lint (the Atlas waits on it).
	checkOne := func(id okf.ConceptID, content string) []Finding {
		var findings []Finding
		relPath := okf.IDToPath(id)
		// Relative links resolve against the file that contains them, which for
		// an expanded concept is "<id>/index.md", not "<id>.md" (D149). relPath
		// stays ID-derived: it is what Finding.Path reports and what callers
		// sort on. The two coincide for a plain concept.
		linkBase := relPathOf[id]
		fmRaw, body, hasFM := okf.SplitFrontmatter(content)
		// Nothing silences a finding (D907): a page that documents a false
		// positive rewrites the text that trips the check.
		emit := func(f Finding) { findings = append(findings, f) }
		parts := strings.Split(string(id), "/")

		linkFindings(k, id, relPath, linkBase, body, exists, graph.Out, readTargetBody, emit)

		// --- unknown_placeholder (D263) ---
		// The keys come from the graph cache's facet (D262), the same parse
		// sync_pull lists them from — not a second regex here.
		if i, ok := lg.Index[id]; ok {
			if undeclared := registry.undeclared(lg.Facets[i].Placeholders); len(undeclared) > 0 {
				emit(unknownPlaceholderFinding(relPath, undeclared))
			}
		}

		// --- forbidden_term (D276) ---
		for _, f := range forbiddenTermFindings(relPath, body, glossary) {
			emit(f)
		}

		// --- source_uncited (D278) ---
		if f, ok := uncitedSourceFinding(relPath, id, content, sourceCited); ok {
			emit(f)
		}

		// --- the page-level checks: one evaluator shared with CheckConcept (D354) ---
		// services/ concepts are rooted outside data/ and therefore have no map
		// descriptor to contract against.
		var parsed *okf.Frontmatter
		if hasFM {
			parsed, _ = okf.ParseFrontmatter(fmRaw)
		}
		in := conceptInput{RelPath: relPath, Body: body, FrontmatterRaw: fmRaw, Parsed: parsed, Registry: registry}
		if len(parts) > 1 && archiveSet[parts[0]] {
			c := contracts[parts[0]]
			in.MapName, in.Contract = parts[0], &c
			if c.HasTemplateKeys() {
				catalogOnce.Do(func() { catalog = k.TemplateCatalog() })
				in.Catalog = catalog
			}
		}
		for _, f := range conceptFindings(conceptCtx{
			ID: id, In: in,
			KBRoot: k.Root, HasSecretsDir: hasSecretsDir, LegacyPaths: instr.legacyPaths,
		}) {
			emit(f)
		}
		for _, f := range validityFindings(id, content, types, slugTargetFree) {
			emit(f)
		}
		for _, f := range drift.conceptFindings(relPath, parsed, in.Contract) {
			emit(f)
		}

		// --- orphan ---
		if f, ok := orphanFinding(id, relPath, isRetired(id), incomingLinks, graph.Out, archiveSet, func(t okf.ConceptID) bool { _, ok := relPathOf[t]; return ok }, func(t okf.ConceptID) bool { return len(sourceCited[string(t)]) > 0 }); ok {
			emit(f)
		}

		// --- broken_relation, link_to_retired ---
		for _, f := range st.conceptChecks(id, relPath) {
			emit(f)
		}
		return findings
	}
	ids := make([]okf.ConceptID, 0, len(toCheck))
	for id := range toCheck {
		ids = append(ids, id)
	}
	perConcept := make([][]Finding, len(ids))
	var next atomic.Int64
	var wg sync.WaitGroup
	// A panic in a check is re-raised on the caller's goroutine, where the
	// HTTP server's per-request recover catches it, as before the workers:
	// in a worker it would take the whole process down.
	var panicked atomic.Value
	for w := 0; w < min(runtime.GOMAXPROCS(0), len(ids)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panicked.CompareAndSwap(nil, fmt.Sprint(r))
				}
			}()
			for i := int(next.Add(1)) - 1; i < len(ids); i = int(next.Add(1)) - 1 {
				perConcept[i] = checkOne(ids[i], toCheck[ids[i]])
			}
		}()
	}
	wg.Wait()
	if r := panicked.Load(); r != nil {
		panic(fmt.Sprintf("lint: concept check: %v", r))
	}
	for _, f := range perConcept {
		findings = append(findings, f...)
	}

	// --- structural checks on maps and expanded concepts (D77 WP4) ---
	// Directory-based (unlike the checks above, not driven by toCheck).
	for _, archiveName := range archives {
		if scopeMatchesDir(scopeNorm, archiveName) {
			// --- legacy_archive_descriptor ---
			// A map still described by the pre-D77 _archive.md shape. Read-compat
			// keeps it working; the finding is the migration backlog.
			if _, mapErr := k.ReadRaw(archiveName + "/_map.md"); errors.Is(mapErr, okf.ErrNotFound) {
				if _, legacyErr := k.ReadRaw(archiveName + "/_archive.md"); legacyErr == nil {
					findings = append(findings, newFinding("legacy_archive_descriptor", Finding{
						Path:    archiveName + "/_archive.md",
						Message: "legacy _archive.md descriptor — rewrite as _map.md with a kind (D77)",
					}))
				}
			}

			// --- contract_legacy_key (D907) ---
			// A key the contract no longer has (it switched a check off or
			// loosened it): the repair rewrites the descriptor.
			if c, ok := contracts[archiveName]; ok && len(c.LegacyKeys) > 0 {
				findings = append(findings, newFinding("contract_legacy_key", Finding{
					Path:    archiveName + "/_map.md",
					Message: fmt.Sprintf("removed contract key(s) %s (D907): no key switches a check off or loosens it — the repair rewrites the descriptor to the current shape", strings.Join(c.LegacyKeys, ", ")),
					Fix:     &Fix{Kind: FixRewriteContract, Field: strings.Join(c.LegacyKeys, ",")},
				}))
			}

			// --- templates against the map's values (D352) ---
			if c, ok := contracts[archiveName]; ok {
				if len(c.Templates) > 0 {
					catalogOnce.Do(func() { catalog = k.TemplateCatalog() })
				}
				findings = append(findings, mapTemplateFindings(archiveName+"/_map.md", archiveName, c, catalog)...)
			}
		}

		// --- index_incomplete / curated-index broken_link (D107) ---
		// Only candidates in toCheck participate, which keeps a scoped lint
		// actionable instead of reporting unrelated map siblings.
		contract, hasContract := contracts[archiveName]
		if hasContract && contract.Index == kb.IndexGenerated {
			// --- index_stale (D301) ---
			// The server owns the concept list of a generated index, so
			// completeness is not the agent's to check: only whether the
			// block still matches what the next write would put there.
			if scopeNorm == "" || scopeNorm == archiveName {
				if content, err := k.ReadIndex(archiveName); err == nil {
					want, werr := k.ExpectedIndexBlock(archiveName, contract)
					if got, _ := kb.IndexBlock(content); werr == nil && got != want {
						findings = append(findings, newFinding("index_stale", Finding{
							Path:    archiveName + "/index.md",
							Message: "generated index block differs from the map's concepts (edited out of band or written by an older server) — the next write regenerates it",
						}))
					}
					_, body, _ := okf.SplitFrontmatter(content)
					checkIndexLinks(k, archiveName+"/index.md", body, &findings, exists)
				}
			}
		} else if hasContract {
			var direct []okf.ConceptID
			for id := range toCheck {
				parts := strings.Split(string(id), "/")
				if len(parts) == 2 && parts[0] == archiveName {
					direct = append(direct, id)
				}
			}
			// A full lint or an explicit map scope also validates the map index
			// when it has no direct concepts. A scope inside an expanded concept
			// must not surface unrelated map-index content, and a concept scope
			// gets only its own entries: the index's dead links are the map's.
			wholeMap := scopeNorm == "" || scopeNorm == archiveName
			if len(direct) > 0 || wholeMap {
				checkCuratedIndex(k, archiveName, archiveName+"/index.md", direct, wholeMap, &findings, exists, isRetired)
			}
		} else if scopeNorm == "" || scopeNorm == archiveName {
			// A folder with no readable descriptor has no contract to say
			// what its index is, so only the links are checked: a dead link
			// is dead either way, and a concept_move's stale [[id]] would
			// otherwise go unreported (#320). Completeness is checked on
			// every map (the branch above); the folder itself is
			// unmapped_folder's.
			if content, err := k.ReadIndex(archiveName); err == nil {
				_, body, _ := okf.SplitFrontmatter(content)
				checkIndexLinks(k, archiveName+"/index.md", body, &findings, exists)
			}
		}

		if scopeNorm == "" || scopeNorm == archiveName {
			findings = append(findings, indexListsRetired(k, archiveName, contracts[archiveName], allConcepts, st.lg)...)
		}

		expandedDirs, err := k.ListExpanded(archiveName)
		if err != nil {
			continue
		}
		for _, d := range expandedDirs {
			expandedID := okf.ConceptID(archiveName + "/" + d)
			if !scopeMatchesDir(scopeNorm, string(expandedID)) {
				continue
			}
			if _, ok := contracts[archiveName]; ok {
				var satellites []okf.ConceptID
				for id := range toCheck {
					if strings.HasPrefix(string(id), string(expandedID)+"/") {
						satellites = append(satellites, id)
					}
				}
				if len(satellites) > 0 {
					// Expanded indexes are emitted by WalkConcepts, so their link
					// targets are already covered by the general broken_link pass.
					checkCuratedIndex(k, string(expandedID), string(expandedID)+"/index.md", satellites, false, &findings, exists, isRetired)
				}
			}

			// --- expanded_missing_index ---
			// WriteConcept (D72 WP4) stubs index.md on every implicitly created
			// directory and ExpandConcept always produces one, so this only
			// fires for directories predating those fixes.
			_, indexErr := k.ReadIndex(string(expandedID))
			if indexErr != nil && errors.Is(indexErr, okf.ErrNotFound) {
				findings = append(findings, newFinding("expanded_missing_index", Finding{
					Path:    string(expandedID) + "/index.md",
					Message: "expanded concept missing index.md",
				}))
			}

			// --- expanded_ambiguous ---
			// Both "<id>.md" and "<id>/index.md" exist: reads silently prefer
			// the direct form and writes are rejected (resolveConceptRelPath),
			// so this is the place that surfaces the conflict.
			if indexErr == nil {
				if _, directErr := k.ReadRaw(string(expandedID) + ".md"); directErr == nil {
					findings = append(findings, newFinding("expanded_ambiguous", Finding{
						Path:    string(expandedID) + ".md",
						Message: fmt.Sprintf("both %s.md and %s/index.md exist — writes to %s are blocked until one form is removed", expandedID, expandedID, expandedID),
					}))
				}
			}

			// --- expanded_as_category ---
			// Many children, mostly not linked from (or to) the concept's own
			// index: the directory is being used as a taxonomy bucket.
			var children []okf.ConceptID
			for id := range allConcepts {
				if strings.HasPrefix(string(id), string(expandedID)+"/") {
					children = append(children, id)
				}
			}
			if len(children) > expandedAsCategoryMinChildren {
				indexTargets := map[okf.ConceptID]bool{}
				if indexContent, ok := allConcepts[expandedID]; ok {
					_, indexBody, _ := okf.SplitFrontmatter(indexContent)
					for _, tgt := range kb.ExtractLinks(indexBody, string(expandedID)+"/index.md", k.AssetExists) {
						indexTargets[tgt] = true
					}
				}
				linked := 0
				for _, child := range children {
					if indexTargets[child] {
						linked++
						continue
					}
					_, childBody, _ := okf.SplitFrontmatter(allConcepts[child])
					for _, tgt := range kb.ExtractLinks(childBody, okf.IDToPath(child)) {
						if tgt == expandedID {
							linked++
							break
						}
					}
				}
				if linked*2 < len(children) {
					findings = append(findings, newFinding("expanded_as_category", Finding{
						Path:    string(expandedID),
						Message: fmt.Sprintf("%d children, only %d linked to the concept's index — directory used as a category; categories belong to curated indexes, not the filesystem (D77)", len(children), linked),
					}))
				}
			}

			// --- orphan_asset ---
			// Assets are intentionally outside WalkConcepts and the graph. They
			// still need a lightweight provenance signal: an asset should be
			// cited by its owner's index or one of that expanded concept's
			// satellite concepts.
			if indexErr == nil {
				// An ambiguous direct+expanded pair is a pre-existing lint
				// condition; assets cannot be resolved safely until the owner
				// form is disambiguated, so leave it to expanded_ambiguous.
				if _, directErr := k.ReadRaw(string(expandedID) + ".md"); directErr == nil {
					continue
				}
				referenced := map[string]bool{}
				for id, content := range allConcepts {
					idStr := string(id)
					if idStr != string(expandedID) && !strings.HasPrefix(idStr, string(expandedID)+"/") {
						continue
					}
					_, body, _ := okf.SplitFrontmatter(content)
					basePath := okf.IDToPath(id)
					if id == expandedID {
						basePath = string(expandedID) + "/index.md"
					}
					for _, target := range kb.ExtractAssetLinks(body, basePath, k.AssetExists) {
						referenced[target] = true
					}
				}
				// A directory lint cannot list (a symlink, a special file) is
				// one finding on its owner, never an aborted run (D270).
				assets, assetErr := k.ListAssets(expandedID)
				if assetErr != nil {
					findings = append(findings, newFinding("unlistable_assets", Finding{
						Path:    string(expandedID),
						Message: fmt.Sprintf("the concept's assets cannot be listed: %v", assetErr),
					}))
					continue
				}
				for _, asset := range assets {
					assetPath := string(expandedID) + "/" + asset.Path
					if asset.Oversized {
						findings = append(findings, newFinding("oversized_asset", Finding{
							Path:    assetPath,
							Message: fmt.Sprintf("asset is %d bytes, above the %d MiB worth versioning in git — move it outside the KB and cite it by link (D270)", asset.Size, kb.AssetMaxFileSize>>20),
						}))
					}
					// A junk file is not an asset to cite (D316): orphan_asset's
					// advice was once followed literally on a .pyc.
					if kb.IsJunkPath(asset.Path) {
						junkAssets[assetPath] = true
						findings = append(findings, newFinding("junk_asset", Finding{
							Path:    assetPath,
							Message: "junk file tracked as asset; delete it with asset_delete",
						}))
						continue
					}
					if referenced[assetPath] {
						continue
					}
					findings = append(findings, newFinding("orphan_asset", Finding{
						Path:    assetPath,
						Message: "asset is not cited by its owning dossier document; cite dossier artifacts from the document that owns them",
					}))
				}
			}
		}
	}

	if scopeNorm == "" {
		findings = append(findings, checkJunkFiles(k, junkAssets)...)
		findings = append(findings, checkSymlinkedPaths(k)...)
	}

	// --- unmapped_folder, stray_file (D357) ---
	findings = append(findings, unmappedFolderFindings(k, archives, allConcepts, func(name string) bool { return scopeMatchesDir(scopeNorm, name) })...)
	findings = append(findings, strayFileFindings(k, scopeNorm)...)

	return findings, nil
}

// linkFindings are the checks one concept's body links drive: broken_link,
// then the trailing links section's duplicate_link, reciprocal_link_item and
// bare_link_list. Shared by runChecks and ScopedCheck (D312), so a write
// response and a whole-KB lint report the same finding for the same body.
// out is the link graph's out-edges (reciprocal_link_item reads it).
func linkFindings(k *kb.KB, id okf.ConceptID, relPath, linkBase, body string, exists func(okf.ConceptID) bool, out map[okf.ConceptID]map[okf.ConceptID]struct{}, readTargetBody func(okf.ConceptID) (string, string), emit func(Finding)) {
	// --- broken_link ---
	// For expanded concepts, detect links broken by past expansions:
	// if the same href resolves from the pre-expansion base "<id>.md",
	// the fix is a rebase (D295 WP2).
	isExpanded := strings.HasSuffix(linkBase, "/index.md") && strings.Count(linkBase, "/") >= 2
	var rebasable map[string]*Fix // broken target path → fix
	if isExpanded {
		preExpBase := strings.TrimSuffix(linkBase, "/index.md") + ".md"
		rebasable = brokenLinkRebaseFixes(body, linkBase, preExpBase, id, exists)
	}
	for _, target := range kb.ExtractLinks(body, linkBase, k.AssetExists) {
		targetPath := okf.IDToPath(target)
		if !exists(target) {
			f := newFinding("broken_link", Finding{
				Path:    relPath,
				Message: fmt.Sprintf("broken link to %s", targetPath),
			})
			if fix, ok := rebasable[targetPath]; ok {
				f.Fix = fix
				f.Message += "; fix: rebase relative to the expanded index"
				if fix.To == "" {
					f.Message = fmt.Sprintf("link to the concept's own page (%s) — drop the link, keep the label", targetPath)
				}
			}
			emit(f)
		}
	}

	// --- index_link_form (D310) ---
	for _, f := range indexLinkForms(body, linkBase, exists) {
		f.Path = relPath
		emit(f)
	}

	// --- duplicate_link / bare_link_list: the trailing links
	// section (linksection.go) ---
	if heading, dups, fixableDups, bare, n := linksSectionIssues(body, linkBase, k.AssetExists); heading != "" {
		if len(dups) > 0 {
			for _, d := range dups {
				f := newFinding("duplicate_link", Finding{
					Path:    relPath,
					Message: fmt.Sprintf("linked both in the text and under %q: %s — keep the link where the text says why (a line of prose keeps the section when its items go)", heading, d),
				})
				if fix, ok := fixableDups[d]; ok {
					f.Fix = fix
				}
				emit(f)
			}
		}
		// --- reciprocal_link_item (D301): an opt-in efficiency
		// fix, never conformance debt; the target's own link keeps the
		// edge navigable both ways through backlinks. ---
		recips := reciprocalLinkItems(body, linkBase, id, out, dups, k.AssetExists, readTargetBody)
		recipIDs := make([]okf.ConceptID, 0, len(recips))
		for target := range recips {
			recipIDs = append(recipIDs, target)
		}
		sort.Slice(recipIDs, func(i, j int) bool { return recipIDs[i] < recipIDs[j] })
		for _, target := range recipIDs {
			emit(newFinding("reciprocal_link_item", Finding{
				Path:    relPath,
				Message: fmt.Sprintf("%s already links back here, so the backlink shows this edge — the item under %q is a second write to keep in sync", target, heading),
				Fix:     &Fix{Kind: FixDropLinkItem, Field: recips[target]},
			}))
		}
		if bare {
			emit(newFinding("bare_link_list", Finding{
				Path:    relPath,
				Message: fmt.Sprintf("%q lists %d link(s) with no word on why each matters — add a short reason per link", heading, n),
			}))
		}
	}
}

// orphanFinding is the orphan check for one concept (shared with ScopedCheck,
// D312). in and out are the link graph's edges; present says whether a link
// target is an existing concept; isRetired is the concept's status, already
// resolved by the caller.
func orphanFinding(id okf.ConceptID, relPath string, isRetired bool, in, out map[okf.ConceptID]map[okf.ConceptID]struct{}, archiveSet map[string]bool, present func(okf.ConceptID) bool, cited func(okf.ConceptID) bool) (Finding, bool) {
	// A retired concept is not asked to be linked: link_to_retired and
	// index_lists_retired push links away from it, so an orphan finding would
	// contradict them (D826).
	if isRetired {
		return Finding{}, false
	}
	// A concept another cites in its frontmatter provenance (a Source in the
	// sources journal) is reached, though no body link says so.
	if len(in[id]) != 0 || (cited != nil && cited(id)) {
		return Finding{}, false
	}
	parts := strings.Split(string(id), "/")
	// A concept at depth=1 inside a known archive is an entry point,
	// reached from the map's index — unless it links to nothing
	// either: the index is not an edge of the graph, so that concept
	// is a node connected to nothing, which nothing else reports.
	atArchiveTop := len(parts) == 2 && archiveSet[parts[0]]
	outgoing := 0
	for t := range out[id] {
		if present(t) && t != id {
			outgoing++
		}
	}
	switch {
	case !atArchiveTop:
		return newFinding("orphan", Finding{
			Path:    relPath,
			Message: "no incoming links",
		}), true
	case outgoing == 0:
		return newFinding("orphan", Finding{
			Path:    relPath,
			Message: "no links in or out: a node connected to nothing in the graph (the map's index is not a link) — link it to the concepts it relates to (link_suggest may propose some by title similarity)",
		}), true
	}
	return Finding{}, false
}

// firstDisallowedMachinePath scans body for machine_path candidates in order
// and returns the first one that is neither inside a URL nor under a
// conventional tool path (builtinAllowPrefixes). Scanning continues past an
// allowed match so a later disallowed match still surfaces. Returns "" when
// every candidate is allowed or there is none.
func firstDisallowedMachinePath(body string) string {
	if all := disallowedMachinePaths(body); len(all) > 0 {
		return all[0]
	}
	return ""
}

// disallowedMachinePaths is every distinct disallowed candidate, in order.
func disallowedMachinePaths(body string) []string {
	candidates := machinePathRe.FindAllStringIndex(body, -1)
	if len(candidates) == 0 {
		return nil
	}
	urlSpans := urlRe.FindAllStringIndex(body, -1)
	var out []string
	seen := map[string]bool{}
	for _, span := range candidates {
		if withinAnySpan(span, urlSpans) {
			continue
		}
		// Prose punctuation after a path is not part of it.
		candidate := strings.TrimRight(body[span[0]:span[1]], ",;:.)")
		if candidate == "" || matchesAllowedPrefix(candidate) || seen[candidate] {
			continue
		}
		seen[candidate] = true
		out = append(out, candidate)
	}
	return out
}

// withinAnySpan reports whether span (a [start, end) match position) is
// fully contained by one of the given spans.
func withinAnySpan(span []int, spans [][]int) bool {
	for _, s := range spans {
		if span[0] >= s[0] && span[1] <= s[1] {
			return true
		}
	}
	return false
}

// matchesAllowedPrefix reports whether candidate is under one of the
// conventional tool paths (D313), using literal segment-boundary prefix
// matching: "~/.ssh/" covers "~/.ssh/config" but not "~/.sshx".
func matchesAllowedPrefix(candidate string) bool {
	for _, prefix := range builtinAllowPrefixes {
		if pathHasPrefix(candidate, prefix) {
			return true
		}
	}
	return false
}

// builtinAllowPrefixes are the conventional tool paths every reader's machine
// agrees on (D313): citing ~/.ssh/config or ~/.kube/config is documentation,
// not a client-local path. No map adds to them (D907): any other path is
// written as {{repo:<key>}}, {{path:<name>}} or $HOME.
var builtinAllowPrefixes = []string{"~/.ssh/", "~/.kube/", "~/.m2/", "~/.config/", "~/.cache/", "~/.local/", "~/.gnupg/"}

var (
	mdLinkPat   = regexp.MustCompile(`\[((?:[^\[\]]|\[[^\[\]]*\])*)\]\(([^)]+)\)`)
	wikiLinkPat = regexp.MustCompile(`\[\[([^\[\]|#]+)(#[^\[\]|]*)?(\|[^\[\]]*)?\]\]`)
)

// indexLinkForms finds the links that spell an expanded concept as
// <concept>/index (D310). The graph already counts them as links to the
// concept; the canonical spelling is the concept itself, and the fix is
// mechanical: a markdown href becomes the relative <concept>.md, a wiki-link
// its bare ID. A target whose parent is no concept is a broken link, not
// this check's business.
func indexLinkForms(body, linkBase string, exists func(okf.ConceptID) bool) []Finding {
	masked := kb.MaskCodeSpans(body)
	baseDir := path.Dir(linkBase)
	var out []Finding
	seen := map[string]bool{}
	for _, m := range mdLinkPat.FindAllStringSubmatch(masked, -1) {
		href := m[2]
		if strings.Contains(href, "://") || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "mailto:") || strings.HasPrefix(href, "/") {
			continue
		}
		pathPart := strings.SplitN(href, "#", 2)[0]
		if !strings.EqualFold(path.Ext(pathPart), ".md") {
			continue
		}
		resolved := path.Clean(path.Join(baseDir, pathPart))
		parent, ok := strings.CutSuffix(strings.TrimSuffix(resolved, path.Ext(resolved)), "/index")
		if !ok || strings.HasPrefix(resolved, "..") || !exists(okf.ConceptID(parent)) {
			continue
		}
		if seen[pathPart] {
			continue
		}
		seen[pathPart] = true
		to := kb.RelLink(baseDir, parent+".md")
		out = append(out, newFinding("index_link_form", Finding{
			Message: fmt.Sprintf("link to %s — the concept is %s; write %s", pathPart, parent, to),
			Fix:     &Fix{Kind: FixRebaseLink, Field: pathPart, To: to},
		}))
	}
	for _, m := range wikiLinkPat.FindAllStringSubmatch(masked, -1) {
		raw := m[1]
		parent, ok := strings.CutSuffix(strings.TrimSuffix(raw, ".md"), "/index")
		if !ok || strings.Contains(raw, "://") || !exists(okf.ConceptID(parent)) || seen["[["+raw] {
			continue
		}
		seen["[["+raw] = true
		out = append(out, newFinding("index_link_form", Finding{
			Message: fmt.Sprintf("link to [[%s]] — the concept is %s; write [[%s]]", raw, parent, parent),
			Fix:     &Fix{Kind: FixRewriteWikiLink, Field: raw, To: parent},
		}))
	}
	return out
}

// brokenLinkRebaseFixes computes, for an expanded concept, which broken
// markdown links would resolve from the pre-expansion base. Each is returned
// keyed by the broken target path (e.g. "map/c/other.md") with a Fix carrying
// the old href (Field) and the correct new href (To). D295 WP2. A link that
// would resolve to the concept itself (self, or its own index.md) carries an
// empty To: the repair keeps the label and drops the link (D310).
func brokenLinkRebaseFixes(body, newBase, oldBase string, self okf.ConceptID, exists func(okf.ConceptID) bool) map[string]*Fix {
	oldDir, newDir := path.Dir(oldBase), path.Dir(newBase)
	masked := kb.MaskCodeSpans(body)
	mdLinkPat := regexp.MustCompile(`\[((?:[^\[\]]|\[[^\[\]]*\])*)\]\(([^)]+)\)`)
	out := map[string]*Fix{}
	for _, m := range mdLinkPat.FindAllStringSubmatch(masked, -1) {
		href := m[2]
		if strings.Contains(href, "://") || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "mailto:") || strings.HasPrefix(href, "/") {
			continue
		}
		pathPart := strings.SplitN(href, "#", 2)[0]
		if pathPart == "" {
			continue
		}
		ext := path.Ext(pathPart)
		if ext != "" && !strings.EqualFold(ext, ".md") {
			continue
		}
		target := pathPart
		if !strings.EqualFold(path.Ext(target), ".md") {
			target += ".md"
		}

		// Resolve from the new base (where it's broken).
		brokenResolved := path.Clean(path.Join(newDir, target))
		if strings.HasPrefix(brokenResolved, "..") {
			continue
		}
		brokenID := strings.TrimSuffix(brokenResolved, ".md")

		// Resolve from the old base (where it should work).
		goodResolved := path.Clean(path.Join(oldDir, target))
		if strings.HasPrefix(goodResolved, "..") {
			continue
		}
		goodID := strings.TrimSuffix(goodResolved, ".md")

		// The link is broken from the new base...
		if exists(okf.ConceptID(brokenID)) {
			continue // not actually broken
		}
		// ...but resolves from the old base.
		if !exists(okf.ConceptID(goodID)) {
			continue // broken from both bases: no fix
		}

		if okf.ConceptID(goodID) == self || okf.ConceptID(goodID) == self+"/index" {
			out[brokenResolved] = &Fix{Kind: FixRebaseLink, Field: pathPart}
			continue
		}

		// Compute correct href relative to the new base.
		newHref := kb.RelLink(newDir, goodResolved)
		// Keep the ".md" or not to match the original style.
		if !strings.EqualFold(path.Ext(pathPart), ".md") {
			newHref = strings.TrimSuffix(newHref, ".md")
		}
		out[brokenResolved] = &Fix{Kind: FixRebaseLink, Field: pathPart, To: newHref}
	}
	return out
}

func pathHasPrefix(candidate, prefix string) bool {
	if !strings.HasPrefix(candidate, prefix) {
		return false
	}
	if len(candidate) == len(prefix) {
		return true
	}
	if last := prefix[len(prefix)-1]; last == '/' || last == '\\' {
		return true // prefix already ends at a separator boundary (e.g. bare "/" or "C:\" root)
	}
	next := candidate[len(prefix)]
	return next == '/' || next == '\\'
}

func emptyFrontmatterValue(value interface{}) bool {
	switch v := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(v) == ""
	case []string:
		if len(v) == 0 {
			return true
		}
		for _, item := range v {
			if strings.TrimSpace(item) != "" {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// frontmatterValue returns fm[key], or nil when the key is absent.
func frontmatterValue(fm *okf.Frontmatter, key string) interface{} {
	v, _ := fm.Get(key)
	return v
}

// firstH1 returns the title of the body's first level-1 heading, or "".
func firstH1(body string) string {
	for _, h := range okf.ListHeadings(body) {
		if h.Level == 1 {
			return strings.TrimSpace(h.Title)
		}
	}
	return ""
}

// retiredOf returns the predicate "this concept's status is retired" over a
// link graph's facets.
func retiredOf(lg *kb.LinkGraph) func(okf.ConceptID) bool {
	return func(id okf.ConceptID) bool {
		i, ok := lg.Index[id]
		return ok && retired(lg.Facets[i].Status)
	}
}

// checkCuratedIndex verifies both directions of a curated index (D907).
// folder is passed to ReadIndex; indexPath is the exact physical base used by
// ExtractLinks so relative Markdown links resolve from the right directory.
// A retired candidate is never reported missing: index_lists_retired (and its
// auto-repair) removes its entry, so asking for it back would loop (D826).
func checkCuratedIndex(k *kb.KB, folder, indexPath string, candidates []okf.ConceptID, validateLinks bool, findings *[]Finding, exists func(okf.ConceptID) bool, isRetired func(okf.ConceptID) bool) {
	content, err := k.ReadIndex(folder)
	if err != nil {
		*findings = append(*findings, newFinding("index_incomplete", Finding{
			Path:    indexPath,
			Message: "curated index is missing or unreadable",
		}))
		return
	}
	_, body, _ := okf.SplitFrontmatter(content)
	targets := map[okf.ConceptID]bool{}
	for _, target := range kb.ExtractLinks(body, indexPath, k.AssetExists) {
		targets[target] = true
		// An expanded concept can be linked as "<c>/index.md", which extracts
		// as "<map>/<c>/index" — resolvable and correct, but not the candidate
		// ID "<map>/<c>", so the entry counted as missing. Accept both forms
		// here rather than canonicalising in ExtractLinks, which would silently
		// rewrite graph edges and break expanded_ambiguous (D149).
		if parent, ok := strings.CutSuffix(string(target), "/index"); ok && parent != "" {
			targets[okf.ConceptID(parent)] = true
		}
	}
	if validateLinks {
		checkIndexLinks(k, indexPath, body, findings, exists)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i] < candidates[j] })
	for _, candidate := range candidates {
		if !targets[candidate] && !isRetired(candidate) {
			*findings = append(*findings, newFinding("index_incomplete", Finding{
				Path:    indexPath,
				Message: fmt.Sprintf("missing curated index entry for %s", candidate),
			}))
		}
	}
}

// checkIndexLinks reports every link in an index body whose target does not
// resolve, as broken_link on the index itself.
func checkIndexLinks(k *kb.KB, indexPath, body string, findings *[]Finding, exists func(okf.ConceptID) bool) {
	for _, target := range kb.ExtractLinks(body, indexPath, k.AssetExists) {
		// A link target is broken iff it was not among the enumerated
		// concepts (D294): this avoids an os.Stat per link.
		if !exists(target) {
			*findings = append(*findings, newFinding("broken_link", Finding{
				Path:    indexPath,
				Message: fmt.Sprintf("broken link to %s", okf.IDToPath(target)),
			}))
		}
	}
}

// scopeMatchesDir reports whether a directory path (a map or an expanded
// concept, e.g. "map/concept") falls within scopeNorm, for checks that are
// directory-based rather than concept-based (see the D77 WP4 structural
// checks). Unlike the concept-scope match above, this also matches when
// scopeNorm points *inside* dir (e.g. scope="map/concept/child" still
// selects dir="map/concept"), since a scoped lint on a child should still
// catch its own expanded concept. An empty scopeNorm matches everything.
func scopeMatchesDir(scopeNorm, dir string) bool {
	if scopeNorm == "" {
		return true
	}
	if dir == scopeNorm || strings.HasPrefix(dir, scopeNorm+"/") {
		return true
	}
	return strings.HasPrefix(scopeNorm, dir+"/")
}

// mapFieldContractFindings applies the value and forbidden-field parts of a
// map contract (D275) to one concept's frontmatter. Nothing silences either
// check (D907). An absent field is never an
// invalid value: presence is required_fields' job.
func mapFieldContractFindings(relPath, mapName string, contract kb.MapContract, parsed *okf.Frontmatter) []Finding {
	if parsed == nil {
		return nil
	}
	var out []Finding
	seen := map[string]bool{}
	var fields []string
	for f := range contract.FieldValues {
		fields = append(fields, f)
	}
	for _, byField := range contract.FieldValuesByType {
		for f := range byField {
			fields = append(fields, f)
		}
	}
	sort.Strings(fields)
	for _, field := range fields {
		if seen[field] {
			continue
		}
		seen[field] = true
		allowed, ok := contract.AllowedValues(parsed.Type(), field)
		if !ok {
			continue
		}
		value, exists := parsed.Get(field)
		if !exists {
			continue
		}
		var got []string
		switch v := value.(type) {
		case string:
			got = []string{v}
		case []string:
			got = v
		default:
			got = []string{fmt.Sprint(v)}
		}
		for _, g := range got {
			g = strings.TrimSpace(g)
			if !fieldValueAllowed(allowed, g) {
				f := newFinding("invalid_field_value", Finding{
					Path:    relPath,
					Message: fmt.Sprintf("field %q has value %q, allowed by map %q: %s", field, g, mapName, strings.Join(allowed, ", ")),
				})
				// D296: a synonym of exactly one allowed value is mechanical.
				if _, scalar := value.(string); scalar {
					if to, ok := familiesFor(&contract).canonicalIn(g, allowed); ok {
						f.Fix = &Fix{Kind: FixSetValue, Field: field, To: to}
						f.Message += fmt.Sprintf(" — fix: set it to %q", to)
					}
				}
				out = append(out, f)
				break
			}
		}
	}
	for _, field := range contract.ForbiddenFields {
		if _, exists := parsed.Get(field); exists {
			out = append(out, newFinding("forbidden_field", Finding{
				Path:    relPath,
				Message: fmt.Sprintf("field %q is forbidden by map %q", field, mapName),
			}))
		}
	}
	return out
}

func fieldValueAllowed(allowed []string, v string) bool {
	for _, a := range allowed {
		if a == v {
			return true
		}
	}
	return false
}

// indexListsRetired is the index_lists_retired check (D344): the curated index of
// a live map still links a retired concept, which is what a hand-made retirement
// (status patch, move, no index edit) leaves behind. One finding per retired
// concept listed (D366: each carries its own fix). Exempt: journals, a
// generated index (the server owns it), and a map where at least half of the
// top-level concepts are retired, which lists them by design (an archive map,
// including its "Moved here" entries). Not a write-path check: it is not tied
// to the written concept, and concept_archive already removes the entry.
//
// The finding carries drop_index_entry (D366) when a line of the index lists
// the concept and nothing else, the same rule concept_archive applies; a
// concept cited only on lines with other links keeps no fix.
func indexListsRetired(k *kb.KB, mapName string, contract kb.MapContract, concepts map[okf.ConceptID]string, lg *kb.LinkGraph) []Finding {
	if contract.Kind == "journal" || contract.Index == kb.IndexGenerated {
		return nil
	}
	isRetired := retiredOf(lg)
	top, retiredTop := 0, 0
	for id := range concepts {
		if rest, ok := strings.CutPrefix(string(id), mapName+"/"); ok && !strings.Contains(rest, "/") {
			top++
			if isRetired(id) {
				retiredTop++
			}
		}
	}
	if kb.IsArchiveMap(contract, top, retiredTop) {
		return nil
	}
	content, err := k.ReadIndex(mapName)
	if err != nil {
		return nil
	}
	_, body, _ := okf.SplitFrontmatter(content)
	var hits []okf.ConceptID
	alone := map[okf.ConceptID]bool{} // listed on a line with no other link
	seen := map[okf.ConceptID]bool{}
	for _, t := range kb.ExtractLinks(body, mapName+"/index.md", k.AssetExists) {
		if !seen[t] && isRetired(t) {
			hits = append(hits, t)
		}
		seen[t] = true
	}
	for _, line := range strings.Split(body, "\n") {
		if ids := kb.ExtractLinks(line, mapName+"/index.md", k.AssetExists); len(ids) == 1 {
			alone[ids[0]] = true
		}
	}
	var out []Finding
	for _, h := range hits {
		f := newFinding("index_lists_retired", Finding{
			Path:    mapName + "/index.md",
			Message: fmt.Sprintf("the index lists 1 retired concept(s): %s — retire pages with concept_archive (it drops the entry; not in tools/list, callable by name), or run kb_repair for index_lists_retired, or remove the lines with index_patch", h),
		})
		if alone[h] {
			f.Fix = &Fix{Kind: FixDropIndexEntry, Field: string(h)}
		}
		out = append(out, f)
	}
	return out
}
