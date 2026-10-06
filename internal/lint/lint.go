// Package lint implements deterministic lint checks on a KB scope.
package lint

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
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
// be inside a URL (urlRe below) or covered by the concept's Map
// machine_path_allow_prefixes contract, both of which make it an
// operational/target path rather than a client-local one.
var machinePathRe = regexp.MustCompile(`(?:/Users/|/home/|~/|C:\\Users\\)[^\s` + "`" + `'"()]*`)

// urlRe matches a URL with an explicit scheme (e.g. "https://", "s3://"). A
// machine_path candidate found inside one of these spans is a URL host/path
// component, not a filesystem path on the machine that wrote it (D124).
var urlRe = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s` + "`" + `'"()]*`)

// Severity levels.
const (
	SevInfo    = "info"
	SevWarning = "warning"
	SevError   = "error"
)

// Severities lists the accepted severity names, ordered from least to most
// severe. Exposed so a caller rejecting an invalid input can name the valid
// ones instead of hardcoding a list that drifts from this one.
var Severities = []string{SevInfo, SevWarning, SevError}

// severityRank orders the three levels. -1 marks a string that is not a
// severity at all, which is how callers tell "below the floor" from "not a
// severity" without a second lookup.
func severityRank(severity string) int {
	switch severity {
	case SevInfo:
		return 0
	case SevWarning:
		return 1
	case SevError:
		return 2
	}
	return -1
}

// ValidSeverity reports whether severity names one of the three levels.
func ValidSeverity(severity string) bool { return severityRank(severity) >= 0 }

// Filter returns the findings at or above floor, together with the counts by
// check and by severity computed on the **input** slice — deliberately before
// filtering. Omitting findings silently would make a response lie about the
// state of the KB: a caller always gets to know the shape of what it is not
// being shown. A finding whose severity is unrecognized is kept, since dropping
// it would hide a check whose severity was mistyped.
func Filter(findings []Finding, floor string) (kept []Finding, byCheck, bySeverity map[string]int) {
	byCheck, bySeverity = map[string]int{}, map[string]int{}
	min := severityRank(floor)
	for _, f := range findings {
		byCheck[f.Check]++
		bySeverity[f.Severity]++
		if rank := severityRank(f.Severity); rank < 0 || rank >= min {
			kept = append(kept, f)
		}
	}
	return kept, byCheck, bySeverity
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

// perConceptChecks are the checks a concept may silence with lint_ignore, i.e.
// the warning/info ones driven by that concept's own body or frontmatter. Kept in
// one place so an unknown name in lint_ignore can be reported rather than
// silently suppressing nothing.
//
// Deliberately excluded: every SevError check. missing_required_field and
// expanded_ambiguous are contract violations, not judgements — letting a concept
// declare its own contract void is not an escape hatch, it is a hole. Also
// excluded: the directory-based checks (map_oversize, index_incomplete,
// expanded_*), which belong to a map or a directory and have no concept
// frontmatter to read.
var perConceptChecks = map[string]bool{
	"broken_link":            true,
	"machine_path":           true,
	"concept_oversize":       true,
	"stale_claim":            true,
	"imported_draft":         true,
	"secrets_on_non_service": true,
	"orphan":                 true,
	"missing_title":          true,
	"duplicate_link":         true,
	"bare_link_list":         true,
	// Structural checks (D243). island is deliberately absent: it belongs to
	// a component, not to one concept.
	"cut_concept":     true,
	"link_to_retired": true,
	"broken_relation": true,
	"map_misfit":      true,
	// Path placeholder registry (D263): a concept documenting an old key on
	// purpose must be writable. unused_placeholder belongs to paths.yaml.
	"unknown_placeholder": true,
	// Glossary (D276): a migration note may quote the old name on purpose.
	"forbidden_term": true,
	// Conformance (D289). tool_param_field is deliberately absent: a concept
	// cannot declare a tool argument a legitimate field.
	"nonstandard_field": true,
	// D296: a sentence in a vocabulary field.
	"prose_value": true,
	// D297: lifecycle decay.
	"stale_open":               true,
	"closed_with_open_items":   true,
	"template_section_missing": true,
	"open_marker":              true,
	// D295: malformed_frontmatter is not suppressible (like tool_param_field),
	// but is a per-concept check so it appears here.
	"malformed_frontmatter": true,
	// D278: an ingested Source nothing cites.
	"source_uncited": true,
	// D298: kb_review kinds. Dismissing a review item is lint_ignore on a
	// concept it names; TestReviewKindsDismissible pins every kind here.
	ReviewDuplicate:     true,
	ReviewZombie:        true,
	ReviewPromotion:     true,
	ReviewGlossary:      true,
	ReviewLintJudgement: true,
	// D301: cost kinds.
	ReviewRepeatedFact: true,
	ReviewReadHotspot:  true,
	// D302.
	ReviewScatteredWork: true,
	// D304: dismissed in a map's _map.md, not on a concept.
	ReviewMapNaming: true,
	// D301: an efficiency choice a concept may decline.
	"reciprocal_link_item": true,
	// D306: a member of an island accepts the whole island (applyMapIgnores).
	"island": true,
}

// lintIgnoreSet reads a concept's lint_ignore frontmatter key (D159). A bare
// string is accepted as a one-element list: the frontmatter parser distinguishes
// the two and a bare string is the obvious authoring mistake.
func lintIgnoreSet(fm *okf.Frontmatter) map[string]bool {
	if fm == nil {
		return nil
	}
	v, ok := fm.Get("lint_ignore")
	if !ok {
		return nil
	}
	out := map[string]bool{}
	switch value := v.(type) {
	case string:
		if strings.TrimSpace(value) != "" {
			out[strings.TrimSpace(value)] = true
		}
	case []string:
		for _, item := range value {
			if strings.TrimSpace(item) != "" {
				out[strings.TrimSpace(item)] = true
			}
		}
	}
	return out
}

// Finding represents a single lint finding.
type Finding struct {
	Path     string // concept path relative to KB (e.g. "arch/runbook.md")
	Check    string // check name (e.g. "broken_link", "stale_claim")
	Severity string // "warning" or "error"
	Message  string
	Fix      *Fix // optional mechanical remedy (D289)
	// Proposal is the structured vocabulary a missing_value_contract
	// finding suggests (D296).
	Proposal *Proposal
	// Count is a check-specific tally (open_marker: markers found, D297).
	Count int
	// members are an island's concepts, for its acceptance (D306).
	members []okf.ConceptID
}

// Now is used for date comparison in stale_claim checks. Override in tests.
var Now = func() time.Time { return time.Now() }

// Run executes all deterministic lint checks on the given scope.
// If scope is empty, lints the entire KB.
// When scopeNeighbors is true, also lint the graph neighbors of concepts in scope.
func Run(k *kb.KB, scope string, scopeNeighbors bool) ([]Finding, error) {
	findings, err := runChecks(k, scope, scopeNeighbors)
	if err != nil {
		return nil, err
	}
	return applyMapIgnores(k, findings), nil
}

// mapOnlyIgnorable are the checks a map's _map.md may accept that no single
// concept owns: they are reported on the map, or on one member of a graph
// component, so lint_ignore on a concept could never reach them.
var mapOnlyIgnorable = map[string]bool{
	"facet_sprawl":           true,
	"missing_value_contract": true,
	"island":                 true,
}

// applyMapIgnores drops the findings a map accepts as a whole with
// lint_ignore in its _map.md (D306): a KB's style choice (a "See also" that
// says why, services linking the infrastructure they run on) is one decision
// for the map, not one write per concept. Every concept-suppressible check
// may be named, plus mapOnlyIgnorable; errors never go. An island goes when
// any member's concept or map accepts it. A name that suppresses nothing is
// itself reported, on the _map.md.
func applyMapIgnores(k *kb.KB, findings []Finding) []Finding {
	archives, err := k.ListArchives()
	if err != nil {
		return findings
	}
	ignores := map[string]map[string]bool{}
	var invalid []Finding
	for _, a := range archives {
		meta, err := k.ReadArchiveMeta(a)
		if err != nil {
			continue
		}
		set := lintIgnoreSet(meta)
		if len(set) == 0 {
			continue
		}
		ignores[a] = set
		names := make([]string, 0, len(set))
		for name := range set {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if perConceptChecks[name] || mapOnlyIgnorable[name] {
				continue
			}
			invalid = append(invalid, Finding{
				Path:     a + "/_map.md",
				Check:    "lint_ignore_invalid",
				Severity: SevWarning,
				Message:  fmt.Sprintf("lint_ignore names %q, which a map cannot accept (unknown, an error, or a directory-level check), so nothing is suppressed", name),
			})
		}
	}
	mapOf := func(path string) string {
		if m, _, ok := strings.Cut(path, "/"); ok {
			return m
		}
		return ""
	}
	accepts := func(mapName, check string) bool {
		return ignores[mapName][check] && (perConceptChecks[check] || mapOnlyIgnorable[check])
	}
	out := findings[:0]
	for _, f := range findings {
		if f.Severity == SevError {
			out = append(out, f)
			continue
		}
		if accepts(mapOf(f.Path), f.Check) {
			continue
		}
		if f.Check == "island" && islandAccepted(k, f, accepts) {
			continue
		}
		out = append(out, f)
	}
	return append(out, invalid...)
}

// islandAccepted reports an island one of whose members, or whose map,
// accepts it: the finding is reported on one member, but the island is the
// whole component's.
func islandAccepted(k *kb.KB, f Finding, accepts func(mapName, check string) bool) bool {
	for _, id := range f.members {
		m, _, _ := strings.Cut(string(id), "/")
		if accepts(m, "island") {
			return true
		}
		if cd, err := k.ReadConcept(id); err == nil {
			if fm, _ := okf.ParseFrontmatter(cd.FrontmatterRaw); fm != nil && lintIgnoreSet(fm)["island"] {
				return true
			}
		}
	}
	return false
}

// runChecks is Run before the map-level lint_ignore pass.
func runChecks(k *kb.KB, scope string, scopeNeighbors bool) ([]Finding, error) {
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

	// Archive name set for orphan skip rule.
	archives, err := k.ListArchives()
	if err != nil {
		return nil, fmt.Errorf("lint.Run: list archives: %w", err)
	}
	archiveSet := make(map[string]bool, len(archives))
	for _, a := range archives {
		archiveSet[a] = true
	}

	var findings []Finding
	// Contracts are descriptor-level data. Cache them once per map for this
	// run, so a map with many concepts does not repeatedly parse _map.md.
	contracts := make(map[string]kb.MapContract, len(archives))
	templateSections := map[string][]string{} // type → its template's H2s, read once per run (D297)
	for _, archive := range archives {
		contract, contractErr := k.ReadMapContract(archive)
		if contractErr != nil {
			continue // A non-map directory has no descriptor/contract.
		}
		contracts[archive] = contract
		if scopeMatchesDir(scopeNorm, archive) {
			for _, malformed := range contract.Malformed {
				findings = append(findings, Finding{
					Path:     malformed.Descriptor,
					Check:    "contract_malformed",
					Severity: SevInfo,
					Message:  fmt.Sprintf("malformed lint contract key %q", malformed.Key),
				})
			}
		}
	}

	// The KB's path placeholder registry (D263). Its own findings — a
	// malformed entry, a declared key nothing cites — belong to paths.yaml,
	// a KB-level file, so only an unscoped lint reports them.
	registry, registryFindings := loadRegistryLint(k)
	if scopeNorm == "" {
		findings = append(findings, registryFindings...)
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
		for _, id := range registry.unused(cited) {
			findings = append(findings, Finding{
				Path:     kb.PathRegistryFile,
				Check:    "unused_placeholder",
				Severity: SevInfo,
				Message:  fmt.Sprintf("{{%s}} is declared but no concept or artifact cites it — drop it, or merge it into the key that is used", id),
			})
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

	// Source citations (D278), over the whole KB whatever the scope.
	sourceCited := kb.SourceCitations(allConcepts)

	// The structural analysis runs on the whole graph whatever the scope, so
	// a scoped lint gives a concept the verdict a whole-KB lint gives it;
	// scope only decides which findings are emitted (D243).
	st, err := analyseStructure(k, lg, archives, contracts)
	if err != nil {
		return nil, fmt.Errorf("lint.Run: structure: %w", err)
	}

	// readTargetBody serves reciprocal_link_item the body of a link target
	// (D309): the target's text, not its links section, must link back. The
	// split is cached across the run — a hub is the target of many pages.
	targetBodies := map[okf.ConceptID]string{}
	readTargetBody := func(target okf.ConceptID) (string, string) {
		b, ok := targetBodies[target]
		if !ok {
			_, b, _ = okf.SplitFrontmatter(allConcepts[target])
			targetBodies[target] = b
		}
		return b, relPathOf[target]
	}

	for id, content := range toCheck {
		relPath := okf.IDToPath(id)
		// Relative links resolve against the file that contains them, which for
		// an expanded concept is "<id>/index.md", not "<id>.md" (D149). relPath
		// stays ID-derived: it is what Finding.Path reports and what callers
		// sort on. The two coincide for a plain concept.
		linkBase := relPathOf[id]
		fmRaw, body, hasFM := okf.SplitFrontmatter(content)
		// lint_ignore (D159): a concept that documents a false positive — a
		// ~/.ssh/config in prose, a deliberately-broken example link — could not
		// be written without generating the findings it describes. One closure
		// rather than a condition at each site, and errors are never suppressible:
		// they route around emit on purpose.
		var conceptIgnores map[string]bool
		if hasFM {
			if parsedForIgnore, _ := okf.ParseFrontmatter(fmRaw); parsedForIgnore != nil {
				conceptIgnores = lintIgnoreSet(parsedForIgnore)
			}
		}
		emit := func(f Finding) {
			if suppressed(f, conceptIgnores) {
				return
			}
			findings = append(findings, f)
		}
		for name := range conceptIgnores {
			if perConceptChecks[name] {
				continue
			}
			reason := "not a known lint check"
			if name == "missing_required_field" || name == "invalid_field_value" || name == "forbidden_field" || name == "expanded_ambiguous" {
				reason = "an error-severity contract violation, which lint_ignore cannot silence"
			} else if name == "tool_param_field" {
				reason = "a tool argument is never a legitimate field, so it cannot be declared one"
			} else if name == "map_oversize" || name == "index_incomplete" || name == "index_stale" || name == "orphan_asset" || name == "oversized_asset" || name == "unlistable_assets" || name == "unused_placeholder" || strings.HasPrefix(name, "expanded_") {
				// orphan_asset belongs to an expanded concept's asset set, reported
				// in the directory pass: there is no single concept frontmatter that
				// owns it, so listing it as suppressible would be a promise the
				// implementation does not keep.
				reason = "a directory-level check, not a per-concept one"
			}
			findings = append(findings, Finding{
				Path:     relPath,
				Check:    "lint_ignore_invalid",
				Severity: SevWarning,
				Message:  fmt.Sprintf("lint_ignore names %q: %s, so nothing is suppressed", name, reason),
			})
		}
		parts := strings.Split(string(id), "/")
		var allowPrefixes []string
		if len(parts) > 1 && archiveSet[parts[0]] {
			allowPrefixes = contracts[parts[0]].MachinePathAllowPrefixes
		}

		// --- broken_link (warning) ---
		// For expanded concepts, detect links broken by past expansions:
		// if the same href resolves from the pre-expansion base "<id>.md",
		// the fix is a rebase (D295 WP2).
		isExpanded := strings.HasSuffix(linkBase, "/index.md") && strings.Count(linkBase, "/") >= 2
		var rebasable map[string]*Fix // broken target path → fix
		if isExpanded {
			preExpBase := strings.TrimSuffix(linkBase, "/index.md") + ".md"
			rebasable = brokenLinkRebaseFixes(body, linkBase, preExpBase, exists)
		}
		for _, target := range kb.ExtractLinks(body, linkBase, k.AssetExists) {
			targetPath := okf.IDToPath(target)
			if !exists(target) {
				f := Finding{
					Path:     relPath,
					Check:    "broken_link",
					Severity: SevWarning,
					Message:  fmt.Sprintf("broken link to %s", targetPath),
				}
				if fix, ok := rebasable[targetPath]; ok {
					f.Fix = fix
					f.Message += "; fix: rebase relative to the expanded index"
				}
				emit(f)
			}
		}

		// --- duplicate_link / bare_link_list (info): the trailing links
		// section (linksection.go) ---
		if heading, dups, fixableDups, bare, n := linksSectionIssues(body, linkBase, k.AssetExists); heading != "" {
			if len(dups) > 0 {
				for _, d := range dups {
					f := Finding{
						Path:     relPath,
						Check:    "duplicate_link",
						Severity: SevInfo,
						Message:  fmt.Sprintf("linked both in the text and under %q: %s — keep the link where the text says why", heading, d),
					}
					if fix, ok := fixableDups[d]; ok {
						f.Fix = fix
					}
					emit(f)
				}
			}
			// --- reciprocal_link_item (info, D301): an opt-in efficiency
			// fix, never conformance debt; the target's own link keeps the
			// edge navigable both ways through backlinks. ---
			recips := reciprocalLinkItems(body, linkBase, id, graph.Out, dups, k.AssetExists, readTargetBody)
			recipIDs := make([]okf.ConceptID, 0, len(recips))
			for target := range recips {
				recipIDs = append(recipIDs, target)
			}
			sort.Slice(recipIDs, func(i, j int) bool { return recipIDs[i] < recipIDs[j] })
			for _, target := range recipIDs {
				emit(Finding{
					Path:     relPath,
					Check:    "reciprocal_link_item",
					Severity: SevInfo,
					Message:  fmt.Sprintf("%s already links back here, so the backlink shows this edge — the item under %q is a second write to keep in sync", target, heading),
					Fix:      &Fix{Kind: FixDropLinkItem, Field: recips[target]},
				})
			}
			if bare {
				emit(Finding{
					Path:     relPath,
					Check:    "bare_link_list",
					Severity: SevInfo,
					Message:  fmt.Sprintf("%q lists %d link(s) with no word on why each matters — add a short reason per link", heading, n),
				})
			}
		}

		// --- unknown_placeholder (warning, D263) ---
		// The keys come from the graph cache's facet (D262), the same parse
		// sync_pull lists them from — not a second regex here.
		if i, ok := lg.Index[id]; ok {
			if undeclared := registry.undeclared(lg.Facets[i].Placeholders); len(undeclared) > 0 {
				emit(unknownPlaceholderFinding(relPath, undeclared))
			}
		}

		// --- forbidden_term (warning, D276) ---
		for _, f := range forbiddenTermFindings(relPath, body, glossary) {
			emit(f)
		}

		// --- source_uncited (warning, D278) ---
		if f, ok := uncitedSourceFinding(relPath, id, content, sourceCited); ok {
			emit(f)
		}

		// --- concept_oversize (info) ---
		// A map's contract may set its own threshold (oversize_bytes, D301).
		oversize := conceptOversizeThreshold
		if len(parts) > 1 && contracts[parts[0]].OversizeBytes > 0 {
			oversize = contracts[parts[0]].OversizeBytes
		}
		if len(body) > oversize {
			// concept_expand requires exactly two segments and the write path caps
			// depth at three, so for a satellite the remedy this check used to
			// advise is structurally unavailable — and the two largest concepts in
			// the reporting KB were satellites (D159).
			remedy := "consider concept_expand to split it into a dossier"
			if len(parts) > 2 {
				remedy = "this is a satellite, so concept_expand does not apply: split it into sibling satellites of the same expanded concept and link them from its index"
			}
			emit(Finding{
				Path:     string(id),
				Check:    "concept_oversize",
				Severity: SevInfo,
				Message:  fmt.Sprintf("%d bytes in one concept (threshold %d; concept_read returns an outline instead of the body above %d) — %s", len(body), oversize, okf.ConceptReadSizeGuard, remedy),
			})
		}

		// --- stale_claim / imported_draft / missing_required_field ---
		// services/ concepts are rooted outside data/ and therefore have no map
		// descriptor to contract against.
		var parsed *okf.Frontmatter
		if hasFM {
			parsed, _ = okf.ParseFrontmatter(fmRaw)
			if parsed != nil {
				// D74 WP1: a concept imported via `cartographer import` (or the
				// agent-side fallback) is marked status: imported until curated.
				// The finding keeps the curation backlog visible and resumable
				// across sessions instead of a big-bang rewrite.
				if statusVal, ok := parsed.Get("status"); ok {
					if statusStr, ok := statusVal.(string); ok && statusStr == "imported" {
						emit(Finding{
							Path:     relPath,
							Check:    "imported_draft",
							Severity: SevWarning,
							Message:  "imported concept awaiting curation",
						})
					}
				}

				// --- secrets_on_non_service (warning, D158) ---
				// service_list/service_get match type Service (case-insensitively
				// since D158): a concept declaring secrets under any other type has
				// them unresolvable, and nothing else reported why.
				if !strings.EqualFold(parsed.Type(), "Service") {
					for _, field := range []string{"secrets_source", "secret_refs"} {
						if v, ok := parsed.Get(field); ok && !emptyFrontmatterValue(v) {
							emit(Finding{
								Path:     relPath,
								Check:    "secrets_on_non_service",
								Severity: SevWarning,
								Message:  fmt.Sprintf("declares %s but type is %q — service_list/service_get only match type Service, so these secrets are unresolvable", field, parsed.Type()),
							})
						}
					}
				}
			}
		}
		// --- stale_claim, machine_path, missing_title, map contracts,
		// nonstandard_field, tool_param_field: one implementation shared with
		// CheckConcept (D289) ---
		in := conceptInput{RelPath: relPath, Body: body, FrontmatterRaw: fmRaw, Parsed: parsed, AllowPrefixes: allowPrefixes, Registry: registry}
		if len(parts) > 1 && archiveSet[parts[0]] {
			c := contracts[parts[0]]
			in.MapName, in.Contract = parts[0], &c
			if c.TemplateSections && parsed != nil {
				typ := parsed.Type()
				if _, done := templateSections[typ]; !done {
					templateSections[typ] = k.TemplateSections(typ)
				}
				in.Sections = templateSections[typ]
			}
		}
		for _, f := range frontmatterFindings(in) {
			emit(f)
		}

		// --- orphan (warning) ---
		if len(incomingLinks[id]) == 0 {
			parts := strings.Split(string(id), "/")
			// A concept at depth=1 inside a known archive is an entry point,
			// reached from the map's index — unless it links to nothing
			// either: the index is not an edge of the graph, so that concept
			// is a node connected to nothing, which nothing else reports.
			atArchiveTop := len(parts) == 2 && archiveSet[parts[0]]
			outgoing := 0
			for t := range graph.Out[id] {
				if _, ok := relPathOf[t]; ok && t != id {
					outgoing++
				}
			}
			switch {
			case !atArchiveTop:
				emit(Finding{
					Path:     relPath,
					Check:    "orphan",
					Severity: SevWarning,
					Message:  "no incoming links",
				})
			case outgoing == 0:
				emit(Finding{
					Path:     relPath,
					Check:    "orphan",
					Severity: SevWarning,
					Message:  "no links in or out: a node connected to nothing in the graph (the map's index is not a link) — link it to the concepts it relates to (link_suggest proposes some)",
				})
			}
		}

		// --- cut_concept, broken_relation, link_to_retired, map_misfit ---
		for _, f := range st.conceptChecks(id, relPath) {
			emit(f)
		}
	}
	findings = append(findings, st.islandFindings(func(id okf.ConceptID) bool {
		_, ok := toCheck[id]
		return ok
	})...)

	// --- structural checks on maps and expanded concepts (D77 WP4) ---
	// Directory-based (unlike the checks above, not driven by toCheck).
	for _, archiveName := range archives {
		if scopeMatchesDir(scopeNorm, archiveName) {
			// --- legacy_archive_descriptor (warning) ---
			// A map still described by the pre-D77 _archive.md shape. Read-compat
			// keeps it working; the finding is the migration backlog.
			if _, mapErr := k.ReadRaw(archiveName + "/_map.md"); errors.Is(mapErr, okf.ErrNotFound) {
				if _, legacyErr := k.ReadRaw(archiveName + "/_archive.md"); legacyErr == nil {
					findings = append(findings, Finding{
						Path:     archiveName + "/_archive.md",
						Check:    "legacy_archive_descriptor",
						Severity: SevWarning,
						Message:  "legacy _archive.md descriptor — rewrite as _map.md with a kind (D77)",
					})
				}
			}

			// --- missing_value_contract (info, D289) ---
			if c, ok := contracts[archiveName]; ok {
				findings = append(findings, valueContractFindings(archiveName, c, allConcepts)...)
			}
			// --- facet_sprawl (info, D297) ---
			if _, ok := contracts[archiveName]; ok {
				findings = append(findings, facetSprawlFindings(archiveName, allConcepts)...)
			}

			// --- map_oversize (info) ---
			mapConcepts := 0
			for id := range allConcepts {
				if strings.HasPrefix(string(id), archiveName+"/") {
					mapConcepts++
				}
			}
			if mapConcepts > mapOversizeThreshold {
				findings = append(findings, Finding{
					Path:     archiveName,
					Check:    "map_oversize",
					Severity: SevInfo,
					Message:  fmt.Sprintf("%d concepts in one map (threshold %d) — consider a thematic split into a new map", mapConcepts, mapOversizeThreshold),
				})
			}
		}

		// --- index_incomplete / curated-index broken_link (D107) ---
		// Only candidates in toCheck participate, which keeps a scoped lint
		// actionable instead of reporting unrelated map siblings.
		contract, hasContract := contracts[archiveName]
		if hasContract && contract.Index == kb.IndexGenerated {
			// --- index_stale (info, D301) ---
			// The server owns the concept list of a generated index, so
			// completeness is not the agent's to check: only whether the
			// block still matches what the next write would put there.
			if scopeNorm == "" || scopeNorm == archiveName {
				if content, err := k.ReadIndex(archiveName); err == nil {
					want, werr := k.ExpectedIndexBlock(archiveName, contract)
					if got, _ := kb.IndexBlock(content); werr == nil && got != want {
						findings = append(findings, Finding{
							Path:     archiveName + "/index.md",
							Check:    "index_stale",
							Severity: SevInfo,
							Message:  "generated index block differs from the map's concepts (edited out of band or written by an older server) — the next write regenerates it",
						})
					}
					_, body, _ := okf.SplitFrontmatter(content)
					checkIndexLinks(k, archiveName+"/index.md", body, &findings, exists)
				}
			}
		} else if hasContract && contract.RequireIndexEntry {
			var direct []okf.ConceptID
			for id := range toCheck {
				parts := strings.Split(string(id), "/")
				if len(parts) == 2 && parts[0] == archiveName {
					direct = append(direct, id)
				}
			}
			// A full lint or an explicit map scope also validates the map index
			// when it has no direct concepts. A scope inside an expanded concept
			// must not surface unrelated map-index content.
			if len(direct) > 0 || scopeNorm == "" || scopeNorm == archiveName {
				checkCuratedIndex(k, archiveName, archiveName+"/index.md", direct, true, &findings, exists)
			}
		} else if scopeNorm == "" || scopeNorm == archiveName {
			// A dead link is dead whether or not the map promised completeness:
			// without this, a map created before its contract kept every stale
			// [[id]] a concept_move left behind and lint reported none (#320).
			// Completeness stays opt-in; only the links are checked here. A
			// missing index is not reported: nothing required one.
			if content, err := k.ReadIndex(archiveName); err == nil {
				_, body, _ := okf.SplitFrontmatter(content)
				checkIndexLinks(k, archiveName+"/index.md", body, &findings, exists)
			}
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
			if contract, ok := contracts[archiveName]; ok && contract.RequireIndexEntry {
				var satellites []okf.ConceptID
				for id := range toCheck {
					if strings.HasPrefix(string(id), string(expandedID)+"/") {
						satellites = append(satellites, id)
					}
				}
				if len(satellites) > 0 {
					// Expanded indexes are emitted by WalkConcepts, so their link
					// targets are already covered by the general broken_link pass.
					checkCuratedIndex(k, string(expandedID), string(expandedID)+"/index.md", satellites, false, &findings, exists)
				}
			}

			// --- expanded_missing_index (warning) ---
			// WriteConcept (D72 WP4) stubs index.md on every implicitly created
			// directory and ExpandConcept always produces one, so this only
			// fires for directories predating those fixes.
			_, indexErr := k.ReadIndex(string(expandedID))
			if indexErr != nil && errors.Is(indexErr, okf.ErrNotFound) {
				findings = append(findings, Finding{
					Path:     string(expandedID) + "/index.md",
					Check:    "expanded_missing_index",
					Severity: SevWarning,
					Message:  "expanded concept missing index.md",
				})
			}

			// --- expanded_ambiguous (error) ---
			// Both "<id>.md" and "<id>/index.md" exist: reads silently prefer
			// the direct form and writes are rejected (resolveConceptRelPath),
			// so this is the place that surfaces the conflict.
			if indexErr == nil {
				if _, directErr := k.ReadRaw(string(expandedID) + ".md"); directErr == nil {
					findings = append(findings, Finding{
						Path:     string(expandedID) + ".md",
						Check:    "expanded_ambiguous",
						Severity: SevError,
						Message:  fmt.Sprintf("both %s.md and %s/index.md exist — writes to %s are blocked until one form is removed", expandedID, expandedID, expandedID),
					})
				}
			}

			// --- expanded_as_category (warning) ---
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
					findings = append(findings, Finding{
						Path:     string(expandedID),
						Check:    "expanded_as_category",
						Severity: SevWarning,
						Message:  fmt.Sprintf("%d children, only %d linked to the concept's index — directory used as a category; categories belong to curated indexes, not the filesystem (D77)", len(children), linked),
					})
				}
			}

			// --- orphan_asset (info) ---
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
					findings = append(findings, Finding{
						Path:     string(expandedID),
						Check:    "unlistable_assets",
						Severity: SevWarning,
						Message:  fmt.Sprintf("the concept's assets cannot be listed: %v", assetErr),
					})
					continue
				}
				for _, asset := range assets {
					assetPath := string(expandedID) + "/" + asset.Path
					if asset.Oversized {
						findings = append(findings, Finding{
							Path:     assetPath,
							Check:    "oversized_asset",
							Severity: SevWarning,
							Message:  fmt.Sprintf("asset is %d bytes, above the %d MiB worth versioning in git — move it outside the KB and cite it by link (D270)", asset.Size, kb.AssetMaxFileSize>>20),
						})
					}
					if referenced[assetPath] {
						continue
					}
					findings = append(findings, Finding{
						Path:     assetPath,
						Check:    "orphan_asset",
						Severity: SevInfo,
						Message:  "asset is not cited by its owning dossier document; cite dossier artifacts from the document that owns them",
					})
				}
			}
		}
	}

	return findings, nil
}

// firstDisallowedMachinePath scans body for machine_path candidates in order
// and returns the first one that is neither inside a URL nor covered by
// allowPrefixes (the concept's Map machine_path_allow_prefixes contract,
// D124). Scanning continues past an allowed match so a later disallowed
// match still surfaces. Returns "" when every candidate is allowed or there
// is none.
func firstDisallowedMachinePath(body string, allowPrefixes []string) string {
	if all := disallowedMachinePaths(body, allowPrefixes); len(all) > 0 {
		return all[0]
	}
	return ""
}

// disallowedMachinePaths is every distinct disallowed candidate, in order.
func disallowedMachinePaths(body string, allowPrefixes []string) []string {
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
		if candidate == "" || matchesAllowedPrefix(candidate, allowPrefixes) || seen[candidate] {
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

// matchesAllowedPrefix reports whether candidate is covered by one of the
// Map's machine_path_allow_prefixes (D124), using literal segment-boundary
// prefix matching: "/home/nonroot" covers "/home/nonroot/.headroom" but not
// "/home/nonroot2" (prefix-boundary collision).
func matchesAllowedPrefix(candidate string, allowPrefixes []string) bool {
	for _, prefix := range allowPrefixes {
		if pathHasPrefix(candidate, prefix) {
			return true
		}
	}
	return false
}

// brokenLinkRebaseFixes computes, for an expanded concept, which broken
// markdown links would resolve from the pre-expansion base. Each is returned
// keyed by the broken target path (e.g. "map/c/other.md") with a Fix carrying
// the old href (Field) and the correct new href (To). D295 WP2.
func brokenLinkRebaseFixes(body, newBase, oldBase string, exists func(okf.ConceptID) bool) map[string]*Fix {
	oldDir, newDir := path.Dir(oldBase), path.Dir(newBase)
	masked := kb.MaskCodeSpans(body)
	mdLinkPat := regexp.MustCompile(`\[([^\]]*)\]\(([^)]+)\)`)
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

// checkCuratedIndex verifies both directions of an opted-in curated index.
// folder is passed to ReadIndex; indexPath is the exact physical base used by
// ExtractLinks so relative Markdown links resolve from the right directory.
func checkCuratedIndex(k *kb.KB, folder, indexPath string, candidates []okf.ConceptID, validateLinks bool, findings *[]Finding, exists func(okf.ConceptID) bool) {
	content, err := k.ReadIndex(folder)
	if err != nil {
		*findings = append(*findings, Finding{
			Path:     indexPath,
			Check:    "index_incomplete",
			Severity: SevWarning,
			Message:  "curated index is missing or unreadable",
		})
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
		if !targets[candidate] {
			*findings = append(*findings, Finding{
				Path:     indexPath,
				Check:    "index_incomplete",
				Severity: SevWarning,
				Message:  fmt.Sprintf("missing curated index entry for %s", candidate),
			})
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
			*findings = append(*findings, Finding{
				Path:     indexPath,
				Check:    "broken_link",
				Severity: SevWarning,
				Message:  fmt.Sprintf("broken link to %s", okf.IDToPath(target)),
			})
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
// map contract (D275) to one concept's frontmatter. Both checks are error
// severity and not suppressible with lint_ignore. An absent field is never an
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
				f := Finding{
					Path:     relPath,
					Check:    "invalid_field_value",
					Severity: SevError,
					Message:  fmt.Sprintf("field %q has value %q, allowed by map %q: %s", field, g, mapName, strings.Join(allowed, ", ")),
				}
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
			out = append(out, Finding{
				Path:     relPath,
				Check:    "forbidden_field",
				Severity: SevError,
				Message:  fmt.Sprintf("field %q is forbidden by map %q", field, mapName),
			})
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
