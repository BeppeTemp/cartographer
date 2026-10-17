package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/execbit"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// concept_transfer (D760) moves concepts, with their assets, from one KB to
// another KB mounted on the same server. Nothing else touches two KBs at once,
// so it does not go through gitWrap (one KB, one lock, one commit): it takes
// both git locks itself (through twoKBMove) and commits each KB on its own.
//
// Order of work, inside both locks: validate everything, write the target and
// commit it, then remove from the source and commit it, then push both. A
// failure in the target step discards the target tree and leaves the source
// untouched; a failure in the source step discards the source tree and undoes
// the target commit with a compensating revert commit (never a hard reset: the
// target's history is append-only and may already be fetched by a client).

// errTransferNeedsServer is the answer on a transport that mounts a single KB.
const errTransferNeedsServer = "concept_transfer needs a server mounting both KBs"

// transferMaxMoves bounds one call: each move is validated and copied under two
// KB locks.
const transferMaxMoves = 50

// transferScanMaxBytes bounds the text scanned for placeholders per file.
const transferScanMaxBytes = 1 << 20

// transferSourceHook is a test seam: twoKBMove.run calls it at the start of the source step so
// a test can make it fail and prove the compensating revert.
var transferSourceHook func() error

type routedSetKey struct{}

// withRoutedSet records, on the context of a call dispatched by the routed
// endpoint, the KBs the connection is bound to (D288): the per-KB server the
// call lands on knows only its own KB, and concept_transfer must refuse a
// target the connection cannot reach.
func withRoutedSet(ctx context.Context, set []string) context.Context {
	return context.WithValue(ctx, routedSetKey{}, set)
}

func routedSetFrom(ctx context.Context) []string {
	set, _ := ctx.Value(routedSetKey{}).([]string)
	return set
}

type transferParams struct {
	TargetKB string             `json:"target_kb"`
	Moves    []conceptMoveEntry `json:"moves"`
}

func toolConceptTransfer(src *kb.KB, deps Deps) Tool {
	t := Tool{
		Name: "concept_transfer",
		Description: "Moves concepts with their assets to another KB on this server, in one commit per KB. " +
			"moves: [{source_id, target_id}], validated first; target slots must be free. Bytes copied verbatim. " +
			"Links to moved pages in this KB become plain text. Reports undeclared placeholders and sops warnings.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["target_kb", "moves"],
			"properties": {
				"target_kb": {"type": "string", "description": "Another KB this server mounts"},
				"moves": {
					"type": "array",
					"items": {
						"type": "object",
						"required": ["source_id", "target_id"],
						"properties": {
							"source_id": {"type": "string"},
							"target_id": {"type": "string", "description": "ID in the target KB"}
						}
					}
				}
			}
		}`),
	}
	t.InputSchema = withReasonProperty(t.Name, t.InputSchema)
	t.GitWrapped = true
	t.Handler = func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
		var p transferParams
		if err := json.Unmarshal(args, &p); err != nil {
			return errorResult("invalid params: " + err.Error()), nil
		}
		if deps.KBByName == nil {
			return errorResult(errTransferNeedsServer), nil
		}
		if p.TargetKB == "" {
			return errorResult("'target_kb' is required"), nil
		}
		if len(p.Moves) == 0 || len(p.Moves) > transferMaxMoves {
			return errorResult(fmt.Sprintf("'moves' needs 1 to %d entries", transferMaxMoves)), nil
		}
		dst, ok := deps.KBByName(p.TargetKB)
		if !ok {
			return errorResult(fmt.Sprintf("unknown target_kb %q", p.TargetKB)), nil
		}
		srcName := kbName(src)
		if dst == src || dst.Root == src.Root || p.TargetKB == srcName {
			return errorResult("target_kb is this KB: use concept_move to rename inside one KB"), nil
		}
		if set := routedSetFrom(ctx); set != nil && !containsString(set, p.TargetKB) {
			return errorResult(fmt.Sprintf("kb %q is not bound to this connection (allowed: %s)", p.TargetKB, strings.Join(set, ", "))), nil
		}
		if err := authorizeTransferTarget(ctx, src, dst, p.TargetKB, p.Moves); err != nil {
			return errorResult(err.Error()), nil
		}

		return runTransfer(ctx, args, src, srcName, dst, p), nil
	}
	return t
}

// authorizeTransferTarget is the target half of the policy for concept_transfer:
// write on the target KB and on each destination slot, the destination
// inheriting the source concept's type like allowedMove. The source half (whole
// KB write, since inbound links are rewritten) is authorizeTool's.
func authorizeTransferTarget(ctx context.Context, src, dst *kb.KB, dstName string, moves []conceptMoveEntry) error {
	p := auth.PrincipalFromContext(ctx)
	if p.ID != "" && p.Policy.Admin {
		return nil
	}
	if !p.Policy.HasKBAccess(dstName, false) {
		return deny(denyNoKBAccess)
	}
	if !p.Policy.HasKBAccess(dstName, true) {
		return deny(denyReadOnlyToken)
	}
	for _, m := range moves {
		if m.TargetID == "" {
			return deny(denyBadArguments)
		}
		// Every satellite of an expanded concept lands in dst too, with its own type.
		for _, pair := range movePairs(src, m.SourceID, m.TargetID) {
			_, _, typ := conceptClass(src, pair.from)
			mapName, journalName, _ := conceptClass(dst, pair.to)
			if !p.Policy.Allows(dstName, mapName, journalName, typ, true) {
				return deny(denyOutsideScope)
			}
		}
	}
	return nil
}

// transferFile is one regular file of an expanded concept's tree.
type transferFile struct {
	rel  string // slash path below the concept directory
	size int64
	exec bool
}

// transferItem is one validated move.
type transferItem struct {
	fromID, toID string
	srcLoc       kb.ConceptLocation
	dstLoc       kb.ConceptLocation
	expanded     bool
	fm           *okf.Frontmatter
	body         string
	files        []transferFile    // expanded only: every file below srcLoc.Dir
	mappings     map[string]string // old concept ID → new, the concept and its satellites
}

// transferPlan is everything validation learned, so that the two steps below
// only write.
type transferPlan struct {
	items     []transferItem
	moveMap   map[string]string // every moved concept, old → new
	outMap    moveMapWithAssets // moveMap plus the moved assets' paths, for outbound links
	oldBase   map[string]string // old concept ID → its physical path in the source
	placehold []string
	sops      []string
	external  []string // "<new id> → <source id>": links to pages that stay behind
}

type moveMapWithAssets = map[string]string

// runTransfer is the concept half of the two-KB transaction (twokbmove.go),
// which takes both git locks.
func runTransfer(ctx context.Context, args json.RawMessage, src *kb.KB, srcName string, dst *kb.KB, p transferParams) ToolResult {
	dstName := p.TargetKB
	var (
		plan   *transferPlan
		tgt    transferTargetOut
		srcOut transferSourceOut
	)
	m := &twoKBMove{
		Tool: "concept_transfer", Things: "concepts",
		Src: src, Dst: dst, SrcName: srcName, DstName: dstName, Args: args,
		AuthorizeTarget: func(ctx context.Context) error {
			return authorizeTransferTarget(ctx, src, dst, p.TargetKB, p.Moves)
		},
		Plan: func() *ToolResult {
			var errRes *ToolResult
			plan, errRes = planTransfer(src, dst, p)
			return errRes
		},
		TargetFiles: func() []string { return plan.targetFiles(dst) },
		ApplyTarget: func() error {
			var err error
			tgt, err = applyTransferTarget(src, dst, plan)
			if err == nil {
				err = dst.AppendLog(fmt.Sprintf("concept_transfer (%d concept(s) from %s):\n%s", len(plan.items), srcName, strings.Join(plan.logLines(srcName, dstName, false), "\n")), time.Now())
			}
			if err == nil {
				if _, regenErr := dst.RegenerateIndexes(); regenErr != nil {
					fmt.Fprintf(os.Stderr, "cartographer: generated index (concept_transfer): %v\n", regenErr)
				}
			}
			return err
		},
		ApplySource: func() error {
			var err error
			srcOut, err = applyTransferSource(src, plan)
			if err == nil {
				err = src.AppendLog(fmt.Sprintf("concept_transfer (%d concept(s) to %s):\n%s", len(plan.items), dstName, strings.Join(plan.logLines(srcName, dstName, true), "\n")), time.Now())
			}
			if err == nil {
				if _, regenErr := src.RegenerateIndexes(); regenErr != nil {
					fmt.Fprintf(os.Stderr, "cartographer: generated index (concept_transfer): %v\n", regenErr)
				}
			}
			return err
		},
		TargetSubject: func() string { return transferSubject(plan, "from", srcName) },
		SourceSubject: func() string { return transferSubject(plan, "to", dstName) },
		Report: func(srcSHA, dstSHA string) map[string]any {
			return transferReport(plan, srcName, dstName, srcSHA, dstSHA, srcOut, tgt)
		},
	}
	return m.run(ctx)
}

// transferReport is the result of a concept_transfer that went through.
func transferReport(plan *transferPlan, srcName, dstName, srcSHA, dstSHA string, srcOut transferSourceOut, tgt transferTargetOut) map[string]any {
	moved := make([]map[string]string, 0, len(plan.items))
	for _, it := range plan.items {
		moved = append(moved, map[string]string{"source_id": it.fromID, "target_id": it.toID})
	}
	findings := map[string][]findingOut{}
	if len(srcOut.findings) > 0 {
		findings[srcName] = srcOut.findings
	}
	if len(tgt.findings) > 0 {
		findings[dstName] = tgt.findings
	}
	result := map[string]any{
		"source": map[string]string{"kb": srcName, "commit": srcSHA},
		"target": map[string]string{"kb": dstName, "commit": dstSHA},
		"moved":  moved,
	}
	if len(srcOut.unlinked) > 0 {
		result["unlinked_in"] = srcOut.unlinked
	}
	if len(srcOut.supersededCleared) > 0 {
		result["superseded_by_cleared"] = srcOut.supersededCleared
	}
	if len(srcOut.notes) > 0 {
		result["source_notes"] = srcOut.notes
	}
	if len(tgt.notes) > 0 {
		result["target_notes"] = tgt.notes
	}
	if len(plan.placehold) > 0 {
		result["undeclared_placeholders"] = plan.placehold
	}
	if len(plan.sops) > 0 {
		result["sops_warnings"] = plan.sops
	}
	if len(plan.external) > 0 {
		result["links_to_source"] = plan.external
	}
	if len(findings) > 0 {
		// Not "findings": the client write-findings hook (D353) reads that key as
		// one list, and this is one list per KB.
		result["findings_by_kb"] = findings
	}
	return result
}

// targetFiles is every file the transfer writes into dst: a flat concept's page,
// or every file of an expanded concept's directory (satellites and assets
// included), as slash paths below dst.Root. The curated index entry and the
// generated indexes are not moved files and are not listed.
func (pl *transferPlan) targetFiles(dst *kb.KB) []string {
	var out []string
	add := func(abs string) {
		if rel, err := filepath.Rel(dst.Root, abs); err == nil {
			out = append(out, filepath.ToSlash(rel))
		}
	}
	for _, it := range pl.items {
		if !it.expanded {
			add(it.dstLoc.File)
			continue
		}
		for _, f := range it.files {
			add(filepath.Join(it.dstLoc.Dir, filepath.FromSlash(f.rel)))
		}
	}
	return out
}

// logLines renders the log.md body of one side.
func (pl *transferPlan) logLines(srcName, dstName string, source bool) []string {
	lines := make([]string, 0, len(pl.items))
	for _, it := range pl.items {
		if source {
			lines = append(lines, fmt.Sprintf("- %s → %s:%s", it.fromID, dstName, it.toID))
		} else {
			lines = append(lines, fmt.Sprintf("- %s:%s → %s", srcName, it.fromID, it.toID))
		}
	}
	return lines
}

// transferSubject is the commit subject: the tool, the direction and the IDs.
func transferSubject(pl *transferPlan, dir, other string) string {
	ids := make([]string, 0, len(pl.items))
	for _, it := range pl.items {
		if dir == "to" {
			ids = append(ids, it.fromID)
		} else {
			ids = append(ids, it.toID)
		}
	}
	return fmt.Sprintf("concept_transfer: %s %s %s", strings.Join(ids, ", "), dir, other)
}

// planTransfer validates every move before any write. Nothing here mutates
// either KB.
func planTransfer(src, dst *kb.KB, p transferParams) (*transferPlan, *ToolResult) {
	plan := &transferPlan{moveMap: map[string]string{}, outMap: map[string]string{}, oldBase: map[string]string{}}
	seenFrom, seenTo := map[string]bool{}, map[string]bool{}
	for _, m := range p.Moves {
		if m.SourceID == "" || m.TargetID == "" {
			return nil, errRes(errorResult("'source_id' and 'target_id' are required for every move"))
		}
		if seenFrom[m.SourceID] {
			return nil, errRes(errorResult("duplicate source_id in batch: " + m.SourceID))
		}
		seenFrom[m.SourceID] = true
		if seenTo[m.TargetID] {
			return nil, errRes(errorResult("duplicate target_id in batch: " + m.TargetID))
		}
		seenTo[m.TargetID] = true
		if _, err := okf.PathToID(m.TargetID + ".md"); err != nil {
			return nil, errRes(errorResult("invalid target_id: " + m.TargetID))
		}

		dstLoc, err := dst.LocateConcept(okf.ConceptID(m.TargetID))
		if err != nil {
			if errors.Is(err, okf.ErrInvalidPath) {
				return nil, errRes(errorResult("target_id resolves outside the target KB root: " + m.TargetID))
			}
			return nil, errRes(errorResult(fmt.Sprintf("concept_transfer: resolve target %q: %v", m.TargetID, err)))
		}
		data, err := src.ReadConcept(okf.ConceptID(m.SourceID))
		if err != nil {
			return nil, errRes(errorResult(fmt.Sprintf("concept_transfer: read source %q: %v", m.SourceID, err)))
		}
		srcLoc, err := src.LocateConcept(okf.ConceptID(m.SourceID))
		if err != nil {
			return nil, errRes(errorResult(fmt.Sprintf("concept_transfer: resolve source %q: %v", m.SourceID, err)))
		}
		if msg := targetOccupied(dst, m.TargetID, dstLoc); msg != "" {
			return nil, errRes(errorResult(msg))
		}
		fm, err := okf.ParseFrontmatter(data.FrontmatterRaw)
		if err != nil {
			return nil, errRes(errorResult(fmt.Sprintf("concept_transfer: parse frontmatter %q: %v", m.SourceID, err)))
		}
		it := transferItem{fromID: m.SourceID, toID: m.TargetID, srcLoc: srcLoc, dstLoc: dstLoc, expanded: srcLoc.Expanded, fm: fm, body: data.Body,
			mappings: map[string]string{m.SourceID: m.TargetID}}
		if info, err := os.Lstat(srcLoc.File); err != nil || !info.Mode().IsRegular() {
			return nil, errRes(errorResult(fmt.Sprintf("concept_transfer: %q is not a regular file (symlinks are refused)", m.SourceID)))
		}
		if it.expanded {
			if len(strings.Split(m.SourceID, "/")) != 2 || len(strings.Split(m.TargetID, "/")) != 2 {
				return nil, errRes(errorResult("expanded concept moves require two-segment source_id and target_id"))
			}
			files, ferr := collectTransferFiles(srcLoc.Dir)
			if ferr != nil {
				return nil, errRes(errorResult(fmt.Sprintf("concept_transfer: %q: %v", m.SourceID, ferr)))
			}
			// TRAP: a gitignored file (a decrypted secret, a local .env) would be
			// committed and pushed by the target, whose ignore rules may differ,
			// and deleted from the source beyond git's reach: refuse instead.
			ignored, ierr := gitx.IgnoredFiles(src.Root, srcLoc.Dir)
			if ierr != nil {
				return nil, errRes(errorResult(fmt.Sprintf("concept_transfer: %q: list ignored files: %v", m.SourceID, ierr)))
			}
			if len(ignored) > 0 {
				return nil, errRes(errorResult(fmt.Sprintf("concept_transfer: %q holds files the source KB's .gitignore excludes (%s): move or delete them first", m.SourceID, strings.Join(kb.CapList(ignored, 10), ", "))))
			}
			// The same on the target side (#873): a destination path the target
			// ignores is left out of the target commit, and the source removal
			// would then delete the only committed copy.
			dstPaths := make([]string, 0, len(files))
			for _, f := range files {
				rel, rerr := filepath.Rel(dst.Root, filepath.Join(dstLoc.Dir, filepath.FromSlash(f.rel)))
				if rerr != nil {
					return nil, errRes(errorResult(fmt.Sprintf("concept_transfer: %q: %v", m.TargetID, rerr)))
				}
				dstPaths = append(dstPaths, filepath.ToSlash(rel))
			}
			dstIgnored, ierr := gitx.IgnoredPaths(dst.Root, dstPaths)
			if ierr != nil {
				return nil, errRes(errorResult(fmt.Sprintf("concept_transfer: %q: list files the target ignores: %v", m.TargetID, ierr)))
			}
			if len(dstIgnored) > 0 {
				return nil, errRes(errorResult(fmt.Sprintf("concept_transfer: the target KB's .gitignore excludes %s: move or delete them first, or change the target's ignore rules", strings.Join(kb.CapList(dstIgnored, 10), ", "))))
			}
			it.files = files
			if err := src.WalkConcepts(func(id okf.ConceptID, _ string) error {
				idStr := string(id)
				if strings.HasPrefix(idStr, m.SourceID+"/") {
					it.mappings[idStr] = m.TargetID + strings.TrimPrefix(idStr, m.SourceID)
				}
				return nil
			}); err != nil {
				return nil, errRes(errorResult(fmt.Sprintf("concept_transfer: list expanded source %q: %v", m.SourceID, err)))
			}
			for _, f := range files {
				if !strings.EqualFold(filepath.Ext(f.rel), ".md") {
					plan.outMap[m.SourceID+"/"+f.rel] = m.TargetID + "/" + f.rel
				}
			}
		}
		plan.items = append(plan.items, it)
	}
	// A page moved on its own and also as part of an expanded dossier would be
	// copied twice.
	for i, a := range plan.items {
		for j, b := range plan.items {
			if i != j && (strings.HasPrefix(b.fromID+"/", a.fromID+"/") || strings.HasPrefix(b.toID+"/", a.toID+"/")) {
				return nil, errRes(errorResult("moves cannot overlap: " + a.fromID + " contains or equals " + b.fromID))
			}
		}
	}
	for _, it := range plan.items {
		for old, nw := range it.mappings {
			plan.moveMap[old] = nw
			plan.outMap[old] = nw
			rel, _ := src.ConceptRelPath(okf.ConceptID(old))
			plan.oldBase[old] = rel
		}
	}

	if err := plan.scan(src, dst, p.TargetKB); err != nil {
		return nil, errRes(errorResult(err.Error()))
	}
	return plan, nil
}

// collectTransferFiles lists every regular file below an expanded concept's
// directory, hidden ones included (they move with the directory in
// concept_move, so they travel here). A symlink anywhere, a special file or an
// asset above kb.AssetMaxFileSize refuses the whole transfer.
func collectTransferFiles(dir string) ([]transferFile, error) {
	return collectTransferFilesBounded(dir, 0, 0)
}

// transferBoundError is a directory over the file count or byte bound given to
// collectTransferFilesBounded; the walk stops there.
type transferBoundError struct {
	limit int64
	unit  string // "files" or "bytes"
}

func (e *transferBoundError) Error() string { return fmt.Sprintf("more than %d %s", e.limit, e.unit) }

// collectTransferFilesBounded is collectTransferFiles that stops at maxFiles
// files or maxBytes bytes in total (a bound of 0 or less is no bound), so a
// directory far over the bound is not walked to the end while both KBs are
// locked. The refusal is a *transferBoundError.
func collectTransferFilesBounded(dir string, maxFiles int, maxBytes int64) ([]transferFile, error) {
	var files []transferFile
	var total int64
	err := filepath.WalkDir(dir, func(abs string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if abs == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, abs)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink: symlinks are refused", rel)
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", rel)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !strings.EqualFold(filepath.Ext(rel), ".md") && info.Size() > kb.AssetMaxFileSize {
			return fmt.Errorf("asset %s is %d bytes, over the %d MiB cap", rel, info.Size(), kb.AssetMaxFileSize>>20)
		}
		files = append(files, transferFile{rel: rel, size: info.Size(), exec: execbit.IsExecutable(info.Mode())})
		total += info.Size()
		if maxFiles > 0 && len(files) > maxFiles {
			return &transferBoundError{limit: int64(maxFiles), unit: "files"}
		}
		if maxBytes > 0 && total > maxBytes {
			return &transferBoundError{limit: maxBytes, unit: "bytes"}
		}
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	return files, err
}

// scan reads what is about to travel and fills the reports that never refuse:
// placeholder keys the target's paths.yaml does not declare, sops material,
// secret references and links to pages that stay behind.
func (pl *transferPlan) scan(src, dst *kb.KB, dstName string) error {
	reg, err := dst.ReadPathRegistry()
	if err != nil {
		return fmt.Errorf("concept_transfer: read the target's paths.yaml: %w", err)
	}
	undeclared := map[string]bool{}
	scanText := func(text string) { collectUndeclaredPlaceholders(reg, text, undeclared) }
	external := map[string]bool{}
	for _, it := range pl.items {
		var paths []struct{ abs, label string }
		if it.expanded {
			for _, f := range it.files {
				paths = append(paths, struct{ abs, label string }{filepath.Join(it.srcLoc.Dir, filepath.FromSlash(f.rel)), it.toID + "/" + f.rel})
			}
		} else {
			paths = append(paths, struct{ abs, label string }{it.srcLoc.File, it.toID})
		}
		for _, f := range paths {
			if strings.Contains(strings.ToLower(filepath.Base(f.abs)), ".sops.") {
				pl.sops = append(pl.sops, f.label+": sops-encrypted, copied verbatim and not re-encrypted: decryptable in "+dstName+" only if its age key is a recipient")
			}
			data, err := readTransferHead(f.abs, transferScanMaxBytes)
			if err != nil || !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
				continue
			}
			text := string(data)
			scanText(text)
			if strings.EqualFold(filepath.Ext(f.abs), ".md") {
				if fmRaw, _, ok := okf.SplitFrontmatter(text); ok {
					if fm, perr := okf.ParseFrontmatter(fmRaw); perr == nil {
						for _, key := range []string{"secrets_source", "secret_refs"} {
							if v, has := fm.Get(key); has && !isEmptyValue(v) {
								pl.sops = append(pl.sops, f.label+": "+key+" names the source KB's secrets/, which do not travel: re-point it in "+dstName)
								break
							}
						}
					}
				}
			}
		}
		// Links out of the moved pages to pages that stay in the source.
		for old, nw := range it.mappings {
			data, rerr := src.ReadConcept(okf.ConceptID(old))
			if rerr != nil {
				continue
			}
			for _, target := range kb.ExtractLinks(data.Body, pl.oldBase[old], src.AssetExists) {
				if _, moved := pl.moveMap[string(target)]; moved {
					continue
				}
				if _, terr := src.ReadConcept(target); terr == nil {
					external[nw+" → "+string(target)] = true
				}
			}
		}
	}
	for id := range undeclared {
		pl.placehold = append(pl.placehold, id)
	}
	sort.Strings(pl.placehold)
	for e := range external {
		pl.external = append(pl.external, e)
	}
	sort.Strings(pl.external)
	return nil
}

// collectUndeclaredPlaceholders adds to into the placeholders text cites that the
// target's paths.yaml does not declare. Without a paths.yaml nothing is declared
// and nothing is enforced, so nothing is reported.
func collectUndeclaredPlaceholders(reg kb.PathRegistryState, text string, into map[string]bool) {
	if !reg.Present {
		return
	}
	for _, id := range okf.Placeholders(text) {
		kind, key, ok := okf.SplitPlaceholderID(id)
		if !ok {
			continue
		}
		if _, declared := reg.Registry.Declared(kind, key); !declared {
			into[id] = true
		}
	}
}

func isEmptyValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case []any:
		return len(x) == 0
	}
	return false
}

func readTransferHead(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit))
}

type transferTargetOut struct {
	findings []findingOut
	notes    []string
}

// applyTransferTarget writes the moved concepts into dst. Any error leaves a
// half-written tree for the caller to discard.
func applyTransferTarget(src, dst *kb.KB, pl *transferPlan) (transferTargetOut, error) {
	var out transferTargetOut
	for _, it := range pl.items {
		if err := os.MkdirAll(filepath.Dir(it.dstLoc.Dir), 0o755); err != nil {
			return out, fmt.Errorf("create target parent of %q: %w", it.toID, err)
		}
		if it.expanded {
			// TRAP (copy, never rename): two KBs are two directories that may sit
			// on different filesystems, where os.Rename fails with EXDEV, and a
			// rename would also remove the source before the target is committed.
			// Bytes are copied verbatim, an encrypted file included: nothing is
			// decrypted or re-encrypted on the way.
			for _, f := range it.files {
				from := filepath.Join(it.srcLoc.Dir, filepath.FromSlash(f.rel))
				to := filepath.Join(it.dstLoc.Dir, filepath.FromSlash(f.rel))
				if err := copyTransferFile(from, to, f.exec); err != nil {
					return out, fmt.Errorf("copy %s of %q: %w", f.rel, it.fromID, err)
				}
			}
			continue
		}
		if _, err := dst.WriteConcept(okf.ConceptID(it.toID), it.fm, it.body, ""); err != nil {
			return out, fmt.Errorf("write target %q: %w", it.toID, err)
		}
	}

	// The moved pages' own relative links, rebased from where they were to where
	// they are (D160). A link to a page that stays behind keeps its path and is
	// reported by the source scan and by findings.
	for _, it := range pl.items {
		for old, nw := range it.mappings {
			data, err := dst.ReadConcept(okf.ConceptID(nw))
			if err != nil {
				return out, fmt.Errorf("read moved %q: %w", nw, err)
			}
			newBase, _ := dst.ConceptRelPath(okf.ConceptID(nw))
			newBody, n := kb.RewriteOutboundLinks(data.Body, pl.oldBase[old], newBase, pl.outMap)
			// RewriteOutboundLinks leaves wiki-links alone (they are IDs, not
			// paths): a [[old-id]] between two moved pages is renamed here, as
			// concept_move's backlink walk does inside one KB.
			newBody, w := kb.RewriteLinks(newBody, newBase, pl.moveMap, dst.AssetExists)
			n += w
			if n == 0 {
				continue
			}
			fm, err := okf.ParseFrontmatter(data.FrontmatterRaw)
			if err != nil {
				return out, fmt.Errorf("parse frontmatter %q: %w", nw, err)
			}
			if _, err := dst.WriteConcept(okf.ConceptID(nw), fm, newBody, data.ContentHash); err != nil {
				return out, fmt.Errorf("rewrite the links of %q: %w", nw, err)
			}
		}
	}

	// Curated index of each destination map that asked for one.
	for _, it := range pl.items {
		mapName, ok := conceptMapName(it.toID)
		if !ok {
			continue
		}
		if contract, err := dst.ReadMapContract(mapName); err == nil && contract.Index != kb.IndexGenerated {
			if err := appendCuratedEntry(dst, mapName, okf.ConceptID(it.toID)); err != nil {
				out.notes = append(out.notes, fmt.Sprintf("curated index %s/index.md: %v", mapName, err))
			} else {
				out.notes = append(out.notes, fmt.Sprintf("curated index %s/index.md: added an entry for %s", mapName, it.toID))
			}
		}
	}

	written := make([]string, 0, len(pl.moveMap))
	for _, nw := range pl.moveMap {
		written = append(written, nw)
	}
	sort.Strings(written)
	out.findings, _, _ = repairWritten(dst, written, nil)
	return out, nil
}

// copyTransferFile copies one file without ever overwriting (O_EXCL), syncs it
// and sets the execute bit as the source had it.
func copyTransferFile(from, to string, exec bool) (err error) {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if exec {
		mode = 0o755
	}
	return os.Chmod(to, mode)
}

type transferSourceOut struct {
	unlinked          []rewrittenConcept
	supersededCleared []string
	notes             []string
	findings          []findingOut
}

// applyTransferSource removes the moved concepts from src and turns every link
// that pointed at them into plain text: there is no cross-KB link syntax, so
// the label survives and the hyperlink does not.
func applyTransferSource(src *kb.KB, pl *transferPlan) (transferSourceOut, error) {
	var out transferSourceOut
	// Links from pages that stay to a moved asset cannot become plain text
	// (UnlinkTargets resolves concepts only): report each, before the files go.
	movedAssets := map[string]bool{}
	for old := range pl.outMap {
		if _, isConcept := pl.moveMap[old]; !isConcept {
			movedAssets[old] = true
		}
	}
	if len(movedAssets) > 0 {
		if err := src.WalkConcepts(func(id okf.ConceptID, content string) error {
			if _, moved := pl.moveMap[string(id)]; moved {
				return nil
			}
			basePath, _ := src.ConceptRelPath(id)
			_, body, _ := okf.SplitFrontmatter(content)
			for _, target := range kb.ExtractAssetLinks(body, basePath, src.AssetExists) {
				if movedAssets[target] {
					out.notes = append(out.notes, fmt.Sprintf("%s links to the moved asset %s: that link now dangles, fix it by hand", id, target))
				}
			}
			return nil
		}); err != nil {
			return out, fmt.Errorf("scan links to moved assets: %w", err)
		}
	}
	// A retired entry in a curated index goes with the concept (any non-generated
	// index, like concept_archive: leaving it is a broken link).
	for _, it := range pl.items {
		mapName, ok := conceptMapName(it.fromID)
		if !ok {
			continue
		}
		if contract, err := src.ReadMapContract(mapName); err != nil || contract.Index == kb.IndexGenerated {
			continue
		}
		removed, kept, err := removeCuratedEntry(src, mapName, okf.ConceptID(it.fromID))
		switch {
		case err != nil:
			out.notes = append(out.notes, fmt.Sprintf("curated index %s/index.md: %v", mapName, err))
		case removed:
			out.notes = append(out.notes, fmt.Sprintf("curated index %s/index.md: removed the entry for %s", mapName, it.fromID))
		case kept != "":
			out.notes = append(out.notes, fmt.Sprintf("curated index %s/index.md: a line cites %s next to other concepts, its link became plain text: %q", mapName, it.fromID, kept))
		}
	}

	gone := make(map[string]bool, len(pl.moveMap))
	oldIDs := make([]okf.ConceptID, 0, len(pl.moveMap))
	for old := range pl.moveMap {
		gone[old] = true
		oldIDs = append(oldIDs, okf.ConceptID(old))
	}
	sort.Slice(oldIDs, func(i, j int) bool { return oldIDs[i] < oldIDs[j] })
	var touched []string
	err := src.WalkConceptsLinkingTo(oldIDs, nil, func(id okf.ConceptID, basePath, content string) error {
		if gone[string(id)] {
			return nil // removed below
		}
		fmRaw, body, _ := okf.SplitFrontmatter(content)
		newBody, count := kb.UnlinkTargets(body, basePath, gone, src.AssetExists)
		clearSuperseded := false
		var fm *okf.Frontmatter
		if strings.Contains(fmRaw, "superseded_by") {
			parsed, perr := okf.ParseFrontmatter(fmRaw)
			if perr == nil {
				if v, ok := parsed.Get("superseded_by"); ok {
					if s, isStr := v.(string); isStr && gone[s] {
						clearSuperseded = true
						fm = parsed
					}
				}
			}
		}
		if count == 0 && !clearSuperseded {
			return nil
		}
		if fm == nil {
			parsed, perr := okf.ParseFrontmatter(fmRaw)
			if perr != nil {
				return fmt.Errorf("parse frontmatter %q: %w", id, perr)
			}
			fm = parsed
		}
		if clearSuperseded {
			fm.Delete("superseded_by")
			out.supersededCleared = append(out.supersededCleared, string(id))
		}
		if _, err := src.WriteConcept(id, fm, newBody, okf.ContentHash(content)); err != nil {
			return fmt.Errorf("write %q: %w", id, err)
		}
		out.unlinked = append(out.unlinked, rewrittenConcept{ID: string(id), Replacements: count})
		touched = append(touched, string(id))
		return nil
	})
	if err != nil {
		return out, fmt.Errorf("unlink the inbound links: %w", err)
	}

	// Root and map index.md files are not concepts, so the walk never read them.
	maps, err := src.ListArchives()
	if err != nil {
		return out, err
	}
	for _, name := range append([]string{""}, maps...) {
		content, hash, ierr := src.IndexHash(name)
		if ierr != nil {
			continue
		}
		fmRaw, body, hasFM := okf.SplitFrontmatter(content)
		basePath := "index.md"
		if name != "" {
			basePath = name + "/index.md"
		}
		newBody, n := kb.UnlinkTargets(body, basePath, gone, src.AssetExists)
		if n == 0 {
			continue
		}
		if _, err := src.PatchIndex(name, hash, joinIndex(fmRaw, hasFM, newBody)); err != nil {
			return out, fmt.Errorf("%s: %w", basePath, err)
		}
		out.notes = append(out.notes, "links to the moved pages in "+basePath+" became plain text")
	}

	// Removed only now: the walks above resolve links (an extensionless asset
	// href included, D150) against the tree as it was, as ExtractLinks did.
	for _, it := range pl.items {
		var err error
		if it.expanded {
			err = os.RemoveAll(it.srcLoc.Dir)
		} else {
			err = os.Remove(it.srcLoc.File)
		}
		if err != nil {
			return out, fmt.Errorf("remove %q from the source: %w", it.fromID, err)
		}
	}

	goneList := make([]string, 0, len(gone))
	for old := range gone {
		goneList = append(goneList, old)
	}
	sort.Strings(goneList)
	sort.Strings(touched)
	out.findings, _, _ = repairWritten(src, touched, goneList)
	return out, nil
}
