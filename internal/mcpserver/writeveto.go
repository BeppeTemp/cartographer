package mcpserver

import (
	"context"
	"encoding/json"

	"github.com/BeppeTemp/cartographer/internal/hooks"
)

// SetHooks attaches the registry of the hooks wired in the server config. A nil
// or empty registry leaves the server exactly as without hooks.
func (s *Server) SetHooks(r *hooks.Registry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hooks = r
}

// vetoWrite asks every write_veto hook that covers the KB, in config order,
// whether it objects to the call, and stops at the first deny. It runs from
// callToolChecked after the core authorization (a hook is never called for a
// request the core refused, the admin included) and BEFORE the handler, so
// before gitWrap takes the KB lock: a slow hook delays this request and no
// other writer. Never call it, or anything of internal/hooks, from gitWrap,
// reauthorizeUnderLock or a function running under k.WithGitLock
// (TestNoHookUnderTheLock). The veto judges the request, not the final diff;
// the content-aware check is the write gate (judgeWrite), which is the core's.
//
// It returns the audit resources to add (the hook and its verdict) and the
// denial to answer with, nil when the write may go on. The payload carries the
// audit's allow-listed resource identifiers and the caller's reason, never
// frontmatter, a body, a patch value or a secret name (hook version 1).
func (s *Server) vetoWrite(ctx context.Context, reg *hooks.Registry, kbName, tool string, args json.RawMessage, resources map[string]string) (map[string]string, error) {
	list := reg.ForPoint(hooks.PointWriteVeto)
	if len(list) == 0 {
		return nil, nil
	}
	var reason struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(args, &reason)
	fields := map[string]any{
		"principal": hookPrincipal(ctx),
		"tool":      tool,
		"class":     "write",
		"target":    resources,
		"reason":    reason.Reason,
	}
	var extra map[string]string
	for _, kbn := range vetoKBs(kbName, tool, args) {
		for i := range list {
			h := &list[i]
			if !h.AppliesTo(kbn) {
				continue
			}
			out := reg.Decide(ctx, h.Name, hooks.Request{KB: kbn, Fields: fields})
			if verdictRank(out.Verdict) > verdictRank(extra["hook_verdict"]) {
				extra = map[string]string{"hook": h.Name, "hook_verdict": out.Verdict}
			}
			if out.Decision != hooks.Deny {
				continue
			}
			if out.Unavailable {
				return extra, deny(denyHookUnavailable)
			}
			hint := denyHints[denyDeniedByHook]
			if out.Reason != "" {
				hint += ": " + out.Reason
			}
			return extra, deny(denyDeniedByHook, hint)
		}
	}
	return extra, nil
}

// withAuditResources returns resources plus extra, without touching either map.
func withAuditResources(resources, extra map[string]string) map[string]string {
	if len(extra) == 0 {
		return resources
	}
	out := make(map[string]string, len(resources)+len(extra))
	for k, v := range resources {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// vetoKBs lists the KBs a write lands in: the source KB, and for the tools that
// write into another KB (concept_transfer, artifact_move) the target_kb too. A
// hook scoped to the target must be asked when content moves into it; a deny from
// either side denies.
func vetoKBs(kbName, tool string, args json.RawMessage) []string {
	out := []string{kbName}
	if tool != "concept_transfer" && tool != "artifact_move" {
		return out
	}
	var a struct {
		TargetKB string `json:"target_kb"`
	}
	if json.Unmarshal(args, &a) == nil && a.TargetKB != "" && a.TargetKB != kbName {
		out = append(out, a.TargetKB)
	}
	return out
}

// verdictRank orders what the audit keeps when several hooks record a verdict:
// deny, then dry_run_deny, then error.
func verdictRank(v string) int {
	switch v {
	case "deny":
		return 3
	case "dry_run_deny":
		return 2
	case "error":
		return 1
	}
	return 0
}
