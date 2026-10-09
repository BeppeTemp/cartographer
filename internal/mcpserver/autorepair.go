package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// The background repair (D323): a KB that is never visited by a kb-doctor
// session still drifts, so the server applies the KB's auto_repair checks
// itself, once per doctor_auto_interval. It is a heartbeat, not a doctor
// session: it never writes the kb-doctor log marker (D290), so it does not
// move last_doctor and does not silence the doctor proposal for the
// judgement work it cannot do.

const (
	// autoRepairLogName is the local record of the runs, under .cartographer/
	// (excluded from git): not a concept and not log.md, so it can neither be
	// linted nor mistaken for a doctor session.
	autoRepairLogName = "auto-repair-log.jsonl"
	// autoRepairReason is the Reason trailer of every commit the heartbeat
	// makes; repair_revert and the Atlas recognise the commits by it.
	autoRepairReason = repairCommitReasonPrefix + " (background)"
	// autoRepairLimit bounds the concepts one run rewrites, all checks
	// together (D355): the fixpoint writes a concept once whatever the number
	// of checks that touched it, so the cost is bounded by concepts. A KB
	// imported with thousands of findings is worked down over a few runs
	// instead of in one enormous commit.
	autoRepairLimit = 500
	// autoRepairPullGap is the least time between a run and the next one
	// that a git pull asks for (D355).
	autoRepairPullGap = 10 * time.Minute
	// autoRepairStartupDelay keeps the first run off the boot path.
	autoRepairStartupDelay = time.Minute
)

// autoRepairCheck is what one check did in one run.
type autoRepairCheck struct {
	Check   string `json:"check"`
	Applied int    `json:"applied"`
	Skipped int    `json:"skipped,omitempty"`
	Commit  string `json:"commit,omitempty"`
	Error   string `json:"error,omitempty"`
}

// autoRepairRun is one line of the JSONL log: one run, every check of it.
type autoRepairRun struct {
	At string `json:"at"`
	// Skipped says why the whole run did nothing (a degraded KB).
	Skipped string            `json:"skipped,omitempty"`
	Checks  []autoRepairCheck `json:"checks,omitempty"`
}

func autoRepairLogPath(k *kb.KB) string {
	return filepath.Join(k.Root, ".cartographer", autoRepairLogName)
}

// appendAutoRepairRun appends one run to the log. A failure to write it is
// reported, never fatal: the repair itself already happened.
func appendAutoRepairRun(k *kb.KB, run autoRepairRun) error {
	line, err := json.Marshal(run)
	if err != nil {
		return err
	}
	p := autoRepairLogPath(k)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if fi, err := os.Lstat(p); err == nil && !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", p) // never write through a symlink (D148)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// lastAutoRepairRun returns the newest run in the log, or false.
func lastAutoRepairRun(k *kb.KB) (autoRepairRun, bool) {
	f, err := os.Open(autoRepairLogPath(k))
	if err != nil {
		return autoRepairRun{}, false
	}
	defer f.Close()
	var last autoRepairRun
	found := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var run autoRepairRun
		if json.Unmarshal(sc.Bytes(), &run) == nil && run.At != "" {
			last, found = run, true
		}
	}
	return last, found
}

// autoRepairSplit divides the KB's auto_repair list into the checks the
// background repair and repair-on-write run, and the ones they ignore (D355):
// only a check whose registered fix is safe to run with no person (D354) runs
// unattended; the operator's own kb_repair is unchanged.
func autoRepairSplit(k *kb.KB) (safe, ignored []string) {
	for _, c := range k.AutoRepair {
		if lint.AutoRepairSafe(c) {
			safe = append(safe, c)
		} else {
			ignored = append(ignored, c)
		}
	}
	return safe, ignored
}

// StartAutoRepair starts the heartbeat for the KB this server serves and
// returns at once. It does nothing when the KB has no runnable auto_repair
// checks or doctor_auto_interval is 0. Cancelling ctx stops it: the goroutine
// ends at the next wait, and no run starts after cancellation.
func (s *Server) StartAutoRepair(ctx context.Context) {
	k := s.kbRef
	if k == nil || k.DoctorAutoIntervalDays <= 0 || len(k.AutoRepair) == 0 {
		return
	}
	safe, ignored := autoRepairSplit(k)
	if len(ignored) > 0 {
		fmt.Fprintf(os.Stderr, "cartographer: auto_repair for %s ignores %s: not safe to run unattended (kb_repair still applies them on request)\n", kbName(k), strings.Join(ignored, ", "))
	}
	if len(safe) == 0 {
		return
	}
	interval := time.Duration(k.DoctorAutoIntervalDays) * 24 * time.Hour
	delay := autoRepairStartupDelay
	// A restart must not push the next run a whole interval away, nor run
	// again when one just happened: schedule from the last logged run.
	if last, ok := lastAutoRepairRun(k); ok {
		if at, err := time.Parse(time.RFC3339, last.At); err == nil {
			if wait := at.Add(interval).Sub(s.now()); wait > delay {
				delay = wait
			}
		}
	}
	// Edits that bypass MCP (an editor, a push) arrive through a pull: when it
	// moves HEAD, the repair runs soon instead of at the next tick (D355).
	// SyncIn calls OnSyncIn only after a pull that changed HEAD.
	s.autoRepairTrigger = make(chan struct{}, 1)
	prev := k.OnSyncIn
	k.OnSyncIn = func() {
		if prev != nil {
			prev()
		}
		s.triggerAutoRepair()
	}
	go s.autoRepairLoop(ctx, interval, delay)
}

// triggerAutoRepair signals the heartbeat after a pull that moved HEAD. It
// never blocks: a signal already pending covers this one.
func (s *Server) triggerAutoRepair() {
	if s.autoRepairTrigger == nil {
		return
	}
	select {
	case s.autoRepairTrigger <- struct{}{}:
	default:
	}
}

// autoRepairLoop waits delay, runs, then runs every interval until ctx ends.
// A pull-triggered run goes through the same goroutine, so it is never
// concurrent with a scheduled one, and at most one is pending: it waits until
// autoRepairPullGap after the previous run (any kind).
func (s *Server) autoRepairLoop(ctx context.Context, interval, delay time.Duration) {
	gap := s.autoRepairPullGap
	if gap <= 0 {
		gap = autoRepairPullGap
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	var lastRun time.Time
	var pending <-chan time.Time
	for {
		scheduled := false
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			scheduled = true
		case <-s.autoRepairTrigger:
			if pending == nil {
				wait := gap - time.Since(lastRun)
				if wait < 0 || lastRun.IsZero() {
					wait = 0
				}
				pending = time.After(wait)
			}
			continue
		case <-pending:
			pending = nil
		}
		if ctx.Err() != nil {
			return
		}
		s.runAutoRepairQuota(ctx)
		lastRun = time.Now()
		if scheduled {
			timer.Reset(interval)
		}
	}
}

// stagedRepair is what one run did, per check.
type stagedRepair struct {
	applied map[string]int // concepts (or files) changed, per check
	skipped map[string]int
	errs    []string
	renames []repairTarget // concepts whose renames the map contracts must follow
}

func newStagedRepair() *stagedRepair {
	return &stagedRepair{applied: map[string]int{}, skipped: map[string]int{}}
}

func (r *stagedRepair) total() int {
	n := 0
	for _, v := range r.applied {
		n += v
	}
	return n
}

// fixpoint runs repairConceptFixpoint on one concept and records the outcome.
// seeded names the checks the concept was selected for, charged with the skip
// when the concept is left alone.
func (r *stagedRepair) fixpoint(k *kb.KB, id okf.ConceptID, local []string, seed []lint.Finding, seeded []string) bool {
	out, skip := repairConceptFixpoint(k, id, local, seed)
	if skip != nil {
		for _, c := range seeded {
			r.skipped[c]++
		}
		return false
	}
	for c, n := range out.Changed {
		if n > 0 {
			r.applied[c]++
		}
	}
	for _, c := range out.Stuck {
		r.skipped[c]++
	}
	if len(out.Renames) > 0 {
		r.renames = append(r.renames, repairTarget{ID: id, Path: okf.IDToPath(id), Fixes: out.Renames})
	}
	return out.Hash != ""
}

// stagedAutoRepair is the heartbeat's work (D355), in the order a KB needs:
//
//  1. every concept with a concept-local finding of an allowed check is
//     repaired to a fixpoint, at most quota concepts, one write each;
//  2. the cross-concept checks run one by one through the per-check applier,
//     which keeps the mutual-pair guard and sees the state stage 1 left;
//  3. if stage 2 changed anything, stage 1 runs once more on the concepts it
//     touched: a rebased link can expose a duplicate.
//
// Artifact checks run last, and only where the KB allows artifact writes. The
// caller holds the KB lock.
func stagedAutoRepair(ctx context.Context, k *kb.KB, checks []string, quota int) *stagedRepair {
	r := newStagedRepair()
	var local, cross, artifact []string
	for _, c := range checks {
		s, _ := lint.Spec(c)
		switch {
		case lint.ArtifactRepairCheck(c):
			artifact = append(artifact, c)
		case s.CrossConcept:
			cross = append(cross, c)
		default:
			local = append(local, c)
		}
	}
	isLocal := map[string]bool{}
	for _, c := range local {
		isLocal[c] = true
	}

	// seedsFor groups the fixable findings of the local checks by concept.
	seedsFor := func() (map[string][]lint.Finding, []string, error) {
		findings, err := lint.Run(k, "", false)
		if err != nil {
			return nil, nil, err
		}
		by := map[string][]lint.Finding{}
		for _, f := range findings {
			if f.Fix == nil || f.Artifact || !isLocal[f.Check] {
				continue
			}
			if id := uiFindingConcept(f.Path); id != "" {
				by[id] = append(by[id], f)
			}
		}
		ids := make([]string, 0, len(by))
		for id := range by {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return by, ids, nil
	}
	checksOf := func(fs []lint.Finding) []string {
		seen := map[string]bool{}
		var out []string
		for _, f := range fs {
			if !seen[f.Check] {
				seen[f.Check] = true
				out = append(out, f.Check)
			}
		}
		return out
	}

	if len(local) > 0 {
		by, ids, err := seedsFor()
		if err != nil {
			r.errs = append(r.errs, "lint: "+err.Error())
			return r
		}
		if len(ids) > quota {
			ids = ids[:quota]
		}
		for _, id := range ids {
			if ctx.Err() != nil {
				return r
			}
			r.fixpoint(k, okf.ConceptID(id), local, by[id], checksOf(by[id]))
		}
	}

	touched := map[string]bool{}
	for _, check := range cross {
		if ctx.Err() != nil {
			return r
		}
		targets, _, err := planRepair(k, check, "")
		if err != nil {
			r.errs = append(r.errs, check+": "+err.Error())
			continue
		}
		if len(targets) > quota {
			targets = targets[:quota]
		}
		if len(targets) == 0 {
			continue
		}
		applied, skipped := applyRepair(k, targets, check == "reciprocal_link_item")
		r.applied[check] += len(applied)
		r.skipped[check] += len(skipped)
		for _, t := range applied {
			touched[string(t.ID)] = true
		}
	}
	if len(touched) > 0 && len(local) > 0 && ctx.Err() == nil {
		by, _, err := seedsFor()
		if err != nil {
			r.errs = append(r.errs, "lint: "+err.Error())
		} else {
			ids := make([]string, 0, len(touched))
			for id := range touched {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				if len(by[id]) == 0 {
					continue
				}
				r.fixpoint(k, okf.ConceptID(id), local, by[id], checksOf(by[id]))
			}
		}
	}

	if k.AllowArtifactWrite {
		for _, check := range artifact {
			if ctx.Err() != nil {
				return r
			}
			targets, _, err := planArtifactRepair(k, check, "")
			if err != nil {
				r.errs = append(r.errs, check+": "+err.Error())
				continue
			}
			if len(targets) > quota {
				targets = targets[:quota]
			}
			if len(targets) == 0 {
				continue
			}
			applied, skipped := applyArtifactRepair(k, targets)
			r.applied[check] += len(applied)
			r.skipped[check] += len(skipped)
		}
	}
	return r
}

// runAutoRepairQuota runs one background repair (D355) and logs it. It goes
// through gitWrap, so the KB lock, the commit (one per run, Reason
// "auto-repair (background)", one body line per check), the git sync and the
// stale-write guard are the ones every write has, not a second copy.
func (s *Server) runAutoRepairQuota(ctx context.Context) autoRepairRun {
	k := s.kbRef
	run := autoRepairRun{At: s.now().UTC().Format(time.RFC3339)}
	if k == nil {
		return run
	}
	// A KB with a conflict to resolve is a KB a person must look at first:
	// writing on top of it would add to the mess.
	if conflicts, err := k.ListConflicts(); err == nil && len(conflicts) > 0 {
		run.Skipped = fmt.Sprintf("%d open git conflict(s)", len(conflicts))
	} else if k.GitSync && k.GitStatusSnapshot().State == "degraded" {
		run.Skipped = "git sync is degraded"
	}
	if run.Skipped != "" {
		fmt.Fprintf(os.Stderr, "cartographer: auto-repair skipped for %s: %s\n", kbName(k), run.Skipped)
		_ = appendAutoRepairRun(k, run)
		return run
	}
	checks, _ := autoRepairSplit(k)
	if len(checks) == 0 {
		return run
	}
	var stats *stagedRepair
	// Named kb_repair so the commit, audit and authorization treat it as the
	// repair it is; it is not registered, so tools/list does not change.
	tool := gitWrap(k, Tool{
		Name: "kb_repair",
		Handler: func(requestContext, json.RawMessage) (ToolResult, error) {
			stats = stagedAutoRepair(ctx, k, checks, autoRepairLimit)
			res := ToolResult{Content: textResult("{}").Content}
			if len(stats.renames) > 0 {
				if _, err := renameInContracts(k, stats.renames); err != nil {
					stats.errs = append(stats.errs, "map contracts: "+err.Error())
				}
			}
			if stats.total() > 0 {
				var lines []string
				for _, c := range checks {
					if n := stats.applied[c]; n > 0 {
						lines = append(lines, fmt.Sprintf("%s: %d", c, n))
					}
				}
				body := strings.Join(lines, "\n")
				_ = k.AppendLog("auto-repair (background)\n\n"+body, time.Now())
				res.CommitSubject = autoRepairReason + "\n\n" + body
			}
			return res, nil
		},
	})
	rctx := auth.ContextWithPrincipal(ctx, auth.LocalAdminPrincipal())
	args, _ := json.Marshal(map[string]any{"reason": autoRepairReason})
	res, err := tool.Handler(rctx, args)
	failure := ""
	switch {
	case err != nil:
		failure = err.Error()
	case res.IsError:
		failure = firstText(res)
	}
	for _, check := range checks {
		entry := autoRepairCheck{Check: check, Error: failure}
		if stats != nil {
			entry.Applied, entry.Skipped = stats.applied[check], stats.skipped[check]
		}
		if entry.Applied > 0 {
			entry.Commit = res.CommitSHA
		}
		run.Checks = append(run.Checks, entry)
		if entry.Applied > 0 {
			fmt.Fprintf(os.Stderr, "cartographer: auto-repair %s on %s: %d concept(s) repaired (%s)\n", check, kbName(k), entry.Applied, shortSHA(entry.Commit))
		}
	}
	if failure != "" {
		fmt.Fprintf(os.Stderr, "cartographer: auto-repair on %s: %s\n", kbName(k), failure)
	}
	if stats != nil {
		for _, e := range stats.errs {
			fmt.Fprintf(os.Stderr, "cartographer: auto-repair on %s: %s\n", kbName(k), e)
		}
	}
	if err := appendAutoRepairRun(k, run); err != nil {
		fmt.Fprintf(os.Stderr, "cartographer: auto-repair log for %s: %v\n", kbName(k), err)
	}
	return run
}

func firstText(res ToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}
	return strings.TrimSpace(res.Content[0].Text)
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
