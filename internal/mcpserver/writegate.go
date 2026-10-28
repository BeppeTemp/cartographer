package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// The write gate (D350), fixed behaviour since D907: no setting turns it off
// or lowers it, and nothing accepts a finding. One enforcement point in
// gitWrap, not per tool: every tool that writes concepts goes through it, and
// the working-tree diff says what changed whatever the tool. After the safe
// fixes a write applies to what it wrote (D349), a write that introduces a
// finding it could have avoided (lint.CheckSpec.AvoidableAtWrite) is rolled
// back to HEAD and refused with each finding and its fix.
// Findings the concepts already had never block a repair, and a check no
// write can avoid (time, a legacy state) is left to the doctor.

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
// lint's deterministic order.
func avoidableFindings(findings []lint.Finding) []lint.Finding {
	var out []lint.Finding
	for _, f := range lint.Order(findings) {
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
//
// A finding on a page the caller cannot see is neither judged nor named: a
// caller scoped to some maps is not blocked by, and learns nothing of, what
// its write does to the rest (the same rule the read tools follow).
func judgeWrite(k *kb.KB, toolName string, view *callerView) *ToolResult {
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
	pairUnmatchedMoves(changes, renamedFrom, func(o, n string) bool { return samePage(k, o, n) })
	post := avoidableFindings(writeLintFindings(k, written, gone))
	if len(post) == 0 {
		return nil
	}

	// Baseline: the same concepts at HEAD. Only paid when the write left
	// something to judge.
	if err := gitx.StashPushAllEnv(k.Root, kb.ServerGitEnv()...); err != nil {
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
		// An expanded concept's findings are reported on its index.md.
		oldPath[newID+"/index.md"] = oldID + "/index.md"
	}

	var introduced []lint.Finding
	for _, f := range post {
		if !view.sees(f) {
			continue
		}
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
			// A fix hint spells the page by file name ("write ../topic.md"):
			// a rename changes that too.
			if nb, ob := path.Base(newID)+".md", path.Base(oldID)+".md"; nb != ob {
				msg = strings.ReplaceAll(msg, nb, ob)
			}
		}
		if !pre[gateKey{p, f.Check, msg}] {
			introduced = append(introduced, f)
		}
	}
	if len(introduced) == 0 {
		return nil
	}
	discard(k, toolName)
	introduced = mergeOrphanAndIndex(introduced)
	for i, f := range introduced {
		introduced[i] = view.redact(f)
	}
	body, _ := json.Marshal(findingsOut(introduced))
	res := errorResult(fmt.Sprintf("write_gate: refused, nothing was written. This call introduced %d finding(s) a write can avoid (D907):\n%s\n"+
		"Fix each one as its message and fix say, then retry; concept_batch is atomic, so a page and the link, index entry or target it needs can land in one call. A new page in a map with a curated index needs index_entry: true on its concept_write, concept_new or creating concept_batch write.",
		len(introduced), body))
	return &res
}

// pairUnmatchedMoves completes renamedFrom for the pages git's similarity
// heuristic reported as a delete plus an add: a small page whose frontmatter or
// links a move rewrote falls under the rename threshold, and its findings would
// then look new under the new id. A deleted and an added concept page that are
// the only ones with that file name, and for which same(deletedPath, addedPath)
// holds (the judge passes: same title and type), are the same page moved. An
// unrelated delete plus create that happens to share a file name (every expanded
// concept is an index.md) is not paired, so it is judged as the new page it is.
func pairUnmatchedMoves(changes []gitx.FileChange, renamedFrom map[string]string, same func(oldPath, newPath string) bool) {
	type page struct{ id, path string }
	deleted, added := map[string][]page{}, map[string][]page{}
	for _, c := range changes {
		id, ok := kb.ConceptIDOfPath(c.Path)
		if !ok {
			continue
		}
		base := path.Base(c.Path)
		switch c.Status {
		case "D":
			deleted[base] = append(deleted[base], page{string(id), c.Path})
		case "A":
			added[base] = append(added[base], page{string(id), c.Path})
		}
	}
	for base, olds := range deleted {
		if news := added[base]; len(olds) == 1 && len(news) == 1 && same(olds[0].path, news[0].path) {
			renamedFrom[news[0].id] = olds[0].id
		}
	}
}

// samePage says whether the page deleted from oldPath (read at HEAD) and the
// one added at newPath (read from the working tree) have the same frontmatter
// title and type: the identity a move keeps.
func samePage(k *kb.KB, oldPath, newPath string) bool {
	oldRaw, err := gitx.ShowFile(k.Root, "HEAD", filepath.ToSlash(oldPath))
	if err != nil {
		return false
	}
	newRaw, err := os.ReadFile(filepath.Join(k.Root, filepath.FromSlash(newPath)))
	if err != nil {
		return false
	}
	ot, oty, ok1 := titleAndType(oldRaw)
	nt, nty, ok2 := titleAndType(string(newRaw))
	return ok1 && ok2 && ot == nt && oty == nty
}

func titleAndType(content string) (title, typ string, ok bool) {
	raw, _, has := okf.SplitFrontmatter(content)
	if !has {
		return "", "", false
	}
	fm, err := okf.ParseFrontmatter(raw)
	if err != nil {
		return "", "", false
	}
	t, _ := fm.Get("title")
	ty, _ := fm.Get("type")
	title, _ = t.(string)
	typ, _ = ty.(string)
	return title, typ, title != ""
}

// discard returns the tree to HEAD; a failure is logged, the refusal still goes
// out (the caller holds the KB lock and checked the tree was clean).
func discard(k *kb.KB, toolName string) {
	if err := gitx.DiscardChanges(k.Root); err != nil {
		fmt.Fprintf(os.Stderr, "cartographer: write_gate: rollback failed (%s): %v\n", toolName, err)
	}
}

// orphanIndexFix is the one fix of a page that is both an orphan and missing
// from its map's curated index (#1273).
const orphanIndexFix = "link the page from another page and pass index_entry: true (or add its entry to the map's index) in the same call"

// mergeOrphanAndIndex folds, for a page the call introduced, the orphan finding
// and the index_incomplete finding naming it into one: the two have one fix,
// and reported apart they read as two problems. The merged finding keeps the
// check "orphan" and the page's path, and names both checks so a caller
// searching for either finds it. A finding without its partner is untouched.
func mergeOrphanAndIndex(in []lint.Finding) []lint.Finding {
	const prefix = "missing curated index entry for "
	missing := map[string]int{} // concept id -> index of its index_incomplete finding
	for i, f := range in {
		if f.Check == "index_incomplete" && strings.HasPrefix(f.Message, prefix) {
			missing[strings.TrimPrefix(f.Message, prefix)] = i
		}
	}
	if len(missing) == 0 {
		return in
	}
	drop := map[int]bool{}
	out := make([]lint.Finding, 0, len(in))
	for i, f := range in {
		if f.Check == "orphan" {
			id := strings.TrimSuffix(f.Path, ".md")
			id = strings.TrimSuffix(id, "/index")
			if j, ok := missing[id]; ok && !drop[j] {
				drop[j] = true
				f.Message = "orphan and index_incomplete: " + f.Message + ", and missing from the map's curated index"
				f.Fix = nil
				f.Message += " — one fix: " + orphanIndexFix
				in[i] = f
			}
		}
	}
	for i, f := range in {
		if !drop[i] {
			out = append(out, f)
		}
	}
	return out
}
