package mcpserver

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

var revertSHARe = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// repairCommitReasonPrefix is the Reason trailer the background repair writes
// (autorepair.go); a hand-run kb_repair commit is recognised by its subject.
const repairCommitReasonPrefix = "auto-repair"

// isRepairCommit says whether a commit is one repair_revert may undo: one made
// by kb_repair (subject "kb_repair: ...") or by the background repair (Reason
// "auto-repair ..."). Anything else — a concept written by an agent, a merge,
// a revert — is refused, so the tool cannot become a way to roll back
// arbitrary history under a write token.
func isRepairCommit(c gitx.CommitSummary) bool {
	return strings.HasPrefix(c.Subject, "kb_repair") || strings.HasPrefix(c.Reason, repairCommitReasonPrefix)
}

// toolRepairRevert undoes one automatic repair as a new commit (D323). The
// handler only stages the inverse change: gitWrap holds the lock, commits and
// syncs, so the revert is an ordinary write.
func toolRepairRevert(k *kb.KB) Tool {
	return Tool{
		Name:        "repair_revert",
		Description: "Reverts one kb_repair or auto-repair commit by sha, as a new commit. Refuses any other commit.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["sha"],
			"properties": {"sha": {"type": "string"}}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				SHA string `json:"sha"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			sha := strings.ToLower(strings.TrimSpace(params.SHA))
			if !revertSHARe.MatchString(sha) {
				return errorResult("repair_revert: sha must be 7 to 40 hex characters"), nil
			}
			if !gitx.IsRepo(k.Root) {
				return errorResult("repair_revert: this KB is not a git repository"), nil
			}
			full, err := gitx.ResolveCommit(k.Root, sha)
			if err != nil {
				return errorResult("repair_revert: " + err.Error()), nil
			}
			if onHead, err := gitx.IsAncestor(k.Root, full, "HEAD"); err != nil || !onHead {
				return errorResult("repair_revert: that commit is not in this KB's current history"), nil
			}
			c, err := gitx.ShowCommit(k.Root, full)
			if err != nil {
				return errorResult("repair_revert: " + err.Error()), nil
			}
			if c.Parents != 1 {
				return errorResult("repair_revert: a merge or root commit cannot be reverted"), nil
			}
			if !isRepairCommit(c) {
				return errorResult(fmt.Sprintf("repair_revert: %.7s is not a kb_repair or auto-repair commit (subject %q); only those can be reverted here", full, c.Subject)), nil
			}
			if status, err := gitx.Status(k.Root); err != nil || strings.TrimSpace(status) != "" {
				return errorResult("repair_revert: the KB has uncommitted changes; retry once they are committed"), nil
			}
			if err := gitx.RevertNoCommit(k.Root, full, k.GitEnv...); err != nil {
				// The tree was clean above: dropping the half-applied revert
				// loses nothing.
				_ = gitx.ResetHardTo(k.Root, "HEAD", k.GitEnv...)
				return errorResult(fmt.Sprintf("repair_revert: %.7s cannot be undone cleanly, a later change touched the same files: fix them by hand (%v)", full, err)), nil
			}
			if status, _ := gitx.Status(k.Root); strings.TrimSpace(status) == "" {
				return errorResult(fmt.Sprintf("repair_revert: %.7s changes nothing any more", full)), nil
			}
			out, _ := json.MarshalIndent(map[string]string{"reverted": full, "subject": c.Subject}, "", "  ")
			res := textResult(string(out))
			res.CommitSubject = fmt.Sprintf("repair_revert: %.7s (%s)", full, c.Subject)
			return res, nil
		},
	}
}
