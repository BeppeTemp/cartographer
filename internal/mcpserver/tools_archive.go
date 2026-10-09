package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// archiveTarget derives the target ID of a concept_archive: the last segment of
// id, filed under the archive map `to`.
func archiveTarget(id, to string) string {
	return to + "/" + id[strings.LastIndex(id, "/")+1:]
}

// toolConceptArchive retires a page in one commit (D344): status, move, inbound
// link rewrite and removal of its entry from the source map's curated index.
// By hand this is four calls, and the first one (patching the status while the
// page is still in its live map) makes link_to_retired fire on it.
func toolConceptArchive(k *kb.KB) Tool {
	return Tool{
		Name: "concept_archive",
		Description: "Retires a concept in one commit: sets its status (deprecated, or archived), moves it to the archive map (default archive) and rewrites inbound links KB-wide. " +
			"Removes its entry from the source map's index, unless that index is generated or the line cites other concepts (reported in curated_indexes). " +
			"reason goes to the log. if_match is the source's content hash. Refuses when the target exists. Returns findings.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["id", "if_match"],
			"properties": {
				"id": {"type": "string", "description": "ConceptID to retire"},
				"to": {"type": "string", "description": "Destination map or journal, one segment (default archive)"},
				"status": {"type": "string", "enum": ["deprecated", "archived"], "description": "Default deprecated"},
				"reason": {"type": "string"},
				"if_match": {"type": "string", "description": "Expected content-hash of the source"}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				ID      string `json:"id"`
				To      string `json:"to"`
				Status  string `json:"status"`
				Reason  string `json:"reason"`
				IfMatch string `json:"if_match"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if params.ID == "" {
				return errorResult("'id' is required"), nil
			}
			if params.IfMatch == "" {
				return errorResult("'if_match' is required"), nil
			}
			to := params.To
			if to == "" {
				to = "archive"
			}
			if strings.Contains(to, "/") {
				return errorResult(fmt.Sprintf("concept_archive: 'to' must be one map name, got %q", to)), nil
			}
			status := params.Status
			if status == "" {
				status = "deprecated"
			}
			if status != "deprecated" && status != kb.StatusArchived {
				return errorResult(fmt.Sprintf("concept_archive: status must be deprecated or archived, got %q", status)), nil
			}
			archives, err := k.ListArchives()
			if err != nil {
				return errorResult(fmt.Sprintf("concept_archive: list maps: %v", err)), nil
			}
			if !slices.Contains(archives, to) {
				return errorResult(fmt.Sprintf("concept_archive: unknown map %q (create it with map_create first)", to)), nil
			}
			if srcMap, ok := conceptMapName(params.ID); ok && srcMap == to {
				return errorResult(fmt.Sprintf("concept_archive: %q is already in %s", params.ID, to)), nil
			}
			target := archiveTarget(params.ID, to)

			// Preflight: nothing is written until the move is known to be valid.
			data, err := k.ReadConcept(okf.ConceptID(params.ID))
			if err != nil {
				return errorResult(fmt.Sprintf("concept_archive: read %q: %v", params.ID, err)), nil
			}
			if data.ContentHash != params.IfMatch {
				return errorResult(fmt.Sprintf("stale_write: concept_archive %q: if_match %q does not match %q", params.ID, params.IfMatch, data.ContentHash)), nil
			}
			srcLoc, err := k.LocateConcept(okf.ConceptID(params.ID))
			if err != nil {
				return errorResult(fmt.Sprintf("concept_archive: resolve %q: %v", params.ID, err)), nil
			}
			if srcLoc.Expanded && len(strings.Split(params.ID, "/")) != 2 {
				return errorResult("expanded concept moves require a two-segment id"), nil
			}
			targetLoc, err := k.LocateConcept(okf.ConceptID(target))
			if err != nil {
				if errors.Is(err, okf.ErrInvalidPath) {
					return errorResult("target resolves outside KB root: " + target), nil
				}
				return errorResult(fmt.Sprintf("concept_archive: resolve target %q: %v", target, err)), nil
			}
			if msg := targetOccupied(k, target, targetLoc); msg != "" {
				if strings.HasPrefix(msg, "conflict:") {
					msg += " (to rename instead, use concept_move)"
				}
				return errorResult(msg), nil
			}
			fm, err := okf.ParseFrontmatter(data.FrontmatterRaw)
			if err != nil {
				return errorResult(fmt.Sprintf("concept_archive: parse frontmatter %q: %v", params.ID, err)), nil
			}

			// The status is written before the move, in the same gitWrap commit:
			// an application error from here on leaves it uncommitted, with the
			// same no-rollback contract as concept_move.
			if cur, _ := fm.Get("status"); cur != status {
				fm.Set("status", status)
				if _, err := k.WriteConcept(okf.ConceptID(params.ID), fm, data.Body, params.IfMatch); err != nil {
					if strings.Contains(err.Error(), "stale") {
						return errorResult(fmt.Sprintf("stale_write: concept_archive %q: %v", params.ID, err)), nil
					}
					return errorResult(fmt.Sprintf("concept_archive: write status of %q: %v", params.ID, err)), nil
				}
			}

			detail := status
			if params.Reason != "" {
				detail += ", " + params.Reason
			}
			result, errRes := applyConceptMoves(k, []conceptMoveEntry{{SourceID: params.ID, TargetID: target}}, true, moveOptions{
				ForceSourceIndexRemoval: true,
				LogTitle:                fmt.Sprintf("concept_archive: %s -> %s (%s)", params.ID, target, detail),
			})
			if errRes != nil {
				return *errRes, nil
			}
			result["status"] = status
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}
