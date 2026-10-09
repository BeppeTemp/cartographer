package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
)

// The opt-in write gate (D350). One enforcement point in gitWrap, not per
// tool: every tool that writes concepts goes through it, and the working-tree
// diff says what changed whatever the tool. A write that introduces a finding
// at or above the KB's floor is rolled back to HEAD and refused; findings the
// concepts already had never block a repair.

// acceptedFindingKey is the git trailer that records an accepted exception
// (D350); gitx reads it back with the same key.
const acceptedFindingKey = "Accepted-Finding"

const acceptFindingsSchemaDescription = "[{check, reason}] accept new findings"

var acceptCheckRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// gateStashPop is the stash restore of the baseline step, a variable only so a
// test can make it fail.
var gateStashPop = gitx.StashPop

// acceptsFindings says whether a wrapped tool is gated and carries
// accept_findings: the concept writers by name prefix (so concept_archive and
// later concept_* tools need no list to edit) and supersede. kb_repair,
// repair_revert, conflict_resolve, log_append, snapshot, map_*, index_patch and
// source_register are exempt by construction.
func acceptsFindings(name string) bool {
	return strings.HasPrefix(name, "concept_") || name == "supersede"
}

// writeGateState is the capability state of the KB's write_gate: off, error or
// warning; inactive when it is set but the gate cannot roll back (it needs
// auto-commit on a git repository). An inactive gate behaves as off.
func writeGateState(k *kb.KB) string {
	switch k.WriteGate {
	case "error", "warning":
		if !k.AutoCommit || !gitx.IsRepo(k.Root) {
			return "inactive"
		}
		return k.WriteGate
	}
	return "off"
}

func writeGateActive(k *kb.KB) bool {
	s := writeGateState(k)
	return s == "error" || s == "warning"
}

// withAcceptFindingsProperty adds the optional accept_findings property to a
// tool schema. Only a KB with an active gate calls it, so a gate-off schema is
// byte-identical to what it was before D350.
func withAcceptFindingsProperty(toolName string, schema json.RawMessage) json.RawMessage {
	top := map[string]json.RawMessage{}
	if len(schema) == 0 {
		top["type"] = json.RawMessage(`"object"`)
	} else if err := json.Unmarshal(schema, &top); err != nil || top == nil {
		panic(fmt.Sprintf("mcpserver: tool %q has an unparsable input schema: %v", toolName, err))
	}
	props := map[string]json.RawMessage{}
	if raw, ok := top["properties"]; ok {
		if err := json.Unmarshal(raw, &props); err != nil || props == nil {
			panic(fmt.Sprintf("mcpserver: tool %q has unparsable schema properties: %v", toolName, err))
		}
	}
	if _, ok := props["accept_findings"]; ok {
		return schema
	}
	entry, _ := json.Marshal(map[string]any{
		"type":        "array",
		"description": acceptFindingsSchemaDescription,
		"items": map[string]any{
			"type":       "object",
			"properties": map[string]any{"check": map[string]string{"type": "string"}, "reason": map[string]string{"type": "string"}},
			"required":   []string{"check", "reason"},
		},
	})
	props["accept_findings"] = entry
	top["properties"], _ = json.Marshal(props)
	out, err := json.Marshal(top)
	if err != nil {
		panic(fmt.Sprintf("mcpserver: tool %q: re-encode input schema: %v", toolName, err))
	}
	return out
}

// acceptedFinding is one validated accept_findings entry.
type acceptedFinding struct {
	Check  string
	Reason string
}

// parseAcceptFindings reads and validates accept_findings before anything
// runs. An unknown check name is not an error: it simply matches nothing.
func parseAcceptFindings(args json.RawMessage) ([]acceptedFinding, error) {
	var p struct {
		Accept []map[string]any `json:"accept_findings"`
	}
	if err := json.Unmarshal(args, &p); err != nil || len(p.Accept) == 0 {
		return nil, nil
	}
	out := make([]acceptedFinding, 0, len(p.Accept))
	for i, e := range p.Accept {
		check, _ := e["check"].(string)
		check = strings.TrimSpace(check)
		if !acceptCheckRe.MatchString(check) {
			return nil, fmt.Errorf("accept_findings[%d]: check is required (a check name such as duplicate_link)", i)
		}
		raw, _ := e["reason"].(string)
		reason := normalizeReason(raw)
		if reason == "" {
			return nil, fmt.Errorf("accept_findings[%d]: reason is required", i)
		}
		out = append(out, acceptedFinding{Check: check, Reason: reason})
	}
	return out, nil
}

// gateOutcome is what judging a write returns: a refusal (the write was rolled
// back) or the trailer lines of the accepted findings to add to the commit.
type gateOutcome struct {
	Refusal  *ToolResult
	Trailers []string
}

// gateFloor is the lowest severity the KB's gate refuses.
func gateFloor(k *kb.KB) string {
	if k.WriteGate == "warning" {
		return lint.SevWarning
	}
	return lint.SevError
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
// index regeneration and any repair-on-write (D349), so what it judges is what
// would be committed. It fails open (nil outcome fields) on a git problem that
// leaves the tree intact, and rolls back on a refusal.
func judgeWrite(k *kb.KB, toolName string, accepts []acceptedFinding) gateOutcome {
	changes, err := gitx.StagedChanges(k.Root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cartographer: write_gate skipped (%s): %v\n", toolName, err)
		return gateOutcome{}
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
		return gateOutcome{}
	}
	floor := gateFloor(k)
	post, _, _ := lint.Filter(writeLintFindings(k, written, gone), floor)
	if len(post) == 0 {
		return gateOutcome{}
	}

	// Baseline: the same concepts at HEAD. Only paid when the write left
	// something to judge.
	if err := gitx.StashPushAll(k.Root); err != nil {
		fmt.Fprintf(os.Stderr, "cartographer: write_gate skipped (%s): %v\n", toolName, err)
		return gateOutcome{}
	}
	base, _, _ := lint.Filter(writeLintFindings(k, baseIDs, nil), floor)
	if err := gateStashPop(k.Root); err != nil {
		fmt.Fprintf(os.Stderr, "cartographer: write_gate: restoring the write failed (%s): %v\n", toolName, err)
		_ = gitx.StashDrop(k.Root)
		discard(k, toolName)
		res := errorResult("write_gate: could not restore the write, nothing was written")
		return gateOutcome{Refusal: &res}
	}
	pre := map[gateKey]bool{}
	for _, f := range base {
		pre[gateKey{f.Path, f.Check, f.Message}] = true
	}
	oldPath := map[string]string{} // finding path of a renamed concept -> its old path
	for newID, oldID := range renamedFrom {
		oldPath[newID+".md"] = oldID + ".md"
	}

	acceptedChecks := map[string]acceptedFinding{}
	for _, a := range accepts {
		acceptedChecks[a.Check] = a
	}
	var introduced []lint.Finding
	matched := map[string]bool{}
	for _, f := range post {
		p := f.Path
		if op, ok := oldPath[p]; ok {
			p = op
		}
		if pre[gateKey{p, f.Check, f.Message}] {
			continue
		}
		if _, ok := acceptedChecks[f.Check]; ok {
			matched[f.Check] = true
			continue
		}
		introduced = append(introduced, f)
	}
	if len(introduced) > 0 {
		discard(k, toolName)
		body, _ := json.Marshal(findingsOut(introduced))
		msg := fmt.Sprintf("write_gate (%s): refused, nothing was written. This call introduced %d finding(s):\n%s\n"+
			"Fix them in the same call, or retry with accept_findings: [{\"check\": \"<name>\", \"reason\": \"<why>\"}].",
			k.WriteGate, len(introduced), body)
		res := errorResult(msg)
		return gateOutcome{Refusal: &res}
	}
	var trailers []string
	for _, a := range accepts { // call order, one line per matched entry
		if matched[a.Check] {
			trailers = append(trailers, acceptedFindingKey+": "+a.Check+": "+a.Reason)
		}
	}
	return gateOutcome{Trailers: trailers}
}

// discard returns the tree to HEAD; a failure is logged, the refusal still goes
// out (the caller holds the KB lock and checked the tree was clean).
func discard(k *kb.KB, toolName string) {
	if err := gitx.DiscardChanges(k.Root); err != nil {
		fmt.Fprintf(os.Stderr, "cartographer: write_gate: rollback failed (%s): %v\n", toolName, err)
	}
}
