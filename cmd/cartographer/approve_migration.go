package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// migratePreApprovalInstalls is the one-time migration of D1166: a hook, skill
// or agent that a client installed before D1141 asked for approvals, and that
// the KB still serves with the same content hash, is approved as
// `cartographer approve` would, because that file already runs identically. It
// runs on the first sync of a client configuration that has never run it: the
// marker (cfg.ApprovalsMigrated, kept in the state file) is set by the same
// call and never cleared, so a later `approve revoke` is not undone. An artifact
// whose hash differs from its lock entry, or that is not in the lock, is left
// needs_approval. cfg is modified in memory; the caller saves it. It reports
// whether cfg changed.
//
// complete says that every KB was pulled: with a failed pull a KB's artifacts
// are missing from the manifests, so the migration waits for a whole run
// instead of spending its single chance.
func migratePreApprovalInstalls(w io.Writer, cfg *clientconfig.Config, lockFile provisioning.LockFile, manifests map[string]provisioning.Manifest, projections []syncProjection, complete bool) bool {
	if cfg.ApprovalsMigrated || !complete {
		return false
	}
	cfg.ApprovalsMigrated = true
	opts := provisioning.ApplyOptions{ApprovedMCP: cfg.ApprovedMCPHashes()}
	done := map[string]bool{}
	for _, p := range projections {
		lock := lockFile.ForProjection(p.Key())
		for _, a := range manifests[projectionKey(p)].Artifacts {
			key := a.Source + "\x00" + provisioning.ApprovalName(a)
			if done[key] || !provisioning.InstalledBeforeApprovals(a, lock, opts) {
				continue
			}
			source := strings.TrimPrefix(a.Source, "kb:")
			if err := cfg.ApproveMCP(source, provisioning.ApprovalName(a), a.ContentHash, time.Now()); err != nil {
				continue
			}
			done[key] = true
			fmt.Fprintf(w, "approved %s/%s [kb:%s]: installed before approvals existed, unchanged\n", a.Kind, a.Name, source)
		}
	}
	return true
}
