package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// toolPRStatus exposes the durable, non-secret review boundary state.
func toolPRStatus(k *kb.KB) Tool {
	return Tool{Name: "pr_status", ReadOnly: true,
		Description: "Returns the server Git profile, dedicated branches, current pull request identity and last forge error. Advanced operator tool.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			_ = k.ReconcileServerPR()
			state := k.ServerGitStatus()
			out, _ := json.MarshalIndent(state, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// toolPRFinalize is deliberately separate from normal agent writes: it is an
// explicit operator action that checks the caller's observed PR head.
func toolPRFinalize(k *kb.KB) Tool {
	return Tool{Name: "pr_finalize",
		Description: "Squash-merges the current reviewed server-profile pull request after checking the supplied remote head SHA, rebase and validation. Advanced operator tool.",
		InputSchema: json.RawMessage(`{"type":"object","required":["head_sha"],"properties":{"head_sha":{"type":"string","description":"Current remote PR head SHA observed by the operator."}}}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			// D76/WP4: flush any pending async push before touching git state
			// directly, so this handler does not race a push scheduled by a
			// preceding write.
			flushPendingPush(k, "pr_finalize")
			var p struct {
				HeadSHA string `json:"head_sha"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := k.FinalizeServerPR(ctx, p.HeadSHA); err != nil {
				return errorResult(fmt.Sprintf("pr_finalize: %v", err)), nil
			}
			return textResult("Pull request squash-merged; working branch reset for the next review cycle."), nil
		},
	}
}
