package mcpserver

import (
	"fmt"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// staleRetryHint ends the stale_write of a concept or an index page: what to
// re-read, with which tool.
func staleRetryHint(what, tool string) string {
	return fmt.Sprintf("re-read the %s (%s) and retry with that hash, or merge your edit onto it", what, tool)
}

// staleConceptSuffix is what a stale_write on a concept appends to its bare
// cause: the hash to retry with and, in a git KB, the newest commit that
// touched the page with its Reason (an auto-repair commit shows as such).
// Read-only lookups, never a lock, a commit or a sync (the handler runs
// under gitWrap's): a failed lookup, or a concept that does not exist (the
// "file not found" stale_write has no current hash), degrades to "", the
// text the error had before.
func staleConceptSuffix(k *kb.KB, id string) string {
	data, err := k.ReadConcept(okf.ConceptID(id))
	if err != nil {
		return ""
	}
	out := "; current content_hash " + data.ContentHash
	if gitx.IsRepo(k.Root) {
		if path, err := conceptGitPath(k, id); err == nil {
			if revs, err := gitx.FileHistory(k.Root, path, 1); err == nil && len(revs) == 1 {
				out += fmt.Sprintf("; last changed by %s %q", shortSHA(revs[0].SHA), revs[0].Subject)
				if revs[0].Reason != "" {
					out += ", reason: " + revs[0].Reason
				}
			}
		}
	}
	return out + "; " + staleRetryHint("concept", "concept_read")
}

// staleIndexSuffix is staleConceptSuffix for an index page, hash only.
func staleIndexSuffix(k *kb.KB, path string) string {
	_, hash, err := k.IndexHash(path)
	if err != nil {
		return ""
	}
	return "; current content_hash " + hash + "; " + staleRetryHint("index", "index_get")
}
