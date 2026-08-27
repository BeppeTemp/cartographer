package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

const genericNotFound = "not found"

const (
	resourceCollection   = "collection"
	resourceExact        = "exact-concept"
	resourceMove         = "source-destination"
	resourceWhole        = "whole-kb"
	resourceCuratedIndex = "curated-index"
	resourceBatch        = "multi-concept-batch"
)

// resourceClassForTool is an exhaustive registry inventory. Keeping it next
// to the policy resolver makes a new tool fail closed at registration until
// its resource semantics are deliberately chosen.
func resourceClassForTool(name string) string {
	switch name {
	case "atlas_overview", "changes_since", "map_list", "concept_list", "graph_neighbors", "search", "contradiction_report", "service_list":
		return resourceCollection
	case "concept_read", "concept_write", "concept_new", "concept_patch", "concept_expand", "concept_delete", "supersede", "asset_read", "asset_list", "asset_write", "asset_delete", "service_get":
		return resourceExact
	case "concept_move":
		return resourceMove
	case "concept_batch":
		return resourceBatch
	case "index_patch":
		return resourceCuratedIndex
	case "index_get", "log_tail", "conflicts_list", "skill_list", "artifact_list", "template_list", "map_create", "map_delete", "log_append", "snapshot", "commit_gate", "conflict_resolve", "git_conflict_resolve", "sync_status", "sync_check", "sync_apply", "sync_pull", "reindex", "kb_status", "pr_status", "pr_finalize", "secret_resolve", "secret_set", "skill_install", "artifact_read", "artifact_write", "artifact_delete", "validate", "lint", "gate_check":
		return resourceWhole
	default:
		return ""
	}
}

// installPolicy binds a KB's immutable data-plane descriptors to the central
// dispatch gate. It performs no I/O until a tool is actually called.
func installPolicy(s *Server, k *kb.KB) {
	s.SetAuthorizer(func(ctx context.Context, tool string, args json.RawMessage) error {
		p := auth.PrincipalFromContext(ctx)
		if p.ID != "" && p.Policy.Admin {
			return nil
		}
		name := s.PolicyKB()
		if name == "" {
			name = kbName(k)
		}
		if tool == "" {
			if p.Policy.HasKBAccess(name, false) {
				return nil
			}
			return errors.New("forbidden")
		}
		return authorizeTool(p.Policy, k, name, tool, args)
	})
}

// reauthorizeUnderLock repeats the same decision immediately before a
// mutation. A changed concept classification between dispatch and git lock
// therefore cannot turn a previously authorized request into a write.
func reauthorizeUnderLock(ctx context.Context, k *kb.KB, tool string, args json.RawMessage) error {
	p := auth.PrincipalFromContext(ctx)
	if p.ID != "" && p.Policy.Admin {
		return nil
	}
	return authorizeTool(p.Policy, k, kbName(k), tool, args)
}

func authorizeTool(policy auth.Policy, k *kb.KB, name, tool string, args json.RawMessage) error {
	write := ToolRequiresWrite(tool)
	if resourceClassForTool(tool) == "" {
		return errors.New("forbidden")
	}
	// Whole-KB tools have no safe partial semantics. The list is deliberately
	// conservative; adding an unclassified tool can only deny a restricted caller.
	switch tool {
	case "index_get", "log_tail", "conflicts_list", "skill_list", "artifact_list", "template_list", "map_create", "map_delete", "log_append", "snapshot", "commit_gate", "conflict_resolve", "git_conflict_resolve", "sync_status", "sync_check", "sync_apply", "sync_pull", "skill_install", "artifact_read", "artifact_write", "artifact_delete", "secret_set", "secret_resolve", "pr_finalize", "reindex", "kb_status", "pr_status", "validate", "lint", "gate_check":
		if policy.AllowsWholeKB(name, write) {
			return nil
		}
		return errors.New("forbidden")
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return errors.New("forbidden")
	}
	if resourceClassForTool(tool) == resourceCollection && !policy.HasKBAccess(name, false) {
		return errors.New("forbidden")
	}
	if tool == "service_get" {
		var serviceArgs struct {
			ResolveSecrets bool `json:"resolve_secrets"`
		}
		if json.Unmarshal(args, &serviceArgs) != nil || (serviceArgs.ResolveSecrets && !policy.AllowsWholeKB(name, true)) {
			return errors.New("forbidden")
		}
	}
	if tool == "graph_neighbors" {
		var graphArgs struct {
			Depth int `json:"depth"`
		}
		_ = json.Unmarshal(args, &graphArgs)
		if graphArgs.Depth > 1 && !policy.AllowsWholeKB(name, false) {
			return errors.New("forbidden")
		}
	}
	if tool == "concept_new" {
		id := stringField(raw, "id")
		proposedType, ok := templateType(k, stringField(raw, "template"))
		if !ok || !allowedID(policy, k, name, id, true, proposedType) {
			return errors.New("forbidden")
		}
		return nil
	}
	// Explicit multi-resource moves must be completely authorized before the
	// git wrapper can synchronize or mutate anything.
	if tool == "concept_move" {
		var moves struct {
			SourceID string `json:"source_id"`
			TargetID string `json:"target_id"`
			Moves    []struct {
				SourceID string `json:"source_id"`
				TargetID string `json:"target_id"`
			} `json:"moves"`
			RewriteLinks *bool `json:"rewrite_links"`
		}
		if json.Unmarshal(args, &moves) != nil {
			return errors.New("forbidden")
		}
		// Link rewriting walks and writes arbitrary inbound concepts. A bounded
		// source/destination grant is sufficient only when it is disabled.
		if (moves.RewriteLinks == nil || *moves.RewriteLinks) && !policy.AllowsWholeKB(name, true) {
			return errors.New("forbidden")
		}
		pairs := moves.Moves
		if len(pairs) == 0 {
			pairs = append(pairs, struct {
				SourceID string `json:"source_id"`
				TargetID string `json:"target_id"`
			}{moves.SourceID, moves.TargetID})
		}
		if len(pairs) == 0 {
			return errors.New("forbidden")
		}
		for _, pair := range pairs {
			if !allowedMove(policy, k, name, pair.SourceID, pair.TargetID) {
				return errors.New("forbidden")
			}
		}
		return nil
	}
	// concept_batch addresses several distinct exact concepts at once
	// (D125): every id in "operations" must individually pass allowedID
	// before the batch can proceed, and a denied id rejects the whole call
	// without disclosing which one — same non-disclosure guarantee as a
	// single concept_write, just applied to the full explicit set up front.
	if tool == "concept_batch" {
		var batchArgs struct {
			Operations []json.RawMessage `json:"operations"`
		}
		if json.Unmarshal(args, &batchArgs) != nil || len(batchArgs.Operations) == 0 {
			return errors.New("forbidden")
		}
		for _, opRaw := range batchArgs.Operations {
			var opFields map[string]json.RawMessage
			if json.Unmarshal(opRaw, &opFields) != nil {
				return errors.New("forbidden")
			}
			id := stringField(opFields, "id")
			proposedType := frontmatterType(opFields["frontmatter"])
			if !allowedID(policy, k, name, id, true, proposedType) {
				return errors.New("forbidden")
			}
		}
		return nil
	}
	// index_patch addresses a bounded root/Map/Journal resource, not a
	// concept: it is authorized against that resource directly rather than
	// falling through to the "id" field logic below, which expects a
	// ConceptID. The root index has no map/journal of its own — it affects
	// the whole KB's atlas view — so it requires the same unscoped write
	// grant as the resourceWhole tools above; a Map/Journal path is checked
	// against that single collection, same as allowedMove's destination.
	if tool == "index_patch" {
		var indexArgs struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(args, &indexArgs) != nil {
			return errors.New("forbidden")
		}
		if normalizeIndexPath(indexArgs.Path) == "" {
			if policy.AllowsWholeKB(name, true) {
				return nil
			}
			return errors.New("forbidden")
		}
		mapName, journalName := normalizeIndexPath(indexArgs.Path), ""
		if meta, err := k.ReadArchiveMeta(mapName); err == nil {
			if kind, _ := meta.Get("kind"); kind == "journal" {
				mapName, journalName = "", mapName
			}
		}
		if policy.Allows(name, mapName, journalName, "", true) {
			return nil
		}
		return errors.New("forbidden")
	}
	if tool == "supersede" {
		var relation struct {
			SourceID string `json:"source_id"`
			TargetID string `json:"target_id"`
		}
		if json.Unmarshal(args, &relation) != nil || !allowedID(policy, k, name, relation.SourceID, true, "") || !allowedID(policy, k, name, relation.TargetID, true, "") {
			return errors.New("forbidden")
		}
		return nil
	}

	// Exact concept operations share the conventional id/concept_id/service_id
	// fields. Missing or forbidden resources intentionally collapse to not found.
	id := stringField(raw, "id", "concept_id", "service_id")
	if id == "" && resourceClassForTool(tool) == resourceExact && write {
		return errors.New("forbidden")
	}
	if id != "" {
		proposedType := frontmatterType(raw["frontmatter"])
		if !allowedID(policy, k, name, id, write, proposedType) {
			return errors.New(genericNotFound)
		}
		if !write && !conceptExists(k, id) {
			return errors.New(genericNotFound)
		}
		return nil
	}
	if tool == "asset_read" || tool == "asset_list" || tool == "asset_write" || tool == "asset_delete" {
		id = stringField(raw, "concept_id")
		if !allowedID(policy, k, name, id, write, "") {
			return errors.New(genericNotFound)
		}
		return nil
	}
	// A caller may only request an explicitly scoped collection inside an
	// allowed map/journal. Unscoped collection reads are filtered by Visible.
	if scope := stringField(raw, "scope", "map", "journal"); scope != "" && !policy.Allows(name, scope, "", "", false) && !policy.Allows(name, "", scope, "", false) {
		return errors.New("forbidden")
	}
	return nil
}

func conceptExists(k *kb.KB, id string) bool {
	conceptID, err := okf.PathToID(id + ".md")
	if err != nil {
		return false
	}
	_, err = k.ReadConcept(conceptID)
	return err == nil
}

func kbName(k *kb.KB) string {
	if k.AuthName != "" {
		return k.AuthName
	}
	parts := strings.Split(strings.TrimRight(k.Root, "/"), "/")
	return parts[len(parts)-1]
}

func stringField(raw map[string]json.RawMessage, names ...string) string {
	for _, name := range names {
		var value string
		if b := raw[name]; len(b) > 0 && json.Unmarshal(b, &value) == nil {
			return value
		}
	}
	return ""
}

func frontmatterType(raw json.RawMessage) string {
	var values map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &values) != nil {
		return ""
	}
	v, _ := values["type"].(string)
	return v
}

// templateType resolves the actual type that concept_new will write. A
// template with an unreadable, missing, or dynamically substituted type is
// denied to scoped callers rather than being authorized as an untyped create.
func templateType(k *kb.KB, slug string) (string, bool) {
	if slug == "" || strings.Contains(slug, "/") || strings.Contains(slug, "\\") {
		return "", false
	}
	path, err := k.ResolveRootPath("templates/" + slug + ".md")
	if err != nil {
		return "", false
	}
	contentBytes, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	content := string(contentBytes)
	fmRaw, _, ok := okf.SplitFrontmatter(content)
	if !ok {
		return "", false
	}
	fm, err := okf.ParseFrontmatter(fmRaw)
	if err != nil {
		return "", false
	}
	t, ok := fm.Get("type")
	typeName, ok := t.(string)
	return typeName, ok && typeName != "" && !strings.Contains(typeName, "{{")
}

// Visible is the reusable collection predicate. It never treats an unreadable
// concept as visible, avoiding an error side channel for restricted callers.
func Visible(ctx context.Context, k *kb.KB, id string) bool {
	p := auth.PrincipalFromContext(ctx)
	if p.ID != "" && p.Policy.Admin {
		return true
	}
	return allowedID(p.Policy, k, kbName(k), id, false, "")
}

func VisibleCollection(ctx context.Context, k *kb.KB, name, kind string) bool {
	p := auth.PrincipalFromContext(ctx)
	if p.ID != "" && p.Policy.Admin {
		return true
	}
	if kind == "journal" {
		return p.Policy.Allows(kbName(k), "", name, "", false)
	}
	return p.Policy.Allows(kbName(k), name, "", "", false)
}

func WholeVisible(ctx context.Context, k *kb.KB, write bool) bool {
	p := auth.PrincipalFromContext(ctx)
	return p.ID != "" && (p.Policy.Admin || p.Policy.AllowsWholeKB(kbName(k), write))
}

// allowedMove requires access to both the source and the destination. The
// destination does not exist yet, so it inherits the source concept's type.
func allowedMove(policy auth.Policy, k *kb.KB, name, sourceID, targetID string) bool {
	if !allowedID(policy, k, name, sourceID, true, "") || targetID == "" {
		return false
	}
	_, _, typ := conceptClass(k, sourceID)
	mapName, journalName, _ := conceptClass(k, targetID)
	return policy.Allows(name, mapName, journalName, typ, true)
}

func allowedID(policy auth.Policy, k *kb.KB, name, id string, write bool, proposedType string) bool {
	if id == "" {
		return false
	}
	mapName, journalName, oldType := conceptClass(k, id)
	if proposedType != "" {
		if !policy.Allows(name, mapName, journalName, proposedType, write) {
			return false
		}
		if !conceptExists(k, id) {
			return true
		}
	}
	return policy.Allows(name, mapName, journalName, oldType, write)
}

func conceptClass(k *kb.KB, id string) (mapName, journalName, typ string) {
	parts := strings.Split(id, "/")
	if len(parts) > 1 {
		mapName = parts[0]
		if meta, err := k.ReadArchiveMeta(mapName); err == nil {
			if kind, _ := meta.Get("kind"); kind == "journal" {
				journalName, mapName = mapName, ""
			}
		}
	}
	if conceptID, err := okf.PathToID(id + ".md"); err == nil {
		if c, err := k.ReadConcept(conceptID); err == nil {
			if fm, _, ok := okf.SplitFrontmatter(c.Content); ok {
				if parsed, err := okf.ParseFrontmatter(fm); err == nil {
					if t, ok := parsed.Get("type"); ok {
						typ, _ = t.(string)
					}
				}
			}
		}
	}
	return
}
