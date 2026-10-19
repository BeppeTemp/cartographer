package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// twoKBMove is the transaction shared by the tools that move something from
// one KB to another on the same server: concept_transfer (D760) and
// artifact_move (D782). It owns everything that is about the two KBs and
// nothing about what travels, so the safety rules learned in review live in one
// copy: validate first, write and commit the target, then remove from and commit
// the source, then push both; a source failure undoes the target with a revert
// commit, never a reset; and when a commit's outcome is unknown nothing is
// discarded or reverted.
//
// The tool handler calls run, which takes both KBs' git locks itself; what
// travels is the callbacks' business: they close over their own plan.
type twoKBMove struct {
	Tool   string // the tool name, in every message and commit subject
	Things string // what moves, in the plural, for the messages ("concepts")

	Src, Dst         *kb.KB
	SrcName, DstName string
	Args             json.RawMessage

	// AuthorizeTarget is the target half of the policy. It runs once more under
	// the locks, like reauthorizeUnderLock does for the source half.
	AuthorizeTarget func(ctx context.Context) error
	// Plan validates everything and writes nothing; a non-nil result is the
	// refusal.
	Plan func() *ToolResult
	// TargetFiles lists every regular file the move writes into Dst, as a slash
	// path below Dst.Root: the moved files only, not the log line nor the
	// generated files. It is read after Plan, and run holds the target commit
	// to it: git may leave a file out of `git add -A` (an ignore rule, a nested
	// repository) and the source removal would then destroy the only copy.
	TargetFiles func() []string
	// ApplyTarget writes the moved things into Dst, its log line and its
	// generated files; ApplySource removes them from Src likewise. Any error
	// leaves a half-written tree for run to discard.
	ApplyTarget func() error
	ApplySource func() error
	// TargetSubject and SourceSubject are the commit subjects, read after Plan.
	TargetSubject func() string
	SourceSubject func() string
	// Report is the tool's result, once both commits are in.
	Report func(srcSHA, dstSHA string) map[string]any
}

func (m *twoKBMove) label(k *kb.KB) string {
	if k == m.Src {
		return m.SrcName
	}
	return m.DstName
}

// run takes both KBs' git locks and runs the transaction under them.
//
// TRAP (lock order): the locks are taken in one global order, the KB root path,
// whichever side is the source. k.mu is not reentrant and a move A→B can run
// beside a move B→A; two callers taking the locks in opposite orders would
// deadlock, one global order cannot. The order lives here, once, so a handler
// cannot take the locks in a different one.
func (m *twoKBMove) run(ctx context.Context) ToolResult {
	first, second := m.Src, m.Dst
	if second.Root < first.Root {
		first, second = second, first
	}
	var res ToolResult
	_ = first.WithGitLock(func() error {
		return second.WithGitLock(func() error {
			res = m.runLocked(ctx)
			return nil
		})
	})
	return res
}

// runLocked is the body of the tool under both git locks.
func (m *twoKBMove) runLocked(ctx context.Context) ToolResult {
	src, dst, srcName, dstName, tool := m.Src, m.Dst, m.SrcName, m.DstName, m.Tool
	for _, k := range []*kb.KB{src, dst} {
		if !gitx.IsRepo(k.Root) {
			return errorResult(tool + " needs both KBs to be git repositories: it undoes a half-done transfer through git")
		}
	}
	for _, k := range []*kb.KB{src, dst} {
		if refused, bad := syncInForWrite(k); bad {
			return refused
		}
	}
	if err := reauthorizeUnderLock(ctx, src, tool, m.Args); err != nil {
		return errorResult(err.Error())
	}
	if err := m.AuthorizeTarget(ctx); err != nil {
		return errorResult(err.Error())
	}
	for _, k := range []*kb.KB{src, dst} {
		commitExternalChanges(k, tool)
		k.RefreshSettings() // as gitWrap does: an in-place settings.yaml edit applies (D746)
		// DiscardChanges below resets the whole tree: it is safe only when the
		// tree held nothing of anyone's before this call.
		if status, err := gitx.Status(k.Root); err != nil || strings.TrimSpace(status) != "" {
			return errorResult(tool + ": " + m.label(k) + " has uncommitted changes that could not be committed (open conflicts?); resolve them first")
		}
	}

	if errRes := m.Plan(); errRes != nil {
		return *errRes
	}
	subject := m.SourceSubject()
	planned := m.TargetFiles()
	newDirs := missingParentDirs(dst.Root, planned)

	// --- target step ---
	err := m.ApplyTarget()
	var dstSHA string
	if err == nil {
		dstSHA, err = commitTransfer(ctx, m.Args, dst, m.TargetSubject(), "Moved-From", srcName)
	}
	if errors.Is(err, errCommitUnknown) {
		return errorResult(fmt.Sprintf("%s: the commit in %s may have landed but could not be confirmed; nothing was changed in the source. Check %s's history before retrying", tool, dstName, dstName))
	}
	if err != nil {
		discardNote := ""
		if dErr := gitx.DiscardChanges(dst.Root); dErr != nil {
			discardNote = fmt.Sprintf("; discarding the target tree failed too: %v", dErr)
		} else if cErr := removeUntrackedPlanned(dst, planned); cErr != nil {
			discardNote = fmt.Sprintf("; removing the copied files the discard keeps (ignored ones) failed too: %v", cErr)
		} else {
			pruneDirs(dst.Root, newDirs)
		}
		return errorResult(fmt.Sprintf("%s: target step failed, nothing was changed in the source: %v%s", tool, err, discardNote))
	}
	if missing, vErr := missingFromCommit(dst.Root, dstSHA, planned); vErr != nil || len(missing) > 0 {
		return m.undoIncompleteTarget(ctx, dstSHA, planned, newDirs, missing, vErr)
	}

	// --- source step ---
	err = nil
	if transferSourceHook != nil {
		err = transferSourceHook()
	}
	if err == nil {
		err = m.ApplySource()
	}
	var srcSHA string
	if err == nil {
		srcSHA, err = commitTransfer(ctx, m.Args, src, subject, "Moved-To", dstName)
	}
	if errors.Is(err, errCommitUnknown) {
		// The removal may be committed: reverting the target could leave the
		// things in neither KB. A duplicate is recoverable, a loss is not.
		return errorResult(fmt.Sprintf("%s: the source commit in %s may have landed but could not be confirmed; the target commit %.7s in %s is kept. Check %s's history: if the removal is not there, remove the duplicates from %s by hand", tool, srcName, dstSHA, dstName, srcName, dstName))
	}
	if err != nil {
		if dErr := gitx.DiscardChanges(src.Root); dErr != nil {
			// The source tree may still hold the uncommitted removal: the next
			// write would commit it as an external change, so the target copy
			// must stay (a duplicate, never a loss).
			return errorResult(fmt.Sprintf("%s: source step failed (%v) and discarding the source tree failed too (%v); the target commit %.7s in %s is kept so nothing is lost: restore %s with git, then remove the duplicates from %s", tool, err, dErr, dstSHA, dstName, srcName, dstName))
		}
		note := ""
		if rErr := revertTransferTarget(ctx, tool, dst, dstSHA, srcName, "the source step failed"); rErr != nil {
			note += fmt.Sprintf("; undoing the target commit %.7s failed (%v): the %s now exist in both KBs, remove them from %s by hand", dstSHA, rErr, m.Things, dstName)
		} else {
			note += fmt.Sprintf("; the target commit %.7s was undone by a revert commit", dstSHA)
		}
		return errorResult(fmt.Sprintf("%s: source step failed, the source is unchanged: %v%s", tool, err, note))
	}

	// Target first, so an inline push normally lands the content before its
	// removal. Not guaranteed: a failed target push does not stop the source
	// push, and with SyncOutDebounce each KB's worker pushes on its own. Nothing
	// is lost locally; the gap closes when the target is pushed (D760).
	res := ToolResult{}
	pushAfterCommit(dst, tool, &res)
	pushAfterCommit(src, tool, &res)

	out, _ := json.MarshalIndent(m.Report(srcSHA, dstSHA), "", "  ")
	res.Content = append([]ContentBlock{{Type: "text", Text: string(out)}}, res.Content...)
	res.CommitSHA = srcSHA
	appendAuthorWarning(ctx, &res)
	return res
}

// errCommitUnknown means the commit may have landed but its SHA could not be
// read back: neither discarding nor reverting is safe then, since either could
// undo a commit that holds the only copy of a concept.
var errCommitUnknown = errors.New("commit: its outcome is unknown (HEAD unreadable)")

// commitTransfer commits one KB's side with its Moved-* trailer. TRAP: the KB
// name is whitespace-collapsed like commitAgent, otherwise a name holding
// "\nReason: x" would forge a trailer. TRAP: CommitOpAs returns "" both for
// "nothing to commit" and for a commit whose SHA could not be read back, so
// success is decided by HEAD moving, never by the returned SHA alone.
func commitTransfer(ctx context.Context, args json.RawMessage, k *kb.KB, subject, trailerKey, otherKB string) (string, error) {
	other := strings.Join(strings.Fields(otherKB), " ")
	msg := stripTokenTrailers(subject)
	if block := commitTrailerBlock(ctx, args, trailerKey+": "+other); block != "" {
		msg += "\n\n" + block
	}
	before, _ := gitx.HeadSHA(k.Root)
	authorName, authorEmail := writerAuthor(ctx)
	sha, err := k.CommitOpAs(msg, authorName, authorEmail)
	if err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	if sha != "" {
		return sha, nil
	}
	after, headErr := gitx.HeadSHA(k.Root)
	switch {
	case headErr != nil:
		return "", errCommitUnknown
	case after != before:
		return after, nil
	}
	return "", errors.New("commit: nothing to commit")
}

// revertTransferTarget undoes the target commit with a new commit that
// inverts it. It never resets: the target's history is append-only, a client may
// already have fetched the commit, and a hard reset would rewrite it.
func revertTransferTarget(ctx context.Context, tool string, dst *kb.KB, sha, srcName, cause string) error {
	if err := gitx.RevertNoCommit(dst.Root, sha, dst.GitEnv...); err != nil {
		_ = gitx.DiscardChanges(dst.Root)
		return err
	}
	msg := fmt.Sprintf("%s: revert %.7s (%s)", tool, sha, cause)
	// nil args: the caller's Reason belongs to the transfer, the revert states its own.
	msg += "\n\n" + commitTrailerBlock(ctx, nil, commitReasonKey+": the transfer from "+strings.Join(strings.Fields(srcName), " ")+" failed after this KB was written")
	authorName, authorEmail := writerAuthor(ctx)
	if _, err := dst.CommitOpAs(msg, authorName, authorEmail); err != nil {
		_ = gitx.DiscardChanges(dst.Root)
		return err
	}
	return nil
}
