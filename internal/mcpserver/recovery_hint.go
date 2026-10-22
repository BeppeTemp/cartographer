package mcpserver

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// recoveryHint ends the error of a write that landed, wholly or in part, and
// that the caller must undo. Both tools it names are read tools of the agent
// profile (TestRecoveryHint_NamesAgentProfileTools). A refusal that wrote
// nothing, such as the write gate's, carries no hint: there is nothing to undo.
const recoveryHint = "to inspect or undo: concept_history(id) lists the commits, concept_read(id, rev=<sha>) shows the page as it was; restore it with concept_write using the current content_hash as if_match"

// batchOpPrefix is how WriteConceptBatch names the operation an error is about.
var batchOpPrefix = regexp.MustCompile(`^batch op (\d+) \(`)

// batchWriteError is the text of a failed WriteConceptBatch: a stale_write
// carries the current hash like the other writes do, and a rollback that
// itself failed carries the recovery hint, because the tree may hold part of
// the batch.
func batchWriteError(k *kb.KB, ops []kb.BatchWriteOp, err error) string {
	if errors.Is(err, okf.ErrStaleWrite) {
		msg := "stale_write: " + err.Error()
		if m := batchOpPrefix.FindStringSubmatch(err.Error()); m != nil {
			if i, convErr := strconv.Atoi(m[1]); convErr == nil && i < len(ops) {
				msg += staleConceptSuffix(k, string(ops[i].ID))
			}
		}
		return msg
	}
	msg := "concept_batch: " + err.Error()
	if strings.Contains(msg, kb.RollbackFailedMarker) {
		msg += "; " + recoveryHint
	}
	return msg
}
