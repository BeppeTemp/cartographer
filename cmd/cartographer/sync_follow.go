package main

import (
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// What a sync that follows the server reports: the KBs it added and removed
// for the providers that follow it, and what of an arrival waits for approval.
// None of this approves anything (D1141, D906).

// followingProviders lists the targeted providers that follow the server: the
// all marker and the default (no binding), as BoundKBs reports them (explicit
// false). An explicit list or a workspace list never changes with the server.
func followingProviders(cfg *clientconfig.Config, targets []string) []string {
	var out []string
	for _, p := range targets {
		if _, explicit := cfg.BoundKBs(p); !explicit {
			out = append(out, p)
		}
	}
	return out
}

// kbDiff is what a sync added to and removed from the mounted KBs.
func kbDiff(previous, current []string) (added, removed []string) {
	for _, kb := range current {
		if !slices.Contains(previous, kb) {
			added = append(added, kb)
		}
	}
	for _, kb := range previous {
		if !slices.Contains(current, kb) {
			removed = append(removed, kb)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

// printKBFollow says, per KB, what this sync did for the providers that follow
// the server. Nothing when no targeted provider follows it.
func printKBFollow(w io.Writer, added, removed, followers []string, dryRun bool) {
	if len(followers) == 0 {
		return
	}
	who := escapeControl(strings.Join(followers, ", "))
	for _, kb := range added {
		if dryRun {
			fmt.Fprintf(w, "[dry-run] would add %s for %s: it is mounted now\n", escapeControl(kb), who)
		} else {
			fmt.Fprintf(w, "%s mounted: added for %s\n", escapeControl(kb), who)
		}
	}
	for _, kb := range removed {
		if dryRun {
			fmt.Fprintf(w, "[dry-run] would remove %s for %s: it is not mounted any more\n", escapeControl(kb), who)
		} else {
			fmt.Fprintf(w, "%s not mounted any more: removed for %s\n", escapeControl(kb), who)
		}
	}
}

// printArrivalApprovals names, for each KB this sync added, the artifacts of it
// that wait for `cartographer approve`: the needs_approval entries the apply
// summary prints, counted once per artifact across providers.
func printArrivalApprovals(w io.Writer, results map[string]provisioning.AppliedResult, added []string) {
	for _, kb := range added {
		var cmds []string
		for _, key := range sortedKeys(results) {
			for _, na := range results[key].NeedsApproval {
				if na.Source != "kb:"+kb {
					continue
				}
				cmd := fmt.Sprintf("cartographer approve %s %s --kb %s", escapeControl(na.Kind), escapeControl(na.Name), escapeControl(kb))
				if !slices.Contains(cmds, cmd) {
					cmds = append(cmds, cmd)
				}
			}
		}
		if len(cmds) > 0 {
			fmt.Fprintf(w, "%s: %d artifact(s) need approval: %s\n", escapeControl(kb), len(cmds), strings.Join(cmds, "; "))
		}
	}
}

// kbChangeRecord is the state's kb_change for a sync that took known_kbs from
// previous to current, nil when it did not change.
func kbChangeRecord(previous, current []string, now time.Time) *clientconfig.KBChange {
	added, removed := kbDiff(previous, current)
	if len(added)+len(removed) == 0 {
		return nil
	}
	return &clientconfig.KBChange{At: now.UTC().Format(time.RFC3339), Added: added, Removed: removed}
}
