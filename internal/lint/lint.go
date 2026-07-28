// Package lint implements deterministic lint checks on a KB scope.
package lint

import (
	"errors"
	"fmt"
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
var machinePathRe = regexp.MustCompile(`(?:/Users/|/home/|~/|C:\\Users\\)[^\s` + "`" + `'"()]*`)

// Severity levels.
const (
	SevInfo    = "info"
	SevWarning = "warning"
	SevError   = "error"
)

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
	// conceptOversizeThreshold is the body size (bytes) above which a
	// concept is flagged for splitting via concept_expand — mirrors the
	// concept_read size guard (D78): a concept this large risks blowing
	// past a client's token budget on a plain read.
	conceptOversizeThreshold = 30000
)

// Finding represents a single lint finding.
type Finding struct {
	Path     string // concept path relative to KB (e.g. "arch/runbook.md")
	Check    string // check name (e.g. "broken_link", "stale_claim")
	Severity string // "warning" or "error"
	Message  string
}

// Now is used for date comparison in stale_claim checks. Override in tests.
var Now = func() time.Time { return time.Now() }

// Run executes all deterministic lint checks on the given scope.
// If scope is empty, lints the entire KB.
// When scopeNeighbors is true, also lint the graph neighbors of concepts in scope.
func Run(k *kb.KB, scope string, scopeNeighbors bool) ([]Finding, error) {
	// Collect all non-reserved concepts.
	allConcepts := map[okf.ConceptID]string{} // id → full content
	if err := k.WalkConcepts(func(id okf.ConceptID, content string) error {
		allConcepts[id] = content
		return nil
	}); err != nil {
		return nil, fmt.Errorf("lint.Run: walk: %w", err)
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

	// Expand scope with 1-hop graph neighbours when requested.
	if scopeNeighbors && len(toCheck) > 0 {
		extra := map[okf.ConceptID]bool{}
		for id := range toCheck {
			neighbors, err := k.GraphNeighbors(id, 1)
			if err != nil {
				return nil, fmt.Errorf("lint.Run: graph neighbors for %s: %w", id, err)
			}
			for nid := range neighbors {
				cid := okf.ConceptID(nid)
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

	// Build the reverse-link graph once over all concepts for the orphan check.
	// It retains self-links, preserving the pre-D108 orphan behaviour.
	incomingLinks, err := k.IncomingLinks()
	if err != nil {
		return nil, fmt.Errorf("lint.Run: incoming links: %w", err)
	}

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

	for id, content := range toCheck {
		relPath := okf.IDToPath(id)
		fmRaw, body, hasFM := okf.SplitFrontmatter(content)

		// --- broken_link (warning) ---
		for _, target := range kb.ExtractLinks(body, relPath) {
			targetPath := okf.IDToPath(target)
			_, readErr := k.ReadRaw(targetPath)
			if readErr != nil && errors.Is(readErr, okf.ErrNotFound) {
				findings = append(findings, Finding{
					Path:     relPath,
					Check:    "broken_link",
					Severity: SevWarning,
					Message:  fmt.Sprintf("broken link to %s", targetPath),
				})
			}
		}

		// --- machine_path (warning, D75 WP6) ---
		if matches := machinePathRe.FindAllString(body, -1); len(matches) > 0 {
			findings = append(findings, Finding{
				Path:     relPath,
				Check:    "machine_path",
				Severity: SevWarning,
				Message:  fmt.Sprintf("machine-specific path %q — use {{repo:<key>}}/{{path:<nome>}} instead (D75)", matches[0]),
			})
		}

		// --- concept_oversize (info) ---
		if len(body) > conceptOversizeThreshold {
			findings = append(findings, Finding{
				Path:     string(id),
				Check:    "concept_oversize",
				Severity: SevInfo,
				Message:  fmt.Sprintf("%d bytes in one concept (threshold %d) — consider concept_expand to split it into a dossier", len(body), conceptOversizeThreshold),
			})
		}

		// --- stale_claim / imported_draft / missing_required_field ---
		// services/ concepts are rooted outside data/ and therefore have no map
		// descriptor to contract against.
		var parsed *okf.Frontmatter
		if hasFM {
			parsed, _ = okf.ParseFrontmatter(fmRaw)
			if parsed != nil {
				if raVal, ok := parsed.Get("review_after"); ok {
					if dateStr, ok := raVal.(string); ok {
						t, parseErr := time.Parse("2006-01-02", dateStr)
						if parseErr == nil && t.Before(Now()) {
							findings = append(findings, Finding{
								Path:     relPath,
								Check:    "stale_claim",
								Severity: SevWarning,
								Message:  fmt.Sprintf("review_after %s is in the past", dateStr),
							})
						}
					}
				}

				// D74 WP1: a concept imported via `cartographer import` (or the
				// agent-side fallback) is marked status: imported until curated.
				// The finding keeps the curation backlog visible and resumable
				// across sessions instead of a big-bang rewrite.
				if statusVal, ok := parsed.Get("status"); ok {
					if statusStr, ok := statusVal.(string); ok && statusStr == "imported" {
						findings = append(findings, Finding{
							Path:     relPath,
							Check:    "imported_draft",
							Severity: SevWarning,
							Message:  "imported concept awaiting curation",
						})
					}
				}
			}
		}
		parts := strings.Split(string(id), "/")
		if len(parts) > 1 && archiveSet[parts[0]] {
			contract := contracts[parts[0]]
			for _, field := range contract.RequiredFor(func() string {
				if parsed == nil {
					return ""
				}
				return parsed.Type()
			}()) {
				missing := parsed == nil
				if !missing {
					value, exists := parsed.Get(field)
					missing = !exists || emptyFrontmatterValue(value)
				}
				if missing {
					findings = append(findings, Finding{
						Path:     relPath,
						Check:    "missing_required_field",
						Severity: SevError,
						Message:  fmt.Sprintf("missing required field %q required by map %q", field, parts[0]),
					})
				}
			}
		}

		// --- orphan (warning) ---
		if len(incomingLinks[id]) == 0 {
			parts := strings.Split(string(id), "/")
			// Skip concepts at depth=1 inside a known archive (expected entry points).
			atArchiveTop := len(parts) == 2 && archiveSet[parts[0]]
			if !atArchiveTop {
				findings = append(findings, Finding{
					Path:     relPath,
					Check:    "orphan",
					Severity: SevWarning,
					Message:  "no incoming links",
				})
			}
		}
	}

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
		if hasContract && contract.RequireIndexEntry {
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
				checkCuratedIndex(k, archiveName, archiveName+"/index.md", direct, true, &findings)
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
					checkCuratedIndex(k, string(expandedID), string(expandedID)+"/index.md", satellites, false, &findings)
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
					for _, tgt := range kb.ExtractLinks(indexBody, string(expandedID)+"/index.md") {
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
					for _, target := range kb.ExtractAssetLinks(body, basePath) {
						referenced[target] = true
					}
				}
				assets, assetErr := k.ListAssets(expandedID)
				if assetErr != nil {
					return nil, fmt.Errorf("lint.Run: list assets for %s: %w", expandedID, assetErr)
				}
				for _, asset := range assets {
					assetPath := string(expandedID) + "/" + asset.Path
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

// checkCuratedIndex verifies both directions of an opted-in curated index.
// folder is passed to ReadIndex; indexPath is the exact physical base used by
// ExtractLinks so relative Markdown links resolve from the right directory.
func checkCuratedIndex(k *kb.KB, folder, indexPath string, candidates []okf.ConceptID, validateLinks bool, findings *[]Finding) {
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
	for _, target := range kb.ExtractLinks(body, indexPath) {
		targets[target] = true
		if validateLinks {
			if _, readErr := k.ReadConcept(target); errors.Is(readErr, okf.ErrNotFound) {
				*findings = append(*findings, Finding{
					Path:     indexPath,
					Check:    "broken_link",
					Severity: SevWarning,
					Message:  fmt.Sprintf("broken link to %s", okf.IDToPath(target)),
				})
			}
		}
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
