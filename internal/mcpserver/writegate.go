package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
)

// The write gate (D350), fixed behaviour since D907: no setting turns it off
// or lowers it, and nothing accepts a finding. One enforcement point in
// gitWrap, not per tool: every tool that writes concepts goes through it, and
// the working-tree diff says what changed whatever the tool. After the safe
// fixes a write applies to what it wrote (D349), a write that introduces a
// finding it could have avoided (lint.CheckSpec.AvoidableAtWrite), at any
// severity, is rolled back to HEAD and refused with each finding and its fix.
// Findings the concepts already had never block a repair, and a check no
// write can avoid (time, growth) is left to the doctor.

// gateStashPop is the stash restore of the baseline step, a variable only so a
// test can make it fail.
var gateStashPop = gitx.StashPop

// gatedTool says whether a wrapped tool goes through the gate: the concept
// writers by name prefix (so concept_archive and later concept_* tools need no
// list to edit) and supersede. kb_repair, repair_revert, conflict_resolve,
// log_append, snapshot, map_*, index_patch and source_register are exempt by
// construction.
func gatedTool(name string) bool {
	// concept_transfer commits two KBs itself and reports findings per KB (D760).
	return (strings.HasPrefix(name, "concept_") && name != "concept_transfer") || name == "supersede"
}

// writeGateActive says whether the gate can judge a write on this KB: a
// refusal rolls the tree back to HEAD, so it needs a git repository. A KB that
// is not one commits nothing either (D903), and its writes report findings
// without a gate.
func writeGateActive(k *kb.KB) bool { return gitx.IsRepo(k.Root) }

// avoidableFindings keeps the findings a write could have avoided (D907), in
// lint's deterministic order, whatever their severity.
func avoidableFindings(findings []lint.Finding) []lint.Finding {
	all, _, _ := lint.Filter(findings, lint.SevInfo)
	var out []lint.Finding
	for _, f := range all {
		if s, ok := lint.Spec(f.Check); ok && s.AvoidableAtWrite {
			out = append(out, f)
		}
	}
	return out
}

// gatePrecheck decides, before the handler runs, whether this call can be
// gated: the tree must be clean (rolling back would destroy foreign changes)
// and HEAD must exist. It prints one stderr line when it skips.
func gatePrecheck(k *kb.KB) bool {
	if gitx.HeadUnborn(k.Root) {
		fmt.Fprintln(os.Stderr, "cartographer: write_gate skipped: repository has no commit yet")
		return false
	}
	status, err := gitx.Status(k.Root)
	if err != nil || strings.TrimSpace(status) != "" {
		fmt.Fprintln(os.Stderr, "cartographer: write_gate skipped: working tree not clean before write")
		return false
	}
	return true
}

type gateKey struct{ path, check, message string }

// judgeWrite is the gate proper. It runs after the handler, the generated
// index regeneration and the repair-on-write (D349), so what it judges is what
// would be committed. It fails open (nil) on a git problem that leaves the
// tree intact, and rolls back on a refusal, which it returns.
func judgeWrite(k *kb.KB, toolName string) *ToolResult {
	changes, err := gitx.StagedChanges(k.Root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cartographer: write_gate skipped (%s): %v\n", toolName, err)
		return nil
	}
	var written, gone, baseIDs []string
	renamedFrom := map[string]string{} // new concept id -> old concept id
	addBase := func(id string) { baseIDs = append(baseIDs, id) }
	for _, c := range changes {
		newID, newOK := kb.ConceptIDOfPath(c.Path)
		switch c.Status {
		case "D":
			if newOK {
				gone = append(gone, string(newID))
				addBase(string(newID))
			}
		case "R":
			oldID, oldOK := kb.ConceptIDOfPath(c.OldPath)
			if oldOK {
				gone = append(gone, string(oldID))
				addBase(string(oldID))
			}
			if newOK {
				written = append(written, string(newID))
				if oldOK {
					renamedFrom[string(newID)] = string(oldID)
				}
			}
		case "A":
			if newOK {
				written = append(written, string(newID))
			}
		default:
			if newOK {
				written = append(written, string(newID))
				addBase(string(newID))
			}
		}
	}
	if len(written) == 0 && len(gone) == 0 {
		return nil
	}
	post := avoidableFindings(writeLintFindings(k, written, gone))
	if len(post) == 0 {
		return nil
	}

	// Baseline: the same concepts at HEAD. Only paid when the write left
	// something to judge.
	if err := gitx.StashPushAll(k.Root); err != nil {
		fmt.Fprintf(os.Stderr, "cartographer: write_gate skipped (%s): %v\n", toolName, err)
		return nil
	}
	base := avoidableFindings(writeLintFindings(k, baseIDs, nil))
	if err := gateStashPop(k.Root); err != nil {
		fmt.Fprintf(os.Stderr, "cartographer: write_gate: restoring the write failed (%s): %v\n", toolName, err)
		_ = gitx.StashDrop(k.Root)
		discard(k, toolName)
		res := errorResult("write_gate: could not restore the write, nothing was written")
		return &res
	}
	pre := map[gateKey]bool{}
	for _, f := range base {
		pre[gateKey{f.Path, f.Check, f.Message}] = true
	}
	oldPath := map[string]string{} // finding path of a renamed concept -> its old path
	for newID, oldID := range renamedFrom {
		oldPath[newID+".md"] = oldID + ".md"
	}

	var introduced []lint.Finding
	for _, f := range post {
		p := f.Path
		if op, ok := oldPath[p]; ok {
			p = op
		}
		if pre[gateKey{p, f.Check, f.Message}] {
			continue
		}
		// A finding that names the moved concept (a map's index_incomplete)
		// is the same finding under its old id.
		msg := f.Message
		for newID, oldID := range renamedFrom {
			msg = strings.ReplaceAll(msg, newID, oldID)
		}
		if !pre[gateKey{p, f.Check, msg}] {
			introduced = append(introduced, f)
		}
	}
	if len(introduced) == 0 {
		return nil
	}
	discard(k, toolName)
	body, _ := json.Marshal(findingsOut(introduced))
	res := errorResult(fmt.Sprintf("write_gate: refused, nothing was written. This call introduced %d finding(s) a write can avoid (D907):\n%s\n"+
		"Fix each one as its message and fix say, then retry; concept_batch is atomic, so a page and the link, index entry or target it needs can land in one call. A new page in a map with a curated index needs index_entry: true on its concept_new or creating concept_batch write.",
		len(introduced), body))
	return &res
}

// discard returns the tree to HEAD; a failure is logged, the refusal still goes
// out (the caller holds the KB lock and checked the tree was clean).
func discard(k *kb.KB, toolName string) {
	if err := gitx.DiscardChanges(k.Root); err != nil {
		fmt.Fprintf(os.Stderr, "cartographer: write_gate: rollback failed (%s): %v\n", toolName, err)
	}
}
